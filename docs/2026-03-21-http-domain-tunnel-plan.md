# 规划：HTTP 域名隧道（TDD 草案）

> 创建时间：2026-03-21  
> 状态：待评审  
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
2. 尽量不改动双端协议，不新造并行协议
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
- `internal/server/tunnel_manager.go` 当前隧道 CRUD 仍主要围绕在线 Client 运行时，离线隧道的编辑 / 删除语义尚未补齐
- 前端 `web/src/components/custom/tunnel/TunnelDialog.tsx` 当前仍以“公网端口”心智为主，HTTP 未完成独立表单
- `domain` 目前只是配置字段，还不是运行时路由键

## 目标行为定义

## 一、HTTP 隧道的语义

HTTP 隧道定义为：

- `type=http`
- `domain`：公网访问域名（路由键）
- `local_ip`：Client 侧可达的上游主机，默认 `127.0.0.1`
- `local_port`：Client 侧可达的上游端口
- `remote_port`：不属于 HTTP 隧道的业务语义

关于 `remote_port`，本期明确约束如下：

- HTTP 隧道对用户配置、前端表单、接口文档都不再暴露 `remote_port`
- HTTP 隧道不参与端口监听、端口冲突、端口白名单检查
- 所有端口相关逻辑都必须先按 `type` 分支：
  - 端口监听仅适用于 `tcp` / `udp`
  - 端口冲突检查仅适用于 `tcp` / `udp`
  - 端口白名单检查及其影响扫描仅适用于 `tcp` / `udp`
- 若实现阶段为了复用共享协议 / 存储结构，内部仍暂时保留该字段，则它只能作为兼容占位字段存在：
  - 写入时统一归零
  - 在 HTTP 分支中的读取、比较、展示、测试时一律忽略
- 后续若有机会拆分更贴切的 HTTP 隧道输入模型，方向应是“彻底不再依赖 `remote_port`”，而不是继续围绕 `0` 做产品语义设计

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

请求分类顺序应当是：

1. 先取请求 Host 并归一化
2. 如果 Host 命中已声明的 HTTP 隧道域名，则整条请求进入 HTTP 隧道逻辑
3. 如果 Host 未命中任何 HTTP 隧道，且服务尚未初始化，则允许 setup UI 所需的管理前端入口、静态资源和 `/api/setup/*`
4. 如果 Host 未命中任何 HTTP 隧道，且 Host 命中生效管理 Host，则走现有管理面 / API / WebSocket 逻辑
5. 其他所有情况一律返回 `404`

这里的“整条请求进入 HTTP 隧道逻辑”是指：

- 无论路径是 `/`
- 还是 `/api/foo`
- 还是 `/ws/chat`

都只看 Host，不再看 path。

也就是说，一旦 Host 命中 HTTP 隧道，就不应该再让 `/api/*`、`/ws/*` 回落到 NetsGo 的管理 API 或控制通道。

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
- `/ws/control`
- `/ws/data`

这意味着：

- HTTP 隧道业务域名无论访问什么 path，都不能触达管理平台任何资源
- 错误域名、其他域名、其他 IP、`localhost`、本机名等，只要不是生效管理 Host，都不能访问管理平台任何资源
- 管理面与 HTTP 隧道的边界完全由 Host 规则决定，而不是依赖前端路由模式

这样可以避免：

- 误配 DNS 后直接看到管理平台
- 已删除或写错的业务域名回落到管理平台
- 业务域名与管理域名边界不清

### 初始化阶段例外

在服务尚未初始化时：

- 此时还不存在有效的 HTTP 隧道域名声明
- setup UI 所需的管理前端入口、静态资源和 `/api/setup/*` 可暂不受 Host 收紧规则影响
- 初始化完成后，应立即切换到严格的生效管理 Host 校验

## 三、反向代理假设

本次设计默认 HTTP 隧道路由只依赖“最终到达 NetsGo 时的 `Host` 值”。

因此：

- HTTP 隧道既可以直接访问到 NetsGo，也可以先经过反向代理
- 反向代理不是前置条件，但必须正确透传 `Host`
- 若反向代理改写 `Host`，则属于部署错误，不由 HTTP 隧道逻辑兜底修正
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
- 生效管理地址是管理平台 / Client 连接地址

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

