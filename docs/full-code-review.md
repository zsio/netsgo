# NetsGo 仓库全面代码审查报告

**审查范围**: 整个仓库（以 PR #48 unified tunnel 为重点，同时覆盖协议层、客户端、服务端、前端、基础设施、测试与整体架构）
**审查日期**: 2026-05-29
**审查原则**: 以正确性为第一要务；不考虑旧版本兼容；不留临时性/妥协/凑合的实现；代码清晰、高效、可读；从用户体验、算法、架构、安全、性能等多角度审查

---

## 0. 总体评价

NetsGo 整体是一个**架构清晰、分层合理、测试覆盖较高**的隧道穿透工具。PR #48 引入的 unified tunnel 是一次重大重构，统一了 server-expose 与 client-to-client 两种拓扑的生命周期管理、Reconcile 引擎、API 与存储模型。

**核心优势**：
- 控制面 / 数据面完全分离（WebSocket JSON + yamux 二进制帧）
- 声明式 Reconcile + Revision 乐观并发 + SQLite 持久化，状态机转换路径清晰
- 协议层 `pkg/protocol/` 抽象良好，二进制帧设计（magic + version + length + payload）便于演进
- 前端采用 TanStack Query + shadcn/ui，组件职责划分清晰
- 测试覆盖全面，包括 SQLite schema 验证、流量 uint64 溢出边界、协议 round-trip、Playwright E2E

**核心风险**：
- 并发场景下的一致性保障不足（Reconcile 竞态、Update/Delete 部分失败窗口、DB 非串行隔离）
- 网络编程边界处理粗糙（UDP 临时错误关闭整个 runtime、TCP stream 无超时）
- 部分硬编码凭据、root 容器、预发布依赖等基础设施安全问题
- 前端错误处理与可访问性存在短板

本报告按严重程度与关注维度组织，所有问题都给出文件位置与修复建议。

---

## 1. Critical（必须修复，影响正确性 / 安全 / 数据一致性）

### 1.1 Reconcile 并发竞态
- **位置**: `internal/server/unified_tunnel_reconcile.go` (schedule / reconcile 路径)
- **问题**: `scheduleUnifiedTunnelReconcile` 每次启动新 goroutine；周期性 reconcile、客户端重连触发、API 触发、runtime report 触发四条路径可并发对同一 tunnel 执行 reconcile，导致 provision ack waiter 冲突、重复 unprovision、状态错乱。
- **建议**: 引入 per-tunnel 互斥锁（`map[string]*sync.Mutex` + 全局锁保护），或将 reconcile 调度改为"标记 dirty + 单 worker 串行消费"。所有 reconcile 统一走 `reconcileUnifiedTunnel(id, reason)` 并从 DB 重新加载最新 `StoredTunnel`，避免使用值捕获的过期快照。

### 1.2 Update/Delete 部分失败窗口
- **位置**: `internal/server/unified_tunnel_api.go` 的 update / delete 路径；`internal/server/store.go` 的 `ReplaceTunnelByID` / `DeleteTunnelByID`
- **问题**: 当前顺序为"读 revision → 校验 → unprovision 旧隧道（客户端状态已变更）→ DB 写入"。若 DB 写入失败，客户端已被 unprovision 但 DB 仍为旧配置，隧道进入不一致状态。
- **建议**: 将 DB 写入前置（事务内完成），DB 成功后再 unprovision；或在事务中记录"意图"，由 reconcile 完成实际 unprovision 并回写最终状态。

### 1.3 Tunnel 创建竞态（SQLite 非串行隔离）
- **位置**: `internal/server/store.go:294-334`
- **问题**: `SELECT ... WHERE name = ?` 与后续 `INSERT` 不在同一事务/锁内，两个并发请求可能都通过存在性检查，创建重名隧道。
- **建议**: 使用 `BEGIN IMMEDIATE`；或 `INSERT ... ON CONFLICT (client_id, name) DO NOTHING` 配合 `RowsAffected` 判断；同时在 schema 上确认 `UNIQUE(client_id, name)` 约束存在。

### 1.4 Data Session 切换竞态
- **位置**: `internal/server/data.go:124-131`
- **问题**: `dataMu` 解锁后关闭旧 session 前，新 session 可能已开始接受 stream，导致 Accept 与 Close 并发冲突。
- **建议**: 在锁内把旧 session 标记为待关闭，由专门的 goroutine 异步关闭；或使用 `yamux.Session.Close` 前先 `SetDeadline` 让 Accept 立即返回。

