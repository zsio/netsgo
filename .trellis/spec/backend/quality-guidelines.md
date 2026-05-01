# Quality Guidelines

> Code quality standards for backend development.

---

## Overview

<!--
Document your project's quality standards here.

Questions to answer:
- What patterns are forbidden?
- What linting rules do you enforce?
- What are your testing requirements?
- What code review standards apply?
-->

(To be filled by the team)

---

## Forbidden Patterns

<!-- Patterns that should never be used and why -->

(To be filled by the team)

---

## Required Patterns

<!-- Patterns that must always be used -->

### Scenario: Client Service Address UX Contract

#### 1. Scope / Trigger

- Trigger: CLI, installer, Web, or docs changes that show users how to connect a NetsGo client.
- Applies to: `netsgo client --server`, `netsgo install` client prompts, Web "Add Client" connection command, README quick-start examples, and tests for those surfaces.

#### 2. Signatures

- CLI flag: `netsgo client --server <service-address>`
- Environment key: `NETSGO_SERVER=<service-address>`
- Go normalization API: `clientaddr.Normalize(raw string, mode clientaddr.Mode) (clientaddr.Address, error)`
- Web command resolver: produce `netsgo client --server <service-address> --key <raw-key>` from the effective server address when available.

#### 3. Contracts

- User-facing primary value is a service address: `http://host[:port]` or `https://host[:port]`.
- `ws://` and `wss://` remain accepted for compatibility, but are normalized to `http://` and `https://` base service addresses before persistence or display in primary command examples.
- Control/data WebSocket endpoints are derived internals:
  - `http://host` -> `ws://host/ws/control` and `ws://host/ws/data`
  - `https://host` -> `wss://host/ws/control` and `wss://host/ws/data`
- Web Add Client must prefer the effective configured service address over a stale persisted value when both are available.

#### 4. Validation & Error Matrix

- Empty value -> `service address cannot be empty`.
- Whitespace in value -> `service address cannot contain whitespace`.
- Managed install without a scheme -> reject; ask for `http://`, `https://`, `ws://`, or `wss://`.
- Unsupported scheme -> reject.
- User info, non-root path, query, or fragment -> reject.
- Invalid port -> reject.
- Legacy `ws(s)` input -> accept and normalize, but do not present as the first-use recommended form.

#### 5. Good/Base/Bad Cases

- Good: `netsgo client --server https://netsgo.example.com --key sk-...`
- Base: `netsgo client --server http://netsgo.zsio.dev:9527 --key sk-...`
- Bad: telling a first-time user to copy `wss://netsgo.example.com/ws/control` or manually convert `https` to `wss`.

#### 6. Tests Required

- Go tests for `internal/clientaddr` normalization and error wording.
- Go tests for install prompt summaries using the Chinese label `服务地址` and not exposing control/data endpoints as primary action rows.
- CLI help tests that prefer `http(s)` examples and only mention `ws(s)` as compatibility.
- Frontend tests for Web Add Client command generation, including `effective_server_addr` precedence and invalid legacy address fallback.

#### 7. Wrong vs Correct

##### Wrong

```text
Client install address: wss://netsgo.example.com
Run: netsgo client --server wss://netsgo.example.com --key sk-...
```

##### Correct

```text
Client install address: https://netsgo.example.com
Run: netsgo client --server https://netsgo.example.com --key sk-...
```

### Scenario: Interactive CLI Confirmation UX Contract

#### 1. Scope / Trigger

- Trigger: changes to interactive CLI commands, installers, service lifecycle managers, or any prompt that asks the user to confirm an action.
- Applies to: `netsgo install`, `netsgo manage`, service update/uninstall/cleanup flows, shared-binary removal, and future interactive commands built on `internal/tui`.

#### 2. Signatures

- Go typed-confirmation API: `tui.ConfirmWithOptions(prompt string, opts tui.ConfirmOptions) (bool, error)`
- Confirmation options: `tui.ConfirmOptions{ConfirmText: "<required phrase>"}`
- Cancellation sentinel/check: `tui.ErrCancelled`, `tui.IsCancelled(err) bool`
- Cobra wrapper for expected interactive cancellation: `runInteractiveCommand(run func() error) error`

#### 3. Contracts

- Confirmation prompts must use typed input, not left/right arrow selection plus Enter.
- Plain yes/no confirmation must require typed `yes`/`y` to continue or `no`/`n` to cancel.
- Confirmation prompt copy is Chinese by default in lifecycle CLI surfaces, but required typed phrases remain stable English tokens.
- High-impact actions must set a concrete `ConfirmText` phrase and must not accept plain `yes` as confirmation.
- Destructive phrases must describe the real impact:
  - uninstall server but keep data -> `uninstall server`
  - cleanup broken server but keep data -> `cleanup server`
  - remove server data -> `remove server data`
  - uninstall client -> `uninstall client`
  - remove shared binary -> `remove binary`
  - apply update -> `apply update`
  - replace the installed binary with the current executable -> `upgrade binary`
