# PR #48 Code Review: Unified Tunnel End-to-End

> **参见**: [`full-code-review.md`](./full-code-review.md) — 覆盖整个仓库（协议层、客户端、服务端、前端、基础设施、测试、整体架构）的全面代码审查报告，本报告仅聚焦 PR #48 本身的变更。

**Branch:** `docs/unified-tunnel-closeout`
**Scope:** 97 files, +15255 / -1213 lines
**Review Date:** 2026-05-29

---

## 1. 变更概述

本 PR 实现了统一隧道的端到端功能，涵盖两大拓扑：

- **Server-Expose**：服务端监听（TCP/HTTP/UDP），通过 yamux 数据流将流量中继到目标客户端的本地服务。
- **Client-to-Client Relay**：入口客户端接受外部连接，服务端中继流量到目标客户端，两个客户端均通过 provision/unprovision 控制消息参与。

核心架构包括：
- 统一隧道生命周期管理（pending → exposed/error → offline/idle）
- Reconcile 引擎（客户端重连恢复、数据通道就绪、周期性重试）
- 新协议消息（tunnel_provision / unprovision / preflight / runtime_report）
- DataStreamHeader 二进制帧头替代旧版 name-based yamux 路由
- REST API `/api/tunnels` + revision 乐观并发控制
- SQLite migration 005（统一隧道 schema、资源锁、流量分桶）
- 前端统一隧道模型 + c2c 拓扑 UI + Playwright E2E
- Dev compose stack + Playwright CI 集成

---

## 2. 审查结论

整体架构合理、设计扎实，状态机转换正确，测试覆盖率高。以下按严重程度列出需要关注的问题。

---

## 3. Critical Issues

**(无)**

---

## 4. Important Issues

### 4.1 并发 Reconcile 可能导致错误的 Error 状态

**文件：** `internal/server/unified_tunnel_reconcile.go:41-64`

`scheduleUnifiedTunnelReconcile` 每次调用都启动一个无界 goroutine。周期性 `unifiedTunnelReconcileLoop` 也会每分钟对所有 running 隧道触发 reconcile。如果两者同时触发同一隧道，两者都会调用 `waitForClientTunnelProvisionAck` → `registerProvisionAckWaiter`。第二个注册会失败（"already has a pending provisioning ack waiter"），被当作 provision 失败处理，可能导致隧道进入 error 状态。

更严重的是，第二个 goroutine 的 error 路径可能调用 `unprovisionClientRelayTunnel` 或 `failUnifiedServerExposeAfterProvision`，从而拆除已经成功 provision 的隧道。

**建议：** 添加 per-tunnel reconcile 互斥锁或 "reconcile in progress" 守卫（如 `sync.Map[tunnelID]struct{}`），使同一隧道的并发 reconcile 串行化或去重。

---

### 4.2 `scheduleUnifiedTunnelReconcile` 使用过时的 StoredTunnel 快照

**文件：** `internal/server/unified_tunnel_reconcile.go:41-64`

函数以值捕获 `stored StoredTunnel` 并在 goroutine 中直接传递。如果在调度到执行之间隧道的期望状态发生变化（例如用户通过 API 停止了隧道），reconcile 会使用过时的 "running" 状态，可能对一个应该被停止的隧道发起 provision。

对比 `reconcileUnifiedTunnel`（第 15-27 行），它通过 `findStoredTunnelByID` 正确地重新读取当前状态。

**建议：** `scheduleUnifiedTunnelReconcile` 应调用 `reconcileUnifiedTunnel(stored.ID, reason)` 而非 `reconcileStoredUnifiedTunnel(stored, reason)`，以确保从存储重新读取当前状态。

---

### 4.3 双重 `releaseUnifiedRuntimeForClient` 调用发送重复 unprovision 消息

**文件：** `internal/server/data.go:162-163` 和 `internal/server/session.go:177`

数据通道断开时，`handleDataWS` 在第 162 行调用 `releaseUnifiedRuntimeForClient`，紧接着在第 163 行调用 `invalidateLogicalSessionIfCurrent`，后者在 `session.go:177` 又调用了一次 `releaseUnifiedRuntimeForClient`。两次调用都会遍历存储的隧道并向 client-relay 参与者发送 unprovision 消息。

虽然客户端应该对 unprovision 幂等，但这浪费带宽并可能导致混乱的日志。

