# 规划：HTTP 域名隧道（TDD 草案）

> 创建时间：2026-03-21  
> 状态：已修订（待实现）  
> 目标读者：后端、前端、测试、评审同事

## 需求起因

当前 NetsGo 已具备 TCP / UDP 隧道能力，协议层也已经预留了 `http` 类型和 `domain` 字段，但运行时仍未真正实现“按域名识别并代理”的 HTTP 隧道。

本次需求的目标不是再做一条“叫 HTTP 的 TCP 端口映射”，而是补齐真正的域名型七层入口能力：

- 外部请求通过域名命中服务端
- 服务端按域名识别目标隧道
- 再通过现有 `/ws/data` + `yamux` 数据面把 HTTP 流量转发到 Client 侧的 HTTP 服务

本次需求已经明确两点边界：

- HTTP 隧道只按域名分流，不按路径分流
- 这不是当前默认方案，而是长期约束；后续也不计划引入 path-based 分流

## 目标

### 业务目标

1. 支持 `type=http` 的隧道配置与运行
2. 以域名作为唯一分流依据，不使用路径或前缀
3. 支持 `local_ip + local_port`，允许代理到 Client 所在局域网的其他 HTTP 设备
4. 明确排除管理平台保留地址与 HTTP 隧道域名冲突
5. 在 Client 离线、隧道暂停、服务端重启恢复等情况下，行为清晰且稳定

### 工程目标

1. 最大限度复用现有单端口架构、控制通道和数据通道
2. 尽量不改动双端消息体协议；内部通道识别通过 WebSocket 子协议完成，不再靠 Host 猜测
3. 通过测试驱动开发推进，先补测试，再补实现
4. 不破坏现有 TCP / UDP / 管理面行为

## 非目标

本次明确不做：

- 路径级路由、路径前缀改写、strip prefix
- 多个域名绑定到同一条隧道的批量配置
- 泛域名匹配（如 `*.example.com`）
- 前端从 Hash Router 切换到 History Router（这是独立的控制台演进任务，不作为本期 HTTP 隧道前置条件）
- 创建 / 恢复阶段对上游 HTTP 服务做主动健康探测
- 上游 HTTPS 探测、证书校验、自定义 CA
- header / body 内容改写
- gRPC / H2C 专项支持
- HTTP 隧道专用鉴权体系
- 基于路径把管理面和业务面混合在同一域名下分流

## 当前事实（基于仓库实现）

### 已存在的能力

- `pkg/protocol/types.go`、`pkg/protocol/message.go` 已经定义了：
  - `ProxyTypeHTTP`
  - `ProxyConfig.Domain`
  - `ProxyNewRequest.Domain`
- Client 数据面已经具备“服务端开 stream，客户端按 `local_ip:local_port` 建立 TCP 连接并 relay”的能力，`internal/client/client.go` 当前对非 UDP 流量统一按 TCP 处理
- 服务端已有：
  - 单端口 HTTP / WebSocket 入口
  - `/ws/control`
  - `/ws/data`
  - 隧道持久化存储 `internal/server/store.go`
  - 隧道生命周期管理 `internal/server/tunnel_manager.go`

### 尚未完成的部分

- `internal/server/proxy.go` 当前除 UDP 外都按 TCP 端口监听处理，HTTP 还没有域名路由语义
- `internal/server/server.go` 当前没有“按 Host 识别 HTTP 隧道”的入口分发
- `internal/server/tunnel_manager.go` 当前隧道 CRUD 仍主要围绕在线 Client 运行时，离线隧道的编辑 / 暂停 / 删除语义尚未补齐
- 前端 `web/src/components/custom/tunnel/TunnelDialog.tsx` 当前仍以“公网端口”心智为主，HTTP 未完成独立表单
- `domain` 目前只是配置字段，还不是运行时路由键

## 目标行为定义

## 一、HTTP 隧道的语义

HTTP 隧道定义为：

- `type=http`
- `domain`：公网访问域名（路由键）
- `local_ip`：Client 侧可达的上游主机，默认 `127.0.0.1`
- `local_port`：Client 侧可达的上游端口
- `remote_port`：不属于 HTTP 隧道的业务语义，本期从产品配置和运行判定中剥离

关于 `remote_port`，本期明确约束如下：

- HTTP 隧道对用户配置、前端表单、接口文档都不再暴露 `remote_port`
- HTTP 隧道不参与端口监听、端口冲突、端口白名单检查
- 所有端口相关逻辑都必须先按 `type` 分支：
  - 端口监听仅适用于 `tcp` / `udp`
  - 端口冲突检查仅适用于 `tcp` / `udp`
  - 端口白名单检查及其影响扫描仅适用于 `tcp` / `udp`
- 若共享协议 / 存储结构暂时仍带该字段，也只允许作为内部无意义占位：
  - 写入时统一归零
  - 在 HTTP 分支中的读取、比较、展示、测试时一律忽略
- 本期不讨论任何存量 HTTP 假实现、老数据迁移或旧行为兼容；相关代码可直接删除重建

典型示例：

- `domain=app.example.com`
- `local_ip=127.0.0.1`
- `local_port=3000`

或：

- `domain=printer.office.example`
- `local_ip=192.168.1.50`
- `local_port=8080`

这意味着：

- 外部访问 `app.example.com`
- 服务端按 `Host` 命中 HTTP 隧道
- 再通过已有数据面，让 Client 去访问 `127.0.0.1:3000`

第二个例子则表示 Client 作为局域网入口，去访问同一局域网中的其他设备 `192.168.1.50:8080`。

## 二、路由原则

HTTP 隧道只按域名分流，不按路径分流。

但 NetsGo 内部控制 / 数据通道是显式协议例外，不属于业务 HTTP 隧道路由。

请求分类顺序应当是：

1. 先提取并归一化请求 `Host`，同时检查 path 和 WebSocket 子协议
2. 如果请求是：
   - `path=/ws/control` 且 `Sec-WebSocket-Protocol: netsgo-control.v1`
   - 或 `path=/ws/data` 且 `Sec-WebSocket-Protocol: netsgo-data.v1`
   则直接进入 NetsGo 内部通道逻辑
3. 否则，如果 Host 命中已声明的 HTTP 隧道域名，则整条请求进入 HTTP 隧道逻辑
4. 否则，如果 Host 未命中任何 HTTP 隧道，且服务尚未初始化，则允许 setup UI 所需的管理前端入口、静态资源和 `/api/setup/*`
5. 否则，如果 Host 未命中任何 HTTP 隧道，且 Host 命中生效管理 Host，则走现有管理面 / API / SSE 逻辑
6. 其他所有情况一律返回 `404`

这里的“整条请求进入 HTTP 隧道逻辑”是指：

- 无论路径是 `/`
- 还是 `/api/foo`
- 还是 `/ws/chat`
- 还是 `/ws/control` / `/ws/data`，但没有带合法的 NetsGo 内部子协议

都只看 Host，不再看 path。

也就是说，除显式 NetsGo 内部 WebSocket 握手外，一旦 Host 命中 HTTP 隧道，就不应该再让 `/api/*`、`/ws/*` 回落到 NetsGo 的管理 API 或控制通道。

### NetsGo 内部 WebSocket 通道识别

为避免“Client 连接请求”和“业务 HTTP / WebSocket 请求”互相混淆，本期明确约束：

- `/ws/control` 必须带 `Sec-WebSocket-Protocol: netsgo-control.v1`
- `/ws/data` 必须带 `Sec-WebSocket-Protocol: netsgo-data.v1`
- 服务端只有在“路径正确 + 子协议正确”时，才把请求视为 NetsGo 内部通道
- 缺失子协议、子协议错误、版本不支持时，服务端绝不能把请求模糊当成内部通道
- 这两个子协议是 NetsGo 内部协议契约，不是前端表单字段，也不是普通业务请求头配置项

这样做的目的有两点：

- Client 可以通过任意可达的地址连接服务端，不再被“必须命中管理 Host”绑死
- 业务域名上的普通 `/ws/*` 请求不会被误判成 NetsGo 控制 / 数据通道

### 内部 WS 子协议握手契约

为避免 server / client / proxy / 测试各自实现一套“差不多”的子协议逻辑，本期把握手契约写死：

