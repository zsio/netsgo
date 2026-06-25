# Proxy Provision Payload Split Plan

## 0. 目的

本文档是 `ProxyNewRequest` 与 unified tunnel runtime/provisioning 拆分的实现契约。

这不是一个小范围 client target cleanup。本次目标是一次性完成边界切分：

- `TunnelSpec` 是所有 unified tunnel create / provision / restore / reconcile / runtime 路径的主模型。
- `ProxyNewRequest` 只保留为 legacy 兼容 payload 或当前 SQLite 存储投影。
- unified runtime 代码不得把 `ProxyNewRequest` 当成中间配置模型。
- 现有用户必须继续可用：当前 client、老 client、当前 server、老 server、历史持久化 tunnel 都必须能正常使用。

如果实现过程中需要改变这些边界，必须先更新本文档。不要先在代码里绕开歧义。

## 1. 不可变决策

### 1.0 术语与判定词

本文档里的术语按下面含义使用，后续实现和 review 不得自行换义：

- legacy flat payload：`ProxyNewRequest` / `ProxyCreateRequest` / `ProxyProvisionRequest` 的 flat JSON shape，字段包括 `id`、`name`、`type`、`local_ip`、`local_port`、`remote_port`、`domain` 等。
- unified provision payload：`TunnelProvisionRequest{TunnelID, Revision, Role, Spec TunnelSpec}`。
- unified code path：create / provision / restore / reconcile / runtime 中以 `TunnelSpec` 或 endpoint-specific runtime config 为源模型的路径。
- endpoint-specific runtime config：从 `EndpointSpec` 解码出的 role-specific 配置，例如 TCP service target config、HTTP host ingress config、SOCKS5 listen config。
- storage projection：为了兼容当前 SQLite schema 和 API 投影而保留的 flat 字段。storage projection 不是 runtime source of truth。

`clean reject` 是可测试语义，不是口号。它必须同时满足：

- 如果 server 在发送 provision 前发现 capability 或 endpoint 不支持：不启动 listener，不写入 client-side target runtime，不发送会导致半激活的 provision；已持久化的 tunnel 必须投影为 `runtime_state=error` 或请求直接失败，并带有结构化错误/issue code。
- 如果 client 收到 unsupported unified provision：回复 `TunnelProvisionAck{Accepted:false, Message:...}`，不写入 `c.tunnels`、`c.socks5Targets` 或 fixed target runtime store。
- 如果 legacy flat provision 被拒绝：回复 `ProxyProvisionAck{Accepted:false, Message:...}`，不写入 `c.proxies`。
- 如果部分 participant 已经 provision 后另一端失败：server 必须 unprovision/close 已启动的 participant runtime，并清理 listener、target runtime、ack waiter 和 session-bound state。
- E2E 断言必须能观察到：预期端口没有 listener、对应 tunnel 没有可用 data path、runtime state/issue 显示失败原因、没有重复 listener 或 stale runtime。

clean reject 的错误码不能只藏在自由文本里。最小结构化要求：

- server/API 层必须暴露可机读 code，例如现有 `TunnelIssue.Code`、API error code，或明确新增字段。
- client ACK 暂时只有 `Message` 时，server 侧必须把它归一化为结构化 `TunnelIssueCodeProvisionAckRejected`，并在 details/message 中保留原始原因。
- capability mismatch、endpoint type unsupported、participant unavailable/listen resource unavailable 至少要能在 server-side issue/API error 层区分。

clean reject E2E 断言模板：

```text
waitTunnelState(tunnel_id, "error", 30s)
GET /api/tunnels/{tunnel_id}
assert issues[*].code contains expected structured issue code
assert expected ingress port has no listener, or request to it fails with connection refused/timeout
assert data path cannot be established
assert server/client logs do not show duplicate listener or stale runtime for the tunnel_id
```

如果测试层没有直接检查 listener 的 helper，必须先新增 `expectNoListenerAt(container, port, proto)` 或等价 helper。

### 1.1 一次性完成

本次不要拆成“先改 client target、以后再改 ingress/runtime/storage”。实现必须一次性移除 unified tunnel runtime 对 legacy proxy payload 的结构性依赖。

这不等于删除所有历史类型。它的意思是：任何剩余的 `ProxyNewRequest` 使用点，都必须明确归类到下面某个 legacy 边界：

- legacy client create request
- legacy server provision payload
- legacy client 启动静态配置
- 当前 SQLite schema 的存储投影
- 向后兼容 API 输入/输出
- 兼容性测试里的旧行为 fixture

不属于这些类别的 `ProxyNewRequest` 使用点，默认是问题，review 时必须要求解释。

### 1.2 必须兼容

兼容性不是可选项，也不是尽力而为。

最终改动必须证明下面组合可用：

- 新 server + 新 client
- 新 server + 老 client
- 老 server + 新 client
- client-to-client 中混合老/新 client
- 当前 server 恢复本次重构前创建的 tunnel
- 当前 server 恢复最新 stable 版本创建的 tunnel rows
- 老版本正在正常使用时，只升级 server，老 client 继续可用
- 老版本正在正常使用时，只升级 client，老 server 继续提供服务
- 老版本 server 与 client 都升级后，原 tunnel 和数据面继续可用

默认旧版本基线是实现时仓库里的最新 stable tag。本文写作时，本地 tag 列表显示最新 stable tag 是 `v0.1.8`。实现前必须重新确认：

```bash
git tag --list 'v*' --sort=-v:refname
git show <latest-stable-tag>:pkg/protocol/types.go
git show <latest-stable-tag>:pkg/protocol/message.go
git show <latest-stable-tag>:internal/client/client.go
```

不要假设老 client 没有 unified 能力。`v0.1.8` 已经会上报 `ClientCapabilities`，并且它的 `MsgTypeProxyProvision` handler 已经按 `tunnel_id` 做 dual-dispatch。兼容设计必须基于实际 tag 行为，不允许凭“老版本应该怎样”猜。

实现前必须把 latest stable 实际支持的 endpoint type、capabilities、provision payload shape 写入 compatibility/upgrade E2E 日志或测试 fixture 注释。测试不能硬编码“老版本应该支持 SOCKS5/HTTP/c2c”这类未经 tag 验证的假设。

开始 TDD 前必须固定兼容基线，例如在 PR 中写明：

```text
COMPAT_BASELINE=v0.1.8
```

同一个 PR 内不要因为发布了新 stable tag 就移动 baseline。需要更新 baseline 时，单独改本文档和对应 testdata。

### 1.3 runtime 生命周期必须干净

