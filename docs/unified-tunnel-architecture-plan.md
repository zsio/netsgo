# NetsGo 统一隧道架构最终规范：Ingress + Target + Transport

> 状态：一步到位架构规范草案  
> 目标读者：后续实现统一隧道模型、端到端隧道、P2P 直连以及未来隧道类型的开发者  
> 重要前提：当前暂不考虑旧 API、旧 payload、旧 Client 协议兼容。新架构直接以 `TunnelSpec` 作为唯一核心模型。  
> 背景参考：`docs/p2p-plan.md`  
> 完成口径：本文允许代码按依赖顺序落地，但最终交付不能只完成“模型”或“中继”。端到端隧道必须支持 `server_relay_only`、`direct_preferred`、`direct_only` 三种流量策略后，才能称为本规划完成。

## 1. 总原则

NetsGo 的隧道应统一抽象为：

```text
Tunnel = Ingress + Target + Transport
```

通俗解释：

```text
流量从哪里进来？
最终到哪里去？
中间怎么走？
```

对应三个核心对象：

1. **Ingress**：入口。描述流量在哪里进入 NetsGo，以及用什么方式进入。
2. **Target**：目标。描述流量最终要到达哪个 Client 上的哪类资源。
3. **Transport**：传输路径。描述 Ingress 与 Target 之间业务流量如何传输。

这个抽象的**当前实现范围**必须表达：

```text
Server 公网 TCP/UDP 入口      → Client 本地 TCP/UDP 服务
Server HTTP Host             → Client 本地 TCP 服务
Client A 本地 TCP/UDP 入口   → Client B 本地 TCP/UDP 服务
```

这个抽象的**未来扩展方向**还应能不改核心模型地表达：

```text
Server HTTP Host             → Client static_file
Client A 本地 TCP 入口       → Client B Unix Socket / Windows Named Pipe 等本地 IPC
Client A 本地 TCP 入口       → Client B 串口设备
```

注意：未来扩展示例只说明模型边界，不表示当前代码要提前加入这些 enum、schema、API 或 UI 表单。

因此：

```text
server_expose / client_to_client 是拓扑，不是协议。
tcp / udp / http_host 是当前 endpoint 能力，不是隧道大类。
unix_socket / static_file / serial_device 是未来 endpoint 示例，当前不进入代码枚举、数据库 CHECK、capability 或创建 API。
server_relay_only / direct_preferred / direct_only 是传输策略，不是 endpoint 类型。
```

## 2. 废弃旧 Proxy 模型

新架构中，以下旧模型不再作为隧道核心模型：

```text
ProxyConfig
ProxyNewRequest
ProxyProvisionRequest
proxy_create
proxy_create_resp
proxy_provision
proxy_provision_ack
proxy_close
```

旧模型的问题是它只能表达：

```text
某个 Client 的 local_ip:local_port 被 Server 通过 remote_port/domain 暴露出去。
```

它不能自然表达：

```text
Client A 本地入口 → Client B 本地服务
Client A 本地入口 → Client B 本地 TCP/UDP 服务
以及未来的 Unix Socket / static_file / 串口设备等 endpoint
```

新架构只使用以下核心概念：

```text
TunnelSpec
EndpointSpec
TunnelRuntime
ParticipantRuntime
TransportRuntime
ResourceKey
DataStreamHeader
```

控制协议也统一改为：

```text
tunnel_create
tunnel_create_resp
tunnel_provision
tunnel_provision_ack
tunnel_unprovision
tunnel_runtime_report
tunnel_stream_close
p2p_session_prepare
p2p_session_ready
p2p_candidate
p2p_connectivity_check
p2p_selected
p2p_failed
p2p_closed
p2p_stats_report
traffic_report
```

不再同时维护 `proxy_*` 与 `tunnel_*` 两套语义。

## 3. 枚举定义

### 3.1 Topology

```text
server_expose
client_to_client
```

含义：

- `server_expose`：入口在 Server，目标在 Client。
- `client_to_client`：入口在 Client，目标在 Client。

首期不支持 `target_location = server`。Server 可以作为 Ingress，但不能作为 Target。

### 3.2 Endpoint Location

```text
server
client
```

规则：

- `ingress.location` 可以是 `server` 或 `client`。
- `target.location` 当前只允许 `client`。
- 当 `location = client` 时，`client_id` 必填。
- 当 `location = server` 时，`client_id` 必须为空。

### 3.3 Ingress Type

首期支持：

```text
tcp_listen
udp_listen
http_host
```

规则：

```text
tcp_listen:
  server_expose 和 client_to_client 都支持。

udp_listen:
  server_expose 和 client_to_client 都支持。

http_host:
  只允许 ingress.location = server。
  只用于 server_expose。
  client_to_client 不支持 http_host。
```

未来可扩展：

```text
unix_listen
stdio
named_pipe_listen
```

### 3.4 Target Type

首期只支持当前明确要运行的目标类型：

```text
tcp_service
udp_service
```

规则：

```text
tcp_service:
  目标为 Client 上的 TCP 服务。

udp_service:
  目标为 Client 上的 UDP 服务。
```

当前代码不得提前加入以下目标类型的 enum、数据库 CHECK、capability 声明、API schema 或前端可提交表单：

```text
unix_socket
static_file
serial_device
```

这些类型只作为未来扩展方向记录在本文的“未来 endpoint 扩展边界”中。等真正实现时，必须为每种类型补齐跨平台语义、资源锁、adapter、校验、测试和 UI，而不是现在先暴露一个不可运行的类型。

### 3.5 Transport Policy

最终命名使用：

```text
server_relay_only
direct_preferred
direct_only
```

不用 `p2p_only` 作为内部协议枚举，因为 P2P 在不同技术栈里可能包含 TURN relay。NetsGo 内部要明确区分：

```text
server_relay: 业务流量经过 NetsGo Server。
peer_direct: 业务流量在两个 Client 之间直连。
turn_relay: 业务流量经过 TURN relay。
```

首期策略定义：

```text
server_relay_only:
  只允许 NetsGo Server 中继业务流量。

direct_preferred:
  优先 peer_direct。
  peer_direct 不可用时回退 server_relay。
  已存在连接不强制迁移，新连接按当前最佳路径选择。

direct_only:
  只允许 peer_direct。
  不允许 server_relay。
  不允许 turn_relay。
```

三种流量策略是端到端隧道的必需能力：

```text
client_to_client + tcp_listen -> tcp_service:
  server_relay_only 必须可用。
  direct_preferred 必须可用，并且 direct 失败时可回退 server_relay。
  direct_only 必须可用，并且 direct 失败时不得回退 server_relay。
```

如果实现过程中先合入 capability-gated 的中间状态，该状态只能作为内部未完成状态，不能作为最终完成声明。`direct_preferred` / `direct_only` 在最终交付中不得因为 WebRTC direct transport 未实现而长期返回 `direct_transport_unavailable`。

### 3.6 Actual Transport

```text
unknown
server_relay
peer_direct
turn_relay
```

首期即使不启用 TURN，也保留 `turn_relay` 枚举，避免后续语义破坏。

规则：

```text
direct_only:
  actual_transport 只能是 unknown 或 peer_direct。
  如果收到 server_relay stream，Server 必须拒绝并记录安全事件。

direct_preferred:
  actual_transport 可以是 peer_direct 或 server_relay。
  当 peer_direct 失败并回退时，actual_transport = server_relay。

server_relay_only:
  actual_transport 只能是 unknown 或 server_relay。
```

### 3.7 Desired State

```text
running
stopped
```

### 3.8 Runtime State

```text
pending
active
offline
idle
error
```

含义：

```text
pending:
  正在 provisioning、监听、准备 target 或建立 transport。

active:
  隧道入口和传输路径可用。
  不表示目标业务服务健康。

offline:
  必要参与 Client 离线。

idle:
  desired_state = stopped，隧道已停止。

error:
  隧道不可用，且没有可用 fallback。
```

不再使用旧 `exposed`。统一使用 `active`。

### 3.9 Participant State

```text
unknown
offline
provision_pending
provision_rejected
ready
listening
listen_failed
target_ready
target_failed
```

说明：

- Ingress participant 通常进入 `listening` 表示入口已监听。
- Target participant 通常进入 `target_ready` 表示已接受目标配置。
- 对 `tcp_service` / `udp_service`，`target_ready` 不表示目标端口业务健康，只表示 Client 已接受配置并可在连接发生时尝试连接。

### 3.10 P2P State

```text
idle
gathering
checking
connected
failed
fallback
closed
```

说明：

- `idle`：尚未开始 direct 尝试。
- `gathering`：正在收集候选地址。
- `checking`：正在做连通性检查。
- `connected`：peer_direct 已建立。
- `failed`：peer_direct 失败。
- `fallback`：peer_direct 失败，`direct_preferred` 当前走 `server_relay`。
- `closed`：P2P session 已关闭。

## 4. P2P 技术选型

一步到位实现必须明确 P2P 技术路线。本文规范选择：

```text
WebRTC / ICE
```

原因：

1. NAT 穿透能力成熟。
2. STUN/ICE 生态成熟。
3. 可以表达 host / srflx / prflx / relay candidate。
4. 后续可扩展 TURN，但不会混淆 direct_only 语义。

