# Tunnel/control/data plane security audit

## Scope

Reviewed the requested tunnel/control/data plane areas: `pkg/protocol`, `pkg/mux`, `internal/server/server.go`, `server_http.go`, WebSocket control/data handlers, tunnel runtime/registry/reconcile paths, and internal client probe/UDP/tunnel stream handling. No source code was modified. No project-wide build/test/lint/format/security scanner was run.

## Files inspected

- `pkg/protocol/message.go`, `pkg/protocol/data_channel.go`, `pkg/protocol/stream_header.go`
- `pkg/mux/mux.go`, `pkg/mux/udp_frame.go`, `pkg/mux/wsconn.go`
- `internal/server/server.go`, `server_http.go`, `control_auth.go`, `control_loop.go`, `data.go`, `session.go`, `session_manager.go`
- `internal/server/proxy.go`, `udp_proxy.go`, `http_tunnel_proxy.go`, `http_tunnel.go`, `public_endpoints.go`
- `internal/server/unified_tunnel_api.go`, `tunnel_preflight.go`, `tunnel_registry.go`, `unified_tunnel_runtime.go`, `unified_tunnel_reconcile.go`, `server_expose_unified.go`, `client_relay.go`
- `internal/server/admin_store.go` for client API-key/token authentication and port allowlist semantics
- `internal/client/client.go`, `unified_tunnel.go`, `udp_handler.go`, `probe.go`

## Confirmed findings

### 1. Server-side TCP/UDP tunnel ingress ignores configured bind IP and always exposes on all interfaces

**Severity:** High when administrators believe a tunnel is loopback- or interface-scoped.

**Evidence:**

- Unified tunnel creation accepts a server/client listen `bind_ip` in endpoint config and stores it in the ingress endpoint: `internal/server/unified_tunnel_api.go:126-129` defines `tcpListenConfigAPI`, `:522-552` decodes and validates `BindIP`, and `:754-759` stores `normalizedIngressConfigRaw(req.Ingress.Type, ingressConfig)`.
- For server-expose tunnels, the same request is converted into legacy `ProxyNewRequest` with only `RemotePort` set from the ingress port; `BindIP` is not carried into `ProxyNewRequest`: `internal/server/unified_tunnel_api.go:767-775` sets `LocalIP`, `LocalPort`, `RemotePort`, and `Domain`, but no server bind address.
- Server TCP runtime binds `fmt.Sprintf(":%d", tunnel.Config.RemotePort)` and calls `net.Listen("tcp", addr)`, which listens on all interfaces: `internal/server/proxy.go:296-297`.
- Server UDP runtime similarly binds `fmt.Sprintf(":%d", tunnel.Config.RemotePort)` and calls `net.ListenPacket("udp", addr)`: `internal/server/udp_proxy.go:195-198`.
- Server preflight also checks all-interface availability rather than the configured bind IP: `internal/server/unified_tunnel_api.go:976-989` builds `addr := fmt.Sprintf(":%d", cfg.Port)` and probes TCP/UDP on that address.

**Exploit/preconditions:** An authenticated admin creates or updates a `server_expose` TCP/UDP tunnel with an ingress config such as `{"bind_ip":"127.0.0.1","port":NNNN}` (or another intended interface-local address). The server exposes `:NNNN` on every interface instead of the requested address. Any host that can reach the server's public interface can connect to the tunnel.

**Impact:** Unexpected public exposure of internal client services. This is especially risky for tunnels intended for local-only testing or private interface binding.

**Notes:** Client-side client-to-client ingress does honor bind IP via `net.JoinHostPort(cfg.BindIP, ...)` in `internal/client/unified_tunnel.go:384-401`, so the bug is specific to server-side TCP/UDP ingress.

### 2. Client-side tunnel target configuration enables admin-driven SSRF / local network pivot from the client host

**Severity:** High in multi-tenant/admin-compromise scenarios; expected but security-sensitive in single-admin remote access deployments.

**Evidence:**

