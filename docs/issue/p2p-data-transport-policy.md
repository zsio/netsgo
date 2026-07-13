# P2P 数据传输策略与设计

## 状态

Implemented and locally validated; carrier-grade NAT and passive ICE-TCP deployment validation remain open

## 严重程度

High

## 已确认的产品语义

- [KNOWN] P2P 只作用于 `client_to_client` 隧道的数据路径。它不是新的隧道类型，也不适用于 `server_expose`。
- [KNOWN] 每个 Client 的控制通道仍连接 Server。Server 继续负责认证、授权、信令、配置和运行态协调；隧道 payload 可以选择经 Server 中继或 Client 间点对点直传。
- [KNOWN] `client_to_client` 隧道只提供三种传输策略：
  - `server_relay_only`：只使用 Server 中继。
  - `direct_preferred`：点对点可用时优先使用点对点，不可用时使用 Server 中继。
  - `direct_only`：只使用点对点；点对点不可用时无法承载新流量。
- [KNOWN] API 和 UI 默认选择 `server_relay_only`。
- [KNOWN] 产品接受由 Client 上报点对点隧道流量，并由 Client 执行点对点路径的限速。它们是 Client 协作语义，不是 Server 直接观察 payload 后实施的强制语义。
- [KNOWN] 第一版不使用 TURN。`direct_only` 表示不允许任何中继；直连失败时新流量失败。
- [KNOWN] 未来可以增加由用户选择 Client C 为 A、B 转发密文的“客户端中继”，但它仍然是 relay，不能计为 `peer_direct` 或显示为 P2P 直连成功。
- [KNOWN] 同一对 Client 共用一个 P2P peer session；不同隧道和应用连接在其上使用相互隔离的逻辑通道。
- [KNOWN] 第一版 peer 实现选择 Pion WebRTC/ICE/DataChannel，不引入 quic-go，也不另建第三方信令服务。
- [KNOWN] 第一版以 Pion UDP ICE/DataChannel 为默认直连路径。产品目标是在同一 PeerConnection 内把经过原型验证的 ICE-TCP candidate 作为机会性备用，但不能把它描述成双方位于 NAT 后仍普遍有效的 TCP 打洞。

[KNOWN] 本节结论覆盖本 issue 中此前任何含糊或冲突的表述。置信度：HIGH。

## 当前实现证据与保护边界

[KNOWN] P2P 数据闭环已经实现：Client pair 共用 Pion PeerConnection，在 negotiated reliable ordered detached DataChannel 上运行 yamux；TCP、UDP-over-stream 和 SOCKS5 共用 transport selector 与 transport-neutral target dispatcher。

[KNOWN] `/api/tunnels` 已对能力匹配的 `client_to_client` 开放 `direct_preferred` 和 `direct_only`；旧 Client、能力未知或实现标识不匹配的 Client 仍只能使用 relay。Web 已开放三种策略，默认仍为 `server_relay_only`。

[KNOWN] 当前 `client_to_client` 路径是 Ingress Client -> Server -> Target Client：Ingress Client 向 Server 打开 yamux stream，Server 校验隧道身份后向 Target Client 打开第二条 yamux stream，再在两条 stream 间转发 payload。

[KNOWN] 当前 UDP 隧道会将每个 datagram 帧化后放入可靠逻辑 stream，并在目的端还原。因此 TCP、UDP-over-stream 和 SOCKS5 已经共享同一套底层逻辑 stream 传输。

[KNOWN] 主要代码位置：

- `pkg/protocol/types.go`：传输策略、实际传输、P2P 状态和能力字段。
- `pkg/protocol/p2p.go`、`pkg/protocol/message.go`：P2P 控制消息、授权、租约、统计和 sender-credit 协议。
- `pkg/protocol/stream_header.go`：逻辑 stream 身份和 transport 元数据。
- `internal/server/data.go`：Server 数据 WebSocket 和 yamux session。
- `internal/server/client_relay.go`：当前 Client-to-Client Server 中继。
- `internal/server/unified_tunnel_reconcile.go`：隧道 reconcile 入口。
- `internal/client/unified_tunnel.go`：Client ingress stream 创建和 UDP association。
- `internal/client/client.go`：Client 数据 session 和 target stream 分发。
- `pkg/mux/udp_frame.go`：UDP-over-stream 帧化。
- `pkg/p2p/session.go`：Pion DataChannel 与 yamux 适配。
- `internal/server/p2p_coordinator.go`：pair session、信令、租约、grant、统计和 credit 授权。
- `internal/client/p2p_manager.go`：Client pair session、direct stream、撤销、过期和统计。
- `web/src/lib/tunnel-model.ts`：当前传输策略构造和显示文案。

