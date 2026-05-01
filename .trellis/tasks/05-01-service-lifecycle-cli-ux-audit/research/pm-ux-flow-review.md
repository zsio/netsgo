# Research: PM UX flow review for lifecycle CLI

- Query: Remap the complete NetsGo lifecycle CLI user journey from a senior product/UX perspective for install, manage, update, upgrade, uninstall, reinstall, and recover flows. Focus on realistic hosts that usually install either server or client, not both.
- Scope: internal
- Date: 2026-05-01

## Findings

### Source Materials Read

- `.trellis/workflow.md` - Trellis workflow and research persistence rules.
- `.trellis/spec/backend/index.md` - backend spec index.
- `.trellis/spec/backend/quality-guidelines.md` - lifecycle CLI confirmation, prompt, Chinese copy, service-address, and test contracts.
- `.trellis/tasks/05-01-service-lifecycle-cli-ux-audit/prd.md` - active task requirements and product constraints.
- `.trellis/tasks/05-01-service-lifecycle-cli-ux-audit/research/lifecycle-walkthrough.md` - observed install/manage/update/upgrade/uninstall/reinstall walkthrough.
- `cmd/netsgo/cmd_install.go` - `netsgo install` command entrypoint.
- `cmd/netsgo/cmd_manage.go` - `netsgo manage` command entrypoint.
- `cmd/netsgo/cmd_update.go` - standalone update guidance.
- `cmd/netsgo/cmd_upgrade.go` - binary replacement plan and confirmation flow.
- `internal/install/install.go` - install preflight and role selection.
- `internal/install/server.go` - server install prompts, summary, recoverable-data path.
- `internal/install/client.go` - client install prompts, link verification, completion summary.
- `internal/install/service_flow.go` - shared install summaries and user-facing state labels.
- `internal/manage/manage.go` - manage entry routing by installed role state.
- `internal/manage/service_menu.go` - role-scoped action menu, status summaries, shared binary prompt.
- `internal/manage/server.go` - server inspect/uninstall/recover/cleanup flows.
- `internal/manage/client.go` - client inspect/uninstall/cleanup flows.
- `internal/manage/uninstall_all.go` - bulk uninstall flow.
- `internal/manage/update.go` - managed-service update flow.
- `internal/tui/tui.go` - select/input/password/typed-confirmation UI primitives and Chinese keymap.

### Related Specs

- `.trellis/spec/backend/quality-guidelines.md`:
  - `Client Service Address UX Contract`
  - `Interactive CLI Confirmation UX Contract`
  - `Managed Service Lifecycle CLI Prompt UX Contract`

### Code Patterns Observed

