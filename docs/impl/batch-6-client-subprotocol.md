# Batch 6：Client 侧子协议发送与业务回归

> 状态：已完成  
> 所属阶段：阶段 5（Client + E2E）  
> 前置条件：Batch 5 完成  
> 估计影响文件：`internal/client/client.go`、`internal/client/client_test.go`

## 目标

让 Client 在连接服务端时主动声明 WebSocket 子协议：

- `/ws/control` -> `netsgo-control.v1`
- `/ws/data` -> `netsgo-data.v1`

然后做 TCP / UDP / HTTP 数据面的回归，确认没有被这次握手调整破坏。

## 边界说明

### 本批次负责

- Client dial 时发送正确子协议
- Client 侧测试和回归

### 本批次不负责

- 服务端是否接受并回写子协议
- 最终入口如何识别内部通道

这些在 Batch 5 已经完成。  
Batch 6 只做“客户端按契约发起握手”。

## 需要修改的内容

### 1. 控制通道 Dial

**文件**：`internal/client/client.go`

要求：

- 不再直接用裸 `websocket.DefaultDialer.Dial(..., nil)`
- 通过 dialer 的 `Subprotocols` 字段声明 `protocol.WSSubProtocolControl`

### 2. 数据通道 Dial

**文件**：`internal/client/client.go`

要求：

- 在数据通道 dialer 中声明 `protocol.WSSubProtocolData`

### 3. 保持 HTTP 仍复用 TCP 数据面

这一点不变：

- `type=http` 仍像 TCP 一样通过 yamux stream 建连
- 不新增 HTTP 专用 ready 探测
- 不新增 HTTP 专用握手

## 推荐测试

### `internal/client/client_test.go`

```text
TestClientControlDial_SendsSubprotocol
TestClientDataDial_SendsSubprotocol
TestClientHTTPTunnel_ReusesTCPDataPath
```

### 必须保留的现有回归

```text
TestClient_TCPTunnel_*
TestClient_UDPTunnel_*
TestClient_Reconnect_*
```

## 实现步骤

1. 复用现有 dialer 创建路径，不额外造新连接栈
2. 给 control dialer 设置 `Subprotocols`
3. 给 data dialer 设置 `Subprotocols`
4. 补客户端测试
5. 跑现有 TCP / UDP / reconnect 回归

## 验收标准

```bash
go test ./internal/client/... -v
go test ./... -v
go build ./...
```

## 手工检查点

1. Client 能重新连上 Batch 5 的 server
2. TCP 隧道行为不变
3. UDP 隧道行为不变
4. HTTP 隧道仍经由 `local_ip:local_port` 工作

## 不引入的改动

- 不补服务端 upgrader 配置
- 不改 nginx/caddy E2E（Batch 7）
- 不改前端（Batch 8）
