# 隧道生产可用性审查（2026-03-24）

## 0. 状态更新（2026-03-27）

自本审查文档写下后，主线又关闭了一批当时仍未收口的问题：

- loopback management host fallback 已改为**默认不放行**，只有显式开启 `AllowLoopbackManagementHost` 时才允许 `localhost / 127.0.0.1 / ::1` 作为管理面兜底入口
- TCP/UDP 已改成**必须填写明确的公网端口**，不再把 `remote_port = 0` 暴露为实际产品能力
- TCP listener 在 `Accept()` 异常退出时，现已补上运行态下沉链路：会把 tunnel 降级为 `running/error`，而不是只打日志后静默退出

因此，这份文档下面有一部分内容现在应视为**历史问题回顾**，不再是当前主线上的活问题。
当前仍然真正未闭环的重点主要是：

- `ready -> exposed` 语义仍然只代表“配置被接受 / 入口已建立”，不是“后端已验证可用”
- client 侧本地 backend dial 失败仍然只打日志，尚未稳定地下沉为 tunnel 运行态
- TCP/UDP 更完整的连接预算、deadline、错误计数、自愈/退避治理仍未建立
- 生产可用性证明仍缺更系统的端到端/手工冒烟闭环

## 1. 状态更新（2026-03-25）

本轮已完成一组“状态模型收敛”修复，用于关闭审查里“配置真值不统一 / 状态语义分裂”的主问题：

- 已把 tunnel 主模型统一到 `desired_state + runtime_state + error`
- 已从协议层、store、`GET /api/clients`、`tunnel_changed`、前端隧道展示/动作权限中移除 legacy `status` 依赖
- 已把运行时资源关闭与业务状态切换分离：`PauseProxy` / UDP runtime close 不再隐式顺手改业务状态
- 已把端口白名单影响列表 `affected_tunnels` 也改成返回双状态，而不是 tunnel `status`

本轮验证已完成：

- `go test ./internal/server ./pkg/protocol -count=1 -timeout 60s`
- `go test ./internal/server -run 'TestTunnelStore_.*|TestEmitTunnelChanged_.*' -count=1 -timeout 60s`
- `cd web && bun test src/lib/tunnel-model.test.ts`
- `cd web && bun run build`

当时仍未关闭的问题（其中部分已于后续主线修复）：

- `Host: localhost / 127.0.0.1 / ::1` 管理面兜底仍保留，属于当时的显式待审风险（**现已于后续主线收口**）
- `ready -> exposed` 仍偏“入口建立成功”，还不是“后端健康已验证”
- TCP/UDP 的超时治理、错误计数、自愈/退避还没进入这一轮
- 还没有补端到端/手工冒烟来证明三类 tunnel 的生产闭环

## 2. 审查结论

结论先给出：

- 当前三种隧道类型 `tcp` / `udp` / `http` 都还不应直接定义为“满足生产要求”。
- `http` 是三者里完成度最高的，但仍有明显阻断项。
- `tcp` 功能可用，但控制面成功不等于数据面可用。
- `udp` 更像“可工作的 UDP 转发能力”，而不是“通用生产级 UDP 隧道”。

整体上，问题的重心不是“功能缺失”，而是：

1. 状态机语义不够真实
2. 配置真值不统一
3. 运行时治理不足
4. 测试覆盖对生产风险证明不够

## 3. 审查范围与方法

本次审查主要基于：

- 协议与共享层：`pkg/protocol/`、`pkg/mux/`
- 服务端隧道与分发：`internal/server/`
- 客户端执行：`internal/client/`
- 前端配置入口：`web/src/components/custom/tunnel/TunnelDialog.tsx`
- 单元测试、集成测试、e2e 测试：`internal/server/*test.go`、`internal/client/*test.go`、`pkg/mux/*test.go`、`test/e2e/`

本地额外执行了：

```bash
go test ./internal/server ./internal/client ./pkg/mux ./pkg/protocol -timeout 60s
```

结果通过。

说明：

- 本次没有跑 `-tags=e2e` 的 Docker 用例。
- 现有 e2e 明显偏向 HTTP 路径，对 TCP / UDP 的生产风险证明不足。

## 4. 总体判断

### 4.1 不是“过度设计”，而是“闭环没闭上”

代码里已经有不少状态与流程，比如：

- `pending / active / paused / stopped / error`
- `create / pause / resume / stop / delete / restore`
- runtime 内存态 + store 持久化态 + client ACK + 数据通道

