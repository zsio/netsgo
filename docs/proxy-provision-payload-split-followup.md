# Proxy Provision Payload Split Follow-up

This document is a handoff for the next agent or developer. It records what is
still planned after the test-plan hardening commit. It is intentionally separate
from `docs/proxy-provision-payload-split-plan.md` so the implementation plan can
stay stable while remaining work is tracked explicitly.

## Current scope

The initial handoff scope was test-plan hardening only. The user later asked to
continue, so production payload-split implementation has started. The historical
`make test-tdd-red-*` targets are now ordinary focused regression targets; the
client/server `requireTDDRed(t)` guards have been removed.

## What has landed

The branch already contains the main compatibility-test scaffolding:

- protocol/client/server unit and guard tests for legacy flat payloads,
  unified payload round-trip, legacy fallback, unprovision revision guards,
  server-expose revision/header behavior, clean reject, and reconcile registry
  behavior;
- `internal/client/testdata/legacy_v0.1.8_*` fixtures for real v0.1.8 flat
  provision and close payload shapes;
- `test/e2e/scripts/test-baseline.sh`, `test-compat.sh`, and
  `test-upgrade.sh`;
- manual `Cross-Version E2E` GitHub workflow;
- PR CI smoke coverage for baseline and mixed-version compatibility;
- `docs/proxy-provision-payload-split-plan.md` section `6.0.5` coverage matrix.

The latest verified stable baseline is:

```bash
make test-baseline-e2e COMPAT_BASELINE=v0.1.8 BASELINE_MODE=full
```

That run reused an existing local `netsgo-e2e:v0.1.8` image. It proves the
v0.1.8 runtime baseline, not the tag-to-image rebuild path.

## Landed after the initial handoff

### Rollback "old server continues service" coverage strengthened

Current state:

- `server-rollback` and `current-write-rollback` revalidate existing
  HTTP/TCP/UDP/SOCKS5 server-expose tunnels after stable server rollback;
- they also create and verify a new HTTP/TCP/UDP/SOCKS5 server-expose suite
  after rollback;
- each new tunnel must reach `active`;
- each new tunnel must have empty `issues`;
- HTTP/TCP/UDP/SOCKS5 data paths must work;
- server listener counts must be `1` for the new TCP/UDP/SOCKS5 ports.

Implementation evidence:

- dedicated server alt port variables were added:
  `E2E_SERVER_TCP_ALT_PORT`, `E2E_SERVER_UDP_ALT_PORT`,
  `E2E_SERVER_SOCKS5_ALT_PORT`;
- `Makefile` passes them to `test-upgrade.sh`;
- corresponding server port mappings were added to
  `test/e2e/docker-compose.system.yml`;
- `assert_new_server_expose_suite_works` was added in
  `test/e2e/scripts/test-upgrade.sh`;
- that helper is called from `case_server_rollback` and
  `case_current_write_rollback`.

These tests intentionally use server alt ports, not `C2C_*` host ports.
`C2C_*` ports are mapped on `ingress-client`, so they are the wrong proof
surface for server-expose rollback creation.

## Remaining work before calling the test plan complete

### 1. External review result

Qoder review has been run after the rollback suite was strengthened. Its
conclusion:

- no hidden design blocker in the test strategy;
- the initial hard blocker was that full cross-version gates had not been
  executed on the current worktree;
- important non-blockers to track:
  - capability-loss / reconcile-stage clean reject now has in-process
    server-expose and client-to-client coverage, while Docker E2E remains
    absent;
  - client-side unknown target provision reject has since been implemented and
    converted to a normal regression;
  - stable server with already-upgraded current clients now creates and
    verifies a new server-expose HTTP/TCP/UDP/SOCKS5 suite in `clients-only`;
  - stable server mutation of current-written rows after rollback is not
    covered;
  - legacy managed tunnel cross-version E2E is absent. It is now explicitly
    scoped as unit/in-process coverage only, because neither current nor
    `v0.1.8` `netsgo client` exposes `Client.ProxyConfigs` through a real
    CLI/env/config product surface, and system E2E is intentionally scoped to
    `/api/tunnels` mutations rather than legacy APIs.

Command used:

```bash
qodercli --yolo "请严肃审查 NetsGo 的 proxy provision payload split 测试规划。重点看 docs/proxy-provision-payload-split-plan.md、docs/proxy-provision-payload-split-followup.md、test/e2e/scripts/test-upgrade.sh、test/e2e/scripts/test-compat.sh、Makefile、.github/workflows/cross-version-e2e.yml。请只评价测试规划和兼容验收，不要实现生产代码。请明确指出 blocker、非 blocker、以及是否还存在 old server/current client、old client/current server、server rollback/current-write rollback 的覆盖缺口。"
```

Workflow follow-up from the review: `.github/workflows/cross-version-e2e.yml`
now passes `BASELINE_MODE=full` explicitly instead of relying on the Makefile
default.

### 2. Full cross-version tests

The required full cross-version gates have been run on the current worktree:

```bash
make test-compat-e2e COMPAT_BASELINE=v0.1.8 COMPAT_MODE=full COMPAT_ABORT_ON_FAILURE=true
make test-upgrade-e2e COMPAT_BASELINE=v0.1.8
```

Evidence:

- `test-compat-e2e`: passed `11/11`.
- `test-upgrade-e2e`: passed `9/9`.
- The full compat run rebuilt `netsgo-e2e:v0.1.8` from tag `v0.1.8`, so the
  stable-image rebuild path has also been exercised.
- While closing the gate, `verify_tcp_http` was changed from `nc` to `curl`
  because BSD/netcat timeout semantics produced false negatives after the HTTP
  backend had already returned a valid response.
- While closing the gate, unified resume was fixed to clear stale server-expose
  runtime state before scheduling reconcile; otherwise current server could
  report an HTTP tunnel active after stop/resume while the route still returned
  502.

### 3. Status labels after evidence exists

After the full test runs, the coverage matrix has been updated:

- legacy managed tunnel create/provision is `[GREEN]` within its explicit
  unit/in-process compatibility scope;
- capability-loss / reconcile-stage clean reject now has in-process
  server-expose and client-to-client coverage, plus a focused Docker E2E via
  the dedicated `e2e_capability_loss` test image and
  `TestSystemCapabilityLossReconcileE2E`;
- keep converted red guards as normal regressions instead of deleting them.

Do not add test-only production APIs to turn scoped rows into Docker E2E. The
capability-loss Docker proof uses a dedicated build-tagged test image instead
of a runtime product switch; `make test-system-e2e-capability-loss` has passed
and the matrix row is now `[GREEN]`.

## Production implementation status

The production payload split work has now been implemented in this worktree.
The completed implementation themes are:

- fixed TCP/UDP/HTTP target runtime must stop relying on legacy `c.proxies`;
- SOCKS5 target runtime must remain endpoint-specific;
- server-expose runtime/reconcile must use `TunnelSpec` endpoint data, not
  `StoredTunnel.ProxyNewRequest`, as the unified runtime source;
- HTTP host dispatch must use the ingress endpoint domain;
- stale provision ACKs must not activate old revisions;
- rejected provision must leave no listener, runtime, or ack waiter;
- reconcile registry dirty rerun/coalescing must be fixed;
- shutdown and in-flight reconcile cleanup have runtime-store-level tests;
- capability-loss / reconcile-stage clean reject now has in-process runtime
  cleanup coverage and a passing focused Docker E2E using a dedicated
  build-tagged test image.

Keep this section as a compact handoff summary of the runtime invariants that
the current tests are protecting.