### 1.5 Goroutine 生命周期与 shutdown 泄漏
- **位置**: `internal/server/unified_tunnel_reconcile.go:125-137`；`internal/client/client.go:449-475`
- **问题**: Reconcile goroutine 在 `s.done` 关闭后仍可能继续执行；客户端 `Shutdown` 仅 `time.Sleep(100ms)` 后调用 cleanup，不等待所有 goroutine 退出。
- **建议**: 引入 `sync.WaitGroup` 或 `errgroup`，在 shutdown 时等待所有 reconcile/ingress goroutine 完成；客户端 shutdown 使用 `context.WithTimeout` + WaitGroup。

### 1.6 协议层 SourceRole/TargetRole 验证漏洞
- **位置**: `pkg/protocol/stream_header.go:180-185`
- **问题**: 仅在非空时校验是否为已知角色，但未结合 `Direction` 做语义约束。`ingress_to_target` 方向允许任意 SourceRole/TargetRole 组合，破坏协议不变量。
- **建议**: 根据 Direction 强制 SourceRole/TargetRole 的合法集合（如 ingress_to_target 时 SourceRole 必须为 `server`/`ingress`，TargetRole 必须为 `target`）。

### 1.7 UDP 读取错误关闭整个 runtime
- **位置**: `internal/client/unified_tunnel.go:415-423`
- **问题**: `ReadFrom` 任意错误（包括 `os.ErrDeadlineExceeded` 等临时错误）都触发 `failIngressTunnelRuntime`，整个 tunnel 不可用。
- **建议**: 区分临时错误（继续重试）与致命错误（关闭 runtime）；先检查 `<-runtime.done` 避免 shutdown 期间的误报。

### 1.8 UDP 回复读取 goroutine 竞态
- **位置**: `internal/client/unified_tunnel.go:578-595`
- **问题**: 检查 `runtime.done` 与 `runtime.packetConn.WriteTo` 之间 runtime 可能已 shutdown，`packetConn` 为 nil 导致 panic；读取失败静默 return 不记录日志。
- **建议**: 在 `runMu` 下取到 `packetConn` 后释放锁再写入；读取失败记录日志；增加 `SetReadDeadline` 配合 done channel。

### 1.9 JSON 配置复杂度拒绝服务
- **位置**: `internal/server/unified_tunnel_api.go:581-595` (`decodeStrictEndpointConfig`)
- **问题**: 未限制 JSON 嵌套深度/复杂度，深层嵌套 JSON 可导致解析栈溢出或高内存占用。
- **建议**: 添加最大深度限制（手写解析器计数，或在 Unmarshal 后做递归校验）。

### 1.10 硬编码凭据
- **位置**: `Makefile:71,76`（`DEV_KEY`、`DEV_INIT_ADMIN_PASSWORD=admin.2026`）；`docker-compose.dev.yml:36`（`password123`）
- **问题**: 开发环境凭据硬编码且可预测，若被误用于生产/公开部署将严重危及安全。
- **建议**: 改为必填环境变量，无默认值；或在 compose 中使用 `secrets` 文件 + `.env`（gitignored）。

### 1.11 Docker 容器以 root 运行
- **位置**: `Dockerfile:64-78` (e2e stage)、`dev-tools` stage
- **问题**: 未显式设置 `USER`，容器默认 root 运行，违反最小权限原则。
- **建议**: 添加 `RUN addgroup/adduser` + `USER netsgo`；生产镜像使用 `scratch`/`distroless`。

### 1.12 E2E Compose 入口已重构
- **位置**: `test/e2e/docker-compose.system.yml`、`test/e2e/docker-compose.proxy.*.yml`
- **现状**: 旧 stack Compose 入口已被移除，系统级 E2E 由 Go 测试控制业务流程，Compose 只负责真实拓扑。

### 1.13 Vite 使用预发布版本
- **位置**: `web/package.json:49` (`vite: ^8.0.0-beta.13`)
- **问题**: 生产构建依赖 beta 版 Vite，存在稳定性与兼容性风险。
- **建议**: 锁定到当前稳定版本（v6.x 或 v7.x）。

---

## 2. High（重要问题，应在发布前修复）

