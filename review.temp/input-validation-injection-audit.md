# Input validation and injection audit

## Scope

Audited input validation and injection sinks across Go, TypeScript, Rust/Tauri, and shell scripts. Focus areas: JSON decoding, path/URL construction, reverse proxy behavior, command execution, dynamic SQL, HTML/DOM injection, log/SSE injection, and template/script generation.

No project-wide build/test/lint/format/security scanner was run.

## Files inspected

- `internal/server/server_http.go`
- `internal/server/admin_api.go`
- `internal/server/admin_security_api.go`
- `internal/server/tunnel_api.go`
- `internal/server/unified_tunnel_api.go`
- `internal/server/store.go`
- `internal/server/traffic_api.go`
- `internal/server/traffic_store.go`
- `internal/server/http_tunnel.go`
- `internal/server/http_tunnel_proxy.go`
- `internal/server/proxy.go`
- `internal/server/rate_limiter.go`
- `internal/server/control_auth.go`
- `internal/server/control_loop.go`
- `internal/server/events.go`
- `internal/clientaddr/address.go`
- `internal/install/client.go`
- `internal/install/install.go`
- `internal/installmethod/systemd_linux.go`
- `web/src/lib/api.ts`
- `web/src/hooks/use-tunnel-mutations.ts`
- `web/src/components/custom/client/AddClientDialog.tsx`
- `web/src/components/custom/client/ShikiCodeBlock.tsx`
- `web/src/components/ui/chart.tsx`
- `desktop/src-tauri/src/lib.rs`
- `scripts/common-update.sh`
- `scripts/install.sh`
- `scripts/upgrade.sh`
- `scripts/build-desktop-sidecar.sh`
- `scripts/package-macos-dmg.sh`
- `scripts/sign-macos-app.sh`
- `scripts/validate-beta-increment.sh`
- `test/e2e/scripts/bootstrap.sh`
- `test/e2e/scripts/create-client-key.sh`
- `test/e2e/scripts/run-client.sh`

## Confirmed findings

### 1. Admin and tunnel JSON request bodies have no size cap or strict trailing-token rejection

Evidence:
- Public management route registration exposes body-decoding endpoints for login/auth/admin/tunnel mutation paths: `internal/server/server_http.go:49-72`, `server_http.go:36-46`, and `server_http.go:54-71`.
- These handlers directly decode from `r.Body` with `json.NewDecoder(r.Body).Decode(...)` and do not wrap `r.Body` in `http.MaxBytesReader`: login at `internal/server/admin_api.go:71-74`, admin key creation at `admin_api.go:139-148`, admin config update at `admin_api.go:278-283`, MFA verify at `internal/server/admin_security_api.go:51-56`, passkey finish at `admin_security_api.go:169-173`, username/password/TOTP/passkey handlers at `admin_security_api.go:286-318`, `345-380`, `409-439`, `487-505`, `537-601`, legacy tunnel create/update at `internal/server/tunnel_api.go:203-210` and `tunnel_api.go:380-396`, and unified tunnel create/update at `internal/server/unified_tunnel_api.go:314-319` and `unified_tunnel_api.go:342-346`.
- The unified endpoint *nested* config is capped and strict only after the outer request has already been decoded: `decodeStrictEndpointConfig` rejects unknown fields and multiple config objects at `internal/server/unified_tunnel_api.go:584-600`, and `validateEndpointConfigComplexity` enforces config byte/depth limits at `unified_tunnel_api.go:603-630`. This does not cap or strictly validate the outer request body.
- No `MaxBytesReader`, `LimitReader`, or body-size guard appears in `internal/server` handlers; the only matching strictness is the nested endpoint config decoder (`internal/server/unified_tunnel_api.go:591-592`).

Exploit preconditions:
- Attacker can reach a JSON endpoint. For login/passkey begin/finish/MFA verify this is unauthenticated route surface; for admin/tunnel mutations the attacker needs a valid admin session/API token or a CSRF/XSS route to cause an authenticated browser to send the request.

Impact:
- Large JSON bodies can force server-side read/parse allocation and CPU work before authentication-specific rejection completes. Duplicate/trailing JSON values can also be accepted according to Go decoder single-`Decode` behavior, which is parser differential risk for security-significant mutations because the server does not verify EOF after the first object.

### 2. Client-supplied identity strings reach line-oriented logs without newline/control-character sanitization