- Interactive user aborts are expected cancellations. `install` and `manage` must not dump Cobra usage for `tui.ErrCancelled`, `huh.ErrUserAborted`, or equivalent abort errors.
- Binary replacement flows must show a user-facing plan before confirmation: source binary, target binary, version transition, services to restart, and risk rows. Do not expose internal parser errors in the main plan.
- If an optional post-action prompt appears after irreversible work has already happened, its cancellation copy must describe the remaining choice, not imply a full rollback. Example: shared binary removal should say `type no to keep the shared binary`.

#### 4. Validation & Error Matrix

- Empty confirmation input -> validation error explaining the required input.
- Plain confirmation with anything except `y`, `yes`, `n`, or `no` -> `type yes or no`.
- Phrase confirmation with exact phrase (case-insensitive) -> continue.
- Phrase confirmation with `n`, `no`, or `cancel` -> cancel without changes.
- Phrase confirmation with `yes` -> validation error; require the exact phrase.
- Interactive abort/Ctrl-C -> print a concise cancelled summary and return success from the command wrapper.
- Non-cancellation error -> propagate as a normal command error.

#### 5. Good/Base/Bad Cases

- Good: `Proceed with server uninstall?` requires `remove server data` when server data will be removed.
- Base: `Proceed with installation?` requires typed `yes` or `no`.
- Bad: rendering `Yes  No` as a left/right toggle and accepting Enter for service uninstall.

#### 6. Tests Required

- Unit tests for confirmation parsing:
  - plain `yes`/`no`
  - required phrase accepts the phrase
  - required phrase rejects plain `yes`
  - required phrase accepts `no` as cancellation
- Caller tests must assert the required `ConfirmText` phrase for install, uninstall, cleanup, update, and shared-binary removal flows.
- Upgrade tests must assert the plan is user-facing: readable service list, no raw semver/parser errors in risk rows, and typed `upgrade binary` confirmation.
- Command tests must assert interactive cancellation prints a concise cancellation summary and does not print Cobra usage.
- Regression tests must cover keep-data cleanup separately from delete-data cleanup so the phrase matches the action.

#### 7. Wrong vs Correct

##### Wrong

```text
┃ Proceed with server uninstall?
┃
┃        Yes     No

←/→ toggle • enter submit
```

##### Correct

```text
┃ Proceed with server uninstall?
┃ Type "remove server data" to continue, or type no to cancel.
┃ >
```

### Scenario: Managed Service Lifecycle CLI Prompt UX Contract

#### 1. Scope / Trigger

- Trigger: changes to `netsgo install`, service initialization defaults, or interactive select menus used by lifecycle commands.
- Applies to: server/client role install, TLS mode selection, trusted proxy input, manage role/action/recovery menus, uninstall mode menus, and release download channel selection.

#### 2. Signatures

- Described select API: `tui.SelectWithOptions(prompt string, options []tui.SelectOption) (int, error)`
- Select option contract: `tui.SelectOption{Label: "<short Chinese label>", Description: "<one-sentence Chinese effect>"}`
- Compatibility API: `tui.Select(prompt string, options []string) (int, error)`
- Server init default used by managed install: `server.InitParams.AllowedPorts = "1024-65535"`
- Server env trusted proxy input: `Trusted proxy CIDRs`, default `0.0.0.0/0`

#### 3. Contracts

