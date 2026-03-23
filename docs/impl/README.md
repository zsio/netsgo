# HTTP 域名隧道实施路径总览（收敛版）

> 基于：`docs/2026-03-21-http-domain-tunnel-plan.md`  
> 创建时间：2026-03-22  
> 本版说明：保留现有 `batch-*` 文件名以减少文档 churn，但**推荐执行顺序**改为按 6 个阶段推进。

## 推荐执行顺序

| 阶段 | 目标 | 对应文档 | 前置条件 | 本阶段完成标志 |
|------|------|----------|----------|----------------|
| 阶段 1 | 修复已确认前置 Bug，消除假失败 | [batch-1-bug-fixes.md](./batch-1-bug-fixes.md) | 无 | HTTP 不再误走 TCP listen，`domain` 不再在 prepare/restore 路径丢失 |
| 阶段 2 | 先锁死规则，再补规则实现 | [batch-2-backend-unit-tests.md](./batch-2-backend-unit-tests.md) + [batch-3-backend-rules-impl.md](./batch-3-backend-rules-impl.md) | 阶段 1 | 域名规则、管理 Host、内部 WS 识别、AdminConfig 冲突/锁定语义全部有测试且通过 |
| 阶段 3 | 补齐生命周期与离线语义 | [phase-3-lifecycle-persistence.md](./phase-3-lifecycle-persistence.md) | 阶段 2 | 离线 HTTP 隧道 `edit / pause / delete`、store-first、一致性与状态语义稳定 |
| 阶段 4 | 完成最终入口分发与 HTTP 运行时闭环 | [batch-4-dispatch-integration-tests.md](./batch-4-dispatch-integration-tests.md) + [batch-5-backend-http-runtime.md](./batch-5-backend-http-runtime.md) | 阶段 3 | 最终 handler、HTTP 代理、业务 WS、SSE、`404/503/502` 全部跑通 |
| 阶段 5 | 对齐 Client 握手并做部署链路验证 | [batch-6-client-subprotocol.md](./batch-6-client-subprotocol.md) + [batch-7-e2e-nginx-caddy.md](./batch-7-e2e-nginx-caddy.md) | 阶段 4 | Client 主动发子协议，直连/nginx/caddy 全链路通过 |
| 阶段 6 | 收口前端表单、展示和管理配置交互 | [batch-8-frontend.md](./batch-8-frontend.md) | 阶段 4 | 表单、列表、详情、概览、AdminConfig、Add Client 文案语义一致 |

## 为什么这样收敛

当前主规划没有问题，问题出在实施拆解少了一个中间层：

1. 阶段 2 解决的是“规则是否正确”
2. 阶段 4 解决的是“请求是否被正确分发和代理”
3. 两者之间还缺一层“生命周期与离线语义”

如果不先把阶段 3 补出来，执行时很容易出现这些混乱：

- 规则层已经支持 HTTP 域名冲突，但离线更新仍走在线 runtime 查找，直接返回 `404`
- 入口分发层开始依赖 `pending / paused / stopped / error / offline` 的声明一致性，但 store/runtime/placeholder 的语义还没稳定
- 测试会出现“明明是生命周期问题，却在分发测试里失败”的噪音

## 推荐依赖图

```text
阶段 1：前置 Bug 修复
  ↓
阶段 2：规则层 TDD + 实现
  ↓
阶段 3：生命周期与离线语义
  ↓
阶段 4：入口分发与 HTTP 运行时
  ↓
阶段 5：Client + E2E
  ↓
阶段 6：Frontend 收尾
```

## 与旧版拆解相比的关键调整

### 1. 新增阶段 3，不再把生命周期问题塞进规则层或运行时层

阶段 3 专门负责：

- 离线 HTTP 隧道 `edit / pause / delete`
- 离线 `resume / stop` 的显式拒绝
- store-first 配置真值
- 旧域名释放 / 新域名声明
- 断线不写回 `paused`
- `domain` 在 runtime / store / placeholder 之间的一致性

### 2. 阶段 2 不允许用 stub 或 `t.Skip` 伪造 TDD

阶段 2 的红测试允许是：

- 断言失败
- 编译失败（`undefined:`）

但不允许：

- 在测试里写临时空实现
- 用 `t.Skip("stub")` 占位

### 3. 服务端 WS 子协议协商前移到阶段 4

`/ws/control` 和 `/ws/data` 的服务端识别、协商与回写，本质上属于**最终入口运行时的一部分**，不应留到 Client 批次。  
阶段 5 只负责让 Client 按约定发送子协议，并做回归与 E2E。

### 4. 前端阶段扩大到“统一语义收口”，不只改表单

阶段 6 除了 TunnelDialog，还要覆盖：

- 类型定义
- mutation hook
- 列表 / 详情 / 概览统一 view model
- AdminConfig
- AddClientDialog 文案

目标不是多做，而是避免多个页面各自解释 tunnel 状态。

## 每阶段最小验证

```bash
# 阶段 1
go test ./internal/server/... ./internal/client/... ./pkg/...

# 阶段 2
go test ./internal/server/... -run 'TestCanonical|TestValidate|TestEffective|TestDomain|TestIsNetsgo|TestTrustedProxy|TestAdminConfig' -v

# 阶段 3
go test ./internal/server/... -run 'TestOffline|TestLifecycle|TestPlaceholder|TestStoreFirst' -v

# 阶段 4
go test ./internal/server/... -run TestDispatch -v

# 阶段 5
go test ./internal/client/... -v
go test ./... -v

# 阶段 6
cd web && bun run build && bun run lint
```

## 执行纪律

为避免执行期再次发散，建议严格遵守下面三条：

1. 不跳阶段。阶段 3 没稳定之前，不进入阶段 4。
2. 不在阶段 2/3 提前写 HTTP 代理实现。那是阶段 4 的工作。
3. 前端直到阶段 4 后端接口和状态语义稳定后再启动。
4. 阶段间串行执行，不并行推进；测试按生产规则同步调整，不为迁就旧测试保留错误入口。
