# brainstorm: service lifecycle CLI UX audit

## Goal

Walk through the real user lifecycle for NetsGo managed service commands in the Linux test environment, covering install, manage, update/upgrade guidance, uninstall, and reinstall. Capture user experience issues and improvement opportunities based on actual command behavior rather than assumptions.

## What I already know

* User wants a realistic test-environment walkthrough of `install`, `manage`, `update`, `uninstall`, and reinstall flows.
* User is open to being grilled if the flow reveals product or UX trade-offs that need a decision.
* `netsgo install` is Linux/systemd/interactive-TTY only and auto-elevates with sudo when needed.
* `netsgo manage` is Linux/interactive-TTY only and supports status, inspect, logs, start, stop, restart, update, uninstall, and all-service uninstall paths.
* `netsgo update` is currently a guidance command that points managed-service users to `netsgo manage` and points binary replacement users to `netsgo upgrade`.
* `docs/linux-e2e-tmux.md` documents a dedicated Debian 13 x86 AMD systemd test host with passwordless sudo and mapped ports for realistic service lifecycle verification.
* A full lifecycle walkthrough was completed through tmux on 2026-05-01 and persisted in `research/lifecycle-walkthrough.md`.

## Assumptions (temporary)

* This audit can use the dedicated Linux E2E host and may perform destructive service lifecycle operations there.
* The desired output is an actionable UX findings report first; code changes can follow after findings are reviewed.
* Existing installed NetsGo services on the E2E host should be cleaned up unless a failure needs the state preserved for debugging.

## Open Questions

* Should the first pass prioritize a pure observation report, or should small obvious fixes be implemented in the same task after the walkthrough?

## Requirements (evolving)

* Inspect local command implementation, docs, tests, and validation paths before drawing conclusions.
* Build or otherwise obtain a current NetsGo binary suitable for the Linux E2E host.
* On the Linux E2E host, record the baseline state before destructive lifecycle testing.
* Exercise the user-facing lifecycle:
  * `netsgo install` for server.
  * `netsgo manage` status/inspect/logs/start/stop/restart/update paths where practical.
  * uninstall path.
  * reinstall path after uninstall.
  * client install/manage lifecycle if enough setup information is derivable from the running server or docs.
