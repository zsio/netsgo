# NetsGo attack surface map

## Scope and method

Scope: repository attack-surface inventory from code, tests, config, scripts, and docs visible in this workspace. I inspected backend Go server/client/installer/updater code, frontend API usage, release scripts, Docker/dev/Tauri config, storage migrations, and tests that exercise routing and dispatch behavior. I did not run project-wide build, test, lint, format, or security-scanner commands.

Files inspected: `cmd/netsgo/main.go`, `cmd/netsgo/cmd_server.go`, `cmd/netsgo/cmd_client.go`, `cmd/netsgo/cmd_install.go`, `cmd/netsgo/cmd_manage.go`, `cmd/netsgo/cmd_upgrade.go`, `cmd/netsgo-release-sign/main.go`; `internal/server/server.go`, `server_bootstrap.go`, `server_http.go`, `http_tunnel.go`, `http_tunnel_proxy.go`, `control_auth.go`, `control_loop.go`, `data.go`, `proxy.go`, `udp_proxy.go`, `events.go`, `console_api.go`, `version_api.go`, `version_cache.go`, `update_capability.go`, `auth_middleware.go`, `admin_api.go`, `admin_security_api.go`, `admin_store.go`, `admin_security_store.go`, `init.go`, `tls.go`, `storage_schema.go`, migrations `001`, `005`, `006`; `internal/client/client.go`, `unified_tunnel.go`, `udp_handler.go`, `state.go`, `state_store.go`; `internal/install/*`, `internal/manage/*`, `internal/svcmgr/*`, `internal/storage/sqlite.go`, `internal/installmethod/*`; `pkg/netutil/netutil.go`, `pkg/updater/*`, `pkg/fileutil/atomic.go`, `pkg/flock/flock_unix.go`, `pkg/protocol/*`; `web/src/lib/api.ts`, `web/src/hooks/use-event-stream.ts`, related frontend hook/route searches; `scripts/install.sh`, `scripts/upgrade.sh`, `scripts/common-update.sh`, `Dockerfile`, `docker-compose.dev.yml`, `desktop/src-tauri/tauri.conf.json`, `web/vite.config.ts`, and tests referenced inline below.

## Executable entrypoints

- `netsgo` CLI root: Cobra root command executes `rootCmd.Execute()` (`cmd/netsgo/main.go:15-40`) and enables `NETSGO_*` env binding via Viper (`cmd/netsgo/main.go:31-35`).
- `netsgo server`: starts the web/API/control/data/tunnel server (`cmd/netsgo/cmd_server.go:105-220`). Important flags/env include `--port`, `--data-dir`, TLS files/mode, trusted proxies, first-run admin init flags, server external address, and loopback management-host allowance (`cmd/netsgo/cmd_server.go:223-274`).
- `netsgo client`: starts a long-lived proxy agent that connects to the server, authenticates with key/token, opens yamux data channel, reports host stats, and obeys tunnel instructions (`cmd/netsgo/cmd_client.go:21-101`, `cmd/netsgo/cmd_client.go:123-151`).
- `netsgo install`: interactive or non-interactive client installer, Linux/systemd/root only, auto-reruns via sudo when not root (`cmd/netsgo/cmd_install.go:13-66`, `internal/install/install.go:78-97`).
- `netsgo manage`: interactive service manager plus offline `reset-admin-user` recovery subcommand (`cmd/netsgo/cmd_manage.go:16-93`). Reset can mutate the server SQLite admin user and auto-sudo only for the default managed data dir (`cmd/netsgo/cmd_manage.go:58-87`).
- `netsgo upgrade`: replaces installed managed-service binary and restarts managed services, auto-reruns via sudo (`cmd/netsgo/cmd_upgrade.go:196-233`, `pkg/updater/upgrade.go:22-87`).
- `netsgo-release-sign`: release signing utility with `sign`, `keygen`, `public`, and `verify-embedded`; writes private/public keys/signatures and shells out to `ssh-keygen` for SSH signatures (`cmd/netsgo-release-sign/main.go:21-24`, `cmd/netsgo-release-sign/main.go:62-97`, `cmd/netsgo-release-sign/main.go:173-215`, `cmd/netsgo-release-sign/main.go:230-276`).
- Container runtime: release image runs `/usr/local/bin/netsgo server`, exposes `9527`, and defaults `NETSGO_PORT=9527` (`Dockerfile:51-62`). Dev compose runs source-mode server and clients via `go run` and exposes server `9527` plus Vite `5173` (`docker-compose.dev.yml:16-45`, `docker-compose.dev.yml:93-159`).
- Desktop/Tauri bundle references external sidecar binary `binaries/netsgo` and a local dev URL, with CSP allowing `ipc`, localhost IPC WebSocket, and local asset origins (`desktop/src-tauri/tauri.conf.json:6-10`, `desktop/src-tauri/tauri.conf.json:21-40`).