- Unified target service config accepts arbitrary `host`/`ip` plus port, defaults empty host to `127.0.0.1`, and performs no denylist/allowlist for loopback, link-local, RFC1918, metadata IPs, or DNS rebinding-sensitive hostnames: `internal/server/unified_tunnel_api.go:558-581`.
- Stored tunnel target is copied directly into `ProxyNewRequest.LocalIP`/`LocalPort`: `internal/server/unified_tunnel_api.go:760-775`.
- On provisioning, the client builds a local proxy request directly from `spec.Target.Config`: `internal/client/unified_tunnel.go:736-766`.
- For TCP/HTTP tunnel traffic, the client dials the configured target with `net.DialTimeout("tcp", localAddr, 5*time.Second)`: `internal/client/client.go:953-962`.
- For UDP tunnel traffic, the client dials the configured UDP service with `net.Dial("udp", localAddr)`: `internal/client/udp_handler.go:23-32`.

**Exploit/preconditions:** An authenticated administrator, or anyone who can exercise the admin tunnel API, creates a target pointing at sensitive addresses from the client's network perspective, e.g. `127.0.0.1:<daemon>`, `169.254.169.254:80`, a Kubernetes service IP, or an internal RFC1918 host reachable only from the client. The server then exposes or relays traffic to that target.

**Impact:** The tunnel system can be used as an SSRF/local-network pivot through any connected client. Depending on deployment, this may be core product behavior, but the code has no policy boundary that prevents exposing loopback, cloud metadata, or private network services accidentally or maliciously.

**Risky assumption:** If NetsGo is intentionally a remote access/tunneling tool where admins are fully trusted to reach arbitrary client-local services, this is a documented trust boundary rather than a vulnerability. The current code does not enforce that boundary in code.

### 3. Control WebSocket upgrades are not explicitly method/subprotocol-gated before authentication

**Severity:** Medium defense-in-depth issue.

**Evidence:**

- Internal WS routes are registered at `/ws/control` and `/ws/data`: `internal/server/server_http.go:76-79`. The host dispatcher also routes requests matching `isNetsgoControlRequest`/`isNetsgoDataRequest` to these handlers before management-host checks: `internal/server/http_tunnel_proxy.go:79-88`.
- Data handler checks `websocket.IsWebSocketUpgrade(r)` before upgrading and requires a binary first frame: `internal/server/data.go:21-30`, `:44-65`.
- Control handler calls `controlUpgrader.Upgrade(w, r, nil)` directly without an explicit `websocket.IsWebSocketUpgrade` check, HTTP method check, or post-upgrade subprotocol verification: `internal/server/control_auth.go:55-57`.
- The upgrader advertises `Subprotocols: []string{protocol.WSSubProtocolControl}` but the handler does not reject clients that omit or negotiate no subprotocol: `internal/server/control_auth.go:35-38`.
- Client does request the subprotocol (`internal/client/client.go:538-540`), but server acceptance does not depend on it.

**Exploit/preconditions:** Network attacker or unauthenticated client can attempt WebSocket upgrades to `/ws/control` from any host routed to the service. Authentication is still required after upgrade, so this does not bypass client auth by itself.

**Impact:** Expands the unauthenticated parsing surface and makes protocol confusion harder to rule out. A strict handler should fail before upgrade unless method/upgrade/subprotocol are exactly expected, reducing resource use and cross-protocol ambiguity.

### 4. Unauthenticated control WebSocket attempts can perform expensive bcrypt work against every configured API key while holding the auth lock

**Severity:** Medium DoS risk; higher with many API keys or low-rate distributed attempts.

**Evidence:**

- Control auth accepts a JSON auth message after upgrade and then either validates a token or exchanges an API key: `internal/server/control_auth.go:121-140`, `:176-245`.
- Failed key exchange records limiter failure only after `RegisterClientAndExchangeToken` returns: `internal/server/control_auth.go:234-245`.
- `RegisterClientAndExchangeToken` takes the store mutex for the whole operation: `internal/server/admin_store.go:1678-1684` and validates the raw key under that lock: `:1691-1694`.
- `validateClientKeyLocked` loads all API keys and compares the supplied key against each bcrypt hash in a loop: `internal/server/admin_store.go:1613-1648`.
- Rate limiting is per client IP with 20 requests/minute and 10 failures before lockout: `internal/server/auth_service.go:33-38`, `internal/server/control_auth.go:99-110`.

**Exploit/preconditions:** Attacker can connect to `/ws/control` and send invalid API keys. With many API keys configured, each attempt may execute many bcrypt comparisons while `AdminStore.mu` is held. Distributed sources can avoid per-IP lockout.

**Impact:** CPU exhaustion and lock contention for authentication/store operations. This does not expose data but can degrade or deny client reconnects and admin operations.

### 5. Per-connection and per-stream goroutines are unbounded for TCP/HTTP/yamux streams

