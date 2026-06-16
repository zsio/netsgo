# P2P placeholder 清理

## Status

Open

## Severity

Low

## Why it matters

P2P 相关消息、字段或状态如果只是占位，容易让读代码者误判功能已实现。

## Current evidence

存储和协议中已有 P2P 字段，但完整 P2P 数据面尚未实现。

主要代码位置：

- `pkg/protocol/types.go`
- `internal/server/migrations/005_unified_tunnel_storage.sql`
- `internal/server/unified_tunnel_reconcile.go`
- 前端 tunnel 状态展示相关代码

## Recommended direction

要么补齐 P2P 设计与实现路线，要么在代码/文档中明确标注 future-only，并清理无用占位。

## Why not in SOCKS5 CONNECT PR

SOCKS5 首期使用 server relay。P2P 清理与 SOCKS5 CONNECT 正确性无关。

## Validation needed

- 文档与 UI 不暗示 P2P 已可用。
- 未实现路径不可被用户配置触发。
- future-only 字段有测试保护或清晰注释。
