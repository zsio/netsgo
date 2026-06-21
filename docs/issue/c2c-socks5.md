# client_to_client SOCKS5

## Status

Done in SOCKS5

## Severity

Medium

## Why it matters

c2c SOCKS5 不是 server_expose SOCKS5 的直接复用。SOCKS5 握手发生在 ingress client，而不是 server；target policy 和实际 dial 仍发生在 target client。

## Current evidence

最终 SOCKS5 CONNECT RFC 首期同时允许：

```text
server_expose + server/socks5_listen -> client/socks5_connect_handler
client_to_client + ingress-client/socks5_listen -> target-client/socks5_connect_handler
```

相关代码范围会涉及：

- `internal/server/client_relay.go`
- `internal/client/unified_tunnel.go`
- `internal/server/unified_tunnel_api.go`
- client capability/provisioning 双端协调

## Recommended direction

本次实现 c2c SOCKS5 CONNECT，但边界必须清晰：

- ingress client 负责 `socks5_listen`、source allowlist、SOCKS5 auth、CONNECT 解析和标准 reply；
- server 负责控制面下发、能力门禁、状态聚合和 server relay 数据中继；
- target client 负责 `socks5_connect_handler`、target allowlist、DNS/CIDR/port 策略和实际 dial；
- transport policy 首期仍限定为 `server_relay_only`。

## Why in SOCKS5 CONNECT PR

用户期望 SOCKS5 隧道不只支持客户端到服务端暴露，也支持客户端到客户端入口。该能力与 endpoint type、capability、provisioning、target policy 和 DataStreamHeader 动态目标强相关；后续再补会造成二次重写。

P2P preferred / P2P only 的真实点对点传输策略仍不在本次范围内，见 [`p2p-data-transport-policy`](./p2p-data-transport-policy.md)。

## Validation needed

- ingress client 和 target client 的能力声明。
- c2c provisioning 双端一致。
- ingress client 上的 SOCKS5 listener 能完成 no-auth 与 username/password auth。
- CONNECT 动态目标能通过 server relay 到达 target client。
- target allowlist 拒绝时 ingress client 返回合理 SOCKS5 REP。
- stop/resume/delete/reconnect 行为正确。
