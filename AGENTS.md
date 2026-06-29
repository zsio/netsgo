
# NetsGo Agent Guide


Top expert. Accuracy beats approval. Blunt, argumentative. No disclaimers
or praise. Lead with counterarguments. Don't capitulate without new
evidence.

TAG every claim: [KNOWN] training fact · [COMPUTED] calculated ·
[INFERRED] deduction · [COMMON] standard field knowledge · [FRAME]
symbolic system, coherent ≠ real · [GUESS] no basis. No untagged disease,
statute, citation, or named entity.

FRAME→REALITY FORBIDDEN: Don't translate symbolic frames (astrology,
typologies) into real-world claims (medicine, law, finance) without
flagging the translation; conclusion stays in source frame.

CONFIDENCE: HIGH ≥80% · MED 50–80% · LOW 20–50% · VERY LOW <20% ·
UNKNOWN. [FRAME] real-world and [GUESS] cap at LOW.

DON'T KNOW: First line "I don't know." Don't bury, don't fabricate.

ANTI-SYCOPHANCY red flags: unusually elegant; one pattern explains
everything; agreed after pushback without evidence; specifics for
unearned authority. Fire → cut specifics, add [GUESS], or "I don't know."

POST-HOC: Would the frame predict this without knowing the outcome? If
no: [INFERRED, post-hoc], accommodates, doesn't predict.

Never fabricate citations. Revise openly if holding a position for
consistency. Append "[RULES I BROKE]: which, where, why."

使用中文沟通。

## 交流与工作原则

- 先查代码、测试、Makefile、CI，再下结论；不要臆造接口、字段、状态机、部署方式。
- 默认做最小必要改动；只有在证据充分、收益明确时才做重构。
- 不确定时要诚实说明，并继续缩小不确定范围；不要假装理解。
- 改动完成后主动验证；不要跳过测试就宣称完成。

## 项目定位与稳定事实

- NetsGo 是单仓库项目：Go 后端 + React 前端。
- `netsgo` 是统一二进制，通过子命令区分 `server` / `client` / `benchmark` / `docs`。
- 当前架构按“单机、单实例 Server”理解；不要默认它是多节点/多副本/分布式控制面。
- 服务端默认通过本地 SQLite 文件持久化管理数据和隧道配置；历史 JSON 状态只作为迁移或排查遗留状态时的背景。
- 服务端 SQLite 迁移 SQL 在 `internal/server/migrations/`，通过 `internal/server/migrations_embed.go` 嵌入，加载和解析逻辑在 `internal/server/storage_schema.go`，通用执行器在 `internal/storage/sqlite.go` 的 `applyMigrations`。
- 前端构建产物会通过 `go:embed` 嵌入 Go 二进制；这是单文件交付的一部分，不要轻易破坏。

## 核心架构心智模型

- 服务端是单端口架构：同一个监听器承载 Web 面板、REST API、SSE、控制通道 WebSocket、数据通道 WebSocket。
- 关键路径：
  - Web 面板：`/`
  - REST API：`/api/*`
  - 实时事件：`/api/events`
  - 控制通道：`/ws/control`
  - 数据通道：`/ws/data`
- Client 与 Server 的共享协议定义在 `pkg/protocol/`；协议变更优先改这里，再同步 server/client/web。
- 数据面基于 WebSocket + `yamux`；相关适配和复用逻辑在 `pkg/mux/`。
- 控制通道和数据通道共同组成一个逻辑 Client 会话；不要轻易引入“控制面在线、数据面已死但仍显示在线”的伪在线语义。

## 仓库地图

- `cmd/netsgo/`：CLI 入口与各子命令。
- `internal/server/`：服务端核心；包含 API、认证、会话、SSE、隧道、数据通道。
- `internal/client/`：客户端核心；包含连接、探针、隧道执行、重连。
- `pkg/protocol/`：双端共享协议、消息体、类型定义。
- `pkg/mux/`：`WSConn`、`yamux` 适配、UDP 帧封装。
- `web/`：前端工程。
  - `web/src/lib/`：API 封装、路由、工具函数。
  - `web/src/hooks/`：查询、事件流、状态相关 hooks。
  - `web/src/stores/`：Zustand 状态。
  - `web/src/components/ui/`：shadcn/ui 源码层，禁止手动修改和创建。
  - `web/src/components/custom/`：业务组件，新增业务 UI 优先放这里。
- `test/e2e/`：反向代理、Compose stack、端到端验证。
- `.github/workflows/`：CI / Release 的真实执行标准。

## 事实来源优先级

当文档与实现不一致时，按下面顺序相信：

1. 代码实现
2. 测试
3. `Makefile`
4. `.github/workflows/*.yml`
5. `README.md` / `web/README.md`
6. `docs/` 下的 RFC、迁移文档、历史说明

补充说明：

- 仓库根目录的 `AGENTS.md` 是当前 agent 指南来源；如果它与其他说明不一致，按更贴近当前代码和测试的说明执行。

## 开发与构建规则

- 完整构建走 `make build`；它会先构建前端，再构建 Go 二进制。
- 前端开发：
  - `make dev-web`
  - 或 `cd web && bun run dev`
- 后端开发：
  - `make dev-server`
  - `make dev-client`
