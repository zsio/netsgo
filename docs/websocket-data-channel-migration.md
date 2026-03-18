# RFC: 数据通道 WebSocket 化迁移方案

> **状态**: 待审核  
> **作者**: AI Assistant  
> **日期**: 2026-03-17  
> **影响范围**: Client / Server 核心通信架构

---

## 1. 背景

### 1.1 当前架构

NetsGo 当前使用单端口复用架构：

```text
外部 TCP 连接 → :PORT
                 │
          peek 首字节
                 │
      ┌──────────┴──────────┐
      │                      │
  0x4E ('N')              其他字节
      │                      │
  数据通道               http.Server
  (自定义二进制协议        ├─ /ws/control (WebSocket)
   + yamux 多路复用)       ├─ /api/*      (REST)
                           ├─ /api/events (SSE)
                           └─ /           (Web 面板)
```

- 控制通道: WebSocket `/ws/control`
- 数据通道: raw TCP + 自定义二进制握手 + yamux
- 区分方式: TCP 首字节魔数 `0x4E`

### 1.2 当前问题

数据通道不是标准 HTTP/WebSocket 流量，无法通过 L7 反向代理：

- nginx / caddy 期望收到合法 HTTP 请求
- Client 当前直接发送二进制魔数握手，代理会将其视为非法 HTTP 并返回 `400 Bad Request`
- 因此 NetsGo 现在无法稳定部署在单机反向代理之后

---

## 2. 已确认约束

这些约束已经明确，不再作为评审争议点：

1. **不考虑向后兼容**
   当前仍处于开发阶段，没有已发布版本，不需要双栈兼容、灰度发布或滚动升级。

2. **只考虑单机单实例 Server**
   本程序只会单机部署一份 Server，不设计多实例负载均衡，不处理跨实例共享在线状态。

3. **必须覆盖三类链路验证**
   - 直连 Server
   - 经 nginx 反向代理
   - 经 caddy 反向代理

### 2.1 目标

将数据通道迁移为标准 WebSocket，使 NetsGo 在以下场景都能工作：

- Client 直连 Server
- Client 经单机 nginx 反向代理接入
- Client 经单机 caddy 反向代理接入

### 2.2 非目标

以下内容不在本 RFC 范围内：

- 多实例 Server 负载均衡
- 控制通道和数据通道跨实例路由
- 旧版 raw TCP 数据通道兼容
- 基于 URL 前缀的子路径部署，例如 `/netsgo/ws/control`
- TLS 模型重做，本 RFC 不改变现有控制通道 TLS 语义

---

## 3. 迁移后架构

```text
外部 TCP 连接 → :PORT → http.Server
                           │
                           ├─ /ws/control  → 控制通道 WebSocket
                           ├─ /ws/data     → 数据通道 WebSocket
                           ├─ /api/*       → REST API
                           ├─ /api/events  → SSE
                           └─ /            → Web 面板
```

核心变化：

1. 删除 `PeekListener` / `PeekConn`
2. 删除数据通道 magic-byte 分流
3. 数据通道统一走 `/ws/data`
4. 使用 `WSConn` 将 `*websocket.Conn` 适配为 `io.ReadWriteCloser`
5. yamux、TCP/UDP relay、隧道管理逻辑继续复用

### 3.1 关键结论

`yamux` 接收的是字节流接口，不要求底层必须是 `net.Conn`。  
因此迁移的核心不是改 yamux，而是提供一个语义正确的 WebSocket 字节流适配器。

但这里有一个重要边界：

- **“能经过反向代理”成立**
- **“自动支持多机负载均衡”不成立**

本 RFC 只解决前者。

### 3.2 逻辑会话模型

这是本次修订后新增的强约束。

控制通道和数据通道不再被视为两个可以独立退化运行的子系统，而是共同组成 **一个逻辑 Client 会话**：

1. 控制通道认证成功后，Client 进入“已认证、等待数据通道”的短暂阶段
2. 数据通道握手成功且 yamux Session 建立完成后，当前逻辑会话才算真正可用
3. 当前数据通道若在运行期断开，视为整个逻辑会话失效
4. 逻辑会话失效后，必须触发 **整会话重建**
   - 关闭当前控制通道
   - 关闭当前数据通道
   - 依赖现有重连流程重新认证、重新获取 `dataToken`、重新恢复隧道

这样设计的原因：

- 避免“控制面在线但数据面已死”的伪在线状态
- 避免在线 Client 无法转发流量却仍被认为健康
- 复用现有整机会话重连和 `restoreTunnels()` 逻辑，降低状态机复杂度

因此，旧设计中的这条行为约束不再保留：

- “数据通道失败时，控制通道仍保持在线”

迁移后改为：

- **数据通道建立失败或运行期断开，当前逻辑会话直接失败并重建**

### 3.3 运行时状态模型

为了让“逻辑会话”在实现里真正成立，服务端运行时必须显式区分至少三种状态：

1. `PendingData`
   - 控制通道已认证成功
   - 已签发本代际 `dataToken`
   - 数据通道尚未 ready
   - **不**发布 `client_online`
   - **不**计入 UI / API 在线列表
   - **不**启动 `restoreTunnels()`

2. `Live`
   - 数据通道握手成功
   - `WSConn + yamux Session` 已建立
   - 可以承载转发流量
   - 可以恢复隧道
   - 对外才算“在线”

3. `Closing`
   - 已触发逻辑会话失效，或正在被新代际替换
   - 不再接受新工作
   - 仅允许执行幂等 teardown

实现要求：

- 服务端必须把“控制已认证但数据面未就绪”和“真正在线”分开建模
- 可以使用 `pendingClients + liveClients` 两张表，也可以使用单表 + 显式状态字段；但对外在线语义只能绑定到 `Live`
- `client_online` 事件、`Online=true`、`restoreTunnels()` 都只能发生在 `PendingData -> Live` 提升之后
- 若会话在 `PendingData` 阶段失败，允许直接清理，不应发布“先 online 再 offline”的伪事件

#### 状态与权限矩阵

| 状态 | 对外算在线 | 允许处理会修改共享状态的控制消息 | 阻止新 token 认证 | 允许 `restoreTunnels()` |
|------|------------|----------------------------------|-------------------|-------------------------|
| `PendingData` | 否 | 否 | 是 | 否 |
| `Live` | 是 | 是 | 是 | 是 |
| `Closing` | 否 | 否 | 否 | 否 |

这里的“会修改共享状态的控制消息”至少包括：

- `probe_report`
- `proxy_new`
- `proxy_close`
- 任何会写 `adminStore`、发布业务事件、启动/停止 listener、修改在线视图或 runtime 引用的控制路径

#### `PendingData` 必须有超时回收

`PendingData` 不是无界等待状态，必须显式设置超时回收。

建议：

- 新增 `pendingDataTimeout`
- 默认值：`15s`
- 起点：控制通道认证成功、当前代际进入 `PendingData` 时开始计时
- 到期后若该 `(clientID, generation)` 仍是当前代际且状态仍为 `PendingData`，必须执行：
  - `invalidateLogicalSessionIfCurrent(clientID, generation, "pending_data_timeout")`