## Listeners and inbound network surfaces

### Server process

- Main HTTP(S) listener binds all interfaces on `:<port>` using `net.Listen("tcp", fmt.Sprintf(":%d", s.Port))`; optional TLS wraps the listener (`internal/server/server_bootstrap.go:134-155`). HTTP server uses `ReadHeaderTimeout: 10s` and `IdleTimeout: 120s` (`internal/server/server_bootstrap.go:166-170`).
- Internal NetsGo WebSocket routes are `/ws/control` and `/ws/data` (`internal/server/server_http.go:76-79`). Dispatch also recognizes those paths only when the expected `Sec-WebSocket-Protocol` is present (`internal/server/http_tunnel.go:245-268`, `internal/server/http_tunnel_proxy.go:79-88`). Tests confirm correct subprotocol gating and business WebSocket passthrough behavior (`internal/server/http_tunnel_test.go:462-493`, `internal/server/http_dispatch_test.go:581-617`).
- Server-exposed TCP tunnels bind `:%d` on all interfaces for the requested/assigned public remote port (`internal/server/proxy.go:296-328`).
- Server-exposed UDP tunnels bind `:%d` with `net.ListenPacket("udp", addr)` on all interfaces (`internal/server/udp_proxy.go:194-213`) and enforce global/per-IP session caps in the UDP read loop (`internal/server/udp_proxy.go:274-323`).
- Server-exposed HTTP tunnels do not bind extra ports; the main HTTP listener dispatches by `Host` to live HTTP tunnel routes (`internal/server/http_tunnel_proxy.go:90-117`, `internal/server/http_tunnel_proxy.go:197-247`). The reverse proxy opens a server-relay yamux stream to the target client and rewrites forwarded headers (`internal/server/http_tunnel_proxy.go:249-298`).
- Server control/data channels carry nested yamux streams over WebSocket (`internal/server/data.go:114-152`, `internal/server/data.go:184-200`).

### Client process

- Client does not expose a listener by default, but server-provisioned client-to-client ingress tunnels bind local TCP/UDP listeners on `bind_ip:port` from server-sent tunnel specs (`internal/client/unified_tunnel.go:373-422`). Preflight probes also bind then close TCP/UDP ports (`internal/client/unified_tunnel.go:256-280`).
- Client accepts data streams from the server/ingress over yamux and dials local TCP or UDP services configured by server instructions (`internal/client/client.go:924-963`, `internal/client/udp_handler.go:14-33`). This is a key trust boundary: authenticated server/admin configuration controls what local host/port the client connects to.
- Client-to-client ingress accepts inbound TCP/UDP on the ingress client, opens yamux streams back to the server relay, and relays to a target client (`internal/client/unified_tunnel.go:425-472`, `internal/client/unified_tunnel.go:535-701`).

### Frontend/dev listeners