但关键问题在于：这些状态和流程还没有严格对应“公网入口此刻真的可靠可用”。

换句话说，系统看上去已经有完整管理面，但很多地方仍然是“功能打通态”，不是“生产语义闭环态”。

### 4.2 配置真值是分裂的

当前系统没有真正统一到“store 是配置真值、runtime 是执行态”的模型：

- `http` 已经明显向这个方向靠拢，存在离线 CRUD 特判
- `tcp/udp` 仍然强依赖 live client runtime
- 同一套 API，对不同隧道类型和不同在线状态，行为差异很大

这会让系统在运维、自动化、恢复、审计上都变得难以推理。

## 5. 全局问题

### 5.1 管理面 loopback fallback（已于后续主线收口）

相关代码：

- `internal/server/http_tunnel_proxy.go:26`
- `internal/server/http_tunnel_proxy.go:42`
- `internal/server/http_tunnel_proxy.go:74`
- `internal/server/http_tunnel_proxy.go:95`

历史现象：

- `hostDispatchHandler()` 先按业务 Host 找 HTTP 隧道
- 如果没命中，再看 `allowSetupRequest()` 或 `isManagementHost()`
- 旧实现曾把 `localhost / 127.0.0.1 / ::1` 直接视为管理 Host 兜底入口

需要特别说明：

- 这**不一定是误实现**
- 按当前项目语境，它很可能是为了开发环境、反代联调和防失联兜底而保留的后门

但即使如此，它仍然是一个需要明确记录的生产风险：

- 它削弱了“Host 决定管理面 / 业务面边界”的模型纯度
- 它不该被默认为“生产安全边界的一部分”
- 至少应该被显式说明为 trade-off，而不是隐式行为

当前结论：

- 这一条在后续主线中已经收口：
  - 默认不再让 loopback host 成为隐式管理入口
  - 只有显式开启 `AllowLoopbackManagementHost` 时才允许该兜底路径
- 因此它不再属于当前主线上的活风险项，但保留在本文档中作为历史问题回顾。

### 5.2 `ready -> active` 的语义失真

相关代码：

- `internal/client/client.go:1003`
- `internal/client/client.go:1019`
- `internal/server/tunnel_ready.go:103`
- `internal/server/tunnel_manager.go:48`
- `internal/server/tunnel_manager.go:125`
- `internal/server/tunnel_manager.go:314`

现象：

- server 下发 `proxy_new`
- client 收到后只是 `Store` 配置并立即回 `success=true`
- server 只要收到这个 ACK，就继续走 `active` / persist / restore 成功

问题在于：

- client 此时并没有验证本地服务真的可连
- 这个 ACK 代表的是“client 接受了配置”
- 不是“外部流量已经可以稳定打通”

这会导致：

- 控制面显示 `active`
- API 返回创建成功 / 恢复成功
- 第一笔真实流量仍然可能立刻失败

这个问题横跨 `tcp` 和 `http`，本质是全局状态机问题。

### 5.3 数据面故障不会可靠地下沉成隧道状态

相关代码：

- `internal/server/proxy.go:247`
- `internal/server/proxy.go:257`
- `internal/server/udp_proxy.go:133`
- `internal/server/udp_proxy.go:139`
- `internal/client/client.go:840`
- `internal/client/client.go:845`

当前状态：

- **TCP listener `Accept()` 异常退出** 已在后续主线中补上运行态下沉，不再只是打日志
- **UDP read loop 异常退出** 已有运行态 error 下沉链路
- **client 本地 dial backend 失败** 仍然只打日志，这是当前仍未解决的关键缺口

仍然缺失的是：

- tunnel 状态降级
- 错误计数
- 事件通知
- 自愈或退避

因此当前仍会出现典型的“伪 active”：

- 控制面/运行态未必能及时反映 client 本地 backend 实际不可达
- 第一笔真实流量仍可能在 client 侧失败后才暴露问题

### 5.4 端口自动分配与白名单设计（已于后续主线收口）

相关代码：

- `internal/server/proxy.go:45`
- `internal/server/proxy.go:131`
- `internal/server/proxy.go:145`
- `web/src/components/custom/tunnel/TunnelDialog.tsx:288`

历史现象：

- 前端曾把 `remote_port = 0` 暴露为“自动分配”
- 后端不会在白名单范围里主动选择端口，而是依赖 OS 分配后再校验

当前结论：

- 这一条在后续主线中已经收口：
  - TCP/UDP 现在要求显式 `remote_port`
  - 前端非 HTTP 隧道也不再暴露“自动分配公网端口”作为产品能力