强约束：

- `PendingData` 超时回收必须释放“当前会话占位”，避免阻塞后续 token 重连
- `PendingData` 超时路径**不得**发布 `client_online`
- 若该会话从未提升到 `Live`，超时回收路径也**不得**发布 `client_offline`
- `pendingDataTimeout` 必须可测试覆盖，不能写死在生产常量里
- Client 侧的数据通道建链与握手超时应显式小于该值，避免服务端先超时、客户端后报错造成歧义

### 3.4 会话代际与当前性判定

当前性不能只靠 `clientID` 判断。

每次控制通道认证成功都必须生成一个新的逻辑会话代际（例如 `generation` / `epoch`），并满足：

1. `dataToken` 绑定到该代际，而不是仅绑定到 `clientID`
2. 所有异步 goroutine 都持有自己的 `(clientID, generation)`
3. 在执行会修改共享状态的动作前，必须再次校验“我仍然是当前代际”
4. 旧代际的 cleanup / restore / data-exit 只能 best-effort 清理自己的资源，不得回写当前代际状态

至少以下路径必须带 currentness 校验：

- 控制通道 `defer cleanup`
- `handleDataWS` 退出路径
- `restoreTunnels()`
- `forceDisconnectClient()`
- `client_online` / `client_offline` 事件发布
- 任何会修改在线视图或 runtime 引用的共享关闭路径

#### 控制消息的 currentness 规则

currentness 校验不能只覆盖“连接建立 / 清理”路径，也必须覆盖 steady-state 控制消息处理。

服务端要求：

1. 控制通道收到消息后，在真正执行 handler 之前，必须再次校验：
   - `(clientID, generation)` 仍是当前代际
   - 当前逻辑会话状态是 `Live`
2. 对下列消息，若 currentness 校验失败，必须 `drop/no-op`，不得继续执行业务副作用：
   - `probe_report`
   - `proxy_new`
   - `proxy_close`
   - 任何会写 `adminStore`、发 SSE 事件、修改 tunnel runtime、变更在线视图的控制消息
3. 旧代际或 `Closing` 状态的控制消息，允许记 debug / warn 日志，但：
   - 不得更新探针快照
   - 不得启动或停止代理 listener
   - 不得持久化新的运行态
   - 不得发布会污染当前代际的事件

Client 侧配套要求：

- 不得再把“控制通道还没真正退出前收到的旧指令”视为合法工作
- 若客户端已经判定当前逻辑会话失效，后续从旧控制通道读到的副作用消息应直接忽略

---

## 4. 协议设计

### 4.1 数据通道握手

数据通道升级为 WebSocket 后，认证信息通过 **首个 binary message** 发送，而不是 URL query 或自定义 header。

握手流程：

```text
Client                                      Server
  │                                           │
  ├── GET /ws/data (Upgrade: websocket) ─────→│
  │                                           │ Upgrade 成功
  ├── Binary Message #1 ─────────────────────→│
  │   [2B ClientID长度][NB ClientID]           │ 解析 + 校验
  │   [2B DataToken长度][NB DataToken]         │
  │                                           │
  │←──────────────────── Binary Message #1 ───┤
  │   [1B 状态码]                              │
  │                                           │
  │←═══════ 后续二进制消息流 ═════════════════→│
  │     WSConn 适配后的 yamux 原始字节流        │
```

约束：

- 握手阶段不使用 `WSConn`
- 握手成功后才切换到 `WSConn + yamux`
- 握手失败后，Server 发送失败结果后必须立即关闭该数据 WebSocket
- `dataToken` 的有效范围是**单个逻辑会话代际**
  - 新一轮控制认证成功后，旧代际签发的 `dataToken` 必须视为失效
  - 服务端校验时必须同时匹配 `clientID` 和当前 `generation`

### 4.2 握手状态码归属

本次迁移后：

- `DataChannelMagic` 删除
- `DataHandshakeOK/Fail/AuthFail` **保留在共享协议层**

原因：

- 这些状态码仍然是 Client 与 Server 之间的共享 wire protocol
- `internal/client` 和 `internal/server` 都要引用
- 将其放成 Server 私有常量会让协议边界变得模糊

建议重构为单独文件，例如：

- `pkg/protocol/data_channel.go`

建议保留/新增的共享常量与辅助函数：

```go
const (
    DataHandshakeOK       byte = 0x00
    DataHandshakeFail     byte = 0x01
    DataHandshakeAuthFail byte = 0x02

    DataHandshakeMaxClientIDLen = 1024
    DataHandshakeMaxTokenLen    = 256
)

func EncodeDataHandshake(clientID, dataToken string) []byte
func DecodeDataHandshake(payload []byte) (clientID, dataToken string, err error)
```

旧的 `DataHandshakeBytes()` 作为测试辅助函数应删除，由共享协议 helper 替代。

这里推荐 `[]byte` 而不是 `io.Reader` 的原因是：

- 迁移后数据通道握手的协议边界就是“首个 WebSocket binary message 的 payload”
- 握手本身不再是一个无边界的字节流读取过程
- 用 `[]byte` API 更能反映真实协议模型，也更便于做“必须恰好消费完整 payload”的校验

### 4.3 为什么不用 URL / Header 传凭证

以下方案都不选：

1. URL query 传 `client_id` / `token`
   - 容易出现在 access log
   - 容易被代理、监控、错误页采集

2. 自定义 header 传 token
   - 同样可能进入代理日志
   - 不比首个 binary frame 更简单

3. WebSocket subprotocol 传 token
   - 仍属于 header 语义
   - 可读性差，不适合承载凭证

最终方案：

- Upgrade 完成后
- 首个 binary message 发送握手凭证
- 握手成功后切换为纯数据流

### 4.4 首帧大小控制方案

这是本次设计里的一个必须写清楚的点。

#### 事实

`gorilla/websocket.Upgrader` **没有** `ReadLimit` 字段。  
因此不能在 `Upgrader` 上限制消息大小。

#### 采用方案

使用 **双层限制**：

1. Upgrade 成功后，立即调用 `conn.SetReadLimit(wsDataMaxMessageSize)`
2. 首个握手帧解析时，再额外按握手格式做严格长度校验

建议值：

- `wsDataMaxMessageSize = 512 * 1024`
- `maxHandshakePayload = 2 + 1024 + 2 + 256 = 1284 bytes`

这样做的原因：

- `SetReadLimit` 保护整个连接生命周期
- 握手解析器保护首帧必须是精确、短小、格式合法的认证包

额外约束：

- `wsDataMaxMessageSize` 不是孤立常量，必须显式大于当前 yamux 单次写入上界
- 若未来调整 `pkg/mux` 的窗口或写包策略，必须同步复核该限制值

#### 其他可选办法

有，但不如双层限制稳妥：

1. 只用 `SetReadLimit`
   - 可以防大包
   - 但首帧仍然缺少更细粒度的语义校验

