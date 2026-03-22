# Batch 4：后端入口集成测试（先写测试）

> 状态：待实现
> 前置条件：Batch 3 完成
> 估计影响文件：`internal/server/http_dispatch_test.go`（新建）

## 目标

在实现路由层之前，先用集成测试把入口分发行为（`hostDispatchHandler`）的规则钉死。测试在本批次全部应当失败（因为 `hostDispatchHandler` 尚未实现），Batch 5 完成实现后再回来跑通。

## 背景

`hostDispatchHandler` 是整个 HTTP 域名隧道的核心入口，需要按以下顺序分发：

1. 内部 WS 控制 / 数据通道（path + 子协议双重识别）
2. HTTP 隧道域名路由（Host 命中）
3. Setup 阶段例外（系统未初始化时放行管理前端）
4. 生效管理 Host（走现有管理面）
5. 其他一律 404

## 需要新建的测试文件

### `internal/server/http_dispatch_test.go`

#### 分组 1：内部 WS 通道识别

```
TestDispatch_InternalControl_ValidSubprotocol
  - 任意 Host（包括 HTTP 隧道域名）+ path=/ws/control + netsgo-control.v1
  - 期望：进入控制通道逻辑（不被 HTTP 隧道截获）

TestDispatch_InternalData_ValidSubprotocol
  - 任意 Host + path=/ws/data + netsgo-data.v1
  - 期望：进入数据通道逻辑

TestDispatch_InternalControl_MissingSubprotocol
  - path=/ws/control，不带子协议
  - 期望：不进入内部通道；若 Host 命中 HTTP 隧道则转发，否则 404

TestDispatch_InternalControl_WrongSubprotocol
  - path=/ws/control + Sec-WebSocket-Protocol: netsgo-data.v1（反串）
  - 期望：不进入内部通道

TestDispatch_Internal_OnNonManagementHost
  - 非管理 Host + path=/ws/control + netsgo-control.v1
  - 期望：仍能进入控制通道（Client 可从任意可达地址连接）
```

#### 分组 2：HTTP 隧道域名路由

```
TestDispatch_HTTPTunnel_AnyPath
  - Host 命中活跃 HTTP 隧道，path 任意（/、/api/foo、/ws/chat）
  - 期望：请求进入 HTTP 隧道代理逻辑

TestDispatch_HTTPTunnel_ManagementAPI_Blocked
  - Host 命中 HTTP 隧道，path=/api/admin
  - 期望：进入 HTTP 隧道代理，不进入管理 API

TestDispatch_HTTPTunnel_Pending_Returns503
  - Host 命中 pending 状态隧道
  - 期望：503 Service Unavailable

TestDispatch_HTTPTunnel_Paused_Returns503
  - 期望：503

TestDispatch_HTTPTunnel_Stopped_Returns503
  - 期望：503

TestDispatch_HTTPTunnel_Error_Returns503
  - 期望：503

TestDispatch_HTTPTunnel_ClientOffline_Returns503
  - 隧道 active，但所属 Client 离线
  - 期望：503

TestDispatch_HTTPTunnel_ProxyFail_Returns502
  - 隧道 active，Client 在线，但反向代理拨号失败（上游拒绝连接）
  - 期望：502 Bad Gateway
```

#### 分组 3：Setup 阶段例外

```
TestDispatch_SetupPhase_AllowsManagementFrontend
  - 系统尚未初始化（setup 未完成）
  - 任意 Host，path=/ 或 /api/setup/*
  - 期望：放行进入 setup / 管理前端逻辑

TestDispatch_SetupPhase_AllowsStaticAssets
  - 系统尚未初始化，path=/assets/xxx.js
  - 期望：放行

TestDispatch_SetupPhase_BlocksOtherAPIs
  - 系统尚未初始化，path=/api/admin（非 setup 接口）
  - 期望：不放行（或由管理鉴权层处理）
```

#### 分组 4：生效管理 Host

```
TestDispatch_ManagementHost_AdminAPI
  - Host == effectiveManagementHost，path=/api/admin
  - 期望：进入管理 API

TestDispatch_ManagementHost_SSE
  - Host == effectiveManagementHost，path=/api/events
  - 期望：进入 SSE

TestDispatch_ManagementHost_StaticAssets
  - Host == effectiveManagementHost，path=/
  - 期望：返回管理前端

TestDispatch_NonManagementHost_NoTunnel_Returns404
  - Host 不命中任何 HTTP 隧道，也不等于 effectiveManagementHost
  - 期望：404 Not Found
```

#### 分组 5：`securityHeadersHandler` 仅包管理面

```
TestSecurityHeaders_OnManagementHost
  - 管理面响应应带 X-Frame-Options、X-Content-Type-Options 等

TestSecurityHeaders_NotOnHTTPTunnel
  - HTTP 隧道代理响应不应注入安全 header（由业务服务自己控制）
```

#### 分组 6：业务 WebSocket 可打通

```
TestDispatch_BusinessWebSocket_CanUpgrade
  - Host 命中活跃 HTTP 隧道，path=/ws/chat（普通业务 WS）
  - 期望：WebSocket 升级成功，双向通信可用
  - 注意：需要 mock 上游 WS 服务
```

#### 分组 7：SSE / chunked 响应即时 flush

```
TestDispatch_SSE_ImmediateFlush
  - Host 命中活跃 HTTP 隧道，上游是 SSE 服务
  - 期望：事件立即到达客户端，不被缓冲
```

## 测试辅助设施

为支持集成测试，建议在测试文件中实现以下 helper：

```go
// buildTestServer 构造一个最小化的 Server 实例用于测试
func buildTestServer(t *testing.T, opts ...testServerOption) *Server

// withHTTPTunnel 向测试 Server 注入一条 mock HTTP 隧道
func withHTTPTunnel(domain, status string, clientOnline bool) testServerOption

// withManagementHost 设置生效管理 Host
func withManagementHost(host string) testServerOption

// withSetupIncomplete 标记系统处于 setup 未完成状态
func withSetupIncomplete() testServerOption

// mockUpstreamHTTP 创建一个 mock 上游 HTTP 服务（用于 502 测试）
func mockUpstreamHTTP(t *testing.T, handler http.Handler) (addr string, cleanup func())
```

## 实现步骤

1. 新建 `internal/server/http_dispatch_test.go`
2. 实现上述所有测试及 helper
3. 确认可编译（运行时失败是预期行为）

## 验收标准

### 本批次完成标志

```bash
# 能编译通过
go build ./internal/server/...

# 新测试预期全部失败
go test ./internal/server/... -run TestDispatch -v
# 期望：FAIL（hostDispatchHandler 尚未实现）
```

### 最终验收（Batch 5 完成后）

```bash
go test ./internal/server/... -run TestDispatch -v
# 期望：全部 PASS
```

## 不引入的改动

- 不改任何实现代码
- 不改前端
- 不改 Client 侧