- 因此它已不再是当前主线上的活问题。

### 5.5 配置真值与离线语义没有统一

相关代码：

- `internal/server/server.go:1421`
- `internal/server/server.go:1471`
- `internal/server/server.go:1528`
- `internal/server/server.go:1568`
- `internal/server/server.go:1622`
- `internal/server/tunnel_manager.go:441`

现象：

- `http` 有一套离线管理特判
- `tcp/udp` 离线时大多数操作直接退化为 `client not found`
- 相同 API，不同类型 / 不同在线态，行为差异明显

这会让“隧道配置”到底属于：

- live session
- persistent config
- 还是两者混合

变得很不清晰。

## 6. TCP 隧道问题

### 6.1 TCP 不应被判定为生产可用

关键阻断项：

1. 假 ready
2. 无连接治理
3. 无健康降级
4. runtime worker 生命周期不够严谨

### 6.2 控制面成功不等于 backend 可用

相关代码：

- `internal/client/client.go:1003`
- `internal/client/client.go:1012`
- `internal/server/tunnel_ready.go:121`
- `internal/server/tunnel_manager.go:55`

TCP 的首要问题不是“能不能建立 listener”，而是：

- 创建时没有验证 backend
- 恢复时没有验证 backend
- restore 时也没有验证 backend

这会把“配置被接受”和“隧道可用”混成一个状态。

### 6.3 公网入口没有连接预算和超时治理

相关代码：

- `internal/server/proxy.go:247`
- `internal/server/proxy.go:262`
- `internal/client/client.go:795`
- `pkg/mux/mux.go:61`

现状：

- 每个外部连接一个 goroutine
- 每个 stream 一个 goroutine
- `Relay()` 是无 deadline 的双向 `io.Copy`

缺失：

- 最大并发连接预算
- idle timeout
- read/write deadline
- 慢连接清理
- 过载拒绝

这类实现对于 demo 够用，但不是生产隧道入口的姿态。

### 6.4 client stream worker 没有完整纳入 runtime 生命周期

相关代码：

- `internal/client/client.go:472`
- `internal/client/client.go:499`
- `internal/client/client.go:795`
- `internal/client/client.go:389`

问题不是“立刻出 bug”，而是：

- runtime 边界不够清晰
- cleanup 等待的是主循环 goroutine
- 不是所有活跃 stream worker

在抖动、慢关闭、重连切代场景里，旧流量 worker 的收敛语义不够明确。

## 7. UDP 隧道问题

### 7.1 UDP 本质上是 `UDP-over-WebSocket/TCP` (用户确定不需要修改)

相关代码：

- `pkg/mux/udp_frame.go:13`
- `pkg/mux/udp_frame.go:53`
- `internal/server/udp_proxy.go:90`
- `internal/client/udp_handler.go:20`

这意味着：

- 它不保留原生 UDP 的丢包 / 乱序语义
- 反而会引入 TCP 的有序阻塞和抖动放大
- 用户确认过, 不需要修改,因为本项目主要目的是穿透和映射, 而不是提供一个高性能的 UDP 代理

### 7.2 单循环导致全隧道级 HoL 风险

相关代码：

- `internal/server/udp_proxy.go:112`
- `internal/server/udp_proxy.go:123`
- `internal/server/udp_proxy.go:159`
- `internal/server/udp_proxy.go:190`

`udpReadLoop` 明确依赖单 goroutine 模型，同时在热循环里做：

- 读 UDP
- 开 yamux stream
- 写帧到 stream

只要某个 session 背压、建流变慢或 client 卡顿，整个 UDP 隧道都会被拖慢。

### 7.3 会话模型容易被打满

相关代码：

- `internal/server/udp_proxy.go:85`
- `internal/server/udp_proxy.go:87`
- `internal/server/udp_proxy.go:144`
- `internal/server/udp_proxy.go:152`

现状：

- 会话按 `srcAddr.String()` 建
- 上限固定 `1024`
- 满了就丢包
- 没有配额、没有驱逐、没有速率限制

这对公网 UDP 入口来说，抗资源耗尽能力不够。

### 7.4 零长度 UDP datagram 被当成非法输入

相关代码：

- `pkg/mux/udp_frame.go:20`
- `pkg/mux/udp_frame.go:44`

零长度 UDP datagram 在语义上是合法的。
当前实现会把它视为协议错误。

## 8. HTTP 隧道问题

### 8.1 HTTP 是最接近生产的，但仍不达标

优点：

