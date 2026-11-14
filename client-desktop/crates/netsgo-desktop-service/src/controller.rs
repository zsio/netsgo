use crate::{
    ipc::{self, Command, EditProfile, Reply},
    manage,
    platform::{self, StartupView},
};
use anyhow::{bail, Result};
use netsgo_client_core::{
    AppearancePreference, ClientSupervisor, ConnectionPhase, StartRequest, SupervisorNotification,
    SupervisorViewState,
};
use serde_json::Value;
use std::{
    path::{Path, PathBuf},
    sync::{mpsc, Arc, Mutex},
    thread,
    time::{Duration, Instant},
};

#[derive(Clone)]
enum Backend {
    Local(ClientSupervisor),
    Service(Remote),
}
struct State {
    backend: Backend,
    busy: bool,
}
#[derive(Clone)]
pub struct DesktopSupervisor {
    root: PathBuf,
    state: Arc<Mutex<State>>,
    notify: mpsc::Sender<SupervisorNotification>,
}

struct RemoteState {
    view: SupervisorViewState,
    version: Option<String>,
    error: Option<String>,
}
#[derive(Clone)]
struct Remote {
    data: PathBuf,
    state: Arc<Mutex<RemoteState>>,
    notify: mpsc::Sender<SupervisorNotification>,
}

impl Remote {
    fn new(
        data: PathBuf,
        view: SupervisorViewState,
        notify: mpsc::Sender<SupervisorNotification>,
    ) -> Self {
        let remote = Self {
            data: data.clone(),
            state: Arc::new(Mutex::new(RemoteState {
                view,
                version: None,
                error: Some("正在连接后台服务…".into()),
            })),
            notify: notify.clone(),
        };
        let weak = Arc::downgrade(&remote.state);
        thread::spawn(move || loop {
            let Some(state) = weak.upgrade() else {
                return;
            };
            let result = ipc::call(&data, Command::State);
            {
                let mut state = state.lock().unwrap();
                match result {
                    Ok(reply) => apply_reply(&mut state, reply, &notify),
                    Err(error) => {
                        state.error = Some(format!("{error:#}"));
                        mark_unavailable(&mut state.view);
                    }
                }
            }
            let _ = notify.send(SupervisorNotification::Updated);
            drop(state);
            thread::sleep(Duration::from_secs(1));
        });
        remote
    }
    fn call(&self, command: Command) -> Result<Value> {
        let reply = ipc::call(&self.data, command)?;
        let value = reply.value.clone();
        apply_reply(&mut self.state.lock().unwrap(), reply, &self.notify);
        let _ = self.notify.send(SupervisorNotification::Updated);
        Ok(value)
    }
}

fn apply_reply(
    state: &mut RemoteState,
    reply: Reply,
    notify: &mpsc::Sender<SupervisorNotification>,
) {
    if let Some(view) = reply.state {
        state.view = view;
    }
    state.version = Some(reply.version);
    state.error = None;
    for notification in reply.notifications {
        let _ = notify.send(notification);
    }
}

fn mark_unavailable(view: &mut SupervisorViewState) {
    view.connected_count = 0;
    view.running_count = 0;
    view.total_active_streams = 0;
    for session in &mut view.sessions {
        session.phase = ConnectionPhase::Error;
        session.status_text = "后台服务状态不可用".into();
        session.last_error = Some("无法读取后台服务状态，请在启动设置中检查或停用服务".into());
        session.snapshot.connected = false;
        session.snapshot.active_connections = 0;
        session.pid = None;
    }
}