**建议：** 移除 `data.go:162` 的 `releaseUnifiedRuntimeForClient` 调用（`invalidateLogicalSessionIfCurrent` 已经处理），或为 `releaseUnifiedRuntimeForClient` 添加 per-session 幂等守卫。

---

### 4.4 `reconcileClientRelayTunnel` 在 provisioning 完成前注册 c2c 条目

**文件：** `internal/server/client_relay.go:127-158`

第 127 行 `s.c2c.set(stored)` 在 target 和 ingress provisioning 之前调用。在这个 `set` 和 provision ack（最长 5 秒）之间，隧道在 c2c registry 中显示为 active。如果 ingress 客户端在此窗口期间打开数据流（例如来自尚未清理的上一次 provision），`handleClientOpenedDataStream` 会接受它并尝试中继流量到一个尚未确认 provisioning 的 target。

**建议：** 将 `s.c2c.set(stored)` 移到两个 provision ack 都成功之后（约第 157 行之前），或为 c2c registry 添加 "provisioning" 状态。

---

### 4.5 Update 操作在验证 revision 原子性之前 unprovision 旧隧道

**文件：** `internal/server/unified_tunnel_api.go:607-653`

更新流程：
1. `findUnifiedTunnelSpecByID`（读取当前 revision）
2. 检查 `req.ExpectedRevision != current.Revision`
3. 构建新 stored tunnel
4. `GetTunnelByIDE` 再次检查 revision
5. **Unprovision 旧隧道**（向活跃客户端发送 unprovision 消息）
6. `ReplaceTunnelByID`（在 SQL 中第三次检查 revision）

如果步骤 6 因 revision 冲突失败（另一个写者抢先），旧隧道已在步骤 5 被 unprovision，客户端处于不一致状态——它们收到了 unprovision 消息但数据库仍持有旧配置。

**建议：** unprovision 应在 DB 写入成功之后执行，或在失败时进行补偿性 re-provision。

---

### 4.6 Delete 操作存在部分失败窗口

**文件：** `internal/server/unified_tunnel_api.go:386-419`

删除流程：unprovision clients → 清理 runtime issues → 从 store 删除 → 发送事件。如果 `deleteStoredUnifiedTunnel` 失败（DB 错误），客户端已经被 unprovision，但隧道仍存在于数据库中。隧道处于僵尸状态：DB 说它存在，但客户端已拆除。

**建议：** 考虑先执行 DB 删除再 unprovision，或在 DB 失败时记录并安排重试清理。

---

### 4.7 三重常量重复定义

**文件：**
- `pkg/protocol/types.go:14-65`
- `internal/server/unified_tunnel_api.go:19-39`
- `internal/server/store.go:25-38`

同一个字符串值（如 `server_expose`、`http_host`、`server_relay`）在三个地方定义。这直接违反了 `agents.md` 的原则："不要新造一套与 `pkg/protocol/` 平行的消息结构"。

**建议：** Server 端应直接使用 `protocol.*` 常量。

---

### 4.8 `TunnelCreateRequest` 是 `TunnelSpec` 的完整别名

**文件：** `pkg/protocol/message.go:102`

`type TunnelCreateRequest = TunnelSpec` 意味着客户端发送完整的 `TunnelSpec`，包括客户端不应设置的字段：`RuntimeState`、`Issues`、`Participants`、`Transport`、`P2P`、`CreatedAt`、`UpdatedAt`、`Capabilities`、`CreatedByUserID`。服务端必须忽略或覆盖这些字段，但没有验证契约来阻止客户端发送误导性值。

**建议：** 使用专门的 struct 只包含客户端可设置的字段，或者在服务端明确 zero/override 这些字段。

---

### 4.9 `TunnelSpec` 内部存在重复状态

**文件：** `pkg/protocol/types.go:138-168`

- `TransportPolicy` 和 `ActualTransport` 同时存在于顶层字段和 `Transport TransportRuntime` 内部。
- `P2P P2PState` 是顶层字段，同时 `Transport.P2PState`/`Transport.P2PError` 以不同形式复制了相同信息。
- `Participants` 包含 `Ingress`/`Target`，各自有 `Revision`，而 `TunnelSpec` 也有顶层 `Revision`。

这造成了歧义：哪个是权威来源？如果它们不同步，消费者（API、事件、存储）将看到不一致的状态。

