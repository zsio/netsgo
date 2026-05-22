# Unified Tunnel 统一运行态实施计划（未实现）

日期：2026-05-22

## 状态声明

本文描述下一轮要一次性实现的统一隧道运行态和 client-to-client 能力，不代表当前版本已经支持这些能力。

当前可用能力仍以 `server_expose + server_relay_only` 为主：

- 外部用户连接服务端上的 TCP、UDP 或 HTTP 入口。
- 服务端通过目标客户端的数据通道中转流量。
- 目标客户端连接自己本地或局域网内的 TCP/UDP 服务。

下一轮不是只补一个 client-to-client 分支，而是把现有 server-expose 和新增 client-to-client 合成一套机制：

- 一套 `TunnelSpec`
- 一套 desired/runtime 状态模型
- 一套内存运行态 issues
- 一套 reconcile 调度
- 一套创建、更新、启动、停止、删除语义
- 一套客户端 provision/unprovision/report/preflight 控制消息
- 一套字段级 API 错误和 Web 展示口径

不保留“server-expose 一套逻辑、client-to-client 另一套逻辑”的并行实现。

## 本次实现范围

必须一次性纳入统一机制的隧道类型：

- `server_expose + tcp_listen -> tcp_service`
- `server_expose + udp_listen -> udp_service`
- `server_expose + http_host -> tcp_service`
- `client_to_client + tcp_listen -> tcp_service`
- `client_to_client + udp_listen -> udp_service`

本次只支持 `transport_policy = server_relay_only`。

本次不做：

- client-to-client HTTP host 入口。
- TCP 或 UDP 的 peer-direct 传输。
- NAT traversal、ICE、STUN 或 TURN。
- `direct_preferred` 或 `direct_only` 可用路径。
- 目标服务健康检查。
- 主动探测用户后端服务。
- `unix_socket`、`static_file`、`serial_device` 等未来 endpoint 类型。
- 多节点或分布式服务端协调。
- 客户端本地配置主动创建跨客户端隧道。

NetsGo 只报告自己负责的链路和运行态健康，不报告用户目标业务服务健康。

## 用户语义

面向用户的命名应避免直接使用 ingress/target 这类工程词。

用户可见概念：

- 服务来源客户端：即使没有建立隧道，也能访问真实目标服务的客户端。
- 访问入口客户端：负责在自己机器上监听入口端口，供本机或局域网访问。
- 目标服务地址：从服务来源客户端视角可访问的地址，例如 `a2:8080`。
- 入口监听地址：从访问入口视角监听的地址，例如 `127.0.0.1:18080` 或 `0.0.0.0:18080`。

client-to-client 示例：

- A 客户端部署在 `a1`。
- A 所在局域网内有 `a2:8080`。
- B 客户端部署在 `b1`。
- B 所在局域网内的 `b2` 希望访问 `b1:18080`，最终到达 `a2:8080`。

这条隧道语义上是：A 把自己能访问到的服务投放到 B 的访问入口。因此：

- owner 属于服务来源客户端 A。
- 内部 `target.client_id` 等于 owner。
- 内部 `ingress.client_id` 是访问入口客户端 B。
- 这与现有中心化隧道保持一致：都是服务来源客户端把服务投放出去。

## Owner 和参与端

统一 owner 规则：

- `server_expose`：owner 是服务来源客户端，即 `target.client_id`。
- `client_to_client`：owner 也是服务来源客户端，即 `target.client_id`。

client-to-client 额外规则：

- `ingress.client_id` 是访问入口客户端。
- `target.client_id` 是服务来源客户端。
- `ingress.client_id != target.client_id`，不允许同一客户端同时作为服务来源和访问入口。

Web 规则：

- 选择访问入口客户端时，应排除当前选中的服务来源客户端。
- 服务端仍必须做同样校验，不能只依赖前端。

## 客户端身份和能力

所有出现在 `ingress.client_id` 或 `target.client_id` 的客户端都必须是服务端已知的稳定客户端。`server_expose` 的 ingress 在服务端，不需要也不能提交 ingress client ID。

