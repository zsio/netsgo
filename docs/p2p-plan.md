## 评估结论

**只在 Web 端加“中继 / 首选 P2P / 仅 P2P”这个选项本身不难；真正难的是让 NetsGo 支持“端到端 Client ↔ Client 隧道 + P2P 直连 + 中继兜底”。**

按当前代码结构看，NetsGo 现在是**单中心中继架构**：Client 主动连 Server，Server 负责公网入口、隧道生命周期、流量转发；数据面是 `WebSocket + yamux`，Server 在收到外部 TCP/UDP/HTTP 流量后打开到 Client 的 yamux stream，再由 Client 转发到本地服务。README 架构图也明确写了：Server 承载 Web/API/SSE/控制通道/数据通道，Client 通过 Control WebSocket 和 Data WebSocket + yamux 接入，用户流量先到 Server 再转到 Client。

所以：
**现有“中继”能力是 Server → 单个 Client 的公网暴露隧道；你想要的“端到端”更像 Client A ↔ Client B 的隧道，这会是一个新能力，不是简单扩展一个字段。**

我的建议是分阶段做：

| 阶段      | 目标                                                       | 难度   |
| ------- | -------------------------------------------------------- | ---- |
| Phase 1 | 给现有 tunnel 增加 `connection_mode` 字段和 Web 选项，但先只实现 `relay` | 低    |
| Phase 2 | 实现端到端“中继模式”：Client A 本地监听，流量经 Server 转发到 Client B        | 中高   |
| Phase 3 | 实现 P2P signaling、候选地址交换、直连状态机、失败回退                       | 高    |
| Phase 4 | 完善 NAT 穿透、加密、可观测性、灰度、测试矩阵                                | 高到很高 |

如果要做成稳定可用的产品功能，**整体难度：高**。
如果只做一个 MVP，让同一公网/简单 NAT 下的两个 Client 能直连，**难度：中高**。
如果要做到类似 Tailscale/ZeroTier 那种在复杂 NAT 下也有较高成功率，**难度：很高**。

---

## 当前代码现状

### 1. 当前协议只建模了“一个 Client 的代理隧道”

核心配置是 `ProxyConfig` / `ProxyNewRequest`，字段包括 `Name`、`Type`、`LocalIP`、`LocalPort`、`RemotePort`、`Domain`、`ClientID`、限速、状态等；隧道类型目前只有 `tcp`、`udp`、`http`。

这说明当前 tunnel 的模型是：

> 某个 Client 上的本地服务 `local_ip:local_port`，通过 Server 的公网端口或域名暴露出去。

但端到端隧道通常需要至少两端：

> Source Client：在哪台机器上监听本地入口。
> Target Client：流量最终打到哪台机器的本地服务。
> Transport Mode：relay / p2p_preferred / p2p_only。
> Actual Path：当前实际走 relay 还是 p2p。

当前 `ProxyConfig` 没有 `source_client_id`、`target_client_id`、`connection_mode`、`actual_transport`、`p2p_state` 等字段。

---

### 2. 当前数据面是 Server 作为唯一中转点

Server 的 `ClientConn` 保存了 `dataSession *yamux.Session`，这是每个 Client 到 Server 的数据通道。

Client 建立数据通道时，会连接 `/ws/data`，发送握手，Server 验证 `clientID + dataToken` 后把 WebSocket 包成 `net.Conn`，再创建 yamux session。

Server 转发 TCP 时，会在外部用户连入 Server 监听端口后，调用 `openStreamToClient` 打开到 Client 的 yamux stream，并写入 `proxyName` 作为 stream header。

Client 侧则只负责接受来自 Server 的 yamux stream，读 `proxyName`，找到本地配置，然后拨打本地服务。

这套结构非常适合“公网入口在 Server”的模式，但不直接支持“Client A 本地监听 → Client B 服务”的端到端模式。

---

### 3. Web/API 也按“某个 Client 的 tunnels”设计

