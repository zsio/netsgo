# 通用 ingress access policy 推进

## Status

Done in SOCKS5 CONNECT scope

## Severity

Medium

## Why it matters

`allowed_source_cidrs`、ingress auth、connection/rate limit 都是 ingress 入口访问控制，不应长期被设计成某个 tunnel type 的私有补丁。SOCKS5 本次必须有 source allowlist；TCP/UDP/HTTP 也必须按同一模型支持 source allowlist，否则用户会看到不同 tunnel type 的访问控制能力不一致。

## Current evidence

现有设计讨论已明确：

- SOCKS5 需要 `allowed_source_cidrs` 防止公开代理入口无边界暴露；
- HTTP Basic Auth 与 SOCKS5 username/password 都属于 server-side ingress auth；
- TCP/UDP/HTTP 也能在 ingress 侧看到 source address，因此具备实现 source allowlist 的条件。

主要代码位置：

- TCP/UDP/HTTP server ingress runtime：`internal/server/proxy.go`、`internal/server/udp_proxy.go`、HTTP dispatch 相关代码
- unified endpoint config decode：`internal/server/unified_tunnel_api.go`
- endpoint config 存储：`internal/server/store.go`
- frontend tunnel model/form：`web/src/lib/tunnel-model.ts`、`web/src/components/custom/tunnel/`

## Recommended direction

定义通用 `IngressAccessPolicy` 语义：

```text
allowed_source_cidrs
auth
connection_limits
rate_limits
```

SOCKS5 CONNECT 本次必须实现 `allowed_source_cidrs` 和 SOCKS5 auth。TCP/UDP/HTTP 的 ingress handler 也必须在同一 PR 中补齐 `allowed_source_cidrs`。

默认值为允许所有，但新 UI/API payload 应显式表达：前端默认填入允许所有，用户可自行收窄。后端不对 loopback 做隐式放行；需要本机访问时必须显式包含 `127.0.0.0/8` 和/或 `::1/128`。为兼容本功能上线前的本地数据与旧调用方，server 读取/视图/legacy 转换路径会把缺失的 `allowed_source_cidrs` 持久化或展示为显式 allow-all；空数组仍然非法，避免歧义。

## Why in SOCKS5 CONNECT PR

SOCKS5 的 source allowlist 是本期 blocker。TCP/UDP/HTTP source allowlist 是同类 ingress policy，若本次不补齐，会留下用户可见的不一致能力和后续补丁债。因此本次一并做。

## Validation needed

- SOCKS5 source allowlist 必须有运行时测试。
- TCP source allowlist 如本期实现，需覆盖允许/拒绝连接。
- UDP source allowlist 如本期实现，需覆盖允许/拒绝 packet。
- HTTP source allowlist 如本期实现，需覆盖允许/拒绝 request，并确认与 HTTP Basic Auth 顺序明确。
- UI/API 必须明确展示哪些 tunnel type 已支持 source allowlist。
