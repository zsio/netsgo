# Service Lifecycle CLI UX Walkthrough

Date: 2026-05-01
Environment: Debian 13 x86_64 E2E host through tmux session `netsgo-e2e-session`
Binary under test: `ae06485`

## Scope Covered

* Captured existing managed service baseline.
* Used current source-built Linux binary to manage an existing install.
* Uninstalled both server and client through `netsgo manage`.
* Reinstalled server through the `No services installed -> Run netsgo install` path.
* Created a client key through the admin API for test setup.
* Reinstalled client through `netsgo install`.
* Verified `netsgo manage` status/inspect/update paths for server and client.
* Ran standalone `netsgo update`.
* Captured final service state.

## Baseline

* Existing install was running both roles:
  * `netsgo-server.service`: active, enabled
  * `netsgo-client.service`: active, enabled
* Installed binary was version `0.1.0`.
* Test binary was version `ae06485`.

## Walkthrough Result

* Bulk uninstall completed successfully after explicit `y` confirmation for each step.
* Reinstalling server completed successfully:
  * Panel URL: `http://netsgo.zsio.dev:9527`
  * service active and enabled
* Reinstalling client completed successfully:
  * Service address: `http://netsgo.zsio.dev:9527`
  * install summary reported `NetsGo link: Established`
* Final state:
  * installed binary: `ae06485`
  * server active/enabled
  * client active/enabled

## Confirmed UX Findings

### P1: confirmation prompts need explicit typed input

Observed prompts render as a left/right `Yes No` selector with Enter submit:

* Proceed with installation?
* Include server uninstall in the bulk removal?
* Include client uninstall in the bulk removal?
* Remove shared binary?

The active choice was not obvious in tmux capture, and pressing Enter once cancelled unexpectedly. The user explicitly stated this should not use arrow keys and Enter. For destructive or high-impact actions, require typed confirmation/cancellation instead.

Suggested direction:

* Non-destructive confirmation: require `yes` / `no` or `y` / `n`.
* Destructive confirmation: require typing a concrete word or phrase, e.g. `delete data`, `uninstall`, or the service name.
* Keep summaries before the prompt; they are useful.

### P1: updated build still has remaining lifecycle UX gaps

After rebuilding the current working tree as `ae06485-dirty` and testing through `netsgo-e2e-session`, the typed confirmation change is directionally correct, but several flow-level issues remain:

* `manage -> server -> update` now has richer development-build guidance, but `Local replace: Run netsgo upgrade to replace the installed managed-service binary with this executable` is too long and uses unclear wording. Users may not know which executable "this executable" refers to.
* `manage -> server -> uninstall` shows a mode prompt titled only `Uninstall mode`; because both server and client can be managed, the prompt should include the role, e.g. `Server uninstall mode`.
* Cancelling a server uninstall from inside `manage` prints `Next step: Run netsgo manage to continue managing services` even though the user is already inside the manage menu. It should say something contextual like `Select another action, or choose Back`.
* `netsgo install` still asks `Select installation role` even when both server and client are already installed. Selecting Server then exits with `Server already installed`. The command should first detect installed roles and summarize the current state instead of asking the user to choose an already-installed role.
* `netsgo upgrade` still uses a separate raw prompt style: `Continue? [y/N]:`. It is input-based, but inconsistent with the new TUI summaries/typed confirmation contract and does not show a plan before confirmation.
* After the second implementation pass, `netsgo upgrade` shows a plan and typed confirmation. A final polish pass removed internal semver parse errors such as `parse installed version: no semver found...` from the plan and formats services as comma-separated names instead of Go slice output.

Suggested direction:

* Preflight `install` should summarize installed roles and route users to `manage`; if only one role is missing, prioritize installing the missing role.
* Role-specific destructive prompts should include the role in prompt titles.
* Cancellation summaries should be context-aware.
* `upgrade` should show an upgrade plan with current path, installed path, versions, and services to restart before any risky confirmation. Development build, unknown installed version, and downgrade confirmations should require an explicit phrase such as `upgrade binary`.
* Upgrade risk rows should be user-facing. Do not expose raw parsing errors unless the user asks for debug detail.

