# NetsGo comprehensive security review — final synthesis

## Objective and completion evidence

User objective: perform a broad security review of the project from multiple angles, place all intermediate files and reports under `review.temp`, do not end lightly, and audit for at least 100 minutes.

Completion evidence:

- Audit start marker: `review.temp/audit-start.txt` recorded `2026-06-12T08:56:22Z`.
- Current completion window used for this report: `2026-06-12T10:57:48Z`.
- Elapsed review time: about 121 minutes, satisfying the requested minimum 100 minutes.
- All review artifacts are under `review.temp/`.
- Source files were not modified by the audit; reports only document findings. Existing unrelated working-tree modification `skills-lock.json` was intentionally not included in the audit deliverables.

## Review artifacts

- `review.temp/attack-surface-map.md` — executable entrypoints, listeners, routes, WebSocket surfaces, CLI privileged operations, persisted secrets, external network calls, local command execution, and trust boundaries.
- `review.temp/backend-auth-audit.md` — management-plane auth, sessions, cookies, JWT, password hashing, MFA, passkeys, CSRF/CORS/security headers, rate limiting.
- `review.temp/tunnel-plane-audit.md` — control/data WebSocket protocol, client auth, data-channel binding, tunnel exposure, yamux/resource exhaustion, target/ingress trust boundaries.
- `review.temp/persistence-install-audit.md` — SQLite migrations, local secrets, file permissions, data-dir layout, install/update scripts, service files, upgrade rollback.
- `review.temp/frontend-security-audit.md` — frontend auth state, token/cookie usage, route guards, XSS/DOM sinks, SSE, update UI, generated install commands, sensitive React state.
- `review.temp/supply-chain-release-audit.md` — dependency manifests, GitHub Actions, GoReleaser, release signing, release index, install/upgrade scripts, Docker, desktop/Tauri packaging.
- `review.temp/secrets-crypto-audit.md` — random token/ID generation, TLS modes and TOFU, password/API key/client token storage, TOTP/WebAuthn, release signing, desktop sidecar secret handling.
- `review.temp/input-validation-injection-audit.md` — JSON body handling, SQL/query construction, URL/path construction, log injection, HTML sinks, shell/script injection, Tauri command arguments.
- `review.temp/static-tooling-audit.md` — govulncheck, bun audit, cargo-audit, gosec, and scanner/tool availability results.

## Highest-priority confirmed issues

### 1. Update/install bootstrap script trust boundary

Evidence:

- Documented install/upgrade UX runs `curl -fsSL https://netsgo.zs.uy/install.sh | sh` and `curl -fsSL https://netsgo.zs.uy/upgrade.sh | sh -s -- -y`.
- Authentic scripts verify signed `checksums.txt` before extracting archives, but the shell script itself is fetched and executed before signature verification.
- See `review.temp/supply-chain-release-audit.md` finding 2 and `review.temp/frontend-security-audit.md` F-03/F-04.

Impact:

- Compromise of `netsgo.zs.uy`, DNS/TLS/CDN, or script publishing path gives code execution with installer privileges, often root.

Recommended fix:

- Publish scripts as signed release artifacts, verify script checksums/signatures before execution, or change documentation/UI to a two-step download-then-verify flow.

### 2. Server-exposed TCP/UDP bind IP is accepted but ignored

Evidence:

- Unified tunnel ingress accepts/stores `bind_ip`.
- Server TCP and UDP runtime bind `:%d`, exposing on all interfaces.
- See `review.temp/tunnel-plane-audit.md` finding 1.

Impact:

- Tunnels believed to be loopback/private-interface scoped may become reachable on public server interfaces.

Recommended fix:

- Carry bind IP into server runtime config and bind `net.Listen` / `net.ListenPacket` to `bind_ip:port`. Align preflight/resource-lock keys with the actual bind address.

### 3. Local update cache uses predictable `/tmp` path in root scripts

Evidence:

- Default update cache root is `${TMPDIR:-/tmp}/netsgo-update-cache`.
- Root-running install/upgrade scripts create/reuse tag/platform subdirectories with ordinary `mkdir -p`, `curl -o`, and `mv`.
- See `review.temp/persistence-install-audit.md` PI-01.

Impact:

- Local unprivileged users can pre-create or race cache paths. Signatures prevent simple binary replacement, but the root process still writes/removes/reuses attacker-influenced filesystem paths, creating local DoS and possible file-clobber hardening risk.

Recommended fix:

- Use a root-owned private cache (`/var/cache/netsgo/update` or `mktemp -d`) with owner/mode checks and reject unsafe `NETSGO_UPDATE_CACHE_DIR` paths.

### 4. Unbounded or weakly bounded resource surfaces

Evidence:

- No explicit JSON body size cap on management login/admin/tunnel handlers.
- TCP/HTTP/yamux streams are goroutine-per-connection/request with no per-tunnel/per-client stream cap.
- HTTP tunnel reverse proxy has no response-header timeout.
- Client auth can force bcrypt scans across every API key before failure is recorded.
- See `review.temp/input-validation-injection-audit.md` finding 1, `review.temp/tunnel-plane-audit.md` findings 4/5, and `review.temp/secrets-crypto-audit.md` finding 3.

