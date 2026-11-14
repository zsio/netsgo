use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// Control commands written as line-delimited JSON to `netsgo client --control-stdio` stdin.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "cmd", rename_all = "snake_case")]
pub enum SidecarCommand {
    Snapshot,
    Shutdown,
    Ping,
}

impl SidecarCommand {
    pub fn to_json_line(&self) -> String {
        let mut line = serde_json::to_string(self).expect("SidecarCommand serializes cleanly");
        line.push('\n');
        line
    }
}

/// Structured JSON log/event emitted by `netsgo client --log-format json` on stderr.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ClientLogEvent {
    #[serde(default)]
    pub time: String,
    #[serde(default)]
    pub level: String,
    #[serde(default)]
    pub event: String,
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub fields: BTreeMap<String, Value>,
}

/// Live link-layer and tunnel runtime snapshot emitted under `client.runtime_snapshot`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct SidecarSnapshot {
    #[serde(default)]
    pub server: String,
    #[serde(default)]
    pub client_id: String,
    #[serde(default)]
    pub connected: bool,
    #[serde(default)]
    pub active_connections: i64,
    #[serde(default)]
    pub max_connections: i64,
    #[serde(default)]
    pub public_ipv4: String,
    #[serde(default)]
    pub public_ipv6: String,
    #[serde(default)]
    pub uptime_seconds: i64,
    #[serde(default)]
    pub tunnels: Vec<SidecarTunnelSnapshot>,
}

/// Summary of a single provisioned tunnel participant on this client.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct SidecarTunnelSnapshot {
    #[serde(default)]
    pub tunnel_id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub revision: i64,
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub topology: String,
    #[serde(default)]
    pub ingress_type: String,
    #[serde(default)]
    pub target_type: String,
    #[serde(default)]
    pub local_endpoint: String,
    #[serde(default)]
    pub transport_policy: String,
    #[serde(default)]
    pub effective_path: String,
    #[serde(default)]
    pub active_connections: i64,
}

impl SidecarTunnelSnapshot {
    pub fn display_name(&self) -> &str {
        if !self.name.trim().is_empty() {
            &self.name
        } else {
            &self.tunnel_id
        }
    }

    pub fn role_badge(&self) -> &'static str {
        match self.role.as_str() {
            "ingress" => "本地入口 (Ingress)",
            "target" => "目标服务 (Target)",
            _ => "参与者",
        }
    }

    pub fn path_badge(&self) -> &'static str {
        match self.effective_path.as_str() {
            "peer_direct" => "P2P 直连",
            "server_relay" => "Server 中继",
            "turn_relay" => "TURN 中继",
            _ => "协商中",
        }
    }

    pub fn endpoint_kind_label(&self) -> &'static str {
        match (self.role.as_str(), self.ingress_type.as_str(), self.target_type.as_str()) {
            ("ingress", "socks5_listen", _) => "SOCKS5 入口",
            ("ingress", "udp_listen", _) => "UDP 入口",
            ("ingress", "tcp_listen", _) => "TCP 入口",
            (_, "http_host", _) => "HTTP 域名",
            (_, "tls_sni", _) => "TLS SNI",
            (_, _, "socks5_connect_handler") => "SOCKS5 出口",
            (_, _, "udp_service") | (_, _, "udp") => "UDP 服务",
            (_, _, "tcp_service") | (_, _, "tcp") => "TCP 服务",
            _ => "隧道",
        }
    }
}

impl ClientLogEvent {
    pub fn parse_line(line: &str) -> Option<Self> {
        let trimmed = line.trim();
        if trimmed.is_empty() {
            return None;
        }
        serde_json::from_str::<Self>(trimmed).ok()
    }

    pub fn extract_snapshot(&self) -> Option<SidecarSnapshot> {
        if self.event != "client.runtime_snapshot" {
            return None;
        }
        let raw = self.fields.get("snapshot")?;
        serde_json::from_value::<SidecarSnapshot>(raw.clone()).ok()
    }

    pub fn field_str(&self, key: &str) -> Option<&str> {
        self.fields.get(key).and_then(Value::as_str)
    }

