use std::env;
use std::fs::{create_dir_all, remove_dir_all, remove_file, OpenOptions};
use std::io::{ErrorKind, Write};
use std::sync::{Arc, Mutex};

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tauri::{Emitter, Manager, RunEvent};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

const SIDECAR_EVENT_NAME: &str = "netsgo://client-sidecar-event";
const SIDECAR_BASE_PATH: &str = "binaries/netsgo";

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .manage(ClientSidecarManager::default())
        .invoke_handler(tauri::generate_handler![
            append_desktop_log,
            clear_client_state_dir,
            start_client_sidecar,
            stop_client_sidecar,
            client_sidecar_status
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| {
            if let RunEvent::Exit = event {
                let manager = app.state::<ClientSidecarManager>();
                let _ = manager.kill_running_child();
            }
        });
}

#[derive(Default)]
struct ClientSidecarManager {
    inner: Arc<Mutex<ClientSidecarState>>,
}

#[derive(Default)]
struct ClientSidecarState {
    child: Option<CommandChild>,
    pid: Option<u32>,
    server: Option<String>,
    mode: Option<String>,
    connected: bool,
    last_error: Option<String>,
    start_seq: u64,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct StartClientSidecarRequest {
    server: String,
    key: Option<String>,
    mode: String,
    data_dir: String,
}

#[derive(Debug, Serialize, Clone)]
struct ClientSidecarStatus {
    running: bool,
    pid: Option<u32>,
    server: Option<String>,
    mode: Option<String>,
    connected: bool,
    last_error: Option<String>,
}

#[derive(Debug, Serialize, Clone)]
struct ClientSidecarEvent {
    kind: String,
    pid: Option<u32>,
    server: Option<String>,
    mode: Option<String>,
    line: Option<String>,
    event: Option<Value>,
    code: Option<i32>,
    signal: Option<i32>,
    error: Option<String>,
}

impl ClientSidecarManager {
    fn status(&self) -> ClientSidecarStatus {
        let state = self.inner.lock().unwrap();
        ClientSidecarStatus {
            running: state.child.is_some(),
            pid: state.pid,
            server: state.server.clone(),
            mode: state.mode.clone(),
            connected: state.connected,
            last_error: state.last_error.clone(),
        }
    }

