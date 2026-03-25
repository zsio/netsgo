# Tunnel State Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把隧道状态模型彻底统一为 `desired_state + runtime_state + error`，删除内部实现、JSON store、API 和前端中的旧 `status` 依赖。

**Architecture:** 服务端内部、持久化层、SSE、`GET /api/clients`、前端展示都直接读写双状态，不再通过单一 `status` 做中间真值。运行时资源操作和状态写入分离，关闭 listener/UDP runtime 不再顺手改“业务状态”，状态转移只在管理动作和故障处理处显式完成。

**Tech Stack:** Go、JSON 文件存储、React、TypeScript、TanStack Query、SSE、WebSocket/yamux。

---

## 实施进度更新（2026-03-25）

### 已完成

- [x] 协议层主链路已删除 tunnel `status` 字段，主读写模型改为 `desired_state + runtime_state + error`
- [x] `internal/server/tunnel_state.go` 已切换为双状态设置/校验 helper
- [x] `internal/server/store.go` 已删除 `StoredTunnel.Status`、`UpdateStatus`、`UpdateState`
- [x] `internal/server/tunnel_manager.go` / `proxy.go` / `udp_proxy.go` / `server.go` / `http_tunnel_proxy.go` 已改为直接读写双状态
- [x] `/api/clients`、`tunnel_changed`、受端口白名单影响的 `affected_tunnels` 已改成双状态输出
- [x] 前端 `web/src/types/index.ts`、`web/src/lib/tunnel-model.ts`、`web/src/routes/admin/config.tsx` 已删除 tunnel `status` 依赖
- [x] 已同步调整 Go/TS 单测，覆盖 store、事件、管理动作和前端展示逻辑

### 已验证

- [x] `go test ./internal/server ./pkg/protocol -count=1 -timeout 60s`
- [x] `go test ./internal/server -run 'TestTunnelStore_.*|TestEmitTunnelChanged_.*' -count=1 -timeout 60s`
- [x] `cd web && bun test src/lib/tunnel-model.test.ts`
- [x] `cd web && bun run build`

### 仍待完成

- [ ] `pkg/mux` / `cmd/netsgo` 全量 Go 回归尚未在本轮一起跑
- [ ] 干净开发环境下的手工冒烟（HTTP/TCP/UDP create/pause/resume/stop/delete、offline、runtime error）尚未完成
- [ ] 生产审查里与状态模型无关的后续项仍待单独推进：management host fallback、backend 健康校验、TCP/UDP 连接治理

---

## 开始前先确认

- 这是**开发阶段破坏性改动**，不做任何兼容迁移。
- `protocol.ProxyConfig`、`StoredTunnel`、前端 `ProxyConfig` 类型里的隧道 `status` 字段都要删掉。
- 旧本地开发数据不迁移。开始实现前先删除本机 `~/.netsgo/tunnels.json`，避免旧数据干扰。
- 本计划只处理“隧道状态模型统一”这一件事，不顺手掺入 HTTP 索引优化、TCP 超时、UDP 慢会话隔离等下一阶段任务。

## 目标状态定义

- `desired_state=running`
  - `runtime_state=pending`：正在建立
  - `runtime_state=exposed`：公网入口已建立
  - `runtime_state=offline`：配置已保存，等待 client 上线
  - `runtime_state=error`：建立失败或运行时故障
- `desired_state=paused`
  - `runtime_state=idle`
- `desired_state=stopped`
  - `runtime_state=idle`
- `error`
  - 只有 `runtime_state=error` 时允许非空

## 文件分工

- 修改 [pkg/protocol/types.go](/Users/dyy/projects/code/netsgo/pkg/protocol/types.go)
  - 删除隧道 `status` 字段与 `ProxyStatus*` 常量。
  - 删除 `NormalizeProxyStates` / `LegacyProxyStatusFromStates`。
  - 保留并收紧 `desired_state`、`runtime_state`、`error` 的定义。
- 修改 [internal/server/tunnel_state.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_state.go)
  - 删除 legacy 归一化逻辑。
  - 改成明确的双状态设置函数和校验函数。
- 修改 [internal/server/store.go](/Users/dyy/projects/code/netsgo/internal/server/store.go)
  - 删除 `StoredTunnel.Status`、`UpdateState`、`UpdateStatus`。
  - 只保留 `UpdateStates`。
- 修改 [internal/server/tunnel_manager.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_manager.go)
  - 所有管理动作直接写双状态。
  - 删除 `persistTunnelState(...status...)` 这一套旧入口。