Evidence:
- Client hostnames are taken from the connecting client during auth without character normalization. The client populates `protocol.ClientInfo.Hostname` from `os.Hostname()` at `internal/client/client.go:606-620`, while tests explicitly allow even an empty hostname to authenticate (`internal/server/server_test.go:1228-1236`) and protocol tests preserve Unicode hostnames (`pkg/protocol/message_test.go:364-385`). No inspected server-side hostname validator rejects `\r`, `\n`, or other control characters before persistence/logging.
- On successful auth, the server logs the client-supplied hostname directly with `%s`: `internal/server/control_auth.go:80-85`.
- Periodic stats logs also interpolate the client info hostname directly: `internal/server/control_loop.go:97-113`.
- Disconnect logs interpolate `info.Hostname` directly: `internal/server/session.go:179-182`.
- Tunnel/proxy names and server-provided IDs are likewise logged with `%s` in several runtime paths (for example client-side proxy receipt at `internal/client/client.go:1191-1201`, server proxy creation at `internal/server/proxy.go:288-325`, and proxy stop/reopen at `proxy.go:492-547`). Some tunnel names are admin-controlled, but client hostnames are supplied by the client process over the control channel.

Exploit preconditions:
- Attacker has any valid client API key/token or compromises a registered client enough to authenticate with a crafted hostname containing CR/LF or terminal control sequences.

Impact:
- Log injection/forgery in plaintext service logs: forged entries, misleading severities, terminal escape/control effects, and corrupted log ingestion if downstream collectors assume one event per line. This does not by itself execute code, but it can hide activity or poison incident triage.

### 3. Desktop sidecar invocation accepts renderer-supplied data-dir as a privileged sidecar argument

Evidence:
- The Tauri command request structure accepts `data_dir` directly from the renderer: `desktop/src-tauri/src/lib.rs:144-151`.
- `start_client_sidecar` validates only that `server` is non-empty at `desktop/src-tauri/src/lib.rs:418-428`; it does not constrain `request.data_dir` to the app-local data directory, reject relative paths, or reject symlink-sensitive locations.
- The sidecar is spawned without a shell, which avoids shell metacharacter injection, but the user-controlled path is passed as a process argument: args are built with `--data-dir`, `request.data_dir.clone()`, `--log-format`, `json` at `desktop/src-tauri/src/lib.rs:449-455`; `app.shell().command(sidecar_path).args(args)` spawns it at `lib.rs:457-465`.
- The same file contains a constrained app-local deletion helper for client state (`clear_client_state_dir` only touches `app_local_data_dir()/client` and `app_local_data_dir()/locks/client.lock`) at `desktop/src-tauri/src/lib.rs:295-319`, showing a safer path-root pattern is already used elsewhere.

Exploit preconditions:
- Attacker can execute JavaScript in the desktop renderer context, abuse an exposed frontend flow that calls `start_client_sidecar`, or otherwise invoke Tauri commands from the local app renderer. This is not remote shell injection because no shell is used.

Impact:
- Arbitrary sidecar state path selection by renderer-controlled input. Depending on sidecar file behavior and app privileges, this can redirect token/state/database writes to attacker-chosen filesystem locations, clobber existing files in writable locations, or persist secrets outside the intended app data root. It also broadens the blast radius of any renderer compromise.

## Non-findings / positive controls

### Reverse proxy host routing avoids management-plane fallback on arbitrary Host headers

Evidence:
- Host dispatch first checks internal WebSocket subprotocol routes, then exact HTTP tunnel host matches, then management host detection; unknown hosts get `404`: `internal/server/http_tunnel_proxy.go:79-116`.
- HTTP tunnel lookup canonicalizes the Host header and only matches live, serviceable HTTP tunnels: `internal/server/http_tunnel_proxy.go:197-238`.
- Management host detection compares against `effectiveManagementHost` and only allows loopback fallback when configured/defaulted as loopback; non-management unknown hosts are rejected unless dev mode is enabled: `internal/server/http_tunnel_proxy.go:119-170`.
- Tests cover this behavior: unknown hosts do not fall back to admin plane (`internal/server/http_dispatch_test.go:549-552`), deleted API paths do not fall back to the frontend (`http_dispatch_test.go:382-386`), and business domains route to the backend before admin API (`http_dispatch_test.go:270-274`, `571-574`).

### HTTP tunnel domain validation rejects common host-header/path injection forms