2. 只用 `io.LimitedReader`
   - 能限制首帧
   - 但对后续数据消息缺少统一保护

3. 用 `ReadMessage()` 一次性读完整帧
   - 对握手小包可行
   - 但 steady-state 的适配层不应依赖整帧分配

最终建议：

- **连接级别 `SetReadLimit`**
- **握手级别长度校验**

两者一起上。

### 4.5 WebSocket 数据通道参数

建议新增独立的数据通道升级器，例如 `dataUpgrader`：

```go
var dataUpgrader = websocket.Upgrader{
    HandshakeTimeout: 10 * time.Second,
    ReadBufferSize:   32 * 1024,
    WriteBufferSize:  32 * 1024,
    CheckOrigin:      sameAsControl,
    EnableCompression: false,
}
```

约束说明：

- `EnableCompression = false`
  - 数据通道承载 yamux 二进制流，不需要 permessage-deflate
  - 避免额外 CPU 开销和消息边界复杂度

- `ReadBufferSize / WriteBufferSize`
  - 必须显式指定，不要依赖默认值
  - gorilla/websocket 在默认情况下使用约 `4KB` 缓冲
  - `4KB` 不是正确性问题，但对承载 yamux 数据面的长连接来说偏小，会增加 syscalls 和帧开销
  - 建议初始值：`32KB`
  - 后续若观测到吞吐瓶颈，可在实现阶段再根据压测结果调整

- `CheckOrigin` 复用控制通道逻辑
  - 无 `Origin` 头: 放行（Go Client）
  - 有 `Origin` 头: 要求 `origin.Host == r.Host`

- 代理必须保留 `Host`
  - 否则带 `Origin` 的浏览器场景会被错误拒绝

### 4.6 握手失败语义

这部分必须写死，否则 Client 和 Server 很容易各自实现出不同的失败行为。

#### A. HTTP 层失败

- 请求 `/ws/data` 但不是 WebSocket Upgrade
- 返回 `426 Upgrade Required` + JSON 提示

这是 HTTP 响应，不进入数据通道握手状态机。

#### B. WebSocket 协议层失败

这类失败 **不走 1 字节状态码**，而是直接用 WebSocket close/error：

1. 首帧不是 binary message
   - Server 直接关闭连接
   - 建议 close code: `1003 Unsupported Data`

2. 首帧超过 `SetReadLimit`
   - gorilla/websocket 会发送 close 并返回错误
   - 等价于握手失败
   - 典型 close code: `1009 Message Too Big`

3. Upgrade 后出现其他 WebSocket 协议错误
   - 直接视为握手失败

#### C. 应用层握手失败

这类失败走 **1 字节 binary 响应 + 立即关闭连接**：

1. `clientID` 为空、超长、格式不合法
   - `DataHandshakeFail`

2. `dataToken` 为空、超长、格式不合法
   - `DataHandshakeFail`

3. `clientID` 未注册
   - `DataHandshakeFail`

4. `dataToken` 校验失败
   - `DataHandshakeAuthFail`

#### D. Client 侧实现要求

Client 侧握手不能只支持“读到 1 字节状态码”这一种成功路径，必须明确处理两类失败：

1. 收到 1 字节 binary 响应且状态码非 `DataHandshakeOK`
   - 视为握手失败

2. 在等待握手响应时直接收到 WebSocket close / read error
   - 同样视为握手失败

换句话说：

- **成功路径必须是 1 字节 binary `OK`**
- **失败路径既可能是 1 字节失败状态码，也可能是 close/error**

### 4.7 控制通道认证失败的机器可读语义

本次迁移后，Client 不能继续靠 `AuthResponse.Message` 或错误字符串前缀去判断：

- 这次认证是否可重试
- 本地 token 是否应该清除

否则“数据面掉线导致的重连”仍然很容易被误实现成“认证失败并清 token”。

建议扩展控制通道认证响应：

```go
type AuthResponse struct {
    Success    bool   `json:"success"`
    Message    string `json:"message,omitempty"`
    ClientID   string `json:"client_id,omitempty"`
    Token      string `json:"token,omitempty"`
    DataToken  string `json:"data_token,omitempty"`

    Code       string `json:"code,omitempty"`
    Retryable  bool   `json:"retryable,omitempty"`
    ClearToken bool   `json:"clear_token,omitempty"`
}
```

字段语义：

- `Message`
  - 仅用于展示和日志
  - **不得**作为客户端分支逻辑依据

- `Code`
  - 机器可读错误码
  - 至少建议覆盖：
    - `ok`
    - `invalid_token`
    - `revoked_token`
    - `invalid_key`
    - `concurrent_session`
    - `rate_limited`
    - `server_uninitialized`

- `Retryable`
  - 当前失败是否允许外层进入重连流程

- `ClearToken`
  - 当前失败是否要求客户端删除本地保存的 token

Client 必须按以下规则实现：

1. 只有在收到 `AuthResponse{Success:false, ClearToken:true}` 时，才允许清除本地 token
2. 只要没有收到明确的 `ClearToken:true`，就必须保留本地 token
3. 连接关闭、读超时、服务端未返回 `auth_resp`、数据通道失败、逻辑会话主动失效，这些都**不是**清 token 条件
4. Client 是否退出重连循环，必须由机器可读结果控制，**不得**继续通过匹配 `"认证"` 之类的错误字符串来判断 fatality

推荐错误码语义：

| `Code` | `Retryable` | `ClearToken` | 说明 |
|--------|-------------|--------------|------|
| `ok` | `false` | `false` | 认证成功 |
| `invalid_token` | `false` | `true` | 本地 token 无效，可在下一轮改用 Key 或人工介入 |
| `revoked_token` | `false` | `true` | token 已吊销，必须丢弃 |
| `invalid_key` | `false` | `false` | 提供的 Key 无效，属于致命认证失败 |
| `concurrent_session` | `true` | `false` | 当前存在有效会话，可等待后重试 |
| `rate_limited` | `true` | `false` | 速率限制，等待后重试 |
| `server_uninitialized` | `true` | `false` | 服务端尚未初始化，可等待后重试 |

---

## 5. WSConn 适配器设计

### 5.1 目标

将 `*websocket.Conn` 适配为 yamux 可用的可靠字节流接口。

建议文件：

- `pkg/mux/wsconn.go`

### 5.2 建议接口

最小目标是：

```go
type WSConn struct {
    conn      *websocket.Conn
    reader    io.Reader
    writeMu   sync.Mutex
    closeOnce sync.Once
}

func NewWSConn(conn *websocket.Conn) *WSConn
func (w *WSConn) Read(p []byte) (int, error)
func (w *WSConn) Write(p []byte) (int, error)
func (w *WSConn) Close() error
```

建议额外实现：

```go
func (w *WSConn) LocalAddr() net.Addr
func (w *WSConn) RemoteAddr() net.Addr
```

原因：

- `yamux.Session.LocalAddr/RemoteAddr()` 会尝试向下透传地址
- 实现后日志和调试信息更完整

### 5.3 Read 语义