    pub fn field_i64(&self, key: &str) -> Option<i64> {
        self.fields.get(key).and_then(Value::as_i64)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AuthMode {
    Key,
    Token,
}

/// Maps structured Go client auth/connection errors into user-facing Chinese messages.
pub fn format_client_event_error(event: &ClientLogEvent, mode: AuthMode) -> String {
    if let Some(code) = event.field_str("code") {
        if let Some(mapped) = map_auth_error_code(code, mode) {
            return mapped.to_string();
        }
    }

    let raw = event
        .field_str("error")
        .or_else(|| event.field_str("message"))
        .filter(|s| !s.trim().is_empty())
        .unwrap_or_else(|| {
            if !event.message.trim().is_empty() {
                event.message.as_str()
            } else if !event.event.trim().is_empty() {
                event.event.as_str()
            } else {
                "未知错误"
            }
        });

    normalize_error_message(raw)
}

pub fn map_auth_error_code(code: &str, mode: AuthMode) -> Option<&'static str> {
    match code {
        "invalid_key" => Some(match mode {
            AuthMode::Token => "本地登录状态不可用，请重新输入 Key",
            AuthMode::Key => "Key 无效，请检查访问凭证",
        }),
        "invalid_token" | "expired_token" => Some("本地登录凭证已失效，请重新输入 Key"),
        "revoked_token" => Some("本地登录凭证已被服务端撤销，请重新输入 Key"),
        "concurrent_session" => Some("该客户端身份已在线，请先断开其他连接"),
        "rate_limited" => Some("认证尝试过于频繁，请稍后再试"),
        "server_uninitialized" => Some("服务器尚未完成初始化"),
        "disabled_key" => Some("该访问 Key 已被禁用"),
        "expired_key" => Some("该访问 Key 已过期"),
        "max_uses_exceeded" => Some("该访问 Key 已达到最大使用次数"),
        "user_disabled" => Some("所属账户已被禁用"),
        _ => None,
    }
}

pub fn normalize_error_message(message: &str) -> String {
    let lower = message.to_lowercase();
    if lower.contains("failed to connect to server") {
        return "无法连接服务器，请检查地址和网络".to_string();
    }
    if lower.contains("tls certificate fingerprint") {
        return "服务器 TLS 证书指纹校验失败".to_string();
    }
    if lower.contains("authentication failed") {
        return "认证失败，请检查访问凭证".to_string();
    }
    if lower.contains("failed to establish data channel") {
        return "数据通道建立失败，请稍后重试".to_string();
    }
    if lower.contains("failed to acquire client singleton lock") || lower.contains("获取锁失败") {
        return "当前 Profile 已有 NetsGo 客户端正在运行".to_string();
    }
    message.to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_runtime_snapshot_event() {
        let line = r#"{"time":"2026-11-12T00:00:00Z","level":"info","event":"client.runtime_snapshot","message":"updated","fields":{"reason":"connected","snapshot":{"server":"https://example.com","client_id":"cli_1","connected":true,"active_connections":3,"max_connections":10000,"tunnels":[{"tunnel_id":"t1","name":"ssh","role":"ingress","ingress_type":"tcp_listen","target_type":"tcp_service","local_endpoint":"127.0.0.1:2222","effective_path":"peer_direct","active_connections":1}]}}}"#;
        let ev = ClientLogEvent::parse_line(line).expect("valid JSON line");
        let snap = ev.extract_snapshot().expect("snapshot extracted");
        assert!(snap.connected);
        assert_eq!(snap.client_id, "cli_1");
        assert_eq!(snap.tunnels.len(), 1);
        assert_eq!(snap.tunnels[0].path_badge(), "P2P 直连");
        assert_eq!(snap.tunnels[0].endpoint_kind_label(), "TCP 入口");
    }

    #[test]
    fn formats_auth_error_codes() {
        let line = r#"{"level":"warn","event":"client.auth_failed","message":"Authentication failed","fields":{"code":"revoked_token"}}"#;
        let ev = ClientLogEvent::parse_line(line).unwrap();
        assert_eq!(
            format_client_event_error(&ev, AuthMode::Token),
            "本地登录凭证已被服务端撤销，请重新输入 Key"
        );
    }

    #[test]
    fn serializes_sidecar_commands() {
        assert_eq!(
            SidecarCommand::Shutdown.to_json_line(),
            "{\"cmd\":\"shutdown\"}\n"
        );
        assert_eq!(
            SidecarCommand::Snapshot.to_json_line(),
            "{\"cmd\":\"snapshot\"}\n"
        );
    }
}