capabilities 校验按角色执行：

- `server_expose`：只校验服务来源客户端的 target 能力，例如 `tcp_service`、`udp_service`。
- `client_to_client`：访问入口客户端校验 ingress 能力，例如 `tcp_listen`、`udp_listen`；服务来源客户端校验 target 能力，例如 `tcp_service`、`udp_service`。
- 服务端自己的 TCP/UDP/HTTP 入口能力不走客户端 capabilities，而由服务端配置校验和 preflight 负责。
- 没有 capabilities 的客户端视为能力不满足。

## 持久化边界

持久化的是配置和用户意图，不是旧运行结果。

应该持久化：

- 稳定 tunnel ID
- name
- topology
- owner/ingress/target
- ingress/target endpoint 配置
- transport policy
- desired state
- revision
- 创建/更新时间

`revision` 是隧道配置版本号。每次配置更新都必须递增；客户端 ACK、runtime report 和数据流头都必须带当前 revision。旧 revision 的 ACK、report 或数据流不能影响当前配置。

不作为权威持久化：

- runtime_state
- issues
- 参与端在线状态
- ACK 等待状态
- listener/router 是否已持有
- relay stream 状态
- 客户端控制通道和数据通道状态

如果现有表里仍有 `runtime_state` 字段，可以暂时保留，但它不能作为权威状态。服务端启动后，所有 `desired_state = running` 的隧道都必须重新 reconcile，以新结果为准，不能从数据库恢复旧 active。

issues 不写 SQLite。它们是运行态内存快照加实时计算结果：

- 服务端重启后旧 issues 全部丢弃。
- running 隧道重新尝试连接。
- 新错误以重启后的实际尝试结果为准。
- 每分钟重试期间内存里保留最新 issues。
- Web 只看到最新状态，不需要知道后台正在第几次重试。

## 状态模型

总状态保持简单：

- `idle`
- `offline`
- `pending`
- `error`
- `active`

状态优先级：

1. `idle`：用户已停止。
2. `offline`：任一必要客户端离线，或控制通道/数据通道不可用。
3. `error`：客户端在线但 capabilities 不满足。
4. `pending`：必要客户端在线、数据通道可用，正在下发配置或等待 ACK。
5. `error`：入口监听、host 注册、provisioning、relay stream 等 NetsGo 自身能力失败。
6. `active`：NetsGo 自身入口、控制通道、数据通道、provisioning 都 ready。

服务端启动后的展示规则：

- stopped：`idle`
- running + 必要客户端离线/数据通道不可用：`offline`
- running + 必要客户端具备下发条件但还在收敛：`pending`
- 尝试成功：`active`
- 尝试失败：`error`

`active` 不代表目标服务可达，也不代表已经跑过测试流量。它只代表 NetsGo 自己负责的入口、控制通道、数据通道和 provisioning 都 ready。

active 的进入顺序必须避免循环定义：先满足除入口资源外的前置条件，再由 reconcile 尝试持有入口资源；入口端口监听或 HTTP 路由注册成功后，隧道才进入 `active`。如果入口资源持有失败，隧道进入 `error` 并写入内存 issues。

不能主动建空连接验证 relay path。实际 relay stream 在真实流量到来时建立。

## Issues 模型

issues 是数组，但只表达 NetsGo 自己负责的问题。

每个 issue 建议结构：