- `netsgo install` preflights both roles before showing a role picker, and already treats "server only" or "client only" as a valid state that requires explicit confirmation before installing the other role: `internal/install/install.go:106`, `internal/install/install.go:144`, `internal/install/install.go:158`, `internal/install/install.go:172`, `internal/install/install.go:206`.
- Fresh install role selection uses described options, which is the right information architecture pattern for first-time users: `internal/install/install.go:112`.
- Server install defaults are now closer to the target UX: port `9527`, trusted proxy CIDR `0.0.0.0/0`, loopback reverse-proxy guidance, and silent tunnel allowed-port default `1024-65535`: `internal/install/server.go:74`, `internal/install/server.go:100`, `internal/install/server.go:186`.
- Server recovery is understandable after role selection but not discoverable before role selection: historical data is handled inside `InstallServerWith`, after the generic install preflight has already returned false: `internal/install/install.go:193`, `internal/install/server.go:53`.
- Client install asks for user-intent inputs: service address and client key. It preserves technical tokens such as `http(s)://`, `ws(s)://`, and `client key`: `internal/install/client.go:75`, `internal/install/client.go:88`.
- Client completion contains a strong success signal, `NetsGo 链路`, after verifying the service can connect: `internal/install/client.go:155`, `internal/install/client.go:177`.
- Manage action menus now include role context and described options: `internal/manage/service_menu.go:31`, `internal/manage/service_menu.go:81`, `internal/manage/service_menu.go:92`.
- Status and inspect summaries are operationally useful but still mix product intent, technical inventory, and debugging detail in one surface: `internal/manage/service_menu.go:106`, `internal/manage/server.go:106`, `internal/manage/client.go:108`.
- Server uninstall shows a plan before typed confirmation and uses action-specific phrases such as `uninstall server` or `remove server data`: `internal/manage/server.go:133`, `internal/manage/server.go:153`.
- Client uninstall clearly states that reinstalling creates a new local identity and does not clean server-side history: `internal/manage/client.go:139`.
- Bulk uninstall gathers server and client confirmations before executing service removal, then asks separately about shared binary removal after service removal: `internal/manage/uninstall_all.go:26`, `internal/manage/uninstall_all.go:44`, `internal/manage/uninstall_all.go:61`, `internal/manage/uninstall_all.go:70`, `internal/manage/uninstall_all.go:91`.
- Standalone `netsgo update` is intentionally a guidance command and keeps next actions short: `cmd/netsgo/cmd_update.go:13`.
- Managed-service update for development builds gives richer guidance and points to `netsgo upgrade`, while release builds show channel choice, plan, typed `apply update`, and completion rows: `internal/manage/update.go:32`, `internal/manage/update.go:43`, `internal/manage/update.go:87`, `internal/manage/update.go:95`.
- `netsgo upgrade` now shows a replacement plan before typed `upgrade binary` confirmation, including source, target, version transition, services to restart, and risk rows: `cmd/netsgo/cmd_upgrade.go:94`, `cmd/netsgo/cmd_upgrade.go:103`, `cmd/netsgo/cmd_upgrade.go:140`.
- TUI primitives now support described select options, typed confirmations, custom cancel descriptions, cancellation normalization, and localized key help: `internal/tui/tui.go:33`, `internal/tui/tui.go:48`, `internal/tui/tui.go:146`, `internal/tui/tui.go:167`.

### Walkthrough Evidence

- The original P1 issue was confirmation safety: arrow/Enter `Yes No` prompts were ambiguous and unsafe for destructive lifecycle actions: `research/lifecycle-walkthrough.md:43`.
- The walkthrough found install/manage/update/upgrade/uninstall/reinstall paths for both roles and captured final cleanup state: `research/lifecycle-walkthrough.md:158`, `research/lifecycle-walkthrough.md:225`, `research/lifecycle-walkthrough.md:320`.
- Confirmed improvements already include no allowed-port prompt, trusted proxy default `0.0.0.0/0`, typed destructive phrases, role-context action menus, shared-binary wording, contextual cancel summaries, and client link success copy: `research/lifecycle-walkthrough.md:238`.
- Remaining observed problems are primarily product-copy and journey-framing issues: mixed English/raw labels, hidden recoverable state before role selection, logs that feel too stale/noisy, and automation brittleness around `delete server data`: `research/lifecycle-walkthrough.md:250`, `research/lifecycle-walkthrough.md:292`, `research/lifecycle-walkthrough.md:300`, `research/lifecycle-walkthrough.md:309`.

### External References

- No external product references were used. This review is based on the task PRD, observed walkthrough, local specs, and local command implementation.

## UX Principles For This CLI

1. **One host, one primary role is normal.** A machine running only `server` or only `client` must read as a complete, valid setup. The CLI should not frame the other role as missing, incomplete, or expected.

2. **Lead with the user's job, not the system inventory.** Users come to "set up this machine as the server", "connect this machine as a client", "restart the service", "replace the binary", or "remove it". Inventory rows are secondary context, not the main story.

3. **Every high-impact action needs a plan before a phrase.** Before uninstalling, deleting data, replacing binaries, or applying updates, show what will be changed, what will be kept, and what can be recovered. Then require a typed phrase that names the impact.

4. **Menus should answer "what happens if I choose this?" inline.** Every role/action/mode/channel option should have a one-sentence effect description. Users should not need `--help` to avoid choosing the wrong lifecycle action.

