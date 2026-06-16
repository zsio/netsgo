# ProxyNewRequest 与 provisioning payload 拆分

## Status

Open

## Severity

High

## Why it matters

同一个 `ProxyNewRequest` 同时承担 client->server create request 与 server->client provisioning payload，字段语义随上下文变化。SOCKS5 target 没有固定 `local_ip/local_port`，继续扩张该类型会扩大旧债。

## Current evidence

`ProxyCreateRequest` 与 `ProxyProvisionRequest` 都是 `ProxyNewRequest` 的别名，字段是 TCP/UDP/HTTP flat 模型。

主要代码位置：

- `pkg/protocol/message.go`
- `pkg/protocol/types.go` 的 `ProxyConfig.ToProxyNewRequest`
- `internal/server/tunnel_ready.go`
- `internal/client/unified_tunnel.go` 的 `proxyRequestFromTunnelSpec`
- `internal/client/client.go` 的 `handleStream` / proxy cache

## Recommended direction

统一让 client 的 unified tunnel 路径使用现有 `TunnelProvisionRequest.Spec`，并从 endpoint config 构造 runtime。

当前代码已经有 `TunnelProvisionRequest{Spec TunnelSpec}`。问题在于 target role 处理时又调用 `proxyRequestFromTunnelSpec` 转成 `ProxyNewRequest`。本次应移除 SOCKS5 target 对该转换的依赖：client 应保存新的 target runtime config，或直接保存经过验证的 `TunnelSpec`。

SOCKS5 CONNECT 本次必须至少做到：server -> client provisioning 能完整表达 `socks5_handler` 所需的 target access policy 和 dial 配置，不能继续依赖固定 `local_ip/local_port` 作为真实语义。

## Why not in SOCKS5 CONNECT PR

本 issue 不代表 SOCKS5 provisioning 表达力可以后置。SOCKS5 所需 provisioning 必须本次解决。可以后置的是：彻底清理 legacy `ProxyNewRequest`、拆分所有 v1 create/provision 历史别名和旧 `MsgTypeProxyProvision` 路径。

## Validation needed

- TCP/UDP/HTTP provisioning 不变。
- SOCKS5 handler config 能完整到达 client。
- 旧 create API 不被误用为 provisioning schema。