```json
{
  "code": "ingress_port_in_use",
  "scope": "ingress_client",
  "client_id": "client-b",
  "severity": "error",
  "message": "访问入口客户端端口已被占用",
  "retryable": true,
  "observed_at": "2026-05-22T10:00:00Z",
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

应进入 issues 的问题：

- 入口监听失败。
- 入口端口被系统其他进程占用。
- HTTP host 路由注册失败。
- provisioning ACK 超时。
- provisioning ACK 被拒绝。
- capabilities 不满足。
- 当前配置 revision 不一致。
- 服务端无法打开到目标客户端的数据流。
- client-to-client 中访问入口客户端无法打开到服务端的数据流。

不进入 issues 的问题：

- 目标服务连接失败。
- 目标 HTTP 返回 500。
- 目标 UDP 服务是否响应。
- 任何需要主动 probe 才能判断的业务服务健康问题。

目标服务连接失败只关闭当前连接/请求，并按现有日志体系输出文本日志。NetsGo 不应把目标业务服务是否健康写入隧道状态。

如果客户端离线或数据通道不可用，之前在线时产生的端口占用、监听失败、ACK 超时等旧 issue 不再展示。离线是当前事实，旧在线运行结果已经不成立。客户端重新上线或数据通道恢复后，应先清空该客户端相关旧 issues，再按当前配置重新尝试。

客户端 runtime report 只能作为事实输入，不能直接决定全局状态。服务端仍是状态权威，必须结合 desired state、当前连接事实、capabilities、revision、入口资源和 report 重新计算 runtime state 与 issues。

客户端 report 必须带 tunnel ID、revision 和 role。服务端只接受当前配置版本和正确角色的 report，旧 report 忽略或只打日志。

## Web 展示

Web 端应紧凑展示问题，避免 UI 噪音。

列表展示：

- 状态 badge 和原因摘要分开。
- 只显示最高优先级问题的一句摘要。
- 如果还有更多问题，显示 `+N` 或等价紧凑标记。

完整 issues：

- 放在弹窗、popover、tooltip 或类似 tip 中。
- 第一版只做简单文案，不做配图。
- 后端返回完整 issues，前端负责紧凑呈现和排序。

issue 摘要优先级：

1. 客户端离线 / 数据通道不可用这类实时阻断。
2. capabilities 不满足。
3. 入口资源失败：端口占用、监听失败、host 注册失败。
4. provisioning 失败：ACK 拒绝、ACK 超时。
5. relay stream 打开失败。
6. 其他 warning/info。

## 入口资源语义

入口资源包括：

- server-expose TCP/UDP 的服务端端口。
- server-expose HTTP 的 host/domain 路由。
- client-to-client TCP/UDP 的访问入口客户端 bind IP + port。

入口资源唯一性是配置级约束，不是 runtime listener 是否存在的副作用。

即使隧道 stopped/offline，配置层也不允许重复入口：

- server-expose TCP/UDP：同一服务端端口和协议不能重复。
- server-expose HTTP：host/domain 路由不能重复。
- client-to-client：同一访问入口客户端、同一协议、同一端口下，相同具体 `bind_ip` 冲突；`0.0.0.0:port` 与同端口任意 IPv4 bind 冲突。

入口资源只在隧道整体具备 active 条件时持有：

- server-expose：目标客户端离线时，不继续暴露 TCP/UDP/HTTP 入口。
- client-to-client：服务来源客户端离线时，访问入口客户端不继续监听入口。
- 任意一端失败或超时，已成功的一端必须回滚 unprovision。
- 只有所有必要条件 ready 后，入口才保持开放。

入口资源持有顺序：先确认必要客户端在线、数据通道可用、capabilities 满足，并完成当前 revision 的 provisioning；然后尝试持有入口资源；入口资源持有成功后才进入 `active`。如果后续 reconcile 发现 active 隧道的 listener、HTTP route 或客户端本地 listener 句柄已经不存在，必须退出 active 并重新收敛。

server-expose HTTP 在目标客户端离线时不注册 host/domain 路由。外部请求按“没有此隧道”处理，不新增 offline tunnel 专属响应；具体返回完全等同于未配置该 host。

server-expose TCP/UDP 未 active 时不监听端口，外部表现为连接失败或端口未开放。

同一个服务来源客户端的同一个目标服务地址可以被多条隧道投放到不同入口。目标服务地址不做唯一性约束、不做 preflight、不做健康状态。

## 创建和更新

创建隧道只表示配置合法并已成功保存，不表示隧道已经联通。这条规则适用于所有 server-expose 和 client-to-client 隧道。

创建/更新前必须做：

- 配置级合法性校验。
- 入口资源唯一性校验。
- 能执行的入口资源 preflight。
- 客户端已知和 capabilities 校验。

创建成功后的运行态由 reconcile 决定：

- 用户创建为 stopped：`idle`
- 必要客户端离线或数据通道不可用：`offline`
- 必要客户端在线且正在下发：`pending`
- 下发或入口持有失败：`error`
- 全部 ready：`active`

更新语义：

- 更新配置后清空旧运行态 issues。
- 如果隧道原来是 running，保存成功后立即按新配置 reconcile。
- 如果隧道原来是 stopped，只保存配置，不自动启动。
- 运行中隧道更新时，必须先清理旧运行态，再按新配置启动。
- 客户端收到同一个 tunnel ID 的新配置时，必须先关闭旧 listener/运行态，再尝试新配置。
- 旧配置产生的 ACK、错误或超时不能影响新配置。

启动/恢复/停止语义：

- 启动或恢复时清空旧 issues，然后重新尝试收敛。
- 停止时清空 issues，只展示已停止/空闲状态。
- 删除时清理所有运行态，并删除配置。

## Preflight

preflight 只检查 NetsGo 自己负责的入口资源，不检查目标服务。

server-expose TCP/UDP：

- 服务端本地检查端口唯一性和真实监听可用性。
- 如果端口已被其他 NetsGo 隧道配置占用，创建/更新失败。
- 如果端口被系统其他进程占用，创建/更新失败。

server-expose HTTP：

- 服务端检查 host/domain 路由合法性和唯一性。
- 不连接目标 HTTP 服务。

client-to-client TCP/UDP：

- 入口监听地址由用户明确填写。
- UI placeholder 可以写 `127.0.0.1 / 0.0.0.0`。
- API 层 `bind_ip` 必填。
- 不由后端静默默认成 `127.0.0.1` 或 `0.0.0.0`。
- `0.0.0.0` 不做额外警告。
- 本次只接受 IPv4。
- 入口监听端口必须由用户指定，不做自动分配。
- 访问入口客户端在线时，通过控制通道发 preflight 请求。
- 请求必须带 `request_id`，响应用 `request_id` 匹配。
- 客户端临时尝试 bind 指定 `protocol + bind_ip + port`，然后立即释放。
- 超时时间使用 3 秒。

client-to-client 创建时：

- 访问入口客户端在线：preflight 必须完成。
- preflight 通过：允许创建。
- preflight 超时、拒绝或端口占用：创建失败，不生成隧道。
- 访问入口客户端离线：允许创建，等上线后由 reconcile 尝试真实监听。

client-to-client 更新时：

- 如果访问入口客户端、协议、监听 IP 或端口发生变化，且新的访问入口客户端在线，必须先做 preflight。
- preflight 通过：保存新配置，清空旧 issues，关闭旧运行态，再 reconcile。
- preflight 失败/超时/端口占用：更新失败，旧配置继续运行。
- 新访问入口客户端离线：允许更新，保存后展示未联通，等上线后自动尝试。

preflight 与真实监听之间的竞态可以接受：

- preflight 通过不代表隧道一定 active。
- 如果真实 provision 时监听失败，隧道进入 error，并写入内存 issues。
- 不引入端口预占用机制。

preflight 失败不写入 runtime issues，因为创建/更新失败时没有新的隧道运行态需要展示。错误直接作为 API 响应返回给 Web。

## Reconcile

需要一套统一的隧道运行态收敛机制，而不是零散分支。

服务端维护两类事实：

- 用户期望：`desired_state = running/stopped`
- 当前事实：客户端是否在线、控制通道是否可用、数据通道是否可用、capabilities 是否满足、当前配置是否已被必要客户端接受、入口资源是否真实持有成功

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
- 客户端 runtime report
- 服务端启动
- 每分钟定时重试

每次 reconcile 都必须检查 active 隧道的运行态句柄是否仍然存在，例如服务端 TCP/UDP listener、HTTP route、客户端入口 listener、目标端当前 revision provisioning 状态。句柄丢失说明隧道不再 active，必须重新收敛；但 reconcile 仍不得主动探测目标业务服务。

自动重试规则：

- 只处理 `desired_state = running` 的隧道。
- 每分钟扫描所有 running 隧道并执行幂等 reconcile。
- 已经 active 且配置和必要运行态一致时，不重复动作。
- offline 时保持 offline，不反复刷旧 issue。
- pending 超时后转为可重试 issue。
- error 且 retryable 时重新尝试。
- 配置 revision 变化后按新配置收敛。

半成功回滚：

- 如果一端 ACK 成功，另一端 ACK 失败或超时，已成功的一端必须被 unprovision。
- 不能保留半可用入口，避免用户误以为隧道可用。
- 下一次自动恢复时再按当前配置重新下发。

## 控制面目标

统一控制面需要支持这些消息语义：

- provision：服务端下发当前 tunnel ID、revision、role、spec。
- provision ack：客户端返回 tunnel ID、revision、role、accepted、message。
- unprovision：服务端要求客户端停止指定 tunnel ID/revision/role。
- runtime report：客户端报告当前 tunnel ID/revision/role 下的本地运行问题。
- preflight request/response：服务端要求客户端检查入口监听资源。

服务端必须拒绝 stale ACK 和 stale runtime report，并以当前 revision 为准。

server-expose：

- 服务来源客户端负责接收 relay stream 或 UDP frame，并连接目标服务地址。
- 服务端负责持有入口资源。
- provision 顺序是：先向服务来源客户端下发当前 revision 并等待 ACK；ACK 成功后服务端再监听 TCP/UDP 端口或注册 HTTP route；任一步失败都回滚已经完成的运行态。
- 如果目标客户端离线或数据通道不可用，服务端不得提前持有入口资源。

client-to-client：

- 访问入口客户端负责监听本地 TCP 或 UDP。
- 服务来源客户端负责接收 relay stream 或 UDP frame，并连接目标服务地址。

## 数据面行为

TCP client-to-client 数据流：

1. 访问入口客户端接受一个本地 TCP 连接。
2. 访问入口客户端带 `DataStreamHeader` 打开到服务端的数据流。
3. 服务端校验 tunnel ID、revision、source role、target role 和 transport。
4. 服务端打开到服务来源客户端的数据流。
5. 服务来源客户端连接配置好的目标 TCP 服务。
6. 服务端在访问入口流和服务来源流之间转发字节。

UDP client-to-client 数据流：

1. 访问入口客户端监听本地 UDP socket。
2. 访问入口客户端把 UDP datagram 封装成现有 UDP frame。
3. 服务端通过隧道数据路径把 frame 转发给服务来源客户端。
4. 服务来源客户端把 datagram 发给配置的目标 UDP 服务。
5. 反向 UDP 流量沿用同一 tunnel ID 和 transport 身份。

统一数据面要求：

- `DataStreamHeader` 继续作为唯一的数据流路由协议头。
- tunnel ID、revision、role 或 transport 不匹配时，必须拒绝数据流。
- 目标客户端必须拒绝未知、过期、已停止或 `direct_only` 的 relay stream。
- 流量统计必须记录稳定 tunnel ID 和 `server_relay` transport。
- UDP 正反向流量都要保留准确的隧道身份。
- 每条隧道的 UDP 状态要有边界，不能无上限增长。
- stop、delete、update 或客户端断线必须关闭 UDP listener 并清理运行态。
- UDP 会话状态必须有明确边界：默认每条隧道最多保留 4096 个 UDP association，单个 association 空闲 2 分钟后清理；超过上限时优先清理最久未使用的 association。

断线和请求失败：

- active 后目标客户端突然断线：当前流量失败关闭，隧道转 offline，入口资源释放，等待自动恢复。
- client-to-client 访问入口客户端断线：服务端关闭相关 relay 状态，隧道转 offline。
- HTTP 请求遇到目标客户端断线、relay path 不可用或建流失败时返回 `502 Bad Gateway`。
- offline 或未 active 的 server-expose HTTP 不注册 host 路由；请求按“没有此隧道”处理。
- TCP 没有 HTTP 状态码，直接关闭连接。
- UDP 丢弃当前 datagram 或清理会话。

relay 与目标服务边界：

- 服务端无法打开到目标客户端的数据流，属于 NetsGo 自身问题，应写入 issues。
- client-to-client 中访问入口客户端无法打开到服务端的数据流，也属于 NetsGo 自身问题，应写入 issues。
- 服务来源客户端连不上用户目标服务，不写 issues，只关闭当前连接/请求并输出文本日志。

## API 和前端

API 行为：

- 统一 create/update 接受 server-expose 和 client-to-client 的 `server_relay_only` 配置。
- 统一 create/update 继续拒绝 client-to-client HTTP ingress。
- 统一 create/update 继续拒绝 direct transport。
- create/update 失败时返回字段级错误，包含 `field`、`code` 和可展示 message。
- API 继续返回 `runtime_state` 字段，但该字段必须由当前事实与内存运行态实时计算得出，不盲信数据库旧 runtime_state。
- API 返回的 `issues` 同样来自实时计算和内存运行态，不从数据库读取旧 issues。
- 字段级错误 code 应稳定复用，至少覆盖：`unknown_client`、`capability_not_supported`、`same_ingress_and_target_client`、`invalid_bind_ip`、`ingress_resource_conflict`、`ingress_preflight_timeout`、`ingress_preflight_rejected`、`ingress_port_in_use`、`direct_transport_unavailable`、`unsupported_topology`、`unsupported_endpoint_type`。
- 按客户端角色查询时：
  - owner 能看到自己拥有的服务来源侧隧道。
  - ingress 能看到自己作为访问入口的隧道。
  - target 能看到自己作为服务来源的隧道。
  - related 能看到任意相关隧道。

前端行为：

- 创建和编辑隧道时允许选择：
  - server expose 或 client to client。
  - 服务来源客户端。
  - client-to-client 的访问入口客户端。
  - TCP、UDP 或 server-expose HTTP endpoint。
  - 入口监听地址和端口，或 HTTP host/domain。
  - 目标服务地址和端口。
  - 仅 server relay transport。
- 选择访问入口客户端时排除当前服务来源客户端。
- 隧道列表清楚展示服务来源客户端、访问入口位置、transport、runtime state 和紧凑问题摘要。
- 问题详情放在弹窗、popover、tooltip 或类似 tip 中。
- peer-direct 设计完成前，direct transport 控制项应隐藏或禁用。

## 验证计划

后端最小测试：

- server-expose TCP/UDP/HTTP 创建、更新、停止、恢复、删除。
- client-to-client TCP/UDP 创建、更新、停止、恢复、删除。
- server-expose 和 client-to-client 走同一套 reconcile 入口。
- owner 派生为服务来源客户端。
- client-to-client 服务来源客户端和访问入口客户端相同时被拒绝。
- 配置中出现的客户端必须是服务端已知稳定客户端，server-expose 不要求 ingress client。
- 客户端 capabilities 不满足时创建失败。
- client-to-client 入口 `bind_ip` 缺失或非 IPv4 时创建失败。
- server-expose TCP/UDP 服务端端口冲突或被系统占用时创建失败。
- server-expose HTTP host/domain 冲突时创建失败。
- client-to-client 同一访问入口客户端重复监听地址/端口冲突时创建失败。
- `0.0.0.0:port` 与同端口任意 IPv4 bind 冲突。
- 访问入口客户端在线时 preflight 成功后允许创建。
- preflight 端口占用、拒绝或超时时创建失败。
- 访问入口客户端离线时允许创建，并显示 offline。
- server-expose 服务来源客户端离线时允许创建，并显示 offline。
- 创建成功不等待目标客户端联通。
- 必要客户端在线且 provisioning 成功，入口资源持有成功后，隧道进入 active。
- 任意必要客户端离线时隧道不能进入 active，且旧在线 issues 不展示。
- 控制通道在线但数据通道未就绪时显示 offline。
- capabilities 不满足时显示 error，不进入 provisioning。
- 一端 ACK 成功、另一端失败或超时时，已成功的一端被 unprovision。
- stale provision ACK 被忽略。
- stale runtime report 被忽略。
- stale unified API update 返回 revision conflict。
- update 清空旧 issues，running 隧道立即 reconcile，stopped 隧道不自动启动。
- stop/resume 清空 issues，并按 desired state 收敛。
- 服务端启动后不恢复旧 active，running 隧道重新 reconcile。
- 每分钟 running 隧道幂等 reconcile。
- active 条件必须包含入口资源真实持有成功。
- target 离线时 server-expose TCP/UDP 不监听端口，HTTP 不注册路由。
- stopped/offline 隧道仍然参与入口资源配置唯一性校验。
- 同一目标服务地址允许投放到多个不同入口。
- tunnel ID、role、revision 或 transport 错误的数据流被拒绝。
- TCP relay 能双向传输字节。
- UDP relay 能双向传输 datagram。
- 服务端打开目标客户端 stream 失败写入 issues。
- 访问入口客户端打开服务端数据流失败通过 runtime report 写入 issues。
- 目标服务连接失败不写入 issues，只关闭当前连接并输出日志。
- HTTP relay path 不可用时返回 502。
- 流量统计使用稳定 tunnel ID 和 `server_relay`。
- stop/delete/update 会关闭入口监听、HTTP 路由和目标运行态。

前端最小测试：

- 能构造 server-expose TCP/UDP/HTTP 创建 payload。
- 能构造 client-to-client TCP/UDP 创建 payload。
- 创建表单使用服务来源客户端、访问入口客户端、目标服务地址、入口监听地址这套用户词汇。
- 访问入口客户端选项排除服务来源客户端。
- owner、ingress、target、related 视图能展示相关隧道。
- update 时保留 expected revision。
- 字段级 API 错误能落到正确表单字段。
- 列表只展示状态 badge、最高优先级问题摘要和 `+N`。
- 完整 issues 能在弹窗、popover、tooltip 或类似 tip 中展示。
- 不把 direct transport 暴露成可用能力。

端到端检查：

- 直连服务端路径。
- nginx 反向代理路径。
- caddy 反向代理路径。
- 已有 running 隧道时客户端重连。
- 服务端重启后恢复 desired running 隧道，但不能在重新 ready 前声称 active。
- 端口占用解除后，每分钟重试能自动恢复。
- target 断线后入口资源释放，恢复后自动重新持有入口资源。

## 完成标准

本次完成时应满足：

- server-expose TCP/UDP/HTTP 和 client-to-client TCP/UDP 都运行在同一套统一机制上。
- 用户可以在 Web 面板创建 TCP client-to-client 隧道，并让流量从访问入口客户端经服务端到达服务来源客户端可访问的目标服务。
- 用户可以在 Web 面板创建 UDP client-to-client 隧道，并让 datagram 从访问入口客户端经服务端到达服务来源客户端可访问的目标服务。
- 现有 server-expose 隧道不再使用平行的旧运行态逻辑。
- stop、resume、update、delete、reconnect、restart 和每分钟 retry 都能保持正确 desired state、runtime state 和内存 issues。
- runtime_state 和 issues 不作为数据库权威状态。
- 入口资源只在整体 active 条件满足时持有。
- UI 和 API 不声称 peer-direct 已可用。
- API 字段级错误能指导用户修正配置。
- 测试覆盖控制面、数据面、入口资源、preflight、reconcile、issues、存储边界、流量统计和基础前端 payload/view 行为。