5. **Preserve technical tokens only where they are useful handles.** Keep `netsgo install`, `netsgo manage`, `netsgo upgrade`, unit names, file paths, URLs, version strings, `TLS`, `server`, `client`, and typed confirmation phrases. Translate ordinary labels, states, advice, and flow summaries.

6. **Use calm, short, next-action copy.** Avoid listing internal steps unless the user is looking at an execution plan. Prefer "运行 netsgo manage，选择 更新" over "检查、确认、下载、校验、应用并重启".

7. **Cancellation is a valid successful outcome.** A user who types `no` or presses Ctrl-C has not failed. The CLI should say no changes were made, or clearly say which optional branch was skipped after irreversible work already happened.

8. **Recovery should be visible before commitment.** If data can be recovered, show that state before a generic role picker, or label the role option as recoverable. Recovery is not an error; it is a user's likely reinstall path.

9. **Manage is an operating console, not a wizard.** `manage` should keep role context visible, return users to the right menu after actions, and avoid telling users to rerun a command they are already inside.

10. **Logs are an advanced affordance.** Logs are useful, but the menu should set expectations: recent journald output, full command available, and potential stale history.

## Recommended Top-Level Command Mental Model

Recommended mental model:

- `netsgo install`: "Set up this machine as a managed NetsGo server or client."
- `netsgo manage`: "Operate or remove managed NetsGo services already on this machine."
- `netsgo update`: "Show the right update path for my current situation."
- `netsgo upgrade`: "Use this downloaded `netsgo` executable to replace the installed managed-service binary."

Command responsibilities:

- `install` owns first setup, optional second-role install, and service recovery from saved server data.
- `manage` owns day-2 lifecycle operations: status, inspect, logs, start, stop, restart, update, uninstall, cleanup, and bulk removal.
- `update` should remain a low-pressure router, not a second updater.
- `upgrade` should be positioned as a local binary replacement tool, not "automatic update". It should always describe source and target binaries in user language.

Avoid a command model where:

- `install` means "finish a two-role inventory".
- `update` and `upgrade` feel interchangeable.
- `manage` starts with system inventory instead of the user's chosen role/action.
- `uninstall` is hidden as an implementation detail rather than a clear lifecycle endpoint.

## Ideal Flow Diagrams / Step Lists

### Fresh Server Install

Goal: user wants this host to become the NetsGo server.

1. Preflight: Linux, systemd, TTY, sudo.
2. Detect role state.
3. If no service exists:
   - Prompt: `选择这台机器的角色`
   - Option: `安装 server - 在这台机器运行 Web 控制台和公网隧道入口。`
   - Option: `安装 client - 把这台机器连接到已有 NetsGo server。`
4. Ask only setup inputs the user can reasonably answer now:
   - `监听端口`
   - `TLS 模式`
   - `可信代理 CIDR`
   - `Server 外部访问地址`
   - admin username/password
5. Do not ask tunnel allowed ports. Use `1024-65535` silently and defer narrowing to Web console.
6. Show install plan:
   - role, mode, port, TLS mode, service address, trusted proxies, service unit, data location, binary target.
7. Confirmation:
   - Prompt: `按以上配置安装 server？`
   - Description: `输入 yes 继续，或输入 no 取消。`
8. Completion:
   - status running, Web console URL, systemd unit, logs command, next step.

Ideal copy:

```text
  安装摘要
  角色:                 server
  安装方式:             全新安装
  Web 控制台:           http://netsgo.example.com:9527
  监听端口:             9527
  TLS:                  未启用
  可信代理:             0.0.0.0/0
  systemd 服务:         netsgo-server.service
  数据目录:             /var/lib/netsgo/server

按以上配置安装 server？
输入 yes 继续，或输入 no 取消。
```

### Fresh Client Install

Goal: user wants this host to connect to an existing server.

1. Preflight role state.
2. If no service exists, ask role with descriptions.
3. Ask:
   - `服务地址`
   - `客户端接入密钥`
   - TLS certificate trust only if needed.