Impact:

- Remote or authenticated attackers can amplify CPU, memory, goroutine, file descriptor, or parser work. Some paths are unauthenticated (`/api/auth/login`, `/ws/control` pre-auth); tunnel paths require reachable tunnel ports or authenticated/compromised clients.

Recommended fix:

- Add strict JSON decoding helper with `http.MaxBytesReader` and EOF checks.
- Add connection/stream limits and HTTP tunnel transport timeouts.
- Add an API-key lookup hash/key identifier to avoid O(number-of-keys) bcrypt scans.

### 5. MFA verify route is unthrottled after password success

Evidence:

- Login limiter wraps `/api/auth/login` and resets failures after correct password.
- `/api/auth/mfa/verify` has no rate limiter/attempt counter and consumes challenges only on success.
- See `review.temp/backend-auth-audit.md` finding 1.

Impact:

- After password compromise, the 5-minute MFA challenge can be brute-forced online with no per-challenge/per-IP attempt ceiling.

Recommended fix:

- Track MFA challenge attempts and throttle/lock per challenge, user, and IP. Do not fully reset abuse state until MFA completion.

### 6. Go standard library vulnerabilities in active toolchain

Evidence:

- `govulncheck` using Go `1.26.3` found reachable `GO-2026-5039` in `net/textproto` and `GO-2026-5037` in `crypto/x509`, both fixed in Go `1.26.4`.
- See `review.temp/static-tooling-audit.md`.

Impact:

- Public HTTP listener and TLS verification paths use affected standard-library packages.

Recommended fix:

- Build and release with Go `1.26.4+` once available in project CI/release toolchain.

### 7. Web dependency audit failures

Evidence:

- `bun audit` in `web/` reported 32 advisories: 6 high, 25 moderate, 1 low.
- High findings include `fast-uri`, `flatted`, `path-to-regexp`, and `picomatch`, mostly through `shadcn`, ESLint/dev tooling, MSW, and Vite dependency paths.
- See `review.temp/static-tooling-audit.md` and `review.temp/supply-chain-release-audit.md`.

Impact:

- Mostly build/dev/design-tool supply-chain risk, not automatically production runtime exploitability. `shadcn` is currently listed under dependencies, which broadens the production install graph unless proven tree-shaken/unused.

Recommended fix:

- Move generator-only tooling out of runtime dependencies, upgrade dependencies, and rerun `bun audit`.

## Additional confirmed issues by domain

### Authentication and browser session

- Passkey login begin leaks whether a passkey exists for the configured admin origin (`review.temp/backend-auth-audit.md` finding 2).
- First-time and reset admin passwords are accepted through command-line flags, exposing secrets to process observers and shell history (`backend-auth-audit.md` finding 3).
- Cookie security is mostly sound, but `Secure` depends on HTTPS/trusted proxy detection; plain HTTP deployments expose cookies/tokens (`backend-auth-audit.md` non-findings/risky assumptions; `attack-surface-map.md`).
- Browser route guard trusts `localStorage` auth state and only discovers revoked/expired sessions reactively (`frontend-security-audit.md` F-01).

### Tunnel/control/data plane

- Admin-controlled target host/port gives client-local SSRF/pivot capability by design (`tunnel-plane-audit.md` finding 2; `attack-surface-map.md` risk note 2).
- Control WebSocket handler upgrades directly; host dispatcher requires NetsGo subprotocol, but handler itself does not explicitly reject wrong method/upgrade/subprotocol (`tunnel-plane-audit.md` finding 3; `attack-surface-map.md` notes dispatcher subprotocol gating).
- API key `max_uses` can be bypassed for the same install ID by refreshing an existing unexpired token (`tunnel-plane-audit.md` finding 6).

### Persistence/local install

- SQLite stores TOTP seed plaintext, plus setup challenge JSON containing seed/provisioning URL while pending (`persistence-install-audit.md` PI-02; `secrets-crypto-audit.md` assumptions).
- Client env and client SQLite store raw credentials/tokens (`persistence-install-audit.md` PI-03).
- Migrations are forward-only; upgrade rollback restores only the binary, not SQLite state (`persistence-install-audit.md` PI-04).
- Migration 005 can fail on unexpected legacy `desired_state` / `runtime_state` values because the new schema has CHECK constraints and legacy schema did not (`persistence-install-audit.md` PI-05).
- Systemd unit/env rendering does not escape whitespace/newlines/metacharacters in paths/env values (`persistence-install-audit.md` PI-06).

### Frontend/desktop/input validation