后端路由是 `/api/clients/{id}/tunnels`，创建、更新、停止、删除都挂在单个 Client 下。

创建隧道时，API 读取 `ProxyNewRequest`，如果 Client 在线就 `createManagedTunnel`，否则创建离线 managed tunnel。

前端类型也是单 Client 模型：`CreateTunnelInput` 里有 `clientId`，但没有 source/target 两端，也没有连接模式字段。

前端弹窗目前只选择协议类型 TCP/UDP/HTTP、本地 IP、本地端口、公网端口或域名、限速，没有端到端连接方式选择。

前端提交时会把数据 POST 到 `/api/clients/${clientId}/tunnels`，payload 也只包含当前模型里的 tunnel 字段。

---

### 4. 当前没有 P2P/NAT 穿透依赖或协议

`go.mod` 里主要依赖是 `gorilla/websocket`、`hashicorp/yamux`、SQLite、JWT、系统探针等；没有 WebRTC、QUIC、STUN/TURN、WireGuard、ICE、libp2p 之类的依赖。

我也没有在仓库里看到已有 `p2p`、`peer`、`stun`、`turn`、`hole punch` 等模块。现有 Client 只是异步上报公网 IPv4/IPv6，但这只是展示/探针信息，不能等同于 NAT 穿透候选地址。

---

## 你想要的三种模式，分别意味着什么

### 1. 中继模式 relay

端到端场景下的 relay 不是当前公网暴露模式的简单复用。

你想要的 relay 更可能是：

```text
用户应用 → Client A 本地监听端口
       → Client A 到 Server 的数据通道
       → Server 转发
       → Client B 到 Server 的数据通道
       → Client B 本地服务
```

当前已有的是：

```text
用户应用 → Server 公网端口 / 域名
       → Server 到 Client B 的数据通道
       → Client B 本地服务
```

所以端到端 relay 需要新增 **Client A 侧本地监听器** 和 **Server 双客户端桥接逻辑**。现有 Client 代码只接受 Server 下发的 stream 并转发到本地服务，没有“在 Client 侧监听一个入口并主动把流量发到远端 Client”的能力。

难度：**中高**。
原因：不涉及 NAT 穿透，但要重构数据面方向、协议、状态、流量统计和权限模型。

---

### 2. 首选 P2P p2p_preferred

语义应该是：

1. 创建端到端隧道后，Server 先下发配置给 Client A 和 Client B。
2. 两个 Client 通过 Server 控制通道交换候选地址、临时 token、连接参数。
3. 尝试 P2P 建连。
4. 成功后走 P2P。
5. 超时或失败后自动回退到 relay。
6. 前端显示“当前实际路径：P2P / relay / 尝试中 / 失败”。

这要求新增：

* P2P signaling 消息类型。
* Peer candidate 上报。
* NAT 探测或 STUN。
* P2P 握手认证。
* 直连传输层。
* P2P session 的生命周期管理。
* relay fallback 状态机。
* 当前实际链路的观测字段。

难度：**高**。

---

### 3. 仅 P2P p2p_only

语义应该是：

1. 只尝试 P2P。
2. P2P 不通就失败。
3. 不经 Server relay 传业务流量。
4. Server 仍可做控制面 signaling，但不能转发数据面。

这个模式产品上清晰，但用户体验会更“硬”：很多家庭宽带、公司网络、移动网络、对称 NAT、UDP 受限环境会失败。因此前端必须显示失败原因和建议，例如“无法打洞，可改用首选 P2P或中继”。

难度：**高**，但比 `p2p_preferred` 少一点 fallback 复杂度。
真正复杂的仍然是 NAT 穿透和安全。

---

## 关键改造点

### A. 数据模型改造

建议不要直接把现有 `ProxyConfig` 硬塞成端到端模型。更稳的是新增一种 `TunnelKind` 或新增独立表。

推荐模型：