1. 创建 / 编辑 HTTP 隧道时，若 `domain` 与 `server_addr` host 相同，禁止保存
2. 修改 `server_addr` 时，若其 host 与任一已有 HTTP 隧道 `domain` 冲突，禁止保存

### 3. 生命周期中的保留规则

HTTP 域名声明应当在以下状态仍然被视为“已占用”：

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
- 允许删除

离线隧道的编辑 / 删除语义应尽量保持简单：

- 编辑 / 删除时，以持久化存储为准立即生效
- 若 Client 当前在线，再同步运行时状态
- 若 Client 当前离线，则待其下次上线时按新的持久化配置恢复
- 删除离线 HTTP 隧道后，域名立即释放

为避免范围膨胀，本期不扩展“给离线 Client 创建全新 HTTP 隧道”的能力：

- 创建新 HTTP 隧道仍要求目标 Client 当前在线且数据面可用

## 六、请求行为约定

### 已命中 HTTP 隧道域名

#### `active` 且 Client / 数据面可用

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

#### 已命中域名，但隧道不可服务

包括：

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

### 未命中任何 HTTP 隧道域名

本期建议做更细的兼容区分：

- 若 Host 命中生效管理 Host，则继续走现有管理面 / API / WebSocket 逻辑
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
- `X-Forwarded-For`
- `X-Forwarded-Host`
- `X-Forwarded-Proto`

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
- `paused` / `stopped` / `error` 占位记录也必须保留 `domain`
- create / update / restore / delete 等生命周期动作，不允许分别手写多套“看起来差不多”的结构体组装逻辑
- 应尽量收敛到共享 helper，避免 `domain` 在某条分支里被漏写或覆盖为空

### 1.2 离线隧道的编辑 / 删除一致性

对已持久化的 HTTP 隧道，离线编辑 / 删除还需要满足：

- 编辑后新的 `domain`、`local_ip`、`local_port` 必须立即写入持久化存储
- 删除后持久化记录立即移除
- 这些变更会立刻影响域名占用判断与管理面冲突校验
- Client 后续重连时，以最新持久化结果恢复

### 2. 服务端重启后的行为

如果服务端重启，但原有 HTTP 隧道仍存在于 `store`：

- 在 Client 尚未重连前
- 该域名仍应被视为已声明
- 命中后应返回 `503`

### 3. Client 断线后的行为

当前系统断线时会把活跃隧道转为暂停语义；HTTP 隧道在此基础上还需要满足：

- 域名声明继续保留
- 外部请求不应落到管理面
- 应返回 `503`

## 用户场景清单

| 场景 | 预期行为 |
|------|---------|
| 创建 `type=http`，`domain=app.example.com`，`127.0.0.1:3000` | 创建成功，请求按域名进入本地 3000 |
| 创建 `type=http`，`domain=printer.office.example`，`192.168.1.50:8080` | 创建成功，Client 作为 LAN 入口代理到其他设备 |
| 创建 `type=http` | 不再要求用户配置 `remote_port` |
| HTTP 隧道走端口白名单、端口冲突、端口影响扫描 | 不参与；这些逻辑只适用于 `tcp` / `udp` |
| 同一 Client 两条不同域名指向同一 `local_ip:local_port` | 允许 |
| 两个不同 Client 使用同一域名 | 禁止，返回冲突 |
| HTTP 隧道域名等于当前 `server_addr` host | 禁止，返回冲突 |
| 修改 `server_addr` 为某个已存在 HTTP 隧道的域名 | 禁止保存，返回冲突清单 |
| 通过 `server_addr` host 访问 `/api/*` | 进入管理 API |
| 通过 HTTP 隧道业务域名访问 `/`、静态资源、`/api/*`、`/ws/*` | 都只进入业务服务，绝不触达管理平台 |
| 通过非 `server_addr` host（包括其他域名、IP、`localhost` 等）访问管理面 | 返回 `404`，不暴露管理平台 |
| 服务未初始化时通过任意 Host 访问 setup UI 所需入口、静态资源和 `/api/setup/*` | 允许 |
| 使用未知业务域名访问 NetsGo | 返回 `404`，不回落管理面 |
| HTTP 隧道暂停 / 停止 / 异常 | 访问该域名返回 `503` |
| Client 断线或服务端重启后 Client 未恢复 | 访问该域名返回 `503` |
| 命中 HTTP 隧道域名且路径为 `/api/foo` | 仍然代理到业务服务，不进入 NetsGo API |
| 命中 HTTP 隧道域名且路径为 `/ws/chat` | 仍然作为业务 WebSocket 代理 |
| 创建 / 恢复时上游 `local_ip:local_port` 尚未启动 | 仍允许创建 / 恢复；第一次真实请求失败时返回 `502` |
| 目标 Client 离线时创建新的 HTTP 隧道 | 本期不支持；仍要求 Client 在线 |
| 离线 Client 的既有 HTTP 隧道编辑 / 删除 | 允许；修改立即写入 store，下次上线按新配置恢复 |
| 生效管理地址由环境变量 / 启动参数锁定 | 管理页展示只读说明，后端拒绝修改 |