## 分层契约

[KNOWN] P2P 位于隧道/endpoint 行为之下、具体网络传输之上。它必须实现为统一 data-channel selector，不能分别复制到 TCP、UDP、SOCKS5 或未来 endpoint handler 中。

[INFERRED] 实现应按以下逻辑分层：

1. 隧道和 endpoint runtime
   - 接收 TCP 连接、UDP datagram 或 SOCKS5 请求。
   - 构造和消费 `DataStreamHeader` 及隧道 payload。
   - 为自身的数据形态定义 payload 统计口径。
2. Transport selector
   - 接收隧道身份、revision、policy 和 stream header。
   - 为每个新逻辑连接或 association 选择且只选择一个可用 transport。
   - 统一负责 `server_relay_only`、`direct_preferred`、`direct_only` 的选择和 fallback。
3. Transport 实现
   - `server_relay`：封装现有 `/ws/data + yamux` 路径。
   - `peer_direct`：通过点对点连接提供相同的逻辑 stream 能力。

[INFERRED] Target 分发入口应接收与 transport 无关的逻辑连接，而不是要求具体的 `*yamux.Stream`。中继 stream 和点对点 stream 必须进入同一套 header 校验以及 TCP、UDP、SOCKS5 target 分发。

[KNOWN] Selector 和 transport 实现不得解释隧道类型。动态 SOCKS5 目的地址等类型专属元数据仍由共享 stream header 和 target runtime 处理。

## 第一版技术选型

[KNOWN] NetsGo Server 自己承担信令角色，但不新增独立信令服务。现有可信控制通道负责认证双方、创建 P2P session、交换 Pion offer/answer 和 ICE candidate、转交临时身份材料、续期授权、撤销授权以及接收连接状态。

[KNOWN] 真实 P2P payload 不经过 Server。Server 只转发小型信令和运行态信息。

[KNOWN] 第一版只使用一套 Pion PeerConnection 数据栈：

1. [KNOWN] Pion ICE 收集、排序和检查 host、局域网、IPv4、IPv6 和 STUN server-reflexive candidate；经过原型验证后可以再启用明确可达的 passive ICE-TCP candidate。
2. [KNOWN] Pion DTLS 负责 peer 数据路径加密和对端临时身份校验。
3. [KNOWN] Pion SCTP/DataChannel 提供可靠有序的消息通道。实现使用 negotiated detached DataChannel，并在其上运行 yamux，把它适配为多个 `net.Conn` 语义的逻辑 stream。
4. [INFERRED] 未来原生不可靠或无序 datagram 可以增加独立 DataChannel 能力并复用现有策略和 pair session 生命周期，但仍需新增 capability/version、association grant、最大 datagram、分片和 fallback 协议。

[KNOWN] 不引入 quic-go。QUIC 只能承载已经建立的 UDP 路径，不能提高 ICE 穿透成功率；与 Pion DataChannel 同时引入还会重复加密、多路复用、连接状态和心跳逻辑。

[KNOWN] UDP 是 Pion WebRTC 默认使用的 ICE 路径。TCP 不作为另一套 P2P 产品或隧道实现；第一版原型只验证 Pion passive ICE-TCP：一端必须成功发布外部可达的 passive TCP candidate，另一端才可能建立 active candidate pair，上层仍使用同一套 DataChannel。

[COMMON] Passive ICE-TCP 主要覆盖同一局域网、可达公网 TCP、可直连 IPv6 或管理员已使一端 TCP listener 外部可达的情况。Pion 不会仅因路由器存在端口映射就自动发现它；双方都处于严格 NAT/CGNAT、UDP 被阻断且没有外部可达 passive TCP candidate 时，纯直连仍会失败。

[KNOWN] 第一版不承诺 TCP server-reflexive candidate、TCP simultaneous-open、严格 NAT TCP 打洞，也不承诺已经选中的 UDP path 失效后无损热切换到 TCP。Pion TCP network type、TCP listener/TCP mux 和 active-to-passive candidate pair 必须通过跨平台原型后才能成为启用能力。

[KNOWN] 第一版不使用 TURN，也不静默依赖项目无法控制的免费公共 TURN 服务。STUN 只处理地址探测，不转发业务 payload，因此不属于 relay。

