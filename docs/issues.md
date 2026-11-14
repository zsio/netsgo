# NetsGo Design Issues

本文档只索引当前仍需要单独治理的实现缺口、设计决策和部署验证。已实现功能移出待办索引；仍有维护价值的设计契约可以保留为实现参考，过时或已被替代的说明应清理。

最近核实：2026-11-12。结论依据当前代码、迁移、测试、`Makefile` 和 CI 配置；测试文件存在与本次实际执行的验证范围分别记录。

## Issue 索引

| Issue | Severity | 状态 | 说明 |
|---|---|---|---|
| [`p2p-data-transport-policy`](./issue/p2p-data-transport-policy.md) | High | UDP 数据路径已实现；部署验证待补 | 三种策略、共享 pair session、租约、流量统计、共享限速和能力门禁均已实现。剩余包括真实 CGNAT、移动网络、企业防火墙及高延迟网络性能验证；passive ICE-TCP 属于尚未实现的后续原型，不是已支持能力 |
| [`endpoint-type-extensibility`](./issue/endpoint-type-extensibility.md) | Medium | 待决定是否放松 CHECK | `017_tls_sni_ingress.sql` 已加入 `tls_sni`；`tunnels` 仍使用 endpoint 类型枚举 CHECK，新增类型仍需迁移。这是扩展方式的设计决策，当前已支持类型可正常使用 |
| [`runtime-state-active-exposed`](./issue/runtime-state-active-exposed.md) | Low | 双命名仍存在 | DB / unified API 使用 `active`，内部 legacy projection 与部分 Web 展示模型仍使用或接受 `exposed`。收口须保留 Client ↔ Server 的跨版本协议边界 |
| [`tunnel-resource-locks-hardening`](./issue/tunnel-resource-locks-hardening.md) | Low | 可选数据库约束硬化 | 资源互斥、冲突回滚和删除清理已有实现与测试；锁表仍缺少 `tunnel_id` 外键和 `resource_kind` CHECK，脏数据处理策略仍待确定 |

## 已实现并移出待办的功能

多用户功能已实现，不再作为 `Planned` 列入待办。[多用户设计契约](./issue/multi-user-support.md) 保留为实现参考：

- 管理员和普通用户统一使用 `users` / `user_sessions`，migration `012` 负责旧身份与资源所有权迁移。
- 用户管理、最后一个正常管理员保护、资源所有权、REST / SSE / 流量 / 活动范围隔离已有实现。
- 用户禁用保留资产与运行意图，恢复后沿用现有重连和 reconcile；删除只接受已禁用用户，并物理清除相关数据。这里没有用户软删除或恢复已删除用户的语义。
- 管理端分页用户列表和用户资源视图已实现；发布时仍需按既有 CI / E2E 门禁回归。

## 核实依据

- **P2P**：[peer transport](../pkg/p2p/session.go)、[Server 协调器](../internal/server/p2p_coordinator.go)、[Client 管理器](../internal/client/p2p_manager.go)、[真实 Client 数据路径测试](../internal/server/unified_tunnel_e2e_test.go)、[NAT 矩阵](../pkg/p2p/network_matrix_extended_test.go) 和[双 NAT Docker E2E](../test/e2e/p2p_double_nat_e2e_test.go)。双 NAT 测试有独立 `make test-p2p-double-nat-e2e` 入口，当前常规 CI 没有调用该目标。
- **多用户**：[迁移](../internal/server/migrations/012_multi_user_ownership.sql)、[API](../internal/server/user_api.go)、[生命周期门禁](../internal/server/user_lifecycle_gate.go)、[迁移验证](../internal/server/storage_schema_test.go)、[并发生命周期测试](../internal/server/user_lifecycle_gate_test.go)、[浏览器生命周期测试](../web/e2e/multi_user_lifecycle.pw.ts)和[多用户实时隔离测试](../web/e2e/multi_user_live_isolation.pw.ts)。
- **Endpoint CHECK**：当前最终表结构见 [migration 017](../internal/server/migrations/017_tls_sni_ingress.sql)，[存储测试](../internal/server/unified_storage_test.go)仍断言未来 target type 被 DB CHECK 拒绝。
- **运行态双命名**：[存储投影](../internal/server/store.go)保留 `storageRuntimeStateFromProtocol` / `protocolRuntimeStateFromStorage`；[前端状态展示](../web/src/lib/tunnel-model.ts)同时处理 `active` / `exposed`。
- **资源锁**：锁表定义仍来自 [migration 005](../internal/server/migrations/005_unified_tunnel_storage.sql)；[存储测试](../internal/server/unified_storage_test.go)覆盖冲突事务回滚及隧道删除后的锁清理。

## 本次验证范围

2026-11-12 核实中执行了以下回归，均通过；前端共 4 个测试文件、34 项测试：

```bash
go test ./internal/server ./internal/storage ./internal/client ./pkg/protocol -run 'User|Owner|Scope|Principal|ControlAuth|DataChannel_Handshake|OpenServerDB|Migration|ResourceLock|RuntimeState|P2P|PeerManager|TransportPolicy' -count=1 -timeout 5m
go test ./internal/server -run '^(TestUnifiedTunnelStorageRejectsFutureOnlyTargetTypes|TestUnifiedClientToClient.*|TestOlderStrictMigrationSetRejects012BeforeWritableOpen)$' -count=1 -timeout 5m
go test ./pkg/p2p -count=1 -timeout 5m
cd web && bun run test src/hooks/use-users.test.ts src/lib/auth.test.ts src/lib/tunnel-model.test.ts src/components/custom/users/user-list-state.test.ts
```

本次是文档状态核实，未重跑 Docker nginx/caddy、双 NAT、完整 Playwright、跨版本/升级矩阵或全仓竞态测试。相应入口见 [Makefile](../Makefile)、[常规 CI](../.github/workflows/ci.yml) 和[完整跨版本工作流](../.github/workflows/cross-version-e2e.yml)；后续发布仍按这些门禁验证，真实运营商网络与 passive ICE-TCP 需要另外准备部署或原型环境。

## 原则

1. 会触及全仓状态语义、迁移兼容、legacy API 写路径或通用安全基础设施的问题，必须独立治理。
2. 每个 issue 都应有明确验证方式，不能只记录问题不定义完成标准。
3. 已实现能力不继续标为 `Open` / `Planned`；若只剩部署验证或可选扩展，状态和说明必须写清剩余范围。
4. 已完成的功能移出待办索引；保留的设计契约应标明实现状态，历史基线不得当作当前实现。相关证据以代码、测试和 Git 历史为准。