## 文件级规划

以下规划以“最小必要改动 + 明确职责边界”为原则。

### 后端：协议与模型

| 文件 | 规划 |
|------|------|
| `pkg/protocol/types.go` | 原则上不新增字段，仅补充注释或测试，明确 HTTP 语义是域名型，不再依赖 `remote_port` |
| `pkg/protocol/message.go` | 原则上不新增字段，仅补充注释或测试 |
| `pkg/protocol/message_test.go` | 补充 HTTP 语义相关回归测试（字段不变、序列化不破坏） |

### 后端：HTTP 域名隧道核心

| 文件 | 规划 |
|------|------|
| `internal/server/http_tunnel.go`（建议新增） | 承载 HTTP 域名归一化、生效管理 Host 提取、runtime + store 共享声明扫描 helper、Host 命中判定、HTTP 反向代理入口 |
| `internal/server/http_tunnel_test.go`（建议新增） | 域名校验、冲突扫描、Host 匹配、状态到返回码映射等单元测试 |
| `internal/server/server.go` | 在现有 HTTP 入口外层增加 Host 级分发；管理面安全头只作用于生效管理 Host；初始化阶段保留 setup UI 所需入口 / 静态资源 / `/api/setup/*` 例外；错误 Host 不得访问管理平台任何资源 |
| `internal/server/proxy.go` | `type=http` 时不再监听公网 TCP 端口，而是进入 HTTP 域名隧道激活流程；同时保证运行时配置中不会丢失 `domain`，且 HTTP 分支不再依赖 `remote_port` 业务语义 |
| `internal/server/tunnel_manager.go` | 在 create / update / pause / resume / stop / delete / restore 流程中维护 HTTP 域名声明；编辑域名时保证原子替换；通过共享 helper 维护 runtime/store/placeholder 一致性；已持久化 HTTP 隧道支持离线编辑 / 删除；HTTP create / resume / restore 不做上游可达性预检查 |
| `internal/server/store.go` | 复用现有 `Domain` 持久化；必要时补充便于按 `type=http` 扫描全部域名声明、以及离线编辑 / 删除既有隧道的辅助方法 |
| `internal/server/admin_api.go` | 扩展 `GET/PUT /api/admin/config` 及 dry-run，返回生效管理地址及其锁定信息；检查生效管理 Host 与 HTTP 隧道域名冲突；锁定时拒绝修改 `server_addr`；冲突时返回结构化结果而不是仅返回字符串错误 |
| `internal/server/admin_api_test.go` | 覆盖 `server_addr` 与 HTTP 隧道冲突的 dry-run / save 拒绝逻辑 |
| `internal/server/server_test.go` | 覆盖管理面入口与 HTTP 隧道入口共存的集成行为；验证只有生效管理 Host 能访问管理面；Host 分流测试必须走最终 handler，而不是只测内部 `newHTTPMux()` |
| `internal/server/proxy_test.go` 或新建 `internal/server/http_proxy_test.go` | 覆盖 HTTP 请求、SSE、WebSocket、暂停/恢复/离线时的域名路由行为；补“上游未启动时创建仍成功、真实请求返回 `502`”场景 |
| `cmd/netsgo/cmd_server.go` | 增加 `--server-addr` / `NETSGO_SERVER_ADDR` 作为生效管理地址的显式来源；启用后锁定管理面 `server_addr` 配置 |

### 后端：Client 侧