实现约束：

```text
p2p_impl = webrtc_ice
candidate_type = host / srflx / prflx / relay
actual_transport = peer_direct / turn_relay
```

首期策略：

```text
NetsGo direct_only 不允许 TURN relay。
NetsGo direct_preferred 优先 peer_direct，失败后走 server_relay。
TURN relay 不作为 direct_preferred 的默认 fallback。
如未来支持 TURN，应增加独立策略或独立配置，例如 turn_relay_allowed，不得改变 direct_only 的语义。
```

P2P session 必须满足：

1. 双方身份可验证。
2. token 绑定双方临时加密身份。
3. signaling 消息带 `tunnel_id`、`revision`、`p2p_session_id`、`sequence`。
4. direct 失败原因可上报。
5. `direct_only` 失败后不得走 Server relay。
6. `direct_preferred` 失败后新连接可回退 Server relay。

## 5. Endpoint Config Schema

所有 endpoint config 必须有明确 schema。Adapter 不允许接受未定义字段并静默忽略关键配置。

### 5.1 `tcp_listen`

```json
{
  "bind_ip": "127.0.0.1",
  "port": 10022
}
```

规则：

```text
port 必须在 1-65535。
bind_ip 必须是合法 IP。
server_expose 默认 bind_ip = 0.0.0.0。
client_to_client 默认 bind_ip = 127.0.0.1。
```

Client 侧安全默认：

```text
client_to_client 的 tcp_listen 默认只绑定 127.0.0.1。
如果绑定 0.0.0.0 或 ::，前端必须明确提示该入口会暴露给源 Client 所在网络。
Server 应提供全局策略控制是否允许 Client 侧 wildcard bind。
```

建议全局策略：

```text
allow_client_ingress_wildcard_bind = false
allow_client_ingress_lan_bind = true
```

### 5.2 `udp_listen`

```json
{
  "bind_ip": "127.0.0.1",
  "port": 15353
}
```

规则同 `tcp_listen`。

### 5.3 `http_host`

```json
{
  "domain": "app.example.com"
}
```

规则：

```text
只能 ingress.location = server。
只能 topology = server_expose。
domain 必须合法。
domain 全局唯一。
domain 不能与管理地址冲突。
```

### 5.4 `tcp_service`

```json
{
  "ip": "127.0.0.1",
  "port": 22
}
```

规则：

```text
target.location 必须是 client。
port 必须在 1-65535。
ip 必须是合法 IP 或未来明确支持的 host。
默认不在 provisioning 阶段主动探测目标服务健康。
```

### 5.5 `udp_service`

```json
{
  "ip": "127.0.0.1",
  "port": 53
}
```

规则同 `tcp_service`。

### 5.6 未来 endpoint 扩展边界（非当前代码 schema）

以下类型当前只保留为架构扩展方向，**不进入当前代码枚举、数据库 CHECK、capability、API payload schema 或前端创建表单**：

```text
unix_socket
static_file
serial_device
```

保留这部分说明的目的，是让后续实现者知道这些能力应该如何接入 `Tunnel = Ingress + Target + Transport`，而不是现在把不可运行的类型塞进模型。

#### 5.6.1 未来 `unix_socket` / 本地 IPC

未来如果要支持本地 IPC target，应单独设计跨平台边界：

```text
Linux/macOS:
  可考虑 Unix domain socket。

Windows:
  不能简单复用 unix_socket 语义；应评估 Windows Named Pipe，必要时使用独立 target type。

资源锁:
  target:client:<client_id>:ipc:<normalized_path_or_name>

校验:
  path/name normalize、权限、是否允许 provisioning 时检查存在、错误如何上报。
```

#### 5.6.2 未来 `static_file`

未来如果要支持静态文件 target，建议只通过应用层 HTTP 入口暴露，例如：

```text
Server HTTP Host -> Client static_file
```

不得把 `static_file` 硬塞成 `Dial() io.ReadWriteCloser`。未来实现时至少要定义：

```text
root 是否必须存在且为目录
path traversal 防护
是否允许目录列表
是否允许上传
是否 follow symlink
是否允许访问隐藏文件
Range / Content-Type / index / SPA fallback 语义
跨平台路径 normalize 与权限策略
```

默认安全建议仍是：只读、禁止目录列表、禁止上传、不 follow symlink 到 root 外、默认隐藏文件不可访问。

#### 5.6.3 未来 `serial_device`

未来如果要支持串口 target，必须单独定义设备独占和连接语义：

```text
Linux/macOS/Windows 设备命名与权限
是否独占打开
max_connections
连接建立时打开还是 provisioning 时打开
连接断开是否关闭串口
DTR/RTS、baud_rate、parity、stop_bits 等参数
参数变更是否触发 reprovision
已有连接时新连接如何拒绝
```

默认建议：`exclusive = true`、`max_connections = 1`、`open_mode = on_connection`。

## 6. 核心数据结构
## 6. 核心数据结构

### 6.1 TunnelSpec

```go
type TunnelSpec struct {
    ID       string `json:"id"`
    Name     string `json:"name"`
    Revision int64  `json:"revision"`

    Topology string `json:"topology"`
    // server_expose / client_to_client

    OwnerClientID string `json:"owner_client_id"`
    // 只用于默认列表归属，不作为权限依据。

    Ingress EndpointSpec `json:"ingress"`
    Target  EndpointSpec `json:"target"`

    TransportPolicy string `json:"transport_policy"`
    // server_relay_only / direct_preferred / direct_only

    ActualTransport string `json:"actual_transport"`
    // unknown / server_relay / peer_direct / turn_relay

    P2P P2PState `json:"p2p"`

    DesiredState string `json:"desired_state"`
    RuntimeState string `json:"runtime_state"`
    Error        string `json:"error,omitempty"`

    Participants TunnelParticipants `json:"participants,omitempty"`
    Transport    TransportRuntime   `json:"transport,omitempty"`

    BandwidthSettings BandwidthSettings `json:"bandwidth_settings"`

    CreatedByUserID string    `json:"created_by_user_id,omitempty"`
    CreatedAt       time.Time `json:"created_at"`
    UpdatedAt       time.Time `json:"updated_at"`
}
```

### 6.2 EndpointSpec

```go
type EndpointSpec struct {
    Location string          `json:"location"`
    ClientID string          `json:"client_id,omitempty"`
    Type     string          `json:"type"`
    Config   json.RawMessage `json:"config"`
}
```

### 6.3 P2PState

```go
type P2PState struct {
    State     string `json:"state"`
    Error     string `json:"error,omitempty"`
    SessionID string `json:"session_id,omitempty"`
}
```

### 6.4 Participant Runtime

```go
type TunnelParticipants struct {
    Ingress ParticipantRuntime `json:"ingress"`
    Target  ParticipantRuntime `json:"target"`
}

type ParticipantRuntime struct {
    ClientID string `json:"client_id"`
    Role     string `json:"role"`
    State    string `json:"state"`
    Revision int64  `json:"revision"`
    Error    string `json:"error,omitempty"`
}
```

### 6.5 Transport Runtime

```go
type TransportRuntime struct {
    Policy          string    `json:"policy"`
    Actual          string    `json:"actual"`
    P2PState        string    `json:"p2p_state,omitempty"`
    P2PError        string    `json:"p2p_error,omitempty"`
    FallbackSince   time.Time `json:"fallback_since,omitempty"`
    LastDirectOK    time.Time `json:"last_direct_ok,omitempty"`
    LastDirectError string    `json:"last_direct_error,omitempty"`
}
```

## 7. 数据库 Schema

统一使用一张 `tunnels` 表。JSON 保存完整 endpoint config，普通列保存可索引资源字段。

