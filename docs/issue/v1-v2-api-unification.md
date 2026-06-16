# v1/v2 API 写路径统一

## Status

Open

## Severity

Medium

## Why it matters

legacy v1 `/api/clients/{id}/tunnels` 与 unified v2 `/api/tunnels` 可能以不同状态机写入同一存储模型，长期维护风险高。

## Current evidence

两套 API 使用不同请求结构和创建/下发顺序。

主要代码位置：

- legacy v1 client tunnel API：`internal/server/admin_api.go` 及相关 tunnel manager 路径
- unified v2 API：`internal/server/unified_tunnel_api.go`
- provisioning/ack 路径：`internal/server/tunnel_ready.go`

## Recommended direction

让 v1 内部转译到 v2 的统一服务层，或者明确 v1 只作为兼容入口并限制支持范围。

SOCKS5 CONNECT 首期应明确只支持 v2 `/api/tunnels` 创建；v1 若收到 SOCKS5 类型应返回清晰错误。

## Why not in SOCKS5 CONNECT PR

SOCKS5 可以只支持 v2 API。统一 v1/v2 会扩大回归面，不是 SOCKS5 的正确前置条件。

## Validation needed

- v1/v2 创建同类 tunnel 结果一致。
- 错误码一致。
- revision 与 provisioning 行为一致。
