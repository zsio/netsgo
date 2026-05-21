# Unified Tunnel 下一阶段实施计划（未实现）

日期：2026-05-21

## 状态声明

本文描述下一阶段要实现的 client-to-client 隧道能力，不代表当前版本已经支持该能力。

当前可用能力仍以 `server_expose + server_relay_only` 为主：

- 外部用户连接服务端上的 TCP、UDP 或 HTTP 入口。
- 服务端通过目标客户端的数据通道中转流量。
- 目标客户端连接自己本地或局域网内的 TCP/UDP 服务。

下一阶段目标是新增 **client-to-client over server relay**。这不是 P2P 直连，服务端仍然在数据路径中。

## 用户语义

面向用户的命名应避免直接使用 ingress/target 这类工程词。

用户可见概念：

- 服务来源客户端：即使没有建立隧道，也能访问真实目标服务的客户端。
- 访问入口客户端：负责在自己机器上监听入口端口，供本机或局域网访问。
- 目标服务地址：从服务来源客户端视角可访问的地址，例如 `a2:8080`。
- 入口监听地址：从访问入口客户端视角监听的地址，例如 `127.0.0.1:18080` 或 `0.0.0.0:18080`。

示例：

- A 客户端部署在 `a1`。
- A 所在局域网内有 `a2:8080`。
- B 客户端部署在 `b1`。
- B 所在局域网内的 `b2` 希望访问 `b1:18080`，最终到达 `a2:8080`。

这条隧道语义上是：A 把自己能访问到的服务投放到 B 的访问入口。因此：

- owner 属于服务来源客户端 A。
- 内部 `target.client_id` 等于 owner。
- 内部 `ingress.client_id` 是访问入口客户端 B。
- 这与现有中心化隧道保持一致：都是服务来源客户端把服务投放出去。

## 第一版支持范围

第一版只做：

- `topology = client_to_client`
- `transport_policy = server_relay_only`
- 访问入口：客户端侧 `tcp_listen` 或 `udp_listen`
- 服务来源：客户端侧 `tcp_service` 或 `udp_service`
- 入口监听地址仅接受 IPv4。
- 入口监听端口必须由用户指定，不做自动分配。

第一版不做：

- client-to-client HTTP host 入口。
- TCP 或 UDP 的 peer-direct 传输。
- NAT traversal、ICE、STUN 或 TURN。
- `direct_preferred` 或 `direct_only` 可用路径。
- 目标服务健康检查。
- 主动探测用户后端服务。
- `unix_socket`、`static_file`、`serial_device` 等未来 endpoint 类型。
- 多节点或分布式服务端协调。
- 客户端本地配置主动创建跨客户端隧道。

## 关键产品决策

### Owner

client-to-client 的 owner 是服务来源客户端，也就是“即使没有隧道也能访问目标服务”的客户端。

服务端规则：

- `owner_client_id = target.client_id`
- `ingress.client_id` 是访问入口客户端。
- `ingress.client_id != target.client_id`，不允许同一个客户端同时作为服务来源和访问入口。

Web 规则：

- 选择访问入口客户端时，应排除当前选中的服务来源客户端。
- 服务端仍必须做同样校验，不能只依赖前端。

### 入口监听地址

入口监听地址由用户明确填写。

- UI placeholder 可以写 `127.0.0.1 / 0.0.0.0`。
- API 层 `bind_ip` 必填。
- 不由后端静默默认成 `127.0.0.1` 或 `0.0.0.0`。
- `0.0.0.0` 不做额外警告。
- 第一版只接受 IPv4 地址。

### 入口端口冲突

同一个访问入口客户端上，不能存在重复入口监听。

冲突规则：

- 同一访问入口客户端。
- 同一协议 TCP/UDP。
- 同一端口。
- 相同具体 `bind_ip` 冲突。
- `0.0.0.0:port` 与同端口任意 IPv4 bind 冲突。

该冲突应在服务端配置层提前校验，而不是等客户端监听失败后才发现。

### 客户端选择与能力

创建 client-to-client 隧道时，两端客户端都必须是服务端已知的稳定客户端。

- 可以离线创建。
- 不能填写服务端从未见过的随机 client ID。
- 创建时必须校验客户端 capabilities。
- 如果服务来源客户端或访问入口客户端能力不足，创建直接失败。
- 没有 capabilities 也视为不满足。
- 如果创建后客户端降级或换成不支持的版本，运行态进入 error，latest issues 记录能力问题；后续 capabilities 恢复后自动恢复。

## 创建与更新语义

创建隧道只表示配置合法并已成功保存，不表示隧道已经联通。