```sql
CREATE TABLE tunnels (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,

    topology TEXT NOT NULL,
    owner_client_id TEXT NOT NULL,

    ingress_location TEXT NOT NULL,
    ingress_client_id TEXT NOT NULL DEFAULT '',
    ingress_type TEXT NOT NULL,
    ingress_config TEXT NOT NULL DEFAULT '{}',

    ingress_bind_ip TEXT NOT NULL DEFAULT '',
    ingress_port INTEGER NOT NULL DEFAULT 0,
    ingress_domain TEXT NOT NULL DEFAULT '',
    ingress_path TEXT NOT NULL DEFAULT '',

    target_location TEXT NOT NULL,
    target_client_id TEXT NOT NULL DEFAULT '',
    target_type TEXT NOT NULL,
    target_config TEXT NOT NULL DEFAULT '{}',

    target_host TEXT NOT NULL DEFAULT '',
    target_port INTEGER NOT NULL DEFAULT 0,
    target_path TEXT NOT NULL DEFAULT '',
    target_resource_key TEXT NOT NULL DEFAULT '',

    transport_policy TEXT NOT NULL,
    actual_transport TEXT NOT NULL DEFAULT 'unknown',

    p2p_state TEXT NOT NULL DEFAULT 'idle',
    p2p_error TEXT NOT NULL DEFAULT '',
    p2p_session_id TEXT NOT NULL DEFAULT '',

    ingress_bps INTEGER NOT NULL DEFAULT 0,
    egress_bps INTEGER NOT NULL DEFAULT 0,

    desired_state TEXT NOT NULL,
    runtime_state TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',

    created_by_user_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,

    CHECK (topology IN ('server_expose', 'client_to_client')),
    CHECK (ingress_location IN ('server', 'client')),
    CHECK (target_location IN ('client')),
    CHECK (
        (topology = 'server_expose' AND ingress_location = 'server' AND ingress_client_id = '' AND target_location = 'client' AND target_client_id <> '')
        OR
        (topology = 'client_to_client' AND ingress_location = 'client' AND ingress_client_id <> '' AND target_location = 'client' AND target_client_id <> '')
    ),
    CHECK (ingress_type IN ('tcp_listen', 'udp_listen', 'http_host')),
    CHECK (target_type IN ('tcp_service', 'udp_service')),
    CHECK (transport_policy IN ('server_relay_only', 'direct_preferred', 'direct_only')),
    CHECK (actual_transport IN ('unknown', 'server_relay', 'peer_direct', 'turn_relay')),
    CHECK (p2p_state IN ('idle', 'gathering', 'checking', 'connected', 'failed', 'fallback', 'closed')),
    CHECK (desired_state IN ('running', 'stopped')),
    CHECK (runtime_state IN ('pending', 'active', 'offline', 'idle', 'error')),

    UNIQUE(owner_client_id, name)
);
```

这些 `CHECK` 不是业务校验的替代品。数据库层只承担不依赖 JSON 解析的硬不变量：

```text
topology 与 endpoint location/client_id 的基本对应关系。
枚举值合法性。
owner/name 基本唯一性。
```

更细的 endpoint 组合规则，例如 `http_host` 只能配 `server_expose`、`direct_only` 只能用于 `client_to_client`，必须由业务校验和测试矩阵保证。未来新增 target type 时，也必须同步补充业务校验和测试矩阵。

索引：

```sql
CREATE INDEX idx_tunnels_owner ON tunnels(owner_client_id, created_at);
CREATE INDEX idx_tunnels_ingress_client ON tunnels(ingress_client_id);
CREATE INDEX idx_tunnels_target_client ON tunnels(target_client_id);
CREATE INDEX idx_tunnels_topology ON tunnels(topology);
CREATE INDEX idx_tunnels_runtime_state ON tunnels(runtime_state);

CREATE INDEX idx_tunnels_ingress_port
ON tunnels(ingress_location, ingress_client_id, ingress_type, ingress_bind_ip, ingress_port);

CREATE INDEX idx_tunnels_ingress_domain
ON tunnels(ingress_domain);

CREATE INDEX idx_tunnels_target_resource
ON tunnels(target_location, target_client_id, target_type, target_resource_key);
```


## 8A. 切换、迁移与失败恢复策略

虽然当前产品假设“不需要兼容旧 API / 旧 payload / 旧 Client 协议”，但本项目已经有 SQLite schema 和开发环境数据。因此必须区分：

```text
API/协议兼容：不保留。
数据库切换：必须有明确迁移、备份和失败恢复策略。
```

### 8A.1 fresh install

新安装直接创建新 schema：

```text
tunnels 使用统一 TunnelSpec schema。
traffic_buckets 使用 tunnel_id 作为主键组成部分。
tunnel_resource_locks 随 schema 一起创建。
```

不创建旧 `ProxyConfig` 语义列作为核心模型。

### 8A.2 existing DB

如果检测到旧 `tunnels` 表，应执行一次性 schema rebuild：

```text
0. 迁移开始前先创建 SQLite 文件级备份。
1. 开启事务。
2. 将旧表重命名为 tunnels_legacy_backup / traffic_buckets_legacy_backup。
3. 创建新的 tunnels / traffic_buckets / tunnel_resource_locks。
4. 将旧 tunnels 转换为 server_expose TunnelSpec 行。
5. 保留旧 tunnel id。如果旧行没有 id，则生成新 id。
6. 保留 name、created_at、desired_state、error、bandwidth。
7. runtime_state 不直接保留 active/exposed；启动后重新计算。
8. 将旧 traffic_buckets 按 tunnel_id 迁移；如果旧 tunnel 无法匹配 id，则丢弃该 tunnel 的历史流量并记录 migration warning。
9. resource_locks 根据新 endpoint 规范重建。
10. 校验新表 row count、resource lock、JSON schema。
11. 提交事务。
```

SQLite 不支持在同一个 schema 内同时存在两个同名 `tunnels` 表。因此 rebuild 必须明确采用：

```text
rename old -> create new -> copy transformed rows
```

不能先创建 `tunnels_new` 再把旧表“重命名为 backup”后留下 `tunnels_new` 不改名。迁移完成后，生产代码只查询新 `tunnels`，绝不能继续读取 backup 表作为运行态来源。

旧类型映射：

```text
旧 type=tcp:
  topology = server_expose
  ingress = server tcp_listen {bind_ip: 0.0.0.0, port: remote_port}
  target = client tcp_service {ip: local_ip, port: local_port}
  transport_policy = server_relay_only

旧 type=udp:
  topology = server_expose
  ingress = server udp_listen {bind_ip: 0.0.0.0, port: remote_port}
  target = client udp_service {ip: local_ip, port: local_port}
  transport_policy = server_relay_only

旧 type=http:
  topology = server_expose
  ingress = server http_host {domain: domain}
  target = client tcp_service {ip: local_ip, port: local_port}
  transport_policy = server_relay_only
```

旧数据字段映射细节：

```text
client_id:
  映射为 target_client_id 和 owner_client_id。

hostname / binding:
  不进入 TunnelSpec 权威模型。hostname 仍属于 Client/展示信息；binding 不再作为隧道身份依据。

remote_port:
  tcp/udp 必填且必须转为 ingress_port。
  http tunnel 的 remote_port 应为 0 或忽略。

domain:
  http 必填并 normalize 为 ingress_domain。

traffic_buckets:
  旧 client_id + tunnel_name + tunnel_type 先通过 legacy tunnels backup 找到 tunnel_id。
  新表 transport 固定写入 server_relay。
  owner_client_id / ingress_client_id / target_client_id / topology 从新 tunnel 行复制。
```

### 8A.3 runtime state migration

旧 `exposed` 不迁移为 `active`。迁移后：

```text
desired_state = stopped -> runtime_state = idle
running tunnel -> runtime_state = pending 或 offline，由 startup coordinator 重新计算
```

原因：

```text
active 必须表示当前入口和传输路径实际可用，不能从旧持久化状态信任恢复。
```

### 8A.4 failed migration

迁移失败时：

```text
事务回滚。
原旧表必须保持可读。
Server 启动失败并输出明确错误。
不得启动半迁移 schema。
```

建议在迁移开始前创建 SQLite 文件级备份：

```text
server.db.pre-unified-tunnel.<timestamp>.bak
```

### 8A.5 dirty dev DB

如果检测到 schema 既不像旧 schema 也不像新 schema：

```text
Server 启动失败。
错误信息提示备份或删除 dev DB。
不得猜测迁移。
```

### 8A.6 downgrade stance

不支持自动 downgrade。迁移完成后旧二进制不保证可读新 schema。

如果必须回退，只能：

```text
停止服务。
恢复 pre-unified-tunnel 备份 DB。
启动旧二进制。
```

### 8A.7 API/protocol cutover

统一架构版本中：

```text
旧 proxy_* 控制消息不再接受。
旧 API 创建 payload 不再接受。
旧 Client 协议不再允许连接为可用隧道 Client。
```

旧 Client 连接时，如果 capability 不包含 `tunnel_spec_version`，Server 应认证失败或认证成功但标记为 unsupported。推荐：

```text
认证失败，返回 code = unsupported_client_version。
```

## 8. ResourceKey 与资源冲突检测

不能只靠 `UNIQUE(owner_client_id, name)`。名称唯一只解决显示名冲突，不解决资源占用冲突。

每个 Ingress Adapter / Target Adapter 必须声明自己占用的 ResourceKey。

示例：

```text
server tcp listen:
  ingress:server:tcp:0.0.0.0:18080

server udp listen:
  ingress:server:udp:0.0.0.0:15353

server http host:
  ingress:server:http_host:app.example.com

client tcp listen:
  ingress:client:client-a:tcp:127.0.0.1:10022
```

资源锁表：

```sql
CREATE TABLE tunnel_resource_locks (
    resource_key TEXT PRIMARY KEY,
    tunnel_id TEXT NOT NULL,
    resource_kind TEXT NOT NULL,
    client_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX idx_tunnel_resource_locks_tunnel
ON tunnel_resource_locks(tunnel_id);

CREATE INDEX idx_tunnel_resource_locks_client
ON tunnel_resource_locks(client_id);
```

创建或更新 tunnel 必须在同一事务中执行：

```text
1. 校验 TunnelSpec。
2. Adapter normalize config。
3. Adapter 生成 ResourceKey。
4. 写入 tunnels。
5. 写入 tunnel_resource_locks。
6. 如果 resource_key 冲突，事务失败，返回资源冲突错误。
```

