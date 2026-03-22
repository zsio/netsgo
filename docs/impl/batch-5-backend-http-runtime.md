# Batch 5：后端 HTTP 域名隧道最小闭环实现

> 状态：待实现
> 前置条件：Batch 1、2、3、4 完成
> 估计影响文件：`internal/server/server.go`、`internal/server/http_tunnel.go`（扩展）、`internal/server/proxy.go`（扩展）

## 目标

实现后端 HTTP 域名隧道的完整运行时闭环：

1. `hostDispatchHandler`：按正确优先级分发所有 HTTP 请求
2. `httpTunnelProxy`：把命中 HTTP 隧道的请求通过 yamux stream 反向代理到 Client
3. `StartHTTPOnly()` 接口变更：返回类型从 `*http.ServeMux` 改为 `http.Handler`
4. `EffectiveServerAddr` 字段写入并参与冲突校验

## 实现细节

### 1. `hostDispatchHandler`

**文件**：`internal/server/server.go` 或 `internal/server/http_tunnel.go`

分发优先级（严格按顺序）：

```
1. isNetsgoControlRequest(r) -> controlHandler
2. isNetsgoDataRequest(r)    -> dataHandler
3. Host 命中 HTTP 隧道域名  -> httpTunnelHandler(tunnel)
4. 系统未初始化             -> setupHandler（只放行 /api/setup/* 和静态资源）
5. Host == effectiveManagementHost -> managementMux（现有管理面）
6. 其余                     -> 404
```

关键约束：

- 步骤 1/2 的识别必须同时满足 path 和 subprotocol 两个条件，缺一不可
- 步骤 3 在 Host 命中后不再看 path，一律转发
- 步骤 4 只在 `s.isSetupComplete() == false` 时生效
- 步骤 5 仅当 Host 精确等于 `effectiveManagementHost` 时才进入管理面
- `securityHeadersHandler` 只包裹步骤 5（管理面），不包裹步骤 3（HTTP 隧道）

伪代码结构：

```go
func (s *Server) hostDispatchHandler(w http.ResponseWriter, r *http.Request) {
    // 1. 内部 WS 通道优先
    if isNetsgoControlRequest(r) {
        s.handleControlWS(w, r)
        return
    }
    if isNetsgoDataRequest(r) {
        s.handleDataWS(w, r)
        return
    }

    // 2. HTTP 隧道域名路由
    host := canonicalHost(r.Host)
    if tunnel, ok := s.findHTTPTunnel(host); ok {
        s.serveHTTPTunnel(tunnel, w, r)
        return
    }

    // 3. Setup 阶段例外
    if !s.isSetupComplete() {
        s.managementMux.ServeHTTP(w, r)
        return
    }

    // 4. 生效管理 Host
    if host == s.effectiveManagementHost() {
        s.securityHeadersHandler(s.managementMux).ServeHTTP(w, r)
        return
    }

    // 5. 其余 404
    http.NotFound(w, r)
}
```

### 2. `findHTTPTunnel(host string) (*ProxyTunnel, bool)`

**文件**：`internal/server/http_tunnel.go`

查找当前运行时中 host 匹配的 HTTP 隧道（仅匹配 active 的）：

```go
func (s *Server) findHTTPTunnel(host string) (*ProxyTunnel, bool) {
    // 遍历所有在线 Client 的隧道
    // 找到 type=http && canonicalHost(tunnel.Config.Domain)==host
    // 返回找到的隧道
}
```

注意：`findHTTPTunnel` 只返回 active 隧道。pending/paused/stopped/error 和 Client 离线时走 `isHTTPDomainDeclared` 判断，返回 503。

### 3. `serveHTTPTunnel(tunnel *ProxyTunnel, w, r)`

**文件**：`internal/server/http_tunnel.go`

**可服务性判定**（必须同时满足）：

- `tunnel.Config.Status == active`
- 所属 Client 当前在线

不满足时返回 503：

```go
if !s.isHTTPTunnelServicable(tunnel) {
    http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
    return
}
```

**域名已声明但不可服务（503）**：

- pending/paused/stopped/error 的隧道 domain 命中时也要 503
- 只需检查 `collectDeclaredHTTPDomains` 结果，如果 host 在声明列表中但 `findHTTPTunnel` 找不到 active 隧道，返回 503

