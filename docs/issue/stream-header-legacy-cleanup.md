# legacy StreamHeader 清理

## Status

Open

## Severity

Low

## Why it matters

旧 StreamHeader helper 若已无生产路径，会增加协议维护噪音。

## Current evidence

仓库存在 legacy stream header helper，与当前 `DataStreamHeader` 并存。

主要代码位置：

- `pkg/protocol/stream_header_helpers.go`
- `pkg/protocol/stream_header.go`
- `pkg/mux/data_stream_header.go`

## Recommended direction

确认无生产调用后删除，或标记 deprecated 并保留迁移说明。

## Why not in SOCKS5 CONNECT PR

与 SOCKS5 无关。混入会扩大协议层 diff。

## Validation needed

- 全仓无生产调用。
- 相关测试删除或迁移。
- 当前 DataStreamHeader 测试仍完整。
