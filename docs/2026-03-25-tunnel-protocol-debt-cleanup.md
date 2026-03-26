# Tunnel Protocol Debt Cleanup

## 这份文档是干什么的

这份文档是写给后续接手同事看的。

目的不是立刻改代码，而是先把一件事说清楚：

**当前 tunnel 控制协议已经进入“半迁移状态”，这是一笔明确的协议债，值得单独治理。**

同时，这项工作要和“当前 PR 的 CI 修复”分开推进，不要混在同一轮里做。

## 一句话结论

- 当前 PR 的目标是修 CI，已经做完，可以先合并。
- 但 tunnel 控制协议本身还有一项后续工作要做。
- 这项后续工作就是：**统一 create / provision / ack 的协议定义、命名、实现和测试。**

## 如果只看一眼，请先记住这 4 句话

1. 当前问题不是单点 bug，而是 **协议已经半迁移**。
2. 现在系统里同时存在旧消息和新消息，两边没有完全收口。
3. 这次 CI 回归已经证明：这不是“风格问题”，而是会变成真实功能故障。
4. 当前 PR 已经先把 CI 修好了，但**协议治理本身还没有做完**，需要后续单独推进。

## 当前状态 vs 理想状态

| 维度 | 当前状态 | 理想状态 |
| --- | --- | --- |
| 共享协议定义 | 还主要是 `proxy_new` / `proxy_new_resp` | `pkg/protocol` 成为唯一协议真相 |
| client 实现 | 已经私有引入 `proxy_create` / `proxy_provision` / `proxy_provision_ack` | 不再私有定义消息类型，直接使用共享协议 |
| server 实现 | 一部分旧语义，一部分兼容新语义 | create / provision / ack 路径都按统一语义处理 |
| waiter / ACK 命名 | 仍然混用 `ready` / `response` | create-result 与 provisioning-ack 命名彻底分开 |
| 测试 | client/server/protocol 对新旧路径覆盖不对称 | 三层测试都围绕同一套协议模型 |

## 这笔债会造成什么实际问题

最直观的问题不是“以后不好看”，而是下面这些真实风险：

- 某一侧已经切到新消息，另一侧还停在旧消息，结果两边互相听不懂。
- 日志里写的是 `ready`，但代码里实际表达的是 “接受配置 ACK”，调试时很容易误判。
- 测试覆盖新旧混杂，导致同样一个路径在 client 侧和 server 侧说法不一致。
- 后续同事不知道哪一套才是正式协议，只能靠读实现猜。

## 一个已经发生过的真实例子

这次 CI 里，`TestE2E_TCPProxyTunnel` 三平台同时失败，就是协议债已经变成真实故障的例子。

当时发生的事情很简单：

1. client 主动创建 tunnel 时，发的是 `proxy_create`
2. server 还主要只按 `proxy_new` 理解
3. 结果就是：
   - server 看不懂 client 发的消息
   - tunnel 没建立起来
   - E2E 测试直接失败

所以这件事已经不是“代码风格想不想统一”，而是**系统边界已经出现真实不一致**。

## 当前问题到底是什么

表面上看，问题像是“消息名字不统一”。

实际上更严重一点：

**共享协议层、client 实现、server 实现、等待/ACK 逻辑、测试，已经不再完全使用同一套 tunnel 控制语义。**

也就是说，系统里本来应该只有一份协议真相，但现在实际上变成了两套半。

## 当前系统里发生了什么

### 1. 共享协议层还是旧消息

在 [message.go](/Users/dyy/projects/code/netsgo/pkg/protocol/message.go) 里，目前公开定义的仍然是：

- `proxy_new`
- `proxy_new_resp`

这意味着从“协议定义层”看，系统还是旧模型。

### 2. client 已经长出了新消息

在 [client.go](/Users/dyy/projects/code/netsgo/internal/client/client.go) 里，client 自己已经开始使用：

- `proxy_create`
- `proxy_create_resp`
- `proxy_provision`
- `proxy_provision_ack`

但这些新消息类型没有正式进入共享协议层。

这意味着：