- Production web UI is served from embedded assets with `http.FileServerFS` when available (`internal/server/server_bootstrap.go:89-96`) and routed via `handleWeb` at `/` (`internal/server/server_http.go:25-27`).
- Vite dev server binds `0.0.0.0`, proxies `/api` to backend HTTP and `/ws` to backend WS; `changeOrigin` is configurable and default true in `web/vite.config.ts:27-44`. `allowedHosts` includes fixed hostnames (`web/vite.config.ts:29-33`).

## HTTP routes and authentication

Registered management routes are in `internal/server/server_http.go:25-74`:

- Static/UI fallback: `GET/any /` -> `handleWeb`.
- Auth/public login routes: `POST /api/auth/login`, `POST /api/auth/mfa/verify`, `POST /api/auth/passkey/begin`, `POST /api/auth/passkey/finish` are not wrapped with `RequireAuth` (`internal/server/server_http.go:49-52`). Login rate limiting is IP based (`internal/server/admin_api.go:55-65`).
- Authenticated status/version/client routes: `GET /api/status`, `GET /api/version/check`, `GET /api/clients`, `DELETE /api/clients/{id}`, `GET /api/clients/{id}/version/check`, `GET /api/console/snapshot`, display name and bandwidth updates (`internal/server/server_http.go:27-35`).
- Authenticated tunnel-management routes: unified `GET/POST /api/tunnels`, `GET/PUT/DELETE /api/tunnels/{tunnel_id}`, `PUT /api/tunnels/{tunnel_id}/{action}` plus legacy per-client tunnel create/update/resume/stop/delete and traffic (`internal/server/server_http.go:36-47`).
- Authenticated admin/API key/config/security routes: API keys, server config, username/password, TOTP, recovery codes, passkeys (`internal/server/server_http.go:53-72`).
- Authenticated SSE route: `GET /api/events` (`internal/server/server_http.go:73`, `internal/server/events.go:107-109`).

Auth details:

- `RequireAuth` accepts `Authorization: Bearer` first, then the `netsgo_session` cookie (`internal/server/auth_middleware.go:17-32`). JWT contains only a session ID and is validated with an HMAC secret stored in SQLite; actual session state is loaded server-side (`internal/server/auth_middleware.go:48-130`).
- Session cookie is `HttpOnly`, `SameSite=Strict`, `Path=/api`, and `Secure` only when `isHTTPSRequest` sees HTTPS (`internal/server/auth_middleware.go:175-198`).
- Browser API wrapper always sends same-origin credentials and does not store tokens in JS (`web/src/lib/api.ts:1-7`, `web/src/lib/api.ts:77-130`). SSE uses `fetch('/api/events')` with `credentials: 'same-origin'` (`web/src/hooks/use-event-stream.ts:448-455`).
- Security headers include CSP, frame denial, nosniff, referrer policy, and HSTS for HTTPS requests (`internal/server/server_http.go:81-94`).

Management-host and tunnel-host dispatch:

- Main handler first gives NetsGo control/data WS requests precedence, then host-matched HTTP tunnel routes, then dev mode, then management-host matching, else 404 (`internal/server/http_tunnel_proxy.go:79-117`).
- Management host is based on `NETSGO_SERVER_ADDR` or stored server config, otherwise listen address; explicit `AllowLoopbackManagementHost` expands loopback Host acceptance (`internal/server/http_tunnel_proxy.go:119-170`, `internal/server/http_tunnel.go:270-279`).

## WebSocket routes and protocol trust boundaries

- `/ws/control`: gorilla WebSocket with origin check `Origin.Host == r.Host` or no Origin allowed (`internal/server/control_auth.go:23-38`). First message must be `MsgTypeAuth`; max control message size is `1 << 20` (`internal/server/control_auth.go:19-21`, `internal/server/control_auth.go:55-65`, `internal/server/control_auth.go:121-136`).
- Client authentication modes:
  - Token renewal path validates persisted client token + install ID and rejects concurrent live sessions (`internal/server/control_auth.go:176-217`).
  - First registration exchanges an API key for a client token and registered client row (`internal/server/control_auth.go:219-255`, `internal/server/admin_store.go:1673-1717`).
  - Successful auth returns `ClientID`, optional new client token, and a 32-byte random data token (`internal/server/control_auth.go:262-294`, `internal/server/control_auth.go:49-53`).
