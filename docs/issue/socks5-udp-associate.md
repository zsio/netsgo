# SOCKS5 UDP ASSOCIATE

## Status

Open

## Severity

Medium

## Why it matters

RFC 1928 定义了 UDP ASSOCIATE，但它不是 CONNECT 的简单扩展，需要 UDP relay 地址、TCP control connection 生命周期、NAT 和超时语义。

## Current evidence

当前 SOCKS5 RFC 首期只设计 CONNECT。

相关代码范围未来会涉及：

- server SOCKS5 listener/runtime
- client UDP handler 与 UDP association 管理
- `pkg/mux` UDP frame 相关代码
- traffic accounting
- frontend/API 配置模型

## Recommended direction

单独设计 SOCKS5 UDP：明确 UDP relay 监听位置、帧格式、会话超时、权限策略和流量统计。

## Why not in SOCKS5 CONNECT PR

混入 UDP ASSOCIATE 会显著扩大数据面复杂度，影响 CONNECT 的正确交付。

## Validation needed

- UDP ASSOCIATE RFC reply 正确。
- UDP 包转发和关联 TCP 连接生命周期一致。
- NAT/超时/资源限制可控。
