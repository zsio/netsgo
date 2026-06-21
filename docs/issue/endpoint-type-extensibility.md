# endpoint type 长期可扩展性

## Status

Partial done in SOCKS5; Open for full CHECK relaxation

## Severity

Medium

## Why it matters

当前 DB 用 enum-like CHECK 限制 `ingress_type` / `target_type`。每新增 endpoint type 都需要重建表。

## Current evidence

`tunnels` 表存在 `CHECK (ingress_type IN (...))` 与 `CHECK (target_type IN (...))`。

主要代码位置：

- `internal/server/migrations/005_unified_tunnel_storage.sql`
- `internal/server/storage_schema.go`
- `internal/server/unified_tunnel_api.go`
- `internal/server/store.go`

## Recommended direction

SOCKS5 CONNECT 本次只扩展 CHECK 到已知类型：`socks5_listen` / `socks5_connect_handler`。长期若要支持插件式 endpoint，应把 endpoint 组合校验集中到 Go 层，并用统一兼容矩阵覆盖所有写路径。

## Why not in SOCKS5 CONNECT PR

完全放松 CHECK 会移除 DB 兜底，必须同时审计 legacy/v1、v2、restore、测试辅助等所有写路径，范围超过 SOCKS5。

## Validation needed

- 所有创建/更新路径调用同一兼容校验。
- 非法 endpoint type 无法写入。
- 非法 topology/endpoint 组合无法写入。
- 迁移后旧数据不变。
- fresh DB 和旧 DB migration 都通过 schema validation。
