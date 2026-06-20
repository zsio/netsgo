# SOCKS5 Tunnel Implementation RFC

> Status: Final design draft
> Date: 2026-06-20
> Supersedes: [`docs/socket-tunnel-storage-design.md`](./socket-tunnel-storage-design.md)

## 0. 结论

NetsGo 应在统一隧道模型中新增 SOCKS5 CONNECT 隧道能力。首个完整实现边界是：

```text
server_expose + server/socks5_listen -> client/socks5_connect_handler
client_to_client + ingress-client/socks5_listen -> target-client/socks5_connect_handler
transport_policy = server_relay_only
SOCKS5 command = CONNECT only
```

这不是“完整 SOCKS5 全命令集”，而是 **SOCKS5 CONNECT over TCP reverse tunnel**。

本次实现必须把 CONNECT 路径做完整、可恢复、可测试；不应把与 SOCKS5 无直接依赖的治理项混进同一个变更，例如 `runtime_state active/exposed` 统一、v1/v2 API 大统一、资源锁 FK/CHECK 硬化、索引清理、P2P 占位代码清理等。这些问题记录在 [`docs/issues.md`](./issues.md)。

## 0.1 当前代码根因与修改入口

本 RFC 不假设读者已经知道现有实现。当前 SOCKS5 不能直接加入的根因如下：

| 范围 | 当前根因 | 主要代码位置 | 本次处理 |
|---|---|---|---|
| DB endpoint type | SQLite `CHECK` 只允许 TCP/UDP/HTTP endpoint | `internal/server/migrations/005_unified_tunnel_storage.sql` | 新增迁移，扩展 CHECK 到 `socks5_listen` / `socks5_connect_handler` |
| API endpoint 校验 | endpoint/topology 组合写死在 unified API 的 if-else 中 | `internal/server/unified_tunnel_api.go` 的 `validateUnifiedEndpointCombination` | 增加 `server_expose` 与 `client_to_client` 的 SOCKS5 合法组合，建议抽兼容矩阵 |
| resource lock | ingress lock 只处理 `tcp_listen` / `udp_listen` / `http_host` | `internal/server/store.go` 的 `tunnelIngressResourceLock` | SOCKS5 与 TCP listen 竞争同一 bind ip + port；client ingress 侧也要复用 TCP listen 资源语义 |
| client capabilities | 默认 client 只声明 TCP/UDP ingress/target 能力 | `pkg/protocol/types.go` 的 `DefaultClientCapabilities` | 增加 `socks5_listen` 与 `socks5_connect_handler` 能力；server 对旧 client 拒绝创建/下发 |
| provisioning | `ProxyNewRequest` 是固定目标 flat 模型 | `pkg/protocol/message.go`、`internal/client/unified_tunnel.go` | 本次必须让 provisioning 能表达 SOCKS5 handler config；不依赖 `local_ip/local_port` |
| stream header | 数据流 header 没有 per-stream 动态目标字段 | `pkg/protocol/stream_header.go` | 增加 `target_host` / `target_port` 等字段，并保持 capability gate |
| server runtime | 现有 server ingress 只有 TCP/UDP/HTTP 处理 | `internal/server/proxy.go`、`internal/server/server_expose_unified.go`、相关 data/open stream 路径 | 新增 SOCKS5 listener、握手、CONNECT、reply、relay |
| c2c relay | `client_to_client` 当前只处理中继固定 TCP/UDP 流 | `internal/server/client_relay.go`、`internal/client/unified_tunnel.go` | ingress client 处理 SOCKS5 handshake/CONNECT，再通过 server relay 打开带动态目标的 target stream |
| client runtime | client `handleStream` 只面向固定 TCP/UDP target，client ingress 只监听 TCP/UDP | `internal/client/client.go`、`internal/client/unified_tunnel.go` | 新增 `socks5_listen` ingress runtime 与 `socks5_connect_handler` target 分支，执行 target policy 与 dial |
| frontend | 表单和模型只表达 TCP/UDP/HTTP 固定目标 | `web/src/lib/tunnel-model.ts`、`web/src/components/custom/tunnel/` | 新增 SOCKS5 表单、错误码、capability gate、脱敏展示 |