[KNOWN] NetsGo Server 已在与 TCP Server 相同的数字端口监听 UDP STUN；Client 在没有显式 ICE server 时从 `ServerAddr` 派生该 STUN 地址。只代理 HTTP/WebSocket 的 nginx/caddy 不会自动转发 UDP，反代部署必须额外代理同端口 UDP，否则只能依赖 host candidate。

[INFERRED] P2P session 的连接层级如下：

```text
NetsGo /ws/control（认证、信令、授权、续期）
                  |
                  v
       每对 Client 一个 Pion PeerConnection
                  |
      ICE 默认 UDP；验证后的 passive TCP 可备用
                  |
        DTLS + SCTP + DataChannel
                  |
     多条 tunnel / TCP / UDP association / SOCKS5
```

[KNOWN] DataChannel framing 已固定：detached DataChannel 的消息语义先由 `dataChannelByteStream` 转换成连续字节流，单个 SCTP user message 最大 16 KiB，再在该字节流上运行 yamux。该适配会保存一次 DataChannel message 中未被 yamux 当前 `Read` 消费的剩余字节；没有这层适配，大型 yamux frame 会因 DataChannel 要求完整 message buffer 而触发 `io.ErrShortBuffer` 并破坏整个 peer session。

## 单路径与切换语义

[KNOWN] 每个逻辑流量单元只选择一个 transport。NetsGo 绝不能把同一份应用 payload 同时通过 `server_relay` 和 `peer_direct` 镜像或重复发送。

- [KNOWN] 每个已接收的 TCP 连接在整个生命周期内固定使用一个 transport。
- [KNOWN] 每个 SOCKS5 CONNECT 请求在整个生命周期内固定使用一个 transport。
- [KNOWN] 每个 UDP association 在整个生命周期内固定使用一个 transport；该 association 的所有 datagram 都走同一 transport。

[KNOWN] 已建立的连接或 association 不会因为 selector 的选择变化而迁移。

[KNOWN] 点对点就绪后，已经存在的 relay 连接继续走 relay，只有之后新建的连接或 association 改走点对点。这个计划内切换不得打断已有应用连接。

[KNOWN] 点对点失败时，现有 direct 连接可以终止。`direct_preferred` 的后续新连接或 association 改走 Server relay；`direct_only` 的后续新流量在点对点恢复前失败。

[KNOWN] 因此，同一隧道可能暂时同时存在 relay 活跃连接和 direct 活跃连接，但任何单独连接或 datagram 都不会在两个 transport 上重复发送。

[KNOWN] 单值 `actual_transport` 当前表达 selector 对新连接的选择，不能完整表达“旧 relay stream 仍在排空，但新 stream 已选择 direct”的混合期。每条 stream 和 traffic bucket 携带自己的真实 transport，避免计量混淆；精确的 `active_transports`/活动连接计数尚未实现，Web 文案不得把 selector 状态描述成所有存量连接均已迁移。

## 策略状态行为

| 策略 | 点对点就绪前 | 点对点就绪后 | 点对点失败后 |
|---|---|---|---|
| `server_relay_only` | [KNOWN] 使用 relay，不尝试 direct。 | [KNOWN] 使用 relay。 | [KNOWN] 使用 relay。 |
| `direct_preferred` | [KNOWN] direct 不可用时使用 relay。 | [KNOWN] 新流量使用 direct；已有 relay 流量自然排空。 | [KNOWN] 新流量使用 relay；已有 direct 流量可以终止。 |
| `direct_only` | [KNOWN] 无法承载新流量。 | [KNOWN] 新流量使用 direct。 | [KNOWN] 无法承载新流量；已有 direct 流量可以终止。 |

[INFERRED] 点对点协商和重试必须独立于隧道类型 handler。连续失败必须使用有上限的重试和退避，避免不可达 peer 持续制造信令或连接负载。

[KNOWN] 只要一对 Client 之间至少存在一条启用 `direct_preferred` 或 `direct_only` 的隧道，并且双方在线且能力兼容，Server 就应主动协调建立共享 PeerConnection，不等待第一条应用连接到来。不存在相关隧道时不得维持无意义的 peer session。

[KNOWN] P2P 重试以 Client pair 为单位，不能按 tunnel 重复发起。第一次立即尝试，失败后可在约 10 秒、30 秒重试，随后稳定为约一分钟一次并加入随机偏移，避免大量 Client 同时重试。Client 重新上线、网络候选变化或用户显式重试可以立即触发新尝试。