任何新增 runtime map、cache、listener、stream registry、endpoint runtime，都必须在下面场景清理：

- unprovision
- reconnect cleanup
- graceful shutdown
- stale revision replacement
- participant/session release

新增 runtime store 但不新增 cleanup 测试，不可接受。

server-side provision 必须按 tunnel id 串行化，或实现等价的 last-write-wins with cleanup 语义。对同一个 `tunnel_id`：

- 同一时刻最多只能有一个 active provision attempt。
- 新 revision 开始 provision 前，必须取消旧 ack waiter，并 unprovision/close 旧 revision 已启动的 listener/target runtime。
- restore/reconcile/API update 并发触发同一 tunnel provision 时，最终只能留下最高有效 revision 对应的 runtime。
- 测试必须覆盖同一 tunnel id 并发 provision，不允许出现重复 listener、重复 ack waiter 或两个 revision 同时可转发。

### 1.4 Transport 语义

`TransportPolicy` 表示用户/系统要求的传输策略。`ActualTransport` 表示当前实际选择的运行时传输路径。

当前稳定生产行为是 server relay。本次重构必须保持这个行为。本次不得顺手实现 peer-direct/P2P 或 TURN relay。

provisioning 阶段，`Spec.ActualTransport` 可能仍是 `"unknown"`，因为 tunnel 还没 active。一个合法的 server-relay data stream 不能只因为 provision-time spec 仍是 `unknown` 就被拒绝。

决策优先级：`TransportPolicy` 高于 provision-time `ActualTransport`。`ActualTransportUnknown` 放行 server-relay 只适用于 `TransportPolicy != direct_only`。

| TransportPolicy | provision-time ActualTransport | server-relay data stream |
|---|---|---|
| `server_relay_only` | empty / `unknown` / `server_relay` | accept |
| `direct_preferred` | empty / `unknown` / `server_relay` | accept |
| `direct_only` | empty / `unknown` / `server_relay` | reject |
| `direct_only` | `peer_direct` | reject server-relay；本次不实现 peer-direct path |

本次不做 P2P 行为，不实现 TURN relay，不把 `ActualTransportUnknown` 当成 direct-only 的例外。

### 1.5 用户级验收

用户级验收标准见第 8 节。这里先列出不可牺牲的用户结果：

- 现有 TCP tunnel 继续工作
- 现有 UDP tunnel 继续工作
- 现有 HTTP tunnel 继续工作
- 现有 SOCKS5 tunnel 继续工作
- 老 client 仍能连接新 server
- 新 client 仍能连接老 server
- 历史持久化 tunnel row 仍能恢复并转发数据
- server/client restart 不留下陈旧 runtime state
- 老版本正在正常使用的部署，按 server-only、client-only、full upgrade 升级后仍可用

如果测试只证明某个 helper 函数，但不能证明这些行为，本次工作不算完成。

## 2. 当前架构事实

### 2.1 wire message alias

wire protocol 上没有独立的 proxy provision 和 tunnel provision 消息名。

`pkg/protocol/message.go` 中：

```go
MsgTypeProxyCreate       = MsgTypeTunnelCreate
MsgTypeProxyCreateResp   = MsgTypeTunnelCreateResp
MsgTypeProxyProvision    = MsgTypeTunnelProvision
MsgTypeProxyProvisionAck = MsgTypeTunnelProvisionAck
MsgTypeProxyClose        = MsgTypeTunnelUnprovision
```

legacy flat provision payload 和 unified provision payload 都通过同一个 wire message type：`"tunnel_provision"`。

区分它们的是 payload shape：

- unified payload：包含 `tunnel_id`、`revision`、`role`、`spec`
- legacy flat payload：包含 `id`、`name`、`type`、`local_ip`、`local_port` 等

### 2.2 client dual-dispatch 必须保留

`internal/client/client.go` 处理 `MsgTypeProxyProvision` 时，会先检查 payload 是否有 `tunnel_id`。

- 有 `tunnel_id`：解析为 `TunnelProvisionRequest`，调用 `handleTunnelProvision`。
- 没有 `tunnel_id`：解析为 `ProxyProvisionRequest`，把 flat config 写入 `c.proxies`。

这个 dual-dispatch 必须保留。它是 old-server/new-client 兼容路径。

### 2.3 当前 unified target 降级问题

当前非 SOCKS5 target provisioning 仍走下面的降级链路：

```text
TunnelProvisionRequest.Spec
  -> proxyRequestFromTunnelSpec
  -> ProxyNewRequest
  -> c.proxies
  -> handleStream
```

这是本问题的核心。

`proxyRequestFromTunnelSpec` 必须删除。把它换个名字、继续做 `TunnelSpec -> ProxyNewRequest -> unified runtime`，不算修复。

### 2.4 SOCKS5 已经证明正确方向

SOCKS5 target runtime 已经绕开 `ProxyNewRequest`：

- target config 存在 `clientSOCKS5TargetRuntime`
- runtime 存在 `c.socks5Targets`
- stream matching 使用 `DataStreamHeader`
- 每条 SOCKS5 stream 的动态 target host/port 从 stream header 读取

TCP、UDP、HTTP target handling 应跟随这种 endpoint-runtime 模式，而不是继续使用 `ProxyNewRequest`。

当前 `internal/client/unified_tunnel.go` 已经存在 `clientTunnelRuntime`，它服务 client-side ingress listener，存储在 `c.tunnels`，key 是 `tunnel_id:role`。本次新增的 fixed service target runtime 不能复用这个名字，也不能让实现者误以为 `clientTunnelRuntime` 是待新增结构。

### 2.5 server-expose runtime 当前还没完全脱钩

不要无条件声称“unified ingress runtime 已经不依赖 `ProxyNewRequest`”。

client-to-client 的 client-side ingress runtime 已经从 `TunnelProvisionRequest` 构造。server-expose 的 server-side ingress runtime 仍使用现有 `ProxyTunnel` / `ProxyConfig` 机制，并且由 `StoredTunnel.ProxyNewRequest` 参与准备。

本次重构必须审计并移动 server-expose runtime 到 `TunnelSpec` / endpoint runtime 边界。内部可以复用现有 listener 和 relay 实现，但 unified runtime setup path 不得把 `ProxyNewRequest` 当作源模型。

当前 server-expose 重点依赖点：