`Read` 必须把 WebSocket 的“消息模式”转换成 yamux 需要的“连续字节流模式”。

建议策略：

1. 若当前没有活跃 reader，则调用 `NextReader()`
2. 只接受 `BinaryMessage`
3. 从当前 reader 读取到 `io.EOF` 后，自动切换到下一条 binary message
4. 对上层暴露为连续字节流

注意：

- 这里不能假设“一条 WebSocket 消息对应一条 yamux 帧”
- `yamux` 内部会对同一帧的 header 和 body 分多次 `Write`
- 因此真实映射更准确的表述是：
  - **一次底层 `WSConn.Write` 对应一条 WebSocket binary message**
  - **`WSConn.Read` 负责跨消息拼接回连续字节流**

### 5.4 Write 语义

建议策略：

1. `writeMu.Lock()`
2. `defer writeMu.Unlock()`
3. `NextWriter(websocket.BinaryMessage)`
4. 成功拿到 `writer` 后，必须 `defer writer.Close()`
5. 写入 `p`
6. 返回时处理 `writer.Close()` 的错误

这里的锁不是可选优化，而是必要约束：

- gorilla/websocket 只允许一个并发 writer
- yamux 在关闭、GoAway、正常发包之间都可能出现竞争写入

这里额外要强调：

- `writer.Close()` 不是“仅成功路径才调用”的收尾动作
- 即使 `writer.Write(p)` 返回错误，也应 best-effort 执行 `writer.Close()`
- 否则容易留下半关闭 writer 状态，后续写入路径也更难保证行为稳定

### 5.5 Close 语义

`Close` 不应使用 `WriteMessage()` 发送关闭帧。  
正确做法应是：

1. `closeOnce.Do(...)`
2. best-effort `WriteControl(CloseMessage, ...)`
3. `conn.Close()`

原因：

- `WriteControl` 可以与其它方法并发
- `WriteMessage` 不能安全地与普通 writer 并发

建议实现意图：

```go
func (w *WSConn) Close() error {
    var err error
    w.closeOnce.Do(func() {
        deadline := time.Now().Add(1 * time.Second)
        _ = w.conn.WriteControl(
            websocket.CloseMessage,
            websocket.FormatCloseMessage(websocket.CloseNormalClosure, "closing"),
            deadline,
        )
        err = w.conn.Close()
    })
    return err
}
```

---

## 6. Server 侧改动

### 6.1 `server.go`

需要改动：

1. `Start()` 直接 `Serve(serveLn)`，删除 `PeekListener`
2. `/ws/data` 从“说明性 JSON”改为真实 WebSocket 数据通道入口
3. 启动日志更新为 WebSocket 路径
4. 删除 `PeekListener` 相关代码

建议保留一个更友好的非升级响应：

- 若访问 `/ws/data` 但不是 WebSocket Upgrade
- 返回 `426 Upgrade Required` + JSON 提示

原因：

- 浏览器或手工 `curl` 调试时更清楚
- 不影响正式 WebSocket Client

### 6.2 `handleDataWS`

建议替换 `handleDataConn(net.Conn)` 为 `handleDataWS(http.ResponseWriter, *http.Request)`。

推荐流程：

1. 检查是否为 WebSocket Upgrade
2. 非 Upgrade 请求直接返回 `426`
3. `dataUpgrader.Upgrade(...)`
4. `conn.SetReadLimit(wsDataMaxMessageSize)`
5. `conn.SetReadDeadline(now + 10s)`
6. 读取首个 WebSocket message
7. 要求首帧必须是 `BinaryMessage`
8. 解析握手 payload
9. 查找 `clientID` 对应的当前逻辑会话记录
10. 使用 `subtle.ConstantTimeCompare` 校验 `dataToken`
    - 校验对象必须是该逻辑会话的当前 `generation`
11. 进入单个 replacement / promotion 临界区
12. 再次校验该逻辑会话仍然是当前 `generation`，且状态不是 `Closing`
13. 为握手成功响应设置短写超时（例如 `2s`）
14. 先发送 1 字节 binary `DataHandshakeOK`
15. 若步骤 14 失败，立即关闭该 WebSocket，且**不得**把当前代际提升为 `Live`
16. 清除握手读写超时
17. 构造 `wsConn := mux.NewWSConn(conn)`
18. `mux.NewServerSession(wsConn, mux.DefaultConfig())`
19. 若步骤 18 失败，必须立即走当前代际共享失效路径
20. 关闭旧 `dataSession`
21. 赋值 `client.dataSession = session`
22. 若当前状态是 `PendingData`，原子提升为 `Live`
23. 退出 replacement / promotion 临界区
24. 仅在 `PendingData -> Live` 提升成功后，发布 `client_online`
25. 仅在 `PendingData -> Live` 提升成功后，启动 `restoreTunnels()`
26. 阻塞等待 `session.CloseChan()`
27. 会话退出时做“当前 session + 当前 generation”条件清理
28. 若退出的是当前 `Live` 会话，则触发逻辑会话失效处理

关键约束：

- 握手失败响应写完后，必须立即关闭该数据 WebSocket
- **不要**在旧 session 被新 session 替换时误伤当前控制通道
- `client_online` 与 `restoreTunnels()` **不得**在控制认证成功时提前发生，只能在步骤 24-25 发生
- replacement / promotion 必须对外表现为原子状态切换；旧 session 退出时不能看到“当前 generation 尚未安装新 session，但已被判为失效”的可见空窗
- **握手成功响应必须先于 `WSConn + yamux` steady-state 可见化**
- 在 1 字节 `OK` 成功写出之前，`client.dataSession`、在线视图和 `Live` 状态都**不得**暴露给其它 goroutine
- 若步骤 18 之后失败，虽然 Client 可能已经收到了 `OK`，服务端仍必须立即关闭数据通道并使当前逻辑会话失效，让 Client 按 retryable startup failure 重连
- replacement / promotion 临界区中不得执行无界阻塞操作；唯一允许的网络写入是带短超时的 1 字节握手成功响应

### 6.3 当前 `dataSession` 退出语义

这是本次修订后新增的重点。

`handleDataWS` 退出时不能只打印日志，必须区分两种情况：

本条默认前提：

- `6.2` 中 replacement 临界区已经保证“关闭旧 session”和“安装新 session”之间不存在可见空窗

#### A. 退出的是旧 session（已被新 session 替换）

判定：

```go
isCurrentGeneration := s.isCurrentGeneration(clientID, generation)

client.dataMu.Lock()
isCurrentSession := client.dataSession == session
if isCurrentSession {
    client.dataSession = nil
}
client.dataMu.Unlock()
```

若 `isCurrentGeneration == false` 或 `isCurrentSession == false`：

- 说明这是旧 session 被显式替换后的正常退出
- 不应关闭当前控制通道
- 不应影响新 session
- 不应发布 `client_offline`

#### B. 退出的是当前 session

若 `isCurrentGeneration == true && isCurrentSession == true`：

- 说明当前有效数据通道已失效
- 必须触发逻辑会话失效
- 由共享关闭路径统一做：
  - 关闭控制通道
  - 暂停代理
  - 让 Client 走整机会话重连

