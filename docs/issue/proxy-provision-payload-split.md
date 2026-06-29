# ProxyNewRequest 与 provisioning payload 拆分

## Status

Planned for one-pass completion

Implementation contract: [`docs/proxy-provision-payload-split-plan.md`](../proxy-provision-payload-split-plan.md). If this issue summary conflicts with the plan, the plan is authoritative.

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

这不等于允许新旧模型无限期共存。本次改造必须一次性完成 unified provisioning/runtime cutover，并把 `ProxyNewRequest` 降级为 legacy-only DTO。

## One-pass scope

本次目标是消除 v2/unified runtime 对 `ProxyNewRequest` 的结构性依赖，而不是只给 SOCKS5 做特判。范围以 [`docs/proxy-provision-payload-split-plan.md`](../proxy-provision-payload-split-plan.md) 为准，核心包括：

1. TCP/UDP/HTTP unified provisioning 也从 `TunnelProvisionRequest.Spec` 构造 runtime config，不再通过 `proxyRequestFromTunnelSpec` 降级；
2. client runtime cache 统一到 endpoint-specific runtime：client-side ingress 继续使用现有 `clientTunnelRuntime`，fixed service target 新增独立 runtime；`client.proxies` 仅保留给 legacy `MsgTypeProxyProvision`；
3. v1 create / legacy wire path 必须继续支持，并在边界层转译到 `TunnelSpec` 或明确标注为兼容入口；
4. `ProxyCreateRequest = ProxyNewRequest` 与 `ProxyProvisionRequest = ProxyNewRequest` 的别名关系应拆分或隔离，避免 create schema 和 provisioning schema 继续共用；
5. 删除 `proxyRequestFromTunnelSpec`，禁止保留改名后的等价降级 helper，使新 endpoint type 无法误用旧 flat model；
6. 迁移测试 fixture 到 `TunnelSpec` / endpoint runtime helper，避免新测试继续扩大 `ProxyNewRequest` 覆盖面。

## Implementation rule

不要再把此问题拆成后续阶段。风险控制通过明确范围、兼容矩阵和测试完成，而不是通过保留 `proxyRequestFromTunnelSpec` 或继续让 TCP/UDP/HTTP unified runtime 依赖旧 flat model。

## Validation needed

- TCP/UDP/HTTP unified provisioning 不再经 `proxyRequestFromTunnelSpec`。
- legacy `MsgTypeProxyProvision` 必须继续兼容旧 client。
- v1 create API 与 v2 unified API 的写入语义必须一致，或 v1 被明确限制为兼容入口但仍保持向后兼容。
- client stream matching 从统一 runtime config 工作，TCP/UDP/HTTP/SOCKS5 均有覆盖。
- 旧 DB 中 TCP/UDP/HTTP tunnel 重启恢复不变。