- 子协议常量统一定义在 `pkg/protocol/types.go`，由 server / client 共享
- Client 侧必须通过 WebSocket Dialer 的 subprotocol 能力发送 `netsgo-control.v1` / `netsgo-data.v1`，而不是在不同调用点手写裸 header
- Server 侧必须先解析请求声明的 subprotocol 列表，再决定是否允许进入内部通道
- 只有当请求声明列表中包含精确期望值时，才允许升级为 NetsGo 内部通道；升级响应也必须回写同一个已协商子协议
- `netsgo-control.v1` 与 `netsgo-data.v1` 不可混用；path 正确但子协议错误时，不得“按 path 猜测”
- 缺失、拼写错误、版本不支持、代理改写后的子协议，都按“非内部请求”处理，不做兼容放行
- 本期不兼容旧 Client；旧 Client 未携带子协议时，不再尝试兜底识别，mixed-version 的 server / client 不在支持范围内

### 生效管理 Host

为避免“错误域名落到管理平台”的问题，本期建议把管理面入口明确收窄为“单一生效管理 Host”，而不是一组宽泛白名单。

生效管理地址的来源优先级如下：

1. 显式环境变量 / 启动参数（如 `NETSGO_SERVER_ADDR` / `--server-addr`）
2. 若未显式指定，则使用管理平台配置中的 `server_addr`

生效管理 Host 取自“生效管理地址”经 `canonicalHost()` 归一化后的 host。

初始化完成后，只有 Host 命中生效管理 Host 的请求，才能访问管理面资源。这里的管理面资源包括：

- `/`
- 管理前端静态资源
- `/api/*`
- `/api/events`

这意味着：

- HTTP 隧道业务域名无论访问什么 path，都不能触达管理平台任何资源
- 错误域名、其他域名、其他 IP、`localhost`、本机名等，只要不是生效管理 Host，都不能访问管理平台任何资源
- 浏览器管理面与 HTTP 隧道的边界由 Host 规则决定；NetsGo 内部 WebSocket 通道是显式的协议例外

这样可以避免：

- 误配 DNS 后直接看到管理平台
- 已删除或写错的业务域名回落到管理平台
- 业务域名与管理域名边界不清

### Client 连接地址与管理地址的关系

本期明确区分三个概念：

- `server_addr`：管理平台持久化保存的默认地址；用于 setup、管理配置页、Add Client 命令中的默认展示值；在未被显式覆盖时，它也作为默认管理地址
- `effective_server_addr`：运行时真正生效的管理地址；来源优先级为 `--server-addr` / `NETSGO_SERVER_ADDR` > 持久化 `server_addr`
- Client 实际连接地址：Client CLI / Agent 真正去 Dial 的服务端地址

约束如下：

- Client 可以连接任意能到达 NetsGo 服务端的地址
- 这个地址可以是域名、IP、内网地址或反向代理入口
- 它不要求与生效管理 Host 相同
- `server_addr` / `effective_server_addr` 都不是“Client 连接白名单”，只是默认推荐值与管理面访问控制依据
- 当 `effective_server_addr != server_addr` 时，UI 和 API 都必须同时展示“保存值”和“当前生效值”，并标明锁定来源
- 服务端不再依赖“Client 必须命中管理 Host”来识别内部通道，而是依赖前述 WebSocket 子协议
- HTTP 隧道与管理面冲突校验，只比较 HTTP `domain` 与生效管理 Host；不把任意 Client 连接地址都纳入保留域名集合

### 初始化阶段例外

在服务尚未初始化时：

- 此时还不存在有效的 HTTP 隧道域名声明
- setup UI 所需的管理前端入口、静态资源和 `/api/setup/*` 可暂不受 Host 收紧规则影响
- 初始化完成后，应立即切换到严格的生效管理 Host 校验

### 管理面 SPA fallback 的边界

现有管理前端在进入管理面分支后，会继续依赖 SPA fallback 处理前端子路由；但这层 fallback 只能发生在“已经确认是管理面请求”之后。

- 外层 `hostDispatchHandler` 必须先完成 Host 分类，再决定是否进入管理面 mux / `handleWeb`
- 只有命中 setup 例外或生效管理 Host 的请求，才允许进入管理面静态资源与 SPA fallback
- 未命中业务域名且不是生效管理 Host 的请求，必须直接返回 `404`，不能再交给管理前端 handler

这条规则的目的不是干预业务 SPA，而是防止未知 Host 被管理前端的 `index.html` fallback 吞掉。

## 三、反向代理假设

本次设计默认 HTTP 隧道路由只依赖“最终到达 NetsGo 时的 `Host` 值”。

因此：

- HTTP 隧道既可以直接访问到 NetsGo，也可以先经过反向代理
- 反向代理不是前置条件，但必须正确透传 `Host`
- 若承载 Client 控制 / 数据通道，也必须正确透传 `Sec-WebSocket-Protocol`
- nginx / caddy / 其他 L7 代理的“默认行为”不视为天然可靠前提；必须以 E2E 验证结果为准，若默认行为不保留 `Sec-WebSocket-Protocol`，则模板必须显式补充转发配置
- 若反向代理改写 `Host`，则属于部署错误，不由 HTTP 隧道逻辑兜底修正
- 若反向代理丢弃或改写 NetsGo 内部 WebSocket 子协议，则 Client 握手应失败，而不是降级到其他路由
- `tls-mode` 只负责管理平台 / Client 连接侧的部署模式，不参与 HTTP 隧道路由判定

关于 HTTPS，再单独强调一条部署边界：

- 浏览器是否能以 HTTPS 成功到达 NetsGo，取决于证书和 TLS 终止部署
- 这影响“请求能不能到达”，但不改变“请求到达后按 Host 路由”的逻辑
- 因此 HTTPS 证书覆盖问题属于部署前提，而不是本期 HTTP 隧道路由语义的一部分

## 四、域名归一化与校验

建议将 HTTP 隧道 `domain` 视为“host 值”，而不是 URL。

### 归一化规则

- 去首尾空格
- 统一转小写
- 去尾部 `.`（若存在）
- 比较时忽略请求里的端口，例如 `app.example.com:80` 视为 `app.example.com`

### 校验规则

允许：

- 普通 host 名
- 多级域名

禁止：

- 带 scheme：`http://app.example.com`
- 带 path：`app.example.com/foo`
- 带 query / fragment
- 带端口：`app.example.com:8080`
- IP 地址
- `localhost`
- 空值
- 泛域名写法（本期不支持）

> 说明：本期把 HTTP 隧道视为“域名型入口”，因此不接受 IP/localhost 作为 `domain`。

### 生效管理地址与 `canonicalHost()` 规则

`domain` 与生效管理地址的职责不同：

- `domain` 是业务入口 host
- 生效管理地址是管理平台访问控制依据；持久化的 `server_addr` 同时也是 UI 推荐连接地址的默认来源

但两者在“是否冲突”“是否允许访问管理面”的问题上，最终都要落到“比较归一化后的 host”。

因此本期建议明确一套共享的 `canonicalHost()` 规则。它至少需要满足：

- 输入先去首尾空格
- 优先按完整 URL 提取 host
- 若不是完整 URL，则内部兼容 `host` 或 `host:port`
- 提取 host 后再统一：
  - 转小写
  - 去尾部 `.`
  - 去端口

输入约束与锁定语义：

- 前端新输入仍应继续要求完整 URL
- 若环境变量 / 启动参数显式设置了生效管理地址，则它应被视为锁定配置：
  - 管理平台配置页必须展示只读状态与来源说明
  - 后端 `PUT /api/admin/config` 必须拒绝修改该字段
  - 所有冲突校验和访问控制都以锁定后的生效管理地址为准
- 若未显式锁定，则以管理平台保存的 `server_addr` 为准

若某个输入连合法 host 都提取不出来，则应视为配置异常，需要先修复，再继续做域名冲突相关操作。

## 五、冲突规则

### 1. HTTP 隧道之间的冲突

归一化后的 `domain` 必须全局唯一。

下列情况都必须拒绝：

- 同一 Client 创建两条相同 `domain` 的 HTTP 隧道
- 不同 Client 创建相同 `domain` 的 HTTP 隧道
- 编辑已有 HTTP 隧道时改成已被其他 HTTP 隧道占用的 `domain`

校验范围不能只看在线 Client 的内存态，还必须覆盖持久化存储中的离线隧道。