这里不能继续维持“控制面在线、数据面断开”的状态。

补充约束：

- 当前性判定**不能**只依赖 `client.dataSession == session`
- 只要当前 `ClientConn` 指针 / generation 已经被替换，旧代际退出就必须变成 no-op 清理

### 6.4 控制通道与数据通道的协同关闭

为避免关闭路径散落，建议 Server 侧统一成一个共享动作：

- `invalidateLogicalSessionIfCurrent(clientID, generation, reason)`

建议最少完成以下动作，且顺序固定：

1. 校验当前 `(clientID, generation)` 仍然有效
2. 原子地将当前逻辑会话标记为 `Closing`
3. 从“在线可见状态”中移除
   - 若使用双表，则从 `liveClients` 移除
   - 若使用单表，则先把状态切到 `Closing`
4. 停止接受新工作
   - 不再恢复隧道
   - 不再对外报告在线
5. 关闭控制通道
6. 关闭当前数据 session
7. 暂停所有 active proxies
8. 清理 runtime 引用
9. 若这是当前 `Live` 会话，才发布 `client_offline`

已有类似辅助逻辑应复用或重构，避免：

- `Shutdown()` 一套
- `forceDisconnectClient()` 一套
- `handleDataWS` 退出又一套

三套逻辑各自演化，后面一定出错。

这里顺序之所以重要，是因为：

- 若先关 socket、后撤在线可见性，重连认证会与旧会话并发存在
- 当前服务端已有“发现旧控制连接仍在就拒绝新 token 连接”的逻辑，因此失效路径必须先把旧代际移出可见当前态，再执行关闭，避免把正常自愈误判成并发登录

### 6.5 优雅关闭顺序

这里要明确：

- WebSocket Upgrade 之后，连接已经脱离普通 HTTP 请求生命周期
- `http.Server.Shutdown()` **不会主动替你关闭这些已升级连接**

因此关闭顺序仍然必须是：

1. 关闭 SSE 事件总线
2. 显式关闭所有控制通道 WebSocket
3. 显式关闭所有 `dataSession`
   - `dataSession.Close()` 会触发 `WSConn.Close()`
   - `WSConn.Close()` 会关闭底层数据 WebSocket
4. 短暂等待 handler 退出
5. 再执行 `httpServer.Shutdown()`

这部分不是“改成 WebSocket 后自然由 `http.Server` 接管”，而是仍需手动关闭。

### 6.6 反向代理要求

虽然只支持单机单实例，但仍应明确反向代理要求：

1. `/ws/control` 和 `/ws/data` 都要转发到同一个 upstream
2. 必须保留 WebSocket Upgrade 头
3. 必须保留 `Host`
4. nginx 需要适当放宽 `proxy_read_timeout`
5. caddy 默认 WebSocket 支持可用，但也应纳入 E2E

---

## 7. Client 侧改动

### 7.1 `deriveDataURL()`

新增：

```go
func (c *Client) deriveDataURL() string
```

规则：

- `http://host:port` -> `ws://host:port/ws/data`
- `https://host:port` -> `wss://host:port/ws/data`

### 7.2 数据通道 WebSocket Dialer

这里不能直接 new 一个零值 `websocket.Dialer`。

推荐做法：

1. 从 `*websocket.DefaultDialer` 拷贝出一个 dialer
2. 显式设置：
   - `HandshakeTimeout = 10s`
   - `ReadBufferSize = 32KB`
   - `WriteBufferSize = 32KB`
   - `EnableCompression = false`
3. 若 `useTLS`，再补 `TLSClientConfig`

原因：

- 保留 `DefaultDialer` 里的默认代理行为和其他默认字段
- 避免遗漏握手超时
- 避免数据通道默认退回到 `4KB` 缓冲
- 避免数据通道被意外启用压缩

建议抽成共享 helper，例如：

```go
func (c *Client) newWSDialer(host string) *websocket.Dialer
```

### 7.3 `connectDataChannel()`

推荐流程：

1. `deriveDataURL()`
2. `dialer := c.newWSDialer(host)`
3. `Dial(dataURL, nil)`
4. `wsConn.SetReadLimit(wsDataMaxMessageSize)`
5. `wsConn.SetReadDeadline(now + 10s)`
6. 发送首个 binary 握手消息
7. 读取握手响应
8. 必须要求响应为 1 字节 binary `DataHandshakeOK`
9. 清除握手读超时
10. `mux.NewClientSession(mux.NewWSConn(wsConn), mux.DefaultConfig())`
11. 赋值 `c.dataSession`

这里的失败处理必须包括两类：

1. 收到 1 字节失败状态码
2. 在读响应前直接读到 close/error

两者都要返回明确错误。

### 7.4 启动阶段的会话建立规则

迁移后不再接受“控制通道认证成功，但数据通道失败也算连接完成”。

改为：

1. 控制通道连接成功
2. 控制通道认证成功
3. 数据通道建立成功
4. 启动 `acceptStreamLoop()`
5. 启动心跳、探针、代理请求等后台逻辑

如果第 3 步失败：

- 当前逻辑会话直接判定失败
- 主动关闭控制通道
- 返回上层，进入统一重连流程
- 这属于**可重试连接失败**，不是认证失败

这样做的原因：

- 避免 Client 处于“看起来在线，但一定无法转发”的无效状态
- 让重连和恢复路径只有一套
- 避免一次数据面启动失败把本地有效 token 误清掉

### 7.5 运行期数据通道失效处理

这是本次修订后新增的必须项。

`acceptStreamLoop()` 不能只在 `AcceptStream()` 返回错误后退出并打日志，必须触发整机会话失效：

1. 当前 `dataSession` 关闭或异常
2. 主动关闭控制通道
3. 让 `controlLoop()` 返回
4. 外层 `Start()` 进入现有重连流程

换句话说：

- **数据通道运行期断开 = 当前逻辑会话失效**
- 这同样属于**可重试连接失败**，默认不得清除本地 token

### 7.6 Token 与重连语义

这是本次修订后必须写死的行为，否则“数据面掉线自愈”会被实现成“认证异常”。

Client 侧要求：

1. 只有在收到 `AuthResponse{Success:false, ClearToken:true}` 时，才允许清除本地 token
2. 数据通道建立失败、运行期独立断开、握手超时、服务端主动关闭当前逻辑会话，这些都属于 retryable reconnect，不得清 token
3. 启动阶段若控制已认证成功，但数据通道失败，应保留当前 token，直接走重连
4. Client 是否继续重连，必须依据机器可读结果（如 `Retryable` / 显式错误类型），**不得**通过匹配错误字符串或 `Message` 文本判断
5. 若控制通道连接在认证阶段直接关闭、超时或返回非 `auth_resp`，默认按 retryable connect failure 处理，且保留 token

Server 侧配套要求：