- client 已经开始讲“新语言”
- 但协议词典还没更新

### 3. server 还是以旧消息为主

在 [server.go](/Users/dyy/projects/code/netsgo/internal/server/server.go) 里，server 当前主要还是按旧的 `proxy_new` / `proxy_new_resp` 处理控制消息。

这就会导致一种典型问题：

- client 以为自己在发新的 create 消息
- server 还在按旧消息理解

这次 CI 里 `TestE2E_TCPProxyTunnel` 三平台同时失败，本质上就已经说明这不是“风格问题”，而是实际回归风险。

### 4. server -> client 的 provisioning 也还在旧语义里

在 [tunnel_manager.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_manager.go) 里，`notifyClientProxyNew()` 仍然使用旧 `MsgTypeProxyNew` 给 client 下发 tunnel 配置。

所以现在不是只有“client 主动创建”这条路径半迁移，而是：

- client 主动创建路径，开始用新语义
- server 下发 provisioning 路径，仍在旧语义

整体上就是一个典型的半迁移协议系统。

### 5. 等待逻辑和命名也还是旧语义

在 [tunnel_ready.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_ready.go) 里，像这些名字：

- `waitForTunnelReady`
- `pendingReady`
- `ProxyNewResponse`

仍然把下面几件事混在一起：

- create result
- provision accepted
- ready
- ack

这会持续制造理解成本。

问题不只是“消息常量”，还有**命名和语义模型**。

### 6. 测试也已经新旧混用

现在 client 侧测试已经有一部分在测新路径，server 侧测试很多地方还主要围绕旧路径。

这说明系统不是“还没开始迁移”，而是已经迁到一半。

这正是协议债最典型的形态。

## 为什么这件事值得单独做

因为这不是一个修一两个 if 就能收尾的问题。

它至少涉及 4 层同时收口：

1. 共享协议定义
2. client / server 实现
3. waiter / ack / ready 语义命名
4. client / server / protocol 测试

如果只改其中一层，债不会消失，只会继续藏着。

## 这次明确不准备一起做的内容

为了避免后续同事接手时 scope 再次失控，这里把“不在本次协议治理里顺手做”的东西也写清楚：

- 不顺手重做 tunnel lifecycle 状态机
- 不顺手改链路层健康语义
- 不顺手做目标服务健康探测
- 不顺手重构无关的控制通道消息（例如 auth / ping / probe）
- 不把这项工作和“修 CI 红叉”混成同一个执行批次

一句话说：**这项工作只解决 tunnel 控制协议自己半迁移的问题。**

## 为什么不要和当前 CI 修复混做

因为两件事目标不同：

- **修 CI**
  - 目标：尽快恢复 PR 的 merge gate
  - 关注：先让当前失败测试恢复

- **协议债治理**
  - 目标：把 tunnel 控制协议彻底讲清楚
  - 关注：长期一致性和单一真相

如果把这两件事混在一起，会很容易：

- scope 变大
- 回归面扩大
- 验证难度上升
- 讨论焦点混乱

所以正确顺序是：

1. 先修当前 PR 的 CI
2. 再单独治理协议债

## 后续建议怎么做

建议走 **两阶段过渡 + 最终清理**，而不是直接一口气全切。

### Phase 1: 新协议正式进入共享层

目标：

- 把 `proxy_create`
- `proxy_create_resp`
- `proxy_provision`
- `proxy_provision_ack`

正式放进 `pkg/protocol`

要求：

- client 不再私有持有这套消息常量
- server/client 都从共享协议层读取
- 旧 `proxy_new` / `proxy_new_resp` 暂时保留兼容

### Phase 2: 命名和等待语义收口

目标：

- 重新命名 `waitForTunnelReady`
- 收口 `pendingReady`
- 收口 `ProxyNewResponse`

原则：

- create result 是一类语义
- provisioning ack 是一类语义
- “ready” 不再作为模糊中间词混用

### Phase 3: 删除旧路径

当前系统一旦稳定，再做最后一步：