```go
type TunnelKind string

const (
    TunnelKindExpose TunnelKind = "expose" // 当前 Server 公网暴露模式
    TunnelKindE2E    TunnelKind = "e2e"    // 新端到端模式
)

type TransportPolicy string

const (
    TransportRelay        TransportPolicy = "relay"
    TransportP2PPreferred TransportPolicy = "p2p_preferred"
    TransportP2POnly      TransportPolicy = "p2p_only"
)
```

端到端 tunnel 至少需要：

```go
SourceClientID string
SourceListenIP string
SourceListenPort int

TargetClientID string
TargetLocalIP string
TargetLocalPort int

Protocol string // tcp/udp
TransportPolicy string
ActualTransport string // relay/p2p/unknown
P2PState string // idle/checking/connected/failed/fallback
LastP2PError string
```

当前 SQLite `tunnels` 表的主键是 `(client_id, name)`，字段也是单 client 隧道模型。
因此端到端功能建议新增 migration，例如：

```sql
ALTER TABLE tunnels ADD COLUMN kind TEXT NOT NULL DEFAULT 'expose';
ALTER TABLE tunnels ADD COLUMN transport_policy TEXT NOT NULL DEFAULT 'relay';
ALTER TABLE tunnels ADD COLUMN actual_transport TEXT NOT NULL DEFAULT '';
ALTER TABLE tunnels ADD COLUMN source_client_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tunnels ADD COLUMN source_listen_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE tunnels ADD COLUMN source_listen_port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tunnels ADD COLUMN target_client_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tunnels ADD COLUMN p2p_state TEXT NOT NULL DEFAULT '';
ALTER TABLE tunnels ADD COLUMN p2p_error TEXT NOT NULL DEFAULT '';
```

但长期更干净的是单独建 `e2e_tunnels` 表，避免污染当前公网暴露 tunnel 的逻辑。

---

### B. 协议层改造

当前控制消息只有 auth、ping/pong、probe_report、proxy_create、proxy_provision、proxy_close 等。

需要新增类似：

```go
MsgTypeE2EProvision
MsgTypeE2EProvisionAck
MsgTypeP2POffer
MsgTypeP2PAnswer
MsgTypeP2PCandidate
MsgTypeP2PCheckResult
MsgTypeP2PSelected
MsgTypeP2PFallbackRelay
MsgTypeE2EClose
```

如果做简版，可以先加：

```go
MsgTypeE2EProvision
MsgTypeE2EConnectRequest
MsgTypeE2EConnectAck
MsgTypeE2ERelayOpen
MsgTypeE2EP2PHandshake
```

核心是 Server 要能协调两个 Client，而不是只对单个 Client 下发 `ProxyProvision`。现在 `TunnelRegistry` 等待的是某个 Client 对某条 tunnel 的 ACK，key 是 `{clientID, generation, name}`。
端到端至少要等待两个 Client 的 ACK，并且要处理一端在线、一端离线、一端重连、一端版本不支持等情况。

---

### C. Client 侧改造

现有 Client 行为是：

1. 连 Server 控制通道。
2. 连 Server 数据通道。
3. 接受 Server 打开的 yamux stream。
4. 根据 `proxyName` 转发到本地服务。 

端到端需要 Client 新增两类角色：

#### Source Client

负责本地监听，例如：

```text
127.0.0.1:10022 -> Target Client 的 127.0.0.1:22
```

它需要：

* 绑定本地 TCP/UDP 监听端口。
* 收到本地连接后，决定走 P2P 还是 relay。
* P2P 成功时把流量写入 peer session。
* relay 时把流量通过 Server bridge 转到 Target Client。
* 断线重连后恢复本地监听。

#### Target Client

负责接受来自 Source Client 的连接，并转发到目标本地服务。这个能力和当前 `handleStream` 比较接近，可以复用部分转发逻辑。

---

### D. Server relay bridge

端到端 relay 模式需要 Server 同时连接两个 Client 的数据 session：

```text
Source Client yamux stream <-> Server <-> Target Client yamux stream
```