实施者应先核对这些代码位置，再开始修改；如果代码已变化，以当前代码为准更新本 RFC。

## 0.2 本次范围裁决

| 项目 | 本次裁决 | 原因 |
|---|---|---|
| SOCKS5 CONNECT | 必做 | 核心功能 |
| SOCKS5 username/password auth | 必做 | RFC 1929 支持与访问控制要求 |
| password hash / 脱敏 | 必做 | 不做会形成安全债 |
| SOCKS5 source allowlist | 必做 | 防止公开入口无边界暴露 |
| SOCKS5 target allowlist | 必做 | 防止 SOCKS5 变成开放代理 |
| TCP/UDP/HTTP source allowlist | 必做 | 同属 ingress source policy，本次应与 SOCKS5 一起补齐 |
| HTTP Basic Auth hashing/脱敏共用工具 | 应做 | 与 SOCKS5 username/password 同类凭据，不应两套实现 |
| endpoint CHECK 扩展 | 必做 | DB blocker |
| endpoint CHECK 完全放松 | 后续 | 需要全写路径 Go 校验兜底 |
| SOCKS5/TCP 端口互斥 | 必做 | 不做会产生端口冲突 bug |
| resource locks FK/CHECK 硬化 | 后续 | 涉及脏数据迁移策略 |
| provisioning 表达 SOCKS5 config | 必做 | client 否则无法执行 target policy |
| 彻底清理 legacy `ProxyNewRequest` | 后续 | 范围较大，可独立治理 |
| `runtime_state active/exposed` 统一 | 后续 | 与 SOCKS5 无直接依赖，迁移风险独立 |
| v1/v2 API 大统一 | 后续 | SOCKS5 可只支持 v2 |
| SOCKS5 UDP ASSOCIATE | 后续 | 独立命令和数据面语义 |
| c2c SOCKS5 | 必做 | 用户需要 client 到 client 的 SOCKS5 入口；握手位置不同但可复用同一 endpoint 语义 |
| P2P data transport policy | 后续 | 独立数据通道大设计 |

## 1. 正确运行模型

SOCKS5 server 角色必须在流量进入的 ingress 侧完成；`socks5_connect_handler` target 侧只处理已解析的动态 CONNECT 目标。

`server_expose` 模型：

```text
External user
  -> Server socks5_listen
     - method negotiation
     - RFC 1929 username/password auth
     - CONNECT request parsing
     - source allowlist check
  -> yamux data stream with target_host/target_port
  -> Client socks5_connect_handler
     - target allowlist check
     - dial target
     - return dial result with bound address
  -> Target service
```

`client_to_client` 模型：

```text
External or local user
  -> Ingress Client socks5_listen
     - method negotiation
     - RFC 1929 username/password auth
     - CONNECT request parsing
     - source allowlist check
  -> yamux data stream to Server relay with target_host/target_port
  -> Target Client socks5_connect_handler
     - target allowlist check
     - dial target
     - return dial result with bound address
  -> Target service
```

server 在 c2c SOCKS5 中负责控制面下发、能力门禁、状态聚合和数据流中继，不负责解析外部用户的 SOCKS5 握手。

### 1.1 不支持的范围

首期不支持：

- SOCKS5 `BIND`
- SOCKS5 `UDP ASSOCIATE`
- P2P SOCKS5 data transport
- server-side target

`BIND` 与 `UDP ASSOCIATE` 必须返回 RFC 1928 `REP=0x07 command not supported`。

## 2. Endpoint 模型

新增 endpoint type：

```text
IngressTypeSOCKS5Listen = "socks5_listen"
TargetTypeSOCKS5ConnectHandler = "socks5_connect_handler"
```

首期合法组合：

| 字段 | 值 |
|---|---|
| `topology` | `server_expose` |
| `ingress.location` | `server` |
| `ingress.type` | `socks5_listen` |
| `target.location` | `client` |
| `target.type` | `socks5_connect_handler` |
| `transport_policy` | `server_relay_only` |

