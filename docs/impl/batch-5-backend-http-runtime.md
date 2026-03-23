# Batch 5：入口分发与 HTTP 运行时闭环

> 状态：待实现  
> 所属阶段：阶段 4（入口分发与运行时）  
> 前置条件：阶段 3 完成，且 Batch 4 的测试已落地  
> 估计影响文件：`internal/server/server.go`、`internal/server/http_tunnel.go`（扩展）、`internal/server/data.go`、`internal/server/server_test.go`

## 目标

把 HTTP 域名隧道真正接到最终入口上，形成最小可工作的运行时闭环：

1. 最终 handler 按固定顺序分发请求
2. 命中 HTTP 隧道时能代理普通 HTTP、业务 WebSocket、SSE
3. 只有合法的 NetsGo 内部 WS 握手才进入控制 / 数据通道
4. `StartHTTPOnly()` 返回最终 handler，而不是旧的内部 mux

## 本批次负责的边界

### 负责

- `hostDispatchHandler`
- `404 / 503 / 502` 的请求级行为
- HTTP 反向代理
- 业务 WebSocket relay
- SSE flush
- 服务端内部 WS 子协议协商与回写

### 不负责

- 离线 HTTP 隧道的 store-first 管理动作
- Client 侧主动发送子协议
- nginx / caddy E2E
- 前端

## 关键约束

### 1. 分发顺序固定

```text
1. 合法 control WS 握手
2. 合法 data WS 握手
3. Host 命中 HTTP 隧道域名
4. setup 例外
5. Host == effectiveManagementHost
6. 其他 -> 404
```

### 2. 内部 WS 识别必须是“path + 子协议”

只靠 path 不行，只靠 header 也不行。  
缺失或错误子协议时，请求必须被当成普通业务请求继续分发。

### 3. 服务端子协议协商属于本批次

这里是旧拆解最容易让执行变乱的地方：  
服务端是否接受并回写 `netsgo-control.v1` / `netsgo-data.v1`，本质上属于入口运行时，不应拖到 Client 批次。

本批次要求：

- control 路径只接受 control 子协议
- data 路径只接受 data 子协议
- 升级成功后回写协商结果
- 不再按 path 猜测内部通道

实现方式可以二选一：

1. control / data 各自使用专用 upgrader
2. 在各自 handler 中读取声明的 subprotocol，并在 `Upgrade` 时显式回写

只要满足协商契约即可，不要求提前抽象一层框架。

### 4. `securityHeadersHandler` 只包管理面

不能把管理面的安全响应头污染到 HTTP 隧道业务响应。

## 实现内容

### 一、`hostDispatchHandler`

建议落在 `internal/server/server.go` 或 `internal/server/http_tunnel.go`。

职责：

- 先判断合法内部 WS 握手
- 再判断 HTTP 隧道域名
- 再判断 setup 例外
- 最后判断管理 Host

### 二、HTTP 隧道可服务性

命中已声明 domain 时：

- active + client 在线 -> 代理
- pending / paused / stopped / error -> 503
- active 但 client 离线 -> 503

注意：

- “domain 已声明但当前不可服务”应该是 `503`
- “domain 根本不存在”才是 `404`

### 三、HTTP 代理

普通 HTTP 请求：

- 通过 yamux stream 转发
- 透传原始 `Host`
- 设置 `X-Forwarded-*`

SSE / chunked：

- `FlushInterval = -1`

业务 WebSocket：

- 不走 `ReverseProxy`
- 走专门的 WebSocket / TCP relay

### 四、`StartHTTPOnly()`

当前 `StartHTTPOnly()` 仍返回旧的 `*http.ServeMux`。  
本批次改为返回最终 `http.Handler`，让测试真正覆盖最外层入口。

这里要按生产规则处理，不为旧测试保留错误入口：

- 生产 `s.httpServer.Handler` 必须从旧的 `s.securityHeadersHandler(s.newHTTPMux())` 切到最终 handler
- `StartHTTPOnly()` 返回类型必须改为 `http.Handler`
- 现有直接调用 `s.newHTTPMux()` 且依赖外部 HTTP 入口语义的测试，必须同步更新
- 不允许为了让旧测试继续通过而保留错误的外层入口

需要重点检查的现有测试文件至少包括：

- `internal/server/server_test.go`
- `internal/server/security_fix_test.go`
- `internal/server/data_test.go`
- `internal/server/admin_api_test.go`

### 五、管理地址相关

`effective_server_addr` 的计算与冲突语义已经在阶段 2 落地。  
本批次只负责在最终 handler 中使用这套语义，不新增新的持久化字段。

## 实现步骤

1. 先实现 `hostDispatchHandler`
2. 接入 HTTP 隧道查找与 503/404 分支
3. 实现 HTTP 请求代理
4. 实现业务 WebSocket relay
5. 确保 SSE / chunked 立即 flush
6. 为 control/data handler 加上服务端子协议协商与回写
7. 修改生产 `s.httpServer.Handler`，同步切到最终 handler
8. 修改 `StartHTTPOnly()` 返回最终 handler
9. 更新受影响的旧测试入口，不再让它们绕过生产外层 handler
10. 跑 Batch 4 的测试直到全部通过

## 验收标准

```bash
go test ./internal/server/... -run TestDispatch -v
go test ./internal/server/... -v
go test ./internal/client/... -v
go test ./pkg/... -v
go build ./...
```

## 关键验收点

1. 非管理 Host + 合法 control 子协议 -> 能进入控制通道
2. `/ws/control` 缺失子协议 -> 不进入内部通道
3. 命中 HTTP 域名的 `/api/*` -> 仍走业务代理
4. 命中 HTTP 域名的 `/ws/*` -> 仍可作为业务 WebSocket
5. 未知 Host -> 404
6. 已声明但当前不可服务 -> 503
7. 单次代理失败 -> 502

## 不引入的改动

- 不改 Client 侧发送逻辑（Batch 6）
- 不改 nginx/caddy E2E（Batch 7）
- 不改前端（Batch 8）
