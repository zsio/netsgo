# Socket Tunnel Storage Design Notes

> Status: Superseded  
> Superseded by: [`docs/socks5-implementation-rfc.md`](./socks5-implementation-rfc.md)

本文档保留为 SOCKS5 隧道设计的历史背景。早期讨论的核心价值是确认 NetsGo 现有存储模型已经接近：

```text
TunnelSpec = Ingress + Target + Transport
```

也就是说，SOCKS5 不应通过新增一套平行 tunnel 模型实现，而应作为新的 endpoint type 纳入统一隧道模型。

最终设计以后续 RFC 为准：[`SOCKS5 Tunnel Implementation RFC`](./socks5-implementation-rfc.md)。

## 保留下来的有效结论

1. `tunnels` 表的 `ingress_config` 与 `target_config` 应继续承载 endpoint-specific 配置，不应为每种代理模式增加专用列。
2. 当前 `ingress_type` / `target_type` 的 SQLite `CHECK` 约束会阻塞新增 endpoint type，需要迁移。
3. 安全配置应按执行位置建模：
   - server ingress 侧负责监听、来源限制、SOCKS5 认证；
   - client target 侧负责目标地址策略与实际 dial。
4. 现有 TCP/UDP/HTTP 隧道数据必须在迁移中保持语义不变。

## 被最终 RFC 修正的内容

早期讨论曾考虑“server 仅做 TCP listener，client 侧解析 SOCKS5”。该方向已经废弃。

正确模型是：

```text
external user -> server SOCKS5 handshake/auth/CONNECT -> client target policy/dial -> target service
```

原因是外部用户连接的是 server 暴露端口，按照 RFC 1928 / RFC 1929，SOCKS5 server 角色必须在 server ingress 侧完成 method negotiation、认证、CONNECT request 解析和标准 reply 返回。

## 后续阅读

- 最终实施设计：[`docs/socks5-implementation-rfc.md`](./socks5-implementation-rfc.md)
- 后续独立治理问题索引：[`docs/issues.md`](./issues.md)