现有 `openStreamToClient(client, proxyName)` 已经能打开到某个 Client 的 stream。
但缺少“Source Client 主动向 Server 打开业务 stream”的协议。当前 Client 的 data yamux 是 `mux.NewClientSession`，Server 是 `mux.NewServerSession`，由 Server `session.Open()`，Client `AcceptStream()`。 

yamux 本身支持双向开 stream，但当前代码路径主要按 Server-open-stream 设计。要做端到端 relay，有两个选择：

1. **复用同一个 yamux session，允许 Source Client 主动 OpenStream。**
   这需要 Server 侧增加 accept stream loop，读取 Source Client 发来的 E2E stream header，再桥接到 Target Client。

2. **保持当前单向设计，Source Client 的本地监听连接通过控制/数据请求让 Server 主动拉起两边 stream。**
   这会复杂一点，延迟也更高。

我建议第一种。

---

### E. P2P 直连方案选择

有三条路线：

#### 方案 1：WebRTC DataChannel / ICE

优点：

* ICE/STUN/TURN/NAT 处理成熟。
* P2P + relay fallback 语义天然匹配。
* Go 可以用 Pion WebRTC。

缺点：

* 引入较大依赖。
* DataChannel/ICE 状态机复杂。
* 要把现有 stream/mux 适配到 DataChannel 或 SCTP。

适合：想较快获得可用 NAT 穿透能力。

#### 方案 2：QUIC + UDP hole punching

优点：

* Go 侧可控性强。
* 建成后可把 QUIC stream 当作类似 `net.Conn` 的数据面使用。
* 性能和可观测性较好。

缺点：

* 自己实现候选交换、NAT 类型处理、打洞流程、失败回退。
* 复杂 NAT 下成功率不如成熟 ICE。

适合：希望长期掌控协议栈。

#### 方案 3：类 Tailscale DERP + WireGuard/Noise

优点：

* 安全模型清晰。
* 端到端加密可以做得很好。
* relay fallback 可控。

缺点：

* 工程量最大。
* 基本是做一个小型 overlay network。

适合：长期产品方向，不适合短期 MVP。

我的建议：
**MVP 用 QUIC/UDP 或 WebRTC 二选一；生产可用优先考虑 WebRTC/ICE 或成熟的 ICE/STUN 方案。**

---

## Web 端选项怎么加

前端改动本身很简单。

当前 `TunnelDialog` 表单已有协议类型、本地 IP、本地端口、公网端口/域名、限速。

可以新增一个连接方式字段：

```ts
export type TunnelTransportPolicy =
  | "relay"
  | "p2p_preferred"
  | "p2p_only";
```

`CreateTunnelInput` / `UpdateTunnelInput` 增加：

```ts
transport_policy?: TunnelTransportPolicy;
```

`buildTunnelMutationPayload` 增加：

```ts
transport_policy: input.transport_policy ?? "relay"
```

但要注意：
**这个字段只有在端到端 tunnel 类型下才有意义。**
当前公网暴露型 tunnel 本质就是 Server relay，不应该让用户误以为它能 P2P。

更合理的 UI 是：

```text
隧道类型：
- 公网暴露隧道
- 端到端隧道

当选择“端到端隧道”时：
- 源 Client
- 源监听地址/端口
- 目标 Client
- 目标服务地址/端口
- 连接方式：
  - 中继
  - 首选 P2P，失败后中继
  - 仅 P2P
```

---

## 难点和风险

### 1. NAT 穿透成功率不可控

P2P 最大风险不在代码结构，而在网络现实：

* 对称 NAT 下 UDP 打洞成功率低。
* 公司网络可能禁 UDP。
* 移动网络 CGNAT 情况复杂。
* 两端都在严格防火墙后时只能 relay。
* IPv6 可提升直连概率，但不能假设用户都有。

所以必须提供 relay fallback。`p2p_only` 一定会有较多失败案例。

---

### 2. 安全模型必须重做一层

当前安全主要是 Client 与 Server 的认证、data token、TLS/TOFU 等。Client 数据通道认证通过 `clientID + dataToken` 完成。