### 2. 与管理平台地址冲突

生效管理地址中的 host 是管理平台保留地址，HTTP 隧道不得与其冲突。

比较规则：

- 只比较 host
- 忽略 scheme
- 忽略端口
- 使用同一套归一化逻辑

必须拦截两类冲突：

1. 创建 / 编辑 HTTP 隧道时，若 `domain` 与当前生效管理 Host 相同，禁止保存
2. 修改 `server_addr` 时，若候选 `server_addr` 的 host 与任一已有 HTTP 隧道 `domain` 冲突，禁止保存；若当前被 flag / env 锁定，则先直接拒绝修改

### 3. 生命周期中的保留规则

HTTP 域名声明应当在以下状态仍然被视为“已占用”：

- `pending`
- `active`
- `paused`
- `stopped`
- `error`

只有真正 `delete` 后，域名才可重新被其他隧道使用。

这样做的原因：

- 避免已暂停 / 已停止 / 异常 / 离线的 HTTP 隧道域名回退到管理平台
- 避免域名在暂时不可用期间被其他隧道抢占

### 4. 离线隧道的管理规则

已存在于持久化存储中的 HTTP 隧道，即使所属 Client 当前离线，也必须满足：

- 仍然参与 `domain` 全局唯一校验
- 仍然参与与生效管理 Host 的冲突校验
- 允许编辑
- 允许暂停
- 允许删除

离线隧道的管理语义应尽量保持简单：

- 不新增离线专用 endpoint，继续沿用同一组 tunnel 管理接口
- 继续使用 `/api/clients/{id}/tunnels/...` 这组路径，但这里的 `{id}` 表示“注册 Client ID”，不是“当前 live session 句柄”
- 对既有 HTTP 隧道的 `update / pause / delete`，以持久化存储为配置真值；Client 在线时再同步 runtime
- 若 Client 当前离线：
  - `update` 直接写 store，并在其下次上线时按最新配置恢复
  - `pause` 直接把持久化状态写成 `paused`，下次上线后保持暂停，不自动恢复
  - `delete` 直接移除 store，域名立即释放
- 为保持语义简单，本期不支持离线 `resume / stop`；这两个动作仍要求目标 Client 当前在线
- 若 Client 当前在线，则上述动作仍需同步运行时状态

为避免范围膨胀，本期不扩展“给离线 Client 创建全新 HTTP 隧道”的能力：

- 创建新 HTTP 隧道仍要求目标 Client 当前在线且数据面可用

## 六、请求行为约定

### 显式 NetsGo 内部 WebSocket 握手

- 命中 `/ws/control` + `netsgo-control.v1` 时，进入内部控制通道
- 命中 `/ws/data` + `netsgo-data.v1` 时，进入内部数据通道
- 这类握手可以从任意可达 Host / IP 进入，不受“必须命中生效管理 Host”限制
- 若 path 命中但子协议缺失或错误，则不进入内部通道，继续按 Host 路由：
  - 若 Host 命中业务域名，则进入业务 HTTP / WebSocket 隧道
  - 若 Host 命中生效管理 Host，也不因为 path 是 `/ws/control` / `/ws/data` 就被放行；应按普通管理面路由处理，通常返回 `404`
  - 若 Host 未命中业务域名，也不是 setup 例外或管理 Host，则返回 `404`

### 已命中 HTTP 隧道域名

#### `active` 且 Client 在线

- 正常代理请求到 Client 侧 `local_ip:local_port`

### 创建 / 恢复阶段的 ready 语义

本期不建议把 HTTP 隧道的“ready”定义为“上游 `local_ip:local_port` 已可达”。

也就是：

- Server 在 create / resume / restore 时下发 `proxy_new`
- Client 收到 `type=http` 后，只需要接受配置并完成本地注册
- 不要求 Client 在这个阶段主动 `DialTimeout` 上游 HTTP 服务
- create / resume / restore 成功，只代表：
  - 服务端已接受这条 HTTP 域名声明
  - Client 已接受这条隧道配置
  - 后续真实请求可以尝试经数据面转发
- 这不等价于“上游应用已经启动并可访问”

这样做的原因：

- HTTP 隧道的职责是域名路由与转发，而不是上游应用健康检查
- 用户完全可能先创建隧道，再稍后启动本地 HTTP 服务
- 把第一次真实请求的失败暴露为 `502` 已足够，不需要在创建阶段提前阻断

HTTP 隧道仍然走现有的 `proxy_new / ready` 握手流程（`waitForTunnelReady`），等待 Client 回复 acknowledge，确认配置已接受。"不做上游探测"仅指 Client 在 ready 阶段不需要调用 `DialTimeout` 测试 `local_ip:local_port` 的连通性，而不是跳过握手本身。

#### 已命中域名，但隧道不可服务

包括：

- `pending`
- `paused`
- `stopped`
- `error`
- Client 离线
- 服务端重启后 Client 尚未恢复

建议返回：

- `503 Service Unavailable`

而不是回落到管理面。

#### 隧道处于 `active`，但单次请求代理失败

如：

- 打开 stream 失败
- 与 Client 的数据通道异常
- 本次反向代理拨号失败
- 上游 HTTP 服务未启动 / 拒绝连接 / 超时

建议返回：

- `502 Bad Gateway`

### HTTP 可服务性判定

HTTP 隧道当前是否可服务，应明确由以下条件共同决定：

- `status == active`
- 所属 Client 当前在线

在当前架构下，`Client online` 已经隐含“控制 / 数据逻辑会话已建立”；因此这里不再额外引入第三个用户可见的“数据面状态轴”。

只有上述条件同时满足时，请求才进入真实代理流程。

其余情况一律视为“域名已声明，但当前不可服务”，返回 `503`。

### 未命中任何 HTTP 隧道域名

本期建议做更严格的区分：

- 若请求已命中合法 NetsGo 内部 WebSocket 握手，则按内部通道处理
- 若 Host 命中生效管理 Host，则继续走现有管理面 / API / SSE 逻辑
- 若 Host 不命中生效管理 Host，则返回 `404 Not Found`

原因：

- 保证管理面只暴露在明确配置的 Host 上
- 避免业务域名、错误域名或其他入口回落到管理平台

## 七、HTTP 头与协议行为

本期应覆盖：

- 普通 HTTP/1.1 请求
- SSE / 流式响应
- WebSocket Upgrade

建议保留或补齐常见反向代理头：

- 原始 `Host`
- `X-Forwarded-Host`
- `X-Forwarded-Proto`
- `X-Forwarded-For`

其中 `Host` 语义需要明确：

- 业务后端应看到外部访问时使用的原始域名 Host
- 不能把 Host 改写为 `local_ip`
- 不能把 Host 改写为 NetsGo 管理域名

这是为了兼容：

- 虚拟主机路由
- 应用内绝对 URL 生成
- 基于 Host 的重定向
- Cookie Domain / 多租户框架

不做：

- 路径改写
- Header 内容定制化策略

### 可信代理语义

HTTP 隧道在向上游转发时，应遵循明确的 trusted proxy 规则，而不是盲信外部请求自带的转发头。

要求如下：

- 保留原始 `Host`
- 设置 `X-Forwarded-Host` 为外部访问时使用的原始 Host
- 设置 `X-Forwarded-Proto` 为 NetsGo 计算后的外部 scheme
- 对 `X-Forwarded-For` 采用“追加当前客户端 IP”语义，而不是简单覆盖
- 来自非可信代理的 `X-Forwarded-*` 不能直接当作事实继续传给上游

实现上应复用仓库现有 trusted proxy 判定与客户端 IP / proto 解析规则，不再为 HTTP 隧道新造一套头信任逻辑。

### WebSocket Upgrade 实现说明

标准 `httputil.ReverseProxy` 不能开箱即用地透传 WebSocket Upgrade，需要显式处理 `Connection: Upgrade` 的升级请求。建议实现时在 `http_tunnel_proxy.go` 中：用 `httputil.ReverseProxy` 并配置正确的 Hop-by-Hop 头处理，或在检测到 `Upgrade: websocket` 时直接接管连接并 Relay。无论哪种方式，都必须通过测试验证 WebSocket 双向通信可以打通。

### SSE / chunked 响应的 Flush 要求