- 修改 [internal/server/proxy.go](/Users/dyy/projects/code/netsgo/internal/server/proxy.go)
  - 运行时开关资源不再隐式写 legacy 状态。
  - 只负责 listener / UDP runtime 生命周期。
- 修改 [internal/server/server.go](/Users/dyy/projects/code/netsgo/internal/server/server.go)
  - `GET /api/clients`、SSE、恢复逻辑只返回双状态。
  - 删除响应中的隧道 `status`。
- 修改 [internal/server/http_tunnel_proxy.go](/Users/dyy/projects/code/netsgo/internal/server/http_tunnel_proxy.go)
  - Host 分发只按双状态判断“可用/不可用”。
- 修改 [internal/server/udp_proxy.go](/Users/dyy/projects/code/netsgo/internal/server/udp_proxy.go)
  - UDP 运行时故障、关闭、恢复都直接写双状态。
- 修改 [web/src/types/index.ts](/Users/dyy/projects/code/netsgo/web/src/types/index.ts)
  - 删除前端隧道 `status` 类型。
- 修改 [web/src/lib/tunnel-model.ts](/Users/dyy/projects/code/netsgo/web/src/lib/tunnel-model.ts)
  - 前端展示和动作权限只按双状态判断。
- 修改测试
  - [internal/server/store_test.go](/Users/dyy/projects/code/netsgo/internal/server/store_test.go)
  - [internal/server/server_test.go](/Users/dyy/projects/code/netsgo/internal/server/server_test.go)
  - [internal/server/offline_http_tunnel_test.go](/Users/dyy/projects/code/netsgo/internal/server/offline_http_tunnel_test.go)
  - [internal/server/offline_managed_tunnel_phase2_test.go](/Users/dyy/projects/code/netsgo/internal/server/offline_managed_tunnel_phase2_test.go)
  - [internal/server/udp_proxy_test.go](/Users/dyy/projects/code/netsgo/internal/server/udp_proxy_test.go)
  - [web/src/lib/tunnel-model.test.ts](/Users/dyy/projects/code/netsgo/web/src/lib/tunnel-model.test.ts)

### Task 1: 锁定目标模型并删掉协议层 legacy 入口

**Files:**
- Modify: [pkg/protocol/types.go](/Users/dyy/projects/code/netsgo/pkg/protocol/types.go)
- Modify: [internal/server/tunnel_state.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_state.go)
- Test: [internal/server/store_test.go](/Users/dyy/projects/code/netsgo/internal/server/store_test.go)
- Test: [internal/server/server_test.go](/Users/dyy/projects/code/netsgo/internal/server/server_test.go)

- [ ] **Step 1: 先写失败测试，锁定“不再返回隧道 status”**

在 [internal/server/server_test.go](/Users/dyy/projects/code/netsgo/internal/server/server_test.go) 增加一个响应形状测试，断言 `GET /api/clients` 返回的单条 tunnel 至少包含：

```json
{
  "desired_state": "running",
  "runtime_state": "offline",
  "error": ""
}
```

并明确断言 **不再有** `status` 字段。

- [ ] **Step 2: 运行测试，确认它先失败**

Run: `go test ./internal/server -run 'TestServer_APIClients_.*WithoutTunnelStatus' -count=1 -timeout 60s`

Expected: FAIL，原因是当前响应仍带 `status`。

- [ ] **Step 3: 删除协议层 legacy 字段和函数**

在 [pkg/protocol/types.go](/Users/dyy/projects/code/netsgo/pkg/protocol/types.go) 做这些改动：

```go
type ProxyConfig struct {
    Name         string `json:"name"`
    Type         string `json:"type"`
    LocalIP      string `json:"local_ip"`
    LocalPort    int    `json:"local_port"`
    RemotePort   int    `json:"remote_port"`
    Domain       string `json:"domain"`
    ClientID     string `json:"client_id"`
    DesiredState string `json:"desired_state"`
    RuntimeState string `json:"runtime_state"`
    Error        string `json:"error,omitempty"`
}
```

同时删除：

- `ProxyStatusPending`
- `ProxyStatusActive`
- `ProxyStatusPaused`
- `ProxyStatusStopped`
- `ProxyStatusError`
- `NormalizeProxyStates`
- `LegacyProxyStatusFromStates`

- [ ] **Step 4: 把 server 侧状态 helper 改成双状态直写**