- `/ws/data`: requires WebSocket upgrade and binary handshake carrying `clientID` + data token. Data token is compared using `subtle.ConstantTimeCompare`; success starts a yamux server session (`internal/server/data.go:21-120`).
- Client dials `/ws/control` and `/ws/data` derived from `--server`, sets the expected subprotocol, can skip TLS verification with `--tls-skip-verify`, and can pin/store certificate fingerprint (`internal/client/client.go:268-291`, `internal/client/client.go:331-348`, `internal/client/client.go:760-833`, `internal/client/client.go:836-888`).

## CLI subcommands and privileged operations

- `install`, `manage`, and `upgrade` auto-elevate using `sudo` through `syscall.Exec` when not root (`cmd/netsgo/cmd_install.go:48-58`, `cmd/netsgo/cmd_upgrade.go:217-227`, `internal/install/install.go:88-97`, `internal/manage/manage.go:96-101`, `cmd/netsgo/exec_unix.go:7-8`).
- Managed install creates system user/group via `groupadd`/`useradd`, copies binary to `/usr/local/bin/netsgo`, writes env under `/etc/netsgo/services`, writes systemd unit under `/etc/systemd/system`, reloads systemd, and enables/starts service (`internal/svcmgr/user_linux.go:10-31`, `internal/svcmgr/binary_linux.go:12-38`, `internal/svcmgr/env.go:31-71`, `internal/svcmgr/unit.go:59-63`, `internal/svcmgr/systemd.go:13-23`, `internal/install/service_flow.go:143-164`).
- Managed data directories are `/var/lib/netsgo/{server,client}` with `/var/lib/netsgo/locks`, mode `0750`, and ownership changed to the `netsgo` user when present (`internal/svcmgr/layout.go:12-18`, `internal/install/dirs.go:23-65`).
- Manage can stream logs by `syscall.Exec("/usr/bin/journalctl", ...)`, start/stop services using systemctl, remove unit/env/data paths, daemon-reload, and maybe remove shared binary (`internal/manage/client.go:77-90`, `internal/manage/client.go:150-160`, `internal/manage/server.go:90-91`).
- Upgrade stops managed services, backs up/restores/replaces `/usr/local/bin/netsgo`, repairs env ownership, restarts services, and rolls back on failure/panic (`pkg/updater/upgrade.go:22-87`, `pkg/updater/replace.go:39-117`, `pkg/updater/update.go:39-70`).
- Release signing command writes private keys `0600`, public/allowed/signature files `0644`, creates a temp private key for `ssh-keygen -Y sign`, and reads signing key from `NETSGO_RELEASE_SIGNING_KEY_PEM` or `--private-key` (`cmd/netsgo-release-sign/main.go:55-65`, `cmd/netsgo-release-sign/main.go:191-215`, `cmd/netsgo-release-sign/main.go:230-276`).

## Persisted secrets and local files

