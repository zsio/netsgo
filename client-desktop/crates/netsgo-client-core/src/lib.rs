pub mod command_parser;

use std::collections::{HashMap, VecDeque};
use std::env;
use std::fs;
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::{mpsc, Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
pub use command_parser::{parse_connect_command, ParsedConnectCommand};
use netsgo_sidecar_proto::{
    format_client_event_error, normalize_error_message, AuthMode, ClientLogEvent, SidecarCommand,
    SidecarSnapshot,
};
use serde::{Deserialize, Serialize};

const MAX_RECENT_LOGS: usize = 80;
const MAX_LOG_MESSAGE_CHARS: usize = 512;

/// User theme preference; defaults to `System` (follows macOS / Windows light & dark mode).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AppearancePreference {
    #[default]
    System,
    Light,
    Dark,
}

impl AppearancePreference {
    pub fn cycle_next(self) -> Self {
        match self {
            Self::System => Self::Light,
            Self::Light => Self::Dark,
            Self::Dark => Self::System,
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Self::System => "跟随系统",
            Self::Light => "浅色",
            Self::Dark => "深色",
        }
    }
}

/// Persisted connection profile metadata (no plaintext key is ever written to disk).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ServerProfile {
    pub id: String,
    pub name: String,
    pub server: String,
    #[serde(default)]
    pub tls_skip_verify: bool,
    #[serde(default)]
    pub tls_fingerprint: String,
    #[serde(default = "default_true")]
    pub auto_connect: bool,
    #[serde(default)]
    pub last_connected_unix: u64,
}

fn default_true() -> bool {
    true
}

