# Secrets management

## Status

Open

## Severity

High

## Why it matters

SOCKS5 username/password、HTTP Basic auth 等 ingress auth 凭据如果长期以明文或可直接恢复形式保存，会造成真实安全风险。

## Current evidence

现有 endpoint config 使用 JSON 字段承载配置，缺少统一 secret 引用模型。SOCKS5 CONNECT 本次实现仍必须先解决本期 auth 凭据的基本安全要求。

主要代码位置：

- endpoint config 类型与 JSON 字段：`pkg/protocol/types.go`
- unified API config decode/encode：`internal/server/unified_tunnel_api.go`
- 存储 JSON config：`internal/server/store.go`
- 前端表单和模型：`web/src/lib/tunnel-model.ts`、`web/src/components/custom/tunnel/`

## Recommended direction

分两层处理：

1. SOCKS5 CONNECT 本次必须实现：
   - password 不明文落库；
   - 存储 `password_hash`；
   - 使用 Argon2id 作为密码哈希方案；
   - API、日志、事件、导出统一脱敏；
   - HTTP Basic Auth 与 SOCKS5 username/password 复用 credential hashing / verify / redact 工具。
2. 后续独立治理：
   - secrets table 或 secret store；
   - secret ID 引用；
   - secret rotation；
   - encryption-at-rest；
   - 多种 secret 类型统一管理。

## Why not in SOCKS5 CONNECT PR

通用 secret store 是安全基础设施，不应阻塞 SOCKS5 CONNECT。但是这不代表本期可以明文保存 SOCKS5/HTTP auth password。本期必须做到 password hash 与脱敏；本 issue 仅追踪后续通用 secret infrastructure。

## Validation needed

- 本期 SOCKS5/HTTP ingress auth password 不明文落库。
- 密码不出现在日志、事件、API 响应、诊断导出。
- password hash verify 有测试覆盖。
- secret 删除/轮换有定义。
- 迁移旧明文配置有路径。