- Server SQLite path: `<data-dir>/server/netsgo.db` (`internal/server/server_bootstrap.go:60-66`). Client SQLite path: `<data-dir>/client/netsgo.db`; legacy JSON path: `<data-dir>/client/client.json` (`internal/client/state.go:14-28`). Default managed data dir is `/var/lib/netsgo` (`internal/svcmgr/layout.go:12-18`).
- SQLite storage creates parent directories `0750`, creates DB with `0600`, chmods DB/WAL/SHM to `0600`, and applies migrations (`internal/storage/sqlite.go:26-63`, `internal/storage/sqlite.go:140-183`).
- Server DB stores: JWT secret (`server_config.jwt_secret`), admin bcrypt password hash, API key bcrypt hash, registered clients, client token SHA-256 hashes, admin sessions, tunnels, traffic (`internal/server/migrations/001_server_runtime_schema.sql:6-144`). Later migration adds TOTP secret, recovery-code bcrypt hashes, passkey credential JSON, WebAuthn challenge JSON (`internal/server/migrations/006_admin_security.sql:14-50`). Unified tunnel migration stores endpoint configs, target host/port, resource locks, runtime state (`internal/server/migrations/005_unified_tunnel_storage.sql:12-164`).
- Admin init generates bcrypt password hash and a random 32-byte JWT secret stored as hex (`internal/server/admin_store.go:295-360`). Admin reset deletes sessions, passkeys, recovery codes, and users, then inserts a new admin bcrypt hash (`internal/server/admin_store.go:597-660`).
- API keys: raw key is generated once as `sk-` + UUID and returned in creation response; only bcrypt key hash is persisted (`internal/server/admin_api.go:162-197`, `internal/server/admin_store.go:1583-1649`).
- Client tokens: raw token is random 32-byte `tk-...`, persisted on client in SQLite, and persisted on server only as SHA-256 hash (`internal/server/admin_store.go:1719-1732`, `internal/server/admin_store.go:1820-1885`, `internal/client/state_store.go:38-96`).
- Client state stores `install_id`, raw client token, and TLS fingerprint in client SQLite (`internal/client/state_store.go:15-19`, `internal/client/state_store.go:62-96`). Legacy JSON state is read and migrated if present (`internal/client/state.go:54-71`, `internal/client/state.go:91-104`).
- Auto TLS writes `server.crt` and `server.key` under `<data-dir>/server/tls` or configured auto dir with directory `0700` and files `0600`; custom TLS reads user-provided cert/key paths (`internal/server/tls.go:77-110`, `internal/server/tls.go:113-177`).
- Env files can contain `NETSGO_KEY`, `NETSGO_TLS_FINGERPRINT`, TLS key path, server address, trusted proxies; written under `/etc/netsgo/services/*.env` as `0640` and ownership repaired (`internal/svcmgr/env.go:31-71`, `internal/svcmgr/env.go:110-143`).
- Lock files: server/client singleton locks under `<data-dir>/locks/*.lock`, opened `0600` after creating lock directory (`cmd/netsgo/cmd_server.go:65-102`, `cmd/netsgo/cmd_client.go:77-81`, `pkg/flock/flock_unix.go:12-18`).
- Update/install scripts cache release detail, checksums, signatures, and archives under `${TMPDIR:-/tmp}/netsgo-update-cache/<tag>/<platform>` unless overridden by `NETSGO_UPDATE_CACHE_DIR`; cleanup behavior differs between completed and failed upgrades (`scripts/upgrade.sh:341-380`, `scripts/upgrade.sh:481-499`).

## External network calls

- Client/server public IP probes call `https://netsgo.zs.uy/ip`, ipify, icanhazip, and ipw.cn; LAN outbound-IP fallback dials UDP `8.8.8.8:80` without sending payload (`pkg/netutil/netutil.go:11-24`, `pkg/netutil/netutil.go:29-41`, `pkg/netutil/netutil.go:112-137`). Server refreshes public IPs on startup and every 2 hours (`internal/server/console_api.go:271-322`); client refreshes public IPs in background during probe reporting (`internal/client/client.go:1079-1135`).
- Version-check API fetches release index from CNB then GitHub raw URLs with a 15s HTTP client and caches results for 12h; authenticated users can force refresh subject to a 10s force cooldown (`pkg/updater/release_index.go:13-19`, `pkg/updater/release_index.go:72-100`, `internal/server/version_api.go:32-42`, `internal/server/version_cache.go:23-95`).
- Install/upgrade scripts download latest/release JSON, checksums, signatures, and archives from allowlisted GitHub/CNB URLs only (`scripts/install.sh:6-8`, `scripts/install.sh:63-98`, `scripts/install.sh:168-223`, `scripts/install.sh:364-429`, `scripts/upgrade.sh:569-609`).
- Installer client TLS verification may actively dial the configured server host:port before writing client service config (`internal/install/client.go:281-307`).
- Client command connects to configured server over WebSocket control/data URLs (`internal/client/client.go:268-291`, `internal/client/client.go:760-833`).
- Tunnels create arbitrary network reachability selected by authenticated admin/server config:
  - server-expose TCP/UDP opens public server ports and relays to client-local target host/port (`internal/server/proxy.go:296-328`, `internal/server/udp_proxy.go:194-323`, `internal/client/client.go:953-962`, `internal/client/udp_handler.go:23-32`);
  - HTTP host tunnels expose arbitrary HTTP/WebSocket traffic by Host on the server listener to client-local targets (`internal/server/http_tunnel_proxy.go:79-117`, `internal/server/http_tunnel_proxy.go:249-298`);
  - client-to-client ingress binds on one client and relays to another client target (`internal/client/unified_tunnel.go:373-422`, `internal/client/unified_tunnel.go:664-701`).