在 [internal/server/tunnel_state.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_state.go) 删除这几个函数：

- `normalizeProxyConfigState`
- `setProxyConfigLegacyStatus`
- `normalizeStoredTunnelState`
- `setStoredTunnelLegacyStatus`

改成只保留：

```go
func setProxyConfigStates(config *protocol.ProxyConfig, desiredState, runtimeState, errMsg string)
func setStoredTunnelStates(tunnel *StoredTunnel, desiredState, runtimeState, errMsg string)
func validateTunnelStates(desiredState, runtimeState, errMsg string) error
```

- [ ] **Step 5: 运行第一轮测试**

Run: `go test ./internal/server -run 'TestServer_APIClients_.*WithoutTunnelStatus|TestTunnelStore_.*' -count=1 -timeout 60s`

Expected: PASS

- [ ] **Step 6: 提交这一小步**

```bash
git add pkg/protocol/types.go internal/server/tunnel_state.go internal/server/store_test.go internal/server/server_test.go
git commit -m "refactor: drop legacy tunnel status model"
```

### Task 2: 清理 store，状态持久化只写双状态

**Files:**
- Modify: [internal/server/store.go](/Users/dyy/projects/code/netsgo/internal/server/store.go)
- Test: [internal/server/store_test.go](/Users/dyy/projects/code/netsgo/internal/server/store_test.go)

- [ ] **Step 1: 先写失败测试，锁定 store 不再依赖 status**

新增两个测试：

- `TestTunnelStore_RoundTripStatesWithoutLegacyStatus`
- `TestTunnelStore_UpdateStates_ClearsErrorOutsideRuntimeError`

重点断言：

- `StoredTunnel` 序列化后没有 `status`
- `UpdateStates` 是唯一合法状态更新入口
- `runtime_state != error` 时 `error` 必须被清空

- [ ] **Step 2: 运行测试，确认先失败**

Run: `go test ./internal/server -run 'TestTunnelStore_(RoundTripStatesWithoutLegacyStatus|UpdateStates_ClearsErrorOutsideRuntimeError)' -count=1 -timeout 60s`

Expected: FAIL

- [ ] **Step 3: 删除 store 里的 legacy 字段和方法**

在 [internal/server/store.go](/Users/dyy/projects/code/netsgo/internal/server/store.go) 做这些改动：

- 删除 `StoredTunnel.Status`
- 删除 `UpdateStatus`
- 删除 `UpdateState`
- 删除任何“从 legacy status 归一化”的逻辑
- `load()` 只接受新字段；遇到旧文件不做兼容处理

- [ ] **Step 4: 把旧文件处理方式写死**

如果 `load()` 读到旧格式导致失败，不做自动迁移。保留清晰错误，让开发者删本地 store 重来。不要在代码里夹带“旧 status 回填双状态”的兜底逻辑。

- [ ] **Step 5: 跑 store 测试**

Run: `go test ./internal/server -run 'TestTunnelStore_.*' -count=1 -timeout 60s`

Expected: PASS

- [ ] **Step 6: 提交这一小步**

```bash
git add internal/server/store.go internal/server/store_test.go
git commit -m "refactor: store tunnel states without legacy status"
```

### Task 3: 改服务端写路径，所有状态迁移都显式写双状态

**Files:**
- Modify: [internal/server/tunnel_manager.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_manager.go)
- Modify: [internal/server/proxy.go](/Users/dyy/projects/code/netsgo/internal/server/proxy.go)
- Modify: [internal/server/server.go](/Users/dyy/projects/code/netsgo/internal/server/server.go)
- Modify: [internal/server/http_tunnel_proxy.go](/Users/dyy/projects/code/netsgo/internal/server/http_tunnel_proxy.go)
- Modify: [internal/server/udp_proxy.go](/Users/dyy/projects/code/netsgo/internal/server/udp_proxy.go)
- Test: [internal/server/server_test.go](/Users/dyy/projects/code/netsgo/internal/server/server_test.go)
- Test: [internal/server/offline_http_tunnel_test.go](/Users/dyy/projects/code/netsgo/internal/server/offline_http_tunnel_test.go)
- Test: [internal/server/offline_managed_tunnel_phase2_test.go](/Users/dyy/projects/code/netsgo/internal/server/offline_managed_tunnel_phase2_test.go)
- Test: [internal/server/udp_proxy_test.go](/Users/dyy/projects/code/netsgo/internal/server/udp_proxy_test.go)