4. Show plan without exposing the key:
   - role, service address, TLS status, service unit, data location.
5. Confirmation:
   - `按以上配置安装 client？`
6. Start service and observe link evidence.
7. Completion:
   - running status, service address, `NetsGo 链路`, logs command, next step.

Ideal copy:

```text
  安装摘要
  角色:                 client
  服务地址:             https://netsgo.example.com
  TLS:                  启用
  跳过 TLS 校验:        否
  systemd 服务:         netsgo-client.service

按以上配置安装 client？
输入 yes 继续，或输入 no 取消。
```

Completion copy:

```text
  Client 安装完成
  状态:                 运行中
  服务地址:             https://netsgo.example.com
  NetsGo 链路:          已建立
  日志:                 journalctl -u netsgo-client.service -n 100 -f
  下一步:               运行 netsgo manage 管理 client 服务
```

### Install When One Role Is Already Installed

Goal: avoid implying a one-role host is incomplete.

1. Detect one installed role and one installable role.
2. Show a validation summary, not an error.
3. Explain the second role is optional.
4. Ask explicit yes/no before continuing.

Ideal copy for server-only host:

```text
  本机已安装 server
  当前状态:             server 托管服务已安装
  说明:                 本机已安装 server。如果这台机器还需要作为 client 连接到另一个 NetsGo server，可以继续安装 client。
  下一步:               需要时继续安装 client；否则保持当前状态

是否也在本机安装 client？
输入 yes 继续，或输入 no 保持当前状态。
```

Mirror copy for client-only host:

```text
  本机已安装 client
  当前状态:             client 托管服务已安装
  说明:                 本机已安装 client。如果这台机器还需要作为 server 提供 Web 控制台和公网隧道入口，可以继续安装 server。
  下一步:               需要时继续安装 server；否则保持当前状态

是否也在本机安装 server？
输入 yes 继续，或输入 no 保持当前状态。
```

Cancellation copy:

```text
  已保持当前安装
  当前角色:             server
  状态:                 未进行任何修改
  下一步:               运行 netsgo manage 管理 server 服务
```

### Install When Both Roles Are Installed

Goal: stop immediately and route to operations.

Ideal copy:

```text
  托管服务已安装
  server:               已安装
  client:               已安装
  下一步:               运行 netsgo manage 管理已安装服务
```

Do not show a role picker.

### Manage

Goal: day-2 operations from user intent.

Entry behavior:

1. No roles installed:
   - summarize no managed services.
   - offer `运行 netsgo install` or `退出`.
2. One role installed:
   - show `正在管理 server` or `正在管理 client`.
   - enter role-scoped action menu directly.
3. Both roles installed:
   - role picker with `管理 server`, `管理 client`, `卸载全部托管服务`, `退出`.
4. Broken/recoverable state:
   - explain what is recoverable or needs cleanup.
   - offer inspect, install/recover, cleanup, back.

Role action menu:

```text
选择 server 操作
状态 - 查看 server 是否已安装、运行中并设置为开机启动。
检查 - 显示服务文件、数据路径、运行设置和检测到的问题。
日志 - 显示最近 100 行 journald 日志。
启动 - 启用并启动 systemd 服务。
停止 - 停止并禁用 systemd 服务。
重启 - 重启服务以重新加载当前配置。
更新 - 检查 release 更新，或查看本地替换指引。
卸载 - 查看卸载计划，并通过输入确认短语移除此服务。
返回 - 返回上一级菜单。
```

Status summary should be short:

```text
  Server 状态
  服务:                 netsgo-server.service
  状态:                 已安装
  运行中:               是
  开机启动:             是
  下一步:               选择检查、日志、重启、更新或卸载
```

Inspect can stay detailed, but label it as detailed:

```text
  Server 详细检查
  服务:                 netsgo-server.service
  二进制路径:           /usr/local/bin/netsgo
  数据目录:             /var/lib/netsgo/server
  Unit 文件:            /etc/systemd/system/netsgo-server.service
  Env 文件:             /etc/netsgo/server.env
  运行用户:             netsgo
  服务地址:             http://netsgo.example.com:9527
```