## Local command execution inventory

- Runtime server/client normal operation: no shell execution observed in core server/client tunnel paths; operations are Go network/file/SQLite calls.
- Installer/manager/updater: `sudo`, `systemctl`, `journalctl`, `groupadd`, `useradd`, and installed binary `--version` are executed as local commands (`cmd/netsgo/cmd_install.go:48-58`, `internal/svcmgr/systemd.go:13-75`, `internal/svcmgr/user_linux.go:19-31`, `cmd/netsgo/cmd_upgrade.go:281-293`, `internal/manage/client.go:77-90`).
- Release signing: `ssh-keygen -Y sign` in production command; tests also verify with `ssh-keygen -Y verify` (`cmd/netsgo-release-sign/main.go:191-215`).
- System info: Linux OS install time may execute `stat --format=%W /` (`pkg/sysinfo/install_time_linux.go:14-18`).
- Shell scripts: install/upgrade scripts execute `curl`, `tar`, `sha256sum`, `jq`, `awk`, `sed`, `sort`, `grep`, `head`, `mkdir`, `mktemp`, `chmod`, `openssl`, `ssh-keygen`, `systemctl`, and the downloaded `netsgo` binary after signature/checksum/version validation (`scripts/install.sh:39-43`, `scripts/install.sh:262-328`, `scripts/install.sh:331-339`, `scripts/upgrade.sh:611-617`).

## Trust boundaries

- Internet/browser/user -> management HTTP API: public login/passkey/MFA endpoints, then cookie/bearer-session protected admin APIs. Boundary enforced by Host dispatch, session JWT+server session validation, SameSite cookie, CSP/security headers, and login rate limiting.
- Internet/client agents -> `/ws/control` and `/ws/data`: clients authenticate by API key exchange or persisted token + install ID; data channel additionally requires per-session random data token. Origin checks only constrain browser-originated WS; non-browser clients can omit Origin.
- Admin/API user -> tunnel configuration -> server/client network exposure: authenticated admin can create server public listeners, HTTP host routes, client-local target dials, and client-side ingress listeners. This intentionally crosses from management plane into network data plane.
- Server -> client host: after client trusts and authenticates to server, server control messages determine local target host/port dials and client ingress listeners (`internal/client/client.go:1156-1262`, `internal/client/unified_tunnel.go:284-422`). A compromised server/admin controls client network reachability within what client process permissions allow.
- External public IP/release services -> status/update UI: public IP providers influence displayed public IP fields; release-index providers influence authenticated version-check recommendations. Install/upgrade scripts add checksum/signature verification before executing downloaded binaries.
- Local privileged boundary: install/manage/upgrade cross from invoking user to root/systemd and mutate `/usr/local/bin`, `/etc/systemd/system`, `/etc/netsgo/services`, `/var/lib/netsgo`.
- Storage boundary: SQLite DB files and env files hold credential material; file modes are intended to restrict local users, but root/system user compromise exposes raw client token, JWT secret, TOTP secret, and TLS keys.
- Dev boundary: dev compose uses predictable admin password default `zsio-netsgo` and plain HTTP (`docker-compose.dev.yml:24-37`); Vite dev proxy binds all interfaces (`web/vite.config.ts:27-44`). This is local/dev surface, not production default.

