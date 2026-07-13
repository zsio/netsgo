# NetsGo Design Issues

本文档只记录当前仍需要单独治理的问题。已经完成或被后续 RFC 替代的文档应删除，避免把历史上下文误读成待办。

## Issue 索引

| Issue | Severity | 状态 | 说明 |
|---|---|---|---|
| [`p2p-data-transport-policy`](./issue/p2p-data-transport-policy.md) | High | Open | Pion WebRTC/ICE 直连、Client-pair 共享 PeerConnection、自有信令、60 秒授权租约、归属方统计、双向公平限速及 UI/API 门禁 |
| [`endpoint-type-extensibility`](./issue/endpoint-type-extensibility.md) | Medium | Open for CHECK relaxation | CHECK 已扩展至 `socks5_listen` / `socks5_connect_handler`；剩余是是否移除 DB enum CHECK |
| [`runtime-state-active-exposed`](./issue/runtime-state-active-exposed.md) | Low | Open | `active` / `exposed` 是同一运行态的双命名；可作为低风险命名收口 |
| [`tunnel-resource-locks-hardening`](./issue/tunnel-resource-locks-hardening.md) | Low | Open for optional DB constraints | 运行时互斥已完成；剩余是可选 DB FK/CHECK 硬化 |

## 原则

1. 会触及全仓状态语义、迁移兼容、legacy API 写路径或通用安全基础设施的问题，必须独立治理。
2. 每个 issue 都应有明确验证方式，不能只记录问题不定义完成标准。
3. 完成态 issue 不保留在本索引中；相关实现证据应以代码、测试和 Git 历史为准。