- [ ] **Step 1: 先写失败测试，锁定关键状态转移**

至少覆盖这些用例：

- 在线创建成功后：`running + exposed`
- 在线创建等待 ready 时：`running + pending`
- 离线创建后：`running + offline`
- pause 后：`paused + idle`
- stop 后：`stopped + idle`
- runtime 故障后：`running + error`

推荐测试名：

- `TestManagedTunnel_Create_SetsRunningPendingThenExposed`
- `TestOfflineManagedTunnel_Create_SetsRunningOffline`
- `TestManagedTunnel_Pause_SetsPausedIdle`
- `TestManagedTunnel_Stop_SetsStoppedIdle`
- `TestUDPReadLoop_UnexpectedReadError_SetsRunningError`

- [ ] **Step 2: 运行测试，确认先失败**

Run: `go test ./internal/server -run 'Test(ManagedTunnel_.*|OfflineManagedTunnel_.*|UDPReadLoop_UnexpectedReadError_.*)' -count=1 -timeout 60s`

Expected: FAIL

- [ ] **Step 3: 删除 status 驱动的写入口**

在 [internal/server/tunnel_manager.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_manager.go) 删除或改写：

- `setTunnelState(...status...)`
- `persistTunnelState(...status...)`
- 所有 `protocol.ProxyStatus*` 分支

改成：

```go
func (s *Server) setTunnelStates(client *ClientConn, name, desiredState, runtimeState, errMsg string) (protocol.ProxyConfig, bool)
func (s *Server) persistTunnelStates(clientID, name, desiredState, runtimeState, errMsg string) error
```

- [ ] **Step 4: 把“资源关闭”和“状态切换”分开**

在 [internal/server/proxy.go](/Users/dyy/projects/code/netsgo/internal/server/proxy.go) 不要再让 `PauseProxy`、`StopProxy` 这种资源动作顺带改业务状态。改成两类函数：

- `closeTunnelRuntime(...)`：只关 listener / UDP runtime / done channel
- 管理动作函数：只在 manager 层改 `desired_state + runtime_state`

- [ ] **Step 5: 把聚合出口改成只返回双状态**

在 [internal/server/server.go](/Users/dyy/projects/code/netsgo/internal/server/server.go) 的这些出口里去掉 tunnel `status`：

- `collectClientViews`
- `emitTunnelChanged`
- 任何恢复/异常事件构造

要求：

- 在线 tunnel 直接返回内存态双状态
- 离线 tunnel 直接返回 store 里的双状态
- 不再存在“为了前端兼容再拼一个 status”

- [ ] **Step 6: 跑 server 相关测试**

Run: `go test ./internal/server -count=1 -timeout 60s`

Expected: PASS

- [ ] **Step 7: 提交这一小步**

```bash
git add internal/server/tunnel_manager.go internal/server/proxy.go internal/server/server.go internal/server/http_tunnel_proxy.go internal/server/udp_proxy.go internal/server/*.go
git commit -m "refactor: drive tunnel lifecycle with desired and runtime states"
```

### Task 4: 改前端与 API 类型，删除对 tunnel status 的依赖

**Files:**
- Modify: [web/src/types/index.ts](/Users/dyy/projects/code/netsgo/web/src/types/index.ts)
- Modify: [web/src/lib/tunnel-model.ts](/Users/dyy/projects/code/netsgo/web/src/lib/tunnel-model.ts)
- Modify: [web/src/routes/admin/config.tsx](/Users/dyy/projects/code/netsgo/web/src/routes/admin/config.tsx)
- Modify: [web/src/components/custom/tunnel/TunnelListTable.tsx](/Users/dyy/projects/code/netsgo/web/src/components/custom/tunnel/TunnelListTable.tsx)
- Test: [web/src/lib/tunnel-model.test.ts](/Users/dyy/projects/code/netsgo/web/src/lib/tunnel-model.test.ts)
- Test: [internal/server/server_test.go](/Users/dyy/projects/code/netsgo/internal/server/server_test.go)

- [ ] **Step 1: 先写失败测试**

在 [web/src/lib/tunnel-model.test.ts](/Users/dyy/projects/code/netsgo/web/src/lib/tunnel-model.test.ts) 增加这些用例：

- 前端状态展示只依赖 `desired_state + runtime_state`
- 缺少 `status` 时仍正常工作
- 动作权限只按双状态判断

推荐测试名：

- `supports dual-state tunnels without legacy status`
- `uses desired and runtime states for action availability`