资源冲突规则至少包括：

```text
Server TCP 端口冲突。
Server UDP 端口冲突。
Server HTTP Host 域名冲突。
Client 本地 TCP 监听端口冲突。
Client 本地 UDP 监听端口冲突。
```


### 8.1 ResourceKey 规范化与冲突矩阵

`resource_key` 不能只做字符串相等比较。Adapter 生成 key 前必须先 normalize，并且部分资源需要冲突矩阵。

#### IP normalize

```text
IPv4、IPv6 必须规范化为标准字符串。
localhost 不允许作为 bind_ip 存储；必须解析为具体 loopback IP，或拒绝。
域名不允许作为 listen bind_ip。
```

#### TCP/UDP 端口规则

```text
port = 0 不允许用于持久化 tunnel。
management listen port 不能被 server tcp/udp ingress 使用。
TCP 与 UDP 同数字端口可以共存，除非具体平台限制另有证据。
同协议同 location/client/bind_ip/port 按冲突矩阵判断。
```

#### Wildcard bind 冲突

同一 host / client 上：

```text
0.0.0.0:PORT 与 任意 IPv4:PORT 冲突。
::PORT 与 任意 IPv6:PORT 冲突。
如果平台 dual-stack IPv6 wildcard 会同时占用 IPv4，则 ::PORT 与 0.0.0.0:PORT 也冲突。
127.0.0.1:PORT 与 192.168.1.2:PORT 默认可共存。
```

实现要求：

```text
Resource lock 可以存多个 normalized resource_key。
Wildcard 监听必须展开出 conflict probe keys，例如 wildcard-v4 和具体端口族。
创建时除插入自身 key 外，还必须查询会与其冲突的 key 集合。
```

#### HTTP Host normalize

```text
host 必须 lower-case。
去掉末尾 dot。
IDN 必须 punycode normalize。
禁止包含 scheme、path、port。
与 Server 管理地址 host 冲突时拒绝。
```

#### Path normalize

未来新增路径型 endpoint（例如本地 IPC、静态文件目录、串口设备）时必须：

```text
清理 .. 和重复分隔符。
转换为绝对路径。
按所在 OS 规则做 normalize，并在 Client 侧做最终 realpath/设备名校验。
```

## 9. Owner、Source、Target 与权限语义

`owner_client_id` 只用于：

```text
默认列表归属
UI 分组
默认 GET /api/clients/{id}/tunnels?role=owner 查询
```

它不能作为权限依据。

端到端隧道本质是：

```text
Ingress Client 获得访问 Target Client 本地资源的能力。
```

因此权限规则必须基于：

```text
created_by_user_id
ingress_client_id
target_client_id
endpoint type
global policy
admin role
```

当前版本规则直接定为：

```text
只有 admin 可以创建、更新、删除 tunnel。
admin 可以创建任意 source → target tunnel。
target client 不需要单独批准。
但 target client 的 related tunnel 必须可见。
```

未来如果引入 viewer、client-owner、approval 等权限模型，不能复用 `owner_client_id` 作为授权依据。

## 10. API 规范

### 10.1 创建与管理

创建入口统一使用全局 API：

```text
POST /api/tunnels
GET /api/tunnels
GET /api/tunnels/{id}
PUT /api/tunnels/{id}
PUT /api/tunnels/{id}/resume
PUT /api/tunnels/{id}/stop
DELETE /api/tunnels/{id}
```

不使用 `POST /api/clients/{id}/tunnels` 创建，避免 path id、ingress client id、target client id 三者产生歧义。

Server 根据 topology 自动推导 owner：

```text
server_expose:
  owner_client_id = target.client_id

client_to_client:
  owner_client_id = ingress.client_id
```

客户端不允许提交 `owner_client_id`。如果提交，Server 必须忽略或拒绝；推荐拒绝并返回明确错误。

### 10.2 Client 视角列表

```text
GET /api/clients/{id}/tunnels?role=owner
GET /api/clients/{id}/tunnels?role=ingress
GET /api/clients/{id}/tunnels?role=target
GET /api/clients/{id}/tunnels?role=related
```

含义：

```text
owner:
  owner_client_id = id。

ingress:
  ingress.client_id = id。

target:
  target.client_id = id。

related:
  owner_client_id = id OR ingress.client_id = id OR target.client_id = id。
```

默认 role：

```text
owner
```

前端 Client 详情页建议至少提供：

```text
我作为入口
我作为目标
```

以避免 target client 对被访问隧道不可见。

### 10.3 创建 server_expose TCP

```json
{
  "name": "web",
  "topology": "server_expose",
  "ingress": {
    "location": "server",
    "type": "tcp_listen",
    "config": {
      "bind_ip": "0.0.0.0",
      "port": 18080
    }
  },
  "target": {
    "location": "client",
    "client_id": "client-b",
    "type": "tcp_service",
    "config": {
      "ip": "127.0.0.1",
      "port": 80
    }
  },
  "transport_policy": "server_relay_only",
  "bandwidth_settings": {
    "ingress_bps": 0,
    "egress_bps": 0
  }
}
```

### 10.4 创建 client_to_client direct_preferred TCP

```json
{
  "name": "ssh-to-b",
  "topology": "client_to_client",
  "ingress": {
    "location": "client",
    "client_id": "client-a",
    "type": "tcp_listen",
    "config": {
      "bind_ip": "127.0.0.1",
      "port": 10022
    }
  },
  "target": {
    "location": "client",
    "client_id": "client-b",
    "type": "tcp_service",
    "config": {
      "ip": "127.0.0.1",
      "port": 22
    }
  },
  "transport_policy": "direct_preferred",
  "bandwidth_settings": {
    "ingress_bps": 0,
    "egress_bps": 0
  }
}
```



### 10.5 更新并发控制

`PUT /api/tunnels/{id}` 必须使用乐观锁。

请求必须包含：

```json
{
  "expected_revision": 17,
  "spec": {}
}
```

规则：

```text
如果当前 revision != expected_revision，返回 409 Conflict。
成功更新后 revision += 1。
只修改 name 或 bandwidth_settings 是否增加 revision 由更新策略决定；如果会影响 provisioning/runtime，必须增加 revision。
```

### 10.6 删除与 unprovision

本规划选择**硬删除**作为当前实现语义。删除 tunnel 后：

```text
tunnels 行被删除。
tunnel_resource_locks 被删除。
在线 participants 收到 tunnel_unprovision。
离线 participant 下次上线不得恢复该 tunnel。
traffic_buckets 默认保留，并继续按 tunnel_id 查询。
tunnel_id 永远不复用。
```

删除 running tunnel 允许，但必须走删除状态机：

```text
1. Server 将 tunnel 置为 deleting 内存状态，停止接收新 stream。
2. 停止 ingress listener、relay runtime、P2P session。
3. 向在线 participants 发送 tunnel_unprovision。
4. 在事务中删除 resource_locks 和 tunnels 行。
5. 保留 traffic_buckets，不使用 ON DELETE CASCADE。
6. 发布 tunnel_deleted / tunnel_changed 事件。
```

历史 traffic 查询必须能处理 tunnel 元数据已不存在的情况：

```text
如果 tunnels 行不存在，API 返回 tunnel_id、traffic bucket 和 metadata_missing=true。
UI 不得因为 tunnel 元数据缺失而崩溃。
如果需要显示历史名称/endpoint，未来可在 traffic bucket 或审计表里保存快照，但当前不引入 deleted_at 软删除。
```

当前不实现软删除：

```text
不增加 deleted_at。
默认列表不需要过滤 deleted_at。
不能一部分路径硬删除、一部分路径软删除。
```

`tunnel_unprovision` 必须包含：

```json
{
  "tunnel_id": "tun_123",
  "revision": 17,
  "role": "ingress",
  "reason": "deleted"
}
```

## 10A. 能力边界与支持矩阵

“模型一步到位”不等于现在暴露所有未来 endpoint。当前实现只暴露可运行、可测试、语义完整的类型。未来类型必须等到实现时再加入 enum/schema/API。

### 10A.1 当前代码允许的 endpoint 类型

```text
ingress_types:
  tcp_listen
  udp_listen
  http_host

target_types:
  tcp_service
  udp_service
```

当前代码必须拒绝以下未来类型，错误应是明确的 `unknown_target_type` 或 `unsupported_target_type`，不能保存成 stopped/offline tunnel：

```text
unix_socket
static_file
serial_device
```

### 10A.2 首个工程闭环建议

这里的“首个工程闭环”只表示合入顺序，不表示最终范围缩水：

```text
server_expose + tcp_listen -> tcp_service + server_relay_only
server_expose + udp_listen -> udp_service + server_relay_only
server_expose + http_host -> tcp_service + server_relay_only
client_to_client + tcp_listen -> tcp_service + server_relay_only
client_to_client + udp_listen -> udp_service + server_relay_only
```

### 10A.3 direct 能力

最终完成必须覆盖 TCP 和 UDP 的端到端 direct：

```text
client_to_client + tcp_listen -> tcp_service:
  server_relay_only
  direct_preferred
  direct_only

client_to_client + udp_listen -> udp_service:
  server_relay_only
  direct_preferred
  direct_only
```