* Note confusing prompts, weak summaries, missing guidance, risky defaults, poor error messages, and cleanup surprises.
* Distinguish confirmed issues from hypotheses.
* For confirmation/cancellation prompts, especially destructive actions such as uninstalling services, deleting data, or removing the shared binary, require explicit typed input from the user instead of arrow-key selection plus Enter. The observed arrow/Enter confirmation UX is too easy to misread and too poor for high-impact actions.
* Treat intentional interactive aborts as cancellations, not errors that dump Cobra usage.
* Suppress or rephrase server install's confusing "Service not yet initialized; please use the install or init command" warning when already running inside `netsgo install`.
* Improve `manage -> Update` guidance for development builds so it is at least as actionable as standalone `netsgo update`.
* `netsgo install` must not ask users to choose a role that is already installed when both roles are installed; it should summarize the installed state and route to `netsgo manage`.
* Role-specific lifecycle prompts must include the role in the prompt title where ambiguity is possible, e.g. `Server uninstall mode` instead of only `Uninstall mode`.
* Cancellation summaries must be context-aware. If cancellation happens inside an active `manage` menu, do not tell the user to rerun `netsgo manage`; tell them they can select another action or go back.
* `netsgo upgrade` must use the same clear plan-and-typed-confirmation UX as other service lifecycle commands. It should show source/target binary, version transition, services to restart, and require an explicit phrase for risky cases such as development builds, unknown installed versions, or downgrades.
* `netsgo upgrade` plan output must be user-facing: services should be formatted as readable names, and unknown installed-version risks must not expose raw semver/parser errors in the main plan.
* Server install must no longer ask for allowed tunnel port ranges. First-time initialization should silently default to `1024-65535`; users can narrow or edit allowed ports later from the Web console.
* Server install trusted proxy CIDRs should default to `0.0.0.0/0` so all IPs are accepted by default. The prompt copy should explain that when NetsGo is behind local Nginx/Caddy, users should prefer `127.0.0.1/8` instead. The user wrote `120.0.0.1/8`, but this task treats it as the loopback `127.0.0.1/8` intent.
* Interactive select menus should include one-sentence option descriptions where the choice is not self-explanatory, including install roles, manage role selection, action menus (`Status`, `Inspect`, `Logs`, `Start`, `Stop`, `Restart`, `Update`, `Uninstall`, `Back`), uninstall modes, recovery actions, and download channel choices.
* Select option descriptions must not require users to open help text elsewhere; each description should clarify the immediate effect of selecting that option.
* For the current pre-i18n product stage, lifecycle CLI interactive copy should be Chinese by default. Do not add a language selector or full i18n framework in this task.
* Preserve stable technical tokens in English where users may type/copy them or where they are platform identifiers: command names (`netsgo install`, `netsgo manage`, `netsgo upgrade`), systemd unit names, file paths, URLs, version strings, role identifiers when embedded in machine-facing rows if needed, and typed confirmation phrases such as `uninstall server` / `remove server data` / `upgrade binary`.
* Chinese copy should cover menu titles, option descriptions, summaries, advice/next-step rows, confirmation descriptions, validation errors that are surfaced during interactive lifecycle use, and update/upgrade guidance.
* Tests must assert representative Chinese lifecycle text so future edits do not drift back to English-only interactive UX.
* Lifecycle copy must speak from the user's intent, not the system's inventory model. Do not describe a role as "missing" or "缺失" merely because the other role is already installed; most users install either server or client on a machine, and the CLI must not imply that installing only one role is incomplete or wrong.
* When exactly one managed role is already installed, `netsgo install` must not automatically start installing the other role. It should summarize the current role, explain that installing the other role on the same machine is optional, and require explicit user confirmation before continuing. A user who installed only server or only client should feel that this is a valid state, not an unfinished setup.
* Update/upgrade guidance must not expose internal execution steps as if the user must perform them. Keep guidance low-pressure and action-oriented, e.g. tell the user to run `netsgo manage` and choose "更新", but do not list internal steps like download, verify, apply, and restart unless the user is viewing a plan or debug detail.
* Chinese lifecycle copy needs a product-copy pass beyond literal translation: every summary, next step, and guidance sentence should be concise, reassuring, and focused on the next user action. Avoid overly operational wording that makes simple flows feel complex.
* Single-role install copy must be concrete, not abstract. Avoid phrases like “只安装 server 是有效配置” or “同机运行” that make users ask what they mean. Prefer direct guidance: “本机已安装 server。如果这台机器还需要作为 client 连接到另一个 NetsGo server，可以继续安装 client。”
* `netsgo update` guidance must not look like a sequence of steps when the rows are alternatives. Prefer conditional/case-based copy such as “通常...” and “已有新版 netsgo 文件...”, or auto-route to the appropriate update path when feasible.
* Do not tell users “本机没有可恢复的 client 身份”. Client recovery is not a concept users need during normal install; guide them through the default client install flow instead.

## Acceptance Criteria (evolving)

* [x] Baseline E2E host state captured.
* [x] Real lifecycle command transcript or summarized observations captured.
* [x] UX findings are ranked by severity and grounded in command behavior.
* [x] Improvement recommendations are concrete enough to implement.
* [x] Any destructive test state is cleaned up or explicitly documented.

## Definition of Done (team quality bar)

* Tests added/updated if code changes are made.
* Lint / typecheck / CI-relevant checks pass if code changes are made.
* Docs/notes updated if behavior changes.
* Rollout/rollback considered if service lifecycle behavior changes.

## Out of Scope (explicit)

* Target-service health checks behind tunnels.
* Multi-node/distributed service lifecycle semantics.
* Windows/macOS service installation support.

## Technical Notes

* Command entrypoints inspected:
  * `cmd/netsgo/cmd_install.go`
  * `cmd/netsgo/cmd_manage.go`
  * `cmd/netsgo/cmd_update.go`
* Main implementation areas:
  * `internal/install/`
  * `internal/manage/`
  * `internal/svcmgr/`
  * `internal/tui/`
* Linux E2E environment doc:
  * `docs/linux-e2e-tmux.md`
* Confirmed UX finding from tmux lifecycle walkthrough:
  * The TUI confirmation widget renders `Yes No` with left/right toggle plus Enter. In practice the active choice is not obvious, Enter cancelled unexpectedly once, and the user explicitly wants typed confirmation/cancellation instead of arrow-key confirmation for these prompts.
  * A rebuilt `ae06485-dirty` binary fixed the core typed confirmation and Ctrl-C usage dump issues, but still exposed remaining flow-level UX gaps around install preflight, role-specific prompt titles, cancellation next steps, manage update wording, and `netsgo upgrade` confirmation style.
* Full observations:
  * `.trellis/tasks/05-01-service-lifecycle-cli-ux-audit/research/lifecycle-walkthrough.md`
