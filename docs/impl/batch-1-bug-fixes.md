# Batch 1：修复前置 Bug

> 状态：待实现
> 前置条件：无
> 估计影响文件：`internal/server/proxy.go`、`internal/server/tunnel_manager.go`

## 目标

当前仓库中存在三处已确认的 Bug，如果不先修复，后续所有 HTTP 隧道的运行时路径都会走错分支或丢失 `domain` 字段。本批次只修复这三处 Bug，不引入任何新功能。

## 背景与问题说明

### Bug 1：`activatePreparedTunnel` —— HTTP 类型错误走 TCP 监听分支

**文件**：`internal/server/proxy.go`

**现状**：`activatePreparedTunnel` 里只有 UDP 和 TCP（`net.Listen`）两个分支，没有 HTTP 分支。HTTP 隧道会错误地走进 TCP 分支，尝试在公网端口上 `net.Listen`，这与 HTTP 域名隧道的语义完全不符（HTTP 隧道不应绑定公网端口，而应挂入 HTTP 路由层）。

**修复方案**：在 UDP 分支之前，加一个 `type=http` 的 early-return，直接返回 `nil`（HTTP 隧道不需要在此处做任何监听，路由层后续会处理）。

```go
// 修复位置：activatePreparedTunnel 函数开头
if tunnel.Config.Type == protocol.ProxyTypeHTTP {
    // HTTP 隧道不绑定公网端口，通过 HTTP 路由层分发
    return nil
}
```

### Bug 2：`prepareProxyTunnel` —— 构造 `ProxyConfig` 时未复制 `Domain`

**文件**：`internal/server/tunnel_manager.go`

**现状**：`prepareProxyTunnel` 函数在从 `ProxyNewRequest` 构造 `ProxyConfig` 时，没有把 `req.Domain` 赋值进去，导致运行时的 `ProxyTunnel.Config.Domain` 始终为空字符串。此问题同时影响 `restoreManagedTunnel` 和 `resumeManagedTunnel` 所走的路径（它们都经过 `prepareProxyTunnel`）。

**修复方案**：在构造 `ProxyConfig` 时补全 `Domain` 字段：

```go
// 修复位置：prepareProxyTunnel 内的 ProxyConfig 构造
Config: protocol.ProxyConfig{
    Name:       req.Name,
    Type:       req.Type,
    LocalIP:    req.LocalIP,
    LocalPort:  req.LocalPort,
    RemotePort: req.RemotePort,
    Domain:     req.Domain,  // 补全
    ClientID:   client.ClientID,
    Status:     protocol.ProxyStatusPending,
},
```

### Bug 3：`restoreTunnels` —— 占位记录构造时未恢复 `Domain`

**文件**：`internal/server/tunnel_manager.go`

**现状**：`restoreTunnels` 在构造 paused/stopped/error 状态及端口不在白名单的占位 `ProxyConfig` 时，都没有把 `StoredTunnel` 里已持久化的 `st.Domain` 带过去，导致服务端重启后 HTTP 隧道的域名声明丢失（无法正确路由、无法做冲突校验）。

**修复方案**：在两处内联构造里补全 `Domain: st.Domain`：

```go
// 修复位置一：paused/stopped/error 占位构造
protocol.ProxyConfig{
    Name:       st.Name,
    Type:       st.Type,
    LocalIP:    st.LocalIP,
    LocalPort:  st.LocalPort,
    RemotePort: st.RemotePort,
    Domain:     st.Domain,  // 补全
    ClientID:   st.ClientID,
    Status:     st.Status,
}

// 修复位置二：端口不在白名单的 error 占位构造
protocol.ProxyConfig{
    Name:       st.Name,
    Type:       st.Type,
    LocalIP:    st.LocalIP,
    LocalPort:  st.LocalPort,
    RemotePort: st.RemotePort,
    Domain:     st.Domain,  // 补全
    ClientID:   st.ClientID,
    Status:     protocol.ProxyStatusError,
    Error:      "port not in allowed range",
}
```

## 实现步骤

1. 修改 `internal/server/proxy.go`：在 `activatePreparedTunnel` 函数中加 `type=http` early-return
2. 修改 `internal/server/tunnel_manager.go`：在 `prepareProxyTunnel` 中补全 `Domain: req.Domain`
3. 修改 `internal/server/tunnel_manager.go`：在 `restoreTunnels` 两处占位构造中补全 `Domain: st.Domain`

## 验收标准

### 自动化验收

```bash
# 跑全量后端测试（确保没有回归）
go test ./internal/server/... -v
go test ./internal/client/... -v
go test ./pkg/... -v
```

### 手动验收检查点

1. **Bug 1 验证**：grep 确认 `activatePreparedTunnel` 里 HTTP early-return 已加入
   ```bash
   grep -n 'ProxyTypeHTTP' internal/server/proxy.go
   ```

2. **Bug 2 验证**：grep 确认 `prepareProxyTunnel` 里 `Domain: req.Domain` 已出现
   ```bash
   grep -n 'Domain:' internal/server/tunnel_manager.go
   ```

3. **Bug 3 验证**：`restoreTunnels` 函数里所有 `ProxyConfig` 构造都带 `Domain` 字段

### 不引入的改动

- 不改任何路由逻辑
- 不加新的测试文件
- 不改协议定义
- 不改前端