**建议：** 明确文档化哪些字段是规范的，哪些是反规范化的投影。理想情况下消除重复或添加验证保持一致性。

---

### 4.10 Ingress goroutine 没有被追踪——无法保证优雅关闭

**文件：** `internal/client/unified_tunnel.go:343-345`

`startIngressTunnelRuntime` 通过裸 `go` 调用启动 `acceptIngressTCP` / `acceptIngressUDP`。这些 goroutine 没有被加入任何 `sync.WaitGroup`。`cleanup()` 调用 `rt.wg.Wait()` 但只覆盖控制循环 goroutine。Ingress goroutine 可能在 cleanup 之后仍然存活。

重连期间存在窗口：上一个 session 的旧 ingress goroutine 仍在运行，而新 session 的已启动。

**建议：** 为 `clientTunnelRuntime` 添加 `sync.WaitGroup`，让 `close()` 等待它。

---

### 4.11 `findUnifiedTunnelSpecByID` 对每次单隧道查询执行全表扫描

**文件：** `internal/server/unified_tunnel_api.go:1381-1395`

每个 GET/PUT/DELETE/action `/api/tunnels/{id}` 都调用 `allUnifiedTunnelSpecs()`，它加载所有存储的隧道并遍历所有活跃客户端的 proxy configs，然后线性扫描结果。对于 PUT，这发生两次。

Store 已有 `GetTunnelByID(id)` 做直接 SQL 主键查询。统一列表函数不应成为单条查询的路径。

**建议：** 单条查询应直接使用 `GetTunnelByID` + 本地构建 `TunnelSpec`，而非加载全量列表。

---

### 4.12 前端 `getTrafficSeriesKey` 在 TrafficChart 和 use-traffic-rates 之间不一致

**文件：**
- `web/src/components/custom/chart/TrafficChart.tsx:60-62`
- `web/src/hooks/use-traffic-rates.ts:41-46`

两个文件用不同语义定义 `getTrafficSeriesKey`。对于 `tunnel_id: "abc"` 但无 `tunnel_name`/`tunnel_type` 的项，TrafficChart 产生 key `"abc"` 而 use-traffic-rates 产生 `"id:abc"`。聚合速率视图和图表视图可能对同一序列做出不同判断。

**建议：** 抽取为单一共享函数。

---

## 5. Minor Issues

### 5.1 `MsgTypeProxyClose` 别名改变了线上消息类型字符串

**文件：** `pkg/protocol/message.go:41`

`MsgTypeProxyClose` 原为 `"proxy_close"`，现在是 `= MsgTypeTunnelUnprovision` 即 `"tunnel_unprovision"`。虽然不需要向后兼容，但 `notifyClientProxyClose` 仍然发送 `ProxyCloseRequest` payload，客户端依赖 `tunnel_id` 字段的存在来区分两种消息格式。一旦旧路径完全移除，别名也应删除。

**建议：** 添加 `// TODO(cutover): remove MsgTypeProxy* aliases` 注释并跟踪移除。

---

### 5.2 `EndpointSpec.Config` 是无约束的 `json.RawMessage`

**文件：** `pkg/protocol/types.go:73`

端点特定配置（如 tcp_listen 的端口、http_host 的域名）通过 `Config json.RawMessage` 传递，协议层零结构验证。畸形或缺失的 config 只会在深层处理器中失败。

**建议：** 至少添加 `Validate()` 方法或协议级检查 `Config` 在必需时非空。

---

### 5.3 新隧道消息类型缺少 round-trip 测试

**文件：** `pkg/protocol/message_test.go`

有 `TunnelIssue` 和 `TunnelSpec` 的 round-trip 测试，但没有 `TunnelProvisionRequest`、`TunnelCreateResponse`、`TunnelProvisionAck`、`TunnelUnprovisionRequest`、`TunnelRuntimeReport`、`TunnelPreflightRequest` 或 `TunnelPreflightResponse` 的测试。这些是核心控制面 payload。

**建议：** 为每种新消息 payload 类型添加 JSON round-trip 测试。

---

### 5.4 `ServerAuthorized` 字段可由客户端设置

**文件：** `pkg/protocol/stream_header.go:42`

`DataStreamHeader` 中的 `ServerAuthorized bool` 字段可被客户端构造。验证函数在第 155 行跳过 `OpenToken` 要求当 `ServerAuthorized == true` 时。