## Confirmed findings / concrete risk notes

1. **Plain-HTTP default for direct server and client examples.** Server default `--tls-mode` is empty until configured, and docs/examples show default no TLS; server warns when TLS mode is off without trusted proxies (`cmd/netsgo/cmd_server.go:116-139`, `cmd/netsgo/cmd_server.go:171-194`, `internal/server/server_bootstrap.go:160-164`). Client default server is `http://localhost:9527` and accepts `ws://`/`http://` (`cmd/netsgo/cmd_client.go:37-52`, `cmd/netsgo/cmd_client.go:123-130`). Preconditions: deployment not behind TLS or uses `--tls-skip-verify`. Impact: admin cookies/tokens, client API key exchange, and tunnel control metadata are exposed to network attackers on the path.

2. **Authenticated admin can turn clients into internal network pivots by design.** Server-provisioned target tunnels cause clients to `net.DialTimeout("tcp", LocalIP:LocalPort)` or `net.Dial("udp", LocalIP:LocalPort)` (`internal/client/client.go:953-962`, `internal/client/udp_handler.go:23-32`); client-to-client ingress can also bind ports on an agent host (`internal/client/unified_tunnel.go:373-422`). Preconditions: attacker has admin session/API capability or compromises server/control channel. Impact: reachability into client-local/private networks and opening listeners on client hosts.

3. **Server-exposed TCP/UDP listeners bind all interfaces.** Server tunnel activation uses `:%d` for TCP and UDP (`internal/server/proxy.go:296-328`, `internal/server/udp_proxy.go:194-213`). Preconditions: authenticated admin creates/resumes a server-expose tunnel and firewall allows the port. Impact: target client service becomes externally reachable on every server interface; this is expected tunnel behavior but must be explicit in threat model and UI.

4. **Host-based HTTP tunnel dispatch shares the management listener.** Host dispatch prioritizes NetsGo WS subprotocols, then host-matched HTTP tunnel routes, then management host (`internal/server/http_tunnel_proxy.go:79-117`). Preconditions: Host header/domain configured for HTTP tunnel, reverse proxies preserve or alter Host. Impact: misconfigured `server_addr`, `trusted-proxies`, or dev `changeOrigin` could route requests unexpectedly between management and tunnel planes. Tests cover subprotocol discrimination and Host handling, but deployment docs should keep this boundary prominent.

5. **Release/upgrade scripts execute a downloaded binary after verification.** Upgrade script fetches release metadata, checks signed checksums, extracts archive, verifies version, then runs the downloaded `netsgo upgrade` (`scripts/upgrade.sh:569-617`). Preconditions: user runs one-line script or update command, release signing key remains trusted, script environment not tampered with. Impact: deliberate privileged code execution path; signature/checksum controls are present but this is still a high-value supply-chain boundary.

## Non-findings observed

- No unauthenticated management API routes besides login/MFA/passkey begin/finish and static UI were registered in `registerManagementRoutes`; admin/config/tunnel/status/events/version routes are wrapped with `RequireAuth` (`internal/server/server_http.go:25-74`).
- `/ws/data` rejects non-WebSocket HTTP requests with 426 and requires binary handshake plus matching per-session data token before yamux (`internal/server/data.go:21-84`, `internal/server/data.go:100-120`).
- WebSocket control/data origin check rejects mismatched browser `Origin` but allows absent Origin for non-browser agents (`internal/server/control_auth.go:23-47`). This is expected for CLI clients.
- Client data streams validate tunnel/revision/role/direction/transport against cached proxy config before dialing local services (`internal/client/client.go:932-987`).
- SQLite DB and auto TLS key files are created/chmodded private (`0600`); managed data dirs are `0750` (`internal/storage/sqlite.go:26-63`, `internal/storage/sqlite.go:140-183`, `internal/server/tls.go:156-164`, `internal/install/dirs.go:23-65`).
- API keys are not persisted raw; client tokens are hashed server-side and raw only in client state (`internal/server/admin_api.go:192-197`, `internal/server/admin_store.go:1719-1732`, `internal/client/state_store.go:38-96`).
- Install/upgrade scripts allowlist official GitHub/CNB release URLs and require signed checksums before archive extraction (`scripts/install.sh:168-223`, `scripts/install.sh:292-328`, `scripts/install.sh:383-429`).

