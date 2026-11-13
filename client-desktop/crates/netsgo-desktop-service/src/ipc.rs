use anyhow::{bail, Context, Result};
use netsgo_client_core::{
    AppearancePreference, StartRequest, SupervisorNotification, SupervisorViewState,
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    io::{BufRead, BufReader, Read, Write},
    net::{Ipv4Addr, SocketAddr, TcpStream},
    path::{Path, PathBuf},
    time::Duration,
};

pub const SCHEMA: u32 = 1;
pub const MAX_FRAME: u64 = 16 * 1024 * 1024;
pub const ENDPOINT_FILE: &str = "agent.endpoint.json";

#[derive(Clone, Serialize, Deserialize)]
pub struct Endpoint {
    pub port: u16,
    pub token: String,
    pub pid: u32,
}

#[derive(Clone, Serialize, Deserialize)]
pub struct EditProfile {
    pub id: String,
    pub server: String,
    pub name: String,
    pub key: Option<String>,
    pub tls_skip_verify: bool,
    pub tls_fingerprint: String,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(tag = "op", content = "args", rename_all = "snake_case")]
pub enum Command {
    State,
    Appearance(AppearancePreference),
    Select(String),
    Rename { id: String, name: String },
    ClearLogs(String),
    Connect(StartRequest),
    Edit(EditProfile),
    Forget(String),
    Reconnect(String),
    ReconnectAll,
    Snapshot(Option<String>),
    Disconnect(Option<String>),
}

#[derive(Serialize, Deserialize)]
pub struct Request {
    pub schema: u32,
    pub token: String,
    pub command: Command,
}

#[derive(Serialize, Deserialize)]
pub struct Reply {
    pub schema: u32,
    pub version: String,
    pub state: Option<SupervisorViewState>,
    pub notifications: Vec<SupervisorNotification>,
    pub value: Value,
    pub error: Option<String>,
}

pub fn endpoint_path(data: &Path) -> PathBuf {
    data.join(ENDPOINT_FILE)
}

pub fn read_frame<T: serde::de::DeserializeOwned>(stream: &mut TcpStream) -> Result<T> {
    read_frame_limited(stream, MAX_FRAME)
}
pub fn read_frame_limited<T: serde::de::DeserializeOwned>(
    stream: &mut TcpStream,
    limit: u64,
) -> Result<T> {
    let mut bytes = Vec::new();
    BufReader::new(stream.take(limit + 1)).read_until(b'\n', &mut bytes)?;
    if bytes.is_empty() || bytes.len() as u64 > limit || bytes.last() != Some(&b'\n') {
        bail!("后台服务消息为空或超出限制");
    }
    Ok(serde_json::from_slice(&bytes).context("后台服务消息格式错误")?)
}

pub fn write_frame<T: Serialize>(stream: &mut TcpStream, value: &T) -> Result<()> {
    let mut bytes = serde_json::to_vec(value)?;
    if bytes.len() as u64 >= MAX_FRAME {
        bail!("后台服务消息超出限制");
    }
    bytes.push(b'\n');
    stream.write_all(&bytes)?;
    stream.flush()?;
    Ok(())
}

pub fn call(data: &Path, command: Command) -> Result<Reply> {
    let endpoint: Endpoint =
        serde_json::from_slice(&std::fs::read(endpoint_path(data)).context("后台服务尚未启动")?)
            .context("后台服务连接文件无效")?;
    if endpoint.token.len() != 64 || !endpoint.token.bytes().all(|c| c.is_ascii_hexdigit()) {
        bail!("后台服务认证配置无效");
    }
    let mut socket = TcpStream::connect_timeout(
        &SocketAddr::from((Ipv4Addr::LOCALHOST, endpoint.port)),
        Duration::from_secs(1),
    )
    .context("无法连接后台服务，请在启动设置中修复或停用服务")?;
    let timeout = if matches!(command, Command::State) {
        Duration::from_secs(2)
    } else {
        Duration::from_secs(30)
    };
    socket.set_read_timeout(Some(timeout))?;
    socket.set_write_timeout(Some(Duration::from_secs(2)))?;
    write_frame(
        &mut socket,
        &Request {
            schema: SCHEMA,
            token: endpoint.token,
            command,
        },
    )?;
    let reply: Reply = read_frame(&mut socket)?;
    if reply.schema != SCHEMA {
        bail!("后台服务协议不兼容，请更新后台服务");
    }
    if let Some(error) = &reply.error {
        bail!("{error}");
    }
    Ok(reply)
}
