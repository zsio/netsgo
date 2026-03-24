# 阶段 3：生命周期与离线语义

> 状态：已完成  
> 所属阶段：阶段 3  
> 前置条件：阶段 2 完成  
> 估计影响文件：`internal/server/server.go`、`internal/server/tunnel_manager.go`、`internal/server/store.go`、`internal/server/server_test.go`、`internal/server/tunnel_manager_test.go`

## 目标

在进入最终入口分发和 HTTP 代理运行时之前，先把“隧道配置状态”和“当前是否可服务”这两件事拆清楚。

本阶段专门解决：

1. 离线既有 HTTP 隧道的 `edit / pause / delete`
2. 离线 `resume / stop` 的显式拒绝
3. store-first 配置真值
4. `domain` 在 runtime / store / placeholder 之间的一致性
5. 断线 / 重连 / 重启时，不把配置状态误写成用户操作状态

## 为什么单独做这一阶段

如果跳过这一步，阶段 4 会同时背三类风险：

- 路由分发是否正确
- HTTP 代理是否正确
- 生命周期语义是否正确

这样测试会很难读，失败原因也会混在一起。  
把这一层先收口，阶段 4 才能只关注“请求进来以后怎么分发和代理”。

## 本阶段覆盖的行为

### 1. 离线已存在 HTTP 隧道允许 `edit / pause / delete`

要求：

- 目标 tunnel 已存在于 store
- 对应 client 当前不在线
- `edit / pause / delete` 仍可执行
- 改动立即写入 store

### 2. 离线 `resume / stop` 明确拒绝

要求：

- 不做隐式排队
- 不偷偷记一个“等 client 上线再执行”
- API 返回明确错误，建议继续使用 `409 Conflict`

### 3. store-first

对“既有 HTTP 隧道”的后台管理动作，配置真值在 store：

- 在线时：store 更新后同步 runtime
- 离线时：只更新 store

create 仍保持当前约束：

- 新建 HTTP 隧道仍要求目标 client 在线

### 4. 域名声明的生命周期

需要验证这些状态变化不会把域名占用搞乱：

- 创建后 domain 被声明
- 编辑后旧 domain 释放、新 domain 生效
- 删除后 domain 释放
- `paused / stopped / error` 占位仍保留 domain

### 5. 断线 / 重启的状态语义

要求：

- client 断线不会把 `active` 自动写成 `paused`
- 服务端重启恢复占位记录时不丢 `domain`
- store 中保存的是配置状态，不是临时会话状态

## 推荐测试

### `internal/server/server_test.go`

```text
TestOfflineHTTPTunnel_Update_StoreFirst
TestOfflineHTTPTunnel_Pause_StoreFirst
TestOfflineHTTPTunnel_Delete_StoreFirst
TestOfflineHTTPTunnel_Resume_Rejected
TestOfflineHTTPTunnel_Stop_Rejected
TestClientDisconnect_DoesNotRewritePaused
```

### `internal/server/tunnel_manager_test.go`

```text
TestHTTPDomainReservation_Create
TestHTTPDomainReservation_Update_ReleasesOldDomain
TestHTTPDomainReservation_Delete_ReleasesDomain
TestPlaceholderPreservesDomainAcrossStatuses
```

> 请求级的 `503/502/404` 验证放到阶段 4；本阶段只先把状态和持久化语义收口。

## 建议实现方式

### 1. 不改 create 语义

`handleCreateTunnel` 保持在线 client 前提，不要在这一阶段扩展“离线创建”。

### 2. 对既有 HTTP 隧道新增一个小的 store 查询入口

目标不是大重构，而是给后台动作一个明确分支：

- 优先查 live client
- live client 不存在时，再查 store 中是否已有这条 HTTP 隧道

只要能做到这一点，就足够支撑离线 `edit / pause / delete`。

边界要求：

- 若 `client ID` 不存在，直接返回 `404`
- 若 `tunnel name` 不存在，直接返回 `404`
- 只有“client 当前不在线，但 store 中存在既有 HTTP 隧道”时，才进入离线处理分支

### 3. API handler 做最小分流

建议在 `handleUpdateTunnel` / `handlePauseTunnel` / `handleDeleteTunnel` / `handleResumeTunnel` / `handleStopTunnel` 中做最小必要分支：

- 在线：走现有 runtime 路径
- 离线且是既有 HTTP 隧道：走 store-first 路径
- 离线且动作为 `resume / stop`：显式返回错误

不建议在这一阶段为了“优雅”先抽一大层新管理器。

### 4. 更新保持原子

涉及旧域名释放 / 新域名声明切换时：

- 校验和写入放在同一临界区或同一条明确事务路径
- 避免出现短暂双占用或空窗

## 实施步骤

1. 先补离线动作和域名生命周期测试
2. 跑针对性测试，确认失败点来自“离线路径未支持”而不是运行时代理
3. 在 `server.go` 增加离线既有 HTTP 隧道的最小查找分支
4. 在 `tunnel_manager.go` 补 store-first 更新 / 暂停 / 删除逻辑
5. 明确离线 `resume / stop` 的错误返回
6. 回归 `domain` 在 placeholder / restore / store 中的一致性

## 验收标准

```bash
go test ./internal/server/... -run 'TestOffline|TestLifecycle|TestPlaceholder|TestStoreFirst' -v
go test ./internal/server/... -v
```

## 不引入的改动

- 不做 `hostDispatchHandler`
- 不做 HTTP 代理
- 不做业务 WebSocket relay
- 不做 SSE flush
- 不改 Client 侧
- 不改前端