`httputil.ReverseProxy` 默认会缓冲响应，这会导致 SSE 事件被延迟合并而不是实时推送。HTTP 隧道的反向代理实现必须设置即时 Flush（如 `FlushInterval: -1`），否则业务 SSE 和流式下载会出现延迟问题。

### 部署注意：业务 SPA 的 fallback 路由

若业务前端是 History Router 模式的 SPA，浏览器直接刷新子页面时会向 NetsGo 发来对应路径的请求。HTTP 隧道会透传该路径到业务后端，SPA 的 404 fallback 需要业务后端自行处理（例如配置 `try_files`），NetsGo 不做干预。

## 八、状态与持久化语义

### 1. 域名声明的来源

HTTP 域名占用判断不能只依赖运行时内存，还要覆盖存储层。

因此建议通过共享 helper 按需生成“HTTP 域名声明视图”，其数据来源包含：

- 在线 Client 的运行时隧道
- `TunnelStore` 中的持久化隧道

本期不建议额外维护一份长期同步的独立索引，以免引入第三份可变状态。

### 1.1 运行时与持久化的一致性要求

HTTP 隧道的 `domain` 不能只是“请求体里带过一次的字段”，而必须是运行时和持久化的一等字段。

因此建议补充以下硬约束：

- 任何把 HTTP 隧道请求体落到运行时 `ProxyConfig` 的路径，都必须保留归一化后的 `domain`
- 任何把运行时配置写回 `StoredTunnel` 的路径，都必须保留 `domain`
- `pending` / `paused` / `stopped` / `error` 占位记录也必须保留 `domain`
- create / update / restore / delete 等生命周期动作，不允许分别手写多套“看起来差不多”的结构体组装逻辑
- 应尽量收敛到共享 helper，避免 `domain` 在某条分支里被漏写或覆盖为空

### 1.2 离线隧道的编辑 / 暂停 / 删除一致性

对已持久化的 HTTP 隧道，离线编辑 / 暂停 / 删除还需要满足：

- 编辑后新的 `domain`、`local_ip`、`local_port` 必须立即写入持久化存储
- 离线暂停必须立即把持久化状态写成 `paused`
- 删除后持久化记录立即移除
- 这些变更会立刻影响域名占用判断与管理面冲突校验
- Client 后续重连时，以最新持久化结果恢复

### 2. 服务端重启后的行为

如果服务端重启，但原有 HTTP 隧道仍存在于 `store`：

- 在 Client 尚未重连前
- 该域名仍应被视为已声明
- 命中后应返回 `503`

### 3. 配置状态、会话状态与可服务性分离

本期明确分离两类事实和一个派生结论：

- 隧道配置状态：`pending / active / paused / stopped / error`
- Client 会话状态：`online / offline`
- 当前可服务性：由“配置状态 + Client 是否在线”派生，不单独持久化成第三种 tunnel 状态

约束如下：

- `paused` / `stopped` 只表示显式配置动作，不表示链路断线
- Client 断线不会把 HTTP 隧道状态自动改成 `paused`
- 前端与后端都必须把“配置状态”和“当前可服务性”分开表达
- 不在共享协议里再发明一个与 `pending / active / paused / stopped / error` 平行的“service status”枚举

### 4. Client 断线后的行为

当前实现中存在“断线时把活跃隧道转为暂停语义”的行为；本期 HTTP 隧道目标设计不再沿用这一语义。

断线后应满足：

- 域名声明继续保留
- 外部请求不应落到管理面
- 若隧道配置状态仍为 `active`，也只表示“配置上应提供服务”，不表示当前真的可服务
- 应返回 `503`

## 用户场景清单

| 场景 | 预期行为 |
|------|---------|
| 创建 `type=http`，`domain=app.example.com`，`127.0.0.1:3000` | 创建成功，请求按域名进入本地 3000 |
| 创建 `type=http`，`domain=printer.office.example`，`192.168.1.50:8080` | 创建成功，Client 作为 LAN 入口代理到其他设备 |
| 创建 `type=http` | 不再要求用户配置 `remote_port` |
| Client 通过任意可达地址访问 `/ws/control`，并携带 `Sec-WebSocket-Protocol: netsgo-control.v1` | 允许，进入 NetsGo 内部控制通道 |
| Client 通过任意可达地址访问 `/ws/data`，并携带 `Sec-WebSocket-Protocol: netsgo-data.v1` | 允许，进入 NetsGo 内部数据通道 |
| HTTP 隧道走端口白名单、端口冲突、端口影响扫描 | 不参与；这些逻辑只适用于 `tcp` / `udp` |
| 同一 Client 两条不同域名指向同一 `local_ip:local_port` | 允许 |
| 两个不同 Client 使用同一域名 | 禁止，返回冲突 |
| HTTP 隧道域名等于当前生效管理 Host | 禁止，返回冲突 |
| 修改 `server_addr` 为某个已存在 HTTP 隧道的域名 | 禁止保存，返回冲突清单 |
| 通过 `effective_server_addr` host 访问 `/api/*` | 进入管理 API |
| 通过 `effective_server_addr` host 访问 `/ws/control` 或 `/ws/data`，但未携带合法 NetsGo 子协议 | 不被当成内部通道放行；按普通管理面路由处理，通常返回 `404` |
| 通过 HTTP 隧道业务域名访问 `/`、静态资源、`/api/*`、`/ws/*` | 都只进入业务服务；只有显式合法的 NetsGo 内部 WS 握手才会作为例外单独识别 |
| 通过 HTTP 隧道业务域名访问 `/ws/control` 或 `/ws/data`，但未携带合法 NetsGo 子协议 | 仍按业务 WebSocket 请求处理，不进入 NetsGo 内部通道 |
| 通过非 `effective_server_addr` host（包括其他域名、IP、`localhost` 等）访问管理面 | 返回 `404`，不暴露管理平台 |
| 服务未初始化时通过任意 Host 访问 setup UI 所需入口、静态资源和 `/api/setup/*` | 允许 |
| 使用未知业务域名访问 NetsGo | 返回 `404`，不回落管理面 |
| HTTP 隧道处于 `pending` | 访问该域名返回 `503` |
| HTTP 隧道暂停 / 停止 / 异常 | 访问该域名返回 `503` |
| Client 断线或服务端重启后 Client 未恢复 | 访问该域名返回 `503`，但不把隧道状态伪装成 `paused` |
| 命中 HTTP 隧道域名且路径为 `/api/foo` | 仍然代理到业务服务，不进入 NetsGo API |
| 命中 HTTP 隧道域名且路径为 `/ws/chat` | 仍然作为业务 WebSocket 代理 |
| 创建 / 恢复时上游 `local_ip:local_port` 尚未启动 | 仍允许创建 / 恢复；第一次真实请求失败时返回 `502` |
| 目标 Client 离线时创建新的 HTTP 隧道 | 本期不支持；仍要求 Client 在线 |
| 离线 Client 的既有 HTTP 隧道编辑 / 暂停 / 删除 | 允许；修改立即写入 store，下次上线按新配置恢复 |
| 离线 Client 的既有 HTTP 隧道 `resume / stop` | 本期不支持；返回冲突或显式错误，不做隐式排队 |
| 旧 Client 未携带内部 WS 子协议连接 `/ws/control` 或 `/ws/data` | 不再兼容放行；按“非内部请求”处理 |
| 生效管理地址由环境变量 / 启动参数锁定 | 管理页展示只读说明，后端拒绝修改 |

## 文件级规划

以下规划以“最小必要改动 + 明确职责边界”为原则。

### 后端：协议与模型

| 文件 | 规划 |
|------|------|
| `pkg/protocol/types.go` | 共享定义 NetsGo 内部 WebSocket 子协议常量 `netsgo-control.v1` / `netsgo-data.v1`；保留现有 `pending / active / paused / stopped / error` 状态语义；明确 HTTP 语义是域名型，不再依赖 `remote_port` |
| `pkg/protocol/message.go` | 原则上不新增消息体字段，仅补充注释或测试；内部通道识别放在 WebSocket 握手层，不另造一套消息协议 |
| `pkg/protocol/message_test.go` | 补充 HTTP 语义相关回归测试（字段不变、序列化不破坏） |

### 后端：HTTP 域名隧道核心