- `restoreUnifiedServerExposeTunnel` 调用 `prepareProxyTunnelWithExclusions(client, stored.ProxyNewRequest, ...)`
- `applyStoredServerExposeConfig` 通过 `storedTunnelToProxyConfig(stored)` 写回 `ProxyTunnel.Config`
- HTTP domain conflict 和 host dispatch 仍通过 `ProxyConfig.Domain` / `StoredTunnel.Domain` 参与匹配
- SOCKS5 server-expose listener 已有 endpoint-specific config decoder，但仍挂在 `ProxyTunnel` runtime 容器上

### 2.6 storage 是兼容投影

`internal/server/store.go` 当前在 `StoredTunnel` 中嵌入 `protocol.ProxyNewRequest`。这与当前 SQLite schema 绑定。

除非实现证明不可避免，本次不重建 storage schema。预期边界是：

```text
SQLite row / StoredTunnel legacy fields
  -> normalize/project to TunnelSpec
  -> endpoint-specific runtime config
```

unified runtime 代码里不要继续这样做：

```text
StoredTunnel.ProxyNewRequest
  -> ProxyNewRequest
  -> unified runtime
```

storage 投影可以保留。runtime 对这个投影的依赖必须移除。

投影规则必须显式实现，不能让每个调用点临时拼字段：

- 必须有单一投影入口，例如 `tunnelSpecFromStoredTunnel(stored StoredTunnel) (protocol.TunnelSpec, error)`。名称可以不同，但入口必须唯一，不能散落多个 ad hoc converter。
- helper 输入是 `StoredTunnel`，输出是 `protocol.TunnelSpec`；不得输出 `ProxyNewRequest` 或 `ProxyConfig` 后再进入 unified runtime。
- 当 `StoredTunnel.Ingress` / `StoredTunnel.Target` 非空且 JSON config 有效时，endpoint fields 优先于 embedded `ProxyNewRequest` flat fields。
- embedded `ProxyNewRequest` flat fields 只用于 backfill 旧 row 缺失的 endpoint fields，或用于 legacy API/compat projection。
- 如果 endpoint fields 与 flat fields 冲突，unified runtime 必须以 endpoint fields 为准；测试必须覆盖这种冲突，防止 runtime 回读 flat fields。
- `StoredTunnel.ProxyNewRequest -> runtime` 的直接调用是禁止状态，即使中间包了一层 `ProxyConfig` 也不合格。

## 3. 目标状态

### 3.1 新路径主模型

所有 unified tunnel 路径使用下面模型：

```text
TunnelSpec
  -> role-specific endpoint runtime config
  -> runtime store/listener/stream handler
```

role-specific runtime config 包括：

- server-expose TCP/UDP/HTTP/SOCKS5 的 server ingress runtime config
- client-to-client TCP/UDP/SOCKS5 的 client ingress runtime config
- TCP/UDP/HTTP target service 的 client target runtime config
- SOCKS5 CONNECT dynamic target 的 client SOCKS5 target runtime config

HTTP 不是 target type。HTTP server-expose 使用：

- ingress type：`IngressTypeHTTPHost`
- target type：`TargetTypeTCPService`

target 侧只负责 dial 到配置里的本地 TCP 服务。HTTP host/domain dispatch 仍属于 server ingress 侧。

HTTP server-expose 的期望路径必须是：

```text
HTTP request Host header
  -> server-side HTTP host dispatch table, keyed by IngressTypeHTTPHost config.domain
  -> selected tunnel_id + revision
  -> DataStreamHeader{TunnelID, Revision, SourceRole: server, TargetRole: target, Direction: ingress_to_target, ActualTransport: server_relay}
  -> client fixed service target runtime
  -> net.DialTimeout("tcp", target host:port)
```

client target 侧不得使用 `ProxyNewRequest.Domain` 做 stream matching。Host header 可以作为普通 HTTP payload 继续透传给本地服务；它不是 client target runtime 的匹配条件。

HTTP tests 必须证明：

- 两个 HTTP tunnel 使用不同 host 时，server-side dispatch 选择正确 `tunnel_id`。
- client target runtime 只按 `DataStreamHeader.TunnelID` / revision / role / direction / transport 匹配。
- 删除或修改 `ProxyNewRequest.Domain` 不会影响 unified HTTP target stream matching。

HTTP host dispatch table 必须从 `TunnelSpec.Ingress.Config` 解码出的 `http_host` endpoint config 构造。允许内部复用现有 HTTP handler、listener 和 routing map，但 map 的写入入口必须是：

```text
TunnelSpec.Ingress{Type: http_host, Config.domain}
  -> httpHostIngressRuntimeConfig
  -> server HTTP host dispatch table
```

禁止从下面路径构造 unified HTTP dispatch：

```text
StoredTunnel.ProxyNewRequest.Domain
  -> ProxyConfig.Domain
  -> HTTP dispatch table
```

如果为了兼容现有 `ProxyTunnel` 容器需要填充 `ProxyConfig.Domain`，它只能是从 `TunnelSpec.Ingress.Config.domain` 反向投影出来的兼容字段，不得反过来成为 dispatch source。

### 3.2 legacy 路径

legacy 路径可以继续使用 `ProxyNewRequest`：

- client CLI/static `ProxyConfigs`
- old server provisioning flat payload
- 仍接收 flat proxy fields 的老 managed tunnel API
- 当前 SQLite storage rows
- 旧行为 compatibility test fixtures

legacy 路径必须满足二选一：

- 作为 legacy runtime 隔离保留
- 在边界层翻译成 `TunnelSpec` 或 endpoint-specific config

legacy 路径不得成为 unified runtime 的隐藏公共路径。

### 3.3 禁止出现的最终状态

下面状态不可接受：

- unified TCP target provision 把 `ProxyNewRequest` 写入 `c.proxies`
- unified UDP target provision 把 `ProxyNewRequest` 写入 `c.proxies`
- unified HTTP target handling 依赖 `ProxyNewRequest.Domain`
- server-expose unified restore 直接从 `StoredTunnel.ProxyNewRequest` 构造 runtime，中间没有 `TunnelSpec` / endpoint config 边界
- `proxyRequestFromTunnelSpec` 或等价 downgrade helper 仍存在
- `ProxyNewRequest` 新增长在 unified endpoint 上的字段，例如 SOCKS5 target policy、source CIDR policy、auth config、dynamic host/port、`spec`、`role`、`tunnel_id`
- reconnect cleanup 后仍残留上一个 session 的 target runtime state

## 4. 实现范围

### 4.1 Protocol 与 DTO 边界

不得破坏 wire compatibility。

`TunnelProvisionRequest{Spec TunnelSpec}` 仍是 canonical unified provision payload。

`ProxyNewRequest` 仍是 legacy flat proxy payload。它不得新增只属于 unified endpoint spec 的字段。

