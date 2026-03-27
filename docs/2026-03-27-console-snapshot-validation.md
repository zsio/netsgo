# Console Snapshot 验证记录（2026-03-27）

## 范围

本记录对应控制台状态同步这一批次的收口：

1. 新增统一的 `GET /api/console/snapshot` 接口
2. 页面在 SSE 重连成功后立即用该接口重新同步客户端和服务端状态
3. 继续保留定时 SSE `snapshot` 作为临时兜底

## 对齐结论

- 当前完整控制台快照已经有统一入口：`/api/console/snapshot`
- SSE 建连后仍会发送 `snapshot`，定时 `snapshot` 也还保留
- 页面重连成功后，不再只等下一次定时 `snapshot`，而是会主动拉一次最新完整快照

## 验证记录

- PASS — `go test ./...`
- PASS — `cd web && bun run build`
- PASS — `cd web && bunx eslint src/hooks/use-event-stream.ts src/types/index.ts`
- PASS — `cd web && bun test`

## 备注

- 这次收口解决的是“页面重连后缺少主动完整状态入口”这一部分，不代表已经可以移除定时 SSE `snapshot`
- 慢连接丢事件后的最终纠偏，目前仍然依赖定时完整快照这条安全网