Evidence:
- `validateDomain` rejects empty input, leading/trailing whitespace or embedded whitespace, wildcards, schemes, paths/query/fragments, ports/IPv6 literals, IP addresses, single-label hosts, overlong labels, and non-ASCII non-punycode characters at `internal/server/http_tunnel.go:103-162`.
- Legacy HTTP tunnel creation calls `validateDomain` and `checkDomainConflict` before accepting an HTTP tunnel at `internal/server/proxy.go:82-89`.
- Unified `http_host` ingress trims and validates the domain before storing it at `internal/server/unified_tunnel_api.go:522-533`.
- The dedicated test table covers invalid scheme/path/IP/IPv6/Unicode/length/wildcard cases and valid subdomain/punycode/trailing-dot cases at `internal/server/http_tunnel_test.go:61-91`.

### Forwarded header handling does not blindly trust spoofed proxy headers for security decisions

Evidence:
- `trustProxyHeaders` trusts proxy headers only from loopback or explicitly configured trusted proxies at `internal/server/rate_limiter.go:210-215`.
- `clientIP` uses X-Forwarded-For / X-Real-IP only under that trust decision; otherwise it falls back to `RemoteAddr` at `internal/server/rate_limiter.go:186-207`.
- `isHTTPSRequest` similarly requires TLS or trusted proxy headers before honoring forwarded proto at `internal/server/rate_limiter.go:217-233`.
- HTTP tunnel forwarding recomputes `X-Forwarded-Host`, `X-Forwarded-Proto`, and `X-Forwarded-For` via `computeForwardedHeaders`; untrusted inbound `X-Forwarded-For` is overwritten with the direct client IP (`internal/server/http_tunnel.go:376-404`). Tests cover appending for trusted proxies and ignoring untrusted spoofing (`internal/server/http_tunnel_test.go:535-587`).

### Reverse proxy uses Go `httputil.ReverseProxy.Rewrite` and a fixed synthetic target, not user-built upstream URLs

Evidence:
- `proxyHTTPRequest` constructs a fixed target URL `http://netsgo-http-tunnel` at `internal/server/http_tunnel_proxy.go:249-253`.
- The transport `DialContext` ignores caller-controlled network/address arguments and opens a NetsGo data stream to the selected client/tunnel at `internal/server/http_tunnel_proxy.go:257-275`.
- The reverse proxy `Rewrite` calls `pr.SetURL(target)` and sets `pr.Out.Host` from the validated tunnel domain / request host via `computeForwardedHeaders` at `internal/server/http_tunnel_proxy.go:278-286`.

### Dynamic SQL uses static fragments plus placeholders for user data in inspected paths

Evidence:
- Admin user/client store queries concatenate only fixed column lists or fixed `where` fragments from callers, while user inputs are placeholders: examples include `ValidateAdminPassword` querying username at `internal/server/admin_store.go:579-584`, `loadRegisteredClient` taking fixed caller-provided `where` plus variadic args at `admin_store.go:741-743`, and client info writes using placeholders at `admin_store.go:772-810`.
- Tunnel update code builds a query string by appending a fixed revision predicate while appending the revision to `args`; stored fields are all passed as placeholders at `internal/server/store.go:647-651`.
- Traffic queries append only a fixed tunnel filter fragment and bind `clientID`, `resolution`, time bounds, `tunnelName`, and `tunnelID` through placeholders at `internal/server/traffic_store.go:424-436`.
- Admin/security update/delete paths likewise use placeholders for names, IDs, passwords, TOTP secrets, and passkey values (for example `internal/server/admin_security_store.go:147-163`, `203-209`, `293-334`, `607-608`, `682-688`).

### Frontend path and query construction generally encodes dynamic path segments

Evidence:
- The main API helper uses `encodeURIComponent` through `encodePath` for unified tunnel and client path segments: `web/src/lib/api.ts:160-187`.
- Legacy fallback tunnel mutation paths encode `tunnelId` in resume/stop/delete/update calls: `web/src/hooks/use-tunnel-mutations.ts:109-148` and `use-tunnel-mutations.ts:160-174`.
- Client traffic URLs encode `clientId` and use `URLSearchParams` for query parameters at `web/src/hooks/use-client-traffic.ts:53-64`.
- Admin security passkey mutation paths encode passkey IDs at `web/src/hooks/use-admin-security.ts:64-75`.

### Generated client install commands quote shell and YAML values before display/copy

Evidence:
- Shell command values are single-quoted with embedded quote escaping at `web/src/components/custom/client/AddClientDialog.tsx:50-52`.
- YAML environment values are double-quoted with backslash and quote escaping at `web/src/components/custom/client/AddClientDialog.tsx:54-56`.
- The generated `netsgo client`, install pipe, Docker run, and Compose snippets use these helpers for server address and key values at `AddClientDialog.tsx:181-236`.

