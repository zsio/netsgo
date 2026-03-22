# Batch 2：后端纯规则单元测试（TDD 先行）

> 状态：待实现
> 前置条件：Batch 1 完成
> 估计影响文件：`internal/server/http_tunnel_test.go`（新建）、`internal/server/admin_api_test.go`（扩展）

## 目标

按照 TDD 顺序，先把所有后端规则以失败测试的形式钉死，再写实现。本批次只写测试文件，不改实现代码（测试全部应当失败，这是预期行为）。

## 为什么先写测试

- 先把规则边界钉死，避免实现阶段边写边改语义
- 测试失败是阶段性正常状态，标志着规则已明确，尚待实现
- 测试通过后才是 Batch 3 的完成标志

## 需要新建的测试文件

### `internal/server/http_tunnel_test.go`

需要覆盖以下测试分组：

#### 1. 域名归一化（`canonicalHost`）

```
TestCanonicalHost
  - 输入 "example.com" -> "example.com"
  - 输入 "example.com:80" -> "example.com"
  - 输入 "example.com:8080" -> "example.com:8080"
  - 输入 "EXAMPLE.COM" -> "example.com"（大写转小写）
  - 输入 "http://example.com" -> "example.com"
  - 输入 "https://example.com:443/path" -> "example.com"
  - 输入 "" -> ""（空字符串安全）
  - 输入带 IPv6 地址 -> 正确处理
```

#### 2. 域名格式校验

```
TestValidateDomain
  - 合法："example.com"
  - 合法："sub.example.com"
  - 合法："a.b.c.example.com"
  - 非法："" （空）
  - 非法："*.example.com"（泛域名，本期不支持）
  - 非法："localhost"（单标签，不允许）
  - 非法：含空格
  - 非法：含 scheme（"http://example.com"）
  - 非法：含路径（"example.com/path"）
  - 非法：纯 IP（"192.168.1.1"）
```

#### 3. 生效管理 Host 推导（`effectiveManagementHost`）

```
TestEffectiveManagementHost
  - 显式 server_addr 设置时，以 canonicalHost(server_addr) 为准
  - server_addr 未设置时，以 ListenAddr 推导
  - 环境变量 NETSGO_SERVER_ADDR 优先级高于配置文件
  - 带端口的 server_addr 保留端口（非标准端口）
  - 标准端口（80/443）的 server_addr 去掉端口后缀
```

#### 4. 生效管理 Host 与 HTTP 隧道域名冲突校验

```
TestDomainConflictWithManagementHost
  - HTTP 隧道 domain == effectiveManagementHost -> 冲突，返回错误
  - HTTP 隧道 domain != effectiveManagementHost -> 不冲突
  - 大小写不敏感比较（"EXAMPLE.COM" vs "example.com" -> 冲突）
```

#### 5. 两个 HTTP 隧道之间的域名冲突

```
TestDomainConflictBetweenTunnels
  - 同一 client 两条 tunnel 使用相同 domain -> 冲突
  - 不同 client 两条 tunnel 使用相同 domain -> 冲突（域名是全局唯一键）
  - pending / paused / stopped / error 状态的 tunnel 仍参与冲突检测
  - 已删除的 tunnel 不参与冲突检测
  - 大小写不敏感
```

#### 6. 内部 WS 子协议识别 helper

```
TestIsNetsgoControlRequest
  - path=/ws/control + Sec-WebSocket-Protocol: netsgo-control.v1 -> true
  - path=/ws/control + 缺失子协议 -> false
  - path=/ws/control + 错误子协议 -> false
  - path=/ws/data + Sec-WebSocket-Protocol: netsgo-control.v1 -> false（path 和协议不匹配）

TestIsNetsgoDataRequest
  - path=/ws/data + Sec-WebSocket-Protocol: netsgo-data.v1 -> true
  - path=/ws/data + 缺失子协议 -> false
  - path=/ws/data + 错误子协议 -> false
  - path=/ws/control + Sec-WebSocket-Protocol: netsgo-data.v1 -> false
```

#### 7. 域名在占位状态下仍被保留（持久化一致性）

```
TestDomainPreservedInPlaceholder
  - 创建 HTTP tunnel 后 status=pending，domain 不为空
  - 隧道转为 paused 后，domain 仍存在于 store
  - 隧道转为 stopped 后，domain 仍存在于 store
  - 隧道转为 error 后，domain 仍存在于 store
  - 服务端重启后从 store 恢复，domain 仍正确
```

#### 8. trusted proxy 下 HTTP 头计算规则

```
TestTrustedProxyHeaders
  - 直连情况下：X-Forwarded-For 设为客户端真实 IP
  - 可信代理情况下：X-Forwarded-For 追加客户端 IP，而不是覆盖
  - 不可信代理：外部传入的 X-Forwarded-* 不能直接当作事实传给上游
  - Host 保留为外部访问的原始 domain
  - X-Forwarded-Host 设为原始 domain
  - X-Forwarded-Proto 基于 NetsGo 计算的外部 scheme
```

### `internal/server/admin_api_test.go`（扩展部分）

#### 9. 离线 HTTP 隧道的 edit / pause / delete 语义

```
TestOfflineTunnelEdit
  - Client 离线时，edit HTTP tunnel -> 允许，新 domain 立即写入 store
  - Client 离线时，pause HTTP tunnel -> 允许，status 写为 paused
  - Client 离线时，delete HTTP tunnel -> 允许，持久化记录移除
  - Client 离线时，resume HTTP tunnel -> 不允许（返回错误）
  - Client 离线时，stop HTTP tunnel -> 不允许（返回错误）
```

#### 10. `GET /api/admin/config` 返回新增字段

```
TestAdminConfigResponse
  - 返回 server_addr 字段
  - 返回 effective_server_addr 字段
  - 返回 server_addr_locked 字段
  - 无环境变量时 server_addr_locked = false
  - 设置环境变量 NETSGO_SERVER_ADDR 后 server_addr_locked = true
```

#### 11. 修改 server_addr 的 dry-run 扩展

```
TestAdminConfigDryRun
  - dry_run=true 始终返回 200
  - 返回 conflicting_http_tunnels 字段（新增）
  - 新 server_addr 的 host 与已有 HTTP 隧道 domain 冲突时，conflicting_http_tunnels 非空
  - 无冲突时 conflicting_http_tunnels 为空数组
```

## 实现步骤

1. 新建 `internal/server/http_tunnel_test.go`，写入上述所有测试用例
2. 扩展 `internal/server/admin_api_test.go`，补充 9-11 的测试用例
3. 确认所有新测试均能编译通过（但运行时失败，这是预期）

## 验收标准

### 阶段性验收（本批次完成时）

```bash
# 所有测试能编译通过
go build ./internal/server/...

# 新增测试按预期失败（不是编译错误，而是 FAIL）
go test ./internal/server/... -run TestCanonicalHost -v
go test ./internal/server/... -run TestValidateDomain -v
go test ./internal/server/... -run TestIsNetsgoControlRequest -v
# 期望：FAIL（函数/类型尚未实现）
```

### 最终验收（Batch 3 完成后回来验证）

```bash
# 全部测试通过
go test ./internal/server/... -v
```

## 不引入的改动

- 不改任何实现代码（`server.go`、`tunnel_manager.go`、`proxy.go` 等）
- 不改协议定义
- 不改前端
- 不新增生产代码文件
