# endpoint type 长期可扩展性

## Status

Open for full CHECK relaxation

## Severity

Medium

## Why it matters

当前 DB 仍用 enum-like CHECK 限制 `ingress_type` / `target_type`。每新增 endpoint type 都需要 table rebuild migration，而不是只在 Go 层注册新类型。

## Current evidence

已完成：

- `008_socks5_endpoint_types.sql` 已把 CHECK 扩展到 `socks5_listen` / `socks5_connect_handler`。
- 后续 `012_multi_user_ownership.sql` 重建表时保留枚举 CHECK；当前 `017_tls_sni_ingress.sql` 再次重建表，将入口 CHECK 扩展到 `tls_sni`，并保留现有 target 类型约束。
- protocol 层已有对应常量。

仍未完成：

- `tunnels` 表仍存在 `CHECK (ingress_type IN (...))` 与 `CHECK (target_type IN (...))`。
- 插件式或更频繁新增 endpoint type 时仍需要 rebuild table。
- endpoint 组合校验还没有完全替代 DB enum CHECK 的约束职责。

主要代码位置：

- `internal/server/migrations/005_unified_tunnel_storage.sql`
- `internal/server/migrations/008_socks5_endpoint_types.sql`
- `internal/server/migrations/012_multi_user_ownership.sql`
- `internal/server/migrations/017_tls_sni_ingress.sql`
- `internal/server/storage_schema.go`
- `internal/server/unified_tunnel_api.go`
- `internal/server/store.go`
- `internal/server/unified_storage_test.go`：`TestUnifiedTunnelStorageRejectsFutureOnlyTargetTypes` 仍验证 DB CHECK 拒绝尚未支持的 target 类型。

## Recommended direction

如果 endpoint type 会继续增长，应把 endpoint type 与 topology 组合校验集中到 Go 层，并决定 DB 是否只保留 shape-level 约束。完全放松 CHECK 会移除 DB 兜底，必须同时审计 legacy storage projection、restore、测试辅助和 migration 写路径。

## Validation needed

- 所有创建/更新路径调用同一兼容校验。
- 非法 endpoint type 无法写入。
- 非法 topology/endpoint 组合无法写入。
- 迁移后旧数据不变。
- fresh DB 和旧 DB migration 都通过 schema validation。