### P2: aborting a nested manage prompt prints scary Cobra usage

Pressing Ctrl-C inside an uninstall-mode selection printed:

* `Error: user aborted`
* full `netsgo manage` usage/help

This is technically accurate but noisy for an intentional user abort. It makes a safe cancellation feel like a command failure.

Suggested direction:

* Treat `user aborted` as a normal cancellation for interactive flows.
* Print a concise summary such as `Cancelled. No changes were made.`
* Avoid dumping Cobra usage for expected interactive cancellation.

### P2: install emits a confusing initialization warning during install

During server install, output included:

* `Service not yet initialized; please use the install or init command to complete initialization`
* then immediately `Service initialization complete`

Because the user is already inside `netsgo install`, the first line reads as contradictory.

Suggested direction:

* Suppress this warning in the install path, or rephrase it as internal progress: `Initializing server data...`.

### P2: development-build update guidance is incomplete

`manage -> Update` on a development build shows:

* Version: `ae06485`
* Status: `Development build - automatic update not supported`

Standalone `netsgo update` gives better guidance:

* managed services: use `netsgo manage` Update
* local binary replacement: use `netsgo upgrade`
* manual release URL

Suggested direction:

* Reuse or link the richer standalone guidance in the manage update path.
* For development builds, explain whether `netsgo upgrade` can still replace the managed binary with the current executable.

### P3: direct IP plus Host header was brittle in ad hoc API setup

Using Python `urllib` with URL `http://127.0.0.1:9527` plus a custom `Host` header resulted in API 404. Using the fixed management domain `http://netsgo.zsio.dev:9527` worked.

This is not necessarily a product bug, but it reinforces the E2E doc guidance to use the fixed domains for management-address flows.

### P3: `jq` is assumed by existing helper scripts but missing on the test host

The E2E host did not have `jq`, while `test/e2e/scripts/bootstrap.sh` uses it. Manual setup had to fall back to Python.

This is a test-environment/tooling issue, not a CLI UX bug.

## Positive UX Notes

* Install summaries are useful and concise.
* Server install defaults are reasonable for the documented E2E path:
  * port `9527`
  * TLS `off`
  * trusted proxy CIDR `127.0.0.1/8`
* `Client installation complete` with `NetsGo link: Established` is a strong completion signal.
* `manage inspect` gives enough operational detail to debug service paths, env files, state paths, and identity state.
* Standalone `netsgo update` guidance is clear.
* The rebuilt typed confirmation prompt correctly rejects empty Enter and accepts `no` as cancellation without performing destructive work.
* Ctrl-C in the rebuilt `manage` flow no longer dumps Cobra usage.
* `netsgo install` now detects when both managed roles are already installed and shows a direct status summary instead of asking the user to choose a role.
* `netsgo upgrade` now shows a readable plan:
  * source binary
  * target binary
  * version transition
  * comma-separated services to restart
  * user-facing risk rows
  * typed `upgrade binary` confirmation

## Clean-Room Walkthrough on 2026-05-01

Binary under test: `ae06485-dirty` built from the current working tree as `/tmp/netsgo-e2e/netsgo-clean-flow`, uploaded to the E2E host as `/tmp/netsgo-clean-flow`.

Starting state was forcibly cleaned:

* disabled/stopped `netsgo-server.service` and `netsgo-client.service`
* removed `/etc/systemd/system/netsgo-*.service`
* removed `/usr/local/bin/netsgo`, `/etc/netsgo`, `/var/lib/netsgo`, and temporary key helper files
* verified both units were not found and managed paths were absent

Walked each interactive step through tmux session `netsgo-e2e-session` with 2-second pauses/captures between actions:

