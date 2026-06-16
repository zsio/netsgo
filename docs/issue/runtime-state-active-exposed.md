# runtime_state active/exposed 双命名

## Status

Open

## Severity

Medium

## Why it matters

数据库存储 `active`，协议/API/前端语义使用 `exposed`。这会让直接 SQL、迁移、测试和前端兼容逻辑长期背负双命名成本。

## Current evidence

- SQLite CHECK 允许 `active`。
- Go 协议常量存在 `ProxyRuntimeStateExposed = "exposed"`。
- 存储层存在 `storageRuntimeStateFromProtocol` / `protocolRuntimeStateFromStorage` 翻译函数。
- 前端兼容 `exposed` 与 `active`。

主要代码位置：

- `internal/server/migrations/005_unified_tunnel_storage.sql`
- `pkg/protocol/types.go`
- `internal/server/store.go`
- `web/src/lib/tunnel-model.ts`

## Recommended direction

单独迁移到统一的 `exposed`，删除存储层翻译函数，并保留必要的读取兼容测试。

## Why not in SOCKS5 CONNECT PR

它不阻塞 SOCKS5。混入会扩大迁移和回归范围，让 SOCKS5 runtime 问题与状态迁移问题难以定位。

## Validation needed

- 旧 DB 中 `active` 正确迁移为 `exposed`。
- 所有写路径不再写入 `active`。
- API/前端/事件流只暴露 `exposed`。
- TCP/UDP/HTTP 隧道恢复行为不变。
