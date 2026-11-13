use crate::{
    ipc::{self, Command, Endpoint, Reply, Request, SCHEMA},
    BUILD_VERSION,
};
use anyhow::{bail, Result};
use fs2::FileExt;
use netsgo_client_core::{ClientSupervisor, SupervisorNotification};
use serde_json::Value;
use std::{
    collections::VecDeque,
    fs::{self, OpenOptions},
    net::{Ipv4Addr, TcpListener, TcpStream},
    path::Path,
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        mpsc, Arc, Mutex,
    },
    thread,
    time::{Duration, Instant},
};
use subtle::ConstantTimeEq;

pub fn run(data: &Path, stop: Arc<AtomicBool>) -> Result<()> {
    fs::create_dir_all(data)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(data, fs::Permissions::from_mode(0o700))?;
    }
    let lock = OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(data.join("agent.lock"))?;
    lock.try_lock_exclusive()
        .map_err(|_| anyhow::anyhow!("另一个后台服务已在管理这些连接"))?;
    let (supervisor, notifications) = ClientSupervisor::new(data.to_owned())?;
    let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, 0))?;
    listener.set_nonblocking(true)?;
    let mut secret = [0u8; 32];
    getrandom::fill(&mut secret).map_err(|e| anyhow::anyhow!("生成本机认证密钥失败：{e}"))?;
    let endpoint = Endpoint {
        port: listener.local_addr()?.port(),
        token: secret.iter().map(|n| format!("{n:02x}")).collect(),
        pid: std::process::id(),
    };
    let path = ipc::endpoint_path(data);
    let tmp = tempfile::NamedTempFile::new_in(data)?;
    fs::write(tmp.path(), serde_json::to_vec(&endpoint)?)?;
    tmp.persist(&path).map_err(|e| e.error)?;
    let errors = Arc::new(Mutex::new(VecDeque::new()));
    let active = Arc::new(AtomicUsize::new(0));
    match fs::read(data.join(crate::manage::RESUME_FILE)) {
        Ok(bytes) => {
            let ids: Vec<String> = serde_json::from_slice(&bytes)?;
            fs::remove_file(data.join(crate::manage::RESUME_FILE))?;
            for id in ids {
                let _ = supervisor.reconnect_profile(&id);
            }
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            supervisor.reconnect_all_saved()
        }
        Err(error) => return Err(error.into()),
    }
    let mut snapshot_at = Instant::now();
    let result = (|| -> Result<()> {
        while !stop.load(Ordering::SeqCst) {
            drain(&notifications, &errors);
            if snapshot_at.elapsed() >= Duration::from_secs(3) {
                supervisor.request_all_snapshots();
                snapshot_at = Instant::now();
            }
            match listener.accept() {
                Ok((socket, _)) => {
                    if active.fetch_add(1, Ordering::SeqCst) >= 8 {
                        active.fetch_sub(1, Ordering::SeqCst);
                        continue;
                    }
                    let (supervisor, errors, active, stop, token) = (
                        supervisor.clone(),
                        errors.clone(),
                        active.clone(),
                        stop.clone(),
                        endpoint.token.clone(),
                    );
                    thread::spawn(move || {
                        let _ = handle(socket, &token, &supervisor, &errors, &stop);
                        active.fetch_sub(1, Ordering::SeqCst);
                    });
                }
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    thread::sleep(Duration::from_millis(80))
                }
                Err(error) => return Err(error.into()),
            }
        }
        Ok(())
    })();
    stop.store(true, Ordering::SeqCst);
    let _ = supervisor.disconnect_all();
    let _ = fs::remove_file(&path);
    result
}

fn drain(
    rx: &mpsc::Receiver<SupervisorNotification>,
    errors: &Mutex<VecDeque<SupervisorNotification>>,
) {
    for item in rx.try_iter() {
        if matches!(item, SupervisorNotification::SessionError { .. }) {
            let mut queue = errors.lock().unwrap();
            if queue.len() == 32 {
                queue.pop_front();
            }
            queue.push_back(item);
        }
    }
}

fn handle(
    mut socket: TcpStream,
    token: &str,
    supervisor: &ClientSupervisor,
    errors: &Mutex<VecDeque<SupervisorNotification>>,
    stop: &AtomicBool,
) -> Result<()> {
    socket.set_read_timeout(Some(Duration::from_secs(2)))?;
    socket.set_write_timeout(Some(Duration::from_secs(2)))?;
    let request: Request = ipc::read_frame_limited(&mut socket, 1024 * 1024)?;
    if request.schema != SCHEMA || !bool::from(request.token.as_bytes().ct_eq(token.as_bytes())) {
        bail!("后台服务认证失败");
    }
    if stop.load(Ordering::SeqCst) {
        bail!("后台服务正在停止");
    }
    let result: Result<Value> = (|| {
        let value = match request.command {
            Command::State => Value::Null,
            Command::Appearance(pref) => {
                supervisor.set_appearance(pref)?;
                Value::Null
            }
            Command::Select(id) => {
                supervisor.select_profile(&id)?;
                Value::Null
            }
            Command::Rename { id, name } => {
                supervisor.rename_profile(&id, &name)?;
                Value::Null
            }
            Command::ClearLogs(id) => {
                supervisor.clear_logs(&id);
                Value::Null
            }
            Command::Connect(request) => Value::String(supervisor.connect(request)?),
            Command::Edit(request) => Value::String(supervisor.save_or_reconnect_edited_profile(
                &request.id,
                &request.server,
                &request.name,
                request.key.as_deref(),
                request.tls_skip_verify,
                &request.tls_fingerprint,
            )?),
            Command::Forget(id) => {
                supervisor.forget_profile(&id)?;
                Value::Null
            }
            Command::Reconnect(id) => Value::String(supervisor.reconnect_profile(&id)?),
            Command::ReconnectAll => {
                supervisor.reconnect_all_saved();
                Value::Null
            }
            Command::Snapshot(id) => {
                if let Some(id) = id {
                    supervisor.request_snapshot(&id);
                } else {
                    supervisor.request_all_snapshots();
                }
                Value::Null
            }
            Command::Disconnect(id) => {
                if let Some(id) = id {
                    supervisor.disconnect_profile(&id)?;
                } else {
                    supervisor.disconnect_all()?;
                }
                Value::Null
            }
        };
        Ok(value)
    })();
    let reply = Reply {
        schema: SCHEMA,
        version: BUILD_VERSION.into(),
        state: Some(supervisor.view_state()),
        notifications: errors.lock().unwrap().drain(..).collect(),
        value: result.as_ref().cloned().unwrap_or(Value::Null),
        error: result.err().map(|e| format!("{e:#}")),
    };
    ipc::write_frame(&mut socket, &reply)
}