这条规则同时适用于现有中心化隧道和新的 client-to-client 隧道：

- 创建成功：配置被服务端接受并持久化。
- 是否联通：由后续运行态和 reconcile 决定。
- Web 不应把“创建成功”理解成“隧道已可用”。

创建成功后的初始运行态应按当前事实计算：

- 用户创建为 stopped：`idle`
- 任意一端离线或数据通道不可用：`offline`
- 两端在线且数据通道可用：`pending`
- 配置非法：创建失败，不产生隧道
- 已知入口监听失败：由后续 reconcile 写成 `error`

更新语义：

- 更新配置后清空旧异常。
- 如果隧道原来是 running，保存成功后立即按新配置 reconcile。
- 如果隧道原来是 stopped，只保存配置，不自动启动。
- 运行中隧道更新时，必须先清理旧运行态，再按新配置启动。
- 客户端收到同一个 tunnel ID 的新配置时，必须先关闭旧 listener/运行态，再尝试新配置。
- 旧配置产生的 ACK、错误或超时不能影响新配置。

启动/恢复/停止语义：

- 启动或恢复时清空旧异常，然后重新尝试收敛。
- 停止时清空异常，只展示已停止/空闲状态。
- 删除时异常随隧道删除。

## Preflight 端口检查

访问入口客户端在线时，创建或更新前必须做入口端口 preflight。

preflight 范围：

- 只检查访问入口监听端口。
- 不检查目标服务端口。
- 不主动探测目标业务服务。

preflight 方式：

- 通过现有控制通道发给访问入口客户端。
- 请求必须带 `request_id`，响应用 `request_id` 匹配。
- 客户端临时尝试 bind 指定 `protocol + bind_ip + port`，然后立即释放。
- 超时时间使用 3 秒。

创建时：

- 访问入口客户端在线：preflight 必须完成。
- preflight 通过：允许创建。
- preflight 超时、拒绝或端口占用：创建失败，不生成隧道。
- 访问入口客户端离线：允许创建，等上线后由 reconcile 尝试真实监听。

更新时：

- 如果访问入口客户端、协议、监听 IP 或端口发生变化，且新的访问入口客户端在线，必须先做 preflight。
- preflight 通过：保存新配置，清空旧异常，关闭旧运行态，再 reconcile。
- preflight 失败/超时/端口占用：更新失败，旧配置继续运行。
- 新访问入口客户端离线：允许更新，保存后展示未联通，等上线后自动尝试。

preflight 与真实监听之间的竞态可以接受：

- preflight 通过不代表隧道一定 active。
- 如果真实 provision 时监听失败，隧道进入 error，并写入 latest issues。
- 不引入端口预占用机制。

preflight 失败不写入持久化 issues，因为创建/更新失败时没有新的隧道状态需要展示。错误直接作为 API 响应返回给 Web。

## 运行态收敛机制

需要一套统一的隧道运行态收敛机制，而不是零散分支。

服务端维护两类事实：

- 用户期望：`desired_state = running/stopped`
- 当前事实：客户端是否在线、控制通道是否可用、数据通道是否可用、当前配置是否已被两端接受、入口监听是否成功

所有触发点都调用同一套 reconcile：

- 创建
- 更新
- 启动
- 停止
- 删除
- 客户端上线
- 客户端断联
- 数据通道 ready/断开
- ACK 成功/拒绝/超时
- 服务端启动
- 每分钟定时重试

自动重试规则：

- 只处理 `desired_state = running` 的隧道。
- 每分钟扫描所有 running 隧道并执行幂等 reconcile。
- 已经 active 且配置和两端状态一致时，不重复动作。
- 离线时保持 offline，不反复刷无意义错误。
- pending 超时后转为可重试 issue。
- error 且 retryable 时重新尝试。
- 配置 revision 变化后按新配置收敛。

半成功回滚：

- 如果一端 ACK 成功，另一端 ACK 失败或超时，已成功的一端必须被 unprovision。
- 不能保留半可用的访问入口监听，避免用户误以为隧道可用。
- 下一次自动恢复时再按当前配置重新下发。

## 运行状态与 Issues

总状态仍保持简单：

- `active`
- `pending`
- `offline`
- `error`
- `idle`

状态计算原则：

- 能从当前连接事实直接推导出来的状态，不作为权威持久化。
- 客户端在线、控制通道、数据通道状态应实时计算。
- 不持久化参与端在线状态作为权威，避免过期状态误导。
- 只持久化不能从当前状态直接推导出来的 latest issues 快照。

latest issues 是当前状态快照：