**Severity:** Medium resource exhaustion risk.

**Evidence:**

- Server TCP accept loop starts a goroutine per external TCP connection with no concurrency limit: `internal/server/proxy.go:424-443`.
- Each external TCP connection opens a yamux stream to the client: `internal/server/proxy.go:448-464` and `internal/server/data.go:221-267`.
- HTTP tunneling creates a new `httputil.ReverseProxy` transport and opens a stream via `DialContext` per request; no request/body/header timeout or in-flight limit is set (`ResponseHeaderTimeout: 0`): `internal/server/http_tunnel_proxy.go:257-276`.
- Client `acceptStreamLoopRuntime` starts a goroutine for every accepted yamux stream with no stream count cap: `internal/client/client.go:905-920`.
- Server also accepts client-opened data streams and spawns a goroutine per stream: `internal/server/data.go:184-201`.
- Yamux config only customizes keepalive/window size; no max-stream or accept backlog limit is configured in `pkg/mux/mux.go:20-42`.

**Exploit/preconditions:** An external attacker can reach an exposed TCP/HTTP tunnel or a malicious/compromised client can open many yamux streams. They hold connections open slowly or indefinitely.

**Impact:** Goroutine, file descriptor, memory, and yamux stream exhaustion on server and/or client. UDP has explicit 4096-session caps (`internal/server/udp_proxy.go:165-170`, `internal/client/unified_tunnel.go:78-82`), but TCP/HTTP/yamux paths do not show equivalent caps.

### 6. API key `max_uses` is bypassable by reusing an existing unexpired token for the same install ID

**Severity:** Low/Medium depending on how one-time client keys are intended to behave.

**Evidence:**

- API keys support a `MaxUses`/`UseCount` field and reject use after `UseCount >= MaxUses`: `internal/server/admin_models.go:17-19`, `internal/server/admin_store.go:1641-1643`.
- `exchangeTokenInTx` first looks for an unrevoked, unexpired token for the same `installID`; if found, it refreshes and returns a new token without incrementing API key use count: `internal/server/admin_store.go:1820-1841`.
- Only when no valid token exists does it find the raw key and increment `api_keys.use_count`: `internal/server/admin_store.go:1844-1859`.

**Exploit/preconditions:** A client (or attacker with the API key and the same install ID) has already exchanged a max-use key once and still has an unexpired token row. Re-supplying the same API key and install ID can refresh a token without consuming another use.

**Impact:** `max_uses=1` behaves more like “one registered install ID may keep refreshing” than “one exchange total.” This may be intended for reconnect ergonomics, but it weakens API-key use limits if operators expect strict one-time bootstrap credentials.

## Non-findings / controls observed