`ProxyCreateRequest` 和 `ProxyProvisionRequest` 的 type alias 是现状，但不是理想终态。本次推荐拆成 JSON shape 向后兼容的显式 struct；如果实现者选择暂不拆 alias，必须在 PR 说明中解释为什么拆 alias 会扩大风险，并给出测试证明 alias 没有继续污染 unified runtime。

无论 alias 是否拆分，必须满足：

- legacy wire JSON 向后兼容
- 所有使用点都被归类为 legacy
- 测试阻止 flat DTO 新增 unified-only 字段

重构是否成功取决于使用边界，不取决于类型名字本身。

### 4.2 Client target runtime

为 fixed service target 新增非 SOCKS5 target runtime：

```go
type fixedServiceTargetRuntime struct {
    tunnelID          string
    revision          int64
    targetType        string // protocol.TargetTypeTCPService or protocol.TargetTypeUDPService
    targetHost        string
    targetPort        int
    transportPolicy   string
    actualTransport   string
    bandwidthSettings protocol.BandwidthSettings
}
```

在 `Client` 中存储为：

```go
fixedTargetRuntimes sync.Map // tunnel_id -> *fixedServiceTargetRuntime
```

规则：

- 直接从 `TunnelProvisionRequest.Spec.Target` 构造
- 只支持 `protocol.TargetTypeTCPService` 和 `protocol.TargetTypeUDPService`
- 未知 `TargetType` 必须 reject，不能用裸字符串 fallback
- 不构造 `ProxyNewRequest`
- unified target runtime 不写入 `c.proxies`
- cleanup、unprovision、stale revision replacement 和测试都必须覆盖它

HTTP tunnel 的 target runtime 就是 `TargetTypeTCPService`。

`fixedTargetRuntimes` 的 key 使用 `tunnel_id` 是刻意设计：同一个 client 对同一个 tunnel 只能承担一个 fixed target role；ingress role 继续由现有 `c.tunnels` / `clientTunnelRuntime` 管理，key 仍是 `tunnel_id:role`。如果实现时发现一个 client 需要同时持有同一 tunnel 的多个 target role，必须先更新本文档和 key 设计。

`actualTransport` 字段只保存 provision-time spec 投影，不能作为拒绝 server-relay stream 的唯一依据。stream matching 的实际规则以第 1.4 节 transport 决策表和第 4.3 节 matching 条件为准：

- `transportPolicy == direct_only` 时拒绝 server-relay。
- `actualTransport == ""` 或 `unknown` 时，不因 actual unknown 拒绝 server-relay。
- `actualTransport == server_relay` 时，server-relay 可以匹配。
- 本次不实现将 fixed target runtime 的 `actualTransport` 动态更新为 peer-direct/TURN 的逻辑。

如果实现者发现 `actualTransport` 没有读者，可以删除该字段；如果保留，必须有测试覆盖 `unknown` 不误拒 server-relay。

revision 语义：

- `TunnelSpec.Revision` / `TunnelProvisionRequest.Revision` 由 server 分配。
- `StoredTunnel.Revision` 是 unified tunnel 的持久化 revision；创建或旧 row backfill 时最低为 `1`，更新时单调递增。
- server restart 后不得把已持久化 tunnel 的 revision 重置为 `0` 或重新从随机 runtime revision 开始。
- legacy flat path 的 `ProxyNewRequest.ProvisionRevision uint64` 是 legacy ACK matching revision；与 unified `Revision int64` 可暂时并存，但转换必须显式且不得允许负数或 0 进入 active unified stream matching。
- data stream header 的 revision 必须来自当前 provisioned `TunnelProvisionRequest.Revision`。

### 4.3 Client target stream dispatch

`handleStream` 必须按下面顺序 dispatch：

1. SOCKS5 target runtime (`c.socks5Targets`)
2. fixed service target runtime (`c.fixedTargetRuntimes`)
3. legacy flat proxy fallback (`c.proxies`)

legacy fallback 必须保留，用于 old-server/new-client 兼容。

fixed target stream matching 必须检查：

- tunnel id
- revision
- target role
- source role 是 server 或 ingress
- direction 是 ingress-to-target
- transport 是 server relay
- direct-only policy 拒绝 server relay
- provision-time `ActualTransport` 为空或 unknown 时，不得拒绝后续 server-relay data stream

revision 语义：

- stream matching 必须要求 `header.Revision == runtime.revision`
- unprovision cleanup 使用覆盖语义：`request.Revision >= runtime.revision` 才删除
- stale unprovision 不能删除 newer runtime
- 不要把 stream matching 和 unprovision 共用一个 revision helper

TCP target 行为：

```text
net.DialTimeout("tcp", targetHost:targetPort, 5s)
-> mux.Relay(stream, localConn)
```

这里的 `5s` 是沿用当前 legacy `handleStream` TCP dial timeout。实现时如改成常量，必须保持等价默认值并更新相关测试。

UDP target 行为：

```text
net.Dial("udp", targetHost:targetPort)
-> mux.UDPRelay(stream, localConn)
```

本次不得添加目标服务健康检查。只有真实流量到来时才 dial。

### 4.4 Client ingress runtime

client-to-client ingress runtime 必须继续基于 `TunnelProvisionRequest` / `TunnelSpec`。

需要审计：

- TCP listen ingress
- UDP listen ingress
- SOCKS5 listen ingress
- client-side listener preflight
- unprovision 与 stale revision handling

不要把 `ProxyNewRequest` 引入这些路径。

### 4.5 Server-expose runtime

server-expose ingress 在本次范围内，因为目标是一次性完成 unified runtime cleanup。

必须形成下面边界：

```text
StoredTunnel
  -> TunnelSpec
  -> server ingress runtime config
  -> listener/HTTP/SOCKS5 runtime
```

现有 listener 和 relay 实现可以复用。现有 storage fields 可以保留。禁止的是：把 `ProxyNewRequest` 当作 unified server-expose setup 的 runtime source of truth。

server-expose unified runtime helper 允许长这样：

```text
TunnelSpec -> serverIngressRuntimeConfig -> ProxyTunnel runtime container
```

这里 `ProxyTunnel` 只是复用现有 listener/relay 生命周期容器，不代表 runtime source 仍是 `ProxyNewRequest`。进入 runtime container 前必须已经完成 `TunnelSpec` / endpoint config 解码。

需要审计并重构：