### 2.1 Server-Expose 创建时状态预判
- **位置**: `internal/server/unified_tunnel_api.go:758-762`
- **问题**: `storedTunnelFromUnifiedRequest` 用创建时的在线快照决定 `RuntimeState`，后续状态变化无法追踪，可能导致 tunnel 长时间停留在 pending。
- **建议**: 移除创建时的状态预判，统一由 reconcile 根据实时状态决定。

### 2.2 Client-Relay preflight revision 计算错误
- **位置**: `internal/server/tunnel_preflight.go:83-91`
- **问题**: 命中 `sameClientIngressResource` 时返回 `current.Revision + 1`，但客户端收到的是相同资源的 revision，应与存储值一致。
- **建议**: 同资源路径使用 `current.Revision`，新资源路径使用 `current.Revision + 1`。

### 2.3 N+1 查询：ingress 资源冲突校验
- **位置**: `internal/server/unified_tunnel_api.go:817-820`
- **问题**: `validateUnifiedIngressResourceAvailable` 调用 `GetAllTunnels()` 全量后逐条比对，隧道数增长后成为瓶颈。
- **建议**: 用 SQL 查询冲突（按 topology/ingress_type/port/domain 精确匹配 + `id != ?` 排除）。

### 2.4 Client reconnect 重复 cleanup
- **位置**: `internal/client/client.go:393-415`
- **问题**: 重连成功后 break，但外层 `c.cleanup()` 与 `connectAndRun` 内部 cleanup 存在语义混淆，旧连接资源与新连接资源边界不清。
- **建议**: 明确 `connectAndRun` 内部负责本 session 退出清理；外层仅在致命错误时调用 cleanup；重连成功前调用 `cleanup` 释放旧资源。

### 2.5 Traffic Accumulator Drain 非原子
- **位置**: `internal/server/traffic_accumulator.go:116-151`
- **问题**: 在 shard 锁内做 `append`（可能触发扩容分配），锁持有时间不可控。
- **建议**: 锁内把 `pending` map 整体 swap 出来，锁外遍历 append。

### 2.6 Traffic Store Flush 错误丢失
- **位置**: `internal/server/traffic_store.go:387-421`
- **问题**: Flush 失败后 `pendingMinute` 已被重置、`pendingErr` 未记录，错误与数据双丢失。
- **建议**: 仅当 flush 成功时清空 pending；失败时保留 pending 并设置 `pendingErr`，下一周期重试。

### 2.7 WSConn.Close 无超时保护
- **位置**: `pkg/mux/wsconn.go:81-92`
- **问题**: `WriteControl` 可能永久阻塞导致 Close hang。
- **建议**: 用带超时的 goroutine 包装 WriteControl，超时后直接关闭底层连接。

### 2.8 UDP buffer 在循环内重复分配
- **位置**: `pkg/mux/udp_frame.go:84`；`internal/client/unified_tunnel.go:436-455`
- **问题**: 高流量场景每次迭代分配 65KB 造成 GC 压力。
- **建议**: 把 buffer 提到循环外；或用 `sync.Pool` 复用。

### 2.9 `Message.ParsePayload` 未处理 nil Payload
- **位置**: `pkg/protocol/message.go:75-77`
- **问题**: `json.Unmarshal(nil, target)` 静默成功，调用者无法区分空对象与 nil。
- **建议**: 显式检查 `m.Payload == nil` 并返回错误。

### 2.10 E2E 测试环境依赖未前置校验
- **位置**: `web/e2e/helpers.ts:37-46`
- **问题**: 强依赖 `NETSGO_ADMIN_USER/PASS` 与已启动的 server + 两个特定 hostname 客户端，环境不满足时测试完全无法运行且错误信息不友好。
- **建议**: 在 `beforeAll` 中 ping `/api/health` 并提供清晰的缺失依赖提示。

### 2.11 并发 tunnel 修改与 server 重启恢复测试缺失
- **位置**: `internal/server/unified_tunnel_api_test.go`、整体 server 测试
- **问题**: 未覆盖并发 update 的 409 路径；未覆盖 server 在 tunnel active 时崩溃后的恢复一致性。
- **建议**: 补充并发 update/delete 测试；补充"创建 active tunnel → 关闭 server → 重启 → 验证状态正确恢复为 offline"的集成测试。

---

## 3. Medium（中等级别，建议尽快修复）

### 3.1 协议层