创建 `direct_preferred` / `direct_only` 必须同时满足：

```text
ingress Client capability 声明 p2p.supported=true 且 impl=webrtc_ice。
target Client capability 声明 p2p.supported=true 且 impl=webrtc_ice。
ingress/target endpoint 组合支持 direct。
当前 Server 已实现 WebRTC direct transport。
```

在最终交付前，如果代码尚未实现 WebRTC direct transport，API 必须拒绝 `direct_preferred` / `direct_only`，错误码建议 `direct_transport_unavailable`。但这只能是内部未完成状态，不能作为本规划完成声明。

不允许保存 direct policy 后运行时才因为“能力缺失”失败；只有临时网络失败才允许进入 runtime fallback/error。

### 10A.4 UI 行为

前端只展示当前 Server 和相关 Client capability 共同支持的组合。未来类型可以出现在文档说明中，但当前创建表单不得提供可提交入口。

### 10A.5 Endpoint / transport 兼容矩阵

| Topology | Ingress | Target | server_relay_only | direct_preferred/direct_only |
| --- | --- | --- | --- | --- |
| server_expose | tcp_listen | tcp_service | 支持 | 不支持 |
| server_expose | udp_listen | udp_service | 支持 | 不支持 |
| server_expose | http_host | tcp_service | 支持 | 不支持 |
| client_to_client | tcp_listen | tcp_service | 支持 | 支持，最终完成必需 |
| client_to_client | udp_listen | udp_service | 支持 | 支持，最终完成必需 |
| client_to_client | http_host | 任意 | 不支持 | 不支持 |
| 任意 | 任意 | target_location=server | 不支持 | 不支持 |
| 任意 | 任意 | unix_socket/static_file/serial_device | 当前不进入代码模型 | 当前不进入代码模型 |

## 11. Provisioning、Revision 与 ACK 规则

每个 `TunnelSpec` 必须有单调递增的 `revision`。

```text
revision 是一致性字段。
updated_at 只是展示字段，不能用于 ACK 匹配。
```

创建 tunnel：

```text
revision = 1
```

每次修改会影响 provisioning 或 runtime 的字段：

```text
revision += 1
```

`tunnel_provision`：

```json
{
  "tunnel_id": "tun_123",
  "revision": 17,
  "role": "ingress",
  "spec": {}
}
```

`tunnel_provision_ack`：

```json
{
  "tunnel_id": "tun_123",
  "revision": 17,
  "role": "ingress",
  "accepted": true,
  "message": ""
}
```

规则：

```text
Server 只接受当前 revision 的 ACK。
旧 revision ACK 直接丢弃，不改变状态。
ACK 必须匹配 client_id、role、tunnel_id、revision。
client_to_client tunnel 必须等待 ingress 和 target 两端当前 revision ACK。
server_expose tunnel 至少等待 target ACK；如果 ingress 是 server，则 server 本地入口准备也作为 participant runtime 记录。
```

更新竞态示例：

```text
用户把 tunnel 更新到 revision 18。
Client 晚到 revision 17 ACK。
Server 必须丢弃 revision 17 ACK。
不能把 revision 18 标记为 active。
```

## 12. Tunnel-level Coordinator

恢复逻辑必须以 tunnel 为中心，而不是以单个 Client 为中心。

任意相关 Client 上线时：

```text
1. 查找所有 related tunnels。
2. 更新 participant online state。
3. 如果必要 participant 都在线：
   3.1 向 ingress participant 下发 tunnel_provision。
   3.2 向 target participant 下发 tunnel_provision。
   3.3 等待当前 revision 双端 ACK。
   3.4 根据 transport_policy 建立 server_relay 或 direct。
4. 如果任意必要 participant 离线：
   4.1 runtime_state = offline。
   4.2 标记具体 participant offline。
```

不能再使用旧模式：

```text
client 上线 -> restore 自己拥有的 tunnels
```

因为 client_to_client 隧道涉及至少两个 participant。

## 13. Runtime 聚合规则

单一 `runtime_state` 是聚合状态。详细状态由 participants 和 transport 表达。

推荐 API 返回：

```json
{
  "desired_state": "running",
  "runtime_state": "active",
  "participants": {
    "ingress": {
      "client_id": "client-a",
      "role": "ingress",
      "state": "listening",
      "revision": 17,
      "error": ""
    },
    "target": {
      "client_id": "client-b",
      "role": "target",
      "state": "target_ready",
      "revision": 17,
      "error": ""
    }
  },
  "transport": {
    "policy": "direct_preferred",
    "actual": "server_relay",
    "p2p_state": "fallback",
    "p2p_error": "connectivity check failed"
  }
}
```

聚合规则：

```text
desired_state = stopped:
  runtime_state = idle

任意必要 Client 离线:
  runtime_state = offline

任意 participant provisioning/listening 中:
  runtime_state = pending

任意硬错误且没有 fallback:
  runtime_state = error

至少一种允许 transport 可用:
  runtime_state = active
```

`runtime_state = active` 的含义必须写死：

```text
active 表示隧道入口和传输路径可用。
active 不表示目标业务服务健康。
```

例如 `Client B:127.0.0.1:22` 未启动时，`tcp_service` tunnel 仍可 active；具体连接会在发生时失败，并记录 connection-level error。


## 13A. Runtime 持久化边界

`participants` 和 `transport` runtime 主要是运行态，不作为长期事实来源。

### 13A.1 持久化内容

`tunnels` 表持久化：

```text
desired_state
runtime_state 聚合值
actual_transport
p2p_state
p2p_error
p2p_session_id
updated_at
```

不持久化完整 participant runtime 作为权威状态。Participant runtime 保存在内存中，并可通过事件流/API 从当前 coordinator 快照生成。

### 13A.2 Server 重启行为

Server 启动后不得信任旧 `active`：

```text
desired_state = stopped -> runtime_state = idle
running tunnel -> runtime_state = pending
如果必要 Client 未在线 -> offline
如果必要 Client 在线 -> 重新 provision 当前 revision
```

P2P session 重启后全部失效：

```text
actual_transport = unknown
p2p_state = idle
p2p_session_id = ''
```

### 13A.3 错误保留

长期配置错误可以保留在 `error` 中，例如：

```text
resource conflict
unsupported capability
```

临时运行错误，例如 P2P connectivity failed，应进入 `p2p_error` 或 runtime event，不应永久阻止下次重试，除非 policy 为 `direct_only` 且重试退避仍失败。

## 14. DataStreamHeader Framing

旧 stream header 使用：

```text
[2B name length][proxy_name bytes]
```

新架构直接改成安全的版本化 framing：

```text
magic        4 bytes   "NGSH"
version      1 byte    1
header_len   4 bytes   uint32 big endian
header_json  N bytes
payload      ...
```

Header JSON：

```json
{
  "kind": "tunnel_stream",
  "tunnel_id": "tun_123",
  "revision": 17,
  "stream_id": "str_456",
  "open_client_id": "client-a",
  "source_role": "ingress",
  "target_role": "target",
  "direction": "ingress_to_target",
  "transport": "server_relay",
  "open_token": "..."
}
```

必填字段：

```text
tunnel_id
revision
stream_id
open_client_id
direction
transport
open_token
```

`TunnelID` 必须取代名称作为 stream 路由依据。名称可以修改，ID 是稳定标识。


### 14.1 Header limits 与失败行为

限制：

```text
magic 固定为 4 bytes: NGSH。
version 初始为 1。
header_len 最大 16 KiB。
header_json 必须是 UTF-8 JSON object。
单个 string 字段最大 1024 bytes，open_token 最大 4096 bytes。
stream_id 必须全局足够随机，建议 128-bit random base64url。
```

读写行为：

```text
读取 header 必须设置短 read deadline，建议 5s。
header 解析完成后清除或恢复正常 stream deadline。
解析失败立即关闭 stream。
Server 可记录 security/runtime event，但不把整个 data session 断开，除非短时间内同一 Client 连续 malformed 超过阈值。
```

严格校验：

```text
未知 kind 拒绝。
未知 enum 拒绝。
缺少必填字段拒绝。
revision <= 0 拒绝。
open_client_id 与当前 data session client 不一致拒绝。
```

错误行为：

```text
yamux stream 没有应用层 close code；直接关闭 stream。
如果 header 已读且可解析，可异步通过 control channel 发送 tunnel_stream_close 或 runtime_report，说明错误原因。
```

同一 framing 用于：

```text
Server 打开到 Target Client 的 stream。
Source Client 主动打开到 Server 的 stream。
未来 peer_direct 上的 logical stream。
```

### 14.2 Malformed-frame DoS 防护

必须测试：

```text
超大 header_len。
header_len 声明大但实际不发送。
非 JSON header。
缺少 open_token。
超长字符串字段。
大量 malformed stream 快速打开。
```

## 15. Client 主动 OpenStream 与 Server 鉴权

端到端 server relay 需要 Source Client 主动打开到 Server 的 yamux stream。

Server 必须为每个 data session 增加：

```go
acceptClientStreams(client)
```

逻辑：