- `restoreUnifiedServerExposeTunnel`
- `applyStoredServerExposeConfig`
- unified path 对 `prepareProxyTunnelWithExclusions` 的使用
- server-expose TCP listener runtime
- server-expose UDP listener runtime
- server-expose HTTP host dispatch runtime
- server-expose SOCKS5 listener runtime
- server restart restore path

如果内部 helper 仍需要 flat legacy config，必须引入命名清晰的 adapter，例如：

```text
TunnelSpec -> serverIngressRuntimeConfig
```

不要引入：

```text
TunnelSpec -> ProxyNewRequest -> server runtime
```

### 4.6 Server client-to-client runtime

client-to-client reconcile/provision/runtime 必须继续使用 `TunnelProvisionRequest` 和 `TunnelSpec`。

需要审计：

- `reconcileClientRelayTunnel`
- `tunnelSpecProtocolForRole`
- `notifyClientTunnelProvision`
- `waitForClientTunnelProvisionAck`
- `openRelayStreamToTarget`
- UDP relay frame path
- SOCKS5 CONNECT relay result path

不要把 client-to-client runtime 转回 `ProxyNewRequest`。

### 4.7 Legacy managed tunnel path

必须保持 legacy managed tunnel 行为可用。

包括：

- `ProxyConfigs`
- `requestProxy`
- `requestProxyRuntime`
- `applyProxyCreateResponse`
- `notifyClientProxyProvision`
- flat `ProxyProvisionRequest`
- flat `ProxyProvisionAck`
- old server provisioning current client

如果 legacy managed tunnel 和 unified tunnel 共享 runtime 代码，共享代码必须接收 endpoint-specific runtime config，而不是 `ProxyNewRequest`。

legacy managed tunnel 的触发入口只允许来自：

- client 静态 `ProxyConfigs`
- legacy client create request
- 兼容旧 server 的 flat provision path
- 当前仍保留的 v1/managed API

server 不得从 unified tunnel 路径降级调用 `notifyClientProxyProvision`。unified server-expose 和 client-to-client 都必须走 `notifyClientTunnelProvision`。

### 4.8 删除项

删除 `proxyRequestFromTunnelSpec`。

不要保留改名后的等价 helper。

使用过这个 helper 的测试必须改写为：

- 测 legacy 行为时，直接构造 legacy `ProxyNewRequest` fixture
- 测 unified 行为时，使用 `TunnelSpec` / endpoint runtime helper

### 4.9 Frontend 与 REST API 影响范围

本次不是前端改版，但实现可能触及 REST API 返回的 tunnel shape。只要改到 API DTO、`ProxyConfig` projection、`TunnelSpec` projection 或 web 可见字段，就必须审计前端：

- `web/src/lib/api.ts`
- tunnel list/detail hooks
- tunnel create/update form
- client detail owned/related tunnel views
- HTTP/SOCKS5 endpoint config 展示和编辑路径

原则：

- REST API 对旧字段的兼容不能被静默破坏。
- 前端如果仍消费 flat fields，server 必须继续提供兼容 projection，或同一 PR 修改前端类型和渲染。
- 如果 web 行为或类型被修改，merge 前必须执行 `cd web && bun run build`，并至少跑覆盖 tunnel create/list/detail 的 Playwright smoke。

## 5. 兼容矩阵

下面每一行都必须有可执行测试，或明确说明为什么本地无法执行。涉及 data path 兼容的行，不能只用 unit test 宣称覆盖。

| 组合 | 必须满足的行为 | 必须提供的证明 |
|---|---|---|
| new server + new clients | TCP、UDP、HTTP、SOCKS5 server-expose 和 TCP、UDP、SOCKS5 client-to-client 都可用 | system E2E |
| new server + old target client | old client 支持的 server-expose TCP/UDP/HTTP/SOCKS5 正常工作 | compatibility E2E |
| new server + old ingress client | old client 支持的 client-to-client TCP/UDP/SOCKS5 ingress 正常工作 | compatibility E2E |
| new server + old target client + current ingress client | 双方都支持的 client-to-client endpoint 正常工作；不支持时 clean reject | compatibility E2E |
| new server + current target client + old ingress client | 双方都支持的 client-to-client endpoint 正常工作；不支持时 clean reject | compatibility E2E |
| new server + old target client + old ingress client | old/old client-to-client endpoint 正常工作，或 server 在发送 provision 前 clean reject | compatibility E2E |
| old server + current target client + old ingress client | old server 支持的 client-to-client endpoint 正常工作 | compatibility E2E |
| old server + old target client + current ingress client | old server 支持的 client-to-client endpoint 正常工作 | compatibility E2E |
| old server + current target client + current ingress client | old server 支持的 client-to-client endpoint 正常工作 | compatibility E2E |
| old server + current single client | current client 接受 old server 发送的 flat provision 和 old unified provision payload | compatibility E2E |
| old persisted DB + new server | old tunnel rows 可恢复并转发数据 | restore E2E |
| new server/client restart | reconnect/restart 不留下 stale target/ingress runtime state | system E2E + unit tests |
| old running stack -> server-only upgrade | old server + old clients 先跑通流量；只替换 server 为 current 后，old clients 重连，原 tunnel 不需重建并继续转发 | upgrade E2E |
| old running stack -> client-only upgrade | old server 继续运行；只替换 target client 或 ingress client 为 current 后，old server 仍能 provision，数据面继续可用 | upgrade E2E |
| old running stack -> full upgrade | old server + old clients 先跑通流量；server 与 clients 都升级到 current 后，复用原数据/配置并继续转发 | upgrade E2E |

## 6. 必须补充的测试

### 6.0 单次交付内的 TDD 顺序

本次仍然是一次性交付，不拆 PR、不拆 release、不允许先上线一部分。下面只是同一个实现 PR 内部的 TDD 写作顺序，用来保证测试先行和每一步都可编译。

1. 测试基础设施先行：先让 compat/upgrade harness 能运行最小 smoke。
2. 先写可编译红测试：只使用当前已经存在的符号和测试 harness，先证明当前行为违反本文档。
3. 写最小实现让这些红测试变绿。
4. 新 runtime/helper 出现后，立刻补依赖这些新符号的结构性红测试，再继续实现。
5. unit / integration / system / compat / upgrade 全部变绿后，才允许认为这个一次性交付完成。

这些步骤不是 scope 分期。任何一步都不能作为可合并终点；只有第 8 节验收标准全部满足，PR 才能合并。

#### 6.0.1 测试基础设施先行

当前 `test/e2e/docker-compose.system.yml` 使用单一 `netsgo-e2e:local` image anchor，无法独立替换 server、target client、ingress client image。因此实现前必须先完成测试基础设施：