- Host 分发模型基本成形
- 管理面 / 业务面隔离思路是对的
- 反代场景已有一定 e2e
- offline HTTP 管理已经开始向持久化真值靠拢

但阻断项仍然明显。

### 8.2 HTTP 管理路径没有验证本地目标

相关代码：

- `internal/server/proxy.go:35`
- `internal/server/proxy.go:42`
- `internal/client/client.go:841`
- `internal/client/client.go:844`

对 `http` 类型，后端只校验：

- `domain`
- 域名冲突

没有校验：

- `local_ip` 是否为空或明显非法
- `local_port` 是否越界或为 0

因此控制面允许写入明显无效的 HTTP 配置。

### 8.3 `error` 隧道更新失败时 API 仍返回成功

相关代码：

- `internal/server/tunnel_manager.go:291`
- `internal/server/tunnel_manager.go:297`
- `internal/server/tunnel_manager.go:303`
- `internal/server/server.go:1692`
- `internal/server/server.go:1711`

这是一个明确的 API 语义 bug：

- `wasError` 分支里，重新拉起失败
- 函数返回的是 `errorConfig, nil`
- handler 因为 `err == nil`，仍然回 `200 + success=true`

这会误导 UI、自动化和审查者。

### 8.4 热路径仍是 O(N) 路由查找

相关代码：

- `internal/server/http_tunnel_proxy.go:102`
- `internal/server/http_tunnel_proxy.go:116`
- `internal/server/http_tunnel_proxy.go:131`
- `internal/server/http_tunnel.go:413`
- `internal/server/http_tunnel.go:421`

每个请求都要：

- 扫 runtime tunnels
- runtime 没命中再扫 store

对小规模系统问题不大，但作为生产 HTTP 入口，这个设计迟早要被 host 索引替代。

### 8.5 代理层每请求新建 `Transport/ReverseProxy`

相关代码：

- `internal/server/http_tunnel_proxy.go:171`
- `internal/server/http_tunnel_proxy.go:174`
- `internal/server/http_tunnel_proxy.go:176`
- `internal/server/http_tunnel_proxy.go:182`

现状：

- 每个请求新建 `Transport`
- 显式关闭 keep-alive
- 没有响应头超时

影响：

- 连接复用失效
- 慢后端会长期占住 goroutine / stream
- 代理栈性能和稳定性都偏“功能优先”

### 8.6 offline HTTP 状态机语义混合了“期望态”和“运行态”

相关代码：

- `internal/server/http_tunnel_proxy.go:20`
- `internal/server/server.go:1427`
- `internal/server/server.go:1489`
- `internal/server/server.go:1546`
- `internal/server/server.go:1574`
- `internal/server/tunnel_manager.go:530`

典型现象：

- store 中 `active` 的 offline HTTP 隧道，路由上其实不可服务
- 但它又代表“等 client 上线后应该恢复”
- 同时它允许 `update / pause / delete`
- 却不允许 `resume / stop`

这说明一个状态字段同时承担了：

- 期望配置态
- 运行可服务态

语义边界不够干净。

## 9. 测试空洞

### 9.1 TCP

- 没有真正的 server + real client + real backend 全链路 e2e
- 没有验证 backend 不可达时 create/resume/restore 应如何呈现
- 没有并发、慢连接、资源治理测试
- 现有 e2e 主要是 HTTP，不是 TCP

### 9.2 UDP

- 没有真实 client 代码链路 e2e
- 没有背压 / HoL / session 打满 / DoS 测试
- 没有零长度 datagram 测试
- 没有长时间 soak、抖动、丢包测试

### 9.3 HTTP

- 没有测试“假 ready”
- 没有测试 HTTP create/update 的本地目标非法输入
- 没有测试 `error` 隧道 update 失败却返回 success
- 没有测试 restore 后第一笔真实流量是否真的成功
- 没有慢后端、长连接、取消、body 上传、并发性能测试

## 10. 建议给二次审查者重点复核的问题

建议二次审查者重点回答下面几个问题：

1. 管理面 `Host: localhost` 兜底是否应保留到生产行为中
2. `ready` 的定义是否应该改成“client 已完成本地目标验证”
3. 是否要统一到“store 为配置真值，runtime 为执行态”
4. `remote_port = 0` 是否应真正变成“从允许范围内选端口”
5. TCP / HTTP 是否需要引入连接预算、deadline、过载拒绝
6. UDP 是否继续保留当前语义，还是明确降级为“实验性 / 功能型”
7. HTTP 路由是否需要 host 索引和长生命周期 transport