```text
for each accepted stream:
  1. read DataStreamHeader
  2. validate magic/version/header_len
  3. validate tunnel_id exists
  4. validate revision is current
  5. validate open_client_id == current data session client.ID
  6. validate current client is tunnel ingress client
  7. validate desired_state == running
  8. validate runtime_state allows new stream
  9. validate transport_policy allows server_relay
  10. validate header.transport == server_relay
  11. validate open_token is valid, unexpired, and bound to tunnel_id/revision/client/stream_id
  12. open target stream to target client
  13. relay source stream <-> target stream
```

关键安全原则：

```text
dataToken 只证明“这是某个 Client 的数据通道”。
dataToken 不证明“这个 Client 有权打开某条 tunnel 的业务 stream”。
```

必须使用 per-tunnel 或 per-stream `open_token` 做二次授权。


## 15A. open_token 生命周期

`open_token` 由 Server 签发，用于授权某个 Client 在某个 tunnel revision 下打开业务 stream。

### 15A.1 签发时机

推荐模式：

```text
Source Client 每次接受本地新连接后，通过控制通道请求 stream open token。
Server 返回短 TTL、单次使用 token。
Source Client 随后在 DataStreamHeader 中携带该 token 打开 yamux stream。
```

可优化模式：

```text
Server 可预下发少量 token 池，但每个 token 仍必须单次使用且有短 TTL。
```

首期优先实现按需签发，避免 token 池复杂性。

### 15A.2 token 绑定字段

Token 必须绑定：

```text
tunnel_id
revision
ingress_client_id
target_client_id
stream_id
transport = server_relay
session_generation 或 data session id
issued_at
expires_at
nonce
```

TTL 建议：

```text
30s
```

### 15A.3 防重放

Server 必须维护近期已使用 token nonce 或 stream_id replay cache：

```text
key = tunnel_id + revision + stream_id
保留时间 = token TTL + clock_skew_window
```

同一 token 或 stream_id 再次使用必须拒绝。

### 15A.4 revision 与重连

```text
Tunnel revision 增加后，旧 revision token 全部失效。
Client data session generation 变化后，绑定旧 generation 的 token 失效。
Server 重启后，未使用 token 全部失效。
```

### 15A.5 target-side stream

Server 打开 Target Client stream 时也必须携带 DataStreamHeader，但该 header 的 token 可使用 Server 内部签名 token 或省略 open_token 并改用 `server_authorized=true` 签名字段。推荐仍使用 token，绑定：

```text
tunnel_id
revision
target_client_id
stream_id
transport = server_relay
server_generation
```

Target Client 必须验证该 stream 来自已认证 Server data session，且 revision 是自己已 ACK 的 revision。

## 16. direct_only 必须三层强制执行

### 16.1 API 层

创建或更新：

```text
transport_policy = direct_only
```

必须校验：

```text
ingress client 支持 direct。
target client 支持 direct。
ingress endpoint type 支持 direct。
target endpoint type 支持 direct。
```

### 16.2 Runtime 层

如果 direct 没有连接成功：

```text
runtime_state = error 或 pending
actual_transport != server_relay
```

不得启动 Server relay bridge。

### 16.3 Stream 层

即使 Source Client 主动打开 `server_relay` stream，Server 也必须拒绝：

```go
if tunnel.TransportPolicy == DirectOnly && header.Transport == ServerRelay {
    close(stream)
    recordSecurityEvent()
}
```

只靠“不主动创建 relay”不够。Server 必须防御错误或恶意 Client。

## 17. Capability 协商

Capability 不能只是 boolean。必须表达协议版本、stream header 版本、当前 endpoint 类型和 transport 实现。

Auth 阶段 Client 上报：

```json
{
  "protocol_version": 1,
  "stream_header_version": 1,
  "tunnel_spec_version": 1,
  "ingress_types": [
    "tcp_listen",
    "udp_listen"
  ],
  "target_types": [
    "tcp_service",
    "udp_service"
  ],
  "transport_policies": [
    "server_relay_only",
    "direct_preferred",
    "direct_only"
  ],
  "p2p": {
    "supported": true,
    "impl": "webrtc_ice",
    "supports_ipv6": true,
    "supports_turn": false
  }
}
```

说明：

```text
Client 侧不应在当前版本声明 unix_socket/static_file/serial_device。
Server 看到未知 target type 时必须拒绝创建，而不是按 capability-gated stopped tunnel 保存。
未来新增 endpoint 时，必须同步更新 capability schema、API 校验、adapter、资源锁和测试。
```

Server 必须持久化最近一次 capability：

```sql
ALTER TABLE registered_clients ADD COLUMN last_capabilities TEXT NOT NULL DEFAULT '{}';
```

原因：支持离线创建 tunnel 时，Server 需要知道该 Client 最近是否支持：

```text
tcp_listen
udp_listen
tcp_service
udp_service
direct_preferred / direct_only
```

如果没有 capability 或 capability 不满足，创建或恢复应失败并返回明确错误。

## 18. Adapter 类型体系

不能把所有 adapter 都抽象成 `io.ReadWriteCloser`。当前已知的协议形态至少分为：

```text
TCP: stream。
UDP: packet。
HTTP Host: request/response 路由入口。
```

当前代码只需要实现当前 endpoint 的 adapter 形态；不要为了未来 `static_file` / `serial_device` / `unix_socket` 提前加入不可运行的代码枚举或空 adapter。

### 18.1 Endpoint Kind

```go
type EndpointKind string

const (
    EndpointKindStream EndpointKind = "stream"
    EndpointKindPacket EndpointKind = "packet"
    EndpointKindHTTP   EndpointKind = "http"
)
```

### 18.2 Stream Adapter

```go
type StreamIngressAdapter interface {
    Type() string
    Validate(config json.RawMessage) (NormalizedEndpoint, []ResourceKey, error)
    Start(ctx context.Context, spec TunnelSpec, opener StreamOpener) error
}

type StreamTargetAdapter interface {
    Type() string
    Validate(config json.RawMessage) (NormalizedEndpoint, []ResourceKey, error)
    Dial(ctx context.Context, spec TunnelSpec) (net.Conn, error)
}
```

当前用于：

```text
tcp_listen
tcp_service
```

### 18.3 Packet Adapter

```go
type PacketIngressAdapter interface {
    Type() string
    Validate(config json.RawMessage) (NormalizedEndpoint, []ResourceKey, error)
    StartPacket(ctx context.Context, spec TunnelSpec, opener PacketSessionOpener) error
}

type PacketTargetAdapter interface {
    Type() string
    OpenPacket(ctx context.Context, spec TunnelSpec) (PacketEndpoint, error)
}
```

当前用于：

```text
udp_listen
udp_service
```

### 18.4 HTTP Ingress Adapter

```go
type HTTPIngressAdapter interface {
    Type() string
    RegisterRoute(ctx context.Context, spec TunnelSpec, handler HTTPRouteHandler) error
}
```

当前用于：

```text
http_host
```

当前 `http_host` 的 target 仍是 `tcp_service`，即 Server HTTP 入口把请求转给 Client 上的 TCP HTTP 服务。

### 18.5 未来 adapter 扩展原则

未来新增类型时再补充对应 adapter，不提前暴露当前不可运行的类型：

```text
static_file:
  应使用 ResourceTargetAdapter 或等价资源服务接口，不应硬塞成 Dial()。

serial_device:
  可复用 stream 形态，但必须处理独占、open_mode、串口参数和跨平台设备名。

unix_socket / named_pipe:
  可复用 stream 形态，但必须先定义 Linux/macOS/Windows 支持矩阵。
```

## 19. HTTP 与 client_to_client 规则

直接定为：

```text
http_host 只用于 ingress.location = server。
client_to_client 不支持 http_host ingress。
client_to_client 访问 HTTP 服务时，使用 tcp_listen -> tcp_service 透传。
```

示例：

```text
Client A:127.0.0.1:18080 -> Client B:127.0.0.1:80
```

这是 TCP 隧道，不是 HTTP adapter。

如果未来要支持：

```text
Client A 本地 HTTP Host / HTTP proxy -> Client B HTTP target
```

需要单独设计应用层 HTTP adapter，不混入当前 TCP stream 语义。

## 20. P2P Token 与加密身份绑定

Server 签发的 P2P token 必须绑定双方临时加密身份，不能只绑定 tunnel_id/source/target。

WebRTC/ICE 下，应绑定：

```text
DTLS certificate fingerprint
```

Token payload 概念：

```json
{
  "tunnel_id": "tun_123",
  "revision": 17,
  "p2p_session_id": "p2p_456",
  "source_client_id": "client-a",
  "target_client_id": "client-b",
  "source_ephemeral_pubkey": "...",
  "target_ephemeral_pubkey": "...",
  "source_dtls_fingerprint": "...",
  "target_dtls_fingerprint": "...",
  "allowed_transport": "peer_direct",
  "nonce": "...",
  "expires_at": 1710000000
}
```

握手时必须验证：

```text
对端持有 token 中绑定 pubkey / DTLS fingerprint 对应的私钥或连接身份。
```

不能只验证“对方拿到了 token”。否则 token 被截获或误发时，可能被重放或换 peer 使用。

## 21. P2P Signaling 协议