impl ServerProfile {
    pub fn display_title(&self) -> &str {
        let trimmed = self.name.trim();
        if trimmed.is_empty() {
            &self.server
        } else {
            trimmed
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct DesktopConfig {
    #[serde(default)]
    pub selected_profile_id: Option<String>,
    /// Legacy single-profile field kept for seamless config migration.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub active_profile_id: Option<String>,
    #[serde(default)]
    pub appearance: AppearancePreference,
    #[serde(default = "default_true")]
    pub auto_reconnect_on_launch: bool,
    #[serde(default)]
    pub profiles: Vec<ServerProfile>,
}

impl DesktopConfig {
    pub fn new_default() -> Self {
        Self {
            selected_profile_id: None,
            active_profile_id: None,
            appearance: AppearancePreference::System,
            auto_reconnect_on_launch: true,
            profiles: Vec::new(),
        }
    }

    pub fn normalize_legacy(&mut self) {
        if self.selected_profile_id.is_none() && self.active_profile_id.is_some() {
            self.selected_profile_id = self.active_profile_id.take();
        }
        if self.selected_profile_id.is_none() {
            self.selected_profile_id = self.profiles.first().map(|p| p.id.clone());
        }
    }

    pub fn selected_profile(&self) -> Option<&ServerProfile> {
        let id = self.selected_profile_id.as_deref()?;
        self.profiles.iter().find(|p| p.id == id)
    }

    pub fn upsert_profile(&mut self, mut profile: ServerProfile) -> ServerProfile {
        if profile.id.trim().is_empty() {
            profile.id = unique_profile_id(&self.profiles, &profile.server);
        }
        profile.name = profile.name.trim().to_string();
        if let Some(existing) = self
            .profiles
            .iter_mut()
            .find(|p| p.id == profile.id || p.server.eq_ignore_ascii_case(&profile.server))
        {
            existing.name = profile.name.clone();
            existing.server = profile.server.clone();
            existing.tls_skip_verify = profile.tls_skip_verify;
            existing.tls_fingerprint = profile.tls_fingerprint.clone();
            existing.auto_connect = profile.auto_connect;
            if profile.last_connected_unix > 0 {
                existing.last_connected_unix = profile.last_connected_unix;
            }
            let updated = existing.clone();
            self.selected_profile_id = Some(updated.id.clone());
            return updated;
        }
        self.selected_profile_id = Some(profile.id.clone());
        self.profiles.push(profile.clone());
        profile
    }

    pub fn remove_profile(&mut self, profile_id: &str) -> Option<ServerProfile> {
        let idx = self.profiles.iter().position(|p| p.id == profile_id)?;
        let removed = self.profiles.remove(idx);
        if self.selected_profile_id.as_deref() == Some(profile_id) {
            self.selected_profile_id = self.profiles.first().map(|p| p.id.clone());
        }
        Some(removed)
    }
}

fn unique_profile_id(existing: &[ServerProfile], server: &str) -> String {
    let base = slug_profile_id(server);
    if existing
        .iter()
        .any(|p| p.server.eq_ignore_ascii_case(server.trim()))
    {
        return existing
            .iter()
            .find(|p| p.server.eq_ignore_ascii_case(server.trim()))
            .map(|p| p.id.clone())
            .unwrap_or(base);
    }
    if !existing.iter().any(|p| p.id == base) {
        return base;
    }
    for idx in 2..1000 {
        let candidate = format!("{base}-{idx}");
        if !existing.iter().any(|p| p.id == candidate) {
            return candidate;
        }
    }
    format!("{base}-{}", now_unix_secs())
}

pub fn infer_profile_name(server: &str) -> String {
    let trimmed = server.trim();
    let without_scheme = trimmed
        .strip_prefix("https://")
        .or_else(|| trimmed.strip_prefix("http://"))
        .or_else(|| trimmed.strip_prefix("wss://"))
        .or_else(|| trimmed.strip_prefix("ws://"))
        .unwrap_or(trimmed);
    without_scheme
        .split('/')
        .next()
        .unwrap_or(trimmed)
        .to_string()
}

pub fn slug_profile_id(server: &str) -> String {
    let mut slug = String::new();
    for ch in infer_profile_name(server).chars() {
        if ch.is_ascii_alphanumeric() {
            slug.push(ch.to_ascii_lowercase());
        } else if !slug.ends_with('-') {
            slug.push('-');
        }
    }
    let cleaned = slug.trim_matches('-');
    if cleaned.is_empty() {
        format!("node-{}", now_unix_secs())
    } else {
        cleaned.to_string()
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ConnectionPhase {
    Idle,
    Saved,
    Connecting,
    Connected,
    Reconnecting,
    Disconnecting,
    Error,
}

impl ConnectionPhase {
    pub fn label(&self) -> &'static str {
        match self {
            Self::Idle => "未配置",
            Self::Saved => "未连接",
            Self::Connecting => "连接中",
            Self::Connected => "在线",
            Self::Reconnecting => "重连中",
            Self::Disconnecting => "断开中",
            Self::Error => "异常",
        }
    }

    pub fn is_busy(&self) -> bool {
        matches!(self, Self::Connecting | Self::Disconnecting)
    }

    pub fn is_running(&self) -> bool {
        matches!(self, Self::Connecting | Self::Connected | Self::Reconnecting)
    }
}

#[derive(Debug, Clone)]
pub struct LogLine {
    pub timestamp: String,
    pub level: String,
    pub event: String,
    pub message: String,
}

/// Independent runtime view for a single server profile.
#[derive(Debug, Clone)]
pub struct ProfileSessionView {
    pub profile: ServerProfile,
    pub phase: ConnectionPhase,
    pub status_text: String,
    pub last_error: Option<String>,
    pub has_saved_token: bool,
    pub mode: AuthMode,
    pub pid: Option<u32>,
    pub snapshot: SidecarSnapshot,
    pub recent_logs: Vec<LogLine>,
}

/// Aggregate multi-server workspace view consumed by GPUI and the system tray.
#[derive(Debug, Clone)]
pub struct SupervisorViewState {
    pub appearance: AppearancePreference,
    pub auto_reconnect_on_launch: bool,
    pub selected_profile_id: Option<String>,
    pub sessions: Vec<ProfileSessionView>,
    pub sidecar_binary: Option<PathBuf>,
    pub connected_count: usize,
    pub running_count: usize,
    pub total_tunnels: usize,
    pub total_active_streams: i64,
}

impl SupervisorViewState {
    pub fn selected_session(&self) -> Option<&ProfileSessionView> {
        if let Some(id) = self.selected_profile_id.as_deref() {
            if let Some(found) = self.sessions.iter().find(|s| s.profile.id == id) {
                return Some(found);
            }
        }
        self.sessions.first()
    }
}

#[derive(Debug, Clone)]
pub struct StartRequest {
    pub profile_id: String,
    pub name: String,
    pub server: String,
    pub key: Option<String>,
    pub mode: AuthMode,
    pub tls_skip_verify: bool,
    pub tls_fingerprint: String,
}

#[derive(Debug, Clone)]
pub enum SupervisorNotification {
    Updated,
    SessionError {
        profile_title: String,
        message: String,
    },
}

struct RunningChild {
    pid: u32,
    stdin: Option<ChildStdin>,
    child: Child,
}

struct ProfileRuntime {
    phase: ConnectionPhase,
    status_text: String,
    last_error: Option<String>,
    mode: AuthMode,
    generation: u64,
    intentional_stop: bool,
    running: Option<RunningChild>,
    snapshot: SidecarSnapshot,
    snapshot_received_at: Option<Instant>,
    recent_logs: VecDeque<LogLine>,
}

impl ProfileRuntime {
    fn new_saved(server: &str) -> Self {
        Self {
            phase: ConnectionPhase::Saved,
            status_text: String::new(),
            last_error: None,
            mode: AuthMode::Token,
            generation: 0,
            intentional_stop: false,
            running: None,
            snapshot: SidecarSnapshot {
                server: server.to_string(),
                ..Default::default()
            },
            snapshot_received_at: None,
            recent_logs: VecDeque::with_capacity(MAX_RECENT_LOGS),
        }
    }

    fn uptime_seconds(&self, now: Instant) -> i64 {
        // Snapshots arrive on runtime events; keep process uptime ticking between them.
        let elapsed = if self.phase.is_running() {
            self.snapshot_received_at
                .map(|received_at| now.saturating_duration_since(received_at).as_secs() as i64)
                .unwrap_or(0)
        } else {
            0
        };
        self.snapshot.uptime_seconds.saturating_add(elapsed)
    }
}

struct SupervisorInner {
    root_dir: PathBuf,
    config: DesktopConfig,
    runtimes: HashMap<String, ProfileRuntime>,
    sidecar_binary: Option<PathBuf>,
    notify_tx: mpsc::Sender<SupervisorNotification>,
}

/// Concurrent multi-server sidecar supervisor. Each `ServerProfile` owns an
/// isolated `--data-dir` (`profiles/<id>/netsgo.db` + `locks/client.lock`) and
/// an independent Go `netsgo client --control-stdio` child process.
#[derive(Clone)]
pub struct ClientSupervisor {
    inner: Arc<Mutex<SupervisorInner>>,
}

impl ClientSupervisor {
    pub fn new(root_dir: PathBuf) -> Result<(Self, mpsc::Receiver<SupervisorNotification>)> {
        fs::create_dir_all(&root_dir)
            .with_context(|| format!("create desktop data root {}", root_dir.display()))?;
        let config_path = config_file_path(&root_dir);
        let mut config = if config_path.exists() {
            let raw = fs::read_to_string(&config_path)
                .with_context(|| format!("read {}", config_path.display()))?;
            serde_json::from_str::<DesktopConfig>(&raw).unwrap_or_else(|_| DesktopConfig::new_default())
        } else {
            DesktopConfig::new_default()
        };
        config.normalize_legacy();

        let mut runtimes = HashMap::new();
        for profile in &config.profiles {
            runtimes.insert(profile.id.clone(), ProfileRuntime::new_saved(&profile.server));
        }

        let (notify_tx, notify_rx) = mpsc::channel();
        let sidecar_binary = resolve_sidecar_binary().ok();

        let inner = SupervisorInner {
            root_dir,
            config,
            runtimes,
            sidecar_binary,
            notify_tx,
        };

        Ok((
            Self {
                inner: Arc::new(Mutex::new(inner)),
            },
            notify_rx,
        ))
    }

    pub fn root_dir(&self) -> PathBuf {
        self.inner.lock().unwrap().root_dir.clone()
    }

    pub fn set_appearance(&self, pref: AppearancePreference) -> Result<()> {
        let mut guard = self.inner.lock().unwrap();
        guard.config.appearance = pref;
        save_config(&guard.root_dir, &guard.config)?;
        let _ = guard.notify_tx.send(SupervisorNotification::Updated);
        Ok(())
    }

    pub fn view_state(&self) -> SupervisorViewState {
        let mut guard = self.inner.lock().unwrap();
        if guard.sidecar_binary.is_none() {
            guard.sidecar_binary = resolve_sidecar_binary().ok();
        }

        let mut connected_count = 0usize;
        let mut running_count = 0usize;
        let mut total_tunnels = 0usize;
        let mut total_active_streams = 0i64;
        let now = Instant::now();

        let sessions = guard
            .config
            .profiles
            .iter()
            .map(|profile| {
                let has_saved_token = has_persisted_client_state(&guard.root_dir, &profile.id);
                if let Some(rt) = guard.runtimes.get(&profile.id) {
                    if rt.phase == ConnectionPhase::Connected {
                        connected_count += 1;
                    }
                    if rt.running.is_some() {
                        running_count += 1;
                    }
                    total_tunnels += rt.snapshot.tunnels.len();
                    total_active_streams += rt.snapshot.active_connections;
                    let mut snapshot = rt.snapshot.clone();
                    snapshot.uptime_seconds = rt.uptime_seconds(now);

                    ProfileSessionView {
                        profile: profile.clone(),
                        phase: rt.phase.clone(),
                        status_text: rt.status_text.clone(),
                        last_error: rt.last_error.clone(),
                        has_saved_token,
                        mode: rt.mode,
                        pid: rt.running.as_ref().map(|r| r.pid),
                        snapshot,
                        recent_logs: rt.recent_logs.iter().cloned().collect(),
                    }
                } else {
                    ProfileSessionView {
                        profile: profile.clone(),
                        phase: ConnectionPhase::Saved,
                        status_text: String::new(),
                        last_error: None,
                        has_saved_token,
                        mode: AuthMode::Token,
                        pid: None,
                        snapshot: SidecarSnapshot {
                            server: profile.server.clone(),
                            ..Default::default()
                        },
                        recent_logs: Vec::new(),
                    }
                }
            })
            .collect();

        SupervisorViewState {
            appearance: guard.config.appearance,
            auto_reconnect_on_launch: guard.config.auto_reconnect_on_launch,
            selected_profile_id: guard.config.selected_profile_id.clone(),
            sessions,
            sidecar_binary: guard.sidecar_binary.clone(),
            connected_count,
            running_count,
            total_tunnels,
            total_active_streams,
        }
    }

    /// Switches the focused profile in the UI without interrupting any running sidecars.
    pub fn select_profile(&self, profile_id: &str) -> Result<()> {
        let mut guard = self.inner.lock().unwrap();
        if !guard.config.profiles.iter().any(|p| p.id == profile_id) {
            anyhow::bail!("未找到指定的服务器节点");
        }
        guard.config.selected_profile_id = Some(profile_id.to_string());
        save_config(&guard.root_dir, &guard.config)?;
        let _ = guard.notify_tx.send(SupervisorNotification::Updated);
        Ok(())
    }

    /// Updates only the display remark (`name`) of a profile without touching its
    /// live connection or persisted auth token.
    pub fn rename_profile(&self, profile_id: &str, new_name: &str) -> Result<()> {
        let mut guard = self.inner.lock().unwrap();
        let Some(profile) = guard.config.profiles.iter_mut().find(|p| p.id == profile_id) else {
            anyhow::bail!("未找到指定的服务器节点");
        };
        profile.name = new_name.trim().to_string();
        save_config(&guard.root_dir, &guard.config)?;
        let _ = guard.notify_tx.send(SupervisorNotification::Updated);
        Ok(())
    }

    /// Clears the in-memory bounded log ring buffer for one profile.
    pub fn clear_logs(&self, profile_id: &str) {
        let mut guard = self.inner.lock().unwrap();
        if let Some(rt) = guard.runtimes.get_mut(profile_id) {
            rt.recent_logs.clear();
            let _ = guard.notify_tx.send(SupervisorNotification::Updated);
        }
    }

    /// Updates an existing profile's configuration & optional Key:
    /// - If `server` changed, a non-empty `key` is REQUIRED and old local token state is wiped.
    /// - If `server` is unchanged and `key` is empty, existing persisted token auth is preserved
    ///   (requires `has_persisted_client_state` to be true).
    pub fn save_or_reconnect_edited_profile(
        &self,
        profile_id: &str,
        server: &str,
        name: &str,
        key: Option<&str>,
        tls_skip_verify: bool,
        tls_fingerprint: &str,
    ) -> Result<String> {
        let trimmed_server = server.trim().to_string();
        if trimmed_server.is_empty() {
            anyhow::bail!("服务器地址不能为空");
        }
        let trimmed_key = key.map(str::trim).filter(|s| !s.is_empty()).map(str::to_string);

        let (old_server, has_token) = {
            let guard = self.inner.lock().unwrap();
            let existing = guard
                .config
                .profiles
                .iter()
                .find(|p| p.id == profile_id)
                .cloned()
                .ok_or_else(|| anyhow::anyhow!("未找到指定的服务器节点"))?;
            let has_token = has_persisted_client_state(&guard.root_dir, profile_id);
            (existing.server, has_token)
        };

        let server_changed = !normalize_server_compare(&old_server)
            .eq_ignore_ascii_case(&normalize_server_compare(&trimmed_server));

        if server_changed && trimmed_key.is_none() {
            anyhow::bail!("更改服务器地址后，原认证凭证不可继续使用，请填写新的访问凭证 (Key)");
        }
        if !server_changed && trimmed_key.is_none() && !has_token {
            anyhow::bail!("本地尚无可用认证凭证，请填写访问凭证 (Key)");
        }

        let mode = if trimmed_key.is_some() {
            AuthMode::Key
        } else {
            AuthMode::Token
        };

        if server_changed {
            if self.is_profile_running(profile_id) {
                self.disconnect_profile(profile_id)?;
            }
            let root_dir = self.root_dir();
            clear_persisted_client_state(&root_dir, profile_id);
        }

        // If currently idle/saved and only metadata changed with empty key, just save config unless running.
        let was_running = self.is_profile_running(profile_id);
        if !was_running && trimmed_key.is_none() && !server_changed {
            let mut guard = self.inner.lock().unwrap();
            if let Some(p) = guard.config.profiles.iter_mut().find(|p| p.id == profile_id) {
                p.name = name.trim().to_string();
                p.tls_skip_verify = tls_skip_verify;
                p.tls_fingerprint = tls_fingerprint.trim().to_string();
            }
            save_config(&guard.root_dir, &guard.config)?;
            let _ = guard.notify_tx.send(SupervisorNotification::Updated);
            return Ok(profile_id.to_string());
        }

        self.connect(StartRequest {
            profile_id: profile_id.to_string(),
            name: name.trim().to_string(),
            server: trimmed_server,
            key: trimmed_key,
            mode,
            tls_skip_verify,
            tls_fingerprint: tls_fingerprint.trim().to_string(),
        })
    }

    pub fn forget_profile(&self, profile_id: &str) -> Result<()> {

        self.disconnect_profile(profile_id)?;
        let mut guard = self.inner.lock().unwrap();
        let data_dir = profile_data_dir(&guard.root_dir, profile_id);
        if data_dir.exists() {
            fs::remove_dir_all(&data_dir)
                .with_context(|| format!("remove profile data dir {}", data_dir.display()))?;
        }
        guard.runtimes.remove(profile_id);
        guard.config.remove_profile(profile_id);
        save_config(&guard.root_dir, &guard.config)?;
        let _ = guard.notify_tx.send(SupervisorNotification::Updated);
        Ok(())
    }

    /// Starts or restarts a specific profile's Go sidecar without disturbing other connected servers.
    pub fn connect(&self, req: StartRequest) -> Result<String> {
        let server = req.server.trim().to_string();
        if server.is_empty() {
            anyhow::bail!("服务器地址不能为空");
        }
        if req.mode == AuthMode::Key
            && req
                .key
                .as_deref()
                .map(str::trim)
                .unwrap_or_default()
                .is_empty()
        {
            anyhow::bail!("请输入访问凭证 (Client Key)");
        }

        let sidecar_path = resolve_sidecar_binary()?;

        // Resolve or create profile first to know its canonical ID.
        let target_id = {
            let mut guard = self.inner.lock().unwrap();
            let profile = guard.config.upsert_profile(ServerProfile {
                id: req.profile_id.clone(),
                name: req.name.clone(),
                server: server.clone(),
                tls_skip_verify: req.tls_skip_verify,
                tls_fingerprint: req.tls_fingerprint.trim().to_string(),
                auto_connect: true,
                last_connected_unix: 0,
            });
            save_config(&guard.root_dir, &guard.config)?;
            guard
                .runtimes
                .entry(profile.id.clone())
                .or_insert_with(|| ProfileRuntime::new_saved(&profile.server));
            profile.id
        };

        // Stop only THIS profile's existing sidecar if running.
        if self.is_profile_running(&target_id) {
            self.disconnect_profile(&target_id)?;
        }

        let (profile, data_dir, generation) = {
            let mut guard = self.inner.lock().unwrap();
            let profile = guard
                .config
                .profiles
                .iter()
                .find(|p| p.id == target_id)
                .cloned()
                .ok_or_else(|| anyhow::anyhow!("节点配置不存在"))?;

            let data_dir = profile_data_dir(&guard.root_dir, &profile.id);
            if req.mode == AuthMode::Key && data_dir.exists() {
                clear_persisted_client_state(&guard.root_dir, &profile.id);
            }
            fs::create_dir_all(&data_dir)
                .with_context(|| format!("create profile data dir {}", data_dir.display()))?;

            guard.sidecar_binary = Some(sidecar_path.clone());
            let notify_tx = guard.notify_tx.clone();
            let rt = guard
                .runtimes
                .entry(profile.id.clone())
                .or_insert_with(|| ProfileRuntime::new_saved(&profile.server));
            rt.generation += 1;
            let generation = rt.generation;
            rt.intentional_stop = false;
            rt.mode = req.mode;
            rt.phase = ConnectionPhase::Connecting;
            rt.status_text = match req.mode {
                AuthMode::Key => "正在验证凭证…".to_string(),
                AuthMode::Token => "正在恢复连接…".to_string(),
            };
            rt.last_error = None;
            rt.snapshot = SidecarSnapshot {
                server: profile.server.clone(),
                ..Default::default()
            };
            rt.snapshot_received_at = None;
            // Start each fresh session with a clean bounded log buffer so logs do not accumulate forever.
            rt.recent_logs.clear();
            push_log(
                &mut rt.recent_logs,
                "info",
                "desktop.connect",
                format!("启动连接 -> {}", profile.server),
            );
            let _ = notify_tx.send(SupervisorNotification::Updated);
            (profile, data_dir, generation)
        };

        let mut cmd = Command::new(&sidecar_path);
        cmd.arg("client")
            .arg("--server")
            .arg(&profile.server)
            .arg("--data-dir")
            .arg(&data_dir)
            .arg("--log-format")
            .arg("json")
            .arg("--control-stdio")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());

        if profile.tls_skip_verify {
            cmd.arg("--tls-skip-verify");
        }
        if !profile.tls_fingerprint.is_empty() {
            cmd.arg("--tls-fingerprint").arg(&profile.tls_fingerprint);
        }
        if req.mode == AuthMode::Key {
            if let Some(key) = req.key.as_deref().map(str::trim).filter(|k| !k.is_empty()) {
                cmd.env("NETSGO_KEY", key);
            }
        } else {
            cmd.env_remove("NETSGO_KEY");
        }

        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            const CREATE_NO_WINDOW: u32 = 0x0800_0000;
            cmd.creation_flags(CREATE_NO_WINDOW);
        }

        let mut child = cmd.spawn().with_context(|| {
            format!("启动 netsgo 边车失败 ({})", sidecar_path.display())
        })?;
        let pid = child.id();
        let stdin = child.stdin.take();
        let stderr = child.stderr.take();
        let stdout = child.stdout.take();

        {
            let mut guard = self.inner.lock().unwrap();
            let notify_tx = guard.notify_tx.clone();
            let Some(rt) = guard.runtimes.get_mut(&profile.id) else {
                let _ = child.kill();
                let _ = child.wait();
                return Ok(profile.id);
            };
            if rt.generation != generation || rt.intentional_stop {
                let _ = child.kill();
                let _ = child.wait();
                return Ok(profile.id);
            }
            rt.running = Some(RunningChild { pid, stdin, child });
            let _ = notify_tx.send(SupervisorNotification::Updated);
        }

        if let Some(stderr) = stderr {
            let inner = Arc::clone(&self.inner);
            let pid_key = profile.id.clone();
            thread::Builder::new()
                .name(format!("netsgo-stderr-{}", profile.id))
                .spawn(move || {
                    let reader = BufReader::new(stderr);
                    for line in reader.lines().map_while(Result::ok) {
                        handle_sidecar_output_line(&inner, &pid_key, generation, &line);
                    }
                    reap_sidecar_after_eof(&inner, &pid_key, generation);
                })
                .ok();
        }

        if let Some(stdout) = stdout {
            let inner = Arc::clone(&self.inner);
            let pid_key = profile.id.clone();
            thread::Builder::new()
                .name(format!("netsgo-stdout-{}", profile.id))
                .spawn(move || {
                    let reader = BufReader::new(stdout);
                    for line in reader.lines().map_while(Result::ok) {
                        handle_sidecar_output_line(&inner, &pid_key, generation, &line);
                    }
                })
                .ok();
        }

        Ok(profile.id)
    }

    pub fn reconnect_profile(&self, profile_id: &str) -> Result<String> {
        let profile = {
            let guard = self.inner.lock().unwrap();
            guard
                .config
                .profiles
                .iter()
                .find(|p| p.id == profile_id)
                .cloned()
                .ok_or_else(|| anyhow::anyhow!("未找到指定的服务器节点"))?
        };
        self.connect(StartRequest {
            profile_id: profile.id,
            name: profile.name,
            server: profile.server,
            key: None,
            mode: AuthMode::Token,
            tls_skip_verify: profile.tls_skip_verify,
            tls_fingerprint: profile.tls_fingerprint,
        })
    }

    /// Profiles whose sidecars should be resumed after an application update.
    pub fn running_profile_ids(&self) -> Vec<String> {
        let guard = self.inner.lock().unwrap();
        guard.runtimes.iter().filter(|(_, rt)| rt.running.is_some())
            .map(|(id, _)| id.clone()).collect()
    }

    /// Reconnects all saved profiles that have local tokens and `auto_connect` enabled.
    pub fn reconnect_all_saved(&self) {
        let ids: Vec<String> = {
            let guard = self.inner.lock().unwrap();
            guard
                .config
                .profiles
                .iter()
                .filter(|p| {
                    p.auto_connect
                        && has_persisted_client_state(&guard.root_dir, &p.id)
                        && guard
                            .runtimes
                            .get(&p.id)
                            .is_none_or(|rt| rt.running.is_none())
                })
                .map(|p| p.id.clone())
                .collect()
        };
        for id in ids {
            let _ = self.reconnect_profile(&id);
        }
    }

    pub fn request_snapshot(&self, profile_id: &str) {
        let mut guard = self.inner.lock().unwrap();
        if let Some(rt) = guard.runtimes.get_mut(profile_id) {
            if let Some(running) = rt.running.as_mut() {
                if let Some(stdin) = running.stdin.as_mut() {
                    let _ = stdin.write_all(SidecarCommand::Snapshot.to_json_line().as_bytes());
                    let _ = stdin.flush();
                }
            }
        }
    }

    pub fn request_all_snapshots(&self) {
        let mut guard = self.inner.lock().unwrap();
        for rt in guard.runtimes.values_mut() {
            if let Some(running) = rt.running.as_mut() {
                if let Some(stdin) = running.stdin.as_mut() {
                    let _ = stdin.write_all(SidecarCommand::Snapshot.to_json_line().as_bytes());
                    let _ = stdin.flush();
                }
            }
        }
    }

    pub fn is_profile_running(&self, profile_id: &str) -> bool {
        self.inner
            .lock()
            .unwrap()
            .runtimes
            .get(profile_id)
            .is_some_and(|rt| rt.running.is_some())
    }

    pub fn has_any_running(&self) -> bool {
        self.inner
            .lock()
            .unwrap()
            .runtimes
            .values()
            .any(|rt| rt.running.is_some())
    }

    pub fn disconnect_profile(&self, profile_id: &str) -> Result<()> {
        let mut running_child = {
            let mut guard = self.inner.lock().unwrap();
            let notify_tx = guard.notify_tx.clone();
            if let Some(p) = guard.config.profiles.iter_mut().find(|p| p.id == profile_id) {
                p.auto_connect = false;
                let _ = save_config(&guard.root_dir, &guard.config);
            }
            let Some(rt) = guard.runtimes.get_mut(profile_id) else {
                return Ok(());
            };
            rt.intentional_stop = true;
            rt.generation += 1;
            let Some(running) = rt.running.take() else {
                rt.phase = ConnectionPhase::Saved;
                rt.status_text.clear();
                let _ = notify_tx.send(SupervisorNotification::Updated);
                return Ok(());
            };
            rt.phase = ConnectionPhase::Disconnecting;
            rt.status_text = "正在断开连接…".to_string();
            let _ = notify_tx.send(SupervisorNotification::Updated);
            running
        };

        stop_child_gracefully(&mut running_child);

        {
            let mut guard = self.inner.lock().unwrap();
            let notify_tx = guard.notify_tx.clone();
            if let Some(rt) = guard.runtimes.get_mut(profile_id) {
                rt.snapshot.connected = false;
                rt.snapshot.tunnels.clear();
                rt.snapshot.active_connections = 0;
                rt.phase = ConnectionPhase::Saved;
                rt.status_text.clear();
                push_log(&mut rt.recent_logs, "info", "desktop.disconnected", "已断开连接");
            }
            let _ = notify_tx.send(SupervisorNotification::Updated);
        }

        Ok(())
    }

    /// Gracefully stops all active sidecars (used on app quit or tray "disconnect all").
    pub fn disconnect_all(&self) -> Result<()> {
        let children: Vec<(String, RunningChild)> = {
            let mut guard = self.inner.lock().unwrap();
            let notify_tx = guard.notify_tx.clone();
            let mut taken = Vec::new();
            for (id, rt) in guard.runtimes.iter_mut() {
                rt.intentional_stop = true;
                rt.generation += 1;
                if let Some(running) = rt.running.take() {
                    rt.phase = ConnectionPhase::Disconnecting;
                    rt.status_text = "正在断开连接…".to_string();
                    taken.push((id.clone(), running));
                }
            }
            if !taken.is_empty() {
                let _ = notify_tx.send(SupervisorNotification::Updated);
            }
            taken
        };

        let handles: Vec<_> = children
            .into_iter()
            .map(|(id, mut child)| {
                thread::spawn(move || {
                    stop_child_gracefully(&mut child);
                    id
                })
            })
            .collect();

        let stopped_ids: Vec<String> = handles
            .into_iter()
            .filter_map(|h| h.join().ok())
            .collect();

        if !stopped_ids.is_empty() {
            let mut guard = self.inner.lock().unwrap();
            let notify_tx = guard.notify_tx.clone();
            for id in stopped_ids {
                if let Some(rt) = guard.runtimes.get_mut(&id) {
                    rt.snapshot.connected = false;
                    rt.snapshot.tunnels.clear();
                    rt.snapshot.active_connections = 0;
                    rt.phase = ConnectionPhase::Saved;
                    rt.status_text.clear();
                    push_log(&mut rt.recent_logs, "info", "desktop.disconnected", "已断开连接");
                }
            }
            let _ = notify_tx.send(SupervisorNotification::Updated);
        }

        Ok(())
    }
}

fn stop_child_gracefully(running_child: &mut RunningChild) {
    if let Some(mut stdin) = running_child.stdin.take() {
        let _ = stdin.write_all(SidecarCommand::Shutdown.to_json_line().as_bytes());
        let _ = stdin.flush();
        drop(stdin);
    }

    let deadline = std::time::Instant::now() + Duration::from_millis(1800);
    loop {
        match running_child.child.try_wait() {
            Ok(Some(_)) => break,
            Ok(None) => {
                if std::time::Instant::now() >= deadline {
                    let _ = running_child.child.kill();
                    let _ = running_child.child.wait();
                    break;
                }
                thread::sleep(Duration::from_millis(30));
            }
            Err(_) => break,
        }
    }
}

impl Drop for ClientSupervisor {
    fn drop(&mut self) {
        if Arc::strong_count(&self.inner) == 1 {
            let _ = self.disconnect_all();
        }
    }
}

fn handle_sidecar_output_line(
    inner: &Arc<Mutex<SupervisorInner>>,
    profile_id: &str,
    generation: u64,
    line: &str,
) {
    let parsed = ClientLogEvent::parse_line(line);
    let mut guard = inner.lock().unwrap();
    let notify_tx = guard.notify_tx.clone();
    let root_dir = guard.root_dir.clone();

    let Some(rt) = guard.runtimes.get_mut(profile_id) else {
        return;
    };
    if rt.generation != generation {
        return;
    }

    let Some(ev) = parsed else {
        if !line.trim().is_empty() {
            push_log(&mut rt.recent_logs, "info", "sidecar.raw", line.trim());
            let _ = notify_tx.send(SupervisorNotification::Updated);
        }
        return;
    };

    if let Some(snap) = ev.extract_snapshot() {
        rt.snapshot = snap;
        rt.snapshot_received_at = Some(Instant::now());
        if rt.snapshot.connected {
            rt.phase = ConnectionPhase::Connected;
            rt.status_text.clear();
            rt.last_error = None;
        }
        let _ = notify_tx.send(SupervisorNotification::Updated);
        return;
    }

    push_log(
        &mut rt.recent_logs,
        &ev.level,
        &ev.event,
        if ev.message.is_empty() {
            line.trim().to_string()
        } else {
            ev.message.clone()
        },
    );

    let mut mark_connected_now = false;
    match ev.event.as_str() {
        "client.connecting" => {
            rt.phase = ConnectionPhase::Connecting;
            rt.status_text = "正在连接控制通道…".to_string();
        }
        "client.connected" => {
            rt.phase = ConnectionPhase::Connecting;
            rt.status_text = "正在验证身份…".to_string();
        }
        "client.authenticated" | "client.token_auth_succeeded" => {
            rt.phase = ConnectionPhase::Connecting;
            rt.status_text = "正在建立数据通道…".to_string();
            if let Some(cid) = ev.field_str("client_id") {
                rt.snapshot.client_id = cid.to_string();
            }
        }
        "client.data_channel_established" => {
            rt.phase = ConnectionPhase::Connected;
            rt.status_text.clear();
            rt.last_error = None;
            rt.snapshot.connected = true;
            mark_connected_now = true;
        }
        "client.connection_lost" => {
            rt.snapshot.connected = false;
            rt.phase = ConnectionPhase::Reconnecting;
            rt.status_text = "连接中断，自动重连中…".to_string();
        }
        "client.reconnecting" => {
            rt.snapshot.connected = false;
            rt.phase = ConnectionPhase::Reconnecting;
            if let Some(ms) = ev.field_i64("delay_ms") {
                let secs = (ms as f64 / 1000.0).max(0.5);
                rt.status_text = format!("{secs:.1}s 后自动重连…");
            } else {
                rt.status_text = "等待重连…".to_string();
            }
        }
        "client.reconnect_attempt" => {
            rt.phase = ConnectionPhase::Reconnecting;
            rt.status_text = "正在重连服务器…".to_string();
        }
        "client.auth_failed"
        | "client.data_channel_failed"
        | "client.token_clear_failed"
        | "client.fatal" => {
            rt.last_error = Some(format_client_event_error(&ev, rt.mode));
        }
        _ => {
            if ev.level == "error" {
                rt.last_error = Some(format_client_event_error(&ev, rt.mode));
            }
        }
    }

    if mark_connected_now {
        let now = now_unix_secs();
        if let Some(p) = guard.config.profiles.iter_mut().find(|p| p.id == profile_id) {
            p.last_connected_unix = now;
            p.auto_connect = true;
        }
        let _ = save_config(&root_dir, &guard.config);
    }

    let _ = notify_tx.send(SupervisorNotification::Updated);
}

fn reap_sidecar_after_eof(
    inner: &Arc<Mutex<SupervisorInner>>,
    profile_id: &str,
    generation: u64,
) {
    let exit_code = {
        let mut guard = inner.lock().unwrap();
        let Some(rt) = guard.runtimes.get_mut(profile_id) else {
            return;
        };
        if rt.generation != generation {
            return;
        }
        rt.running
            .as_mut()
            .and_then(|r| r.child.wait().ok())
            .and_then(|s| s.code())
    };

    let mut guard = inner.lock().unwrap();
    let notify_tx = guard.notify_tx.clone();
    let profile_title = guard
        .config
        .profiles
        .iter()
        .find(|p| p.id == profile_id)
        .map(|p| p.display_title().to_string())
        .unwrap_or_else(|| profile_id.to_string());

    let Some(rt) = guard.runtimes.get_mut(profile_id) else {
        return;
    };
    if rt.generation != generation {
        return;
    }
    rt.running = None;
    rt.snapshot.connected = false;
    rt.snapshot.tunnels.clear();
    rt.snapshot.active_connections = 0;

    if rt.intentional_stop {
        rt.phase = ConnectionPhase::Saved;
        rt.status_text.clear();
        let _ = notify_tx.send(SupervisorNotification::Updated);
    } else if exit_code.unwrap_or(1) != 0 || rt.last_error.is_some() {
        rt.phase = ConnectionPhase::Error;
        let err = rt
            .last_error
            .clone()
            .unwrap_or_else(|| normalize_error_message("连接已退出，请检查服务器地址与凭证"));
        rt.status_text.clear();
        rt.last_error = Some(err.clone());
        let _ = notify_tx.send(SupervisorNotification::SessionError {
            profile_title,
            message: err,
        });
    } else {
        rt.phase = ConnectionPhase::Saved;
        rt.status_text.clear();
        let _ = notify_tx.send(SupervisorNotification::Updated);
    }
}

pub fn default_desktop_data_dir() -> PathBuf {
    if let Ok(explicit) = env::var("NETSGO_DESKTOP_DATA_DIR") {
        if !explicit.trim().is_empty() {
            return PathBuf::from(explicit);
        }
    }
    dirs::data_local_dir()
        .or_else(dirs::home_dir)
        .unwrap_or_else(|| PathBuf::from("."))
        .join("netsgo-desktop")
}

pub fn profile_data_dir(root_dir: &Path, profile_id: &str) -> PathBuf {
    root_dir.join("profiles").join(profile_id)
}

/// Matches `internal/client/state.go`: Go client stores its SQLite identity &
/// exchanged token at `<data_dir>/client/netsgo.db` (or legacy `client/client.json`).
pub fn has_persisted_client_state(root_dir: &Path, profile_id: &str) -> bool {
    let base = profile_data_dir(root_dir, profile_id);
    base.join("client").join("netsgo.db").is_file()
        || base.join("client").join("client.json").is_file()
        || base.join("netsgo.db").is_file()
}

fn clear_persisted_client_state(root_dir: &Path, profile_id: &str) {
    let data_dir = profile_data_dir(root_dir, profile_id);
    let client_dir = data_dir.join("client");
    for path in [
        client_dir.join("netsgo.db"),
        client_dir.join("netsgo.db-wal"),
        client_dir.join("netsgo.db-shm"),
        client_dir.join("client.json"),
        data_dir.join("netsgo.db"),
        data_dir.join("netsgo.db-wal"),
        data_dir.join("netsgo.db-shm"),
    ] {
        let _ = fs::remove_file(path);
    }
}

pub fn normalize_server_compare(server: &str) -> String {
    server.trim().trim_end_matches('/').to_string()
}

fn config_file_path(root_dir: &Path) -> PathBuf {
    root_dir.join("desktop-config.json")
}

fn save_config(root_dir: &Path, cfg: &DesktopConfig) -> Result<()> {
    fs::create_dir_all(root_dir)?;
    let path = config_file_path(root_dir);
    let tmp = root_dir.join("desktop-config.json.tmp");
    let content = serde_json::to_string_pretty(cfg)?;
    fs::write(&tmp, content)?;
    fs::rename(&tmp, &path)?;
    Ok(())
}

pub fn resolve_sidecar_binary() -> Result<PathBuf> {
    if let Ok(override_bin) = env::var("NETSGO_SIDECAR_BIN") {
        let p = PathBuf::from(override_bin.trim());
        if p.is_file() {
            return Ok(p);
        }
    }

    let triple = current_target_triple();
    let ext = if cfg!(windows) { ".exe" } else { "" };
    let plain_name = format!("netsgo{ext}");
    let triple_name = format!("netsgo-{triple}{ext}");

    let mut candidates = Vec::new();

    if let Ok(exe) = env::current_exe() {
        if let Some(exe_dir) = exe.parent() {
            candidates.push(exe_dir.join(&plain_name));
            candidates.push(exe_dir.join(&triple_name));
            candidates.push(exe_dir.join("../Resources").join(&plain_name));
            candidates.push(exe_dir.join("../Resources").join(&triple_name));
        }
    }

    let manifest_root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..");
    candidates.push(manifest_root.join("binaries").join(&triple_name));
    candidates.push(manifest_root.join("binaries").join(&plain_name));
    candidates.push(manifest_root.join("../bin").join(&plain_name));
    candidates.push(
        manifest_root
            .join("../desktop/src-tauri/binaries")
            .join(&triple_name),
    );

    for candidate in &candidates {
        if candidate.is_file() {
            return Ok(candidate.canonicalize().unwrap_or_else(|_| candidate.clone()));
        }
    }

    let tried = candidates
        .iter()
        .map(|p| p.display().to_string())
        .collect::<Vec<_>>()
        .join(", ");
    anyhow::bail!(
        "未找到 netsgo 边车二进制（请先运行 `make build-client-desktop-sidecar`）。已尝试路径: {tried}"
    )
}

pub fn current_target_triple() -> &'static str {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        "aarch64-apple-darwin"
    }
    #[cfg(all(target_os = "macos", target_arch = "x86_64"))]
    {
        "x86_64-apple-darwin"
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        "x86_64-pc-windows-msvc"
    }
    #[cfg(all(target_os = "windows", target_arch = "aarch64"))]
    {
        "aarch64-pc-windows-msvc"
    }
    #[cfg(all(target_os = "linux", target_arch = "x86_64"))]
    {
        "x86_64-unknown-linux-gnu"
    }
    #[cfg(all(target_os = "linux", target_arch = "aarch64"))]
    {
        "aarch64-unknown-linux-gnu"
    }
}