- Managed server install must not ask the user to choose allowed tunnel port ranges.
- Fresh managed server init must silently use allowed ports `1024-65535`.
- The server install summary must not show allowed tunnel ports; users configure/narrow this later in the Web console.
- Trusted proxy CIDRs default to `0.0.0.0/0`.
- Trusted proxy prompt copy must tell users that local Nginx/Caddy deployments should prefer `127.0.0.1/8`.
- Select-style menus must include concise descriptions for choices whose effect is not obvious from the label alone.
- `Select` remains for compatibility, but new lifecycle menus should use `SelectWithOptions` or local wrappers that fall back to labels for older fake UIs.
- Current pre-i18n lifecycle CLI copy is Chinese by default. Do not add language selectors, locale detection, or a full i18n framework until the product explicitly designs multilingual support.
- Lifecycle copy must speak from the user's intent, not from an inventory deficit model. A machine with only `server` or only `client` installed is a valid state.
- If exactly one role is already installed, `netsgo install` must summarize the current role and require typed yes/no confirmation before installing the other role on the same machine. It must not say the other role is "missing" or automatically continue into the other role's install flow.
- Single-role install copy must be concrete. Prefer `本机已安装 server。如果这台机器还需要作为 client 连接到另一个 NetsGo server，可以继续安装 client。` over abstract reassurance such as `只安装 server 是有效配置` or `同机运行`.
- Update guidance should point to conditional user choices, not list internal implementation steps. For example, standalone `netsgo update` can say `托管服务：运行 'netsgo manage'，选择“更新”` and `已有新版 netsgo 文件：执行新版文件的 'netsgo upgrade'`; it should not enumerate check/download/verify/apply/restart as if the user must perform those steps.
- Do not introduce client recovery as a normal install concept. After client removal, guide users to get a fresh client key and run `netsgo install` instead of saying there is no recoverable client identity.
- Preserve stable technical tokens in English where users copy/type them or where they are platform identifiers:
  - command names such as `netsgo install`, `netsgo manage`, `netsgo upgrade`
  - typed confirmation phrases such as `uninstall server`, `remove server data`, `upgrade binary`
  - systemd unit names, file paths, URLs, version strings, channel names, and protocol names such as `TLS`
- Avoid Go debug formatting in user-facing lifecycle summaries. Service lists should be comma-separated text, not `%v` slice output such as `[netsgo-server.service]`.

#### 4. Validation & Error Matrix

- Fresh server install -> no `Allowed port ranges` prompt, init uses `1024-65535`.
- Fresh server install summary -> no `Allowed ports` row.
- Trusted proxy prompt default -> `0.0.0.0/0`.
- Trusted proxy prompt description missing `127.0.0.1/8` local proxy guidance -> test failure.
- Described select menu option with empty description in requested lifecycle menus -> test failure.
- Legacy caller using `Select` -> still renders labels and returns the selected index.
- Lifecycle menus, summaries, advice rows, and validation errors drifting back to English-only copy -> test failure unless the text is a preserved technical token.
- Single-role install preflight describes the other role as `缺失`, `未安装`, or implied incomplete setup -> test failure.
- Single-role install preflight uses abstract copy such as `有效配置` or `同机运行` -> test failure.
- Single-role install preflight directly calls `InstallServer`/`InstallClient` without typed confirmation -> test failure.
- Standalone update guidance lists internal steps such as `检查、确认、下载、校验` -> test failure.
- Standalone update guidance presents alternatives as ordered steps instead of conditional cases -> test failure.
- Shared-binary prompt after service removal says `no` cancels the whole action -> test failure; it must say `no` keeps the binary.
- User-facing service list renders as a Go slice -> test failure.

#### 5. Good/Base/Bad Cases

- Good: `状态 - 查看服务是否已安装、运行中并设置为开机启动。`
- Base: server install uses `1024-65535` internally and leaves allowed port editing to the Web console.
- Bad: saying `将安装缺失的 client 角色` because `server` is installed on this machine.
- Bad: asking first-time users to choose `10000-11000` during install before they understand tunnel configuration.

#### 6. Tests Required

- Install tests assert `ApplyInit` receives `AllowedPorts: "1024-65535"` for fresh managed server install.
- Install tests assert the allowed-port input prompt is absent and the install summary omits allowed ports.
- Install tests assert trusted proxy default is `0.0.0.0/0` and prompt description mentions `127.0.0.1/8` plus local Nginx/Caddy.
- TUI tests cover select option formatting with and without descriptions.
- Manage/install/update tests assert lifecycle select menus call described options and every option has a non-empty description.
- Install tests assert the single-role preflight treats one installed role as valid, asks before installing the other role, and has a cancellation path that keeps the current role unchanged.
- Update command tests assert guidance stays concise and does not expose internal update steps as user work.
- Tests assert representative Chinese lifecycle text for install, manage, update, upgrade, cancellation, and validation paths.
- Tests assert typed confirmation phrases remain stable English tokens while surrounding prompt copy is Chinese.

#### 7. Wrong vs Correct

##### Wrong

```text
┃ Allowed port ranges
┃ Comma-separated list of port ranges or single ports
┃ > 10000-11000
```

##### Correct

```text
  安装摘要
  角色:                server
  安装模式:            fresh
  端口:                9527
  TLS 模式:            off
  服务地址:            http://netsgo.example.com:9527
  可信代理:            0.0.0.0/0
```

---

## Testing Requirements

<!-- What level of testing is expected -->

(To be filled by the team)

---

## Code Review Checklist

<!-- What reviewers should check -->

(To be filled by the team)