Signaling 消息：

```text
p2p_session_prepare
p2p_session_ready
p2p_candidate
p2p_connectivity_check
p2p_selected
p2p_failed
p2p_closed
p2p_stats_report
```

所有消息必须带：

```text
tunnel_id
revision
p2p_session_id
from_client_id
to_client_id
sequence
created_at 或 expires_at
```

示例：

```json
{
  "type": "p2p_candidate",
  "payload": {
    "tunnel_id": "tun_123",
    "revision": 17,
    "p2p_session_id": "p2p_456",
    "from_client_id": "client-a",
    "to_client_id": "client-b",
    "candidate": {
      "type": "srflx",
      "protocol": "udp",
      "address": "1.2.3.4",
      "port": 45678
    },
    "sequence": 12,
    "created_at": "2026-05-17T00:00:00Z"
  }
}
```

必须使用 `p2p_session_id`，否则同一 tunnel 多次 retry、Client 重连、旧 signaling 延迟到达时会串消息。


## 21A. WebRTC/ICE 运行细节

### 21A.1 Go dependency

技术路线已定为 WebRTC/ICE。Go 实现建议优先选择 Pion WebRTC，但这仍是 dependency gate，不是产品方向待确认：

```text
实现语言 Go。
支持 ICE/STUN/TURN/DataChannel。
可访问 DTLS fingerprint。
社区和维护度较好。
```

在修改 go.mod 前必须由 dependency-expert 确认：

```text
跨平台 Linux/macOS/Windows 构建。
二进制大小影响。
CI 时间影响。
许可证。
DTLS fingerprint / certificate pinning 能力。
DataChannel backpressure 能力。
```

如果 Pion 被验证不可接受，不能退回“暂不做 direct”。必须重新提交等价的 WebRTC/ICE Go 实现选择，并保持本文所有 direct_only / direct_preferred 语义不变。

### 21A.2 ICE server 配置

Server 管理配置应支持：

```json
{
  "ice_servers": [
    {"urls": ["stun:stun.example.com:3478"]}
  ],
  "allow_public_stun": false,
  "allow_turn": false
}
```

默认策略：

```text
不内置第三方公共 STUN。
未配置 STUN 时，只收集 host candidates；直连成功率较低但隐私更可控。
TURN 初始不启用。
```

### 21A.3 Candidate filtering

```text
direct_only:
  只允许 host/srflx/prflx 形成的 peer_direct。
  relay candidate 必须过滤或即使出现也不得 selected。

direct_preferred:
  初始同样优先 peer_direct。
  不使用 TURN relay 作为 fallback；fallback 是 NetsGo server_relay。
```

Host candidate 隐私：

```text
默认允许局域网 host candidate 参与，因为这是 P2P 直连基础。
如未来需要隐私模式，可增加 hide_host_candidates 配置，但会降低直连成功率。
```

### 21A.4 DataChannel 映射

WebRTC DataChannel 是 message-oriented。NetsGo 必须定义自己的 logical stream framing。

推荐：

```text
每个 P2P session 建立一个可靠有序 control/data DataChannel。
在 DataChannel 上承载 NetsGo logical stream frame。
每个用户 TCP 连接对应一个 logical stream_id。
UDP 使用 packet frame，包含 flow/session key。
```

Frame 类型：

```text
stream_open
stream_data
stream_close
packet_open
packet_data
packet_close
flow_control
ping
pong
```

要求：

```text
TCP logical stream 必须可靠、有序。
UDP packet 可以使用可靠有序 DataChannel 简化首版；未来可评估 unreliable/unordered。
必须有 backpressure / send buffer 上限。
单 frame payload 最大值必须限制，建议 <= 16 KiB，必要时分片。
```

### 21A.5 Observability

必须上报：

```text
candidate types gathered
selected candidate pair type
ICE state
DTLS state
DataChannel state
last_direct_ok
last_direct_error
fallback_since
bytes sent/received by logical direction
```

## 22. direct_preferred 切换语义

### 22.1 Relay 已在跑，Direct 后来成功

```text
已有 server_relay 连接继续走 server_relay，直到自然关闭。
新连接开始走 peer_direct。
```

不得强制迁移已有 TCP stream，避免断开用户连接。

### 22.2 Peer Direct 已在跑，Direct 断开

```text
已有 peer_direct 连接失败。
direct_preferred 下，新连接立即走 server_relay，并后台重试 peer_direct。
direct_only 下，新连接拒绝，runtime_state = error 或 pending。
```

### 22.3 P2P 抖动与退避

需要维护：

```text
p2p_retry_backoff
last_direct_ok
last_direct_error
last_direct_error_reason
fallback_since
```

避免频繁在 direct 与 relay 之间抖动切换。

## 23. 流量统计

新 traffic 表必须按 `tunnel_id`，不能按 name。

```sql
CREATE TABLE traffic_buckets (
    tunnel_id TEXT NOT NULL,
    owner_client_id TEXT NOT NULL,
    ingress_client_id TEXT NOT NULL DEFAULT '',
    target_client_id TEXT NOT NULL DEFAULT '',
    topology TEXT NOT NULL,
    transport TEXT NOT NULL,
    resolution TEXT NOT NULL,
    bucket_start INTEGER NOT NULL,
    ingress_bytes INTEGER NOT NULL DEFAULT 0,
    egress_bytes INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tunnel_id, transport, resolution, bucket_start)
);

CREATE INDEX idx_traffic_owner_query
ON traffic_buckets(owner_client_id, resolution, bucket_start);

CREATE INDEX idx_traffic_ingress_query
ON traffic_buckets(ingress_client_id, resolution, bucket_start);

CREATE INDEX idx_traffic_target_query
ON traffic_buckets(target_client_id, resolution, bucket_start);
```

方向定义：

```text
ingress_bytes:
  入口 → 目标方向 payload bytes。

egress_bytes:
  目标 → 入口方向 payload bytes。
```

P2P direct 流量不经过 Server，因此 Server 无法被动统计。必须由 Client 上报：

```text
P2P direct 流量统计用于展示，不应视为计费级可信数据。
```

建议双端上报：

```text
source_sent_bytes
source_recv_bytes
target_sent_bytes
target_recv_bytes
```

Server 做对账，差异过大时标记：

```text
traffic_report_mismatch
```

## 24. 限速规则

限速方向：

```text
ingress_bps:
  入口 → 目标方向 payload bytes/s。

egress_bps:
  目标 → 入口方向 payload bytes/s。
```

执行位置：

```text
server_relay:
  Server relay bridge enforce。
  Client 可以辅助 enforce，但 Server 是主执行点。

peer_direct:
  Source 和 Target Client enforce。
  Server 不经过业务流量，无法执行限速。
```

Server 下发 `TunnelSpec` 时必须包含限速配置。Client 在 direct transport 上必须执行限速，否则 `direct_only` 会绕过限速。

## 25. 更新与重启策略

更新 tunnel 时必须定义运行态处理规则：

```text
name:
  不重启，只更新显示名和列表。

bandwidth_settings:
  不重启，热更新 limiter。

transport_policy:
  需要重建 transport，不一定重建 ingress/target。

ingress config:
  必须重启 ingress listener。

target config:
  target side reprovision。
  已有连接不迁移，新连接使用新配置。

target type:
  必须完整 reprovision。

topology:
  不允许修改；必须删除重建。

ingress client / target client:
  不允许修改；必须删除重建。
```

任何会影响 provisioning 的更新都必须：

```text
revision += 1
```

## 26. 事件分发与前端展示

`tunnel_changed` 事件不能只发 owner。端到端 tunnel 至少关联：

```text
owner_client_id
ingress_client_id
target_client_id
```

事件应可被以下视角消费：

```text
owner client 页面
ingress client 页面
target client 页面
global dashboard
```

前端展示至少包含：

```text
隧道形态：server_expose / client_to_client
入口：位置、Client、类型、地址/端口/域名
目标：Client、类型、资源
传输策略：server_relay_only / direct_preferred / direct_only
当前路径：server_relay / peer_direct / turn_relay / unknown
P2P 状态：idle / gathering / checking / connected / failed / fallback
聚合运行状态：pending / active / offline / idle / error
participant 状态：ingress 与 target 分别展示
错误信息
```

对于：

```text
transport_policy = direct_preferred
actual_transport = server_relay
p2p_state = fallback
```

前端应展示：

```text
隧道可用，但当前不是直连，已回退中继。
```

对于：

```text
transport_policy = direct_only
p2p_state = failed
```

前端应展示：

```text
直连失败，且该隧道不允许中继，因此当前不可用。
```

## 27. 目标服务健康检查边界

默认不做目标服务健康检查。

允许做的链路层健康：

```text
控制通道是否在线
数据通道是否在线
ingress listener 是否启动
server_relay 是否可用
peer_direct 是否连接
是否已 fallback
隧道运行态是否异常
```

不默认做：

```text
主动请求 Client B 的 HTTP 服务
主动连接 TCP target 判断端口是否开放
未来若实现 static_file，不主动读取用户文件判断业务健康
未来若实现 serial_device，不主动打开串口判断业务健康
```

