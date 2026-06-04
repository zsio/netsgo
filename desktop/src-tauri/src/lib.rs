use std::env;
use std::fs::{create_dir_all, metadata, remove_dir_all, remove_file, rename, OpenOptions};
use std::io::{ErrorKind, Write};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tauri::{
    image::Image,
    menu::{Menu, MenuBuilder, MenuItem},
    tray::TrayIconBuilder,
    Emitter, Manager, RunEvent, WindowEvent,
};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

const SIDECAR_EVENT_NAME: &str = "netsgo://client-sidecar-event";
const SIDECAR_BASE_PATH: &str = "binaries/netsgo";
const TRAY_ACTION_ID: &str = "connection-action";
const TRAY_SHOW_ID: &str = "show-window";
const TRAY_QUIT_ID: &str = "quit-app";
const TRAY_ID: &str = "main";
const DESKTOP_LOG_MAX_ENTRY_BYTES: usize = 16 * 1024;
const DESKTOP_LOG_MAX_FILE_BYTES: u64 = 2 * 1024 * 1024;
const DESKTOP_LOG_ROTATED_FILE_NAME: &str = "desktop.1.jsonl";

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .manage(ClientSidecarManager::default())
        .manage(TrayConnectionState::default())
        .setup(|app| {
            let menu = build_tray_menu(app.handle(), TrayConnectionAction::Hidden)?;

            let mut tray = TrayIconBuilder::with_id(TRAY_ID)
                .tooltip("NetsGo")
                .menu(&menu)
                .show_menu_on_left_click(true)
                .on_menu_event(|app, event| match event.id().as_ref() {
                    TRAY_ACTION_ID => {
                        let manager = app.state::<ClientSidecarManager>();
                        let tray_state = app.state::<TrayConnectionState>();
                        let action = tray_state.action();
                        if action == TrayConnectionAction::Disconnect {
                            if let Some(window) = app.get_webview_window("main") {
                                let _ = window.emit("netsgo://tray-disconnect-request", ());
                            } else {
                                let _ = manager.kill_running_child();
                            }
                        } else if action == TrayConnectionAction::Connect {
                            if let Some(window) = app.get_webview_window("main") {
                                let _ = window.show();
                                let _ = window.set_focus();
                                let _ = window.emit("netsgo://tray-start-request", ());
                            }
                        }
                    }
                    TRAY_SHOW_ID => {
                        if let Some(window) = app.get_webview_window("main") {
                            let _ = window.show();
                            let _ = window.set_focus();
                        }
                    }
                    TRAY_QUIT_ID => {
                        let manager = app.state::<ClientSidecarManager>();
                        let _ = manager.kill_running_child();
                        app.exit(0);
                    }
                    _ => {}
                });

            if let Some(icon) = app.default_window_icon() {
                tray = tray.icon(disconnected_tray_icon(icon));
            }
            tray.build(app)?;
            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .invoke_handler(tauri::generate_handler![
            append_desktop_log,
            clear_client_state_dir,
            start_client_sidecar,
            stop_client_sidecar,
            client_sidecar_status,
            update_tray_connection_menu
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

#[derive(Default)]
struct TrayConnectionState {
    inner: Mutex<TrayConnectionAction>,
}

#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
enum TrayConnectionAction {
    #[default]
    Hidden,
    Connect,
    Disconnect,
}

impl TrayConnectionState {
    fn action(&self) -> TrayConnectionAction {
        *self.inner.lock().unwrap()
    }

    fn set_action(&self, action: TrayConnectionAction) {
        *self.inner.lock().unwrap() = action;
    }
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct StartClientSidecarRequest {
    server: String,
    key: Option<String>,
    mode: String,
    data_dir: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct TrayConnectionMenuRequest {
    state: String,
    has_saved_connection: bool,
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
    let rotated_path = dir.join(DESKTOP_LOG_ROTATED_FILE_NAME);
    rotate_desktop_log_if_needed(&path, &rotated_path)?;

    let sanitized = sanitize_log_value(entry);
    let line =
        serde_json::to_string(&sanitized).map_err(|err| format!("encode app log entry: {err}"))?;
    if line.len() > DESKTOP_LOG_MAX_ENTRY_BYTES {
        return Err(format!(
            "desktop log entry exceeds {} bytes",
            DESKTOP_LOG_MAX_ENTRY_BYTES
        ));
    }

    let mut file = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
        .map_err(|err| format!("open app log file: {err}"))?;

    file.write_all(line.as_bytes())
        .and_then(|_| file.write_all(b"\n"))
        .and_then(|_| file.flush())
        .map_err(|err| format!("write app log file: {err}"))?;

    Ok(())
}

fn rotate_desktop_log_if_needed(path: &Path, rotated_path: &Path) -> Result<(), String> {
    match metadata(path) {
        Ok(meta) if meta.len() >= DESKTOP_LOG_MAX_FILE_BYTES => {
            match remove_file(rotated_path) {
                Ok(()) => {}
                Err(err) if err.kind() == ErrorKind::NotFound => {}
                Err(err) => return Err(format!("remove rotated app log file: {err}")),
            }
            rename(path, rotated_path).map_err(|err| format!("rotate app log file: {err}"))?;
        }
        Ok(_) => {}
        Err(err) if err.kind() == ErrorKind::NotFound => {}
        Err(err) => return Err(format!("stat app log file: {err}")),
    }
    Ok(())
}

fn sanitize_log_value(value: Value) -> Value {
    match value {
        Value::Object(map) => Value::Object(
            map.into_iter()
                .map(|(key, value)| {
                    let sanitized = if is_sensitive_log_key(&key) {
                        Value::String("[REDACTED]".into())
                    } else {
                        sanitize_log_value(value)
                    };
                    (key, sanitized)
                })
                .collect(),
        ),
        Value::Array(values) => Value::Array(values.into_iter().map(sanitize_log_value).collect()),
        other => other,
    }
}

fn is_sensitive_log_key(key: &str) -> bool {
    let normalized = key.to_ascii_lowercase();
    matches!(
        normalized.as_str(),
        "key" | "token" | "datatoken" | "data_token" | "authorization" | "password" | "secret"
    ) || normalized.ends_with("_key")
        || normalized.ends_with("_token")
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
fn update_tray_connection_menu(
    app: tauri::AppHandle,
    tray_state: tauri::State<TrayConnectionState>,
    request: TrayConnectionMenuRequest,
) -> Result<(), String> {
    let action = tray_action_for_state(&request.state, request.has_saved_connection);
    tray_state.set_action(action);

    let menu = build_tray_menu(&app, action).map_err(|err| format!("build tray menu: {err}"))?;
    let tray = app
        .tray_by_id(TRAY_ID)
        .ok_or_else(|| format!("tray icon not found: {TRAY_ID}"))?;
    tray.set_menu(Some(menu))
        .map_err(|err| format!("update tray menu: {err}"))?;
    if let Some(icon) = app.default_window_icon() {
        let icon = if action == TrayConnectionAction::Disconnect {
            icon.clone()
        } else {
            disconnected_tray_icon(icon)
        };
        tray.set_icon(Some(icon))
            .map_err(|err| format!("update tray icon: {err}"))?;
    }
    Ok(())
}

fn tray_action_for_state(state: &str, has_saved_connection: bool) -> TrayConnectionAction {
    match state {
        "connected" | "connecting" | "disconnecting" => TrayConnectionAction::Disconnect,
        "saved" if has_saved_connection => TrayConnectionAction::Connect,
        _ => TrayConnectionAction::Hidden,
    }
}

fn build_tray_menu<R: tauri::Runtime>(
    manager: &impl Manager<R>,
    action: TrayConnectionAction,
) -> tauri::Result<Menu<R>> {
    let show = MenuItem::with_id(manager, TRAY_SHOW_ID, "显示", true, None::<&str>)?;
    let quit = MenuItem::with_id(manager, TRAY_QUIT_ID, "退出程序", true, None::<&str>)?;
    let builder = MenuBuilder::new(manager);

    match action {
        TrayConnectionAction::Connect => builder
            .item(&MenuItem::with_id(
                manager,
                TRAY_ACTION_ID,
                "连接",
                true,
                None::<&str>,
            )?)
            .item(&show)
            .separator()
            .item(&quit)
            .build(),
        TrayConnectionAction::Disconnect => builder
            .item(&MenuItem::with_id(
                manager,
                TRAY_ACTION_ID,
                "断开",
                true,
                None::<&str>,
            )?)
            .item(&show)
            .separator()
            .item(&quit)
            .build(),
        TrayConnectionAction::Hidden => builder.item(&show).separator().item(&quit).build(),
    }
}

fn disconnected_tray_icon(icon: &Image<'_>) -> Image<'static> {
    let mut rgba = icon.rgba().to_vec();
    for pixel in rgba.chunks_exact_mut(4) {
        let [r, g, b, a] = pixel else {
            continue;
        };
        if *a > 0 && is_netsgo_orange(*r, *g, *b) {
            *r = 41;
            *g = 58;
            *b = 71;
        }
    }
    Image::new_owned(rgba, icon.width(), icon.height())
}

fn is_netsgo_orange(r: u8, g: u8, b: u8) -> bool {
    r > 180 && (80..=180).contains(&g) && b < 60
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

    let sidecar_key = request
        .key
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.trim().is_empty())
        .map(str::to_string);
    let mut args = vec!["client".to_string(), "--server".to_string(), server.clone()];
    args.extend([
        "--data-dir".to_string(),
        request.data_dir.clone(),
        "--log-format".to_string(),
        "json".to_string(),
    ]);

    let sidecar_path = resolve_sidecar_path()?;
    let mut command = app.shell().command(sidecar_path).args(args);
    if let Some(key) = sidecar_key {
        command = command.env("NETSGO_KEY", key);
    }

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

fn resolve_sidecar_path() -> Result<PathBuf, String> {
    let target_triple = option_env!("TAURI_ENV_TARGET_TRIPLE")
        .map(str::to_string)
        .or_else(|| env::var("TARGET").ok())
        .unwrap_or_else(current_target_triple);

    let mut tried = Vec::new();
    for candidate in sidecar_path_candidates(&target_triple) {
        tried.push(candidate.display().to_string());
        if candidate.exists() {
            return Ok(candidate);
        }
    }

    Err(format!(
        "sidecar binary not found; tried: {}",
        tried.join(", ")
    ))
}

fn sidecar_path_candidates(target_triple: &str) -> Vec<PathBuf> {
    let mut candidates = Vec::new();
    if let Ok(exe) = env::current_exe() {
        if let Some(exe_dir) = exe.parent() {
            for name in packaged_sidecar_names(target_triple) {
                candidates.push(exe_dir.join(name));
                candidates.push(exe_dir.join("binaries").join(name));
            }
        }
    }

    let relative = sidecar_relative_path(target_triple);
    if let Ok(cwd) = env::current_dir() {
        candidates.push(cwd.join(&relative));
        candidates.push(cwd.join("src-tauri").join(&relative));
    }
    candidates.push(PathBuf::from(relative));
    candidates
}

fn packaged_sidecar_names(target_triple: &str) -> &'static [&'static str] {
    if is_windows_target(target_triple) {
        &["netsgo.exe", "netsgo"]
    } else {
        &["netsgo", "netsgo.exe"]
    }
}

fn sidecar_relative_path(target_triple: &str) -> PathBuf {
    let filename = if is_windows_target(target_triple) {
        format!("{SIDECAR_BASE_PATH}-{target_triple}.exe")
    } else {
        format!("{SIDECAR_BASE_PATH}-{target_triple}")
    };
    PathBuf::from(filename)
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
    use serde_json::json;
    use std::fs;

    #[test]
    fn macos_packaged_sidecar_candidate_uses_flat_binary_name() {
        let candidates = sidecar_path_candidates("aarch64-apple-darwin");

        assert!(candidates.iter().any(|path| {
            path.file_name().and_then(|name| name.to_str()) == Some("netsgo")
                && path.parent().is_some_and(|parent| {
                    parent.file_name().and_then(|name| name.to_str()) == Some("MacOS")
                        || !parent.ends_with("binaries")
                })
        }));
    }

    #[test]
    fn dev_sidecar_candidate_keeps_target_triple_suffix() {
        let relative = sidecar_relative_path("x86_64-pc-windows-msvc");

        assert_eq!(
            relative,
            PathBuf::from("binaries/netsgo-x86_64-pc-windows-msvc.exe")
        );
    }

    #[test]
    fn windows_packaged_sidecar_candidate_includes_exe_extension() {
        let candidates = sidecar_path_candidates("x86_64-pc-windows-msvc");

        assert!(candidates
            .iter()
            .any(|path| { path.file_name().and_then(|name| name.to_str()) == Some("netsgo.exe") }));
    }

    #[test]
    fn packaged_sidecar_name_order_matches_target_platform() {
        assert_eq!(
            packaged_sidecar_names("x86_64-pc-windows-msvc"),
            &["netsgo.exe", "netsgo"]
        );
        assert_eq!(
            packaged_sidecar_names("aarch64-apple-darwin"),
            &["netsgo", "netsgo.exe"]
        );
    }

    #[test]
    fn tray_action_matches_connection_state() {
        assert_eq!(
            tray_action_for_state("idle", false),
            TrayConnectionAction::Hidden
        );
        assert_eq!(
            tray_action_for_state("saved", false),
            TrayConnectionAction::Hidden
        );
        assert_eq!(
            tray_action_for_state("saved", true),
            TrayConnectionAction::Connect
        );
        assert_eq!(
            tray_action_for_state("connected", true),
            TrayConnectionAction::Disconnect
        );
        assert_eq!(
            tray_action_for_state("connecting", true),
            TrayConnectionAction::Disconnect
        );
        assert_eq!(
            tray_action_for_state("disconnecting", true),
            TrayConnectionAction::Disconnect
        );
    }

    #[test]
    fn disconnected_tray_icon_recolors_orange_pixels() {
        let rgba = vec![251, 133, 4, 255, 41, 58, 71, 255, 251, 133, 4, 0];
        let icon = Image::new(&rgba, 3, 1);
        let disconnected = disconnected_tray_icon(&icon);

        assert_eq!(&disconnected.rgba()[0..4], &[41, 58, 71, 255]);
        assert_eq!(&disconnected.rgba()[4..8], &[41, 58, 71, 255]);
        assert_eq!(&disconnected.rgba()[8..12], &[251, 133, 4, 0]);
    }

    #[test]
    fn sanitize_log_value_redacts_nested_secrets() {
        let sanitized = sanitize_log_value(json!({
            "event": "desktop.test",
            "key": "client-key",
            "nested": {
                "refresh_token": "token-value",
                "safe": "kept"
            },
            "items": [
                { "dataToken": "data-token" }
            ]
        }));

        assert_eq!(sanitized["key"], "[REDACTED]");
        assert_eq!(sanitized["nested"]["refresh_token"], "[REDACTED]");
        assert_eq!(sanitized["nested"]["safe"], "kept");
        assert_eq!(sanitized["items"][0]["dataToken"], "[REDACTED]");
    }

    #[test]
    fn rotate_desktop_log_moves_oversized_current_log() {
        let dir =
            std::env::temp_dir().join(format!("netsgo-desktop-log-test-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).expect("create temp log dir");
        let path = dir.join("desktop.jsonl");
        let rotated_path = dir.join(DESKTOP_LOG_ROTATED_FILE_NAME);
        fs::write(&path, vec![b'x'; DESKTOP_LOG_MAX_FILE_BYTES as usize])
            .expect("write current log");

        rotate_desktop_log_if_needed(&path, &rotated_path).expect("rotate log");

        assert!(!path.exists());
        assert!(rotated_path.exists());
        let _ = fs::remove_dir_all(&dir);
    }
}