- [ ] **Step 2: 运行前端测试，确认先失败**

Run: `cd web && bun test src/lib/tunnel-model.test.ts`

Expected: FAIL

- [ ] **Step 3: 删除前端 tunnel status 类型和 fallback**

在 [web/src/types/index.ts](/Users/dyy/projects/code/netsgo/web/src/types/index.ts) 删除：

```ts
status: ProxyStatus;
```

在 [web/src/lib/tunnel-model.ts](/Users/dyy/projects/code/netsgo/web/src/lib/tunnel-model.ts) 删除：

- `rawStatus`
- `resolveTunnelStatus()` 里的 `status` fallback
- 所有 `status === 'active'` 之类的动作门禁

改成只看：

- `desired_state`
- `runtime_state`
- `error`

- [ ] **Step 4: 清理页面层直接读 tunnel.status 的地方**

先搜一遍：

Run: `rg -n "tunnel\\.status|status === 'active'|status === 'paused'|status === 'stopped'|status === 'error'" web/src`

把命中的隧道状态判断全部改成双状态判断。

- [ ] **Step 5: 跑前端验证**

Run:

```bash
cd web && bun test src/lib/tunnel-model.test.ts
cd web && bun run build
```

Expected: PASS

- [ ] **Step 6: 提交这一小步**

```bash
git add web/src/types/index.ts web/src/lib/tunnel-model.ts web/src/lib/tunnel-model.test.ts web/src/routes/admin/config.tsx web/src/components/custom/tunnel/TunnelListTable.tsx
git commit -m "refactor: remove legacy tunnel status from web client"
```

### Task 5: 全量清理、验收和收尾

**Files:**
- Modify: 本轮所有改动文件
- Optional Docs: [docs/2026-03-24-tunnel-production-review.md](/Users/dyy/projects/code/netsgo/docs/2026-03-24-tunnel-production-review.md)

- [ ] **Step 1: 全仓搜索，确认没有漏掉 tunnel status**

Run:

```bash
rg -n "ProxyStatus|status\\s*[:=]|\\.Status\\b|json:\"status\"" internal/server pkg/protocol web/src
```

只允许剩下这些无关结果：

- 服务端整体运行状态 `ServerStatus.Status`
- 其他非隧道领域的 status 字段

如果还剩“隧道 status”，继续清理，不要留尾巴。

- [ ] **Step 2: 跑 Go 全量回归**

Run: `go test ./internal/server ./pkg/mux ./pkg/protocol ./cmd/netsgo -count=1 -timeout 60s`

Expected: PASS

- [ ] **Step 3: 跑前端回归**

Run:

```bash
cd web && bun test src/lib/tunnel-model.test.ts
cd web && bun run build
```

Expected: PASS

- [ ] **Step 4: 用干净开发环境手工冒烟一次**

手工前提：

- 删除 `~/.netsgo/tunnels.json`
- 启动 server/client
- 新建一条 HTTP、一条 TCP、一条 UDP
- 分别验证 create / pause / resume / stop / delete
- 验证 client 离线后列表展示为 `offline`
- 验证 runtime 故障后展示为 `error`

- [ ] **Step 5: 如有需要，更新评审文档状态**

如果本次完成后，评审文档里“第二阶段状态模型未收敛”的问题已关闭，可在 [docs/2026-03-24-tunnel-production-review.md](/Users/dyy/projects/code/netsgo/docs/2026-03-24-tunnel-production-review.md) 补一个已完成说明，但不要额外改动无关章节。

- [ ] **Step 6: 最后提交**

```bash
git add pkg/protocol/types.go internal/server web/src docs/2026-03-24-tunnel-production-review.md
git commit -m "refactor: simplify tunnel state model"
```

## 验收标准

- 隧道模型中不再存在 legacy `status`
- 服务端内部状态更新不再通过单一 `status` 中转
- store 里只保存 `desired_state + runtime_state + error`
- `/api/clients` 和 `tunnel_changed` 事件不再返回 tunnel `status`
- 前端展示、动作权限、错误提示都只按双状态工作
- 全量 Go 回归与前端构建通过

## 不要做的事

- 不要保留“过渡期双写”
- 不要新增“为了兼容旧测试”的中间 helper
- 不要在前端偷偷保留 `status` fallback
- 不要顺手把 TCP/UDP/HTTP 其他稳定性改动混进这一轮
- 不要写自动迁移旧 store 的代码