P2P 后，业务流量不再经过 Server，必须新增：

* 每条端到端 tunnel 的临时会话密钥。
* Peer 身份校验。
* 防重放 nonce。
* Server 签发的 peer connect token。
* P2P 连接上的端到端加密。
* 防止恶意 Client 伪装成其他 Client。

不能只靠“两个 Client 都连过同一个 Server”来信任 P2P 数据面。

---

### 3. 状态机会明显变复杂

当前 tunnel 状态是：

```go
pending / exposed / offline / idle / error
```

并且配合 desired_state `running / stopped`。

P2P 需要额外状态：

```text
transport_policy: relay / p2p_preferred / p2p_only
actual_transport: unknown / relay / p2p
p2p_state: idle / gathering / checking / connected / failed / fallback
p2p_error: ...
```

否则前端无法解释“用户选择了首选 P2P，但当前实际走中继”。

---

### 4. 测试成本高

当前已有不少 server/client/tunnel 测试，端到端 P2P 需要新增：

* 双 Client relay e2e 测试。
* P2P 成功测试。
* P2P 超时 fallback 测试。
* 仅 P2P 失败测试。
* Source Client 离线 / Target Client 离线。
* Client 重连恢复。
* Server 重启恢复。
* TCP/UDP 分别测试。
* 版本兼容测试：旧 Client 不支持 P2P 时如何处理。

---

## 推荐实施路线

### Phase 1：先把模型预留好，但默认只走 relay

目标：

* 新增 `transport_policy` 字段。
* 前端仅在“端到端隧道”中展示连接方式。
* 后端先接受字段，但只支持 `relay`。
* 对 `p2p_preferred` / `p2p_only` 返回明确错误：当前 Client/Server 版本暂不支持。

工作量：**2–4 天**。
风险：低。

---

### Phase 2：实现端到端 relay

目标：

```text
Client A 本地端口 → Server → Client B 本地服务
```

需要：

* 新增 E2E tunnel 数据模型。
* Source Client 本地 TCP 监听。
* Server 接受 Source Client 业务 stream。
* Server 打开 Target Client stream。
* 双向 relay。
* 前端新增源/目标 Client 选择。
* SSE 展示端到端 tunnel 状态。
* 流量统计归属规则。

工作量：**1–2 周**。
风险：中等。

这是非常关键的一步。没有它，P2P fallback 就没有基础。

---

### Phase 3：P2P MVP

目标：

* 两个 Client 通过 Server signaling 交换候选信息。
* 尝试 UDP/QUIC 或 WebRTC 直连。
* 成功后 Source Client 和 Target Client 之间直接传输。
* `p2p_preferred` 失败后回落到 Phase 2 relay。
* `p2p_only` 失败后报错。

工作量：**2–4 周**。
风险：高。

---

### Phase 4：生产化

目标：

* 更完整 NAT 兼容。
* 加密和身份校验完善。
* P2P 连接质量探测。
* 自动重试与路径切换。
* 前端展示实际路径、延迟、失败原因。
* 大量网络环境测试。

工作量：**4–8 周或更久**。
风险：高。

---

## 是否“好实现”

分开回答：

**Web 端增加选项：好实现。**
主要改 `web/src/types/index.ts`、`TunnelDialog.tsx`、`use-tunnel-mutations.ts`、`tunnel-model.ts`，再加后端字段解析和数据库 migration。

**端到端 relay：中等偏难，但可控。**
因为现有 `WebSocket + yamux + relay` 基础不错，可以复用不少传输代码。

**P2P：难。**
不是因为 Go 写不出来，而是因为要解决 NAT 穿透、signaling、身份认证、失败回退、状态展示、重连恢复和测试矩阵。当前项目没有这部分基础依赖或抽象，需要新增一条完整的数据面。

最终建议：
**先做“端到端 relay”，再做“首选 P2P/仅 P2P”。不要一开始就直接做完整 P2P。**