* fresh server install: selected Server, accepted port `9527`, TLS `off`, trusted proxy `127.0.0.1/8`, service address `http://netsgo.zsio.dev:9527`, admin `admin`, password `NetsgoE2E-2026!`, allowed ports `10000-11000`, confirmed with typed `yes`
* server manage with only server installed: status, inspect, update, stop, start, restart
* server uninstall cancel: selected keep-data uninstall, observed plan, typed `no`, confirmed no changes and returned to action menu
* server uninstall real path: selected keep-data uninstall, typed `uninstall server`, skipped shared binary removal with `no`, verified unit was gone and `/var/lib/netsgo/server/netsgo.db` still existed
* server reinstall from recoverable data: selected Server, accepted the recoverable-data summary, confirmed with typed `yes`, service restored and running
* client key setup: created an API key through the admin API using `http://netsgo.zsio.dev:9527`; `jq` was unavailable, so Python was used
* client install: `install` detected installed server + missing client, continued directly to client setup, accepted service address, pasted key, confirmed with typed `yes`, link reported `Established`
* dual-role manage: observed role selector, managed server status/inspect/update, managed client status/inspect/update/stop/start/restart
* client uninstall cancel: observed plan and typed `no`
* client uninstall real path: typed `uninstall client`, then reinstalled client through the missing-role install path and got `NetsGo link: Established`
* both-installed install preflight: `install` reported both server and client installed and recommended `netsgo manage`
* standalone update: `update` showed managed-service guidance plus `upgrade` and manual release options
* standalone upgrade cancel: `upgrade` showed source/target/version/services/risk plan and accepted typed `no`
* bulk uninstall: selected `Uninstall all managed services`, confirmed server keep-data with `uninstall server`, confirmed client with `uninstall client`, confirmed shared binary removal with `remove binary`
* final cleanup: manually removed `/etc/netsgo`, `/var/lib/netsgo`, and temporary key files after the keep-data bulk path; verified both units were not found and managed paths were absent

Additional clean-room findings:

### P1: manage action menus lose role context

When only one role is installed, `netsgo manage` enters `Select an action` directly. When both roles are installed, after choosing `Manage server` or `Manage client`, it also enters a generic `Select an action` menu. The summaries printed after choosing Status/Inspect include the role, but the active menu itself does not say whether the user is managing server or client.

Suggested direction:

* Title the action menu with the role, e.g. `Select a server action` / `Select a client action`.
* When exactly one role is installed, show a short entry summary such as `Managing server` and mention the other role is not installed.

### P1: shared binary prompt says "cancel" after irreversible work has already happened

After confirming server uninstall, the service files were removed before the shared binary prompt appeared. The prompt says `Type "remove binary" to continue, or type no to cancel`; at that point `no` only skips binary deletion, it does not cancel the service uninstall.

Suggested direction:

* Reword the second branch as `type no to keep the binary`.
* Consider asking all destructive choices before executing any removal, then print a final all-in plan.

### P2: recoverable-data install is clear after role selection, but hidden before it

After uninstalling the server while keeping data, running `netsgo install` first asked `Select installation role`. Only after selecting Server did it reveal `Recoverable server data detected`.

Suggested direction:

* If historical server data exists and no server service exists, show the recoverable state before the role picker, or label the Server option as recoverable.

### P2: bulk uninstall delete-data phrase is hard to drive through tmux automation

The required phrase `delete server data` is good for a human, but `tmux send-keys` treats `delete` as a key name unless driven very carefully. This is an automation harness issue rather than a product issue. The keep-data bulk path was completed and final data deletion was done manually.

## Follow-Up Chinese Walkthrough on 2026-05-01

Binary under test: `ae06485-dirty`, uploaded to the E2E host as `/tmp/netsgo-cn-ux`.

This pass continued the live tmux session from a clean host, then finished with a full manual cleanup. Each interactive action was followed by an approximately 2-second pause and pane capture.

Flow covered:

* fresh server install from no services installed
* server manage: status, inspect, logs, stop, start, restart, update
* server uninstall cancel, then keep-data uninstall, then recoverable-data reinstall
* API key creation through the Web/API server for client install setup
* client install through the missing-role preflight path
* dual-role manage role selector and client status, inspect, update, stop, start, restart
* all-installed `netsgo install` preflight
* standalone `netsgo update`
* standalone `netsgo upgrade` plan and typed cancel
* attempted bulk delete-data uninstall, then final forced cleanup of units, binary, config, data, and temp key files