| 文件 | 规划 |
|------|------|
| `internal/server/http_tunnel_rules.go`（建议新增） | 承载 HTTP 域名归一化、生效管理 Host 提取、runtime + store 共享声明扫描 helper、Host 命中判定、状态到返回码映射等“规则层”逻辑；避免把规则、I/O、反向代理全部塞进一个大文件 |
| `internal/server/http_tunnel_proxy.go`（建议新增） | 承载 HTTP 反向代理入口、业务 WebSocket relay、SSE flush、`Host` / `X-Forwarded-*` 的 trusted proxy 语义收口等“执行层”逻辑 |
| `internal/server/http_tunnel_test.go`（建议新增） | 域名校验、冲突扫描、Host 匹配、状态到返回码映射、trusted proxy 头处理等单元测试 |
| `internal/server/server.go` | 在最外层增加 `hostDispatchHandler`，请求顺序固定为“合法内部 WS 握手 -> HTTP 域名隧道 -> setup 例外 -> 管理面 / API / SSE -> 404”。**`securityHeadersHandler` 必须只包住管理面分支，不能包住整个外层 handler**，否则安全头（CSP / X-Frame-Options 等）会污染业务响应。`/ws/control` / `/ws/data` 只有在 path + 子协议都合法时才可进入内部通道，未带合法子协议时不能被模糊接纳。**管理面 SPA fallback 只能发生在已经命中 setup 例外或生效管理 Host 之后**，未知 Host 不能再落入 `handleWeb`。**已知 bug（P1）**：`restoreTunnels` 中为 `paused / stopped / error` 状态以及端口不在白名单时构造的两处占位 `ProxyTunnel`，均未从 `StoredTunnel` 赋值 `Domain` 字段；重启后这些 HTTP 隧道的路由键为空字符串，导致 Host 分发失效且域名冲突扫描漏判；实现时须在两处占位构造中补全 `Domain: st.Domain`，并补对应重启恢复场景的集成测试。 |
| `internal/server/proxy.go` | `type=http` 时不再监听公网 TCP 端口，进入 HTTP 域名隧道激活流程。**已知 bug（P0）**：`activatePreparedTunnel` 当前只区分 UDP / 其他，"其他"分支直接走 `net.Listen("tcp", ":port")`；HTTP 隧道 `RemotePort == 0` 时会绑随机 TCP 端口，与"不监听公网端口"的语义完全冲突——必须在 UDP 分支前加 `type=http` 的 early-return 分支。**已知 bug（P0）**：`prepareProxyTunnel` 构造 `ProxyConfig` 时漏掉 `Domain` 字段赋值（对比 `upsertTunnelPlaceholder` 已有 `Domain: req.Domain`），HTTP 隧道创建成功后路由键实际为空；此外 `restoreManagedTunnel` 与 `resumeManagedTunnel` 均调用 `prepareProxyTunnel`，因此恢复/恢复路径的 `Domain` 也会消失；实现时须同步修复。HTTP 分支不再依赖 `remote_port` 业务语义。 |
| `internal/server/tunnel_manager.go` | 在 create / update / pause / resume / stop / delete / restore 流程中维护 HTTP 域名声明；编辑域名时校验和写入放在同一 `proxyMu.Lock()` 保护范围内，保证“读-校验-写”原子；通过共享 helper 维护 runtime/store/placeholder 一致性；已持久化 HTTP 隧道支持离线 `update / pause / delete`；既有隧道的这三类动作以 store 为配置真值，Client 在线时再同步 runtime；**离线 Client 的 delete 只操作 store，不触发 `proxy_close` 通知**；**离线 `resume / stop` 明确返回错误，不做隐式排队**；HTTP create / resume / restore 不做上游可达性预检查。`findTunnelsAffectedByPortChange` 已有 `RemotePort == 0` 防护，HTTP 隧道天然不受端口白名单影响，无需修改。 |
| `internal/server/store.go` | 复用现有 `Domain` 持久化；补充便于按 `type=http` 扫描全部域名声明、以及离线编辑 / 暂停 / 删除既有隧道的辅助方法；为既有 tunnel 的 store-first 读取提供清晰入口 |
| `internal/server/rate_limiter.go` 或提取共享 helper | 复用现有 trusted proxy 的客户端 IP / proto 解析能力，供 HTTP 隧道构造 `X-Forwarded-*` 时统一使用，避免新造一套头信任逻辑 |
| `internal/server/admin_api.go` | 扩展 `GET/PUT /api/admin/config` 及 dry-run，返回生效管理地址及其锁定信息；检查生效管理 Host 与 HTTP 隧道域名冲突；锁定时拒绝修改 `server_addr`；冲突时返回结构化结果而不是仅返回字符串错误。接口语义上要明确：`server_addr` 是持久化保存的默认地址与推荐连接地址，真正用于访问控制的是 `effective_server_addr`；二者都不是 Client 连接白名单。 |
| `internal/server/admin_api_test.go` | 覆盖 `server_addr` 与 HTTP 隧道冲突的 dry-run / save 拒绝逻辑 |
| `internal/server/server_test.go` | 覆盖管理面入口、HTTP 隧道入口、内部 WS 通道三者共存的集成行为；验证合法子协议可以在非管理 Host 上建立控制 / 数据通道，非法或缺失子协议不会误入内部通道；Host 分流测试必须走最终 handler，而不是只测内部 `newHTTPMux()`。**配套遗漏（P2）**：加入 `hostDispatchHandler` 后，`StartHTTPOnly()` 以及仓库中现有大量直接调用 `s.newHTTPMux()` 的测试将只覆盖旧路由；需同步把 `StartHTTPOnly()` 改为返回新的外层 handler，并更新相关测试入口，否则测试将成为无效覆盖。 |
| `internal/server/proxy_test.go` 或新建 `internal/server/http_proxy_test.go` | 覆盖 HTTP 请求、SSE、业务 WebSocket、暂停/恢复/离线时的域名路由行为；补“业务域名上的 `/ws/control` / `/ws/data` 未带合法子协议时仍走业务”、“上游未启动时创建仍成功、真实请求返回 `502`”场景 |
| `cmd/netsgo/cmd_server.go` | 增加 `--server-addr` / `NETSGO_SERVER_ADDR` 作为生效管理地址的显式来源；启用后锁定管理面 `server_addr` 配置。**配套遗漏（P2）**：`Server` 结构体当前没有用于承载运行时生效管理地址的字段；`--server-addr` / `NETSGO_SERVER_ADDR` 如何在启动后传递给 `hostDispatchHandler` 和冲突校验逻辑，需在实现前明确；建议在 `Server` 结构体中新增 `EffectiveServerAddr string` 字段，由 `cmd_server.go` 在启动时按“env/flag > admin config”优先级写入，运行时只读该字段。 |

### 后端：Client 侧

| 文件 | 规划 |
|------|------|
| `internal/client/client.go` | 在 Dial `/ws/control` 时发送 `Sec-WebSocket-Protocol: netsgo-control.v1`，在 Dial `/ws/data` 时发送 `Sec-WebSocket-Protocol: netsgo-data.v1`；应优先使用 dialer 的 subprotocol 协商能力，而不是散落的自定义 header 拼接；原则上复用现有“非 UDP 统一按 TCP relay”的实现；HTTP 不新增上游主动探测逻辑，继续保持“接受配置即可 ready” |
| `internal/client/client_test.go` 或新建专项测试 | 回归验证 control / data 通道会携带正确子协议，且不依赖命中特定管理 Host；验证 server upgrade 响应回写的子协议正确；HTTP 类型仍通过现有 TCP 数据面工作，不破坏原有 TCP 行为，也不把上游应用是否启动耦合到隧道创建阶段 |

### 前端：隧道配置与展示

前端允许按业务组件重构，不要求保留现有临时 UI，但应坚持以下约束：

- 服务端状态仍以 TanStack Query 为唯一远程状态源
- 不把 `client.online`、`tunnel.status` 再复制成多套平行 store
- 统一在单一 view-model helper 中派生“当前可服务性 / 不可服务原因 / 展示文案”，列表页、详情页、概览页复用同一套规则
- `web/src/components/ui/` 仍尽量不动；重构重点放在 `components/custom`、`hooks`、`routes`、`lib`