| 字段 | 值 |
|---|---|
| `topology` | `client_to_client` |
| `ingress.location` | `client` |
| `ingress.client_id` | ingress client |
| `ingress.type` | `socks5_listen` |
| `target.location` | `client` |
| `target.client_id` | target client |
| `target.type` | `socks5_connect_handler` |
| `transport_policy` | `server_relay_only` |

不允许：

```text
tcp_listen -> socks5_connect_handler
socks5_listen -> tcp_service
server-side socks5_connect_handler
```

SOCKS5 解析发生在 ingress 所在位置；ingress 必须是 `socks5_listen`，不能把普通 TCP listener 和 SOCKS5 listener 混为一谈。

`socks5_connect_handler` 这个 target type 名称是有意选择的：target 侧不是 SOCKS5 server，也不处理外部用户的 SOCKS5 method negotiation/auth/CONNECT request。它只执行 ingress 已解析出的 SOCKS5 CONNECT 动态目标：校验 target policy、dial 目标、返回 dial result、参与 relay。名称中包含 `connect` 是为了避免读者误以为首期支持完整 SOCKS5 server、BIND 或 UDP ASSOCIATE。

### 2.1 兼容规则来源

本次应把 endpoint 组合规则集中到一个 Go 端校验函数中，避免继续散落在 API if-else 里。建议位置：`pkg/protocol/` 或 server 内部共享校验包。若放到 `pkg/protocol/`，server 和 client 可以共享协议语义；前端仍需要 TypeScript 镜像或后续 schema/codegen。

首期兼容矩阵：

```text
server_expose:
  tcp_listen    -> tcp_service
  udp_listen    -> udp_service
  http_host     -> tcp_service
  socks5_listen -> socks5_connect_handler

client_to_client:
  tcp_listen    -> tcp_service
  udp_listen    -> udp_service
  socks5_listen -> socks5_connect_handler
```

不得首期加入 `tcp_listen -> socks5_connect_handler` 或 `socks5_listen -> tcp_service`。

## 3. Access control 与配置位置

本次 SOCKS5 实现不能把 auth / allowlist 设计成 SOCKS5 私有补丁。正确模型应分为：

```text
IngressAccessPolicy:
  allowed_source_cidrs
  auth
  connection_limits
  rate_limits

TargetAccessPolicy:
  allowed_target_cidrs
  allowed_target_hosts
  allowed_target_ports
  dial_timeout
```

`IngressAccessPolicy` 回答“谁可以使用这个 tunnel 入口”，适用于 TCP/UDP/HTTP/SOCKS5。`TargetAccessPolicy` 回答“这个 tunnel 可以访问哪些目标”，对 SOCKS5 这类动态目标代理是本期必做能力；普通 TCP/UDP/HTTP 固定目标主要在创建/更新时做 target config 校验。

本次应把清晰的通用 ingress access control 一并补齐：

- SOCKS5 的 `allowed_source_cidrs`、username/password auth、target allowlist 必须实现；
- TCP/UDP/HTTP 的 `allowed_source_cidrs` 属于同一类 ingress source policy，本次必须一并支持；
- HTTP Basic Auth 与 SOCKS5 username/password 应复用同一套 password hashing 与脱敏机制；
- 通用 secret store / secret rotation / secret ID 引用可以后续单独治理，但不能作为本期明文存储 password 的理由。

### 3.1 ingress/server 配置

`server_expose` 的 `ingress_config`：

```json
{
  "bind_ip": "0.0.0.0",
  "port": 6005,
  "auth": {
    "type": "socks5_username_password",
    "username": "user",
    "password_hash": "$argon2id$..."
  },
  "allowed_source_cidrs": ["203.0.113.0/24"]
}
```

server 侧负责执行：

- bind/listen
- source CIDR 检查
- SOCKS5 method negotiation
- username/password auth
- password hash 校验与所有输出脱敏
- CONNECT request parsing
- 对外返回 RFC 1928 reply

`client_to_client` 的 `ingress_config` 使用同一结构，但由 ingress client 执行 listen、source CIDR、SOCKS5 auth、CONNECT 解析和 SOCKS5 reply。server 只负责把该配置下发给 ingress client 并聚合运行态。

### 3.2 target/client 配置

`target_config`：