| 文件 | 规划 |
|------|------|
| `internal/client/client.go` | 原则上复用现有“非 UDP 统一按 TCP relay”的实现；HTTP 不新增上游主动探测逻辑，继续保持“接受配置即可 ready” |
| `internal/client/client_test.go` 或新建专项测试 | 回归验证 HTTP 类型仍通过现有 TCP 数据面工作，不破坏原有 TCP 行为，也不把上游应用是否启动耦合到隧道创建阶段 |

### 前端：隧道配置与展示

| 文件 | 规划 |
|------|------|
| `web/src/types/index.ts` | 补充 HTTP 隧道相关冲突返回类型、管理配置 dry-run 冲突类型，以及生效管理地址锁定元信息 |
| `web/src/components/custom/tunnel/TunnelDialog.tsx` | `type=http` 时展示 `domain + local_ip + local_port`；移除 `remote_port` 的用户输入心智；创建和编辑时做前端基础校验 |
| `web/src/components/custom/tunnel/TunnelListTable.tsx` | HTTP 隧道展示为 `domain -> local_ip:local_port`，不再展示 `:remote_port` 作为主映射 |
| `web/src/hooks/use-tunnel-mutations.ts` | 确保 create / update 发送 `domain`，并能消费后端冲突错误；HTTP 分支不再把 `remote_port` 当成用户配置来源 |
| `web/src/routes/admin/config.tsx` | 修改 `server_addr` 时，dry-run 除端口影响外还显示 HTTP 域名冲突；若生效管理地址被环境变量 / 启动参数锁定，则展示只读说明并禁止修改 |
| `web/src/lib/server-address.ts` | 复用或补充“提取并归一化 host”的工具逻辑，供前端做基础冲突校验，并与后端共享同一管理 Host 心智 |
| `web/src/lib/auth.ts` 或控制台入口层 | 基于当前 `window.location.host` 与生效管理 Host 做一次前端自检；不匹配时停止继续进入控制台并展示拒绝访问提示（安全边界仍以后端为准） |

### E2E 与构建链路

| 文件 | 规划 |
|------|------|
| `test/e2e/http_domain_e2e_test.go`（建议新增） | 直接验证 Host 分流、离线 503、恢复后重新可用 |
| `test/e2e/compose_stack_e2e_test.go` | 复用 Compose 栈做 nginx / caddy 下的 Host 透传验证 |
| `test/e2e/proxy_e2e_test.go` | 如需兼容现有 TCP E2E，可拆分或补充 HTTP 场景 |
| `test/e2e/nginx.conf.template` | 一般无需大改，但要在测试中确认 Host 被保留 |
| `test/e2e/Caddyfile` | 一般无需大改，但要在测试中确认 Host 被保留 |
| `Makefile` | 视需要增加更明确的 HTTP E2E 目标，或扩展现有 compose 测试说明 |

## 接口与校验策略建议

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
- 已存在的 HTTP 隧道在目标 Client 离线时，仍允许 update / delete
- 若请求体仍出现 `remote_port`，也不把它作为 HTTP 隧道的业务判定依据

### 失败返回建议

创建 / 编辑 HTTP 隧道冲突时，建议返回 `409 Conflict`，并带上结构化信息，至少能区分：

- `server_addr_conflict`
- `http_tunnel_conflict`

这样前端可以给出明确错误，而不是统一 toast “保存失败”。

## 二、修改生效管理地址

继续沿用现有 `dry_run=true` 机制，但扩展返回体。

建议 `GET /api/admin/config` 额外返回：

- `effective_server_addr`：当前真正生效的管理地址
- `server_addr_locked`：当前是否被环境变量 / 启动参数锁定
- `server_addr_lock_source`：锁定来源说明（如 `env` / `flag`）

建议 dry-run 返回：

- `affected_tunnels`：已有端口白名单影响列表（保持现状）
- `conflicting_http_tunnels`：与新 `server_addr` host 冲突的 HTTP 隧道列表（新增）
- `can_apply`：布尔值；当存在结构性冲突时为 `false`

规则：

