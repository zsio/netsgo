# Batch 3：后端核心规则实现

> 状态：待实现
> 前置条件：Batch 1、Batch 2 完成
> 估计影响文件：`internal/server/http_tunnel.go`（新建）、`internal/server/tunnel_manager.go`、`internal/server/admin_api.go`、`internal/server/admin_models.go`

## 目标

实现 Batch 2 中所有失败测试对应的生产代码，使测试全部通过。本批次不涉及路由层、前端、Client 侧。

## 要实现的内容

### 1. 新建 `internal/server/http_tunnel.go`

这个文件承载所有 HTTP 域名隧道的纯规则逻辑（不含路由、不含 handler）。

#### 1.1 子协议常量（与 `pkg/protocol/` 共享）

在 `pkg/protocol/types.go` 中补充：

```go
const (
    WSSubProtocolControl = "netsgo-control.v1"
    WSSubProtocolData    = "netsgo-data.v1"
)
```

#### 1.2 `canonicalHost(addr string) string`

从任意形式的地址提取归一化的 host（小写，去掉标准端口和 scheme）：

- 输入 `"example.com"` → `"example.com"`
- 输入 `"example.com:80"` → `"example.com"`
- 输入 `"example.com:8080"` → `"example.com:8080"`
- 输入 `"EXAMPLE.COM"` → `"example.com"`
- 输入 `"http://example.com"` → `"example.com"`
- 输入 `"https://example.com:443/path"` → `"example.com"`

实现要点：先 `url.Parse`，再 `net.SplitHostPort`，最后 `strings.ToLower`，标准端口（80/443）去掉端口后缀。

#### 1.3 `validateDomain(domain string) error`

校验域名合法性：

- 不允许空字符串
- 不允许 `*.` 开头（泛域名）
- 不允许单标签（无点，如 `localhost`）
- 不允许带 scheme
- 不允许带路径
- 不允许纯 IP
- 允许多级子域名

#### 1.4 `isNetsgoControlRequest(r *http.Request) bool`

判断请求是否是合法的 NetsGo 控制通道握手：

```go
func isNetsgoControlRequest(r *http.Request) bool {
    return r.URL.Path == "/ws/control" &&
        containsProtocol(r.Header, protocol.WSSubProtocolControl)
}
```

#### 1.5 `isNetsgoDataRequest(r *http.Request) bool`

同上，检查 `/ws/data` + `netsgo-data.v1`。

#### 1.6 `effectiveManagementHost(cfg *AdminConfig, listenAddr string) string`

按优先级推导生效管理 Host：

1. 环境变量 `NETSGO_SERVER_ADDR`
2. `cfg.ServerAddr`
3. 从 `listenAddr` 推导

返回 `canonicalHost` 归一化后的结果。

#### 1.7 `isServerAddrLocked() bool`

检查 `NETSGO_SERVER_ADDR` 环境变量是否设置了非空值。

#### 1.8 `collectDeclaredHTTPDomains(server *Server) map[string]string`

收集所有已声明的 HTTP 隧道域名（key = domain，value = tunnelName），来源：

- 运行时 `server.clients` 中所有在线 client 的 `type=http` 隧道
- `TunnelStore` 中所有持久化的 `type=http` 隧道（含各种状态）

注意去重（运行时与持久化可能重叠）。

#### 1.9 `checkDomainConflict(domain, excludeName, excludeClientID string, server *Server) error`

校验新 domain 是否与已有 HTTP 隧道或生效管理 Host 冲突。

- 与生效管理 Host 冲突 → `"server_addr_conflict"`
- 与其他 HTTP 隧道冲突 → `"http_tunnel_conflict"`
- `excludeName + excludeClientID` 用于 update 时排除自身

#### 1.10 `computeForwardedHeaders(r *http.Request, domain string, isTrustedProxy bool) http.Header`

计算应写入上游请求的转发头：

- `Host`：原始 domain
- `X-Forwarded-Host`：原始 domain
- `X-Forwarded-Proto`：外部 scheme
- `X-Forwarded-For`：追加当前客户端 IP（可信代理模式），或直接设置（非可信代理模式）

### 2. 扩展 `internal/server/tunnel_manager.go`

#### 2.1 创建 HTTP 隧道的校验逻辑

在 `CreateTunnel` / `prepareProxyTunnel` 中加入：

- 对 `type=http` 的请求调用 `validateDomain`
- 调用 `checkDomainConflict`
- 若 `type=http` 且请求体带了 `remote_port`，直接覆盖为 `0`

#### 2.2 离线 HTTP 隧道的 edit / pause / delete

补充当 Client 离线时允许的操作逻辑：

- `edit`（type=http）：更新 store，新 domain 立即持久化
- `pause`（type=http）：写 store status = paused
- `delete`（type=http）：从 store 移除
- `resume` / `stop` 离线时：返回明确错误

### 3. 扩展 `internal/server/admin_models.go`

在 `AdminConfig` 响应结构中补充字段：

```go
type AdminConfigResponse struct {
    // 已有字段...
    ServerAddr          string `json:"server_addr"`
    EffectiveServerAddr string `json:"effective_server_addr"`
    ServerAddrLocked    bool   `json:"server_addr_locked"`
}
```

在 dry-run 响应结构中补充：

```go
type AdminConfigDryRunResponse struct {
    // 已有字段...
    ConflictingHTTPTunnels []string `json:"conflicting_http_tunnels"`
}
```

### 4. 扩展 `internal/server/admin_api.go`

- `GET /api/admin/config`：填充 `EffectiveServerAddr` 和 `ServerAddrLocked`
- `PUT /api/admin/config?dry_run=true`：返回 `ConflictingHTTPTunnels`
- `PUT /api/admin/config`（实际保存）：校验 `ConflictingHTTPTunnels`，冲突时返回 `409 Conflict`

## 实现步骤

1. 在 `pkg/protocol/types.go` 中补充 `WSSubProtocolControl` 和 `WSSubProtocolData` 常量
2. 新建 `internal/server/http_tunnel.go`，实现上述 1.1-1.10 所有函数
3. 修改 `internal/server/tunnel_manager.go`，加入 HTTP 隧道的校验和离线操作逻辑
4. 修改 `internal/server/admin_models.go`，补充新字段
5. 修改 `internal/server/admin_api.go`，填充新逻辑
6. 运行测试，使 Batch 2 中的所有测试用例全部通过

## 验收标准

```bash
# 全部后端单元测试通过（含 Batch 2 写的测试）
go test ./internal/server/... -v
go test ./pkg/... -v

# 无编译错误
go build ./...
```

### 关键测试用例必须通过

- `TestCanonicalHost`
- `TestValidateDomain`
- `TestEffectiveManagementHost`
- `TestDomainConflictWithManagementHost`
- `TestDomainConflictBetweenTunnels`
- `TestIsNetsgoControlRequest`
- `TestIsNetsgoDataRequest`
- `TestDomainPreservedInPlaceholder`
- `TestTrustedProxyHeaders`
- `TestOfflineTunnelEdit`
- `TestAdminConfigResponse`
- `TestAdminConfigDryRun`

## 不引入的改动

- 不改路由层（`server.go` 的路由注册）
- 不改 Client 侧代码
- 不改前端
- 不实现 `hostDispatchHandler`
