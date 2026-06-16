# P2P data transport policy

## Status

Open

## Severity

High

## Why it matters

client_to_client 数据通道未来应支持 `server_relay_only`、`direct_preferred`、`direct_only`。控制通道仍必须经 server，下述策略只针对数据通道。

## Current evidence

存储模型已有 `transport_policy`、`actual_transport`、`p2p_state` 等字段，但 P2P 发送/接收实现尚未形成完整闭环。

主要代码位置：

- `pkg/protocol/types.go` 的 transport/P2P 字段
- `internal/server/migrations/005_unified_tunnel_storage.sql`
- `internal/server/client_relay.go`
- `internal/server/unified_tunnel_reconcile.go`
- client 数据通道和 stream 打开路径

## Recommended direction

单独设计 P2P 数据通道状态机：候选收集、握手、fallback、direct_only 失败语义、TURN/relay 统计与 UI 展示。

## Why not in SOCKS5 CONNECT PR

SOCKS5 首期限定 `server_relay_only`。P2P 是跨 server/client/protocol 的大设计，不能作为 SOCKS5 附带修改。

## Validation needed

- relay only / preferred / only 三种策略行为明确。
- direct 失败时 fallback 或 error 符合策略。
- 控制通道始终经 server。
- 数据面统计能区分 transport。