### Update / Upgrade

Goal: distinguish release update from local binary replacement.

`netsgo update` ideal behavior:

1. Does not mutate anything.
2. Tells the user which command to use.
3. Does not list internal update mechanics.

Ideal copy:

```text
托管服务：运行 'netsgo manage'，选择“更新”。
已有新版 netsgo 文件：执行新版文件的 'netsgo upgrade'。
手动下载：https://github.com/zsio/netsgo/releases
```

Managed update from `manage`:

1. If development build:
   - explain automatic release update is unavailable.
   - give local replacement path.
2. If release build:
   - choose channel.
   - check for update.
   - show plan only when an update exists.
   - require `apply update`.

Development-build copy:

```text
  更新
  当前版本:             ae06485-dirty
  状态:                 开发构建不支持自动 release 更新
  托管服务:             正式 release 可在 netsgo manage 中选择“更新”
  已有新版 netsgo 文件: 执行新版文件的 netsgo upgrade
  手动下载:             https://github.com/zsio/netsgo/releases
```

`netsgo upgrade`:

1. Detect installed managed services.
2. If current binary is already installed, say no replacement needed.
3. Show replacement plan:
   - source binary: current executable.
   - target binary: installed managed-service path.
   - version transition.
   - services to restart.
   - risk rows for development/unknown/downgrade.
4. Require `upgrade binary`.
5. Completion: binary replaced and services restarted.

Ideal copy:

```text
  替换计划
  源二进制:             /tmp/netsgo
  目标二进制:           /usr/local/bin/netsgo
  版本变化:             0.1.0 -> 0.2.0
  将重启服务:           netsgo-server.service, netsgo-client.service
  风险:                 目标二进制是开发构建（ae06485-dirty）

用当前二进制替换已安装版本？
输入 "upgrade binary" 继续，或输入 no 取消。
```

### Server Uninstall

Goal: make data retention vs deletion unmistakable.

1. Choose mode:
   - keep server data.
   - delete server data.
2. Show plan:
   - will stop/disable service.
   - remove unit/env.
   - keep or remove server data.
   - keep or optionally remove shared binary.
3. Require typed phrase:
   - keep data: `uninstall server`.
   - delete data: `remove server data`.
4. If no other role exists, ask binary removal as an optional post-action branch with "keep binary" wording.
5. Completion explains reinstall path.

Ideal copy:

```text
  Server 卸载计划
  模式:                 仅移除服务，保留数据
  将移除:               /etc/systemd/system/netsgo-server.service
  将移除:               /etc/netsgo/server.env
  将保留:               /var/lib/netsgo/server
  可选:                 卸载后可选择是否移除共享二进制 /usr/local/bin/netsgo

继续卸载 server？
输入 "uninstall server" 继续，或输入 no 取消。
```

Post-action binary prompt:

```text
未检测到其他托管角色。是否同时移除共享二进制 /usr/local/bin/netsgo？
输入 "remove binary" 移除，或输入 no 保留共享二进制。
```

### Client Uninstall

Goal: explain what will be removed without exposing internal identity concepts.

1. No mode picker unless future client data retention has product meaning.
2. Show plan:
   - remove service and local connection state.
   - reinstall requires a fresh `client key` from the Web console.
   - server-side historical records are not automatically cleaned.
3. Require `uninstall client`.

Ideal copy:

```text
  Client 卸载计划
  影响:                 移除托管 client 服务和本地连接状态
  结果:                 重新安装 client 时需要从 Web 控制台获取新的 client key
  结果:                 不会自动清理 server 端历史记录
  将移除:               /etc/systemd/system/netsgo-client.service
  将移除:               /etc/netsgo/client.env
  将移除:               /var/lib/netsgo/client

继续卸载 client？
输入 "uninstall client" 继续，或输入 no 取消。
```

### Bulk Uninstall

Goal: make it clear this removes all managed roles on this host.

1. Start with top-level summary:
   - installed roles.
   - shared binary.
   - server data choice.
   - client identity removal.