- 删除旧 `proxy_new`
- 删除旧 `proxy_new_resp`
- 删除兼容分支
- 删除依赖旧语义的测试和日志

## 接手时建议先看哪些文件

如果后续同事要真正开始做，不建议从文档脑补实现，建议按下面顺序读代码：

1. [message.go](/Users/dyy/projects/code/netsgo/pkg/protocol/message.go)
   先看当前共享协议层到底正式定义了什么。
2. [client.go](/Users/dyy/projects/code/netsgo/internal/client/client.go)
   看 client 现在实际发什么消息、收什么 ACK。
3. [server.go](/Users/dyy/projects/code/netsgo/internal/server/server.go)
   看 server 控制循环现在实际处理什么消息。
4. [tunnel_manager.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_manager.go)
   看 server 给 client 下发 provisioning 时走的是哪条路径。
5. [tunnel_ready.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_ready.go)
   看当前 waiter / ready / ack 语义到底是怎么混在一起的。
6. 测试：
   - [client_test.go](/Users/dyy/projects/code/netsgo/internal/client/client_test.go)
   - [server_test.go](/Users/dyy/projects/code/netsgo/internal/server/server_test.go)
   - [message_test.go](/Users/dyy/projects/code/netsgo/pkg/protocol/message_test.go)

## 开工前建议先回答的 3 个问题

后续真正开始实现前，最好先在 issue/PR 描述里把下面 3 个问题说死：

1. 双栈兼容要保留多久？
   也就是旧 `proxy_new` / `proxy_new_resp` 是只过渡一轮，还是要跨多个 PR。

2. `ready` 这个词最终还保不保留？
   如果保留，它具体只能表示什么；如果不保留，要替换成什么命名。

3. 最终 clean break 的完成定义是什么？
   例如：
   - 共享协议层已有新消息
   - client/server 都不再私有定义 tunnel 控制消息
   - 旧消息只剩 alias 或已完全删除
   - 测试和日志不再出现旧 ready 语义

## 不推荐的做法

### 不推荐 1：什么都不做，长期靠兼容层活着

这样看起来最省事，但债永远都在。

后面每个同事都得自己猜：

- 现在正式协议到底是哪套
- 哪些是临时兼容
- 哪些路径是历史遗留

### 不推荐 2：现在立刻直接 clean break

虽然产品未发布，理论上可以“一次切干净”。

但当前系统已经处于半迁移态，直接全切的风险是：

- 会和刚修好的 CI 范围重新耦合
- 变更面会突然扩大
- 一次性验证成本太高

所以更稳的做法是：

**先进入受控双栈阶段，再做 clean break。**

## 最终目标

最终应该达到下面这个状态：

- `pkg/protocol` 是 tunnel 控制协议的唯一真相
- client/server 不再各自维护一套 tunnel 控制消息
- create / provision / ack / close 各自语义清晰
- waiter 和日志命名不再混用 `ready`
- 测试覆盖和协议定义一致
- 旧 `proxy_new` / `proxy_new_resp` 被删除，或者只在明确标注的兼容层里短暂存在

## 对接手同事的直接建议

如果你准备接这件事，建议顺序如下：

1. 先不要在当前 CI 修复分支里顺手继续扩协议范围
2. 新开一个专门的协议治理分支
3. 先统一共享协议层
4. 再统一 waiter / ack 命名
5. 最后删旧路径

## 相关文件

- [message.go](/Users/dyy/projects/code/netsgo/pkg/protocol/message.go)
- [client.go](/Users/dyy/projects/code/netsgo/internal/client/client.go)
- [server.go](/Users/dyy/projects/code/netsgo/internal/server/server.go)
- [tunnel_manager.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_manager.go)
- [tunnel_ready.go](/Users/dyy/projects/code/netsgo/internal/server/tunnel_ready.go)
- [client_test.go](/Users/dyy/projects/code/netsgo/internal/client/client_test.go)
- [server_test.go](/Users/dyy/projects/code/netsgo/internal/server/server_test.go)
- [message_test.go](/Users/dyy/projects/code/netsgo/pkg/protocol/message_test.go)
