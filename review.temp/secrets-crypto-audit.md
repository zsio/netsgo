# Secrets and cryptography security review

## Scope

Reviewed secrets and cryptography paths across Go, TypeScript, Rust, and shell:

- Go server/client auth, session, token, MFA, WebAuthn, TLS, and state persistence.
- TypeScript web auth state, API client, WebAuthn client helpers, and raw client-key handling.
- Rust desktop sidecar launch and log sanitization.
- Shell/Go release checksum signing and install/upgrade verification.

No project-wide build/test/lint/security scanner was run. Source code was not modified.

## Files inspected

- `internal/server/admin_store.go`
- `internal/server/admin_security_store.go`
- `internal/server/admin_security_api.go`
- `internal/server/admin_webauthn.go`
- `internal/server/admin_api.go`
- `internal/server/auth_middleware.go`
- `internal/server/control_auth.go`
- `internal/server/data.go`
- `internal/server/tls.go`
- `internal/client/client.go`
- `internal/client/state.go`
- `internal/client/state_store.go`
- `internal/install/client.go`
- `internal/svcmgr/env.go`
- `internal/svcmgr/env_linux.go`
- `web/src/stores/auth-store.ts`
- `web/src/lib/api.ts`
- `web/src/routes/login.tsx`
- `web/src/lib/webauthn.ts`
- `desktop/src-tauri/src/lib.rs`
- `scripts/common-update.sh`
- `scripts/install.sh`
- `scripts/upgrade.sh`
- `scripts/release-index.mjs`
- `scripts/sign-macos-app.sh`
- `cmd/netsgo-release-sign/main.go`
- `internal/releasesign/sign.go`
- `.github/workflows/release.yml`

## Confirmed findings

### Finding 1: Random UUID / data-token helpers ignore CSPRNG failure and can mint predictable all-zero secrets/IDs

**Severity:** Medium for data-channel token; Low/Medium for identifiers depending on callsite.

**Evidence:**

- `internal/server/admin_store.go:71-77` implements `generateUUID()` with `rand.Read(buf[:])` but discards the error at line 73 before setting UUID version/variant bits.
- `internal/server/control_auth.go:49-52` implements `generateDataToken()` with a 32-byte `rand.Read(buf)` but also discards the error at line 51.
- Security-sensitive consumers of `generateUUID()` include admin user IDs (`internal/server/admin_store.go:313-315`, `630-632`), session IDs (`1365-1373`), auth challenge IDs (`internal/server/admin_security_store.go:467-474`), API key IDs (`internal/server/admin_store.go:2122-2127`), registered client IDs (`795-801`), client token row IDs (`1862-1869`), tunnel IDs (`internal/server/proxy.go:207-208`, `internal/server/tunnel_manager.go:122-123`, `450-451`, `internal/server/unified_tunnel_api.go:750-751`), and preflight request IDs (`internal/server/tunnel_preflight.go:93-95`).
- `generateDataToken()` output is used as the data-channel bearer secret for a pending client connection (`internal/server/control_auth.go:268-269`) and is checked with constant-time compare in `internal/server/data.go:77-79`.

**Exploit preconditions:**

An attacker must be able to trigger or coincide with `crypto/rand.Reader` failure or replacement. On normal Unix-like systems this is rare, but Go APIs require callers to check entropy read failures. In tests or compromised runtime environments, a failing reader would cause zero-filled buffers.

**Impact:**

- `generateDataToken()` would return 64 hex zeroes if CSPRNG reads fail. A network attacker who can open a data WebSocket for a known pending `clientID` could satisfy the data-channel handshake if they know or guess this failure mode.
- `generateUUID()` would repeatedly generate the deterministic UUID `00000000-0000-4000-8000-000000000000` under CSPRNG failure, causing collisions and potentially confusing session/challenge/tunnel ownership.

**Recommended fix:**

Make both helpers return `(string, error)`, propagate failures at all callsites, and fail closed. For UUIDs, use a helper that checks `rand.Read` exactly like `internal/client/state.go:146-151` and `pkg/protocol/stream_header.go:46-49` already do. For `generateDataToken`, return an authentication failure if token generation fails.

### Finding 2: Client TLS bypass is persisted as `InsecureSkipVerify=true`; TOFU reduces but does not remove first-connection MITM risk

**Severity:** Medium.

**Evidence:**