- 不做版本历史。
- 不做审计日志。
- 只保留最新情况。
- 配置更新、启动、恢复、停止时清空旧 issues。
- 新配置产生新问题后再写入新的 issues。

每个 issue 建议结构：

```json
{
  "code": "ingress_port_in_use",
  "scope": "ingress_client",
  "client_id": "client-b",
  "severity": "error",
  "message": "访问入口客户端端口已被占用",
  "retryable": true,
  "observed_at": "2026-05-21T10:00:00Z",
  "details": {
    "bind_ip": "0.0.0.0",
    "port": 18080,
    "os_error": "address already in use"
  }
}
```

字段语义：

- `code`：机器可判断的错误类型。
- `scope`：问题归属，例如 `ingress_client`、`target_client`、`server`、`transport`。
- `client_id`：如果问题归属于某个客户端则填写。
- `severity`：`info`、`warning` 或 `error`。
- `message`：后端生成的默认展示文案，前端第一版可以直接展示。
- `retryable`：是否可由自动 retry 恢复。
- `observed_at`：最后一次观察到问题的时间。
- `details`：JSON 扩展字段，供后续 Web 图形、文案和诊断扩展使用。

第一版 issues 只记录 NetsGo 自己负责的链路和运行态问题，例如：

- 入口监听失败。
- 入口端口占用。
- provisioning ACK 超时。
- provisioning ACK 被拒绝。
- capabilities 不满足。
- 当前配置 revision 不一致。
- 服务端 relay 建流失败。

不写入 issues 的内容：

- 目标服务连接失败。
- 目标 HTTP 返回 500。
- 目标 UDP 服务是否响应。
- 任何需要主动 probe 才能判断的业务服务健康问题。

目标服务连接失败只关闭当前连接/请求，并按现有日志体系输出文本日志。NetsGo 不应把目标业务服务是否健康写入隧道状态。

## 控制面实现目标

服务端应能够为一条 client-to-client 隧道派生两端参与者：

- owner：服务来源客户端，即 `target.client_id`。
- ingress participant：访问入口客户端，即 `ingress.client_id`。
- target participant：服务来源客户端，即 `target.client_id`。

服务端持久化一条包含两端参与者的 tunnel spec，并按角色下发 provisioning：

- 访问入口客户端负责监听本地 TCP 或 UDP。
- 服务来源客户端负责接收 relay 过来的 stream 或 packet，并连接目标服务地址。

两端客户端 ACK 时必须带上服务端下发的 tunnel ID、revision 和 role。服务端必须拒绝 stale ACK，并以当前配置为准。

期望运行状态：

- 两端都 ACK 成功后，隧道进入 active。
- 任意一端拒绝或超时，隧道进入 error，并写入 latest issues。
- 如果用户希望 running，但任意一端离线，隧道不能进入 active，应保持 offline。
- stop 和 delete 必须让两端都 unprovision。
- update 必须产生新 revision，并拒绝 stale update。

## TCP Client-To-Client Relay

TCP 数据流：

1. 访问入口客户端接受一个本地 TCP 连接。
2. 访问入口客户端带 `DataStreamHeader` 打开到服务端的数据流。
3. 服务端校验 tunnel ID、revision、source role、target role 和 transport。
4. 服务端打开到服务来源客户端的数据流。
5. 服务来源客户端连接配置好的目标 TCP 服务。
6. 服务端在访问入口流和服务来源流之间转发字节。

必须满足：

- tunnel ID、revision、role 或 transport 不匹配时，服务端必须拒绝数据流。
- 服务来源客户端必须拒绝未知、过期、已停止或 `direct_only` 的 relay stream。
- 入口监听失败时，隧道 runtime 进入 error，并写入 latest issues。
- 目标连接失败只影响当前连接，并输出文本日志；不要提前探测目标服务，不写入隧道 issues。
- 流量统计必须记录稳定 tunnel ID 和 `server_relay` transport。

## UDP Client-To-Client Relay

UDP 在 TCP 稳定后实现。

UDP 数据流：

1. 访问入口客户端监听本地 UDP socket。
2. 访问入口客户端把 UDP datagram 封装成现有 UDP frame。
3. 服务端通过隧道数据路径把 frame 转发给服务来源客户端。
4. 服务来源客户端把 datagram 发给配置的目标 UDP 服务。
5. 反向 UDP 流量沿用同一 tunnel ID 和 transport 身份。

必须满足：

- UDP 正反向流量都要保留准确的隧道身份。
- 每条隧道的 UDP 状态要有边界，不能无上限增长。
- stop、delete、update 或客户端断线必须关闭 UDP listener 并清理运行态。
- UDP 目标错误只能来自实际流量处理，不能通过主动探测目标服务制造状态。