[KNOWN] `direct_preferred` 在 direct 尚未就绪时不阻塞第一条应用连接，而是立即使用 relay；PeerConnection 就绪后只有新逻辑流量改走 direct。`direct_only` 可以等待当前 P2P 尝试的有界超时，但不能无限排队，超时后该次连接失败。

[INFERRED] 具体退避和超时值应写成代码常量并通过 NAT E2E 调整，不做环境变量；稳定重试目标约为一分钟，而不是要求所有 Client 精确同步在第 60 秒重试。

## 流量统计与限速

[KNOWN] 点对点 payload 绕过 Server 数据路径，因此 Client 负责统计点对点隧道 payload、执行配置的点对点限速，并通过控制通道向 Server 上报观测值。

[KNOWN] “隧道归属方”按协议机械定义为 `owner_client_id` 指向的 Target Client。对固定目标隧道，它是运行真实服务并把服务映射出去的 Client；对 SOCKS5，它是执行动态目标拨号的 Client。无论访问入口映射到哪个 Client，双向 payload 都会经过归属方的逻辑隧道边界。

[KNOWN] 服务归属方是 `peer_direct` 流量唯一的正式上报来源，但该来源仍是 Client 协作且不可由 Server 独立验证的。访问方 Client 和未来的客户端中继 C 不上报这条隧道的正式 direct 业务流量；relay payload 仍由 Server 计量，Server 不能把 direct 与 relay 对同一逻辑 payload 重复累计。

[KNOWN] 不同 transport 必须使用相同的 payload 统计语义。特别是 UDP 仍统计原始 datagram payload，不能仅因改走点对点就开始统计 framing 或 peer transport 开销。

[KNOWN] 服务归属方在固定逻辑边界分别累计“发往服务”和“服务返回”的 payload bytes，每份 payload 只统计一次；握手、ICE、DTLS、SCTP、DataChannel framing 和心跳开销不计入隧道业务流量。

[INFERRED] 点对点流量报告应使用累计计数，并至少绑定 tunnel ID、tunnel revision、本次运行/计数 epoch、P2P session 身份和 actual transport。报告需要单调顺序或等价去重身份，确保控制通道重试不会造成重复计数；这用于处理正常重连，不是防作弊校验。

[KNOWN] P2P 隧道限速是两个方向共用的 tunnel-wide 总额度，不是每个方向各自获得一份完整额度。服务归属方统一协调共享额度和公平分配，两个 Client 分别在自身发送点执行归属方发放的 credit。

[KNOWN] 协议、SQLite、API 和 Web 已新增显式 `total_bps`。Relay 与 direct 都执行同一个 tunnel-wide 共享总额度；旧 `ingress_bps`、`egress_bps` 字段继续保留，用于跨版本兼容和原有方向限制。

[KNOWN] `total_bps` 由 migration `009_tunnel_total_bandwidth` 增加，默认 `0` 表示未配置共享总额度，不会从旧方向字段推导。旧方向字段没有自动迁移或废弃。

[KNOWN] 限速调度必须 work-conserving：只有一个方向有待发送数据时，它可以使用 100% 总额度；两个方向都有待发送数据时，应快速趋向约 50% + 50%。“繁忙”表示确实存在待发送 payload，而不是仅保持着空闲连接。

[KNOWN] 两个网络发送方向起源于不同 Client，归属方不能仅靠暂停本地读取来控制数据到达前的网络消耗，也不能依赖共享 SCTP/PeerConnection 的全局背压，否则可能连带阻塞同一 pair 上的其他 tunnel。

[INFERRED] 共享限速应使用按 tunnel 隔离的 sender credit 协议：归属方运行总额度和公平调度器，归属方本地发送方向以及访问方远端发送方向都必须先获得该 tunnel、该方向的有限 credit 才能写入 DataChannel。访问方向 Client 上报实际待发送需求，归属方按需求发放带序号、上限和短有效期的 credit；访问方只负责遵守 credit，不成为正式流量上报方。

[KNOWN] 两个方向必须拥有相互隔离的待发送状态和 credit。A 已经积压的数据不能让 B 排到 A 的 backlog 末尾；B 从空闲变为有数据时，归属方必须停止继续把全部新额度授予 A，并让 B 参与下一轮额度分配。B 仍可能等待需求通知传播、下一个额度产生以及 A 已经获得的有限 credit，但这个等待必须由 credit window/调度块明确限制，不能随 A 的 backlog 增长。