Confirmed improvements:

* Server install no longer asks for an allowed port range. The install summary also does not mention the range.
* Server install defaults trusted proxies to `0.0.0.0/0`, with Chinese guidance recommending `127.0.0.1/8` behind local Nginx/Caddy.
* Destructive confirmations now require typed phrases such as `uninstall server`, `remove binary`, and `upgrade binary`; `no` cancels or skips the specific optional step.
* Server action menu titles now include role context: `选择 server 操作`; client uses `选择 client 操作`.
* Shared binary prompt now says `no` keeps the binary, instead of implying it cancels already-completed service removal.
* Cancelling server uninstall from inside `manage` now says no changes were made and returns users to the current menu context.
* Client install reports `NetsGo 链路: 已建立` after the service connects.

Remaining UX findings from this pass:

### P1: Chinese mode still has mixed English labels and raw state values

Examples observed:

* completion summaries still show `Service:` and `Logs:`
* status and inspect still show `状态: installed`
* installed-role preflight still shows `Server: installed` and `Client: installed`
* client install summary still uses `TLS:`, and inspect uses `Server URL:`
* standalone upgrade prints `Upgrade 计划` and `Upgrade 已取消，未进行任何修改。`

Suggested direction:

* Keep stable technical tokens where useful, but translate ordinary UI labels and user-facing state values.
* Map raw states like `installed` / `not-installed` to Chinese in interactive output.

### P1: TUI footer help remains English

Every Bubble Tea/huh menu still ends with:

* `up`
* `down`
* `filter`
* `enter submit`

This is visible directly under Chinese menu titles and makes the interface feel half-localized.

Suggested direction:

* Configure or wrap the TUI component help text if the library supports localization.
* If not configurable, consider replacing these menus with a local wrapper for lifecycle CLI prompts.

### P2: recoverable-data path exposes internal English diagnostics

Recoverable server data was detected correctly, but the summary included:

* `问题: Recoverable server historical data was detected, but the managed service definition is missing`

Suggested direction:

* Convert recoverable-state diagnostics to Chinese and make them actionable.
* Example intent: `检测到历史 server 数据，但 systemd 服务文件不存在。继续安装会恢复服务。`

### P2: recoverable-data state is still hidden until after role selection

After keep-data server uninstall, `netsgo install` first showed the generic role picker. Only after choosing Server did it reveal that server data was recoverable.

Suggested direction:

* Show recoverable state before the role picker, or annotate the Server option as recoverable.

### P2: logs action can overwhelm the user with stale history

`manage -> server -> 日志` showed old logs from previous test runs, including old client connect/disconnect events before the current reinstall. This is technically correct journald behavior, but noisy as a lifecycle UX.

Suggested direction:

* For the menu action, consider showing recent logs since the current service start, or make the action description explicit: `显示最近 100 行 journald 日志`.
* Keep the full `journalctl -u ...` command in summaries for advanced users.

### P3: delete-data phrase is still awkward for tmux automation

The bulk server delete-data phrase `delete server data` could not be entered reliably through the tmux automation path in this pass: attempts resulted in only `server data` reaching the prompt. Entering the word as separate tmux keys had the same result.

This is likely an automation harness issue, but it makes scripted E2E coverage for this exact destructive phrase brittle.

Suggested direction:

* Keep typed destructive confirmation for humans.
* Consider a test-only non-interactive flag or a confirmation phrase that does not collide with tmux key names, if we want stable automated coverage of delete-data flows.

## State Left Behind

The E2E host was cleaned after the final walkthrough.

* `netsgo-server.service`: unit not found
* `netsgo-client.service`: unit not found
* `/usr/local/bin/netsgo`, `/etc/netsgo`, `/var/lib/netsgo`: absent
* temporary client key/helper files: removed

## Follow-Up Update/Upgrade Copy Walkthrough on 2026-05-01