- 若 `server_addr_locked=true`，则前端直接以只读方式展示，且不再尝试提交新的 `server_addr`
- 若 `conflicting_http_tunnels` 非空，则前端直接禁止保存
- 实际保存接口也必须再次做相同校验，不能只依赖前端 dry-run

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
4. 生效管理 Host 与 tunnel domain 冲突
5. 两个 HTTP 隧道 domain 冲突
6. 域名声明在 `paused/stopped/error` 仍被保留
7. 离线 HTTP 隧道 edit / delete 的 store 语义
8. runtime / store / placeholder 路径不会丢失 `domain`

建议测试文件：

- `internal/server/http_tunnel_test.go`
- `internal/server/admin_api_test.go`

## 阶段 B：补服务端集成测试

目标：先定义入口行为，再改路由。

建议先写失败测试：

1. Host 命中 HTTP 隧道时，请求不进入管理 API
2. Host 命中 HTTP 隧道时，任意 path 都转发到业务服务
3. 只有生效管理 Host 能访问管理面
4. HTTP 隧道暂停 / 停止 / 异常 / 离线时返回 `503`
5. Host 命中 active HTTP 隧道但单次代理失败时返回 `502`
6. WebSocket Upgrade 可以打通
7. SSE / chunked response 不被截断
8. Host 未命中任何 HTTP 隧道且不属于生效管理 Host 时返回 `404`
9. 业务后端收到的原始 `Host` 不被改写
10. 服务未初始化时，setup UI 所需入口 / 静态资源 / `/api/setup/*` 不被 Host 规则误伤
11. Host 分流测试走最终 handler，而不是只走内部 `newHTTPMux()`

建议测试文件：

- `internal/server/server_test.go`
- `internal/server/proxy_test.go` 或 `internal/server/http_proxy_test.go`

## 阶段 C：补生命周期与持久化测试

目标：确保不因状态迁移破坏域名占用与请求行为。

建议先写失败测试：

1. 创建 HTTP 隧道后域名被声明
2. 编辑 HTTP 隧道时旧域名释放、新域名声明
3. 删除后域名释放
4. 离线 HTTP 隧道仍可 edit / delete
5. Client 断线后访问域名返回 `503`
6. 服务端重启 + Client 未恢复前访问域名返回 `503`
7. Client 重连恢复后域名重新可服务
8. create / resume / restore 不因上游服务未启动而失败
9. 上游未启动时，第一次真实请求返回 `502`
10. paused / stopped / error 占位记录保留 `domain`

建议测试文件：

- `internal/server/tunnel_manager.go` 对应测试
- `internal/server/server_test.go`

## 阶段 D：补 E2E

目标：确认真实部署路径下 Host 不丢失，且 nginx / caddy / 直接访问都工作正常。

建议场景：

1. 不经过反向代理，直接以 Host 头命中 HTTP 隧道
2. nginx 反代后 Host 保持不变，HTTP 隧道可用
3. caddy 反代后 Host 保持不变，HTTP 隧道可用
4. 反代重启后 HTTP 隧道仍可恢复
5. Client 重启后 HTTP 隧道恢复
6. 业务后端观测到的 Host 与外部请求域名一致

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
6. 隧道列表展示正确

最少验证：

- `cd web && bun run build`

## 测试目标清单

### 必须新增的后端测试目标

- 域名归一化与格式校验
- 生效管理地址 host 提取与归一化
- 生效管理地址冲突校验
- 生效管理地址锁定语义
- HTTP 隧道全局唯一校验（含离线隧道）
- HTTP 隧道不依赖 `remote_port` 的语义约束
- Host 级请求分流
- 只有生效管理 Host 能访问管理面
- `503` / `502` 行为
- 未知域名 `404` 行为
- setup 阶段 setup UI / 静态资源 / `/api/setup/*` 的 Host 例外
- WebSocket / SSE
- 原始 `Host` 透传
- runtime / store / placeholder 不丢 `domain`
- 离线 HTTP 隧道 edit / delete
- 最终 handler 入口覆盖，而不是只测内部 mux
- 生命周期：create / update / pause / resume / stop / delete / restore

### 必须保留的现有回归目标

- TCP 隧道不受影响
- UDP 隧道不受影响
- 管理 API / Web 面板 / `/ws/control` / `/ws/data` 不受影响
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

## 兼容性与风险

## 一、对现有系统的兼容性要求

- 不能破坏现有 TCP / UDP 隧道
- 不能破坏现有管理面登录、API、SSE、控制通道、数据通道
- 不能因为 HTTP 隧道引入 path-based 路由副作用
- 不能把业务流量错误加上管理面的安全响应头