- Backend-supplied update `release_url` is rendered as an anchor without protocol/host validation (`frontend-security-audit.md` F-02).
- Backend-supplied upgrade commands are displayed and copied verbatim (`frontend-security-audit.md` F-03).
- Quick-start install commands execute remote scripts and embed live client keys in copied shell/YAML (`frontend-security-audit.md` F-04).
- Several legacy frontend paths interpolate unencoded `clientId` (`frontend-security-audit.md` F-05); exploitability depends on backend client ID format.
- Admin security dialogs retain passwords/MFA/recovery material in React state after close/success in several flows (`frontend-security-audit.md` F-06).
- Client-supplied identity fields can reach line-oriented logs without CR/LF/control-character sanitization (`input-validation-injection-audit.md` finding 2; gosec `G706`).
- Tauri sidecar command accepts renderer-supplied `data_dir` as sidecar `--data-dir` (`input-validation-injection-audit.md` finding 3).
- Desktop sidecar path resolution can fall back to current-working-directory `binaries/netsgo-*` paths in dev/broken packaging contexts (`supply-chain-release-audit.md` finding 5).

### Secrets/cryptography

- `generateUUID()` and `generateDataToken()` ignore `crypto/rand` errors; data token failure could produce all-zero bearer secret (`secrets-crypto-audit.md` finding 1).
- TLS skip verification is persisted and TOFU only protects after first connection (`secrets-crypto-audit.md` finding 2; gosec `G402`).
- API key validation performs O(number-of-keys) bcrypt scans and can double-scan on success (`secrets-crypto-audit.md` finding 3).

### Supply chain/release/desktop

- Update-check metadata is unsigned and URL/provider fields are not constrained in Go parser (`supply-chain-release-audit.md` finding 1).
- Release workflow uses mutable action tags instead of full commit SHAs in jobs with elevated permissions/secrets (`supply-chain-release-audit.md` finding 3).
- macOS desktop artifacts are ad-hoc signed and not notarized (`supply-chain-release-audit.md` finding 4).
- `cargo-audit` reported GTK3 gtk-rs unmaintained warnings and `glib 0.18.5` unsoundness warning (`static-tooling-audit.md`).

## Positive controls observed

- Management routes besides login/MFA/passkey begin/finish and static UI are server-side `RequireAuth` protected.
- JWTs are backed by server-side sessions; tokens contain session ID only and server session state is loaded on each request.
- Session cookies are `HttpOnly`, `SameSite=Strict`, scoped to `/api`, and conditionally `Secure` when HTTPS/trusted proxy says HTTPS.
- Passwords, recovery codes, and API keys are hashed at rest; raw API keys are only returned on creation.
- Client tokens are 256-bit random, stored server-side as SHA-256 hashes, compared constant-time, bound to install ID, and revocable/expiring.
- WebAuthn origin/RP checks are present and enforce HTTPS except localhost.
- Data WebSocket requires per-control-session data token and generation checks before yamux.
- Data stream headers have structural validation and size limits.
- UDP paths have payload/session caps.
- Host dispatch prevents arbitrary Host fallback to the admin UI and separates management/tunnel hosts.
- HTTP tunnel domain validation rejects schemes, paths, wildcards, IP literals, ports, whitespace, and invalid labels.
- SQLite DB/WAL/SHM files are created/chmodded `0600`; managed runtime dirs are `0750`.
- Release archives are protected by signed checksums once the authentic script is running.
- Docker runtime image is distroless nonroot.
- Tauri capabilities do not expose arbitrary shell execution to the frontend API; sidecar spawning happens in Rust with argument arrays.

## Recommended remediation order

1. **Release/bootstrap hardening:** signed bootstrap or verified script flow; pin GitHub Actions by SHA; upgrade Go toolchain for standard-library CVEs.
2. **Exposure correctness:** honor server TCP/UDP `bind_ip`; document/admin-confirm network pivot semantics; add target allow/deny policy if clients should not expose arbitrary local networks.
3. **DoS hardening:** body caps, stream/connection limits, HTTP tunnel timeouts, API-key lookup optimization, MFA verify attempt limits.
4. **Local privilege/install hardening:** private update cache, chown walk symlink hardening, DB backup before migrations, migration 005 legacy-value handling, escaped service env/unit rendering.
5. **Frontend/admin safety:** validate release links and copied commands, remove `localStorage` as route-auth authority, clear sensitive React state, encode all path segments consistently.
6. **Crypto/secrets cleanup:** propagate CSPRNG errors, tighten TLS-skip install flow around explicit fingerprint pinning, decide and document plaintext TOTP/client-token threat model.
7. **Dependency cleanup:** move `shadcn` to dev/generator-only usage or remove from runtime deps, upgrade web/Rust dependency stacks, rerun audits with node_modules/build/session exclusions.

## Verification performed

- Source-level review across backend, client, protocol, frontend, desktop, scripts, workflows, Docker, migrations, and tests.
- Dedicated sub-reports written by parallel audit agents and checked by Main.
- Tooling runs:
  - `govulncheck` completed with reachable Go standard-library findings.
  - `bun audit` completed in `web/` and `desktop/`.
  - `cargo-audit` completed for Tauri Rust lockfile.
  - `gosec` completed via `go run`; output requires scanner exclusions but produced useful first-party findings.
- Secret regex search found expected examples/dev/test values and ignored local session artifacts, not confirmed committed production private keys.

## Explicit non-goals

This review did not implement fixes. It produced audit artifacts only. Scanner output should be rerun after dependency/toolchain changes and with an explicit exclusion config for generated/vendor/session directories.