- **`pkg/protocol/message.go:86`** `AuthRequest.Client` 缺少 `omitempty`，强制必填不符合可选语义。
- **`pkg/protocol/stream_header_helpers.go:95-108`** `TransportPolicy` / `ActualTransport` 未做枚举校验。
- **`pkg/protocol/stream_header.go:158-160`** `ServerAuthorized=true` 可跳过 `OpenToken` 校验，依赖调用方正确设置，存在被滥用风险。
- **`pkg/protocol/types.go:291-292`** `ProxyConfig` 使用 `*EndpointSpec`，而 `TunnelSpec` 使用值类型，不一致。
- **`pkg/mux/mux.go:74,81`** `io.Copy` 错误被 `_` 丢弃，异常传输时无法定位原因。

### 3.2 服务端

- **`internal/server/tunnel_registry.go:135-137`** 向 buffered channel 发送后立即 close，多接收者场景可能 panic；建议用 `sync.Once` 或 context。
- **`internal/server/tunnel_preflight.go:37-52`** Timer 残留：`!timer.Stop()` 时未 drain `timer.C`。
- **`internal/server/console_api.go:119-207`** `storedProxyViewsForClient` 对每个客户端重复查询，应批量加载后按 client 分组。
- **`internal/server/tunnel_manager.go:355-391`** `proxyMu` 持有期间调用 `closeTunnelRuntimeResources`，网络超时场景会阻塞其他访问。
- **`internal/server/tunnel_manager.go:64-72`** provision ack 失败不区分 timeout/cancelled/其他错误，客户端无法差异化重试。
- **`internal/server/unified_tunnel_runtime.go:109-111`** Issue 去重仅比较 Code/Scope/ClientID，不同时间的相似问题会被重复记录。
- **`internal/server/unified_tunnel_runtime.go:88-107`** Runtime registry 依赖完整 `StoredTunnel`，建议改为按 tunnel ID 检索。
- **`internal/server/traffic_store.go:525-580`** 聚合查询使用 `(bucket_start / 3600) * 3600`，无法利用索引，长周期查询全表扫描。
- **`internal/server/migrations/005_unified_tunnel_storage.sql:58-62`** CHECK 约束使用 `= ''`，未来若引入 NULL 语义会不一致；迁移后建议补校验 SQL。

### 3.3 客户端

- **`internal/client/unified_tunnel.go:571-574`** 打开 stream 后注册到 udpAssociations 前若 `runtime.run()` 失败，stream 已打开但清理路径不够清晰。
- **`internal/client/unified_tunnel.go:458-466`** `TunnelRuntimeReport` 未填充 `Spec` 字段，部分服务端路径可能依赖此字段。
- **`internal/client/client.go:903-913`** `handleStream` 无读超时、TCP 连接未注册到 `runtime.tcpConns`、失败日志缺少 TunnelID/ClientID 上下文。
- **`internal/client/client.go:1033-1051`** 单次 heartbeat 失败立即终止连接，过于敏感；建议连续失败 N 次再触发。
- **`internal/client/unified_tunnel.go:174-199`** `removeOldestUDPAssociation` 中 `keyString` 未定义，应 `key.(string)`。

### 3.4 前端

- **`web/src/components/custom/tunnel/TunnelDialog.tsx:339-351`** 未校验 `remote_port` 是否在 `allowed_ports` 范围内。
- **`web/src/components/custom/tunnel/TunnelDialog.tsx:103`** `clientId` 多级回退 (`client_id`/`owner_client_id`/`clientId`) 混用蛇形/驼峰，难维护。
- **`web/src/hooks/use-tunnel-mutations.ts`** `shouldUseLegacyTunnelEndpoint` 回退逻辑在 4 个 mutation 中重复，建议抽 `withFallback` 包装器。
- **`web/src/hooks/use-event-stream.ts:303-313`** SSE 持续失败无用户反馈，应加重试上限 + 手动重连按钮。
- **`web/src/components/custom/tunnel/TunnelListTable.tsx:369-376`** 客户端列可点击但缺少键盘操作支持（`tabIndex`/`onKeyDown`）。
- **`web/src/hooks/use-event-stream.ts:76-199`** SSE 事件 JSON.parse 后直接类型断言，无运行时校验；建议引入 zod 或 type guard。

### 3.5 基础设施