2. Gather all destructive choices before executing removals.
3. Ask role-specific confirmations.
4. Execute once all confirmations are complete.
5. Ask optional shared binary removal only if not already included in the final plan, or make it part of the pre-execution plan.

Recommended product direction: prefer "collect full plan first, execute once" over executing server/client removal before the shared-binary prompt. This makes "cancel" semantics simpler and aligns with user expectations.

### Reinstall / Recover

Goal: make recovery feel intentional, not broken.

Server keep-data reinstall:

1. Preflight detects historical server data.
2. Before role picker, show:
   - `检测到可恢复的 server 数据`
   - what will be reused.
   - that systemd service definition is missing.
3. Offer:
   - `恢复 server - 使用现有 server 数据重新创建托管服务。`
   - `安装 client - 连接到已有 NetsGo server。`
   - `退出`
4. Confirm before restoring service.

Ideal copy:

```text
  检测到可恢复的 server 数据
  状态:                 server 数据仍在，systemd 服务文件不存在
  将复用:               管理员、服务地址、已有隧道配置
  下一步:               恢复 server 会重新创建托管服务并保留现有数据

使用现有数据恢复 server？
输入 yes 继续，或输入 no 取消。
```

Client reinstall:

1. Treat as a fresh client install after local identity removal.
2. Explain that a new client key or server-issued access credential is needed.
3. Do not imply recovery unless product explicitly supports client identity recovery.

Ideal copy:

```text
  Client 需要重新接入
  状态:                 需要新的 client key
  下一步:               从 Web 控制台获取 client key，然后运行 netsgo install 安装 client
```

## Exact Recommended Chinese Copy Examples

### Command Help Short Descriptions

- `install`: `交互式安装 NetsGo server 或 client 托管服务`
- `manage`: `管理本机已安装的 NetsGo 托管服务`
- `update`: `显示适合当前情况的 NetsGo 更新入口`
- `upgrade`: `用当前 netsgo 可执行文件替换已安装的托管服务二进制`

### Role Picker

```text
选择这台机器的角色
安装 server - 在这台机器运行 Web 控制台和公网隧道入口。
安装 client - 把这台机器连接到已有 NetsGo server。
退出 - 不安装托管服务。
```

### Existing Single Role

```text
  本机已安装 server
  当前状态:             server 托管服务已安装
  说明:                 本机已安装 server。如果这台机器还需要作为 client 连接到另一个 NetsGo server，可以继续安装 client。
  下一步:               需要时继续安装 client；否则保持当前状态

是否也在本机安装 client？
输入 yes 继续，或输入 no 保持当前状态。
```

### Existing Both Roles

```text
  托管服务已安装
  server:               已安装
  client:               已安装
  下一步:               运行 netsgo manage 管理已安装服务
```

### Recoverable Server

```text
  检测到可恢复的 server 数据
  状态:                 server 数据仍在，systemd 服务文件不存在
  建议:                 继续安装会恢复托管服务，并保留现有配置
```

### Server Install Prompts

- `监听端口`
  - Description: `server 监听的 TCP 端口（1024-65535）。`
- `TLS 模式`
  - `off - 不启用 TLS；适合由反向代理终止 HTTPS 的部署。`
  - `auto - 生成自签名证书，供 client 首次信任使用。`
  - `custom - 使用已有证书和私钥文件。`
- `可信代理 CIDR`
  - Description: `逗号分隔的可信代理 CIDR；默认接受所有来源。若 NetsGo 位于本机 Nginx/Caddy 后方，建议使用 127.0.0.1/8。`
- `Server 外部访问地址`
  - Description: `client 访问此 server 的公网 URL（http:// 或 https://）。`

### Client Install Prompts

- `服务地址`
  - Description: `粘贴 Web 控制台或 server 安装摘要中的服务地址，通常为 http(s)://；兼容旧的 ws(s):// 输入并会自动规范化。`
- `客户端接入密钥`
  - Description: `从 Web 控制台的 Clients 页面获取 client key。`