| 文件 | 规划 |
|------|------|
| `web/src/types/index.ts` | `ProxyStatus` 补齐 `pending`；补充 HTTP 隧道相关冲突返回类型、管理配置 dry-run 冲突类型，以及生效管理地址锁定元信息；区分“原始 API 类型”和“前端派生展示类型” |
| `web/src/lib/tunnel-view.ts`（建议新增） | 统一把 `Client + ProxyConfig` 原始数据派生为前端展示模型，例如“是否可服务”“不可服务原因”“主映射文案”；避免概览页、列表页、详情页各自重复判断 |
| `web/src/hooks/use-clients.ts` / `web/src/hooks/use-event-stream.ts` | 继续以 `/api/clients` + SSE 为唯一数据来源；允许重构缓存合并逻辑，但不要再造一套与后端平行的 tunnel/session 状态机 |
| `web/src/components/custom/tunnel/TunnelDialog.tsx` | `type=http` 时展示 `domain + local_ip + local_port`；移除 `remote_port` 的用户输入心智；创建和编辑时做前端基础校验 |
| `web/src/components/custom/tunnel/TunnelListTable.tsx` | HTTP 隧道展示为 `domain -> local_ip:local_port`，不再展示 `:remote_port` 作为主映射；“paused/stopped/error/pending”与“client offline”分开展示，不再把 offline 伪装成 paused |
| `web/src/components/custom/tunnel/TunnelTable.tsx` / `web/src/components/custom/dashboard/DashboardTunnelTable.tsx` / `web/src/components/custom/dashboard/OverviewPage.tsx` | 统一消费派生后的 tunnel view model；允许整体重排当前临时列表结构，但应保持同一套状态解释与交互文案 |
| `web/src/hooks/use-tunnel-mutations.ts` | 确保 create / update 发送 `domain`，并能消费后端冲突错误；HTTP 分支不再把 `remote_port` 当成用户配置来源 |
| `web/src/routes/dashboard/clients.$clientId.tsx` | 客户端详情页应复用同一套 view model 和动作可用性矩阵，不允许“列表页能看出离线，详情页又把它显示成 paused”这类语义分叉 |
| `web/src/routes/admin/config.tsx` | 修改 `server_addr` 时，dry-run 除端口影响外还显示 HTTP 域名冲突；若生效管理地址被环境变量/启动参数锁定，则展示只读说明并禁止修改；同时明确区分“保存值 `server_addr`”与“当前生效值 `effective_server_addr`” |
| `web/src/components/custom/client/AddClientDialog.tsx` | 继续展示默认推荐连接地址，但文案必须明确：Client 可以连接任意可达地址，`server_addr` 只是默认推荐值，不是唯一允许连接地址；若存在锁定的 `effective_server_addr`，应明确展示当前生效来源 |
| `web/src/lib/server-address.ts` | 复用或补充“提取并归一化 host”的工具逻辑，供前端做基础冲突校验，并与后端共享同一管理 Host 心智 |

### E2E 与构建链路

| 文件 | 规划 |
|------|------|
| `test/e2e/http_domain_e2e_test.go`（建议新增） | 直接验证 Host 分流、合法内部 WS 握手、非法子协议不误判、离线 503、恢复后重新可用 |
| `test/e2e/compose_stack_e2e_test.go` | 复用 Compose 栈做 nginx / caddy 下的 Host 透传与 `Sec-WebSocket-Protocol` 透传验证 |
| `test/e2e/proxy_e2e_test.go` | 如需兼容现有 TCP E2E，可拆分或补充 HTTP 场景 |
| `test/e2e/nginx.conf.template` | 以 E2E 结果为准；若默认配置未保留 `Host` 或 `Sec-WebSocket-Protocol`，则模板必须显式补齐转发配置 |
| `test/e2e/Caddyfile` | 以 E2E 结果为准；若默认配置未保留 `Host` 或 `Sec-WebSocket-Protocol`，则模板必须显式补齐转发配置 |
| `Makefile` | 视需要增加更明确的 HTTP E2E 目标，或扩展现有 compose 测试说明 |

## 接口与校验策略建议

## 零、内部 WS 子协议不是用户配置项

- `netsgo-control.v1` / `netsgo-data.v1` 是内部传输契约，由 server / client 自动处理
- 前端管理界面不暴露任何“内部通道子协议”输入项
- 业务 HTTP 隧道配置也不允许用户自定义这两个头来“声明自己是内部请求”

## 一、创建 / 编辑 HTTP 隧道

后端应做最终权威校验，前端只做基础校验。

### 前端基础校验

- `domain` 非空
- `domain` 格式合法
- `domain` 不等于当前生效管理 Host
- `local_ip` 非空
- `local_port` 在 1-65535

### 后端权威校验

- `domain` 合法且已归一化
- `domain` 不与生效管理 Host 冲突
- `domain` 不与任何现存 HTTP 隧道冲突（含离线 / 持久化）
- HTTP create / resume / restore 不对上游 `local_ip:local_port` 做健康探测
- HTTP create 仍要求目标 Client 当前在线且数据面可用
- 已存在的 HTTP 隧道在目标 Client 离线时，仍允许 `update / pause / delete`
- 已存在的 HTTP 隧道在目标 Client 离线时，不允许 `resume / stop`
- 若请求体仍出现 `remote_port`，服务端直接忽略或覆盖为 `0`，不把它作为 HTTP 隧道的业务判定依据
- 只拿 `domain` 与生效管理 Host 做冲突判断；不把任意 Client 连接地址都当成保留 Host

### 失败返回建议

创建 / 编辑 HTTP 隧道冲突时，建议返回 `409 Conflict`，并带上结构化信息，至少能区分：

- `server_addr_conflict`
- `http_tunnel_conflict`
- `client_offline_action_not_allowed`

这样前端可以给出明确错误，而不是统一 toast “保存失败”。

## 二、修改生效管理地址

继续沿用现有 `dry_run=true` 机制，但扩展返回体。

建议 `GET /api/admin/config` 额外返回：

- `server_addr`：管理平台保存的默认地址 / 推荐连接地址
- `effective_server_addr`：当前真正生效的管理地址
- `server_addr_locked`：当前是否被环境变量 / 启动参数锁定

建议 dry-run 返回：

- `affected_tunnels`：已有端口白名单影响列表（保持现状）
- `conflicting_http_tunnels`：与新 `server_addr` host 冲突的 HTTP 隧道列表（新增）

规则：

- 若 `server_addr_locked=true`，则前端直接以只读方式展示，且不再尝试提交新的 `server_addr`
- 若 `conflicting_http_tunnels` 非空，则前端直接禁止保存
- 实际保存接口也必须再次做相同校验，不能只依赖前端 dry-run
- `server_addr` 表示“持久化保存的默认地址 / 推荐连接地址”
- `effective_server_addr` 表示“当前真正生效的管理地址”
- 两者都不意味着“Client 只能通过这个地址连接”

建议进一步明确 HTTP 状态码与返回结构：

- `dry_run=true`：
  - 始终返回 `200 OK`
  - 返回完整结构化结果，供前端展示
- 实际 `PUT /api/admin/config`：
  - 若 `server_addr_locked=true` 且请求试图修改 `server_addr`，返回 `409 Conflict`
  - 若存在 `conflicting_http_tunnels`，返回 `409 Conflict`
  - 返回与 dry-run 同结构的冲突列表，避免前端只能拿到一条字符串错误
- 若同时存在：
  - `affected_tunnels` 非空
  - `conflicting_http_tunnels` 非空
  - 则以前者“可确认继续”的语义和后者“必须阻止保存”的语义分离处理
  - 也就是：端口白名单影响可以二次确认，HTTP 域名冲突必须阻止保存

## TDD 实施顺序

## 阶段 A：先补纯后端单元测试

目标：把规则先钉死，不急着改实现。

建议先写失败测试：

1. 域名归一化
2. 域名格式校验
3. `canonicalHost(server_addr)` 对 URL / `host` / `host:port` 的提取
4. `/ws/control` / `/ws/data` 的 path + 子协议识别 helper
5. 生效管理 Host 与 tunnel domain 冲突
6. 两个 HTTP 隧道 domain 冲突
7. 域名声明在 `pending/paused/stopped/error` 仍被保留
8. 离线 HTTP 隧道 `edit / pause / delete` 的 store-first 语义
9. runtime / store / placeholder 路径不会丢失 `domain`
10. trusted proxy 下 `Host` / `X-Forwarded-*` 的计算规则