- `internal/install/client.go:172-194` probes HTTPS with normal verification, then interactive install can set `tlsSkipVerify = true` after confirmation if the operator accepts a failed TLS probe.
- That decision is persisted into the service env as `NETSGO_TLS_SKIP_VERIFY` through `internal/install/client.go:246-248` and `internal/svcmgr/env.go:57-71`.
- The client builds TLS configs with `InsecureSkipVerify: c.TLSSkipVerify` in `internal/client/client.go:331-336`, used for WebSocket control/data dialers at `339-347`.
- The client then performs certificate fingerprint TOFU in `internal/client/client.go:836-889`: first connection records the SHA-256 fingerprint when no saved fingerprint exists (`862-873`), subsequent mismatches fail (`877-885`). The fingerprint is persisted via `saveTLSFingerprint` (`internal/client/state.go:154-158`).

**Exploit preconditions:**

A user accepts the interactive TLS-skip prompt, or `NETSGO_TLS_SKIP_VERIFY=true` is otherwise configured. An attacker must be in the network path during the first successful connection before the client records a fingerprint, or must be able to modify the persisted fingerprint/state.

**Impact:**

The initial client key and token exchange occur over a TLS connection whose PKI identity is not validated. TOFU prevents silent later certificate changes, but it cannot detect a first-connection MITM. A first-connection attacker can capture the client key/token exchange or bind the client to the attacker certificate fingerprint.

**Recommended fix:**

Prefer explicit fingerprint pinning during install when PKI validation fails, rather than persisting `InsecureSkipVerify=true` without a known fingerprint. If skip is kept, make the UI wording and docs state that first connection is trust-on-first-use and must be performed only on a trusted network.

### Finding 3: API key validation is O(number of keys) bcrypt scans, enabling CPU amplification by unauthenticated client auth attempts

**Severity:** Medium.

**Evidence:**

- Raw client API keys are generated as `"sk-" + generateUUID()` at `internal/server/admin_api.go:162-164` and stored with bcrypt in `internal/server/admin_store.go:2111-2120`.
- Client key validation loads every API key (`internal/server/admin_store.go:1613-1615`) and runs `bcrypt.CompareHashAndPassword` over each stored key until a match (`1633-1645`). `findKeyByRawLocked` performs another full bcrypt scan (`1651-1662`) during token exchange.
- `RegisterClientAndExchangeToken` calls `validateClientKeyLocked` first (`1691-1694`) and then `exchangeTokenInTx`, which calls `findKeyByRawLocked` (`1701`, `1820-1846`), so successful exchanges do two bcrypt scans.
- Client authentication is reachable before a client is trusted in `internal/server/control_auth.go:233-236`; failures record limiter state at `236-239`, but the bcrypt work has already happened.

**Exploit preconditions:**

Server has multiple API keys configured. An unauthenticated remote party can attempt client WebSocket authentication with arbitrary keys. Rate limiting may reduce repeated attempts per source IP, but each attempt still forces bcrypt work over stored keys before failure is returned.

**Impact:**

CPU denial-of-service potential grows linearly with API key count and bcrypt cost. The same pattern also increases latency for legitimate client registrations.

**Recommended fix:**

Use random high-entropy API keys with a non-secret key identifier prefix (for lookup) plus an HMAC/SHA-256 or keyed hash of the secret for constant-time verification of one row. Alternatively store a fast SHA-256 lookup hash in addition to bcrypt and only bcrypt-verify the candidate row. Avoid the second scan by returning the matched key from validation.

## Non-findings / positive controls