- 拆分 compose image 配置：
  - `server.image: ${NETSGO_SERVER_IMAGE:-netsgo-e2e:local}`
  - `target-client.image: ${NETSGO_TARGET_CLIENT_IMAGE:-netsgo-e2e:local}`
  - `ingress-client.image: ${NETSGO_INGRESS_CLIENT_IMAGE:-netsgo-e2e:local}`
  - 依赖 e2e 工具镜像的 backend helper 可继续使用 current/local 工具镜像，但必须显式命名，不能借 server/client image。
- 新增 Makefile targets：
  - `docker-build-e2e-current`
  - `docker-build-e2e-stable COMPAT_BASELINE=<tag>`
  - `test-compat-e2e`
  - `test-upgrade-e2e`
- 新增或整理脚本：
  - `test/e2e/scripts/test-compat.sh`
  - `test/e2e/scripts/test-upgrade.sh`
- `test-compat-e2e` 和 `test-upgrade-e2e` 在业务断言补齐前，至少要能跑一个 smoke：启动 stack、登录 admin、等待 server/target/ingress client 都在线、关闭 stack。

如果这些 target 不存在，后续兼容和升级测试都不算可执行。

#### 6.0.2 先写可编译红测试

这组红测试不得依赖尚未实现的新符号，例如 `fixedServiceTargetRuntime`、`fixedTargetRuntimes`、`tunnelSpecFromStoredTunnel`。它们必须在当前代码上能编译并失败。

优先写这些测试：

- protocol schema tests：
  - `ProxyNewRequest` forbidden field whitelist，阻止 `tunnel_id`、`revision`、`role`、`spec`、source/target CIDR、auth、dynamic target 字段进入 flat payload。
  - `TunnelProvisionRequest` TCP/UDP/HTTP/SOCKS5 endpoint config round-trip。
- server storage/projection behavior tests：
  - 构造 `StoredTunnel`，让 `Ingress.Config.domain=endpoint.example.com` 且 `ProxyNewRequest.Domain=flat.example.com`，断言 unified HTTP dispatch/projection 必须使用 endpoint domain。当前实现若回读 flat domain，该测试应红。
  - 构造 `Target.Config.host/port` 与 `ProxyNewRequest.LocalIP/LocalPort` 冲突，断言 unified target projection 使用 endpoint target。
- current-code negative tests：
  - 用当前 `handleTunnelProvision` 的 target TCP/UDP provision 路径证明它仍写入 `c.proxies`。该测试应红，目标行为是不写 `c.proxies`。
  - `Client.cleanup()` 的 fixed target runtime store 清理测试要等 store 符号出现后立刻补，不要在符号不存在时写不可编译测试。
- meta guard tests：
  - 可以增加一个小脚本或 Go test helper，列出 `proxyRequestFromTunnelSpec` 的当前调用点。实现过程中每删一个调用点都更新预期，最终要求函数和调用点均不存在。

#### 6.0.3 新符号出现后立刻补结构性红测试

这些测试可以依赖同一 PR 中刚新增的符号。新增 `fixedServiceTargetRuntime` / `fixedTargetRuntimes` / storage projection helper 之后，立即补下面测试，再继续实现：

- fixed target runtime registration、stream matching、unprovision、cleanup。
- HTTP dispatch table 从 endpoint config 构造，不从 flat domain 构造。
- single storage projection helper 覆盖 endpoint-vs-flat 冲突。
- 同一 tunnel id 并发 provision 只留下最高有效 revision runtime。

### 6.1 Unit tests

client tests 必须覆盖：

- [NEW] `handleTunnelProvision` 把 fixed TCP target runtime 存入 `fixedTargetRuntimes`
- [NEW] `handleTunnelProvision` 把 fixed UDP target runtime 存入 `fixedTargetRuntimes`
- [NEW] HTTP target 使用 TCP fixed target runtime
- [NEW] unified fixed target provision 不写 `c.proxies`
- [NEW] fixed target stream matching 接受合法 server-relay stream
- [NEW] fixed target stream matching 要求 revision 相等
- [NEW] fixed target stream matching 拒绝 stale revision
- [NEW] fixed target stream matching 拒绝 wrong role
- [NEW] fixed target stream matching 拒绝 wrong direction
- [NEW] fixed target stream matching 拒绝 wrong transport
- [NEW] fixed target stream matching 拒绝 `direct_only`
- [NEW] provision-time `ActualTransportUnknown` 且 policy 非 `direct_only` 时接受后续 server-relay stream
- [NEW] unprovision 按 tunnel id + revision 删除 fixed target runtime
- [NEW] newer unprovision 删除 older fixed target runtime
- [NEW] stale unprovision 不删除 newer fixed target runtime
- [NEW] `cleanup()` 删除 `fixedTargetRuntimes`
- [REGRESSION] legacy flat `MsgTypeProxyProvision` 仍写入 `c.proxies`
- [REGRESSION] legacy fallback stream handling 仍工作
- [REGRESSION] SOCKS5 target runtime 现有 test case 保留并通过，不得删除 fixture 来让新代码变绿
- [REGRESSION] client ingress runtime 现有 test case 保留并通过，不得删除 fixture 来让新代码变绿

server tests 必须覆盖：

- [REGRESSION] unified server-expose provision payload 是 `TunnelProvisionRequest`
- [REGRESSION] legacy managed provision payload 仍是 flat `ProxyProvisionRequest`
- [NEW] server-expose runtime setup 以 `TunnelSpec` / endpoint runtime config 为 unified 边界
- [NEW] server-expose TCP、UDP、HTTP、SOCKS5 runtime setup 不从 `ProxyNewRequest` 读取 runtime source
- [NEW] HTTP server-side host dispatch 选择正确 tunnel id，client target 不按 domain 匹配
- [REGRESSION] client-to-client provision/ack/reconcile 行为不变
- [REGRESSION] capability checks 基于实际上报 capabilities，不基于“老 client 应该怎样”的假设
- [NEW] old stored rows 仍可投影成 `TunnelSpec`
- [NEW] endpoint fields 与 flat fields 冲突时，unified runtime 以 endpoint fields 为准

protocol tests 必须覆盖：

- `ProxyNewRequest` 仍是 flat legacy schema
- `ProxyNewRequest` 不新增 `tunnel_id`、`revision`、`role`、`spec`、source CIDR、target CIDR、auth、dynamic target host、dynamic target port 等字段
- `TunnelProvisionRequest` 覆盖 TCP endpoint config round-trip
- `TunnelProvisionRequest` 覆盖 UDP endpoint config round-trip
- `TunnelProvisionRequest` 覆盖 HTTP ingress + TCP target config round-trip
- `TunnelProvisionRequest` 覆盖 SOCKS5 ingress/target config round-trip