impl DesktopSupervisor {
    pub fn new(root: PathBuf) -> Result<(Self, mpsc::Receiver<SupervisorNotification>)> {
        let (notify, rx) = mpsc::channel();
        let (local, local_rx) = ClientSupervisor::new(root.clone())?;
        let backend = if platform::owned_manifest(&root)?.is_some_and(|manifest| manifest.enabled) {
            // Also binds beta.4 services whose earlier installer did not record the app path.
            if let Ok(installation) = crate::presence::Installation::current() {
                crate::presence::bind(&platform::data_root()?, &installation)?;
            }
            Backend::Service(Remote::new(
                platform::data_root()?,
                local.view_state(),
                notify.clone(),
            ))
        } else {
            bridge(local_rx, notify.clone());
            Backend::Local(local)
        };
        Ok((
            Self {
                root,
                state: Arc::new(Mutex::new(State {
                    backend,
                    busy: false,
                })),
                notify,
            },
            rx,
        ))
    }
    fn backend(&self) -> Result<Backend> {
        let state = self.state.lock().unwrap();
        if state.busy {
            bail!("正在切换后台运行方式，请稍候");
        }
        Ok(state.backend.clone())
    }
    pub fn is_service_managed(&self) -> bool {
        matches!(self.state.lock().unwrap().backend, Backend::Service(_))
    }
    pub fn view_state(&self) -> SupervisorViewState {
        match &self.state.lock().unwrap().backend {
            Backend::Local(local) => local.view_state(),
            Backend::Service(remote) => remote.state.lock().unwrap().view.clone(),
        }
    }
    pub fn startup_view(&self) -> StartupView {
        let state = self.state.lock().unwrap();
        let (version, error) = match &state.backend {
            Backend::Local(_) => (None, None),
            Backend::Service(remote) => {
                let remote = remote.state.lock().unwrap();
                (
                    remote.version.clone(),
                    remote.error.clone().or_else(|| {
                        platform::data_root()
                            .ok()
                            .and_then(|data| crate::presence::pause_reason(&data))
                    }),
                )
            }
        };
        StartupView {
            login_enabled: platform::login_enabled().unwrap_or(false),
            boot_enabled: matches!(state.backend, Backend::Service(_)),
            busy: state.busy,
            service_version: version,
            service_error: error,
        }
    }
    pub fn set_login_enabled(&self, enabled: bool) -> Result<()> {
        platform::set_login_enabled(enabled)
    }
    pub fn launch_uninstaller(&self) -> Result<()> {
        let installation = crate::presence::Installation::current()?;
        crate::uninstall::launch(&self.root, Some(&installation.app_root), false)
    }
    pub fn shutdown_ui(&self) -> Result<()> {
        if let Backend::Local(local) = self.state.lock().unwrap().backend.clone() {
            local.disconnect_all()?;
        }
        Ok(())
    }
    pub fn set_boot_enabled(&self, enabled: bool) -> Result<()> {
        let old = {
            let mut state = self.state.lock().unwrap();
            if state.busy {
                bail!("正在切换后台运行方式");
            }
            if matches!(state.backend, Backend::Service(_)) == enabled {
                return Ok(());
            }
            state.busy = true;
            state.backend.clone()
        };
        let running = self.running_profile_ids();
        let result = (|| -> Result<Backend> {
            if enabled {
                if let Backend::Local(local) = &old {
                    if local
                        .view_state()
                        .sessions
                        .iter()
                        .any(|s| s.phase.is_running() && !s.has_saved_token)
                    {
                        bail!("请先完成首次连接认证，再启用免登录运行");
                    }
                    local.disconnect_all()?;
                }
                manage::request(
                    manage::Operation::Install,
                    &self.root,
                    None,
                    running.clone(),
                )?;
                let data = platform::data_root()?;
                let reply = wait_for_service(&data)?;
                Ok(Backend::Service(Remote::new(
                    data,
                    reply
                        .state
                        .ok_or_else(|| anyhow::anyhow!("后台服务未返回状态"))?,
                    self.notify.clone(),
                )))
            } else {
                manage::request(
                    manage::Operation::Uninstall,
                    &self.root,
                    None,
                    running.clone(),
                )?;
                let (local, rx) = ClientSupervisor::new(self.root.clone())?;
                bridge(rx, self.notify.clone());
                for id in &running {
                    let _ = local.reconnect_profile(id);
                }
                Ok(Backend::Local(local))
            }
        })();
        let mut state = self.state.lock().unwrap();
        state.busy = false;
        match result {
            Ok(backend) => {
                state.backend = backend;
                let _ = self.notify.send(SupervisorNotification::Updated);
                Ok(())
            }
            Err(error) => {
                match platform::owned_manifest(&self.root) {
                    Ok(Some(manifest)) if manifest.enabled => {
                        if enabled {
                            let mut view = match &old {
                                Backend::Local(local) => local.view_state(),
                                Backend::Service(remote) => {
                                    remote.state.lock().unwrap().view.clone()
                                }
                            };
                            mark_unavailable(&mut view);
                            state.backend = Backend::Service(Remote::new(
                                platform::data_root()?,
                                view,
                                self.notify.clone(),
                            ));
                        }
                    }
                    Ok(_) => {
                        if enabled {
                            if let Backend::Local(local) = &old {
                                for id in &running {
                                    let _ = local.reconnect_profile(id);
                                }
                            }
                        } else {
                            let (local, rx) = ClientSupervisor::new(self.root.clone())?;
                            bridge(rx, self.notify.clone());
                            for id in &running {
                                let _ = local.reconnect_profile(id);
                            }
                            state.backend = Backend::Local(local);
                        }
                    }
                    Err(_) => {
                        // Never start local sidecars when the system owner cannot be determined.
                        if enabled {
                            let mut view = self.view_state_without_lock(&old);
                            mark_unavailable(&mut view);
                            state.backend = Backend::Service(Remote::new(
                                platform::data_root()?,
                                view,
                                self.notify.clone(),
                            ));
                        }
                    }
                }
                Err(error)
            }
        }
    }
    fn view_state_without_lock(&self, backend: &Backend) -> SupervisorViewState {
        match backend {
            Backend::Local(local) => local.view_state(),
            Backend::Service(remote) => remote.state.lock().unwrap().view.clone(),
        }
    }
    pub fn update_for_app(&self, package: &Path) -> Result<()> {
        self.service_operation(|| {
            if !self.is_service_managed() {
                return Ok(());
            }
            let binaries = manage::unpack_binaries(package)?;
            manage::request(
                manage::Operation::Upgrade,
                &self.root,
                Some(binaries.path()),
                self.service_restart_profile_ids(),
            )?;
            wait_for_service(&platform::data_root()?)?;
            Ok(())
        })
    }
    pub fn repair_service(&self) -> Result<()> {
        self.service_operation(|| {
            if !self.is_service_managed() {
                bail!("后台服务尚未启用");
            }
            manage::request(
                manage::Operation::Upgrade,
                &self.root,
                None,
                self.service_restart_profile_ids(),
            )?;
            wait_for_service(&platform::data_root()?)?;
            Ok(())
        })
    }
    fn service_operation(&self, operation: impl FnOnce() -> Result<()>) -> Result<()> {
        {
            let mut state = self.state.lock().unwrap();
            if state.busy {
                bail!("正在配置后台运行，请稍候再更新");
            }
            state.busy = true;
        }
        let result = operation();
        self.state.lock().unwrap().busy = false;
        let _ = self.notify.send(SupervisorNotification::Updated);
        result
    }
    pub fn running_profile_ids(&self) -> Vec<String> {
        self.view_state()
            .sessions
            .into_iter()
            .filter(|s| s.phase.is_running())
            .map(|s| s.profile.id)
            .collect()
    }
    fn service_restart_profile_ids(&self) -> Vec<String> {
        let state = self.state.lock().unwrap();
        let Backend::Service(remote) = &state.backend else {
            return Vec::new();
        };
        let paused = crate::presence::pause_reason(&remote.data).is_some();
        let remote = remote.state.lock().unwrap();
        remote
            .view
            .sessions
            .iter()
            .filter(|s| {
                s.phase.is_running()
                    || ((remote.error.is_some() || paused)
                        && s.profile.auto_connect
                        && s.has_saved_token)
            })
            .map(|s| s.profile.id.clone())
            .collect()
    }
    pub fn has_any_running(&self) -> bool {
        self.view_state().running_count > 0
    }
    pub fn set_appearance(&self, value: AppearancePreference) -> Result<()> {
        match self.backend()? {
            Backend::Local(v) => v.set_appearance(value),
            Backend::Service(v) => v.call(Command::Appearance(value)).map(|_| ()),
        }
    }
    pub fn select_profile(&self, id: &str) -> Result<()> {
        match self.backend()? {
            Backend::Local(v) => v.select_profile(id),
            Backend::Service(v) => v.call(Command::Select(id.into())).map(|_| ()),
        }
    }
    pub fn rename_profile(&self, id: &str, name: &str) -> Result<()> {
        match self.backend()? {
            Backend::Local(v) => v.rename_profile(id, name),
            Backend::Service(v) => v
                .call(Command::Rename {
                    id: id.into(),
                    name: name.into(),
                })
                .map(|_| ()),
        }
    }
    pub fn connect(&self, request: StartRequest) -> Result<String> {
        match self.backend()? {
            Backend::Local(v) => v.connect(request),
            Backend::Service(v) => string_result(v.call(Command::Connect(request))?),
        }
    }
    pub fn reconnect_profile(&self, id: &str) -> Result<String> {
        match self.backend()? {
            Backend::Local(v) => v.reconnect_profile(id),
            Backend::Service(v) => string_result(v.call(Command::Reconnect(id.into()))?),
        }
    }
    pub fn forget_profile(&self, id: &str) -> Result<()> {
        match self.backend()? {
            Backend::Local(v) => v.forget_profile(id),
            Backend::Service(v) => v.call(Command::Forget(id.into())).map(|_| ()),
        }
    }
    pub fn disconnect_profile(&self, id: &str) -> Result<()> {
        match self.backend()? {
            Backend::Local(v) => v.disconnect_profile(id),
            Backend::Service(v) => v.call(Command::Disconnect(Some(id.into()))).map(|_| ()),
        }
    }
    pub fn disconnect_all(&self) -> Result<()> {
        match self.backend()? {
            Backend::Local(v) => v.disconnect_all(),
            Backend::Service(v) => v.call(Command::Disconnect(None)).map(|_| ()),
        }
    }
    pub fn save_or_reconnect_edited_profile(
        &self,
        id: &str,
        server: &str,
        name: &str,
        key: Option<&str>,
        tls_skip_verify: bool,
        tls_fingerprint: &str,
    ) -> Result<String> {
        match self.backend()? {
            Backend::Local(v) => v.save_or_reconnect_edited_profile(
                id,
                server,
                name,
                key,
                tls_skip_verify,
                tls_fingerprint,
            ),
            Backend::Service(v) => string_result(v.call(Command::Edit(EditProfile {
                id: id.into(),
                server: server.into(),
                name: name.into(),
                key: key.map(str::to_owned),
                tls_skip_verify,
                tls_fingerprint: tls_fingerprint.into(),
            }))?),
        }
    }
    pub fn reconnect_all_saved(&self) {
        if let Ok(backend) = self.backend() {
            match backend {
                Backend::Local(v) => v.reconnect_all_saved(),
                Backend::Service(v) => {
                    let _ = v.call(Command::ReconnectAll);
                }
            }
        }
    }
    pub fn request_snapshot(&self, id: &str) {
        if let Ok(backend) = self.backend() {
            match backend {
                Backend::Local(v) => v.request_snapshot(id),
                Backend::Service(v) => {
                    let _ = v.call(Command::Snapshot(Some(id.into())));
                }
            }
        }
    }
    pub fn clear_logs(&self, id: &str) {
        if let Ok(backend) = self.backend() {
            match backend {
                Backend::Local(v) => v.clear_logs(id),
                Backend::Service(v) => {
                    let _ = v.call(Command::ClearLogs(id.into()));
                }
            }
        }
    }
}

fn string_result(value: Value) -> Result<String> {
    value
        .as_str()
        .map(str::to_owned)
        .ok_or_else(|| anyhow::anyhow!("后台服务返回了无效结果"))
}
fn bridge(rx: mpsc::Receiver<SupervisorNotification>, tx: mpsc::Sender<SupervisorNotification>) {
    thread::spawn(move || {
        for event in rx {
            if tx.send(event).is_err() {
                break;
            }
        }
    });
}
fn wait_for_service(data: &Path) -> Result<Reply> {
    let start = Instant::now();
    loop {
        match ipc::call(data, Command::State) {
            Ok(reply) => return Ok(reply),
            Err(error) if start.elapsed() >= Duration::from_secs(20) => return Err(error),
            Err(_) => thread::sleep(Duration::from_millis(200)),
        }
    }
}
