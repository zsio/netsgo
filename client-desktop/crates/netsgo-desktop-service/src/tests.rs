use crate::{
    host,
    ipc::{self, Command, Endpoint, Reply, Request, SCHEMA},
};
use netsgo_client_core::{config_file_path, DesktopConfig, ServerProfile};
use std::{
    fs,
    net::{Ipv4Addr, TcpStream},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    thread,
    time::{Duration, Instant},
};

fn start() -> (
    tempfile::TempDir,
    Arc<AtomicBool>,
    thread::JoinHandle<anyhow::Result<()>>,
) {
    let dir = tempfile::tempdir().unwrap();
    let mut config = DesktopConfig::new_default();
    config.profiles.push(ServerProfile {
        id: "fixture".into(),
        name: "First".into(),
        server: "https://127.0.0.1:1".into(),
        tls_skip_verify: false,
        tls_fingerprint: String::new(),
        auto_connect: false,
        last_connected_unix: 0,
    });
    fs::write(
        config_file_path(dir.path()),
        serde_json::to_vec(&config).unwrap(),
    )
    .unwrap();
    let stop = Arc::new(AtomicBool::new(false));
    let signal = stop.clone();
    let root = dir.path().to_owned();
    let task = thread::spawn(move || host::run(&root, signal));
    let start = Instant::now();
    while ipc::call(dir.path(), Command::State).is_err() {
        assert!(
            start.elapsed() < Duration::from_secs(5),
            "agent did not start"
        );
        thread::sleep(Duration::from_millis(30));
    }
    (dir, stop, task)
}

#[test]
fn independent_agent_keeps_state_across_detached_clients_and_stops_cleanly() {
    let (dir, stop, task) = start();
    let initial = ipc::call(dir.path(), Command::State).unwrap();
    assert_eq!(initial.state.unwrap().sessions.len(), 1);
    // Each call uses and closes a separate UI connection; the supervisor remains the owner.
    ipc::call(
        dir.path(),
        Command::Rename {
            id: "fixture".into(),
            name: "Renamed".into(),
        },
    )
    .unwrap();
    let second = ipc::call(dir.path(), Command::State).unwrap();
    assert_eq!(second.state.unwrap().sessions[0].profile.name, "Renamed");
    stop.store(true, Ordering::SeqCst);
    task.join().unwrap().unwrap();
    assert!(!ipc::endpoint_path(dir.path()).exists());
    assert!(ipc::call(dir.path(), Command::State).is_err());
    let saved: DesktopConfig =
        serde_json::from_slice(&fs::read(config_file_path(dir.path())).unwrap()).unwrap();
    assert_eq!(saved.profiles[0].name, "Renamed");
}

#[test]
fn unauthenticated_or_wrong_protocol_clients_cannot_read_or_change_profiles() {
    let (dir, stop, task) = start();
    let endpoint: Endpoint =
        serde_json::from_slice(&fs::read(ipc::endpoint_path(dir.path())).unwrap()).unwrap();
    for (schema, token) in [
        (SCHEMA, "wrong".to_owned()),
        (SCHEMA + 1, endpoint.token.clone()),
    ] {
        let mut socket = TcpStream::connect((Ipv4Addr::LOCALHOST, endpoint.port)).unwrap();
        socket
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        ipc::write_frame(
            &mut socket,
            &Request {
                schema,
                token,
                command: Command::Rename {
                    id: "fixture".into(),
                    name: "Unauthorized".into(),
                },
            },
        )
        .unwrap();
        assert!(ipc::read_frame::<Reply>(&mut socket).is_err());
    }
    let state = ipc::call(dir.path(), Command::State)
        .unwrap()
        .state
        .unwrap();
    assert_eq!(state.sessions[0].profile.name, "First");
    stop.store(true, Ordering::SeqCst);
    task.join().unwrap().unwrap();
}

#[cfg(unix)]
#[test]
fn service_authentication_file_is_private_to_its_owner() {
    use std::os::unix::fs::PermissionsExt;
    let (dir, stop, task) = start();
    assert_eq!(
        fs::metadata(ipc::endpoint_path(dir.path()))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    stop.store(true, Ordering::SeqCst);
    task.join().unwrap().unwrap();
}

#[test]
fn second_agent_cannot_take_over_the_same_data_root() {
    let (dir, stop, task) = start();
    assert!(host::run(dir.path(), Arc::new(AtomicBool::new(false))).is_err());
    assert_eq!(
        ipc::call(dir.path(), Command::State)
            .unwrap()
            .state
            .unwrap()
            .sessions
            .len(),
        1
    );
    stop.store(true, Ordering::SeqCst);
    task.join().unwrap().unwrap();
}