## 二、主要风险点

| 风险 | 说明 | 应对 |
|------|------|------|
| Host 路由优先级错误 | 命中 HTTP 隧道后仍进入管理 API | 先写 Host 级集成测试 |
| 域名声明只看在线内存 | 离线隧道被漏判，导致错误回落管理面 | 冲突扫描和请求命中都覆盖 `store` |
| 离线隧道无法编辑 / 删除 | 域名占用无法释放或修正 | 明确 update / delete 以 store 为准，在线时再同步 runtime |
| `domain` 在运行时 / 持久化路径中丢失 | 创建成功后实际没有形成稳定路由键 | 用共享 helper 统一构造 runtime / store / placeholder，并补回归测试 |
| 更新域名时旧值/新值切换不原子 | 产生短暂双占用或空窗 | 在 `tunnel_manager` 中显式设计替换顺序并写测试 |
| 管理面安全头污染业务响应 | 导致业务前端 CSP / iframe / 静态资源异常 | 管理面 handler 与 HTTP 隧道 handler 分开 |
| 反代保留 Host 假设不成立 | nginx/caddy 配置改坏后行为异常 | E2E 明确验证 Host 透传 |
| 额外维护独立 HTTP 域名索引 | 引入第三份状态，增加一致性成本 | 使用共享 helper 按需扫描 runtime + store，不引入额外长期索引 |
| 只做前端校验 | 离线冲突无法发现 | 后端必须做最终权威校验 |
| 生效管理地址来源不清 | 访问控制与冲突校验不一致 | 明确 env / flag > admin config 的优先级，并统一走 `canonicalHost()` |
| 锁定地址仍被 UI 或 API 改写 | 用户误操作导致管理面不可达 | 配置页只读 + 后端拒绝修改双保险 |
| `remote_port` 心智未收敛 | HTTP 隧道继续被错误纳入端口逻辑 | 在文档、UI、测试中把 HTTP 的 `remote_port` 降为内部兼容细节 |
| 未知域名回落管理面 | 误配域名直接暴露管理登录页 | 把未知域名与管理面保留入口分开测试与实现 |
| setup 阶段被 Host 收紧误伤 | 首次初始化入口直接不可用 | 明确 setup UI / 静态资源 / `/api/setup/*` 例外并补测试 |
| 测试只覆盖内部 mux | 实际最外层 Host 分流改错却测不出来 | Host 分流测试必须覆盖最终 handler |

## 验收标准

满足以下条件后，视为本期 HTTP 域名隧道可进入实现评审：

1. 文档中定义的域名语义、冲突规则、状态语义已被团队认可
2. 测试用例已先于实现落地，至少完成后端单元 / 集成测试设计
3. 文件级改动边界明确，不引入与本需求无关的大重构
4. 已明确 `local_ip` 在 HTTP 隧道中保留，且允许指向 Client 所在 LAN 的其他主机
5. 已明确生效管理 Host 的来源、严格访问规则、锁定语义，以及它与 HTTP 隧道域名的双向冲突校验
6. 已明确 HTTP create / resume / restore 不做上游主动探测，真实失败通过请求期 `502` 暴露
7. 已明确“永不按路径分流”的长期约束
8. 已明确离线已存在 HTTP 隧道可编辑 / 删除，且 Client 重连后以 store 为准恢复
9. 已明确测试必须贴近最终入口场景，而不是只测内部子 handler

## 推荐实现顺序（执行版）

1. 先补后端域名规则、生效管理 Host、`canonicalHost()` 与锁定语义测试
2. 再补 runtime / store / placeholder 的 `domain` 一致性，以及离线 edit / delete 测试
3. 再补 Host 路由、setup 例外与生命周期测试
4. 再补 `server_addr` 冲突 dry-run / save 测试
5. 再实现后端 HTTP 域名隧道最小闭环
6. 再补 nginx / caddy E2E
7. 最后调整前端表单、只读提示、冲突提示和展示文案

这样做的原因是：

- 先把后端规则钉死，避免边实现边改语义
- 先保证系统行为稳定，再做前端交互优化
- 先守住现有 TCP / UDP / 管理面的回归，再扩展 HTTP 能力