- **`Makefile:144-172`** Playwright target `set -e` 与 `|| status=$?` 混用，逻辑复杂难维护。
- **`Dockerfile:36-49`** 未分离 `go.mod` 缓存层，每次源码变更都会重新下载模块。
- **`Dockerfile:57`** 缺少 `HEALTHCHECK`。
- **`docker-compose.dev.yml:69-90`** `client-key` 的 `entrypoint` 与脚本组合易混淆，需核对启动语义。
- **`.github/workflows/ci.yml:136-162`** 系统级 E2E 已统一到 `make test-system-e2e-nginx/caddy`。
- **`.github/workflows/ci.yml`** Go modules 与 Playwright 浏览器未缓存，CI 耗时与成本增加。

---

## 4. Low（改进建议）

- 注释中英混杂，建议统一为一种语言。
- `pkg/protocol/stream_header_helpers.go` 缺少对应单元测试。
- `pkg/protocol/data_channel.go:18-31` `EncodeDataHandshake` 宽进严出，建议前置长度校验。
- `internal/server/store.go` `TunnelStore` 直接依赖 `*sql.DB`，建议抽取 `TunnelStoreInterface` 便于测试。
- `internal/server/tunnel_preflight.go:34-36` 默认 timeout 硬编码，建议抽常量。
- `internal/client/unified_tunnel.go:654` TCP Relay 完成无日志，建议记录传输字节数与持续时间。
- `web/src/lib/tunnel-model.ts` 单文件 511 行，建议拆为 payload / viewmodel / errors 三模块。
- `web/src/hooks/use-tunnel-mutations.ts:10-15` `invalidateTunnelQueries` 失效范围过宽，按 mutation 类型精细失效。
- `web/package.json:33` `tailwindcss ^4.2.1` 仍是 v4 早期版本，需持续关注。
- `test/e2e/*.pw.ts` 所有 E2E 测试共享 admin session 与 server 状态，`beforeEach` 应清理自创建的 tunnel。
- `.gitignore` 未排除 `bun.lockb`、`go.work.sum`。

---

## 5. 测试质量评估

### 5.1 亮点

- **状态机覆盖全面**：`unified_tunnel_api_test.go` 覆盖 `pending → error`、`capability loss`、`provision timeout` 等关键路径。
- **SQLite schema 验证最佳实践**：`storage_schema_test.go` 验证列类型、默认值、索引列顺序、外键移除、迁移记录。
- **uint64 溢出边界防御**：`traffic_store_test.go` 覆盖 `Flush` / `PendingMerge` / `Query` / `Compact` 四类溢出路径。
- **E2E helpers 使用 Playwright `expect.poll`**：自动重试 + 失败 trace，优于手写轮询。
- **协议消息测试结构化**：`message_test.go` 覆盖零值、Unicode、大 payload、JSON tag 一致性、omitempty。

### 5.2 短板

- **固定间隔轮询引发 flaky**：至少 11 处使用 `time.Sleep(10-50ms)` 等待状态变化（详见 Critical/High 之外的 Medium 列表），CI 环境易超时失败；应改为 channel 通知或指数退避。
- **并发与容错测试缺失**：未覆盖并发 update 的 409 路径；未覆盖 server 崩溃恢复。
- **E2E 端口共享**：`helpers.ts` 固定 `tcpIngressHostPort: 19190` / `udpIngressHostPort: 19191`，并发运行会冲突；`playwright.config.ts` `workers: 1` 是临时规避。
- **E2E 重试策略弱**：`retries: process.env.CI ? 1 : 0` 仅 CI 重试，本地 flaky 无保护。
- **测试辅助函数分散**：`mustAddStableTunnel` / `mustClose` 等在多文件重复定义，建议抽到 `internal/testutil/`。

---

## 6. 架构评估

### 6.1 整体架构（合理）

- **单端口架构**：同一 listener 承载 Web / REST / SSE / 控制通道 / 数据通道，复用 TLS 终止，符合 frp/ngrok 主流设计。
- **控制面 / 数据面分离**：WebSocket JSON + yamux 二进制帧，yamux 提供流控与拥塞控制，对代理环境友好。
- **分层清晰**：API → Reconcile → Runtime → Storage → Data Relay，各层职责明确，便于独立测试。
- **声明式 Reconcile**：周期性 + 事件触发，状态驱动，符合 Kubernetes operator 模式的最佳实践。

### 6.2 统一隧道设计（方向正确，需补齐一致性）