```json
{
  "allowed_target_cidrs": ["10.0.0.0/8", "192.168.0.0/16"],
  "allowed_target_hosts": ["db.internal.example"],
  "allowed_target_ports": [80, 443, 5432],
  "dial_timeout": 10
}
```

client 侧负责执行：

- target host/port 策略检查
- DNS 解析
- target CIDR 检查
- dial timeout
- 实际连接目标服务
- 返回 dial result

## 4. SOCKS5 协议要求

### 4.1 Method negotiation

server 必须按 RFC 1928 处理：

1. external user 发送：`VER=0x05, NMETHODS, METHODS...`
2. server 选择 method 并返回：`VER=0x05, METHOD`
3. 无可接受 method 时返回 `METHOD=0xFF` 并关闭连接。

支持 method：

- `0x00` no authentication required
- `0x02` username/password

### 4.2 Username/password auth

选择 `0x02` 时，server 必须按 RFC 1929 处理：

```text
client -> server: VER=0x01, ULEN, UNAME, PLEN, PASSWD
server -> client: VER=0x01, STATUS
```

`STATUS != 0x00` 时必须关闭连接。

### 4.3 CONNECT request

支持 address type：

- IPv4 (`ATYP=0x01`)
- domain (`ATYP=0x03`)
- IPv6 (`ATYP=0x04`)

不支持的 address type 返回 `REP=0x08 address type not supported`。

### 4.4 CONNECT reply

对外 reply 必须符合 RFC 1928：

```text
VER | REP | RSV | ATYP | BND.ADDR | BND.PORT
```

不能只返回内部状态码。

成功时，`BND.ADDR` / `BND.PORT` 应来自 client 实际 dial 目标时的本地 socket 地址和端口。失败时按内部错误映射为 RFC `REP`。

## 5. server/client 内部握手

server 在完成 CONNECT request 解析后打开 data stream，并在 `DataStreamHeader` 中传递动态目标：

```go
type DataStreamHeader struct {
    // existing fields...
    TargetHost     string `json:"target_host,omitempty"`
    TargetPort     int    `json:"target_port,omitempty"`
    TargetAddrType string `json:"target_addr_type,omitempty"`
    OriginalHost   string `json:"original_host,omitempty"`
}
```

`target_host` / `target_port` 对 `socks5_connect_handler` 必填。`target_addr_type` / `original_host` 可用于保留 domain、IPv4、IPv6 语义。

client dial result 不能只是一字节，至少需要：

```json
{
  "status": "success | target_denied | network_unreachable | host_unreachable | connection_refused | dial_timeout | general_failure",
  "bound_addr": "192.0.2.10",
  "bound_port": 49152
}
```

server 根据该结果生成标准 SOCKS5 reply。

## 6. 地址和策略语义

### 6.1 Domain

domain target 必须先规范化：

- lower-case
- IDNA / punycode 归一化
- 不定义 wildcard，除非另有明确设计

`allowed_target_hosts` 对规范化后的 host 做精确匹配。

### 6.2 CIDR

`allowed_target_cidrs` 对 client 实际解析/拨号得到的 IP 生效。server 可以对 literal IP 做早期拒绝，但最终授权必须在 client 侧执行，因为 client 侧 DNS 解析结果才是实际连接目标。

IPv4 与 IPv6 CIDR 都应支持。

### 6.3 Port

`allowed_target_ports` 对 CONNECT 目标端口做数值精确匹配。

### 6.4 默认策略

`allowed_source_cidrs` 与 `allowed_target_*` 的产品默认值确定为“允许所有”。但这不能通过缺省字段或空数组隐式表达，必须在 UI/API 中作为显式配置处理：

- 前端创建表单默认填入“允许所有”的表达；
- allowlist 字段对支持该策略的 tunnel type 必填；
- 用户可以自行收窄为 CIDR、host、port 列表；
- API 必须能区分“用户显式选择允许所有”和“字段缺失/客户端未填写”；
- 文案必须提示：允许所有来源或所有目标有安全风险，尤其 SOCKS5 target allow-all 可能形成开放代理。

建议使用明确表达而不是空数组，例如：

```json
{
  "allowed_source_cidrs": ["0.0.0.0/0", "::/0"],
  "allowed_target_cidrs": ["0.0.0.0/0", "::/0"]
}
```

