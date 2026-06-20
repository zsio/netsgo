# ProxyNewRequest 与 provisioning payload 拆分

## Status

Partial in SOCKS5; next phase planned

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

`TunnelProvisionRequest{Spec TunnelSpec}` 应成为 v2/unified provisioning 的 canonical schema。client unified tunnel runtime 应从 endpoint config 构造 endpoint-specific runtime，而不是先统一降级成 `ProxyNewRequest`。

当前代码已经有 `TunnelProvisionRequest{Spec TunnelSpec}`。问题在于 target role 处理时又调用 `proxyRequestFromTunnelSpec` 转成 `ProxyNewRequest`。SOCKS5 CONNECT 本次必须先止血：

- SOCKS5 ingress/target runtime 不得依赖 `ProxyNewRequest`、`local_ip` 或 `local_port`；
- server -> client provisioning 必须能完整表达 `socks5_connect_handler` 所需的 target access policy 和 dial 配置；
- `ProxyNewRequest` 不得为了 SOCKS5 增加动态 target、target policy、auth 或 access policy 字段；
- `ProxyProvisionRequest = ProxyNewRequest` 仅保留给旧 `MsgTypeProxyProvision` wire path；
- 新增 endpoint type 默认不得扩展 `ProxyNewRequest`，必须走 `TunnelSpec` / endpoint-specific runtime。

这不等于允许新旧模型无限期共存。下一期应把本 issue 作为独立治理项，完成 unified provisioning/runtime cutover，并把 `ProxyNewRequest` 降级为 legacy-only DTO。

## Next phase scope

下一期目标是消除 v2/unified runtime 对 `ProxyNewRequest` 的结构性依赖，而不是只给 SOCKS5 做特判。建议范围：

1. TCP/UDP/HTTP unified provisioning 也从 `TunnelProvisionRequest.Spec` 构造 runtime config，不再通过 `proxyRequestFromTunnelSpec` 降级；
2. client runtime cache 统一到 `clientTunnelRuntime` / endpoint-specific runtime，`client.proxies` 仅保留给 legacy `MsgTypeProxyProvision`；
3. v1 create / legacy wire path 若继续支持，应在边界层转译到 `TunnelSpec` 或明确标注为兼容入口；
4. `ProxyCreateRequest = ProxyNewRequest` 与 `ProxyProvisionRequest = ProxyNewRequest` 的别名关系应拆分或隔离，避免 create schema 和 provisioning schema 继续共用；
5. 删除或收缩 `proxyRequestFromTunnelSpec`，使新 endpoint type 无法误用旧 flat model；
6. 迁移测试 fixture 到 `TunnelSpec` / endpoint runtime helper，避免新测试继续扩大 `ProxyNewRequest` 覆盖面。

## Why not fully in SOCKS5 CONNECT PR

SOCKS5 CONNECT 本次必须解决 SOCKS5 provisioning 表达力和 runtime 依赖问题；这部分不能后置。可以后置的是全仓 legacy cutover：它会同时重写 TCP/UDP/HTTP provisioning、legacy v1 create、client stream dispatch、UDP handler、offline managed tunnel 恢复和大量测试 fixture，回归面独立且较大。

本期边界应是“切断 SOCKS5/unified 新 endpoint 对旧 flat model 的依赖并禁止继续扩张”，下一期再做“把现有 TCP/UDP/HTTP unified runtime 也迁出旧模型”。

## Validation needed

SOCKS5 本期：

- SOCKS5 handler config 能完整到达 client。
- SOCKS5 runtime 不读取 `LocalIP` / `LocalPort` 作为 target 语义。
- `ProxyNewRequest` 没有新增 SOCKS5 dynamic target、target policy、auth 或 access policy 字段。
- 旧 TCP/UDP/HTTP provisioning 不变。
- 旧 create API 不被误用为 SOCKS5 provisioning schema。

下一期：

- TCP/UDP/HTTP unified provisioning 不再经 `proxyRequestFromTunnelSpec`。
- legacy `MsgTypeProxyProvision` 仍兼容旧 client，或有明确移除/迁移策略。
- v1 create API 与 v2 unified API 的写入语义一致，或 v1 被明确限制为兼容入口。
- client stream matching 从统一 runtime config 工作，TCP/UDP/HTTP/SOCKS5 均有覆盖。
- 旧 DB 中 TCP/UDP/HTTP tunnel 重启恢复不变。