- `跳过 TLS 证书校验？`
  - Description: `输入 yes 跳过校验，或输入 no 继续配置证书信任。`

### Manage Menus

```text
选择要管理的角色
管理 server - 检查、重启、更新或卸载 server 服务。
管理 client - 检查、重启、更新或卸载 client 服务。
卸载全部托管服务 - 通过一个引导流程移除本机所有托管角色。
退出 - 离开服务管理，不做任何修改。
```

```text
选择 server 操作
状态 - 查看 server 是否已安装、运行中并设置为开机启动。
检查 - 显示服务文件、数据路径、运行设置和检测到的问题。
日志 - 显示最近 100 行 journald 日志。
启动 - 启用并启动 systemd 服务。
停止 - 停止并禁用 systemd 服务。
重启 - 重启服务以重新加载当前配置。
更新 - 检查 release 更新，或查看本地替换指引。
卸载 - 查看卸载计划，并通过输入确认短语移除此服务。
返回 - 返回上一级菜单。
```

### Confirmation Descriptions

- Plain confirmation: `输入 yes 继续，或输入 no 取消。`
- Keep current single-role install: `输入 yes 继续，或输入 no 保持当前状态。`
- Server keep-data uninstall: `输入 "uninstall server" 继续，或输入 no 取消。`
- Server delete-data uninstall: `输入 "remove server data" 继续，或输入 no 取消。`
- Client uninstall: `输入 "uninstall client" 继续，或输入 no 取消。`
- Shared binary removal: `输入 "remove binary" 移除，或输入 no 保留共享二进制。`
- Release update: `输入 "apply update" 继续，或输入 no 取消。`
- Local binary replacement: `输入 "upgrade binary" 继续，或输入 no 取消。`

### Cancellation Summaries

Inside `install`:

```text
  安装已取消
  状态:                 未进行任何修改
  下一步:               需要时再次运行 netsgo install
```

Inside `manage`:

```text
  已取消
  状态:                 未进行任何修改
  下一步:               选择其他操作，或选择返回
```

Optional binary skip after uninstall:

```text
  已保留共享二进制
  路径:                 /usr/local/bin/netsgo
  下一步:               需要时可手动移除，或用于后续重新安装
```

### Completion Summaries

Server:

```text
  Server 安装完成
  状态:                 运行中
  Web 控制台:           http://netsgo.example.com:9527
  systemd 服务:         netsgo-server.service
  日志:                 journalctl -u netsgo-server.service -n 100 -f
  下一步:               打开 Web 控制台，或运行 netsgo manage 管理 server 服务
```

Client:

```text
  Client 安装完成
  状态:                 运行中
  服务地址:             https://netsgo.example.com
  NetsGo 链路:          已建立
  systemd 服务:         netsgo-client.service
  日志:                 journalctl -u netsgo-client.service -n 100 -f
  下一步:               运行 netsgo manage 管理 client 服务
```

Upgrade:

```text
  替换完成
  已停止:               netsgo-server.service, netsgo-client.service
  已启动:               netsgo-server.service, netsgo-client.service
  下一步:               运行 netsgo manage 查看服务状态
```

## Anti-Patterns To Avoid

- Calling the uninstalled role `missing`, `缺失`, or `未完成` when the host intentionally has only one role.
- Asking users to choose an installed role and then telling them it is already installed.
- Showing a generic `选择操作` after users selected `server` or `client`.
- Using English labels like `Service`, `Logs`, `Status`, `installed`, `not-installed`, or `Upgrade plan` in otherwise Chinese lifecycle flows, unless the term is a preserved technical token.
- Presenting internal update mechanics as user chores, such as "check, download, verify, apply, restart" outside an explicit plan.
- Accepting Enter on ambiguous destructive confirmation widgets.
- Accepting plain `yes` for destructive actions that delete data, remove services, or replace binaries.
- Saying `no` cancels an action after irreversible service removal has already happened; say it keeps or skips the remaining optional step.
- Dumping Cobra usage for expected Ctrl-C or typed `no` cancellations.
- Showing raw Go errors or parser errors in primary plans and summaries.
- Hiding recoverable server data until after a generic role picker.
- Overloading `检查` with too much diagnostic detail without making it clear this is a detailed/advanced view.
- Showing stale journald history without setting scope expectations.
- Asking first-time server users to choose tunnel allowed ports during install.
- Exposing or echoing client keys in summaries.
- Introducing a language selector or full i18n architecture for this pre-i18n task.