如果最终实现选择使用 `allow_all: true` 之类结构化字段，也必须保证 API 和 UI 能明确展示这是用户选择的 allow-all。

## 7. 安全要求

### 7.1 no-auth 风险

`auth_required=false` 等于任何能连接监听端口的人都能使用代理。它只能用于受控网络、localhost、VPN/TLS 外层保护等场景。UI/API 应明确提示风险。

`server_expose` 的 SOCKS5 创建/更新表单如果未配置认证，必须要求用户勾选显式确认复选框后才能提交。建议文案：

```text
我知道未启用认证会让可访问该端口的人使用此代理。
```

该确认是一次提交行为，不应作为长期安全状态存入 `ingress_config`。创建/更新请求应在 `ingress.config` 之外携带类似 `confirm_no_auth_risk=true` 的提交级字段；API 必须在 `server_expose + socks5_listen` 且无认证时校验该字段，避免绕过前端直接提交时缺少显式确认。

### 7.2 密码存储与脱敏

本次不得把 SOCKS5 password 或 HTTP Basic Auth password 以可直接恢复的明文形式写入 `ingress_config`、日志、事件、API 响应或诊断导出。

本次最低要求：

- 存储 `password_hash`，不存明文 `password`；
- 使用 Argon2id 作为密码哈希方案；
- 创建/更新时可接收明文 password，但只用于生成 hash；
- 列表、详情、事件、导出、日志永远不返回明文 password；
- 更新 password 只能重新设置，不能读取旧值；
- HTTP Basic Auth 与 SOCKS5 username/password 复用同一套 credential hashing / verify / redact 工具。

RFC 1929 username/password 子协商本身不是加密协议，公网暴露时仍应依赖 TLS/VPN 或其他安全边界。通用 secret store / secrets table / secret rotation 属于后续独立治理，记录在 [`docs/issue/secrets-management.md`](./issue/secrets-management.md)，但该 issue 不允许本期明文存储 password。

## 8. 存储迁移

当前 SQLite schema 中 endpoint type 有硬编码 CHECK。SOCKS5 必须迁移。

本次推荐扩展 CHECK，而不是完全放松：

```sql
CHECK (ingress_type IN ('tcp_listen', 'udp_listen', 'http_host', 'socks5_listen')),
CHECK (target_type IN ('tcp_service', 'udp_service', 'socks5_connect_handler'))
```

原因：

- 当前项目 schema 风格是 DB 层保护结构正确性；
- 本次只新增已知 endpoint type；
- 完全放松 CHECK 需要先把所有写路径统一到 Go 校验，否则会失去 DB 兜底。

长期 endpoint extensibility 记录在 [`docs/issue/endpoint-type-extensibility.md`](./issue/endpoint-type-extensibility.md)。

迁移必须保持旧 TCP/UDP/HTTP tunnel 字段和语义不变。

### 8.1 迁移验证边界

迁移测试必须覆盖：

- 旧 TCP/UDP/HTTP rows 完全保留；
- 原有 `id`、`revision`、`desired_state`、`runtime_state`、`transport_policy`、流量相关字段不被意外改写；
- 新 endpoint type 可插入、读取、重启恢复；
- migration 失败时 SQLite transaction 回滚；
- fresh DB 与旧 DB 升级两条路径都通过 schema validation。

## 9. 资源锁

SOCKS5 监听端口本质是 TCP listen port，必须与同一位置的普通 TCP tunnel 互斥。

推荐资源 key 语义：

```text
ingress:server:tcp:<bind_ip>:<port>
ingress:client:<client_id>:tcp:<bind_ip>:<port>
```

也就是说，`tcp_listen` 与 `socks5_listen` 应在 server 或同一个 ingress client 上竞争同一个 TCP 端口资源，而不是拆成互不冲突的 `*_tcp_port` / `*_socks5_port`。

如果实现上需要 `resource_kind` 区分展示，可以额外记录 kind，但冲突判断必须基于同一个 bind ip + port 资源。

## 10. 能力门禁与协议兼容

`ClientCapabilities` 必须增加：

```text
IngressTypes: socks5_listen
TargetTypes: socks5_connect_handler
```