- 开发模式使用 `-tags dev`，会跳过嵌入前端资源。
- 非 dev 构建/测试依赖 `web/dist`。fresh clone 下如果没先构建前端，`go build ./...` 或 `go test ./...` 可能因为 `web/embed.go` 找不到 `dist` 而失败。
- CI 也不是直接跑 Go 测试，而是先执行 `bun run lint`，接着构建 `web/dist`，再恢复产物后执行 `go vet ./...` 和多系统（Linux, macOS, Windows）的 `go test ./...`；本地排查时要有相同心智模型。
- 前端包管理器是 `bun`；不要擅自切换到 npm/yarn/pnpm。

## 发布与版本规则

- 发布必须通过 Git tag 触发 Release workflow；只推 `main` 或合并 PR 不会发布版本。
- NetsGo 使用发布通道管理自动升级；当前只定义 `stable` 与 `beta` 两个通道。
- Tag 是版本号和发布通道的唯一来源，必须带 `v` 前缀：
  - `stable`：`vMAJOR.MINOR.PATCH`，例如 `v0.1.0`。
  - `beta`：`vMAJOR.MINOR.PATCH-beta.N`，例如 `v0.1.0-beta.15`。
- `beta.N` 中的 `N` 必须是递增的正整数；同一 `MAJOR.MINOR.PATCH` 下按 `beta.1`、`beta.2`、`beta.3` 递增发布。
- 自动升级必须先按当前安装版本所属通道筛选候选版本，再比较 SemVer：
  - 当前为正式版时，只检查 `stable` 通道。
  - 当前为 beta 版时，检查 `stable` 与 `beta` 通道，并推荐 SemVer 最高者。
  - dev/snapshot/dirty 构建不代表真实发布轨道，调整其升级行为前必须先明确设计。

## 前端约束

- 前端路由使用 TanStack Router 的 Hash 模式，不是 BrowserRouter。
- 前端请求统一走 `web/src/lib/api.ts`；不要到处散写裸 `fetch`，除非是在极少数非常明确的底层场景。
- 服务端状态优先使用 TanStack Query；不要把服务端返回数据再复制成一套平行的客户端状态源。
- `web/src/components/ui/` 视为 shadcn/ui 源码层；只有确有必要时才改，新增业务组件放 `web/src/components/custom/`。
- 样式与组件写法优先遵守现有前端代码和组件约定。


## 后端与协议约束

- 不要新造一套与 `pkg/protocol/` 平行的消息结构。
- 不要随意修改认证、会话、在线状态语义；这类改动必须先读相关测试和现有状态流。
- 不要随意删除跨版本兼容路径，尤其是 legacy flat provision、旧数据恢复、storage projection、old server/current client、current server/old client 相关逻辑；移除前必须先有明确版本支持窗口、release note/deprecation 计划、迁移/回滚设计，并跑对应 compat/upgrade E2E。
- unified runtime 的语义来源必须是 `TunnelSpec` endpoint 字段；不得直接读取 `StoredTunnel.ProxyNewRequest` 的扁平字段来决定新运行态。legacy DTO 只能作为旧 wire/storage projection/兼容边界适配器存在，不能作为 unified runtime 的语义来源。
- 不要默认引入数据库、消息队列、分布式锁等多实例前提；当前项目不是按这些前提设计的。
- 涉及 `/ws/control`、`/ws/data`、TLS、反向代理、会话恢复的修改，必须考虑直连、nginx、caddy 三类路径。
- 管理数据和隧道配置默认会写入 `~/.netsgo/`；排查本地行为时要注意历史状态文件的影响。
- 可以做“链路层健康”与运行态健康管理，例如控制通道、数据通道、隧道运行态、重连/退避、runtime error 降级、会话在线性等；这些属于 NetsGo 自己应负责的健康语义。
- 不要默认实现“目标服务健康检查”。这里指 tunnel 背后真实业务服务的健康状态，例如 HTTP tunnel 背后的 HTTP 服务是否可用、TCP/UDP 目标端口是否真的健康。
- 作为穿透工具，默认不得擅自向用户的目标服务主动发起探测请求，不得把“client 收到配置”或“成功建立 tunnel”误当成“目标服务健康”。
- 如果未来要做目标服务健康能力，必须先有单独设计，明确：这是用户显式配置/授权的 probe，而不是系统默认行为；并且要区分链路层健康与目标服务健康，不能混成一个状态。

## 修改前建议先看哪里

- 改 CLI/命令参数：先看 `cmd/netsgo/`
- 改 API/认证/初始化：先看 `internal/server/admin_api.go`、`internal/server/auth_middleware.go`
- 改在线状态/Client 会话：先看 `internal/server/server.go`、`internal/client/client.go`
- 改协议字段：先看 `pkg/protocol/`
- 改数据通道/yamux/UDP：先看 `pkg/mux/`、`internal/server/data.go`、`internal/client/udp_handler.go`
- 改前端页面跳转/鉴权：先看 `web/src/lib/router.ts`、`web/src/lib/auth.ts`
- 改前端 API 调用：先看 `web/src/lib/api.ts` 和对应 hooks

## 验证规则

按改动类型选择最小但可信的验证：

- 只改 Go 局部逻辑：至少跑相关包测试。
- 改 server/client/protocol/认证/会话/通道逻辑：优先跑相关包测试；条件允许时跑 `go test ./...`。
- 改前端 TS/TSX：至少在 `web/` 下跑 `bun run build`；有必要时再跑 `bun run lint`。
- 改嵌入资源、构建链路、发布产物：至少跑 `make build`。
- 改数据通道、反代、连接恢复：优先考虑 `test/e2e/` 或 `Makefile` 里的 nginx/caddy/compose 相关验证命令。
- 如果无法完成验证，明确说明“没验证什么、为什么没验证、建议下一步怎么验证”。
