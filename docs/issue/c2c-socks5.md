# client_to_client SOCKS5

## Status

Open

## Severity

Medium

## Why it matters

c2c SOCKS5 不是 server_expose SOCKS5 的直接复用。SOCKS5 握手发生在哪里、谁监听、谁认证、谁执行 target policy 都需要重新定义。

## Current evidence

最终 SOCKS5 CONNECT RFC 首期只允许 `server_expose + socks5_listen -> socks5_handler`。

相关代码范围未来会涉及：

- `internal/server/client_relay.go`
- `internal/client/unified_tunnel.go`
- `internal/server/unified_tunnel_api.go`
- client capability/provisioning 双端协调

## Recommended direction

单独定义 c2c SOCKS5 产品语义，并明确 ingress client 的监听、server 控制面下发、target client dial 与数据通道策略。

## Why not in SOCKS5 CONNECT PR

c2c SOCKS5 会改变首期“server 侧处理 SOCKS5 握手”的模型，不应混入。

## Validation needed

- ingress client 和 target client 的能力声明。
- c2c provisioning 双端一致。
- stop/resume/delete/reconnect 行为正确。