server 必须拒绝向未声明相应能力的 client 创建或下发 SOCKS5 tunnel：

- `server_expose` target client 必须支持 `socks5_connect_handler`；
- `client_to_client` ingress client 必须支持 `socks5_listen`；
- `client_to_client` target client 必须支持 `socks5_connect_handler`。

`DecodeDataStreamHeader` 当前使用 `DisallowUnknownFields` 是合理防御，不应为了新字段直接放弃。新字段加入结构体后，旧 client 仍不能收到 SOCKS5 stream；这依赖 capability gate 保证。

## 11. Provisioning payload

当前代码已经存在 unified provisioning 消息：`TunnelProvisionRequest{TunnelID, Revision, Role, Spec TunnelSpec}`。这是正确方向，因为 `TunnelSpec` 已经包含 `Ingress` / `Target` / `TransportPolicy`。

问题根源不是“没有 TunnelSpec 下发”，而是 target role 的 client 处理逻辑会把 `TunnelSpec` 再转换成旧式 `ProxyNewRequest`：

```text
TunnelProvisionRequest.Spec -> proxyRequestFromTunnelSpec -> ProxyNewRequest
```

`ProxyNewRequest` 是旧式 flat 结构，适合固定 TCP/UDP/HTTP target，不适合 SOCKS5 dynamic target。

本次推荐方向：

1. 继续使用现有 unified `TunnelProvisionRequest` 下发 `TunnelSpec`；
2. 将 `TunnelProvisionRequest.Spec` 明确为 v2/unified provisioning 的 canonical schema；
3. 修改 client unified tunnel 处理逻辑，不再把所有 role/endpoint 都强制降级为 `ProxyNewRequest`；
4. 对 SOCKS5 ingress/target 从 `TunnelSpec` 构造 endpoint-specific runtime config，或直接保存经过验证的 `TunnelSpec`；
5. legacy `ProxyProvisionRequest = ProxyNewRequest` 仅保留给旧 `MsgTypeProxyProvision` wire path，不再作为 unified tunnel 的 canonical provisioning schema；
6. `ProxyNewRequest` 本期不得为了 SOCKS5 增加动态 target、target policy、auth 或 access policy 字段。

不建议新增一个与 `TunnelSpec` 平行的 `ProxyProvisionPayload` 协议消息，除非后续要专门清理 legacy v1 provisioning；否则会增加第三套表达。该独立治理项记录在 [`docs/issue/proxy-provision-payload-split.md`](./issue/proxy-provision-payload-split.md)。

本次必须解决的是：server -> client provisioning 能完整表达 SOCKS5 ingress config、target config 和 access policy，且 SOCKS5 runtime 不依赖 `ProxyNewRequest` / `local_ip` / `local_port` 语义。彻底清理 legacy `ProxyNewRequest` 的所有历史用途可以后续单独做，但本次必须先切断 unified SOCKS5 对旧 flat 模型的依赖，并把 `ProxyNewRequest` 的剩余用途限定为 legacy-only。

### 11.1 最低可接受 payload 能力

使用 `TunnelProvisionRequest.Spec` 时，client 至少必须从 spec 中验证并提取：

```text
tunnel_id
revision
role = ingress | target
ingress_type = socks5_listen
ingress bind_ip/port/auth/allowed_source_cidrs
target_type = socks5_connect_handler
allowed_target_cidrs
allowed_target_hosts
allowed_target_ports
dial_timeout
bandwidth_settings
transport_policy
```

ingress runtime 与 target runtime 都必须按 `tunnel_id + revision` 校验 stream header，避免旧 provision 继续处理新 stream。

## 12. 本次不混入的独立治理项

以下问题不应作为 SOCKS5 CONNECT PR 的前置 blocker：

- `runtime_state active/exposed` 统一；
- v1/v2 API 大统一；
- `tunnel_resource_locks` FK/CHECK 硬化；
- endpoint type 完全可扩展化；
- 通用 secret store / secrets table / secret rotation；
- SOCKS5 UDP ASSOCIATE；
- P2P data transport policy 实现；
- legacy stream header 清理；
- P2P 占位代码清理。

以下小修复或局部能力应随本次一起完成，避免后续补丁更分散：