## API 和前端

API 行为：

- 统一 create/update 接受 `client_to_client + server_relay_only`。
- 统一 create/update 继续拒绝 client-to-client HTTP ingress。
- 统一 create/update 继续拒绝 direct transport。
- create/update 失败时返回字段级错误，包含 `field`、`code` 和可展示 message。
- 按客户端角色查询时：
  - owner 能看到自己拥有的服务来源侧隧道。
  - ingress 能看到自己作为访问入口的隧道。
  - target 能看到自己作为服务来源的隧道。
  - related 能看到任意相关隧道。

前端行为：

- 创建和编辑隧道时允许选择：
  - server expose 或 client to client。
  - 服务来源客户端。
  - 访问入口客户端。
  - TCP 或 UDP endpoint。
  - 入口监听地址和端口。
  - 目标服务地址和端口。
  - 仅 server relay transport。
- 选择访问入口客户端时排除当前服务来源客户端。
- 隧道列表清楚展示服务来源客户端、访问入口客户端、transport、runtime state 和 issues message。
- 第一版只展示简单异常文案，不做配图。
- peer-direct 设计完成前，direct transport 控制项应隐藏或禁用。

## 验证计划

后端最小测试：

- 创建、更新、停止、恢复、删除 TCP client-to-client 隧道。
- 创建、更新、停止、恢复、删除 UDP client-to-client 隧道。
- owner 派生为服务来源客户端。
- 服务来源客户端和访问入口客户端相同时被拒绝。
- 两端都必须是服务端已知客户端。
- 客户端 capabilities 不满足时创建失败。
- 入口 `bind_ip` 缺失或非 IPv4 时创建失败。
- 同一访问入口客户端重复监听地址/端口冲突时创建失败。
- `0.0.0.0:port` 与同端口任意 IPv4 bind 冲突。
- 访问入口客户端在线时 preflight 成功后允许创建。
- preflight 端口占用、拒绝或超时时创建失败。
- 访问入口客户端离线时允许创建，并显示 offline。
- 两端都在线时 provisioning 成功，隧道进入 active。
- 任意一端离线时隧道不能进入 active。
- 一端 ACK 成功、另一端失败或超时时，已成功的一端被 unprovision。
- stale provision ACK 被忽略。
- stale unified API update 返回 revision conflict。
- update 清空旧 issues，running 隧道立即 reconcile，stopped 隧道不自动启动。
- stop/resume 清空 issues，并按 desired state 收敛。
- 每分钟 running 隧道幂等 reconcile。
- tunnel ID、role、revision 或 transport 错误的数据流被拒绝。
- TCP relay 能双向传输字节。
- UDP relay 能双向传输 datagram。
- 目标连接失败不写入 issues，只关闭当前连接并输出日志。
- 流量统计使用稳定 tunnel ID 和 `server_relay`。
- stop/delete/update 会关闭入口监听和目标运行态。

前端最小测试：

- 能构造 TCP 和 UDP client-to-client 创建 payload。
- 创建表单使用服务来源客户端、访问入口客户端、目标服务地址、入口监听地址这套用户词汇。
- 访问入口客户端选项排除服务来源客户端。
- owner、ingress、target、related 视图能展示 client-to-client 隧道。
- update 时保留 expected revision。
- 字段级 API 错误能落到正确表单字段。
- issues message 能在列表或详情中展示。
- 不把 direct transport 暴露成可用能力。

端到端检查：

- 直连服务端路径。
- nginx 反向代理路径。
- caddy 反向代理路径。
- 已有 running client-to-client 隧道时客户端重连。
- 服务端重启后恢复 desired running 隧道，但不能在两端都 ready 前声称 active。
- 端口占用解除后，每分钟重试能自动恢复。

## 完成标准

本阶段完成时应满足：

- 用户可以在 Web 面板创建 TCP client-to-client 隧道，并让流量从访问入口客户端经服务端到达服务来源客户端可访问的目标服务。
- 用户可以在 Web 面板创建 UDP client-to-client 隧道，并让 datagram 从访问入口客户端经服务端到达服务来源客户端可访问的目标服务。
- stop、resume、update、delete、reconnect、restart 和每分钟 retry 都能保持正确 desired state、runtime state 和 latest issues。
- UI 和 API 不声称 peer-direct 已可用。
- API 字段级错误能指导用户修正配置。
- 测试覆盖控制面、数据面、存储、流量统计、preflight、reconcile、issues 和基础前端 payload/view 行为。