**建议：** 为 `ValidateDataStreamHeader` 添加文档明确说明 `ServerAuthorized` 验证仅是结构检查，不可作为唯一授权门控。

---

### 5.5 `issuesForStoredTunnel` 多次获取/释放 RLock，可能读到不一致数据

**文件：** `internal/server/unified_tunnel_runtime.go:79-92`

函数在 RLock 下读 `serverIssues`，释放锁，然后为每个 role 调用 `issueForRole` 单独获取/释放 RLock。在这些锁获取之间，另一个 goroutine 可能修改 map。结果可能是新旧数据的混合。

---

### 5.6 `recordServerIssue` 用单条 issue 替换所有 issue

**文件：** `internal/server/unified_tunnel_runtime.go:74-76`

如果一个隧道存在多个不同 issue（如 provision 超时后 ingress 路由失败），只保留最新的。早期 issue 被静默丢弃。

---

### 5.7 `reconcileRunningUnifiedTunnels` 静默丢弃 reconcile 错误

**文件：** `internal/server/unified_tunnel_reconcile.go:92`

周期性重试循环的错误被静默丢弃（`_ = s.reconcileStoredUnifiedTunnel(...)`）。虽然 `scheduleUnifiedTunnelReconcile` 记录了错误，但重试循环没有。这使得诊断持久性 reconcile 失败更加困难。

---

### 5.8 `preflightServerIngressResource` 端口可用性检查存在 TOCTOU

**文件：** `internal/server/unified_tunnel_api.go:941-955`

TCP/UDP 端口可用性检查执行 `net.Listen`/`net.ListenPacket` 然后立即关闭。在关闭和实际隧道 provisioning 之间，另一个进程（或并发请求）可以占用端口。`tunnel_resource_locks` 表提供 DB 级冲突检测，但 listen-probe 和实际 bind 之间的间隙仍然存在。

---

### 5.9 `Traffic buckets` 主键不包含 `client_id`

**文件：** `internal/server/migrations/005_unified_tunnel_storage.sql:196`

```sql
PRIMARY KEY (tunnel_id, transport, resolution, bucket_start)
```

`client_id` 不是 PK 的一部分。如果两个不同客户端以某种方式共享相同的 `tunnel_id`，流量数据会冲突。

---

### 5.10 `AddTunnel` TOCTOU：存在性检查在事务外

**文件：** `internal/server/store.go:294-333`

唯一性检查在事务开始之前执行。虽然 DB 的 `UNIQUE(client_id, name)` 约束提供安全网，但错误消息将是原始 SQLite 约束违规，而非友好的错误信息。

---

### 5.11 `revisionConflictPayload` 有冗余 `error` 和 `message` 字段

**文件：** `internal/server/unified_tunnel_api.go:368-384`

`tunnelMutationErrorResponse` 的 `error` 和 `message` 都被设置为相同字符串。同样 `error_code` 和 `code` 也总是相同值。冗余的 API 表面。

---

### 5.12 `decodeServiceEndpointConfig` 静默将 `IP` 别名为 `Host`

**文件：** `internal/server/unified_tunnel_api.go:550-569`

函数将 `IP` 规范化为 `Host`，然后将 `Host` 写回 `IP`。提交 `{"host":"myhost","ip":"1.2.3.4"}` 的调用者会发现 `ip` 被 `host` 静默覆盖。

---

### 5.13 UDP session 常量调整后缺少文档

**文件：** `internal/server/udp_proxy.go:164-172`

`UDPSessionTimeout` 从 60s 改为 2min，`MaxUDPSessions` 从 1024 改为 4096，`MaxUDPSessionsPerIP` 设为等于 `MaxUDPSessions`。这些是重要的行为变更，但缺少解释变更原因的注释。

---

### 5.14 `unsafe type assertion` 在 `proxyForDataStreamHeader`

**文件：** `internal/client/client.go:983`

```go
cfg := val.(protocol.ProxyNewRequest)
```

非检查类型断言。如果任何代码路径在 `proxies` 中存储了非 `ProxyNewRequest` 的值，会 panic。线性扫描回退（第 991 行）使用了安全模式 `candidate, ok := value.(...)`，初始查找应使用相同模式。

---

### 5.15 `getOrCreateIngressUDPAssociation` 在原子检查之前打开流

**文件：** `internal/client/unified_tunnel.go:444-500`