    fn kill_running_child(&self) -> Result<(), String> {
        let child = {
            let mut state = self.inner.lock().unwrap();
            state.child.take()
        };

        if let Some(child) = child {
            child.kill().map_err(|err| format!("kill sidecar: {err}"))?;
        }

        let mut state = self.inner.lock().unwrap();
        state.connected = false;
        state.pid = None;
        Ok(())
    }
}

#[tauri::command]
fn append_desktop_log(app: tauri::AppHandle, entry: Value) -> Result<(), String> {
    let base = app
        .path()
        .app_local_data_dir()
        .map_err(|err| format!("resolve app log dir: {err}"))?;
    let dir = base.join("logs");
    create_dir_all(&dir).map_err(|err| format!("create app log dir: {err}"))?;

    let path = dir.join("desktop.jsonl");
    let mut file = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
        .map_err(|err| format!("open app log file: {err}"))?;

    let line =
        serde_json::to_string(&entry).map_err(|err| format!("encode app log entry: {err}"))?;
    file.write_all(line.as_bytes())
        .and_then(|_| file.write_all(b"\n"))
        .and_then(|_| file.flush())
        .map_err(|err| format!("write app log file: {err}"))?;

    Ok(())
}

#[tauri::command]
fn clear_client_state_dir(app: tauri::AppHandle) -> Result<(), String> {
    let base = app
        .path()
        .app_local_data_dir()
        .map_err(|err| format!("resolve app data dir: {err}"))?;

    for path in [base.join("client"), base.join("locks").join("client.lock")] {
        match remove_dir_all(&path) {
            Ok(()) => continue,
            Err(err) if err.kind() == ErrorKind::NotFound => continue,
            Err(dir_err) => match remove_file(&path) {
                Ok(()) => continue,
                Err(file_err) if file_err.kind() == ErrorKind::NotFound => continue,
                Err(file_err) => {
                    return Err(format!(
                        "clear client state {}: {dir_err}; remove file: {file_err}",
                        path.display()
                    ));
                }
            },
        }
    }

    Ok(())
}

#[tauri::command]
fn client_sidecar_status(manager: tauri::State<ClientSidecarManager>) -> ClientSidecarStatus {
    manager.status()
}

#[tauri::command]
fn start_client_sidecar(
    app: tauri::AppHandle,
    manager: tauri::State<ClientSidecarManager>,
    request: StartClientSidecarRequest,
) -> Result<ClientSidecarStatus, String> {
    let server = request.server.trim().to_string();
    if server.is_empty() {
        return Err("server must not be empty".into());
    }

    {
        let state = manager.inner.lock().unwrap();
        if state.child.is_some() {
            return Ok(ClientSidecarStatus {
                running: true,
                pid: state.pid,
                server: state.server.clone(),
                mode: state.mode.clone(),
                connected: state.connected,
                last_error: state.last_error.clone(),
            });
        }
    }

    let mut args = vec!["client".to_string(), "--server".to_string(), server.clone()];
    if let Some(key) = request
        .key
        .as_ref()
        .filter(|value| !value.trim().is_empty())
    {
        args.push("--key".to_string());
        args.push(key.trim().to_string());
    }
    args.extend([
        "--data-dir".to_string(),
        request.data_dir.clone(),
        "--log-format".to_string(),
        "json".to_string(),
    ]);

    let sidecar_path = resolve_sidecar_path(&app)?;
    let command = app.shell().command(sidecar_path).args(args);

    let (mut rx, child) = command
        .spawn()
        .map_err(|err| format!("spawn sidecar: {err}"))?;
    let pid = child.pid();

    let start_seq = {
        let mut state = manager.inner.lock().unwrap();
        state.start_seq += 1;
        state.child = Some(child);
        state.pid = Some(pid);
        state.server = Some(server.clone());
        state.mode = Some(request.mode.clone());
        state.connected = false;
        state.last_error = None;
        state.start_seq
    };

    let state_ref = manager.inner.clone();
    let app_ref = app.clone();
    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            let payload = process_sidecar_event(&state_ref, start_seq, pid, event);
            let _ = app_ref.emit(SIDECAR_EVENT_NAME, payload);
        }
    });

    Ok(manager.status())
}

fn resolve_sidecar_path(app: &tauri::AppHandle) -> Result<std::path::PathBuf, String> {
    let target_triple = option_env!("TAURI_ENV_TARGET_TRIPLE")
        .map(str::to_string)
        .or_else(|| env::var("TARGET").ok())
        .unwrap_or_else(current_target_triple);

    let mut tried = Vec::new();
    for relative in sidecar_relative_candidates(&target_triple) {
        let resource_path = app
            .path()
            .resolve(&relative, tauri::path::BaseDirectory::Resource)
            .map_err(|err| format!("resolve sidecar resource path: {err}"))?;
        tried.push(resource_path.display().to_string());
        if resource_path.exists() {
            return Ok(resource_path);
        }

        let dev_path = std::path::PathBuf::from(&relative);
        tried.push(dev_path.display().to_string());
        if dev_path.exists() {
            return Ok(dev_path);
        }
    }

    Err(format!(
        "sidecar binary not found; tried: {}",
        tried.join(", ")
    ))
}

fn sidecar_relative_candidates(target_triple: &str) -> Vec<String> {
    let base = format!("{SIDECAR_BASE_PATH}-{target_triple}");
    if is_windows_target(target_triple) {
        vec![format!("{base}.exe"), base]
    } else {
        vec![base]
    }
}

fn is_windows_target(target_triple: &str) -> bool {
    target_triple.contains("-windows-")
}

