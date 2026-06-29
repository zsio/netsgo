# NetsGo Design Issues

本文档只记录当前仍需要单独治理的问题。已经完成或被后续 RFC 替代的文档应删除，避免把历史上下文误读成待办。

## Issue 索引

| Issue | Severity | 状态 | 说明 |
|---|---|---|---|
| [`proxy-provision-payload-split`](./issue/proxy-provision-payload-split.md) | High | Planned for one-pass completion | 一次性完成 unified runtime 对 `ProxyNewRequest` 的脱钩，隔离 legacy create/provision DTO，并删除 `proxyRequestFromTunnelSpec` |
| [`p2p-data-transport-policy`](./issue/p2p-data-transport-policy.md) | High | Open | client_to_client 数据通道中继/P2P preferred/P2P only 策略 |
| [`secrets-management`](./issue/secrets-management.md) | High | Partial done in SOCKS5 | password hash/脱敏已完成；通用 secret store 后续治理 |
| [`runtime-state-active-exposed`](./issue/runtime-state-active-exposed.md) | Medium | Open | DB 使用 `active`，协议/API 使用 `exposed` 的双命名债务 |
| [`endpoint-type-extensibility`](./issue/endpoint-type-extensibility.md) | Medium | Partial done in SOCKS5 | CHECK 已扩展至 socks5_listen/socks5_connect_handler；长期是否移除 DB enum CHECK 后续治理 |
| [`tunnel-resource-locks-hardening`](./issue/tunnel-resource-locks-hardening.md) | Medium | Partial done in SOCKS5 | SOCKS5/TCP 端口互斥已完成；FK/CHECK 与脏数据处理后续治理 |
| [`v1-v2-api-unification`](./issue/v1-v2-api-unification.md) | Medium | Open | 统一 legacy v1 与 unified v2 API 写路径 |
| [`socks5-udp-associate`](./issue/socks5-udp-associate.md) | Medium | Open | SOCKS5 UDP ASSOCIATE 支持设计 |
| [`stream-header-legacy-cleanup`](./issue/stream-header-legacy-cleanup.md) | Low | Open | 旧 StreamHeader helper 清理 |
| [`p2p-placeholder-cleanup`](./issue/p2p-placeholder-cleanup.md) | Low | Open | P2P 占位消息/状态的归档或实现计划 |

## 原则

1. 会触及全仓状态语义、迁移兼容、legacy API 写路径或通用安全基础设施的问题，必须独立治理。
2. 每个 issue 都应有明确验证方式，不能只记录问题不定义完成标准。
3. 完成态 issue 不保留在本索引中；相关实现证据应以代码、测试和 Git 历史为准。
