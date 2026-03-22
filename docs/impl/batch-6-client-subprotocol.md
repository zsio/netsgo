# Batch 6：Client 侧子协议发送与业务回归

> 状态：待实现
> 前置条件：Batch 5 完成
> 估计影响文件：`internal/client/client.go`、`pkg/protocol/types.go`（已在 Batch 3 完成）

## 目标

让 Client 在连接服务端时主动声明 WebSocket 子协议（`netsgo-control.v1` / `netsgo-data.v1`），使其能与 Batch 5 实现的 `hostDispatchHandler` 正确握手。完成后进行 TCP/UDP 业务回归验证，确认子协议变更不破坏现有功能。

## 背景

Batch 5 的 `hostDispatchHandler` 要求 `/ws/control` 必须带 `Sec-WebSocket-Protocol: netsgo-control.v1`，否则不会进入控制通道。旧 Client 未携带子协议，连接将失败。本批次修复 Client 侧，使其发送正确的子协议。

**注意**：本期不兼容旧 Client，旧 Client 连接服务端会失败，这是预期行为（设计文档明确约束）。

## 需要修改的内容

### 1. 修改控制通道 Dial（`/ws/control`）

**文件**：`internal/client/client.go`

找到建立控制通道 WebSocket 连接的代码，在 Dialer 中加入子协议声明：

```go
// 修改前（伪代码）
conn, _, err := websocket.DefaultDialer.Dial(controlURL, nil)

// 修改后
header := http.Header{}
header.Set("Sec-WebSocket-Protocol", protocol.WSSubProtocolControl)
conn, _, err := websocket.DefaultDialer.Dial(controlURL, header)
```

或使用 `gorilla/websocket` 的 `Dialer.Subprotocols` 字段：

```go
dialer := websocket.Dialer{
    Subprotocols: []string{protocol.WSSubProtocolControl},
    // 其他配置保持不变...
}
conn, _, err := dialer.Dial(controlURL, nil)
```

### 2. 修改数据通道 Dial（`/ws/data`）

**文件**：`internal/client/client.go`

同上，在数据通道连接时发送 `netsgo-data.v1`：

```go
dialer := websocket.Dialer{
    Subprotocols: []string{protocol.WSSubProtocolData},
    // 其他配置保持不变...
}
conn, _, err := dialer.Dial(dataURL, nil)
```

### 3. 服务端握手响应确认子协议

确认 Batch 5 中服务端 upgrader 在响应握手时会回写已协商的子协议。检查 `gorilla/websocket` 的 `Upgrader` 配置：

```go
upgrader := websocket.Upgrader{
    Subprotocols: []string{protocol.WSSubProtocolControl},
    // 或 WSSubProtocolData
}
```

gorilla/websocket 会自动从客户端声明的列表中选择第一个匹配的子协议并回写到响应中。

## HTTP 隧道 Client 侧行为

**HTTP 仍复用 TCP 数据面**，不做任何 Client 侧修改：

- Client 收到服务端发来的 `ProxyNewRequest`（type=http），像 TCP 一样处理
- 通过 yamux stream 建立连接
- 按 `local_ip:local_port` 建立 TCP 连接并 relay
- 不新增 ready 阶段探测
- 不新增 HTTP 专用握手

这意味着 Client 侧对 HTTP 隧道几乎不需要改动（已在 Batch 1 的 Bug 修复之后覆盖）。

## 测试补充

### `internal/client/client_test.go`（扩展）

```
TestClientControlDial_SendsSubprotocol
  - 建立控制通道时，请求头包含 Sec-WebSocket-Protocol: netsgo-control.v1

TestClientDataDial_SendsSubprotocol
  - 建立数据通道时，请求头包含 Sec-WebSocket-Protocol: netsgo-data.v1

TestClientHTTPTunnel_ReusesTCPDataPath
  - Client 收到 type=http 的 ProxyNewRequest
  - 期望：通过 yamux stream + local_ip:local_port 建立 TCP relay
  - 期望：不触发任何 HTTP 专用握手
```

### 回归测试（使用现有测试用例）

```
TestClient_TCPTunnel_*    （现有 TCP 隧道测试全部通过）
TestClient_UDPTunnel_*    （现有 UDP 隧道测试全部通过）
TestClient_Reconnect_*    （重连逻辑不受影响）
```

## 实现步骤

1. 确认 `pkg/protocol/types.go` 中 `WSSubProtocolControl` 和 `WSSubProtocolData` 已在 Batch 3 定义
2. 修改 `internal/client/client.go`：控制通道 Dial 加子协议
3. 修改 `internal/client/client.go`：数据通道 Dial 加子协议
4. 确认服务端 upgrader 配置正确（回写已协商子协议）
5. 补充 Client 侧测试
6. 运行完整测试套件

## 验收标准

```bash
# Client 测试全部通过（含新增子协议测试）
go test ./internal/client/... -v

# 全量测试无回归
go test ./... -v

# 构建通过
go build ./...
```

### 端到端手动验证

1. 启动 Server（Batch 5 实现后）
2. 启动 Client（本批次修改后），确认控制通道连接成功
3. 配置一条 TCP 隧道，确认正常工作（回归）
4. 配置一条 UDP 隧道，确认正常工作（回归）
5. 配置一条 HTTP 隧道（type=http），通过域名访问确认请求被转发到 local_ip:local_port

## 不引入的改动

- 不改 nginx/caddy E2E（Batch 7 做）
- 不改前端（Batch 8 做）
- 不新增 HTTP 隧道专用 Client 握手