[KNOWN] 不设置单方向 95% 的永久硬上限，也不永久空出 5% 或固定 10KB/s～1MB/s 带宽。空闲方向的额度可以被完全借用；公平性通过短时间片、有限连续发送块和新活跃方向优先参与调度实现，不能以浪费带宽换取反向响应。

[INFERRED] Credit 请求、授予和消耗消息必须绑定 tunnel grant、方向、计数 epoch 和单调序号，并与业务 DataChannel 的控制消息隔离。精确调度时间片、credit window、最大连续发送块和 burst 参数必须写成代码常量并通过低速、高速、双向突发、控制延迟和多 stream benchmark 确定；不能预先授予会造成长时间反向阻塞的大额未来 credit。

[KNOWN] Client 侧执行和上报属于协作语义。被修改或有缺陷的 Client 可以忽略限速或上报错误总量，Client 突然故障也可能丢失尚未上报的观测值；产品接受这些边界，不得将其描述为 Server 权威的点对点路径强制执行。

[KNOWN] Traffic storage、内存 accumulator 和查询 projection 已按每份 `TrafficDelta.Transport` 分桶。Relay 数据路径显式记录 `server_relay`，不能读取 selector 当前值替代 stream 的真实路线；因此 selector 已切到 direct 后仍在排空的旧 relay stream 不会被计入 direct。

[KNOWN] P2P session 关闭后保留 15 秒的最终统计宽限期。只有原 tunnel owner、原 generation、匹配的 session/grant/tunnel/revision/epoch 和向前 sequence 才能补交单调累计值；重放、越权和超时报告仍被拒绝。Client 优雅关闭会在发送控制 WebSocket close frame之前先刷新累计统计。

## Session 与授权边界

[KNOWN] P2P 不会移除 Server 控制通道，也不会让 `direct_only` Client 省略现有 Server `/ws/data`。当前 control generation 有效且当前 Server data session 已建立，才构成可以续租 P2P 的健康逻辑 Client 会话。

[KNOWN] 已认证控制通道被视为可信信令通道，但直接网络路径仍不可信。Server 应把 offer/answer/candidate、临时 DTLS identity/fingerprint、规范化 Client pair、双方当前 control generation 和 ICE 角色绑定到 pair session；Client 只能接受 Server 为该 pair session 指定的 peer identity。

[KNOWN] 每条隧道在 pair session 上拥有独立的 tunnel grant。Grant 绑定 pair-session ID、tunnel ID、revision、双方 tunnel role 和有效期；A、B 在不同 tunnel 中角色相反时仍使用各自 grant，不能把 tunnel role 固化在共享 pair session 上。

[KNOWN] P2P 授权采用最长 60 秒的短租约，并分为两个作用域：Client-pair peer session 租约确认双方逻辑 Client 会话仍健康；每条 tunnel authorization 租约确认 tunnel 仍启用且 revision/角色仍有效。续期间隔必须明显短于 60 秒，初始建议约 20 秒，并写成代码常量而不是环境变量。

[KNOWN] 任意一方 control 或当前 Server data session 失效时，Server 立即停止该 pair lease 及其全部 tunnel grant lease；Client 当前 runtime 一旦检测到自身逻辑会话失效，也应立即关闭所属 P2P session。即使断开或撤销消息被拦截，无法获得新租约的 P2P session 及 tunnel grant 也必须在最后一次有效续期后的 60 秒内关闭，不能保留一天。

[KNOWN] 删除、禁用或修改 tunnel 时，Server 立即撤销对应 tunnel authorization 并停止它的租约续期。若同一 Client pair 还有其他授权隧道，共享 PeerConnection 可以保留，但被撤销 tunnel 的已有 stream 和新开流权限必须关闭；撤销消息被阻断时，该 tunnel 也必须在最后一次有效 tunnel authorization 续期后的 60 秒内失效。若 pair session 本身不再获准，则关闭整个 PeerConnection。

[INFERRED] Peer session 与 tunnel authorization 的租约和续期都必须绑定 session ID、双方当前 control generation、单调序号或不可重放身份和到期边界；tunnel authorization 还必须绑定 tunnel ID、revision 和双方角色。攻击者仅阻断控制消息或重放旧续期，不能把 pair session 或某条 tunnel 的授权延长到 60 秒上限之外。