- **Client API keys are not stored in plaintext.** New API keys are bcrypt-hashed before persistence: `internal/server/admin_store.go:2111-2125`. Admin API responses sanitize key hashes and only return the raw key on creation: `internal/server/admin_api.go:22-31`, `:193-196`.
- **Long-lived client tokens are random and hashed at rest.** Tokens are 32 random bytes hex-encoded with `tk-` prefix (`internal/server/admin_store.go:1725-1731`) and stored as SHA-256 hashes (`:1719-1723`, `:1862-1879`). Validation uses constant-time comparison of hashes and checks revocation, install ID, and inactivity expiry: `:1887-1938`.
- **Data WebSocket is bound to the current authenticated control session via per-session data token and generation.** Server creates a random data token in auth response (`internal/server/control_auth.go:262-284`), data handshake requires binary `{clientID,dataToken}` (`internal/server/data.go:44-65`), validates against the current client record with constant-time compare (`:67-84`), rejects closing/stale generation (`:85-98`), and closes old sessions when replacing data session (`:127-134`).
- **Control/data WebSocket origins are constrained to same host when an Origin header is present.** `checkWSOrigin` allows empty origin, parses non-empty origin, and requires `u.Host == r.Host`: `internal/server/control_auth.go:23-33`; both control and data upgraders use it (`:35-47`). This is acceptable for non-browser agents; browser CSWSH protection relies on same-host origin matching.
- **HTTP tunnel domains are validated as FQDNs, not schemes/paths/IPs/wildcards.** `validateDomain` rejects whitespace, wildcards, schemes, paths/queries, ports/IPv6 literals, IP addresses, single-label names, overlong labels, and non-ASCII: `internal/server/http_tunnel.go:103-162`.
- **Management host dispatch avoids serving the admin UI on arbitrary Host headers outside dev/explicit loopback mode.** Host dispatch routes tunnel WS and HTTP routes first, otherwise only serves management if `isManagementHost` passes: `internal/server/http_tunnel_proxy.go:79-116`; loopback host relaxation is gated by `AllowLoopbackManagementHost`: `:165-169`.
- **Tunnel mutation fields are mostly server-owned.** Unified create rejects submitted `id`, `revision`, and `owner_client_id`: `internal/server/unified_tunnel_api.go:695-704`, derives owner from target client (`:446-460`, `:717-720`), and requires optimistic revision on update (`:347-356`).
- **Client-to-client control/data binding checks current live sessions and exact tunnel role/revision.** Provision ACK waiters include client ID, generation, tunnel name/ID, revision, and role: `internal/server/tunnel_registry.go:77-95`, `:112-137`; runtime reports are checked against stored tunnel role/client and revision: `internal/server/control_loop.go:227-263`, `:266-277`; client-opened relay streams validate revision, open client, role, direction, and transport: `internal/server/client_relay.go:318-337`, `:420-436`.
- **Data stream framing has structural size limits.** Data handshake limits client ID/token lengths and total payload: `pkg/protocol/data_channel.go:13-16`, `:33-63`. Data stream headers have a 16 KiB max, string/token field limits, UTF-8 check, unknown-field rejection, required roles/direction/transport, and authorization-token-or-server-authorized invariant: `pkg/protocol/stream_header.go:14-20`, `:78-115`, `:127-200`.
- **UDP datagram/session paths have payload and association caps.** UDP frame max payload is 65507 bytes: `pkg/mux/udp_frame.go:14-22`, `:35-48`. Server UDP proxy caps sessions at 4096 and reaps idle sessions: `internal/server/udp_proxy.go:165-170`, `:306-323`, `:422-443`. Client UDP ingress caps associations at 4096 and reaps idle associations: `internal/client/unified_tunnel.go:78-82`, `:493-513`, `:548-552`.
- **Online state for tunnel activation is tied to live client state and data session presence, not only stored last-seen.** `loadLiveClient` requires `clientStateLive`: `internal/server/session.go:99-108`; server-expose reconcile requires live client plus data session: `internal/server/unified_tunnel_reconcile.go:194-203`; client relay reconcile requires both participants online and both data sessions present: `internal/server/client_relay.go:88-110`.
- **Client-side data stream target authorization checks the provisioned tunnel config.** The client looks up stream headers by tunnel ID/config (`internal/client/client.go:990-1020`) and rejects mismatched revision, target/source roles, direction, transport, direct-only policy, and unexpected actual transport: `:932-945`, `:965-988`.

## Risky assumptions

- Admin users appear to be fully trusted to choose client target hosts/ports. If admins are not fully trusted, the arbitrary target-host behavior is a serious privilege boundary failure.
- Empty `Origin` is allowed for WebSocket clients (`internal/server/control_auth.go:23-27`). This is normal for non-browser clients, but any browser-accessible credential material would need separate CSWSH analysis.
- `ws://` client connections are supported by design (`internal/client/client.go:243-291`). API keys/client tokens are protected only by transport when TLS is enabled; plaintext deployments expose bootstrap keys/tokens to network observers.
- Client-provided probe fields are treated as status/display data. The server stores hostname/IP/public IP/stats from client messages (`internal/server/control_loop.go:81-118`; `internal/server/admin_store.go:1112-1155`), so they should not be used as authoritative security facts without additional validation.

## Follow-up checks for Main

1. Confirm intended semantics for `ingress.config.bind_ip` on server-expose TCP/UDP tunnels. If it is meant to scope exposure, bind `net.Listen`/`ListenPacket` and preflight to that address instead of `:%d`.
2. Decide and document/enforce the target-host trust boundary. If arbitrary client-local access is not intended, add target host/IP allow/deny policy before storing/provisioning tunnels.
3. Add strict control-WS upgrade tests for method, Upgrade header, and required subprotocol, then enforce before/after `Upgrade`.
4. Load-test or unit-test TCP/HTTP/yamux stream exhaustion behavior; consider per-tunnel and per-client in-flight stream/connection limits and HTTP timeouts.
5. Clarify `max_uses` semantics for API keys. If “one exchange” is intended, token refresh should not bypass use count for the same install ID.
