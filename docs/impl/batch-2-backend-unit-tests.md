# Batch 2：规则层与管理配置契约测试（TDD 先行）

> 状态：已完成  
> 所属阶段：阶段 2（规则层）  
> 前置条件：Batch 1 完成  
> 估计影响文件：`internal/server/http_tunnel_test.go`（新建）、`internal/server/admin_api_test.go`（扩展）

## 目标

先把“纯规则”和“AdminConfig 接口契约”钉死，再进入实现。

本批次只做两类测试：

1. HTTP 域名隧道的纯规则测试
2. 管理配置读取 / dry-run / 实际保存的接口契约测试

本批次**不**处理离线 HTTP 隧道 `edit / pause / delete`、store-first、断线状态语义，这些统一放到阶段 3。

## 测试范围

### 一、`internal/server/http_tunnel_test.go`

需要覆盖以下分组：

#### 1. 域名归一化（`canonicalHost`）

```text
TestCanonicalHost
  - "example.com" -> "example.com"
  - "example.com:80" -> "example.com"
  - "example.com:8080" -> "example.com:8080"
  - "EXAMPLE.COM" -> "example.com"
  - "http://example.com" -> "example.com"
  - "https://example.com:443/path" -> "example.com"
  - "" -> ""
  - IPv6 地址 -> 正确处理
```

#### 2. 域名格式校验（`validateDomain`）

```text
TestValidateDomain
  - 合法："example.com"
  - 合法："sub.example.com"
  - 合法："a.b.c.example.com"
  - 非法：""
  - 非法："*.example.com"
  - 非法："localhost"
  - 非法：含空格
  - 非法：含 scheme
  - 非法：含路径
  - 非法：纯 IP
```

#### 3. 生效管理 Host 推导（`effectiveManagementHost`）

```text
TestEffectiveManagementHost
  - env `NETSGO_SERVER_ADDR` 优先级最高
  - 无 env 时使用持久化 `server_addr`
  - 都没有时从 `listenAddr` 推导
  - 非标准端口保留
  - 标准端口 80/443 去掉端口后缀
```

#### 4. 管理 Host 冲突

```text
TestDomainConflictWithManagementHost
  - domain == effectiveManagementHost -> 冲突
  - domain != effectiveManagementHost -> 不冲突
  - 大小写不敏感
```

#### 5. HTTP 隧道之间的域名冲突

```text
TestDomainConflictBetweenTunnels
  - 同一 client 重复 domain -> 冲突
  - 不同 client 重复 domain -> 冲突
  - pending / paused / stopped / error 仍参与冲突检测
  - 已删除的不参与冲突检测
  - 大小写不敏感
```

#### 6. 内部 WS 子协议识别 helper

```text
TestIsNetsgoControlRequest
  - /ws/control + netsgo-control.v1 -> true
  - /ws/control + 缺失子协议 -> false
  - /ws/control + 错误子协议 -> false
  - /ws/data + netsgo-control.v1 -> false

TestIsNetsgoDataRequest
  - /ws/data + netsgo-data.v1 -> true
  - /ws/data + 缺失子协议 -> false
  - /ws/data + 错误子协议 -> false
  - /ws/control + netsgo-data.v1 -> false
```

#### 7. trusted proxy 下的转发头计算

```text
TestTrustedProxyHeaders
  - 直连：X-Forwarded-For 由 NetsGo 直接设置
  - 可信代理：X-Forwarded-For 追加客户端 IP
  - 不可信代理：外部传入的 X-Forwarded-* 不能被直接信任
  - Host 保留为原始 domain
  - X-Forwarded-Host 为原始 domain
  - X-Forwarded-Proto 为 NetsGo 计算出的外部 scheme
```

### 二、`internal/server/admin_api_test.go`

#### 8. `GET /api/admin/config`

```text
TestAdminConfigResponse
  - 返回 `server_addr`
  - 返回 `effective_server_addr`
  - 返回 `server_addr_locked`
  - 无 env 时 `server_addr_locked=false`
  - 设置 `NETSGO_SERVER_ADDR` 后 `server_addr_locked=true`
```

#### 9. `PUT /api/admin/config?dry_run=true`

```text
TestAdminConfigDryRun
  - dry_run 始终返回 200
  - 返回 `affected_tunnels`
  - 返回 `conflicting_http_tunnels`
  - 冲突时 `conflicting_http_tunnels` 非空
  - 无冲突时返回空数组
```

#### 10. `PUT /api/admin/config` 实际保存路径

```text
TestAdminConfigUpdateRejectsWhenLocked
  - `server_addr_locked=true` 且请求试图修改 `server_addr`
  - 期望：409 Conflict
  - 返回结构化冲突信息

TestAdminConfigUpdateRejectsWhenHTTPDomainConflicts
  - 新 `server_addr` host 与已有 HTTP 隧道 domain 冲突
  - 期望：409 Conflict
  - 返回 `conflicting_http_tunnels`
```

## 实施要求

### 允许的红测试

本批次允许第一次 `go test` 失败表现为：

- 断言失败
- 编译失败（例如 `undefined: canonicalHost`）

这两种都属于正常的 TDD 红阶段。

### 不允许的做法

- 不要在测试文件里写临时空实现桩
- 不要用 `t.Skip("stub")` 占位
- 不要为了让测试“先编译过”而引入伪实现

## 实现步骤

1. 新建 `internal/server/http_tunnel_test.go`
2. 扩展 `internal/server/admin_api_test.go`
3. 先跑针对性测试，确认进入红阶段
4. 不改生产代码，直接进入 Batch 3 做实现

## 验收标准

### 本批次完成标志

```bash
go test ./internal/server/... -run TestCanonicalHost -v
go test ./internal/server/... -run TestValidateDomain -v
go test ./internal/server/... -run TestAdminConfig -v
```

期望：

- 可以是断言失败
- 也可以是 `undefined:` 编译失败
- 但不应存在 stub / skip

### 最终验收（Batch 3 完成后）

```bash
go test ./internal/server/... -run 'TestCanonical|TestValidate|TestEffective|TestDomain|TestIsNetsgo|TestTrustedProxy|TestAdminConfig' -v
```

## 不引入的改动

- 不改 `server.go` 的 tunnel CRUD handler
- 不改 `tunnel_manager.go` 的离线 store-first 逻辑
- 不改入口路由
- 不改 Client 侧
- 不改前端