函数先做非原子 `Load`（第 446 行），如果未找到，打开 yamux 流（第 466 行），写入数据流头（第 482 行——网络 I/O），然后才做 `LoadOrStore`（第 491 行）。如果来自同一源地址的两个数据报并发到达，两个 goroutine 都会打开流并写入头。输家丢弃其流，处理正确，但在每次竞争时浪费 yamux 流和不必要的网络 I/O。

---

### 5.16 CI `test-e2e` 中重复 `bun install` + `build-web`

**文件：** `.github/workflows/ci.yml:154-162` 和 `Makefile:144`

`test-e2e` job 已经下载并恢复了 `web-dist` artifact（含 `web/dist/`）。新的 Playwright 步骤然后运行 `bun install --frozen-lockfile`（完整依赖安装）和 `make test-playwright-e2e-smoke`（依赖 `build-web`，再次运行 `bun install && bun run build`）。依赖被安装两次，前端被构建两次。

---

### 5.17 Playwright compose volume 挂载路径 `/var/lib/netsgo` 未被使用

**文件：** `test/e2e/docker-compose.playwright.yml:28,77,101`

Server 和 client 没有传 `--data-dir`，所以使用 `DefaultDataDir()`。在 Docker 容器内解析为 `/root/.local/state/netsgo`（非 `/var/lib/netsgo`）。命名卷从未被写入。

---

### 5.18 前端 TunnelDialog `isValid` 逻辑 edit 和 create 模式完全重复

**文件：** `web/src/components/custom/tunnel/TunnelDialog.tsx:345-365`

`isEdit ? ... : ...` 两个分支字符级相同。应简化为单一表达式。

---

### 5.19 `TunnelTable.tsx` traffic24h map key 使用可能为 undefined 的字段

**文件：** `web/src/components/custom/tunnel/TunnelTable.tsx:30`

```ts
`${item.tunnel_type}:${item.tunnel_name}`
```

`TunnelTrafficSeries` 上两个字段都是可选的。对 metadata-missing 项产生 `"undefined:undefined"`。应使用 `use-traffic-rates.ts` 中的 `getTrafficSeriesKey` helper。

---

## 6. Nit Issues

### 6.1 变量名 `strings` 遮蔽包 `strings`

**文件：** `pkg/protocol/stream_header.go:158`

局部变量 `strings` 遮蔽标准库 `strings` 包。虽非 bug，但可能导致未来编辑时混淆。

---

### 6.2 mux 层常量命名与 protocol 层不一致

**文件：**
- `pkg/protocol/stream_header.go:18-20`：`DataStreamHeaderMaxLen`
- `pkg/mux/data_stream_header.go:12-14`：`MaxDataStreamHeaderLength`

建议对齐命名风格。

---

### 6.3 `mustRawJSON` 在 HTTP handler 路径中 panic

**文件：** `internal/server/unified_tunnel_api.go:1266-1272`

虽然对简单 struct marshal 不应失败，但 panic 在 HTTP handler 中过于激烈。考虑返回 error。

---

### 6.4 `trafficAccumulatorShardCount` 硬编码为 32 无注释

**文件：** `internal/server/traffic_accumulator.go:14`

选择 32 的原因未文档化。

---

### 6.5 `checkedTrafficAdd` 溢出检查使用 `^uint64(0)` 而非 `math.MaxUint64`

**文件：** `internal/server/traffic_store.go:624-629`

`^uint64(0)` 不如 `math.MaxUint64` 可读。

---

### 6.6 硬编码 `OpenToken` 魔法字符串

**文件：** `internal/client/unified_tunnel.go:537`

```go
OpenToken: "server-relay",
```

应定义为 `pkg/protocol/` 中的常量。

---

### 6.7 Tunnel 代码使用 `log.Printf` 而非结构化 `EventLogger`

**文件：** `internal/client/unified_tunnel.go`（多处）

主客户端代码使用 `c.logger().Info/Warn(...)` 进行结构化事件日志，但所有隧道代码使用 `log.Printf` 带 emoji 前缀。这使得隧道事件对结构化事件管道不可见。

---

### 6.8 Playwright `waitForTunnelState` 硬编码 `client_to_client` 拓扑

**文件：** `web/e2e/helpers.ts:149`

helper 仅适用于 c2c 隧道。未来测试如需验证 server-expose 隧道会失败。建议接受 topology 参数。

---

### 6.9 `ConfirmDialog` 没有 focus trap

