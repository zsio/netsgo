# HTTP 域名隧道实施路径总览

> 基于：`docs/2026-03-21-http-domain-tunnel-plan.md`
> 创建时间：2026-03-22

## 实施批次一览

| 批次 | 文件 | 核心内容 | 前置条件 | 影响范围 |
|------|------|----------|----------|----------|
| Batch 1 | [batch-1-bug-fixes.md](./batch-1-bug-fixes.md) | 修复前置 Bug（HTTP early-return、Domain 字段丢失） | 无 | `proxy.go`、`tunnel_manager.go` |
| Batch 2 | [batch-2-backend-unit-tests.md](./batch-2-backend-unit-tests.md) | 后端纯规则单元测试（TDD 先行，预期失败） | Batch 1 | 新建测试文件 |
| Batch 3 | [batch-3-backend-rules-impl.md](./batch-3-backend-rules-impl.md) | 后端核心规则实现（使 Batch 2 测试通过） | Batch 2 | `http_tunnel.go`（新建）、`admin_api.go`、`admin_models.go` |
| Batch 4 | [batch-4-dispatch-integration-tests.md](./batch-4-dispatch-integration-tests.md) | 入口分发集成测试（TDD 先行，预期失败） | Batch 3 | 新建测试文件 |
| Batch 5 | [batch-5-backend-http-runtime.md](./batch-5-backend-http-runtime.md) | 后端 HTTP 域名隧道最小闭环实现 | Batch 4 | `server.go`、`http_tunnel.go`（扩展） |
| Batch 6 | [batch-6-client-subprotocol.md](./batch-6-client-subprotocol.md) | Client 侧 WS 子协议发送与业务回归 | Batch 5 | `client.go` |
| Batch 7 | [batch-7-e2e-nginx-caddy.md](./batch-7-e2e-nginx-caddy.md) | nginx / caddy E2E 验证 | Batch 6 | `test/e2e/` |
| Batch 8 | [batch-8-frontend.md](./batch-8-frontend.md) | 前端表单、状态展示、冲突提示重构 | Batch 5 | `web/src/` |

## 依赖关系图

```
Batch 1（Bug 修复）
  └─ Batch 2（单元测试，先写测试）
       └─ Batch 3（实现规则，使测试通过）
            └─ Batch 4（集成测试，先写测试）
                 └─ Batch 5（实现运行时闭环）
                      ├─ Batch 6（Client 子协议）
                      │    └─ Batch 7（E2E）
                      └─ Batch 8（前端，与 Batch 6/7 并行可行）
```

## 核心设计决策（执行前必读）

### 请求分发优先级（严格顺序）

1. 内部 WS 通道：`path=/ws/control` + `Sec-WebSocket-Protocol: netsgo-control.v1`
2. 内部 WS 通道：`path=/ws/data` + `Sec-WebSocket-Protocol: netsgo-data.v1`
3. Host 命中 HTTP 隧道域名 → 转发（任意 path）
4. 系统未初始化 → setup 例外
5. Host == 生效管理 Host → 管理面
6. 其他 → 404

### 关键约束

- HTTP 隧道**只按域名分流**，不按路径
- 内部 WS 通道识别**必须 path + 子协议双重满足**，缺一不可
- `securityHeadersHandler` **只包管理面**，不包 HTTP 隧道
- `StartHTTPOnly()` 返回类型从 `*http.ServeMux` → `http.Handler`（破坏性变更，须同步更新测试）
- HTTP 隧道复用 TCP 数据面，Client 侧无需额外改动
- 本期**不兼容旧 Client**（旧 Client 未发子协议，连接会失败）

### 已知需要特别注意的地方

- `restoreTunnels` 有两处内联 `ProxyConfig` 构造，都要补 `Domain`（Batch 1）
- nginx 默认不透传 `Sec-WebSocket-Protocol`，E2E 配置须显式加（Batch 7）
- `httputil.ReverseProxy` 默认缓冲响应，SSE 必须设 `FlushInterval: -1`（Batch 5）
- WebSocket Upgrade 需要绕开 `ReverseProxy`，改用 TCP relay（Batch 5）

## 验收方式速查

```bash
# Batch 1 完成后
go test ./internal/server/... ./internal/client/... ./pkg/...

# Batch 3 完成后（Batch 2 测试全部通过）
go test ./internal/server/... -run 'TestCanonical|TestValidate|TestEffective|TestDomain|TestIsNetsgo|TestTrustedProxy|TestOffline|TestAdminConfig' -v

# Batch 5 完成后（Batch 4 集成测试全部通过）
go test ./internal/server/... -run TestDispatch -v

# Batch 6 完成后（全量回归）
go test ./... -v

# Batch 8 完成后
cd web && bun run build && bun run lint
```