**实际代理**：

使用 `httputil.ReverseProxy`：

```go
proxy := &httputil.ReverseProxy{
    Director: func(req *http.Request) {
        req.URL.Scheme = "http"
        req.URL.Host = fmt.Sprintf("%s:%d", tunnel.Config.LocalIP, tunnel.Config.LocalPort)
        // 设置转发头
        applyForwardedHeaders(req, originalHost, proto, clientIP, isTrustedProxy)
    },
    Transport: &yamuxStreamTransport{
        client: client,
    },
    FlushInterval: -1, // 立即 flush，支持 SSE
    ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
        http.Error(w, "Bad Gateway", http.StatusBadGateway)
    },
}
```

**WebSocket 处理**：

检测到 `Upgrade: websocket` 时，不使用 `ReverseProxy`，改用 TCP relay：

```go
if isWebSocketUpgrade(r) {
    s.relayWebSocket(tunnel, w, r)
    return
}
```

### 4. `yamuxStreamTransport`

**文件**：`internal/server/http_tunnel.go`

实现 `http.RoundTripper` 接口，底层通过 yamux session 打开新 stream，再建立 HTTP/1.1 连接：

```go
type yamuxStreamTransport struct {
    session *yamux.Session
}

func (t *yamuxStreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    stream, err := t.session.Open()
    if err != nil {
        return nil, err
    }
    // 通过 stream 发送 HTTP 请求，读取响应
    return http.ReadResponse(bufio.NewReader(stream), req)
}
```

### 5. `StartHTTPOnly()` 接口变更

**文件**：`internal/server/server.go`

当前返回类型是 `*http.ServeMux`，需要改为 `http.Handler`：

```go
// 变更前
func (s *Server) StartHTTPOnly() *http.ServeMux

// 变更后
func (s *Server) StartHTTPOnly() http.Handler
```

返回的 handler 应是 `hostDispatchHandler`（包裹 TLS 等中间件后的最终 handler）。

**重要**：这是破坏性接口变更，必须同步更新所有测试中调用 `StartHTTPOnly()` 的地方。

涉及文件需要先用 grep 确认：

```bash
grep -rn 'StartHTTPOnly' internal/ cmd/
```

### 6. `EffectiveServerAddr` 字段写入

**文件**：`internal/server/admin_models.go`、`internal/server/admin_api.go`

- `Server` 结构体（或 AdminConfig）加 `EffectiveServerAddr string` 字段
- 启动时由 `effectiveManagementHost()` 计算并写入
- 参与 HTTP 隧道 domain 冲突校验
- `GET /api/admin/config` 返回

## 实现步骤

1. 在 `internal/server/http_tunnel.go` 中实现 `findHTTPTunnel`、`serveHTTPTunnel`、`yamuxStreamTransport`、`relayWebSocket`
2. 在 `internal/server/server.go` 中实现 `hostDispatchHandler`，替换现有顶层路由注册
3. 修改 `StartHTTPOnly()` 返回类型，同步更新所有调用点
4. 补充 `EffectiveServerAddr` 字段并写入
5. 运行 Batch 4 的集成测试，逐一修复直到全部通过

## 验收标准

```bash
# Batch 2 的单元测试全部通过
go test ./internal/server/... -run 'TestCanonical|TestValidate|TestEffective|TestDomain|TestIsNetsgo|TestTrustedProxy|TestOffline|TestAdminConfig' -v

# Batch 4 的集成测试全部通过
go test ./internal/server/... -run TestDispatch -v

# 全量测试无回归
go test ./internal/server/... -v
go test ./internal/client/... -v
go test ./pkg/... -v

# 构建通过
go build ./...
```

### 关键行为验收

1. 使用 curl 模拟请求命中 HTTP 隧道 domain，得到 503（Client 未连接）
2. 使用 curl 请求管理面 domain，得到管理面响应
3. 不带子协议的 `/ws/control` 请求不会进入控制通道
4. 带正确子协议的 `/ws/control` 可以在非管理域名上建立

## 不引入的改动

- 不改 Client 侧子协议发送（Batch 6 做）
- 不改 nginx/caddy E2E（Batch 7 做）
- 不改前端（Batch 8 做）