**文件：** `web/src/components/custom/common/ConfirmDialog.tsx:26-53`

自定义 ConfirmDialog 没有使用 focus trap。键盘用户可以 tab 到对话框后面。考虑迁移到 shadcn `AlertDialog` 组件。

---

## 7. 架构评估

### 7.1 状态机设计

状态转换路径清晰：`pending → exposed/error → offline/idle`。`tunnelRuntimeSnapshot` 正确追踪 ingress、target、transport 三个参与者的独立状态，`aggregateTunnelRuntimeState` 提供了正确的聚合逻辑。revision 机制有效防止了过期 ACK 覆盖新配置。

### 7.2 数据面设计

`DataStreamHeader` 二进制帧（magic "NGDS" + version + length + JSON）设计合理，验证彻底（UTF-8 检查、未知字段拒绝、尾部数据检查、长度限制）。yamux 流路由从 name-based 迁移到 tunnel/revision/role/transport identity，是正确的方向。

### 7.3 Reconcile 引擎

Reconcile 触发来源全面：客户端重连、数据通道就绪、周期性重试、runtime report、API 变更。Server-expose 和 client-relay 两条路径的处理逻辑清晰。但并发安全问题（Issue 4.1）需要优先解决。

### 7.4 API 设计

REST 端点遵循标准 CRUD + action 模式。Revision 乐观并发控制正确。Preflight 检查提供了创建前验证。但 Update/Delete 的部分失败窗口（Issue 4.5/4.6）需要在合并前或紧随其后解决。

### 7.5 流量追踪

多分辨率流量追踪（内存秒级实时索引、pending 分钟桶、持久化分钟/小时 SQLite 桶，带压缩和保留策略）设计良好。overflow 检查正确。

### 7.6 前端

类型系统使用有效。字段级错误处理是好的 UX 触点。E2E 测试覆盖了 happy path、生命周期操作、验证错误和响应式布局。流量 key 不一致问题（Issue 4.12）需要修复。

---

## 8. 建议优先级

| 优先级 | Issue | 说明 |
|--------|-------|------|
| P0 | 4.1 | 并发 reconcile 可能导致生产环境隧道错误状态 |
| P0 | 4.2 | 过时快照可能导致已停止隧道被重新 provision |
| P0 | 4.5 | Update 部分失败窗口导致客户端不一致 |
| P1 | 4.3 | 重复 unprovision 消息（功能正确但浪费） |
| P1 | 4.4 | C2C 注册时序窗口（低概率但可能接受未准备好的流量） |
| P1 | 4.6 | Delete 部分失败窗口（DB 失败导致僵尸隧道） |
| P1 | 4.7 | 三重常量重复定义（维护风险） |
| P1 | 4.10 | Ingress goroutine 未追踪（重连确定性） |
| P2 | 4.8 | TunnelCreateRequest 类型别名（安全但设计不洁） |
| P2 | 4.9 | TunnelSpec 内部重复状态（歧义源） |
| P2 | 4.11 | 单条查询全表扫描（性能，当前可接受） |
| P2 | 4.12 | 前端流量 key 不一致（数据显示可能不准确） |

---

## 9. 测试覆盖评估

### 9.1 Go 测试

覆盖率高，涵盖：
- TCP/UDP relay 端到端
- 过期 revision 拒绝
- Provision timeout 和 ingress provision 失败清理
- Active reconcile 幂等性
- Capability loss 错误投影
- Target data offline 场景
- 流量 accumulator 聚合、overflow 保护、flush/reload
- 流量 store 压缩、保留驱逐、重命名合并、小时 rollup
- Schema migration（空 DB、现有数据、孤儿流量）
- 并发访问安全

建议补充：
- Update 部分失败（unprovision 成功但 DB 写入失败）
- 并发更新同一隧道的 revision 冲突

### 9.2 前端测试

- `tunnel-model.test.ts`：模型构建和验证
- `use-tunnel-mutations.test.ts`：mutation 路径
- `use-traffic-rates.test.ts`：速率计算
- Playwright E2E：c2c 创建、生命周期（stop/edit/resume/delete）、表单 UX、验证错误、端口冲突

### 9.3 E2E 测试

- Client-to-client TCP/UDP relay 端到端
- Client relay delete/stop unprovision
- Direct-only relay rejection
- 统一隧道 Playwright 覆盖