建议测试文件：

- `internal/server/http_tunnel_test.go`
- `internal/server/admin_api_test.go`

## 阶段 B：补服务端集成测试

目标：先定义入口行为，再改路由。

建议先写失败测试：

1. 合法内部 WS 握手可以在非管理 Host 上建立控制 / 数据通道
2. `/ws/control` / `/ws/data` 缺失或带错子协议时，不会误入内部通道
3. Host 命中 HTTP 隧道时，请求不进入管理 API
4. Host 命中 HTTP 隧道时，任意 path 都转发到业务服务
5. 只有生效管理 Host 能访问管理面 / API / SSE
6. HTTP 隧道 `pending` / 暂停 / 停止 / 异常 / 离线时返回 `503`
7. Host 命中 active HTTP 隧道但单次代理失败时返回 `502`
8. 业务 WebSocket Upgrade 可以打通
9. SSE / chunked response 不被截断
10. Host 未命中任何 HTTP 隧道且不属于生效管理 Host 时返回 `404`
11. 业务后端收到的原始 `Host` 不被改写，`X-Forwarded-*` 符合 trusted proxy 规则
12. 服务未初始化时，setup UI 所需入口 / 静态资源 / `/api/setup/*` 不被 Host 规则误伤
13. 未知 Host 不会落入管理前端 `handleWeb` 的 SPA fallback
14. Host 分流测试走最终 handler，而不是只走内部 `newHTTPMux()`

建议测试文件：

- `internal/server/server_test.go`
- `internal/server/proxy_test.go` 或 `internal/server/http_proxy_test.go`

## 阶段 C：补生命周期与持久化测试

目标：确保不因状态迁移破坏域名占用与请求行为。

建议先写失败测试：

1. 创建 HTTP 隧道后域名被声明
2. 编辑 HTTP 隧道时旧域名释放、新域名声明
3. 删除后域名释放
4. 离线 HTTP 隧道仍可 `edit / pause / delete`，且以 store 为真值
5. Client 断线不会把 `active` 隧道写回成 `paused`
6. Client 断线后访问域名返回 `503`
7. 服务端重启 + Client 未恢复前访问域名返回 `503`
8. Client 重连恢复后域名重新可服务
9. create / resume / restore 不因上游服务未启动而失败
10. 上游未启动时，第一次真实请求返回 `502`
11. `pending` / `paused` / `stopped` / `error` 占位记录保留 `domain`

建议测试文件：

- `internal/server/tunnel_manager.go` 对应测试
- `internal/server/server_test.go`

## 阶段 D：补 E2E

目标：确认真实部署路径下 Host 不丢失，且 nginx / caddy / 直接访问都工作正常。

建议场景：

1. 不经过反向代理，直接以 Host 头命中 HTTP 隧道
2. 不经过反向代理，Client 通过非管理 Host 建立 `/ws/control` / `/ws/data` 且子协议生效
3. nginx 反代后 Host 与 `Sec-WebSocket-Protocol` 保持不变，HTTP 隧道与内部 WS 都可用
4. caddy 反代后 Host 与 `Sec-WebSocket-Protocol` 保持不变，HTTP 隧道与内部 WS 都可用
5. 反代重启后 HTTP 隧道仍可恢复
6. Client 重启后 HTTP 隧道恢复
7. 业务后端观测到的 Host 与外部请求域名一致

建议测试文件：

- `test/e2e/http_domain_e2e_test.go`
- `test/e2e/compose_stack_e2e_test.go`

## 阶段 E：前端联调与构建回归

仓库当前没有现成的前端单元测试框架，因此本阶段不强行引入新测试基础设施；前端以“后端冲突测试 + 前端构建回归 + 手工交互清单”组合验证。

前端验证点：

1. 选择 HTTP 类型时表单显示正确
2. `server_addr` 冲突时前端即时提示
3. 后端返回 `409` 时前端提示明确
4. 管理配置页 dry-run 能展示 HTTP 域名冲突
5. 生效管理地址被锁定时配置页展示为只读
6. 隧道列表将 `pending / paused / stopped / error` 与 offline 分开展示，且三处视图共用同一派生规则
7. Client 接入文案不会把管理 Host 错写成唯一连接地址
8. 配置页能同时解释 `server_addr`（保存值）与 `effective_server_addr`（生效值）

最少验证：

- `cd web && bun run build`

## 测试目标清单

### 必须新增的后端测试目标

- 域名归一化与格式校验
- 生效管理地址 host 提取与归一化
- 内部 WS 的 path + 子协议识别
- 生效管理地址冲突校验
- 生效管理地址锁定语义
- HTTP 隧道全局唯一校验（含离线隧道）
- HTTP 隧道不依赖 `remote_port` 的语义约束
- Client 内部通道可通过非管理 Host 建立
- 缺失 / 错误子协议时不能误入内部通道
- Host 级请求分流
- 只有生效管理 Host 能访问管理面
- 未知 Host 不会落入管理前端 SPA fallback
- 配置状态与当前可服务性分离
- `503` / `502` 行为
- 未知域名 `404` 行为
- setup 阶段 setup UI / 静态资源 / `/api/setup/*` 的 Host 例外
- WebSocket / SSE
- 原始 `Host` 透传
- `X-Forwarded-Host` / `X-Forwarded-Proto` / `X-Forwarded-For` 的 trusted proxy 语义
- runtime / store / placeholder 不丢 `domain`
- 离线 HTTP 隧道 `edit / pause / delete`
- 最终 handler 入口覆盖，而不是只测内部 mux
- 生命周期：create / update / pause / resume / stop / delete / restore

### 必须保留的现有回归目标

- TCP 隧道不受影响
- UDP 隧道不受影响
- 管理 API / Web 面板 / SSE 不受影响
- 控制通道 / 数据通道在合法子协议下正常工作
- 端口白名单现有逻辑不受影响
- 端口白名单 / 端口冲突 / 端口影响扫描仍只作用于 `tcp` / `udp`

### 建议执行命令

```bash
go test ./internal/server/... -count=1
go test ./internal/client/... -count=1
go test ./pkg/protocol/... -count=1

cd web && bun run build

make test-e2e-nginx
make test-e2e-caddy
make test-compose-stack-nginx
make test-compose-stack-caddy
```

若本次实现新增独立的 HTTP E2E 入口，也应补充对应 `make` 目标。

## 回归约束与风险

## 一、实现边界与回归约束

- 本期不考虑现有 HTTP 假实现、老数据迁移或旧 Client 不带内部 WS 子协议的兼容
- HTTP 隧道相关旧代码可以直接删除重建
- 不能破坏现有 TCP / UDP 隧道
- 不能破坏现有管理面登录、API、SSE，以及在合法子协议下的控制通道、数据通道
- 不能因为 HTTP 隧道引入 path-based 路由副作用
- 不能把业务流量错误加上管理面的安全响应头

## 二、主要风险点

