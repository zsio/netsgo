# 前端错误码对齐

## Status

Open; should be included if SOCKS5 frontend/API error handling is touched

## Severity

Low

## Why it matters

前端处理的部分错误码与后端实际返回不一致，会导致用户看到通用错误文案。

## Current evidence

前端存在 `unknown_target_type` / `unsupported_target_type` 等分支，后端使用 `unsupported_endpoint_type` / `invalid_target_type` 等错误码。

主要代码位置：

- `web/src/lib/tunnel-model.ts`
- `web/src/lib/tunnel-model.test.ts`
- `pkg/protocol/types.go` 的 mutation error code 常量
- `internal/server/unified_tunnel_api.go` 的 validation error 返回

## Recommended direction

对齐前后端错误码，删除死分支，补充 i18n 文案和测试。

## Why not in SOCKS5 CONNECT PR

这是 UI 体验修复，不是架构 blocker。但 SOCKS5 会新增 endpoint/capability/access-control 错误，如果本次已经修改前端/API 错误处理，应把该小修一起完成，避免后续再分散补丁。

## Validation needed

- 后端实际错误码都有前端展示文案。
- 测试覆盖 unsupported endpoint、invalid target、capability not supported。