1. 并发连接保护只应用于当前仍有效的 `PendingData` / `Live` 会话
2. 若旧会话已经进入 `Closing`，新连接不应被按“并发登录”拒绝
3. 数据通道掉线触发的整会话失效，不应让下一次 token 认证退化成“服务端异常响应”
4. 对认证失败，必须返回 machine-readable 的 `Code` / `Retryable` / `ClearToken`，而不只是人类可读 `Message`

### 7.7 Client 行为不变点

以下行为继续保持：

- 通过控制通道完成认证并获取 `clientID` / `dataToken`
- `cleanup()` 负责关闭旧 `dataSession`
- 重连后重新认证、重新拿 `dataToken`
- 代理恢复仍然依赖服务端现有 `restoreTunnels()` 主流程

---

## 8. 文件改动范围

### 8.1 新建文件

| 文件 | 说明 |
|------|------|
| `pkg/mux/wsconn.go` | WebSocket 到字节流的适配器 |
| `pkg/mux/wsconn_test.go` | `WSConn` 单元测试 |
| `pkg/protocol/data_channel.go` | 数据通道共享握手常量与 helper |

### 8.2 修改文件

| 文件 | 改动 |
|------|------|
| `internal/server/server.go` | 删除 peek 分发，接入 `/ws/data`，引入 `PendingData/Live/Closing` 状态语义，更新启动日志、关闭流程和生命周期语义 |
| `internal/server/data.go` | `handleDataConn` 重写为 `handleDataWS`，补当前 session 退出处理 |
| `internal/server/tunnel_manager.go` | 收敛逻辑会话 currentness 校验、共享失效路径、代际安全的强制断连与隧道恢复 |
| `internal/client/client.go` | 新增 `deriveDataURL()`、数据通道 dialer helper、重写 `connectDataChannel()`、收敛会话失效处理与 token 保留语义 |
| `pkg/protocol/message.go` | 为控制通道认证结果增加 machine-readable 字段（如 `Code` / `Retryable` / `ClearToken`） |
| `pkg/protocol/types.go` | 删除 `DataChannelMagic`，清理相关注释 |
| `internal/server/data_test.go` | 从 raw TCP 测试改为 WebSocket 数据通道测试 |
| `internal/client/client_test.go` | 改造 `connectDataChannel`、会话失败语义和 mock server 相关测试 |
| `internal/client/client_tls_test.go` | 改造数据通道 TLS 测试 |
| `internal/server/server_test.go` | 删除/替换 `PeekConn`、`PeekListener` 测试，补 shutdown、逻辑会话失效与 `/ws/data` 路由测试 |
| `e2e_test.go` | 直连 E2E 升级为真实数据 WebSocket 场景，并补空闲保活与重连恢复 |
| `README.md` | 更新架构图、数据通道说明、部署说明 |

### 8.3 删除文件

| 文件 | 原因 |
|------|------|
| `internal/server/peek.go` | 不再需要 magic-byte 分流 |

### 8.4 明确无需大改的核心转发逻辑

以下业务层转发逻辑原则上不需要因 WebSocket 化而整体重写，但必须回归验证：

| 文件 | 原因 |
|------|------|
| `pkg/mux/mux.go` | 仍以 `io.ReadWriteCloser` 为抽象边界 |
| `pkg/mux/udp_frame.go` | 操作的是 yamux stream |
| `internal/server/proxy.go` | 依赖 `openStreamToClient`，不依赖底层传输类型 |
| `internal/server/udp_proxy.go` | 同上 |
| `internal/client/udp_handler.go` | 同上 |
| `internal/server/proxy_test.go` | 手工注入 `dataSession`，主要关注转发语义 |
| `internal/server/udp_proxy_test.go` | 同上 |
| `internal/client/client_stream_test.go` | 直接构造 yamux session，不依赖数据通道建链方式 |

---

## 9. 可测试性要求

仅靠生产默认超时和保活参数，代理 E2E 很容易因为等待时间过长而变得不稳定或执行过慢。

因此本 RFC 新增一条工程要求：

- **必须提供测试可覆盖的时间参数注入点**

至少应允许测试环境覆盖：

1. 数据通道握手超时
2. yamux keepalive interval
3. `restoreTunnels()` 的等待间隔/总时长
4. 代理 E2E 中使用的空闲窗口
5. 逻辑会话 `PendingData -> Live -> Closing` 状态切换可被稳定观察
6. `pendingDataTimeout`
7. 控制通道认证失败的机器可读分支（`Retryable` / `ClearToken`）可被稳定覆盖

生产默认值保持不变，但测试环境必须能缩短这些时长，否则很难把“空闲保活”和“掉线恢复”测透。

---

## 10. 测试策略

这部分必须完整，不允许只改 `data_test.go` 就宣称完成迁移。

### 10.1 必须重写或删除的旧测试

#### Server 侧

1. `internal/server/data_test.go`
   - 全部从 `net.Pipe()` + raw TCP 改为 `httptest.Server` + WebSocket client

2. `internal/server/server_test.go`
   - 删除 `TestPeekConn_*`
   - 删除 `TestPeekListener_*`
   - 新增 `/ws/data` 路由与生命周期场景测试

#### Client 侧

3. `internal/client/client_test.go`
   - `TestClient_DataChannelConnectErrorHandling`
   - `TestClient_ConnectDataChannel_Success`
   - `TestClient_ConnectDataChannel_Rejected`
   - `TestClient_ConnectDataChannel_NoPort`
   - 新增 `deriveDataURL()` 测试

4. `internal/client/client_tls_test.go`
   - `TestScenario_TLS_DataChannelUsesTLS`
   - `TestScenario_PlainWS_DataChannelUsesPlainWS`

### 10.2 需要间接改造的测试

这类测试虽然不直接断言数据通道协议，但当前实现依赖“数据通道快速失败”的旧行为，迁移后不应再靠 sleep 碰运气。

需要检查并按需改造：

1. `internal/client/client_test.go`
   - `TestClient_ConnectAndAuth`
   - `TestClient_HeartbeatSent`
   - `TestClient_ProbeReportSent`
   - `TestClient_ServerDisconnect_WithReconnect`
   - `TestClient_Reconnect_AfterDisconnect`
   - `TestClient_RequestProxy`
   - `TestClient_ControlLoop_ProxyNewResp_*`

建议改法：

- 为 mock server 补 `/ws/data` handler
- 对“需要健康数据通道”的测试提供可控的假数据通道
- 对“故意测试数据通道失败”的测试显式返回 404 / `426` / 握手拒绝 / close code
- 不再依赖“旧 TCP 连接失败大约 1-2 秒”这种时间假设

### 10.3 新增单元测试

#### `pkg/mux/wsconn_test.go`

至少覆盖：

1. `Read` 能跨多条 binary message 拼成连续字节流
2. `Write` 每次写入都产生一条 binary message
3. `Close` 幂等
4. `Close` 与普通 `Write` 并发不 panic
5. 收到非 binary message 时返回错误
6. `Write` 在中途写失败时仍会执行 `writer.Close()`
7. `LocalAddr/RemoteAddr` 透传正确（若实现）

#### `pkg/protocol/data_channel_test.go`