[INFERRED] Pair-level offer/answer/candidate/ready/closed 信令必须绑定认证后的 Client 身份、双方当前 control generation 和 pair-session ID；tunnel open/revoke/stats 信令必须另外绑定 tunnel grant、revision 和 tunnel role。Payload 自报的 Client ID 不能作为授权依据。

[INFERRED] Direct 逻辑 stream 必须携带为当前 tunnel、revision、角色和 peer session 派生的短期授权。它不能复用当前固定的 `"server-relay"` 占位 token，也不能向 peer 暴露长期 Client/Server 凭据。

[INFERRED] Offer/answer、candidate 和 description 转发必须限制大小、数量、存活期和速率。普通日志不得记录敏感 description、candidate 或临时凭据。

[KNOWN] P2P 连接状态只表示 NetsGo 链路层健康，不代表目标应用服务健康。

[KNOWN] 本 issue 不允许在 Server control/data 逻辑会话失效后长期维持 direct 流量。立即关闭是正常路径，60 秒租约只是撤销或断线消息无法送达时的安全上限，不是离线运行宽限期。

## 运行态与 Web 展示

[KNOWN] Web 必须分别展示配置策略、P2P readiness 和实际数据选择，不能只显示一个容易误导的 `actual_transport` 文案。

- [KNOWN] 配置策略显示 `server_relay_only`、`direct_preferred` 或 `direct_only`。
- [KNOWN] Peer readiness 至少能区分未启用、正在连接、direct 已就绪、失败后重试、对端离线和能力不兼容。
- [KNOWN] `direct_preferred` 连接失败时应明确显示“P2P 未就绪，当前新连接使用 Server 中继”。
- [KNOWN] PeerConnection 刚就绪时应显示“新连接将使用直连；已有 relay 连接可能继续存在”，不能暗示所有存量连接已经迁移。
- [INFERRED] Server 只有在双方对同一 peer session 均确认就绪时才投影 direct ready；单方报告应显示为连接中、待确认或状态不一致，而不是成功。
- [INFERRED] Web 可以展示最近尝试时间、最近失败原因和下一次重试时间，但不得把 ICE/P2P 链路状态解释成目标服务健康。

## 未来隧道与 datagram 扩展

[KNOWN] 任何能够使用共享可靠双向逻辑 stream 契约的未来隧道类型，都可以复用 relay 和 direct transport，无需修改 P2P 信令或建连过程。

[KNOWN] 新增这类隧道仍可能需要新的 endpoint handler、stream header 元数据和明确的 payload 统计口径。这些改动属于隧道/endpoint 层，不属于 P2P selector。

[INFERRED] 如果 NetsGo 未来增加原生不可靠或无序 datagram 路径，它应作为 stream 能力旁边的增量 transport 能力。现有策略、P2P pair session 生命周期和 fallback 协调应继续复用，现有 stream transport 不得被替换；同时必须增量扩展 capability/version、association grant、最大 datagram、分片和相关信令，不能假定现有 stream 授权原样覆盖 datagram。

[INFERRED] 因此，未来 selector 可以同时提供 stream 操作和可选 datagram 操作。当前 UDP-over-stream 继续使用 stream 操作，未来原生 UDP 隧道可以使用 datagram 操作。

## 未来客户端中继

[KNOWN] 第一版不存在 TURN 或客户端 C 中继。未来若 A、B 无法直连，可以新增由用户选择 C 为两者转发数据的能力。

[KNOWN] C 中继本质是 relay，必须使用独立的 transport 名称和 UI 状态，不能复用 `peer_direct`，也不能让 `direct_only` 暗中选择 C。

[KNOWN] A、B 的 payload 必须保持端到端加密，C 只转发密文。Server 仍负责确认 A、B、C 的授权关系、租约、限速/流量角色和运行态。

[INFERRED] 是否基于 Pion TURN、opaque packet relay 或其他机制实现 C 中继，留到该功能正式进入范围后单独设计；第一版不得为这个未来功能引入 TURN 运行时和端口范围。

## 兼容与可用性规则

- [KNOWN] 旧 Client 或能力未知的 Client 继续视为不支持 P2P。
- [KNOWN] Server 不得向未明确声明兼容 P2P 实现的 Client 发送 P2P 信令。
- [KNOWN] 只有双方明确广告匹配的 P2P capability 时，API/UI 才允许 `direct_preferred` 和 `direct_only`。
- [KNOWN] `server_relay_only` 以及未协商 P2P 的跨版本 Client/Server 组合必须保持当前 relay 行为不变。
- [KNOWN] Web UI 只为 `client_to_client` 显示 transport policy 选择，默认仍是 `server_relay_only`，并以 Server 校验为最终准绳。