- **Admin password hashing:** Admin passwords are hashed with `bcrypt.GenerateFromPassword` using default cost in production (`internal/server/admin_store.go:111-117`, `295-304`; password rotation at `internal/server/admin_security_store.go:171-179`). Tests intentionally lower cost.
- **JWT signing secret:** Initialization generates a 32-byte random JWT secret and fails if entropy fails (`internal/server/admin_store.go:306-310`). Missing initialized secrets fail closed (`239-249`, `398-413`). JWTs use HMAC signing (`internal/server/auth_middleware.go:63-64`) and parsing rejects non-HMAC signing methods (`95-99`). Sessions are server-side and checked on every request (`107-120`).
- **Browser token storage:** The web app no longer stores the JWT in JavaScript-readable storage. `web/src/stores/auth-store.ts:30-32` documents that only user/auth state is persisted; `web/src/lib/api.ts:86-90` sends credentials via same-origin cookie. Login handlers use the returned user and ignore the token field (`web/src/routes/login.tsx:49-57`, `70-76`).
- **Session cookie flags:** Cookies are HttpOnly, SameSite=Strict, scoped to `/api`, and Secure when the request is HTTPS/trusted-proxy HTTPS (`internal/server/auth_middleware.go:175-184`, `188-197`).
- **Client long-lived tokens:** Client tokens are 256-bit random (`internal/server/admin_store.go:1725-1731`), stored server-side as SHA-256 hashes (`1719-1723`, `1862-1865`), compared with `subtle.ConstantTimeCompare` (`1911-1913`), bound to install ID (`1916-1919`), revoked/expired, and refreshed on key exchange (`1825-1840`).
- **Client install ID:** Client install IDs use 16 bytes from `crypto/rand` and propagate entropy failure (`internal/client/state.go:146-151`).
- **Data-channel secret comparison:** The data-channel token is compared with constant-time compare and rejects empty server-side tokens (`internal/server/data.go:77-79`). The generation failure issue is covered above.
- **TOTP and recovery codes:** TOTP setup uses `pquerna/otp/totp.Generate` (`internal/server/admin_security_store.go:218-247`), setup challenges are short-lived (`adminAuthChallengeDefaultTTL = 5 * time.Minute` at `23-31`), recovery codes use 10 bytes of CSPRNG per code (`388-398`), are stored bcrypt-hashed (`402-417`), and are single-use (`420-451`).
- **WebAuthn origin/RP checks:** WebAuthn requires HTTPS except localhost, rejects non-local IP origins, and enforces request Origin equality with the configured server address (`internal/server/admin_webauthn.go:49-79`). Passkey login finish reloads challenge metadata and rejects mismatched request context (`internal/server/admin_security_api.go:174-185`).
- **Server TLS generation:** Auto TLS uses ECDSA P-256 with `crypto/rand`, random serial, x509 certificate creation, 0600 private-key file permissions, and TLS minimum version 1.2 (`internal/server/tls.go:149-177`, `180-257`). Custom TLS also sets minimum TLS 1.2 (`91-110`).
- **Release signing:** Release checksum manifests are signed with Ed25519 (`internal/releasesign/sign.go:26-28`, `cmd/netsgo-release-sign/main.go:173-188`) and SSH signatures (`cmd/netsgo-release-sign/main.go:191-220`). Install/upgrade scripts embed public keys (`scripts/common-update.sh:8-16`) and require signature verification of `checksums.txt` before checksum verification (`scripts/common-update.sh:261-309`). Release workflow signs checksums with `NETSGO_RELEASE_SIGNING_KEY_PEM` (`.github/workflows/release.yml:348-375`).
- **Desktop sidecar secret logging:** The Rust desktop layer passes client keys to the sidecar via environment rather than command-line args (`desktop/src-tauri/src/lib.rs:443-460`) and recursively redacts common secret fields in logs (`267-293`) with tests for nested `key`, `refresh_token`, and `dataToken` fields (`798-815`).
- **Linux service env file permissions:** Service env files containing `NETSGO_KEY` are written atomically as `0640` (`internal/svcmgr/env.go:139-142`), then on Linux chowned to installer user and the service group, keeping service user from rewriting credentials (`internal/svcmgr/env_linux.go:29-37`).

## Risky assumptions

- The security of API keys generated as `sk-` plus UUID currently depends on `generateUUID()` actually receiving CSPRNG bytes; because `generateUUID()` ignores errors, this assumption is not enforced.
- Client TOFU assumes the first TLS connection after accepting a certificate warning is not intercepted.
- Client token hashes are unsalted SHA-256. This is acceptable only because tokens are 256-bit random; it would not be acceptable for user-chosen secrets.
- TOTP secrets, WebAuthn credential JSON, JWT secret, API key hashes, and token hashes are stored in SQLite without application-layer encryption. This assumes local filesystem permissions and host security protect the server DB.
- Release verification in shell accepts either raw Ed25519 signature or SSH signature. This is reasonable because both are over the signed checksum manifest and both public keys are embedded, but it assumes the embedded-key refresh process remains controlled.
- macOS app signing script defaults to ad-hoc signing (`CODESIGN_IDENTITY=-` in `.github/workflows/release.yml:121-136`), which provides integrity for local bundle structure but not Developer ID/notarization trust.

## Follow-up checks for Main

1. Confirm whether another audit task is covering rate limiting. If not, inspect client-auth rate limiter ordering/effectiveness against the bcrypt-scan CPU issue.
2. Review whether `generateUUID()` callsites can all tolerate an `(string, error)` cutover; prioritize session IDs, auth challenges, API keys, client IDs, tunnel IDs, and preflight IDs.
3. Check install UX/docs for TLS skip/TOFU so operators understand first-connection MITM risk.
4. Inspect DB and data-dir permissions on all supported install modes, especially the server SQLite DB containing JWT/TOTP/WebAuthn/token material.
5. Verify release install/upgrade call order ensures `verify_signature` runs before `verify_checksum` and extraction in `install.sh`, `upgrade.sh`, and `common-update.sh` wrappers.