fn current_target_triple() -> String {
    let arch = match env::consts::ARCH {
        "aarch64" => "aarch64",
        "x86_64" => "x86_64",
        "x86" => "i686",
        "arm" => "armv7",
        other => other,
    };
    let os = match env::consts::OS {
        "macos" => "apple-darwin".to_string(),
        "linux" => {
            let env = if cfg!(target_env = "musl") {
                "musl"
            } else {
                "gnu"
            };
            format!("unknown-linux-{env}")
        }
        "windows" => {
            let env = if cfg!(target_env = "gnu") {
                "gnu"
            } else {
                "msvc"
            };
            format!("pc-windows-{env}")
        }
        other => other.to_string(),
    };
    format!("{arch}-{os}")
}

#[tauri::command]
fn stop_client_sidecar(
    manager: tauri::State<ClientSidecarManager>,
) -> Result<ClientSidecarStatus, String> {
    manager.kill_running_child()?;
    Ok(manager.status())
}

fn process_sidecar_event(
    state_ref: &Arc<Mutex<ClientSidecarState>>,
    start_seq: u64,
    pid: u32,
    event: CommandEvent,
) -> ClientSidecarEvent {
    match event {
        CommandEvent::Stdout(bytes) | CommandEvent::Stderr(bytes) => {
            let line = String::from_utf8_lossy(&bytes).trim().to_string();
            let parsed = if line.is_empty() {
                None
            } else {
                serde_json::from_str::<Value>(&line).ok()
            };

            let (server, mode) = {
                let mut state = state_ref.lock().unwrap();
                if state.start_seq == start_seq {
                    if parsed
                        .as_ref()
                        .and_then(|value| value.get("event"))
                        .and_then(Value::as_str)
                        == Some("client.data_channel_established")
                    {
                        state.connected = true;
                        state.last_error = None;
                    }
                    if parsed
                        .as_ref()
                        .and_then(|value| value.get("level"))
                        .and_then(Value::as_str)
                        == Some("error")
                    {
                        state.last_error = Some(line.clone());
                    }
                }
                (state.server.clone(), state.mode.clone())
            };

            ClientSidecarEvent {
                kind: "line".into(),
                pid: Some(pid),
                server,
                mode,
                line: Some(line),
                event: parsed,
                code: None,
                signal: None,
                error: None,
            }
        }
        CommandEvent::Terminated(payload) => {
            let (server, mode, last_error) = {
                let mut state = state_ref.lock().unwrap();
                if state.start_seq == start_seq && state.pid == Some(pid) {
                    state.child = None;
                    state.pid = None;
                    state.connected = false;
                }
                (
                    state.server.clone(),
                    state.mode.clone(),
                    state.last_error.clone(),
                )
            };

            ClientSidecarEvent {
                kind: "terminated".into(),
                pid: Some(pid),
                server,
                mode,
                line: None,
                event: None,
                code: payload.code,
                signal: payload.signal,
                error: last_error,
            }
        }
        CommandEvent::Error(error) => {
            let (server, mode) = {
                let mut state = state_ref.lock().unwrap();
                if state.start_seq == start_seq {
                    state.last_error = Some(error.clone());
                }
                (state.server.clone(), state.mode.clone())
            };

            ClientSidecarEvent {
                kind: "error".into(),
                pid: Some(pid),
                server,
                mode,
                line: None,
                event: Some(json!({
                    "level": "error",
                    "event": "desktop.sidecar_error",
                    "message": error,
                })),
                code: None,
                signal: None,
                error: Some(error),
            }
        }
        _ => ClientSidecarEvent {
            kind: "unknown".into(),
            pid: Some(pid),
            server: None,
            mode: None,
            line: None,
            event: None,
            code: None,
            signal: None,
            error: None,
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn windows_sidecar_candidates_try_exe_first() {
        let candidates = sidecar_relative_candidates("x86_64-pc-windows-msvc");

        assert_eq!(
            candidates,
            vec![
                "binaries/netsgo-x86_64-pc-windows-msvc.exe",
                "binaries/netsgo-x86_64-pc-windows-msvc",
            ]
        );
    }

    #[test]
    fn unix_sidecar_candidates_do_not_add_exe() {
        let candidates = sidecar_relative_candidates("aarch64-apple-darwin");

        assert_eq!(candidates, vec!["binaries/netsgo-aarch64-apple-darwin"]);
    }
}