fn push_log(buf: &mut VecDeque<LogLine>, level: &str, event: &str, message: impl Into<String>) {
    let raw_msg = message.into();
    let mut clean_msg: String = raw_msg.chars().take(MAX_LOG_MESSAGE_CHARS).collect();
    if raw_msg.chars().count() > MAX_LOG_MESSAGE_CHARS {
        clean_msg.push('…');
    }

    // Coalesce consecutive identical noisy events (e.g. repeated reconnect backoff ticks).
    if let Some(back) = buf.back_mut() {
        if back.event == event && back.message == clean_msg {
            let secs = now_unix_secs() % 86_400;
            back.timestamp = format!(
                "{:02}:{:02}:{:02}",
                secs / 3600,
                (secs % 3600) / 60,
                secs % 60
            );
            return;
        }
    }

    while buf.len() >= MAX_RECENT_LOGS {
        buf.pop_front();
    }
    let secs = now_unix_secs() % 86_400;
    let h = secs / 3600;
    let m = (secs % 3600) / 60;
    let s = secs % 60;
    buf.push_back(LogLine {
        timestamp: format!("{h:02}:{m:02}:{s:02}"),
        level: level.to_string(),
        event: event.to_string(),
        message: clean_msg,
    });
}

fn now_unix_secs() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn uptime_advances_between_snapshots_and_resets_for_new_process() {
        let dir = tempdir().unwrap();
        let mut cfg = DesktopConfig::new_default();
        let profile = cfg.upsert_profile(ServerProfile {
            id: "uptime-node".to_string(),
            name: String::new(),
            server: "http://127.0.0.1:9527".to_string(),
            tls_skip_verify: false,
            tls_fingerprint: String::new(),
            auto_connect: false,
            last_connected_unix: 0,
        });
        save_config(dir.path(), &cfg).unwrap();
        let (sup, _rx) = ClientSupervisor::new(dir.path().to_path_buf()).unwrap();

        let snapshot_line = r#"{"event":"client.runtime_snapshot","fields":{"snapshot":{"connected":true,"uptime_seconds":0}}}"#;
        handle_sidecar_output_line(&sup.inner, &profile.id, 0, snapshot_line);
        {
            let mut guard = sup.inner.lock().unwrap();
            let rt = guard.runtimes.get_mut(&profile.id).unwrap();
            let received_at = rt.snapshot_received_at.unwrap();
            assert_eq!(rt.uptime_seconds(received_at), 0);
            assert_eq!(rt.uptime_seconds(received_at + Duration::from_secs(65)), 65);

            // A network reconnect keeps the same sidecar process and its uptime.
            rt.phase = ConnectionPhase::Reconnecting;
            assert_eq!(rt.uptime_seconds(received_at + Duration::from_secs(125)), 125);
            rt.phase = ConnectionPhase::Saved;
            assert_eq!(rt.uptime_seconds(received_at + Duration::from_secs(200)), 0);
        }

        // A fresh sidecar snapshot replaces the previous process's clock anchor.
        handle_sidecar_output_line(&sup.inner, &profile.id, 0, snapshot_line);
        let guard = sup.inner.lock().unwrap();
        let rt = guard.runtimes.get(&profile.id).unwrap();
        assert_eq!(
            rt.uptime_seconds(rt.snapshot_received_at.unwrap() + Duration::from_secs(2)),
            2
        );
    }

    #[test]
    fn session_view_includes_elapsed_time_since_last_snapshot() {
        let dir = tempdir().unwrap();
        let (sup, _rx) = ClientSupervisor::new(dir.path().to_path_buf()).unwrap();
        {
            let mut guard = sup.inner.lock().unwrap();
            let profile = guard.config.upsert_profile(ServerProfile {
                id: "uptime-node".to_string(),
                name: String::new(),
                server: "http://127.0.0.1:9527".to_string(),
                tls_skip_verify: false,
                tls_fingerprint: String::new(),
                auto_connect: false,
                last_connected_unix: 0,
            });
            let mut rt = ProfileRuntime::new_saved(&profile.server);
            rt.phase = ConnectionPhase::Connected;
            rt.snapshot.uptime_seconds = 10;
            rt.snapshot_received_at = Some(Instant::now() - Duration::from_secs(5));
            guard.runtimes.insert(profile.id, rt);
        }
        let state = sup.view_state();
        assert!(state.selected_session().unwrap().snapshot.uptime_seconds >= 15);
    }

    #[test]
    fn multi_profile_upsert_select_and_independent_runtimes() {
        let dir = tempdir().unwrap();
        let mut cfg = DesktopConfig::new_default();
        let p1 = cfg.upsert_profile(ServerProfile {
            id: String::new(),
            name: "HK Edge".to_string(),
            server: "https://hk.example.com:9527".to_string(),
            tls_skip_verify: false,
            tls_fingerprint: String::new(),
            auto_connect: true,
            last_connected_unix: 100,
        });
        let p2 = cfg.upsert_profile(ServerProfile {
            id: String::new(),
            name: "Tokyo Core".to_string(),
            server: "https://tyo.example.com:9527".to_string(),
            tls_skip_verify: false,
            tls_fingerprint: String::new(),
            auto_connect: true,
            last_connected_unix: 200,
        });
        save_config(dir.path(), &cfg).unwrap();

        let (sup, _rx) = ClientSupervisor::new(dir.path().to_path_buf()).unwrap();
        {
            let mut g = sup.inner.lock().unwrap();
            g.runtimes.get_mut(&p1.id).unwrap().generation = 1;
            g.runtimes.get_mut(&p2.id).unwrap().generation = 1;
        }

        // Simulate both p1 and p2 connected simultaneously.
        handle_sidecar_output_line(
            &sup.inner,
            &p1.id,
            1,
            r#"{"level":"info","event":"client.data_channel_established","message":"Ready"}"#,
        );
        handle_sidecar_output_line(
            &sup.inner,
            &p2.id,
            1,
            r#"{"level":"info","event":"client.data_channel_established","message":"Ready"}"#,
        );

        let state = sup.view_state();
        assert_eq!(state.connected_count, 2);
        assert_eq!(state.sessions.len(), 2);

        // Selecting p1 does not disconnect p2.
        sup.select_profile(&p1.id).unwrap();
        let state_after = sup.view_state();
        assert_eq!(state_after.connected_count, 2);
        assert_eq!(
            state_after.selected_session().unwrap().profile.id,
            p1.id
        );
    }

    #[test]
    fn spawns_multiple_concurrent_go_sidecars_cleanly() {
        if resolve_sidecar_binary().is_err() {
            return;
        }
        let dir = tempdir().unwrap();
        let (sup, _rx) = ClientSupervisor::new(dir.path().to_path_buf()).unwrap();

        let id1 = sup
            .connect(StartRequest {
                profile_id: "srv-a".into(),
                name: "Node A".into(),
                server: "http://127.0.0.1:1".into(),
                key: Some("key-a".into()),
                mode: AuthMode::Key,
                tls_skip_verify: false,
                tls_fingerprint: String::new(),
            })
            .unwrap();

        let id2 = sup
            .connect(StartRequest {
                profile_id: "srv-b".into(),
                name: "Node B".into(),
                server: "http://127.0.0.1:2".into(),
                key: Some("key-b".into()),
                mode: AuthMode::Key,
                tls_skip_verify: false,
                tls_fingerprint: String::new(),
            })
            .unwrap();

        assert_ne!(id1, id2);
        thread::sleep(Duration::from_millis(120));
        sup.disconnect_all().unwrap();
        assert!(!sup.has_any_running());
    }

    #[test]
    fn detects_go_client_subdir_sqlite_db_for_switch_reconnect() {
        let dir = tempdir().unwrap();
        let mut cfg = DesktopConfig::new_default();
        let p = cfg.upsert_profile(ServerProfile {
            id: String::new(),
            name: String::new(),
            server: "https://netsgo.iuv.me".to_string(),
            tls_skip_verify: false,
            tls_fingerprint: String::new(),
            auto_connect: false,
            last_connected_unix: 100,
        });
        save_config(dir.path(), &cfg).unwrap();

        // Go client writes `<data_dir>/client/netsgo.db`
        let go_client_dir = profile_data_dir(dir.path(), &p.id).join("client");
        fs::create_dir_all(&go_client_dir).unwrap();
        fs::write(go_client_dir.join("netsgo.db"), b"sqlite").unwrap();

        let (sup, _rx) = ClientSupervisor::new(dir.path().to_path_buf()).unwrap();
        let view = sup.view_state();
        let session = view.selected_session().expect("session exists");
        assert!(
            session.has_saved_token,
            "must detect Go client DB under <profile_dir>/client/netsgo.db"
        );
    }

    #[test]
    fn edit_profile_rejects_empty_key_when_server_url_changes() {
        let dir = tempdir().unwrap();
        let mut cfg = DesktopConfig::new_default();
        let p = cfg.upsert_profile(ServerProfile {
            id: "node-1".to_string(),
            name: "Office".to_string(),
            server: "https://old.example.com".to_string(),
            tls_skip_verify: false,
            tls_fingerprint: String::new(),
            auto_connect: false,
            last_connected_unix: 100,
        });
        save_config(dir.path(), &cfg).unwrap();
        let go_client_dir = profile_data_dir(dir.path(), &p.id).join("client");
        fs::create_dir_all(&go_client_dir).unwrap();
        fs::write(go_client_dir.join("netsgo.db"), b"sqlite").unwrap();

        let (sup, _rx) = ClientSupervisor::new(dir.path().to_path_buf()).unwrap();

        // Changing server URL without providing a new Key must fail.
        let err = sup
            .save_or_reconnect_edited_profile(
                &p.id,
                "https://new.example.com",
                "Office",
                None,
                false,
                "",
            )
            .unwrap_err();
        assert!(err.to_string().contains("更改服务器地址后"));

        // Inline rename works without touching connection or requiring Key.
        sup.rename_profile(&p.id, "新备注").unwrap();
        assert_eq!(
            sup.view_state().selected_session().unwrap().profile.name,
            "新备注"
        );
    }
}