- **两种拓扑抽象合理**：`server_expose` 单方参与、`client_to_client` 三方参与，实现路径分离清晰。
- **状态机完整**：`DesiredState` / `RuntimeState` / `Participant` / `P2P` 多套状态正交，`aggregateTunnelRuntimeState` 正确聚合。
- **修订号乐观并发**：`Revision` 贯穿 API/存储/协议，是分布式状态一致的基石。
- **待补齐**：并发 Reconcile、Update/Delete 部分失败、协议版本协商。

### 6.3 协议设计（扎实）

- **二进制帧头**：`magic(4B) + version(1B) + length(4B) + payload`，便于演进与调试。
- **`DisallowUnknownFields`** 防止未知字段注入，`UTF-8` 校验、字符串长度上限。
- **`WSConn` 并发安全**：`writeMu` 保护写、`closeOnce` 保证 Close 幂等。
- **待补齐**：`ServerAuthorized` 语义收紧、角色枚举校验、nil Payload 明确错误。

### 6.4 数据模型（合理）

- **`TunnelSpec` 统一** API / 协议 / 存储 / 运行时的事件流，避免多源数据漂移。
- **流量分层**：秒级内存 accumulator → 分钟桶 pending → 小时桶压缩，保留策略 30 天分钟 / 365 天小时。
- **待优化**：聚合查询索引、accumulator 宕机丢失风险的文档说明。

### 6.5 扩展性

- **多节点/多实例**：当前 SQLite + 本地 sync.Map 不支持；如需扩展，建议先单机多进程，再引入 Redis/etcd。
- **多租户**：`OwnerClientID` 已存在但无隔离模型；短期可按 owner 维度做 API 过滤，长期需引入租户概念。
- **协议扩展**：`EndpointType` / `TransportPolicy` 已预留扩展点，P2P 能力（ICE/TURN）未启用。

### 6.6 安全性

- **认证**：Key → Token 兑换 + Token 复用 + Token 失效降级，有 rate limiting。
- **待补齐**：无 RBAC、无 Token 撤销列表、无 mutual TLS、无每隧道连接数限制（恶意客户端可耗尽资源）、Domain 未做保留列表（DNS rebinding 风险）。

---

## 7. 优先级行动建议

### P0（合并前必须修复）
1. 并发 Reconcile 互斥 / 单 worker 化（§1.1）
2. Update/Delete 部分失败窗口的事务化（§1.2）
3. Tunnel 创建竞态的 `BEGIN IMMEDIATE` / `ON CONFLICT` 修复（§1.3）
4. Data Session 切换竞态（§1.4）
5. 硬编码凭据与 root 容器（§1.10, §1.11）
6. 系统级 E2E Compose harness 持续扩展（§1.12）
7. UDP 读取错误分类与 packetConn 竞态（§1.7, §1.8）

### P1（发布前修复）
1. Goroutine 生命周期 + WaitGroup（§1.5）
2. 协议角色枚举 + ServerAuthorized 语义（§1.6, §3.1）
3. Vite 锁定到稳定版（§1.13）
4. Server-Expose 状态预判移除（§2.1）
5. Preflight revision 计算修复（§2.2）
6. N+1 查询改为 SQL 冲突检测（§2.3）
7. Client reconnect cleanup 语义澄清（§2.4）
8. Traffic accumulator / store 错误处理（§2.5, §2.6）
9. WSConn.Close 超时（§2.7）
10. 测试补齐：并发 update、server 崩溃恢复、E2E 环境校验（§2.11）

### P2（后续迭代）
1. 协议层可选字段 omitempty / 指针类型一致性（§3.1）
2. 前端端口范围校验 + 键盘可访问性 + SSE 重试上限（§3.4）
3. CI 缓存 + Dockerfile 层优化 + HEALTHCHECK（§3.5）
4. 测试 flaky 治理：轮询改指数退避 / channel 通知（§5.2）
5. 隧道连接数限制、Token 撤销、RBAC（§6.6）
6. 多实例架构设计（§6.5）

---

## 8. 结语

NetsGo 的整体架构与代码质量处于**良好**水平，PR #48 的 unified tunnel 设计方向正确、测试覆盖可观。当前的主要短板集中在**并发场景下的一致性保障**与**网络编程的边界处理**，这两类问题若不修复将在生产环境暴露为间歇性故障。基础设施与前端层面的问题相对易修，可在短期内完成。

建议按 P0 → P1 → P2 顺序推进，P0 项应在合并前解决，P1 项在首个正式发布前完成，P2 项纳入后续迭代计划。完成 P0 + P1 后，系统即可具备生产可用的正确性基线。
