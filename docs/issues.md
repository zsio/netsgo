# NetsGo Design Issues

本文档记录不应完整混入 SOCKS5 CONNECT 首次实现的独立治理问题。部分 issue 中存在“本次必须完成的局部能力”和“后续单独治理的大范围清理”，每个 issue 文档会明确边界。

## Issue 索引

| Issue | Severity | 状态 | 说明 |
|---|---|---|---|
| [`runtime-state-active-exposed`](./issue/runtime-state-active-exposed.md) | Medium | Open | DB 使用 `active`，协议/API 使用 `exposed` 的双命名债务 |
| [`endpoint-type-extensibility`](./issue/endpoint-type-extensibility.md) | Medium | Partial in SOCKS5 | 本次扩展 CHECK；长期是否移除 DB enum CHECK 后续治理 |
| [`tunnel-resource-locks-hardening`](./issue/tunnel-resource-locks-hardening.md) | Medium | Partial in SOCKS5 | 本次做 SOCKS5/TCP 端口互斥；FK/CHECK 与脏数据处理后续治理 |
| [`proxy-provision-payload-split`](./issue/proxy-provision-payload-split.md) | High | Partial in SOCKS5; Next phase planned | 本次切断 unified SOCKS5 对 `ProxyNewRequest` 的依赖并禁止扩张旧类型；下一期统一 v2 provisioning runtime、隔离/降级 legacy create/provision DTO |
| [`v1-v2-api-unification`](./issue/v1-v2-api-unification.md) | Medium | Open | 统一 legacy v1 与 unified v2 API 写路径 |
| [`secrets-management`](./issue/secrets-management.md) | High | Partial in SOCKS5 | 本次必须做 password hash/脱敏；通用 secret store 后续治理 |
| [`ingress-access-policy-rollout`](./issue/ingress-access-policy-rollout.md) | Medium | In SOCKS5 | SOCKS5/TCP/UDP/HTTP source allowlist 本次一并补齐 |
| [`socks5-udp-associate`](./issue/socks5-udp-associate.md) | Medium | Open | SOCKS5 UDP ASSOCIATE 支持设计 |
| [`c2c-socks5`](./issue/c2c-socks5.md) | Medium | In SOCKS5 | client_to_client SOCKS5 本次一并实现，P2P 传输策略后续治理 |
| [`p2p-data-transport-policy`](./issue/p2p-data-transport-policy.md) | High | Open | client_to_client 数据通道中继/P2P preferred/P2P only 策略 |
| [`frontend-error-code-alignment`](./issue/frontend-error-code-alignment.md) | Low | In SOCKS5 if touched | 前端错误码映射与后端实际错误码对齐，小修建议随 SOCKS5 前端改动完成 |
| [`stream-header-legacy-cleanup`](./issue/stream-header-legacy-cleanup.md) | Low | Open | 旧 StreamHeader helper 清理 |
| [`p2p-placeholder-cleanup`](./issue/p2p-placeholder-cleanup.md) | Low | Open | P2P 占位消息/状态的归档或实现计划 |

## 原则

1. SOCKS5 CONNECT PR 必须包含 SOCKS5 正确运行所必需的改动，也应包含低风险、同路径的小修复。
2. 会触及全仓状态语义、迁移兼容、legacy API 写路径或通用安全基础设施的问题，必须独立治理。
3. 每个 issue 都应有明确验证方式，不能只记录问题不定义完成标准。