- SOCKS5 provisioning 表达力；
- client_to_client SOCKS5 provisioning、ingress runtime 与 target relay；
- password hash 与脱敏；
- SOCKS5/TCP 端口资源互斥；
- endpoint CHECK 扩展到 SOCKS5 已知类型；
- 前端错误码与新增 endpoint/capability 错误文案对齐；
- TCP/UDP/HTTP 的 `allowed_source_cidrs` 按同一 ingress access policy 一并补齐。

这些问题见 [`docs/issues.md`](./issues.md)。

## 13. 验证清单

### 13.1 数据库

- 旧 TCP tunnel 迁移后字段不变；
- 旧 UDP tunnel 迁移后字段不变；
- 旧 HTTP tunnel 迁移后字段不变；
- 新 `socks5_listen` / `socks5_connect_handler` 可插入、读取、恢复；
- TCP tunnel 与 SOCKS5 tunnel 不能监听同一 bind ip + port；
- 两个 SOCKS5 tunnel 不能监听同一 bind ip + port。
- 同一 ingress client 上的 TCP tunnel 与 SOCKS5 tunnel 不能监听同一 bind ip + port。

### 13.2 协议与能力

- 未声明 `socks5_connect_handler` 的 client 被拒绝；
- c2c ingress client 未声明 `socks5_listen` 时被拒绝；
- 声明 `socks5_connect_handler` 的 client 可接收 SOCKS5 provisioning；
- c2c ingress client 可接收 `socks5_listen` provisioning，target client 可接收 `socks5_connect_handler` provisioning；
- `DataStreamHeader` 新字段 round-trip；
- 普通 TCP/UDP/HTTP stream header 不受影响；
- `DisallowUnknownFields` 行为有测试覆盖。
- provisioning payload 能完整携带 SOCKS5 handler config，不依赖固定 `local_ip/local_port`。

### 13.3 server runtime

- no-auth 正常；
- username/password auth 正常；
- auth 失败关闭连接；
- `BIND` / `UDP ASSOCIATE` 返回 command not supported；
- IPv4/IPv6/domain CONNECT 正常；
- allowed_source_cidrs 生效；
- client offline/data channel missing 时返回 SOCKS5 failure，不挂住连接；
- client dial 失败映射为合理 RFC REP；
- 成功 reply 包含 BND.ADDR/BND.PORT。

### 13.4 c2c ingress runtime

- ingress client no-auth 正常；
- ingress client username/password auth 正常；
- auth 失败关闭连接；
- `BIND` / `UDP ASSOCIATE` 返回 command not supported；
- IPv4/IPv6/domain CONNECT 正常；
- allowed_source_cidrs 在 ingress client 生效；
- target client offline/data channel missing 时返回 SOCKS5 failure，不挂住连接；
- target dial 失败映射为合理 RFC REP；
- 成功 reply 包含 BND.ADDR/BND.PORT。

### 13.5 target client runtime

- literal IPv4 allow/deny；
- literal IPv6 allow/deny；
- domain 解析后 CIDR 检查；
- host allowlist 生效；
- port allowlist 生效；
- dial timeout 生效；
- 成功后双向 relay。

### 13.6 前端/API

- 创建 SOCKS5 tunnel 的 spec 正确；
- 表单不要求固定 local_ip/local_port；
- 不支持 SOCKS5 的 client 不允许创建，或 API 返回明确错误；
- password 不在 UI/API/日志中明文回显；
- password 更新只能重新设置，不显示旧值；
- endpoint/capability/access-control 错误码有明确文案。
- allowlist 字段在 UI 中必填，默认值明确展示为允许所有，用户可自行收窄。
- server_expose SOCKS5 未配置认证时，必须勾选风险确认复选框才能提交。

### 13.7 回归

- 现有 TCP tunnel 创建、停止、恢复、删除；
- 现有 UDP tunnel 创建、停止、恢复、删除；
- 现有 HTTP tunnel 路由；
- server 重启后 SOCKS5 tunnel 恢复；
- client 重连后 SOCKS5 tunnel 恢复；
- c2c SOCKS5 的 stop/resume/delete/reconnect 行为正确。
