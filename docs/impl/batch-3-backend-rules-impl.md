# Batch 3：规则层实现与管理配置保存语义

> 状态：待实现  
> 所属阶段：阶段 2（规则层）  
> 前置条件：Batch 1、Batch 2 完成  
> 估计影响文件：`internal/server/http_tunnel.go`（新建）、`internal/server/proxy.go`、`internal/server/admin_api.go`、`internal/server/admin_models.go`、`pkg/protocol/types.go`

## 目标

实现 Batch 2 的失败测试，使规则层和 AdminConfig 契约稳定落地。

本批次只解决：

1. 域名与管理 Host 规则
2. 内部 WS 子协议识别 helper
3. HTTP 创建时的 domain 校验 / 冲突校验 / `remote_port=0`
4. 管理配置读取、dry-run、实际保存的锁定 / 冲突语义

本批次**不**处理：

- 离线 HTTP 隧道 `edit / pause / delete`
- store-first
- 断线 / 重启下的生命周期一致性
- 最终入口分发和 HTTP 代理运行时

这些统一放到阶段 3 和阶段 4。

## 要实现的内容

### 1. 共享子协议常量

**文件**：`pkg/protocol/types.go`

新增：

```go
const (
    WSSubProtocolControl = "netsgo-control.v1"
    WSSubProtocolData    = "netsgo-data.v1"
)
```

这里只新增常量，不改消息体结构。

### 2. 新建 `internal/server/http_tunnel.go`

承载纯规则 helper，不放路由和代理执行逻辑。

建议包含以下函数：

#### 2.1 `canonicalHost(addr string) string`

- 去 scheme
- 去 path
- 小写化
- 去除标准端口 80/443
- 保留非标准端口

#### 2.2 `validateDomain(domain string) error`

- 不允许空
- 不允许泛域名
- 不允许单标签
- 不允许 scheme / path
- 不允许纯 IP

#### 2.3 `containsProtocol(h http.Header, expected string) bool`

- 统一解析 `Sec-WebSocket-Protocol`
- 避免 control/data/helper 各写一套 split 逻辑

#### 2.4 `isNetsgoControlRequest(r *http.Request) bool`

- `/ws/control` + `netsgo-control.v1`

#### 2.5 `isNetsgoDataRequest(r *http.Request) bool`

- `/ws/data` + `netsgo-data.v1`

#### 2.6 `effectiveManagementHost(cfg *ServerConfig, listenAddr string) string`

优先级：

1. `NETSGO_SERVER_ADDR`
2. 持久化 `server_addr`
3. `listenAddr`

#### 2.7 `isServerAddrLocked() bool`

- 判断 `NETSGO_SERVER_ADDR` 是否设置为非空值

#### 2.8 `collectDeclaredHTTPDomains(server *Server) map[string]string`

- 扫描 runtime + store
- 只收 `type=http`
- 用于全局冲突检查

#### 2.9 `checkDomainConflict(domain, excludeName, excludeClientID string, server *Server) error`

- 与 `effectiveManagementHost` 冲突 -> `server_addr_conflict`
- 与其他 HTTP 隧道冲突 -> `http_tunnel_conflict`

#### 2.10 `computeForwardedHeaders(...)`

- 统一计算 `Host`
- `X-Forwarded-Host`
- `X-Forwarded-Proto`
- `X-Forwarded-For`

### 3. 创建 HTTP 隧道时的规则收口

**文件**：`internal/server/proxy.go`

在创建路径里加入 HTTP 专属规则：

- `type=http` 时调用 `validateDomain`
- `type=http` 时调用 `checkDomainConflict`
- `type=http` 时把 `remote_port` 归零

这里的目标是确保“HTTP 创建语义”稳定，不把它继续带入端口语义。

### 4. `GET /api/admin/config`

**文件**：`internal/server/admin_api.go`、`internal/server/admin_models.go`

返回：

- `server_addr`
- `allowed_ports`
- `effective_server_addr`
- `server_addr_locked`

建议使用单独的 response 结构或 handler 组装的 map，避免把计算值写回持久化结构。

### 5. `PUT /api/admin/config?dry_run=true`

返回：

- `affected_tunnels`
- `conflicting_http_tunnels`

规则：

- 始终 `200 OK`
- 不做持久化

### 6. `PUT /api/admin/config`

实际保存路径必须和 dry-run 使用同一套判断逻辑：

- `server_addr_locked=true` 且请求修改 `server_addr` -> `409 Conflict`
- `conflicting_http_tunnels` 非空 -> `409 Conflict`
- 返回结构化冲突信息，而不是单一字符串

## 实现步骤

1. 在 `pkg/protocol/types.go` 增加子协议常量
2. 新建 `internal/server/http_tunnel.go`，实现纯规则 helper
3. 修改 `internal/server/proxy.go`，收口 HTTP 创建时的校验与 `remote_port=0`
4. 修改 `internal/server/admin_api.go`，统一 `GET` / `dry_run` / `save` 的返回与冲突逻辑
5. 如有必要，在 `internal/server/admin_models.go` 补充响应结构
6. 明确 tunnel mutation 的最小 `409` JSON 契约，并补对应 handler 级测试
7. 跑 Batch 2 的测试直到通过

## 最小 `409` JSON 契约

### 一、HTTP 隧道 create / update

适用路由：

- `POST /api/clients/{id}/tunnels`
- `PUT /api/clients/{id}/tunnels/{name}`

发生 HTTP 域名冲突时，返回：

```json
{
  "success": false,
  "error": "human readable message",
  "error_code": "server_addr_conflict"
}
```

或：

```json
{
  "success": false,
  "error": "human readable message",
  "error_code": "http_tunnel_conflict"
}
```

要求：

- HTTP 状态码为 `409 Conflict`
- `error_code` 至少支持：
  - `server_addr_conflict`
  - `http_tunnel_conflict`

### 二、离线 client 上不允许的动作

适用路由：

- `PUT /api/clients/{id}/tunnels/{name}/resume`
- `PUT /api/clients/{id}/tunnels/{name}/stop`

当目标 client 不在线且动作不允许离线执行时，返回：

```json
{
  "error": "human readable message",
  "error_code": "client_offline_action_not_allowed"
}
```

要求：

- HTTP 状态码为 `409 Conflict`

### 三、Admin config 保存冲突

适用路由：

- `PUT /api/admin/config`

当保存的 `server_addr` 与 HTTP 域名冲突时，返回：

```json
{
  "error": "human readable message",
  "conflicting_http_tunnels": ["tunnel-a", "tunnel-b"]
}
```

要求：

- HTTP 状态码为 `409 Conflict`
- `conflicting_http_tunnels` 与 dry-run 保持同字段名

## 验收标准

```bash
go test ./internal/server/... -run 'TestCanonical|TestValidate|TestEffective|TestDomain|TestIsNetsgo|TestTrustedProxy|TestAdminConfig' -v
go test ./pkg/... -v
go build ./...
```

## 明确移出本批次的内容

- 不改 `readClientFromPath`
- 不改 `handleUpdateTunnel` / `handlePauseTunnel` / `handleDeleteTunnel` 的离线路径
- 不做 store-first
- 不做 `hostDispatchHandler`
- 不做 HTTP 代理运行时
- 不做 Client 侧子协议发送