legacy wire fixtures 必须固化到 `testdata/` 或等价目录，来源必须可追溯到 `COMPAT_BASELINE`：

- `legacy_<tag>_proxy_provision_tcp.json`
- `legacy_<tag>_proxy_provision_udp.json`
- `legacy_<tag>_proxy_provision_http.json`
- 如果 baseline 支持 SOCKS5，则补 `legacy_<tag>_tunnel_provision_socks5_*.json`

client dual-dispatch 兼容测试必须直接使用这些 fixture 构造 `protocol.Message`，不能只手写一个“看起来像旧版本”的 payload。

### 6.1.5 Integration tests

在 unit 和 docker system E2E 之间必须补 in-process integration tests。它们不依赖 Docker，但要跨 server/client/protocol 关键边界：

- server 发送 `TunnelProvisionRequest`，client ACK，server runtime state 从 pending 变为 active/error。
- legacy flat provision message 进入 current client，current client 走 dual-dispatch fallback。
- unsupported endpoint/capability clean reject 后，server 持久化 issue，且没有启动 listener/runtime。
- server restart restore path 从 `StoredTunnel` 投影到 `TunnelSpec`，不经 `ProxyNewRequest` runtime source。
- 同一 tunnel id 并发 provision/reconcile 只保留一个 active runtime。

这些 integration tests 是 system E2E 之前的门槛；它们失败时不要先跑 Docker E2E。

### 6.2 现有 system E2E

继续执行：

```bash
make test-system-e2e-nginx
make test-system-e2e-caddy
```

system E2E suite 必须包含或扩展到包含：

- server-expose TCP data path
- server-expose UDP data path
- server-expose HTTP data path
- server-expose SOCKS5 data path
- client-to-client TCP data path
- client-to-client UDP data path
- client-to-client SOCKS5 data path
- server restart restore
- target client restart recovery
- ingress client restart recovery

server restart restore 必须覆盖完整 data path，不只是 runtime state：

- server-expose TCP
- server-expose UDP
- server-expose HTTP
- server-expose SOCKS5
- client-to-client TCP
- client-to-client UDP
- client-to-client SOCKS5

如果现有 `system_e2e_test.go` 已覆盖其中一部分，本次实现必须补齐缺口，而不是新建一个只测当前重构路径的窄测试。

### 6.3 跨版本 compatibility E2E

新增真实 compatibility E2E target。建议形态：

```bash
make test-compat-e2e
```

测试 harness 至少构建或拉取两个镜像：

```text
netsgo-e2e:current
netsgo-e2e:<latest-stable-tag>
```

compose files 必须允许 server、target client、ingress client 独立选择镜像，例如：

```text
NETSGO_SERVER_IMAGE=netsgo-e2e:current
NETSGO_TARGET_CLIENT_IMAGE=netsgo-e2e:v0.1.8
NETSGO_INGRESS_CLIENT_IMAGE=netsgo-e2e:current
```

必测场景：

1. current server + current clients
2. current server + old target client + current ingress client
3. current server + current target client + old ingress client
4. current server + old target client + old ingress client
5. old server + current target client + old ingress client
6. old server + old target client + current ingress client
7. old server + current target client + current ingress client
8. old server 创建/持久化 tunnels，然后 current server 恢复同一份持久化数据

每个场景都要测试该版本组合支持的所有 tunnel type。如果某个 endpoint type 不被旧版本支持，测试必须断言 clean capability rejection，而不是出现 partial activation。

`test/e2e/scripts/test-compat.sh` 必须至少执行：

1. 构建/准备 `netsgo-e2e:current` 和 `netsgo-e2e:${COMPAT_BASELINE}`。
2. 对每个矩阵组合设置 `NETSGO_SERVER_IMAGE`、`NETSGO_TARGET_CLIENT_IMAGE`、`NETSGO_INGRESS_CLIENT_IMAGE`。
3. 启动 stack，等待 admin 可登录，等待预期 client 在线。
4. 运行该组合支持的 tunnel data-path tests。
5. 对 unsupported endpoint 执行 clean reject 断言模板。
6. 清理 stack 和 volume，避免不同组合共享脏状态。

### 6.4 跨版本 upgrade E2E

新增真实 upgrade E2E target。建议形态：

```bash
make test-upgrade-e2e
```

upgrade E2E 与 compatibility E2E 的区别：

- compatibility E2E 可以直接用不同版本组合启动 fresh stack。
- upgrade E2E 必须先用 latest stable 版本启动一套正在正常使用的 stack，创建 tunnel，跑通真实 data path，然后再替换 server/client 二进制或镜像。
- upgrade E2E 必须复用同一份 server data dir 和 client 配置，不能通过重建 tunnel、清空 DB 或换一套测试数据来掩盖问题。

`test/e2e/scripts/test-upgrade.sh` 必须把每个升级步骤写成可重复命令，不接受人工操作说明。每个步骤至少包括：

```text
docker compose up -d
waitForAdminToken
waitForClientPair
createOrLoadBaselineTunnels
assertAllSupportedDataPaths
docker compose stop <role>
docker compose up -d --no-deps <role> with new image env
waitForRestoreOrReProvision
assertAllSupportedDataPaths
```

`waitForRestoreOrReProvision` 必须有可观察判据：90 秒内 `/api/tunnels/{id}` runtime state 达到 active，且新 stream data path 成功。failed rollback 场景还必须证明 baseline server 重新启动后能读同一 data dir。

升级过程不要求零断连。允许进程重启和 WebSocket 重连。硬要求是：升级完成后，不需要用户重新创建 tunnel，所有被旧版本和新版本共同支持的 tunnel type 都必须恢复并继续转发数据。

基础场景：

1. 用 latest stable server + latest stable target client + latest stable ingress client 启动。
2. 创建该版本支持的 TCP、UDP、HTTP、SOCKS5 server-expose tunnel。
3. 创建该版本支持的 TCP、UDP、SOCKS5 client-to-client tunnel。
4. 在升级前先断言每个 tunnel 的 data path 都正常。

如果 latest stable 不支持某个 endpoint type，该 endpoint type 不属于 upgrade success 断言范围；但测试必须记录这个差异，并在 current 侧断言 unsupported endpoint 是 clean reject，不是 partial runtime activation。

必测升级路径：