例外：某些 target adapter 的资源安全校验属于 provisioning 的一部分，例如：

```text
当前 tcp_service / udp_service 不做目标服务健康探测。
未来路径型或设备型 target 可做资源安全校验，但必须在对应 endpoint 设计中单独定义。
```

这些不等同于业务健康检查。

## 27A. 关键实现风险与硬门槛

以下三项不是普通“注意事项”，而是实现计划的硬门槛。任何执行计划、任务拆分或完成声明都必须显式覆盖它们。

### 27A.1 WebRTC/ICE 依赖门槛

技术方向已经定为 WebRTC/ICE，但 Go 依赖必须先过 dependency gate，不能直接把依赖塞进 `go.mod` 后再发现不可交付。

必须在实现 direct transport 前确认：

```text
候选库是否支持 Linux / macOS / Windows 构建。
许可证是否适合项目发布。
二进制体积和 CI 时间是否可接受。
是否能访问 / 绑定 DTLS fingerprint 或等价加密身份。
DataChannel 是否有可用的 backpressure / buffered amount 控制。
STUN/TURN/ICE 配置能力是否满足本文策略。
```

如果 Pion 或首选实现不满足要求，结论不能是“暂不做 direct”。必须选择等价的 Go WebRTC/ICE 实现，继续满足：

```text
direct_preferred 可运行。
direct_only 可运行。
direct_only 不使用 server_relay 或 TURN relay。
```

### 27A.2 UDP direct packet framing 门槛

TCP direct 不能代表 UDP direct 完成。最终完成必须有 UDP 的 peer_direct 数据面。

UDP over WebRTC DataChannel 必须有明确 packet frame，而不是临时复用 TCP stream frame。至少要定义：

```text
frame_type = packet_open / packet_data / packet_close。
tunnel_id。
revision。
flow_id 或 session key。
direction。
source/target endpoint 标识。
payload length 上限。
flow idle timeout。
backpressure / drop / queue 上限策略。
统计字段如何归入 ingress_bytes / egress_bytes。
限速执行位置。
```

首版可以使用可靠有序 DataChannel 简化实现，但必须在文档和测试中明确这是首版选择；未来如果改为 unreliable/unordered，也不能改变 TunnelSpec 和 transport policy 语义。

### 27A.3 `direct_only` 三层拒绝中继门槛

`direct_only` 是安全语义，不只是路由偏好。必须在三层同时 enforce：

```text
API 层：不满足 direct capability / endpoint 支持时拒绝创建或更新。
Runtime 层：direct 未建立时不得启动 server relay fallback。
Stream 层：即使 Client 主动打开 server_relay stream，Server 也必须拒绝并记录安全事件。
```

完成声明必须包含对应证据：

```text
API 拒绝用例。
runtime 不 fallback 用例。
伪造 / 错误 server_relay stream 被拒用例。
```

## 28. 实现组织建议

虽然产品目标是一步到位，但代码内部仍应按依赖顺序组织：

```text
1. 定义 TunnelSpec、EndpointSpec、runtime 枚举。
2. 重建 tunnels schema 和 resource_locks。
3. 实现 endpoint config normalize/validate。
4. 实现 ResourceKey 冲突检测。
5. 实现 /api/tunnels 和 client role 列表 API。
6. 实现 capability 上报与持久化。
7. 实现 tunnel_provision / ACK / revision coordinator。
8. 让 server_expose TCP/UDP/HTTP 基于 TunnelSpec 跑通。
9. 实现 Source Client 本地 tcp/udp listen。
10. 实现 Client 主动 OpenStream 与 Server AcceptLoop 鉴权。
11. 实现 client_to_client TCP/UDP server_relay。
12. 引入 WebRTC/ICE P2P signaling。
13. 实现 P2P token 与 DTLS fingerprint 绑定。
14. 实现 peer_direct TCP logical stream 数据面。
15. 实现 peer_direct UDP packet 数据面。
16. 实现 direct_preferred fallback 与 direct_only 三层拒绝 server_relay。
17. 实现 traffic_report 和 P2P 流量统计。
18. 实现 direct 下 Client 限速。
19. 更新前端展示和创建表单。
20. 补齐测试矩阵。
```

这不是分阶段上线要求，而是内部工程依赖顺序。最终完成必须同时覆盖 TCP 与 UDP 的端到端 `server_relay_only`、`direct_preferred`、`direct_only`。

未来 `unix_socket` / `static_file` / `serial_device` 不在当前实现顺序中；当前只需要保证 adapter registry、endpoint validation、resource lock 和 capability schema 以后可以扩展，而不是现在暴露不可运行的类型。

因此实施状态必须用以下标签区分：

```text
contract_ready:
  schema/protocol/API 形状已定义，但功能不能宣称完成。

relay_ready:
  server_expose 与 client_to_client 的 TCP/UDP server_relay_only 可运行，但 direct 策略未完成。

direct_ready:
  TCP/UDP direct_preferred 与 direct_only 可运行，direct_only 三层拒绝中继已验证。

complete:
  relay_ready + direct_ready + 前端/统计/安全/反代验证全部通过。
```

对用户可见的完成声明只能使用 `complete`。`contract_ready` 和 `relay_ready` 都是内部工程状态。

## 29. 测试矩阵

### 29.1 Schema 与模型完整性

```text
新 tunnels 表字段和 CHECK 约束正确。
resource_locks 冲突时事务回滚。
TunnelSpec JSON roundtrip 正确。
Endpoint config normalize 后索引列正确。
owner_client_id 由 Server 推导，API 不能伪造。
```

### 29.2 server_expose

```text
Server TCP listen → Client tcp_service。
Server UDP listen → Client udp_service。
Server HTTP Host → Client tcp_service。
端口冲突拒绝。
域名冲突拒绝。
Client 离线 runtime_state = offline。
Client 重连后 revision ACK 正确恢复。
```

### 29.3 client_to_client server_relay

```text
Client A TCP listen → Server relay → Client B tcp_service。
Client A UDP listen → Server relay → Client B udp_service。
Source offline。
Target offline。
Source bind conflict。
Target resource conflict。
Source reconnect。
Target reconnect。
Server restart。
```

### 29.4 direct / P2P

```text
TCP peer_direct 成功。
UDP peer_direct 成功。
peer_direct 失败。
direct_preferred fallback 到 server_relay。
direct_only 失败后不可用。
direct_only 下 server_relay stream 被拒。
relay 已有连接不因 direct 成功被强制迁移。
direct 断开后 direct_preferred 新连接走 server_relay。
direct retry backoff 生效。
```

### 29.5 安全

```text
Source 伪造 tunnel_id 被拒。
Source 打开非自己 ingress 的 tunnel stream 被拒。
open_token 过期被拒。
open_token revision 错误被拒。
open_token stream_id 不匹配被拒。
旧 revision ACK 被丢弃。
P2P token 重放被拒。
P2P token DTLS fingerprint 不匹配被拒。
```

### 29.6 Resource 与当前 endpoint

```text
tcp listen 冲突。
udp listen 冲突。
http domain 冲突。
未来 target type（unix_socket/static_file/serial_device）当前提交时被拒绝为 unknown/unsupported，不会写入 tunnels 表。
```

### 29.7 反向代理路径

涉及 `/ws/control`、`/ws/data`、signaling、数据通道时，需要验证：

```text
直连 Server
nginx
caddy
```


## 29A. 验证命令与完成证据

最小本地验证按改动面执行：

```text
协议/后端局部：
  go test ./pkg/protocol ./internal/server ./internal/client

全后端：
  go test ./...

前端：
  cd web && bun run build
  cd web && bun run lint

嵌入资源/发布构建：
  make build
```

由于非 dev Go 测试可能依赖 `web/dist`，完整验证应遵循 CI 心智模型：

```text
cd web && bun run lint
cd web && bun run build
go vet ./...
go test ./...
```

完成声明必须包含：

```text
通过的测试命令。
未运行的验证及原因。
已覆盖的隧道组合。
已覆盖的安全拒绝用例。
已覆盖的迁移/重启场景。
```

## 30. 最终结论

最终方向必须坚持：

```text
Tunnel = Ingress + Target + Transport
```

并且一步到位采用：

```text
TunnelSpec 作为唯一模型。
/api/tunnels 作为创建入口。
tunnels 统一表。
resource_locks 统一冲突检测。
revision + 双端 ACK 保证 provisioning 一致性。
DataStreamHeader + open_token 保证 Client 主动 stream 安全。
WebRTC/ICE 作为 P2P 技术路线。
server_relay_only / direct_preferred / direct_only 作为传输策略。
unknown / server_relay / peer_direct / turn_relay 作为实际路径。
active 取代旧 exposed。
当前代码只暴露 tcp_service / udp_service；unix_socket / static_file / serial_device 仅为未来扩展示例。
硬删除作为 tunnel 删除语义，traffic history 需处理 metadata_missing。
```

一句话总结：

```text
不要再把隧道理解成“Server 帮某个 Client 暴露 local_ip:local_port”。
NetsGo 的隧道应统一理解为“一个入口 endpoint 到一个目标 endpoint 的连接资源，并由明确的 transport policy 决定业务流量路径”。
```
