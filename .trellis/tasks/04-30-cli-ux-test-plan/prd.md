# brainstorm: CLI 操作路径与测试策略

## Goal

梳理 NetsGo 当前 CLI 的用户操作路径，找出需要重点体验测试的路径与风险点，并形成后续 CLI UX 改造可复用的测试策略。

## What I already know

* 当前分支目标是优化 CLI 使用体验。
* 用户已准备好真实测试环境。
* CLI 基于 Cobra，根命令为 `netsgo`，当前可见业务命令包括 `server`、`client`、`install`、`manage`、`update`、`upgrade`。
* `install` / `manage` 是 Linux + systemd + interactive TTY 场景；会在非 root 时通过 sudo 重新执行。
* `server` / `client` 是直跑、开发调试、容器路径；长期 Linux 服务建议走 `install` + `manage`。
* `client` 运行时支持 `ws/wss/http/https`；managed install 也接受四种 scheme，但要求显式 scheme，拒绝裸 host、path、query、fragment、userinfo。
* 已有较多单元测试覆盖 install/manage 的 fake UI 分支、systemd 参数、update/upgrade 安全分支、client address normalization、server init flag 校验。
* 真实 TTY 的端到端体验仍需要在 Linux 测试机上用脚本化手工清单验证。
* 当前 server 安装成功摘要显示的是 `Panel URL`，即 `http(s)` 管理面地址。
* Web 的“添加 Client”弹窗生成的连接命令当前使用 `netsgo client --server <http(s) server_addr> --key <key>`，方向正确。
* README 仍提示 client service 安装时应使用 `ws://` 或 `wss://`，这与代码和 Web 默认命令不一致，会让首次使用者困惑。
* Client 安装摘要会展示 Control/Data WebSocket endpoint，虽然有诊断价值，但会把内部派生端点暴露到首次安装心智模型里。

## Assumptions (temporary)

* 测试重点优先覆盖“新用户从下载二进制到 server/client 服务跑起来”的黄金路径。
* 真实 Linux 测试机可用于破坏性 service lifecycle 测试，测试后默认清理现场。
* 本任务先做一批小范围 CLI UX 修复：统一用户可见连接地址口径，再补对应测试/文档。

## Requirements (evolving)

* 梳理 CLI 路径时按用户意图分组，而不是按源码文件分组。
* 测试策略需要同时覆盖：
  * 命令帮助与文案一致性。
  * 直跑 server/client。
  * Linux managed install/manage。
  * update/upgrade 入口差异。
  * 错误路径、取消路径、重复执行、残留状态恢复。
  * server 安装成功、Web 创建 client key、client install 输入提示之间的服务地址协议一致性。
* 对每条路径明确验证目标、关键命令、观察点和通过标准。
* 测试优先级应先覆盖会影响初次使用信心的路径。
* 用户可见的默认连接地址应统一为 `http(s)` 服务地址；`ws(s)` 控制/数据通道地址只作为系统派生细节或高级诊断，不要求用户选择或转换。
* Web 创建 client key 后给出的复制命令、README 快速开始、`netsgo client --help` 示例、`netsgo install` 的 Client 输入提示应使用同一种默认表达。
* 避免在普通用户文案里突出 `server_addr` 这类内部字段名；需要展示时用“服务地址”或“连接地址”。

## Acceptance Criteria (evolving)

* [ ] 有一份 CLI 操作路径地图。
* [ ] 有一份分层测试策略，区分本地快速验证、Go 单测、Docker E2E、真实 Linux TTY E2E。
* [ ] 有一份建议的首轮测试清单，可直接在现有测试环境执行。
* [ ] 明确指出当前已观察到的潜在 UX 风险点。
* [ ] 验证 server 安装完成提示、Web 添加 Client 弹窗、README 快速开始、client install 输入提示中的协议口径一致。

## Definition of Done

* 测试策略能指导下一步实际执行，不依赖口头记忆。
* 如果后续进入实现，相关 spec/context 会写入 `implement.jsonl` 与 `check.jsonl`。
* 修改代码后必须按影响范围运行对应 Go 测试、前端构建或真实 Linux E2E。

## Technical Approach

* 用户可见的默认连接地址统一使用 `http://` 或 `https://` 服务地址。
* `ws://` / `wss://` 只作为内部派生的 control/data endpoint，保留在诊断或 inspect 类信息里，避免出现在新用户必须理解的主路径。
* 调整 README、`netsgo client --help`、`netsgo install` 的 Client 输入说明、Web 添加 Client 弹窗文案，使它们都表达同一个默认路径：复制 Web 面板/server 安装完成显示的服务地址即可连接。
* 保留代码已有的 http(s) 到 ws(s) 自动转换能力，不引入新的并行地址结构。
* 补充/更新测试，覆盖 http(s) 输入被接受、摘要/命令使用 http(s) 服务地址、误导性的 ws(s) 主路径文案被移除。

## Out of Scope

* 本轮不设计目标服务健康检查。
* 本轮不默认改 Web UI。
* 本轮不引入数据库、队列、多实例控制面等新前提。
* 本轮不把 Linux service 生命周期测试迁移到 macOS 本地模拟。

## Technical Notes

* CLI 入口：`cmd/netsgo/main.go`。
* 直跑 server：`cmd/netsgo/cmd_server.go`。
* 直跑 client：`cmd/netsgo/cmd_client.go`。
* managed install：`cmd/netsgo/cmd_install.go`、`internal/install/`。
* managed service management：`cmd/netsgo/cmd_manage.go`、`internal/manage/`。
* update/upgrade：`cmd/netsgo/cmd_update.go`、`cmd/netsgo/cmd_upgrade.go`、`pkg/updater/`。
* service layout/systemd/env inspection：`internal/svcmgr/`。
* client server address normalization：`internal/clientaddr/address.go`。
* Linux 测试机说明：`docs/linux-e2e-tmux.md`。
* CI 真实标准：`.github/workflows/ci.yml` 先构建 web dist，再跑 lint/govulncheck/vet/test/race/e2e/cross-build。