## Risky assumptions and evidence-backed open questions for Main

- **CSRF posture depends on SameSite Strict plus JSON-only API, not explicit CSRF tokens.** Cookie is SameSite Strict and path `/api` (`internal/server/auth_middleware.go:175-184`), and frontend uses same-origin credentials (`web/src/lib/api.ts:86-90`). Follow-up: confirm intended browser support and reverse-proxy/domain topology do not weaken SameSite assumptions, especially if management UI and tunnel domains share parent domains.
- **`isHTTPSRequest` is security-sensitive behind reverse proxies.** Cookie `Secure` and HSTS depend on `s.isHTTPSRequest(r)` (`internal/server/auth_middleware.go:181-183`, `internal/server/server_http.go:90-92`). Follow-up: inspect `isHTTPSRequest` and trusted-proxy handling in more detail if not already covered by backend-auth audit; wrong proxy config can create non-Secure cookies in externally HTTPS deployments.
- **No per-client local-address allowlist seen in client dial path.** Client trusts server-sent target host/port after data-stream header validation (`internal/client/client.go:937-987`, `internal/client/udp_handler.go:23-32`). Follow-up: decide whether this is product intent or whether clients need local target allow/deny policy (loopback, metadata IPs, RFC1918, etc.).
- **Public IP probes disclose runtime IPs to third-party services.** Code contacts multiple external IP services from both server and client (`pkg/netutil/netutil.go:11-24`, `internal/server/console_api.go:300-322`, `internal/client/client.go:1086-1135`). Follow-up: confirm privacy policy/opt-out requirements and whether probes should be configurable.
- **HTTP tunnel reverse proxy has no response-header timeout.** `ResponseHeaderTimeout: 0` in tunnel transport (`internal/server/http_tunnel_proxy.go:257-276`). Follow-up: assess DoS risk from slow/unresponsive client-local targets and whether surrounding connection/session limits are sufficient.
- **TOCTOU remains between preflight port probe and real bind.** Client preflight binds then closes (`internal/client/unified_tunnel.go:256-275`) and server validation similarly probes server ingress ports (`internal/server/unified_tunnel_api.go:977-986`). Follow-up: confirm resource locks and activation error handling are adequate for concurrent local processes; docs already mention this pattern in review notes.
- **Dev compose exposes predictable credentials and all-interface dev UI.** Defaults are `admin` / `zsio-netsgo`, plain HTTP, Vite `0.0.0.0` (`docker-compose.dev.yml:24-39`, `web/vite.config.ts:27-44`). Follow-up: ensure this is clearly dev-only and not copied to production examples.
- **Installer env file contains raw client registration key at rest.** `NETSGO_KEY` is written to client env (`internal/svcmgr/env.go:57-71`) and env file mode is `0640` (`internal/svcmgr/env.go:139-142`). Client later exchanges key for token, but the key may remain in service env. Follow-up: verify whether server-side API keys are intended long-lived after client registration and whether installer should omit or rotate/remove `NETSGO_KEY` after token persistence.
- **Release script temp/cache directories may be attacker-influenced via `TMPDIR`/`NETSGO_UPDATE_CACHE_DIR`.** Paths are configurable and archives are extracted after signature/checksum/version validation (`scripts/upgrade.sh:341-349`, `scripts/upgrade.sh:407-429`, `scripts/upgrade.sh:609-617`). Follow-up: review symlink/path ownership hardening for root-run one-line install/upgrade scripts.