| 风险 | 说明 | 应对 |
|------|------|------|
| **[P0] 未严格校验内部 WS 子协议** | 普通业务 `/ws/control` / `/ws/data` 请求可能被误判成 NetsGo 内部通道，导致业务流量与 Client 流量混线 | 服务端必须同时校验 path + `Sec-WebSocket-Protocol`；Client Dial 必发对应子协议；补合法 / 非法握手测试 |
| **[P0] HTTP 走进 TCP 监听分支** | `activatePreparedTunnel` 只区分 UDP / 其他，HTTP 隧道 `RemotePort == 0` 时会调用 `net.Listen("tcp", ":0")` 绑随机 TCP 端口，与"不监听公网端口"语义完全冲突 | 在 UDP 判断前加 `type=http` early-return 分支，直接进入 HTTP 激活流程 |
| **[P0] `domain` 在运行时 / 恢复路径中丢失** | `prepareProxyTunnel` 构造 `ProxyConfig` 时漏掉 `Domain` 字段；`restoreManagedTunnel`、`resumeManagedTunnel` 均调用此函数，恢复/恢复路径的 `Domain` 也会消失；HTTP 隧道建成后路由键实际为空 | 修复 `prepareProxyTunnel` 补全 `Domain: req.Domain`，使其与 `upsertTunnelPlaceholder` 一致；用共享 helper 统一构造 runtime / store / placeholder，并补回归测试 |
| **[P1] `restoreTunnels` 占位记录漏 `Domain`** | `restoreTunnels` 为 `paused / stopped / error` 及端口不在白名单时构造的两处占位 `ProxyTunnel`，均未从 `StoredTunnel` 取 `Domain`；重启后 HTTP 隧道路由键变空字符串，Host 分发失效且冲突扫描漏判 | 这两处占位构造与 `prepareProxyTunnel` bug 一同修复，补全 `Domain: st.Domain`；补对应重启恢复场景的集成测试 |
| **[P1] 既有 HTTP 隧道若仍走 liveClient 查找，会导致离线操作返回 404** | 当前 update / delete 逻辑偏向在线 runtime；若不改成 store-first，将与“离线既有隧道可编辑 / 暂停 / 删除”的目标直接冲突 | 对既有 HTTP 隧道统一以 store 为配置真值；Client 在线时再同步 runtime |
| **[P1] 断线仍被写回 `paused`** | 会把“配置状态”和“会话状态”混在一起，前端也会错误展示为用户主动暂停 | 明确断线只影响 session online/offline，不写回 tunnel status；补生命周期测试与前端展示回归 |
| **[P1] 反向代理丢失 `Sec-WebSocket-Protocol`** | Client 经 nginx / caddy / L7 入口接入时，控制 / 数据通道握手失败 | E2E 明确验证 `Sec-WebSocket-Protocol` 透传；服务端在子协议不合法时 fail closed |
| Host 路由优先级错误 | 命中 HTTP 隧道后仍进入管理 API | 先写 Host 级集成测试 |
| 管理面 SPA fallback 误吞未知 Host | 外层 Host 收紧写得不严时，未知 Host 仍可能进入 `handleWeb` 并返回管理前端 `index.html` | Host 分类必须发生在 `handleWeb` 之前；未知 Host 直接 `404`，补最终 handler 测试 |
| 域名声明只看在线内存 | 离线隧道被漏判，导致错误回落管理面 | 冲突扫描和请求命中都覆盖 `store` |
| 更新域名时旧值/新值切换不原子 | 产生短暂双占用或空窗 | 校验和写入放在同一 `proxyMu.Lock()` 内，并写测试 |
| 管理面安全头污染业务响应 | `securityHeadersHandler` 套在外层，导致 HTTP 隧道响应带上 CSP / X-Frame-Options 等管理面安全头，破坏业务前端 | `securityHeadersHandler` 只包住管理面分支，HTTP 隧道 handler 完全绕开 |
| 反代保留 Host 假设不成立 | nginx/caddy 配置改坏后行为异常 | E2E 明确验证 Host 透传 |
| trusted proxy 头被盲信 | 上游拿到伪造的客户端 IP / proto / host，日志与鉴权依据都可能错误 | 复用现有 trusted proxy helper；只对可信代理透传链条做追加，其余情况重算 |
| 额外维护独立 HTTP 域名索引 | 引入第三份状态，增加一致性成本 | 使用共享 helper 按需扫描 runtime + store，不引入额外长期索引 |
| 只做前端校验 | 离线冲突无法发现 | 后端必须做最终权威校验 |
| 生效管理地址来源不清 | 访问控制与冲突校验不一致；`Server` 结构体缺少承载运行时生效地址的字段 | 明确 env / flag > admin config 的优先级，并统一走 `canonicalHost()`；在 `Server` 结构体新增 `EffectiveServerAddr` 字段由启动时写入 |
| 锁定地址仍被 UI 或 API 改写 | 用户误操作导致管理面不可达 | 配置页只读 + 后端拒绝修改双保险 |
| `remote_port` 心智未收敛 | HTTP 隧道继续被错误纳入端口逻辑 | 在文档、UI、测试中把 HTTP 的 `remote_port` 降为内部无意义字段 |
| 未知域名回落管理面 | 误配域名直接暴露管理登录页 | 把未知域名与管理面保留入口分开测试与实现 |
| setup 阶段被 Host 收紧误伤 | 首次初始化入口直接不可用 | 明确 setup UI / 静态资源 / `/api/setup/*` 例外并补测试 |
| 测试只覆盖内部 mux | 实际最外层 Host 分流改错却测不出来；`StartHTTPOnly()` 返回旧 mux 也会成为无效覆盖 | Host 分流测试必须覆盖最终 handler；`StartHTTPOnly()` 同步改为返回新外层 handler |

## 验收标准

满足以下条件后，视为本期 HTTP 域名隧道可进入实现评审：

1. 文档中定义的域名语义、冲突规则、状态语义已被团队认可
2. 测试用例已先于实现落地，至少完成后端单元 / 集成测试设计
3. 文件级改动边界明确，不引入与本需求无关的大重构
4. 已明确 `local_ip` 在 HTTP 隧道中保留，且允许指向 Client 所在 LAN 的其他主机
5. 已明确生效管理 Host 的来源、严格访问规则、锁定语义，以及它与 HTTP 隧道域名的双向冲突校验
6. 已明确内部控制 / 数据通道只通过 path + WebSocket 子协议识别，Client 可连接任意可达地址
7. 已明确 HTTP create / resume / restore 不做上游主动探测，真实失败通过请求期 `502` 暴露
8. 已明确“永不按路径分流”的长期约束，但显式合法的 NetsGo 内部 WS 握手属于协议例外
9. 已明确离线已存在 HTTP 隧道可编辑 / 暂停 / 删除，且以 store 为配置真值恢复
10. 已明确 trusted proxy 下 `Host` 与 `X-Forwarded-*` 的行为
11. 已明确测试必须贴近最终入口场景，而不是只测内部子 handler

## 推荐实现顺序（执行版）

1. **先固化共享契约与已拍板决策**
   - 定义 `netsgo-control.v1` / `netsgo-data.v1` 这两个内部 WS 子协议常量
   - 明确 `server_addr` 是持久化默认地址 / 推荐连接地址，`effective_server_addr` 才是运行时生效管理地址
   - 明确既有 HTTP 隧道的配置真值在 store，断线不写回 `paused`
2. **优先修复已确认的前置 bug**（不改就跑不通，先修再测）
   - 修复 `activatePreparedTunnel`：在 UDP 分支前加 `type=http` early-return，不走 TCP 监听
   - 修复 `prepareProxyTunnel`：补全 `Domain: req.Domain`（同步覆盖 `restoreManagedTunnel` / `resumeManagedTunnel` 路径）
   - 修复 `restoreTunnels`：两处占位构造（paused/stopped/error 及端口不在白名单）均补全 `Domain: st.Domain`
3. 先补后端纯规则测试：域名规则、生效管理 Host、`canonicalHost()`、内部 WS 子协议识别、trusted proxy 头语义
4. 再补 runtime / store / placeholder 的 `domain` 一致性，以及离线 `edit / pause / delete` / serviceability 生命周期测试
5. 再补入口集成测试：`hostDispatchHandler` 顺序、setup 例外、合法 / 非法内部 WS 握手、`503` / `502` / `404`
6. 再实现后端 HTTP 域名隧道最小闭环
   - `hostDispatchHandler` 按“内部 WS -> HTTP 域名 -> setup -> 管理面 -> 404”收口
   - `securityHeadersHandler` 只包管理面
   - `StartHTTPOnly()` 同步改为返回新外层 handler
   - `EffectiveServerAddr` 字段写入并参与冲突校验
7. 再实现 Client 侧子协议发送与业务回归
   - control / data Dial 必须发送对应 WS 子协议
   - HTTP 仍复用 TCP 数据面，不新增 ready 阶段探测
8. 再补 nginx / caddy E2E，确认 Host 与 `Sec-WebSocket-Protocol` 都能透传
9. 最后重构前端表单、只读提示、冲突提示、状态展示和 Client 接入文案
   - 先落统一的 tunnel view model，再让列表 / 概览 / 详情页复用
   - 明确区分 `server_addr`（保存值）与 `effective_server_addr`（生效值）

这样做的原因是：

- 先把后端规则钉死，避免边实现边改语义
- 先把内部通道识别与 Host 路由边界钉死，避免业务流量和 Client 流量混线
- 先保证系统行为稳定，再做前端交互优化
- 先守住现有 TCP / UDP / 管理面的回归，再扩展 HTTP 能力