## 仍有意保持开放的决策

- [KNOWN] Peer 实现已固定为 Pion WebRTC/ICE/DataChannel，第一版不使用 quic-go；当前选择是在一个可靠有序 detached DataChannel 上复用 yamux stream。
- [KNOWN] Peer session 已固定为按 Client pair 共享；精确空闲关闭、重建和最大并发资源上限仍需实现阶段确定。
- [KNOWN] 第一版不使用 TURN，`direct_only` 不允许 relay。Server 已提供同数字端口的 UDP STUN listener。Pion vnet 已覆盖 full-cone、address-restricted、port-restricted、symmetric、公网端、STUN 故障、candidate 异常、IPv6、丢包/延迟/重复包和并发 stream；独立 Linux network namespace 的双 NAT Docker E2E 还验证了 direct、UDP 全阻断后的 relay fallback、解除阻断后的生产定时重试恢复。真实运营商 CGNAT、移动网络和企业防火墙仍需独立部署验证。
- [KNOWN] Passive ICE-TCP 是机会性备用路径的原型目标，而不是已经确认的第一版能力。Pion 在 Linux、macOS、Windows、IPv4/IPv6、同 LAN、外部可达 listener 和 UDP 阻断场景下的实际覆盖必须由原型固定；第一版不承诺 TCP srflx、simultaneous-open 或严格 NAT TCP 打洞。
- [KNOWN] P2P sender-credit 与 relay 共享限速已经实现 work-conserving 公平调度，最大连续调度块为 256 KiB；仍需真实高延迟网络 benchmark 调整性能参数。
- [KNOWN] 共享总额度使用 `total_bps`；旧方向字段继续兼容，当前没有退出时间表。
- [KNOWN] 未来客户端 C 中继的协议、选择规则、资源授权和计量方式不属于第一版，需要独立设计。

## 已获得的验证证据

- [KNOWN] `pkg/p2p/network_test.go` 与 `network_matrix_extended_test.go` 使用 Pion vnet 构造真实路由与 NAT 行为，不使用两个 localhost socket 冒充 NAT；矩阵同时固定成功与预期失败组合，并覆盖 malformed/error STUN、candidate 延迟/逆序/重复、IPv4/IPv6、多地址、受损网络、大 payload、并发 stream 和持续低速传输。
- [KNOWN] `pkg/p2p/session_test.go` 验证真实 Pion PeerConnection、detached DataChannel、超过 1 MiB 的双向 payload、16 条并发 yamux stream 和有界关闭；`datachannel_stream_test.go` 单独验证大 Write 分片和任意小 Read 不改变字节序列。
- [KNOWN] `internal/server/unified_tunnel_e2e_test.go` 使用真实 Server 和 Client 覆盖 TCP、UDP-over-stream、SOCKS5 direct。UDP 使用多个不同长度和内容的 datagram，验证边界、无重复及原始 payload byte 精确统计；SOCKS5 验证握手不进入 direct 业务流量统计。
- [KNOWN] 同一 E2E 覆盖 relay → direct 的旧连接固定、新连接切换、direct 失败后旧 stream 关闭、后续新连接只回退一次、生产代码 10 秒自动重试后恢复 direct，以及持久 Client 身份断线重连后 generation 更新和 P2P session 重建。每条路径都使用精确 payload 长度检查 relay/direct 分桶，没有用 `>=` 掩盖重复字节。
- [KNOWN] 共享限速 E2E 在 64 KiB/s tunnel-wide 总额度下持续制造 8 MiB 正向 backlog，目标端反向数据仍在 2 秒有界时间内到达，证明反向流量没有排在正向 backlog 尾部。单元测试另覆盖单向借满、双向平分、闲置份额借用和 256 KiB 最大调度块。
- [KNOWN] Docker system E2E 的 current-only nginx 与 caddy 变体把 TCP、UDP、SOCKS5 `client_to_client` 配置为 `direct_preferred`，并同时等待 `p2p.state=connected` 与 `actual_transport=peer_direct`；Client 和 Server 重启后重复该断言，不能以 tunnel `active` 代替 P2P 成功。
- [KNOWN] v0.1.8/current 的完整 nginx 跨版本矩阵 11/11 通过。兼容矩阵显式固定 `server_relay_only`，因为只观察 Client capability 不能证明旧 Server 理解新 direct policy；current-only system E2E 则通过 `NETSGO_E2E_REQUIRE_P2P=1` 强制 direct capability 和实际直连断言。
- [KNOWN] 当前验证通过 `go test ./... -count=1`、`go vet ./...`、`make test-race`、Web lint/171 项测试/build、`make build` 和 `git diff --check`。