至少覆盖：

1. 握手编码正确
2. 握手解码正确
3. `clientID` 超长拒绝
4. `dataToken` 超长拒绝
5. 空 token 拒绝
6. 首帧超长拒绝

### 10.4 Server 数据通道测试矩阵

`internal/server/data_test.go` 至少覆盖：

1. 正确 `clientID + dataToken` -> `OK`
2. `clientID` 长度为 0 -> `Fail`
3. `clientID` 超长 -> `Fail`
4. `dataToken` 长度为 0 -> `Fail`
5. `dataToken` 超长 -> `Fail`
6. 未注册 `clientID` -> `Fail`
7. token 错误 -> `AuthFail`
8. server 侧 `client.dataToken == ""` -> `AuthFail`
9. 首帧不是 binary -> WebSocket close
10. 首帧超过 `SetReadLimit` -> WebSocket close / read error
11. 非 Upgrade 请求 `/ws/data` -> `426`
12. 同一 Client 二次接入 -> 旧 session 被关闭，新 session 替换
13. `openStreamToClient` 成功
14. `openStreamToClient` 在无 session 时失败
15. 旧 generation 的 `dataToken` -> `AuthFail`
16. 控制认证完成但数据通道未 ready 时，不得被视为 online

### 10.5 新增关闭 / 生命周期测试

这是本次迁移里最容易漏掉、也最容易线上出事故的部分。

至少要有：

1. `TestServer_GracefulShutdown_WithActiveDataWS`
   - 建立真实控制通道
   - 建立真实数据 WebSocket
   - 创建 yamux session
   - 调用 `Shutdown()`
   - 断言不会阻塞，数据通道被关闭

2. `TestHandleDataWS_ClearsCurrentSessionOnExit`
   - 当前 session 退出后，`client.dataSession` 被置空

3. `TestHandleDataWS_ReplacedOldSessionDoesNotTearDownCurrentControl`
   - 旧 session 被新 session 替换后退出
   - 不得误关当前控制通道

4. `TestHandleDataWS_StaleGenerationDoesNotPromoteOrInvalidateCurrentSession`
   - 旧 generation 的 goroutine 晚到
   - 不得把自己提升成当前 live
   - 不得误伤新 generation

5. `TestHandleDataWS_CurrentSessionIndependentDataDropInvalidatesLogicalSession`
   - 当前有效数据通道独立断开
   - 必须触发整会话失效

6. `TestServer_ClientOnlineEventOnlyAfterDataReady`
   - 控制认证成功后先处于 `PendingData`
   - 建立数据通道并提升到 `Live` 后才发布 `client_online`

7. `TestServer_RestoreTunnels_StartsOnlyAfterLivePromotion`
   - `restoreTunnels()` 不得在 `PendingData` 提前启动
   - 旧 generation 的 restore goroutine 必须 no-op

8. `TestServer_ReconnectDuringClosingNotRejectedAsConcurrentLogin`
   - 旧会话进入 `Closing`
   - 新 token 认证不应被误判成并发连接

9. `TestClient_DataChannelStartupFailureAbortsControlSession`
   - 启动阶段数据通道建立失败
   - 控制通道必须被主动关闭

10. `TestClient_RuntimeIndependentDataChannelLossForcesReconnect`
   - 运行期数据通道独立断开
   - Client 进入统一重连流程

11. `TestClient_RetryableReconnectDoesNotClearToken`
   - 数据通道失败 / close / timeout
   - 只要不是明确 auth reject，就必须保留 token

12. `TestClient_Cleanup_ClosesWSBackedDataSession`
   - Client cleanup 能关闭 WebSocket-backed `dataSession`

13. `TestClient_ConnectDataChannel_HandlesCloseWithoutStatusByte`
   - 例如首帧非 binary / 超限
   - Client 能把 close/error 识别为握手失败

14. `TestServer_PendingDataTimeoutInvalidatesSession`
   - 会话停留在 `PendingData` 超过超时
   - 必须释放当前占位，不得留下 zombie session

15. `TestServer_StaleOrClosingControlMessagesAreNoOp`
   - 旧 generation 或 `Closing` 状态收到 `probe_report` / `proxy_new` / `proxy_close`
   - 不得更新共享状态、不得发布污染当前代际的事件

16. `TestHandleDataWS_AckPrecedesLiveVisibility`
   - `DataHandshakeOK` 必须先成功写出
   - 在此之前不得安装当前 `dataSession`，不得提升 `Live`

17. `TestClient_AuthResponseFlagsDriveRetryAndTokenRetention`
   - `Code` / `Retryable` / `ClearToken` 决定是否重连、是否清 token
   - 不能依赖 `Message` 文本匹配

### 10.6 E2E 集成测试矩阵

这部分是本 RFC 的硬门槛。

每一类入口都至少要覆盖三组场景：

#### A. 首次建链成功

目标：

- 建立控制通道与数据通道
- 创建 TCP 隧道
- 完成真实请求转发

#### B. 空闲后仍可转发

目标：

- Client 与 Server 建链成功后保持空闲
- 空闲时间必须跨过至少一个 keepalive 周期
- 空闲结束后再次请求，隧道仍可正常转发

这组测试是为了防止：

- 代理超时提前回收 `/ws/data`
- keepalive 没有真实起效
- WSConn / yamux 在空闲后状态不一致

#### C. 数据通道独立断开后的恢复

目标：

- 人为打断当前 `/ws/data`
- 验证会触发整会话重连
- 验证 active 隧道能恢复
- 验证恢复后再次请求成功

#### D. 具体矩阵

1. 直连 E2E
   - `TestE2E_TCPProxyTunnel_Direct_Initial`
   - `TestE2E_TCPProxyTunnel_Direct_IdleSurvives`
   - `TestE2E_TCPProxyTunnel_Direct_DataDropReconnects`

2. nginx 反代 E2E
   - `TestE2E_TCPProxyTunnel_Nginx_Initial`
   - `TestE2E_TCPProxyTunnel_Nginx_IdleSurvives`
   - `TestE2E_TCPProxyTunnel_Nginx_DataDropReconnects`

3. caddy 反代 E2E
   - `TestE2E_TCPProxyTunnel_Caddy_Initial`
   - `TestE2E_TCPProxyTunnel_Caddy_IdleSurvives`
   - `TestE2E_TCPProxyTunnel_Caddy_DataDropReconnects`

### 10.7 性能 / 稳定性验证

这部分不是“锦上添花”，而是为了确认 WebSocket 化没有把数据面退化到不可接受。

至少要补：

1. `BenchmarkDataChannelTransport_YamuxOverPipe_vs_WSConn`
   - 在不引入公网网络噪声的前提下，对比纯字节流与 `WSConn` 承载 yamux 的开销
   - 至少覆盖小包 / 中包 / 大包三档负载

2. `TestSoak_DataChannel_IdleAndTraffic`
   - 使用测试缩短后的 keepalive 参数
   - 至少跨过多个 keepalive 周期
   - 验证无异常重连、无伪在线、无 handler 泄漏