### TypeScript HTML/DOM injection sinks are limited to controlled library/theme output in inspected files

Evidence:
- `ShikiCodeBlock` uses `dangerouslySetInnerHTML` only with HTML returned by Shiki `codeToHtml(code, { lang, theme })`, where `language` is a closed union of `bash | yaml` and themes are constants; fallback rendering uses React text escaping in `<code>{code}</code>` at `web/src/components/custom/client/ShikiCodeBlock.tsx:5-18`, `77-109`, and `156-161`.
- The chart component `dangerouslySetInnerHTML` builds CSS from static `THEMES` entries, not user input, at `web/src/components/ui/chart.tsx:92-100`.
- Clipboard fallback creates a `textarea` and assigns `.value`, not `.innerHTML`, before `document.execCommand('copy')`: `web/src/components/custom/client/AddClientDialog.tsx:58-75` and `web/src/components/custom/common/CopyButton.tsx:22-30`.

### Shell install/update scripts avoid shell eval of release metadata and enforce official URLs plus signatures/checksums

Evidence:
- Release provider selection is restricted to `auto|cnb|github` and official hard-coded latest URLs at `scripts/common-update.sh:46-60`.
- Downloaded release-detail asset URLs are allowed only under official GitHub/CNB prefixes by `official_url_allowed` / `download_official` at `scripts/common-update.sh:167-185`.
- Release tags are regex-validated by `valid_release_tag` at `scripts/common-update.sh:105-107`, and install rejects invalid index versions at `scripts/install.sh:579-583`.
- Release details are structurally checked with `jq` for schema/project/version/signature/checksum/asset fields at `scripts/common-update.sh:224-239`.
- Checksums and signatures are verified before archive extraction: `verify_checksum` / `checksum_matches` at `scripts/common-update.sh:242-259`, Ed25519/OpenSSH signature verification at `common-update.sh:261-309`, and install flow at `scripts/install.sh:591-594`.
- Archive extraction targets a fixed `netsgo` member into a temporary destination and does not execute archive-controlled filenames as commands: `scripts/common-update.sh:330-337`.

### Command execution in inspected Go/Rust paths does not use a shell

Evidence:
- Go journal/systemctl helpers use `exec.Command` with argument arrays, not `sh -c`: `internal/install/client.go:21-24` and `internal/installmethod/systemd_linux.go:13-25`.
- The sudo re-exec path resolves `sudo` and calls an exec dependency with `append([]string{"sudo"}, os.Args...)`; arguments are not shell-expanded at `internal/install/install.go:88-97`.
- Tauri sidecar startup passes a resolved sidecar path and `args` vector to `app.shell().command(...).args(args)` at `desktop/src-tauri/src/lib.rs:449-465`; the key is passed via environment at `lib.rs:459-461`.

## Risky assumptions

1. Client hostnames and tunnel names are treated as presentation/log fields, but several components assume they are safe for line-oriented logs. If any client can register with arbitrary hostname bytes, log injection is practical even though JSON API responses and React rendering escape them.
2. Renderer-originated Tauri commands are assumed trusted. If the desktop renderer can ever load remote content or suffer XSS, `start_client_sidecar` exposes filesystem-path influence through `data_dir`.
3. The lack of `MaxBytesReader` may be partly mitigated by upstream reverse proxies, but the Go server does not enforce its own JSON body limits in the inspected handlers.
4. Shiki output is assumed safe for `dangerouslySetInnerHTML`; this depends on Shiki continuing to escape source code correctly and no untrusted custom language/theme injection being introduced.
5. Shell update scripts rely on official release-index JSON and signature verification. The URL allowlist prevents arbitrary hosts, but CDN/account compromise of the official prefixes remains in scope for supply-chain review, not this injection pass.

## Follow-up checks for Main

1. Add a focused check or test proving oversized JSON bodies are rejected after introducing body caps; cover unauthenticated `/api/auth/login` and authenticated `/api/tunnels`.
2. Add a unit test for hostname/tunnel-name log sanitization with `\n`, `\r`, and ANSI escape bytes once a sanitizer is added.
3. Decide whether Tauri `data_dir` should be removed from renderer input entirely or constrained to `app_local_data_dir()/client`; then test that absolute/relative traversal paths are rejected.
4. Consider a helper for strict JSON decoding (`MaxBytesReader` + `DisallowUnknownFields` where compatible + EOF check) to avoid per-handler drift.
5. If Shiki remains behind `dangerouslySetInnerHTML`, pin/track the Shiki escaping guarantee and add a component test with code containing `<img onerror=...>` to assert it renders escaped markup.