1. server-only upgrade：保持 old clients 运行或允许其重连；停止 old server，使用同一 data dir/config 启动 current server；断言 old clients 重连后原 tunnel 继续工作。
2. target-client-only upgrade：保持 old server 运行；把 target client 从 old 替换为 current，复用同一 client 配置；断言 old server 仍能完成 provision，server-expose target data path 继续工作。
3. ingress-client-only upgrade：保持 old server 运行；把 ingress client 从 old 替换为 current，复用同一 client 配置；断言 client-to-client ingress data path 继续工作，或对旧版本不支持的 endpoint clean reject。
4. server-first rolling upgrade：old full stack 先跑通流量；升级 server 到 current，验证；再升级 clients 到 current，验证。
5. client-first rolling upgrade：old full stack 先跑通流量；先升级 clients 到 current，验证；再升级 server 到 current，验证。
6. full cold upgrade：old full stack 先跑通流量；停止全部 old processes；使用同一 server data dir 和 client 配置启动 current server + current clients；断言所有共同支持的 tunnel 恢复并转发。
7. failed server upgrade rollback：old full stack 先跑通流量；启动 current server 读同一 data dir 并至少完成一次 restore/reconcile；停止 current server；再用 latest stable server 读同一 data dir；断言 latest stable 仍能读取 tunnel rows，old clients 重连后 data path 可用。

每条升级路径都必须断言：

- 升级前 data path 正常
- 升级后 data path 正常
- 不需要重新创建 tunnel
- server 没有丢失旧 tunnel rows
- 没有重复 listener
- 没有 stale target/ingress runtime
- client 能在 90 秒内 reconnect/re-provision，除非实现前先在本文档中把超时与现有 backoff 常量绑定
- unsupported endpoint 在 runtime activation 前 clean reject

升级窗口期的 in-flight data stream 不要求零断连，也不要求 drain。允许旧 stream 出现 TCP reset、yamux stream reset 或应用层重试。测试不得把升级期间持续连接完全不中断作为通过条件；必须断言的是升级完成后新建 stream 能正常建立并转发。

### 6.5 merge 前最小验证

merge 前必须执行：

```bash
go test -tags dev ./internal/client ./internal/server ./pkg/protocol
make test-system-e2e-nginx
make test-system-e2e-caddy
make test-compat-e2e
make test-upgrade-e2e
```

如果改到前端行为或 UI 文案，额外执行：

```bash
cd web && bun run build
make test-playwright-e2e-smoke
```

如果没有新增 `make test-compat-e2e` 和 `make test-upgrade-e2e`，实现不满足本文档。

## 7. 不在本次范围内

除非先更新本文档，否则下面事项不在本次范围：

- 实现 peer-direct/P2P data transport
- 实现 TURN relay
- 新增 SOCKS5 UDP ASSOCIATE 支持
- 实现目标服务健康检查
- 主动探测用户 target service
- 新增 secret store
- 重建 SQLite tunnel schema
- 将所有 storage rows 迁移到新 schema
- 改变 release channel 语义
- 改动与保持现有 workflow 无关的前端 UX

重要区别：storage schema redesign 不在范围内，但 runtime 对 legacy storage projection 的依赖在范围内。

## 8. 验收标准

只有下面条件全部满足，才算完成：

- `TunnelSpec` 是 unified create/provision/restore/reconcile/runtime paths 的 canonical source model。
- unified fixed TCP target runtime 不再依赖 `ProxyNewRequest`。
- unified fixed UDP target runtime 不再依赖 `ProxyNewRequest`。
- unified HTTP target handling 使用 TCP service target runtime，不依赖 `ProxyNewRequest.Domain`。
- unified SOCKS5 target runtime 仍是 endpoint-specific，且不回退。
- client-side unified ingress runtime 仍基于 `TunnelProvisionRequest`。
- server-expose unified runtime 有 `TunnelSpec` / endpoint runtime 边界，不把 `ProxyNewRequest` 当作 unified runtime source model。
- `proxyRequestFromTunnelSpec` 已删除。
- old server 发送的 legacy flat provision 在 new client 中仍工作。
- legacy managed tunnel create/provision 仍工作。
- old clients 能连接 new servers，并使用每个被双方支持的 tunnel type。
- new clients 能连接 old servers，并使用每个被双方支持的 tunnel type。
- mixed old/new client-to-client 组合能工作，或在 runtime activation 前 clean reject。
- current server 能恢复 old persisted tunnel rows 并转发数据。
- endpoint fields 与 flat storage projection 冲突时，unified runtime 以 endpoint fields 为准。
- server-only upgrade 后，old clients 能重连并继续使用原 tunnel。
- client-only upgrade 后，old server 能继续 provision current client，数据面继续可用。
- full upgrade 后，current server + current clients 能复用旧数据/配置并继续转发。
- failed server upgrade rollback 后，latest stable server 仍能读取同一 data dir 并恢复共同支持的 tunnel。
- reconnect/shutdown/unprovision cleanup 清理所有 unified runtime stores。
- `ProxyNewRequest` 未新增 endpoint-specific 字段。
- 除非先更新本文档并给出 migration plan，否则不新增 SQLite migration。
- system E2E 通过。
- cross-version compatibility E2E 通过。
- cross-version upgrade E2E 通过。

## 9. Review checklist

approve 实现前必须检查：

- 没有 unified runtime path 调用 `proxyRequestFromTunnelSpec`
- 没有引入等价的 `TunnelSpec -> ProxyNewRequest -> unified runtime` adapter
- 每个剩余 `ProxyNewRequest` 使用点都被标注为 legacy、storage projection 或 compatibility fixture
- `Client.cleanup()` 清理所有新增 runtime store
- unprovision 覆盖 stale、matching、newer revisions
- old-version facts 已经按真实 git tag 验证
- compatibility E2E 能独立选择 server/client images
- upgrade E2E 从 latest stable 正常运行态开始，并复用同一份 server data dir 和 client 配置
- 测试覆盖真实 data flow，不只是 JSON round-trip
- capability check failure 不留下 half-active runtime state
- clean reject 有 wire-level ACK/error、runtime state/issue、no-listener/no-runtime 断言
- HTTP domain matching 仍在 server-side；HTTP E2E 必须断言不同 Host 路由到不同 tunnel id，client target 不读取 Domain
- storage projection helper 存在且统一；不得散落多个 `StoredTunnel -> runtime` 拼字段实现
- endpoint-vs-flat 冲突测试证明 unified runtime 不回读 `StoredTunnel.ProxyNewRequest`
- 没有新增 target service health probe