3. 代理路径压测或基准记录
   - 若无法在 `go test` 中长期运行，也必须在实现说明中记录基准结果
   - 至少记录吞吐、CPU、连接稳定性三个维度

只有功能正确但没有这部分验证，仍不能说明迁移风险已被真正关闭。

### 10.8 执行方式建议

推荐自动化方案：

1. Go 直连 E2E: 直接走 `go test`
2. nginx / caddy E2E: **必须**使用容器化测试环境
   - 不依赖宿主机预装 nginx / caddy
   - 不在开发机或 CI 机器上额外安装系统级代理软件
   - 优先使用 Docker
   - `Dockerfile` 已存在，可直接复用
   - 补测试配置文件，例如：
     - `test/e2e/nginx.conf`
     - `test/e2e/Caddyfile`
     - `test/e2e/docker-compose.nginx.yml`
     - `test/e2e/docker-compose.caddy.yml`

这样做的原因：

- 避免污染开发机环境
- 避免不同机器上的本机 nginx / caddy 版本差异导致测试漂移
- 让代理配置、端口映射、网络拓扑都进入可复现的测试资产

如果 CI 环境暂时不支持 Docker，也不能把代理 E2E 降级成“可选”；至少要作为合并前或发布前的强制验证步骤，在支持 Docker 的环境中执行。

---

## 11. 验收标准

以下全部满足，才算迁移完成：

1. `go build ./...` 通过
2. `go test ./...` 通过
3. `go vet ./...` 通过
4. 直连首次建链 E2E 通过
5. 直连空闲保活 E2E 通过
6. 直连数据通道断开恢复 E2E 通过
7. nginx 首次建链 E2E 通过
8. nginx 空闲保活 E2E 通过
9. nginx 数据通道断开恢复 E2E 通过
10. caddy 首次建链 E2E 通过
11. caddy 空闲保活 E2E 通过
12. caddy 数据通道断开恢复 E2E 通过
13. `internal/server/peek.go` 已删除
14. 代码中不再存在 `DataChannelMagic`
15. `/ws/data` 非 Upgrade 请求返回 `426`
16. 数据通道 Upgrader / Dialer 已显式设置 buffer sizes 与 `EnableCompression = false`
17. 数据通道运行期独立断开会触发整会话重连，而不是留下伪在线状态
18. 只有 `Live` 会话才会出现在在线视图中，`client_online` 只会在数据通道 ready 后发布
19. `restoreTunnels()` 只会在 `PendingData -> Live` 成功后启动，旧 generation 的 restore 不会污染当前代际
20. retryable reconnect 不会错误清除本地有效 token
21. `Shutdown()` 在存在活跃数据 WebSocket 时仍能及时返回
22. 已补充 `BenchmarkDataChannelTransport_YamuxOverPipe_vs_WSConn` 或同等级基准，并记录结果
23. 已完成 `TestSoak_DataChannel_IdleAndTraffic` 或同等级 soak 验证，并确认无异常重连、无伪在线、无 handler 泄漏
24. `README.md`、架构说明、启动日志已同步为 `/ws/data`
25. `PendingData` 有显式超时回收，不会留下不可见但占位的 zombie session
26. `probe_report` / `proxy_new` / `proxy_close` 等副作用控制消息在 stale generation 或 `Closing` 状态下不会污染共享状态
27. 控制通道认证结果已提供 machine-readable `Code` / `Retryable` / `ClearToken`，Client 不再依赖字符串匹配做重连与清 token 决策
28. 数据通道 1 字节 `OK` 握手响应先于 `Live` 可见化与 `dataSession` 发布

---

## 12. 工作量重估

原估算仍然偏低，需要继续上调。

| 任务 | 预估 |
|------|------|
| 新建 `pkg/mux/wsconn.go` + 测试 | 2h |
| 新建/整理 `pkg/protocol/data_channel.go` + 测试 | 1h |
| 扩展控制通道认证响应（machine-readable auth result） | 1h |
| 重写 `internal/server/data.go` | 2h |
| 服务端 `PendingData/Live/Closing` 状态模型 + generation helper | 2h |
| `PendingData` 超时回收 + 控制消息 currentness 收敛 | 1.5h |
| 修改 `internal/server/server.go` | 2h |
| 重写 `internal/client/client.go` 中的数据通道建立、逻辑会话失效处理与 token 语义 | 2.5h |
| 删除 `peek.go` 并清理相关引用 | 0.5h |
| 增加测试可覆盖的时间参数注入点 | 1.5h |
| 重写 `internal/server/data_test.go` | 2h |
| 改造 `internal/client/client_test.go` | 2h |
| 改造 `internal/client/client_tls_test.go` | 1h |
| 改造 `internal/server/server_test.go` | 2h |
| 直连 E2E（首次 / 空闲 / 重连恢复） | 1.5h |
| nginx E2E（首次 / 空闲 / 重连恢复） | 2h |
| caddy E2E（首次 / 空闲 / 重连恢复） | 2h |
| 基准 / soak 验证与结果整理 | 2h |
| README / 文档同步 | 0.5h |
| **合计** | **~31h** |

---

## 13. 最终结论

在当前明确约束下，这个迁移方案仍然是可行的，但必须按下面五条执行：

1. **协议边界要收敛清楚**
   - 删除 magic
   - 保留共享握手状态码
   - 握手 helper 统一放协议层
   - 写清 HTTP 失败、WebSocket 失败、应用层握手失败三类语义

2. **实现语义要按 WebSocket 真实规则来写**
   - `SetReadLimit` 在 `Conn` 上设，不在 `Upgrader` 上设
   - `Close` 用 `WriteControl`
   - `WSConn` 适配的是字节流，不是“每帧一消息”的理想化模型
   - Client dialer 必须显式设置握手超时，且从 `DefaultDialer` 派生

3. **逻辑会话必须显式建模成 `PendingData / Live / Closing`，并带 generation**
   - 不再接受“控制在线、数据已死”的运行状态
   - `client_online` / 在线视图 / `restoreTunnels()` 只能绑定 `Live`
   - `PendingData` 必须有超时回收，不能留下 zombie session
   - 旧 generation 的 cleanup / restore / data-exit / 副作用控制消息必须 no-op，不得误伤当前连接

4. **重连语义必须把“认证失败”和“数据面可重试失败”分开**
   - 数据通道启动失败或运行期断开，都要触发整会话重建
   - 但 retryable reconnect 不得误清本地 token
   - 旧会话处于 `Closing` 时，新 token 认证不应被按并发登录拒绝
   - 控制通道认证结果必须提供 machine-readable `Code` / `Retryable` / `ClearToken`

5. **验证必须覆盖直连 + nginx + caddy 的首次建链、空闲保活、断线恢复和性能基线**
   - 这不是可选验收
   - 这是本次迁移存在的根因验证

如果按这版 RFC 实施，迁移风险是可控的；如果缺少“显式状态模型 / generation currentness / token 重连语义 / 空闲与断线恢复验证 / 基准验证”，则风险不可接受。