## 已采用的实现顺序

1. [KNOWN] 引入 transport-neutral stream 契约和 selector，并让 TCP、UDP-over-stream、SOCKS5 共用该路径。
2. [KNOWN] 引入 Pion PeerConnection、detached DataChannel 和 yamux，增加强类型信令与按 Client pair 的 session registry。
3. [KNOWN] 增加 pair/tunnel 短租约、grant/revoke、generation/revision/role/sequence 校验以及 candidate 大小、数量和速率限制。
4. [KNOWN] 增加 `total_bps` wire/storage/UI/relay 语义，再实现 direct sender-credit、公平调度和 payload-byte 统计。
5. [KNOWN] 开放 capability-gated API/UI，增加 direct、fallback、direct-only、TCP、UDP、SOCKS5 与迁移 E2E。
6. [KNOWN] 保持 TURN、quic-go 和 passive ICE-TCP 不在第一版运行范围内。

## 所需验证

- [KNOWN] 三种 policy 行为符合上表，`server_relay_only` 仍为默认值。
- [KNOWN] 任何应用 payload 或 UDP datagram 都不会在 relay 和 direct 间重复发送。
- [KNOWN] 计划内 relay-to-direct 切换期间，已有连接保持固定，只有新流量使用新选择。
- [KNOWN] Direct 失败按文档影响已有 direct 流量，只有后续 `direct_preferred` 流量改走 relay。
- [KNOWN] TCP、UDP-over-stream 和 SOCKS5 共用一个 selector 和一个 target 分发，不存在按类型复制的 P2P 实现。
- [KNOWN] Direct 流量报告不会重复计数，保持 payload-byte 语义，并按 actual transport 与 relay 流量分离。
- [KNOWN] 只有服务归属方上报 Direct 流量；另一端报告不会造成重复计数，控制重试不会重复累计。
- [KNOWN] Direct 限速使用一个显式双向共享总额度：单向活跃可使用 100%，双向活跃快速趋向平分，新活跃方向不会排在另一方向的 backlog 后面。
- [KNOWN] 新共享字段以及旧 `ingress_bps` / `egress_bps` 的迁移、跨版本投影和退出有明确测试，relay 与 direct 不会解释出不同的限速结果。
- [KNOWN] 限速在极低、极高、单向、双向突发和多 stream 场景下保持 work-conserving 且不会饿死任一方向。
- [KNOWN] 过期 session、generation、revision、role、credential 和 candidate 消息会被拒绝。
- [KNOWN] 删除/禁用时 Server 立即发送 tunnel grant 撤销，Client 收到后立即关闭对应 direct stream；撤销消息被阻断或旧续期被重放时，该 grant 仍会在最后一次有效续期后的 60 秒内关闭且不影响同 pair 的其他有效 tunnel。
- [KNOWN] Pion UDP candidate 是默认路径；只有 passive ICE-TCP 原型通过后才启用可达 TCP candidate 备用，TCP srflx、simultaneous-open 和严格 NAT 失败不会被误报为已支持。
- [KNOWN] P2P 建连与重试按 Client pair 合并，稳定重试约一分钟并带随机偏移；多条 tunnel 不会成倍制造 ICE session。
- [KNOWN] Web 同时正确展示策略、P2P readiness 和当前新连接 transport，并说明旧 relay stream 不迁移。
- [KNOWN] 控制信令和 relay fallback 在 Server 直连、nginx、caddy 三类路径下均可工作。
- [KNOWN] System E2E 覆盖同网络直连、两个独立 namespace/NAT 的真实内核转发、direct 被阻断后的 fallback 与自动恢复、`direct_only` 失败、Client 重连和 Server 重启行为。
- [KNOWN] 双 NAT Docker E2E 另以真实 Linux network namespace 验证 UDP hairpin；该结论不依赖 Pion vnet，因为 vnet 的 NAT hairpin 选项没有实现。
- [KNOWN] 跨版本兼容保证不支持 P2P 的 Client 保持 relay，且不会收到 P2P 信令。
- [KNOWN] UI 只在双方能力匹配时开放 direct 策略，并且不得把尚未验证的 NAT/ICE-TCP 拓扑描述成已支持。
