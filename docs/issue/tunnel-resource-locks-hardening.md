# tunnel_resource_locks 硬化

## Status

Partial done in SOCKS5; Open for FK/CHECK hardening

## Severity

Medium

## Why it matters

资源锁表缺少 FK 和 `resource_kind` CHECK，崩溃或旧 bug 可能留下孤儿锁或非法 kind。

## Current evidence

`tunnel_resource_locks` 当前包含 `resource_key`、`tunnel_id`、`resource_kind`、`client_id`、`created_at`，但未声明 FK/CHECK。

主要代码位置：

- `internal/server/migrations/005_unified_tunnel_storage.sql`
- `internal/server/store.go` 的 resource lock 生成与写入逻辑
- `internal/server/storage_schema_test.go`

## Recommended direction

SOCKS5 CONNECT 本次必须保证 `socks5_listen` 与普通 `tcp_listen` 竞争同一个 bind ip + port 资源。FK/CHECK 硬化应单独迁移并明确脏数据策略：迁移前检测并失败，或从 `tunnels` 表重建 locks。不要盲目 copy 旧 locks 到带约束的新表。

## Why not in SOCKS5 CONNECT PR

SOCKS5 需要端口互斥语义正确，这部分不能后置。FK/CHECK 硬化会引入脏数据迁移策略和额外失败模式，应单独验证。

## Validation needed

- orphan lock 处理符合设计。
- unknown resource_kind 处理符合设计。
- cascade delete 生效。
- 资源锁可从 tunnels 重建且结果一致。