Binary under test: `ae06485-ux-e2e`, built from the current working tree and uploaded as `/tmp/netsgo-ux-e2e`.

This pass used the required tmux SSH session `netsgo-e2e-session`, started from a fully cleaned host, and paused about 2 seconds between interactive steps for observation.

Flow covered:

* full cleanup before starting: stopped units, removed unit files, `/usr/local/bin/netsgo`, `/etc/netsgo`, `/var/lib/netsgo`, and temporary key files
* fresh server install: selected `安装 server`, accepted port `9527`, TLS `off`, trusted proxy default `0.0.0.0/0`, service address `http://netsgo.zsio.dev:9527`, admin user/password, typed `yes`
* server manage: status, inspect, update, stop, start, restart
* API key creation through `/api/auth/login` and `/api/admin/keys`
* client install through the single-role preflight path, typed `yes`, service address, `client key`, typed `yes`
* all-installed `netsgo install` preflight
* standalone `netsgo update`
* standalone `netsgo upgrade` plan and typed `no` cancel
* dual-role manage role selector and client status/update
* client uninstall with typed `uninstall client`
* client reinstall after obtaining a new `client key`
* bulk uninstall keep-data path with typed `uninstall server`, typed `uninstall client`, and typed `remove binary`
* final forced cleanup and verification of units, binary, config, data, and temporary files

Confirmed improvements:

* Server install did not ask for allowed tunnel port ranges, and the install summary did not disclose the internal `1024-65535` default.
* Trusted proxy default was `0.0.0.0/0`; prompt copy included the local Nginx/Caddy recommendation `127.0.0.1/8`.
* Single-role install copy no longer says `缺失`, `未安装 client`, `有效配置`, or `同机运行`. It explains the concrete user intent for installing the other role.
* `manage` action menus are role-specific: `选择 server 操作` and `选择 client 操作`.
* `manage -> 更新` for development builds now presents alternatives as cases: `托管服务`, `已有新版 netsgo 文件`, `手动下载`. It no longer lists internal update chores.
* Standalone `netsgo update` now prints three alternatives:
  * `托管服务：运行 'netsgo manage'，选择“更新”`
  * `已有新版 netsgo 文件：执行新版文件的 'netsgo upgrade'`
  * `手动下载：https://github.com/zsio/netsgo/releases`
* `netsgo upgrade` now says `替换计划` and asks `用本次运行的 netsgo 文件替换已安装版本？`, which is clearer than "current executable".
* Client uninstall no longer mentions unrecoverable identity. It says reinstalling client requires a new `client key` from the Web console.
* Client reinstall completed normally and reported `NetsGo 链路: 已建立`.
* Shared binary removal prompt says `输入 "remove binary" 继续，或输入 no 保留共享二进制。`, so `no` no longer sounds like it cancels already-completed service removal.

Remaining observations:

### P2: `delete server data` is still brittle for tmux automation

The phrase is correct for a human, but `tmux send-keys` treats `delete` as a key name. Attempts to drive the delete-data bulk path through tmux sent only `server data` into the prompt, and the CLI correctly rejected it.

Suggested direction:

* Keep the typed destructive phrase for real users.
* If we want stable scripted E2E for destructive delete-data paths, add a test harness that writes literal text to the PTY, or add a test-only non-interactive confirmation mechanism.

### P3: initialization logs remain partly English

Fresh server install still logs:

* `Initializing server data...`
* `Service initialization complete, admin user: admin`

These are less harmful than the old contradictory warning, but they still stand out in an otherwise Chinese lifecycle flow.

Suggested direction:

* Convert these install-path progress logs to Chinese or suppress them behind debug logging.

## State Left Behind After Update/Upgrade Copy Walkthrough

The E2E host was cleaned after this pass.

* `netsgo-server.service`: unit not found
* `netsgo-client.service`: unit not found
* `/usr/local/bin/netsgo`, `/etc/netsgo`, `/var/lib/netsgo`: absent
* temporary client key/helper files: removed