## Prioritized Implementation Recommendations

### P0 - Preserve Already-Correct Safety Contracts

1. Keep typed confirmations as the only confirmation pattern for lifecycle commands.
2. Keep destructive confirmation phrases stable and English:
   - `uninstall server`
   - `remove server data`
   - `cleanup server`
   - `uninstall client`
   - `cleanup client`
   - `remove binary`
   - `apply update`
   - `upgrade binary`
3. Keep all high-impact operations behind a printed plan before confirmation.
4. Keep Ctrl-C and user aborts as normal cancellations, not Cobra usage errors.

### P1 - Fix Journey Framing And Copy Consistency

1. Sweep lifecycle summaries for ordinary English labels/raw states and translate them to Chinese while preserving useful technical tokens.
2. Replace inventory-style copy with intent-style copy:
   - "本机已安装 server" instead of "client missing".
   - "本机已安装 server。如果这台机器还需要作为 client 连接到另一个 NetsGo server，可以继续安装 client。"
3. Add a short one-role entry summary in `manage` before entering the role action menu:
   - `正在管理 server`
   - `这台机器当前只运行 server，需要时可以再安装 client。`
4. Rename status/inspect summary titles by role:
   - `Server 状态`
   - `Client 状态`
   - `Server 详细检查`
   - `Client 详细检查`
5. Ensure every next-step row is context-aware:
   - inside `manage`: `选择其他操作，或选择返回`
   - outside `manage`: `运行 netsgo manage 管理服务`

### P1 - Improve Recover / Reinstall Discoverability

1. Detect recoverable server data before the generic role picker.
2. Show recoverable server state as a first-class path:
   - `恢复 server - 使用现有 server 数据重新创建托管服务。`
3. For client historical data, avoid "recover" language unless client identity recovery is actually supported. Tell the user to get a new `client key`.

### P2 - Reduce Cognitive Load In Manage

1. Split `状态` and `检查` intent:
   - `状态` should be short and operational.
   - `检查` can be detailed and diagnostic.
2. Make log scope explicit:
   - menu option: `日志 - 显示最近 100 行 journald 日志。`
   - summary: include full `journalctl` command for advanced users.
3. Consider adding a simple post-action return rhythm:
   - after status/inspect/update/start/stop/restart, keep users in the same role menu.
   - after uninstall, return to role selection or no-service menu depending on remaining roles.

### P2 - Tighten Update / Upgrade Language

1. Keep `update` as guidance and `upgrade` as local binary replacement.
2. In managed update, avoid ambiguous source-binary wording. Say `用当前运行的 netsgo 文件替换 /usr/local/bin/netsgo`.
3. In upgrade completion, add `下一步: 运行 netsgo manage 查看服务状态`.
4. Keep risk rows user-facing:
   - good: `无法确定已安装版本；无法完成版本安全检查`
   - bad: raw semver/parser error.

### P3 - E2E/Test Harness Follow-Up

1. Use `remove server data` for humans and automation; it stays explicit about destructive server data removal without colliding with `tmux send-keys` key names.
2. Do not weaken human confirmation safety for automation.

## Caveats / Not Found

- `task.py current --source` reported no active task in this session. The user supplied the exact task directory, so this artifact was written only under `.trellis/tasks/05-01-service-lifecycle-cli-ux-audit/research/`.
- No E2E was run for this review, per request.
- No product code was modified.
- This review did not inspect every test file line-by-line; it focused on PRD, walkthrough, specs, command entrypoints, and lifecycle implementation surfaces needed for product/UX mapping.
- No external references were used.
