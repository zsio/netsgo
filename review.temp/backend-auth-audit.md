# Backend auth/security audit

## Scope
Audited backend management-plane authentication and security code only: `internal/server/admin_api.go`, `auth_middleware.go`, `admin_store.go`, `admin_models.go`, `admin_security_api.go`, `admin_security_store.go`, `admin_webauthn.go`, related route/security/rate-limit/dispatch tests, and admin reset/init entrypoints in `cmd/netsgo` and `internal/manage`.

No project-wide build/test/lint/format/security scanner was run.

## Files inspected
- `internal/server/server_http.go`
- `internal/server/http_tunnel_proxy.go`
- `internal/server/auth_middleware.go`
- `internal/server/auth_middleware_test.go`
- `internal/server/admin_api.go`
- `internal/server/admin_api_test.go`
- `internal/server/admin_store.go`
- `internal/server/admin_models.go`
- `internal/server/admin_security_api.go`
- `internal/server/admin_security_store.go`
- `internal/server/admin_security_test.go`
- `internal/server/admin_webauthn.go`
- `internal/server/rate_limiter.go`
- `internal/server/rate_limit_integration_test.go`
- `cmd/netsgo/cmd_manage.go`
- `cmd/netsgo/cmd_server.go`
- `internal/manage/admin_user.go`

## Confirmed findings

### 1. MFA login challenges are not rate-limited after password success

Evidence:
- Password login rate limiting only wraps `/api/auth/login`: `handleAPILogin` checks `s.auth.loginLimiter.Allow(ip)` before parsing credentials at `internal/server/admin_api.go:55-64`, records failures only on password validation error at `internal/server/admin_api.go:82-88`, and resets failures immediately after a correct password at `internal/server/admin_api.go:91-94`.
- If TOTP is enabled, a correct password calls `maybeBeginMFALogin` and returns without a session at `internal/server/admin_api.go:96-99`; the MFA challenge is created in `internal/server/admin_security_api.go:710-729`.
- `/api/auth/mfa/verify` itself has no rate limiter or lockout check: it decodes `mfa_token`/`code` at `internal/server/admin_security_api.go:43-56`, calls `verifyLoginMFA` at lines `66-73`, consumes the challenge only on success at lines `75-79`, and returns `401` on wrong code without recording any failure.
- `verifyLoginMFA` accepts TOTP or recovery code through `verifyMFAInTx` at `internal/server/admin_security_api.go:82-103`; `verifyMFAInTx` validates TOTP and then scans unused recovery codes at `internal/server/admin_security_store.go:112-126`.
- Tests confirm the intended MFA branch does not set a session cookie but do not cover MFA verify throttling: `internal/server/admin_security_test.go:146-164`.

Exploit preconditions:
- Attacker knows the admin password, or has obtained a valid `mfa_token` after a correct password attempt.
- The `mfa_token` is live for `adminAuthChallengeDefaultTTL` (`5 * time.Minute`) per `internal/server/admin_security_store.go:23-31` and `StoreAuthChallenge` expiry at lines `466-475`.

Impact:
- The second factor becomes the only online brute-force target for the challenge TTL. Six-digit TOTP is still time-limited, but the code currently imposes no per-IP, per-user, or per-challenge attempt ceiling on `/api/auth/mfa/verify`; recovery codes are stronger but are also attempted through the same unthrottled path. A correct password success also resets the login failure counter before MFA completion, so password-stage lockout does not limit MFA-stage guesses.

### 2. Passkey login begin leaks whether a passkey exists for the configured admin origin

Evidence:
- `/api/auth/passkey/begin` is unauthenticated in route registration at `internal/server/server_http.go:49-52`.
- `handleAPIPasskeyLoginBegin` loads passkeys by relying party and origin at `internal/server/admin_security_api.go:111-120` and returns `404 passkey_not_registered` when no passkey is registered at lines `121-123`.
- When passkeys exist, the same handler loads the admin user and returns a WebAuthn assertion challenge at `internal/server/admin_security_api.go:125-158`.
- The test suite codifies this distinguishable unauthenticated response: `TestAPI_PasskeyBeginRequiresRegisteredCredential` expects `404` and code `passkey_not_registered` at `internal/server/admin_security_test.go:205-224`.

Exploit preconditions:
- Attacker can reach the management host and send `POST /api/auth/passkey/begin` with an Origin matching the configured server address. Origin mismatch is rejected (`internal/server/admin_webauthn.go:75-78`), but same-origin requests from the login page or direct HTTP clients can observe the response.

Impact:
- Remote user enumeration of the admin account’s passkey enrollment posture. This is lower severity than credential bypass, but it leaks whether passwordless/passkey login is available for the management plane and can guide phishing or targeted attack choice.

### 3. First-time and reset admin passwords are accepted through command-line flags, exposing secrets to local process observers and shell history

Evidence:
- First-time server initialization accepts `--init-admin-password` as a plain string flag in `cmd/netsgo/cmd_server.go:232-234`; the value is read from viper into `InitParams.AdminPassword` at `cmd/netsgo/cmd_server.go:36-39`, and examples show inline password usage at `cmd/netsgo/cmd_server.go:127-130`.
- Offline reset accepts `--password` as a required plain string flag in `cmd/netsgo/cmd_manage.go:46-55`, reads it at lines `66-75`, and the command examples show inline passwords at lines `41-42`.
- The reset implementation then validates server stopped state and rewrites the admin user in `internal/manage/admin_user.go:23-47`; no alternative stdin/env/file secret path is evident in the inspected entrypoint.

Exploit preconditions:
- Local user/process with permission to observe process command lines or shell history on the host running initialization/reset.

Impact:
- Admin passwords can be exposed outside the application’s database hashing boundary before bcrypt is applied. This is especially relevant for the documented recovery/reset path, which likely runs with elevated privileges because default data dir reset reruns through sudo at `cmd/netsgo/cmd_manage.go:82-86`.

## Non-findings / positive controls

### Protected admin and management APIs require server-side sessions

Evidence:
- Management routes except login/MFA/passkey begin/finish are wrapped with `RequireAuth`: `internal/server/server_http.go:27-48`, logout and admin key/config/security routes at lines `53-71`, and SSE at line `73`.
- `RequireAuth` accepts Bearer token or `netsgo_session` cookie at `internal/server/auth_middleware.go:19-31`, rejects missing credentials at lines `77-80`, rejects unavailable admin store at lines `83-87`, parses JWT with the store-backed secret at lines `88-104`, then requires a live server-side session at lines `107-112`.
- Anonymous access and single-session invalidation are covered in tests: protected routes return `401` without a token at `internal/server/admin_api_test.go:232-236`; a second login invalidates the old token at lines `239-250`.

### JWT/session handling avoids stateless long-lived bearer-only auth

Evidence:
- JWT claims contain only a session id plus registered claims at `internal/server/auth_middleware.go:34-38`; token expiry is set from the persisted session at lines `55-64`.
- JWT secret is generated with 32 random bytes during initialization at `internal/server/admin_store.go:306-310` and stored in `server_config` at lines `355-358`; `GetJWTSecret` errors if uninitialized or missing at `internal/server/admin_store.go:398-414`.
- Session creation deletes existing sessions for the user before inserting the new one at `internal/server/admin_store.go:1359-1399`; expired sessions are rejected by `GetSession` at lines `1402-1418`.
- Invalid signature and old fallback-secret tokens are rejected in tests at `internal/server/auth_middleware_test.go:95-145`; cookie auth success, invalid cookie, header-over-cookie priority, and missing credential behavior are tested at `internal/server/auth_middleware_test.go:356-439`.

### Session cookie posture is mostly sound for browser use

Evidence:
- Cookie is set `HttpOnly`, path `/api`, `Secure` only when HTTPS/trusted proxy indicates HTTPS, and `SameSite=Strict` at `internal/server/auth_middleware.go:175-184`; clearing uses the same path/security posture at lines `188-197`.
- Cookie behavior is tested: `HttpOnly`, `SameSiteStrict`, `/api`, non-empty value, and no Secure flag on plain HTTP are asserted at `internal/server/admin_api_test.go:898-940`.
- CSRF risk for cookie-authenticated mutating APIs is reduced by `SameSite=Strict` and by no observed CORS allow-origin header for SSE in `internal/server/server_test.go:1029-1043` (found via search), but see risky assumptions about custom clients and same-site subdomain threat model.

### Passwords and API keys are hashed before persistence

Evidence:
- Admin initialization and reset hash passwords with bcrypt at `internal/server/admin_store.go:301-304` and `606-609`; password verification uses bcrypt at lines `579-594`.
- Nonexistent usernames trigger a dummy bcrypt compare at `internal/server/admin_store.go:583-586`, and security-credential verification does the same when user id is missing at `internal/server/admin_security_store.go:80-85`.
- API keys are generated server-side as `sk-` plus UUID at `internal/server/admin_api.go:162-164`, hashed with bcrypt before persistence at `internal/server/admin_store.go:2111-2120`, and the raw key is only returned on create at `internal/server/admin_api.go:192-197`.

### Sensitive account changes require current password and, if enabled, MFA

Evidence:
- Username/password/TOTP begin/disable/recovery regeneration/passkey update/delete/register begin all call `VerifyAdminSecurityCredentials` before mutation: `internal/server/admin_security_api.go:291-295`, `321-325`, `350-354`, `414-418`, `443-447`, `491-495`, `509-513`, and `542-546`.
- `VerifyAdminSecurityCredentials` verifies current password with bcrypt and then requires MFA when `user.TOTPEnabled` is true at `internal/server/admin_security_store.go:69-109`.
- Password update invalidates sessions and auth challenges at `internal/server/admin_security_store.go:203-215`; username update does the same at lines `148-168`; TOTP confirm/disable/recovery regeneration and passkey add/delete also delete sessions/challenges at lines `299-304`, `331-340`, `362-374`, `611-623`, and `737-746`.

### TOTP/WebAuthn origin/RP checks are present

Evidence:
- Passkeys require HTTPS or localhost and reject non-localhost IP addresses as RP IDs at `internal/server/admin_webauthn.go:61-74`.
- Request Origin must match the configured server origin at `internal/server/admin_webauthn.go:75-79`; `sameOrigin` compares scheme and host case-insensitively at lines `82-89`.
- WebAuthn config requires resident keys and user verification at `internal/server/admin_webauthn.go:91-112`.
- Tests cover rejection of non-localhost HTTP and origin mismatch at `internal/server/admin_security_test.go:190-202` and `227-239`.

### CSRF/CORS/header posture: no broad CORS found; admin responses get security headers through host dispatch

Evidence:
- Management handler is wrapped by `securityHeadersHandler` in `internal/server/server_http.go:9-10`.
- Security headers include `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`, CSP with `frame-ancestors 'none'` and `form-action 'self'`, and HSTS only when request is HTTPS/trusted proxy HTTPS at `internal/server/server_http.go:81-93`.
- Host dispatch only serves management when the Host matches the effective management host (except dev mode / explicit loopback fallback) at `internal/server/http_tunnel_proxy.go:100-116` and `119-169`.

### Login and client-auth rate limiting exists and trusts proxy headers only from trusted sources

Evidence:
- Login limiter defaults: 10 requests/minute, 5 failures, 15-minute lockout at `internal/server/auth_service.go:23-31`.
- `RateLimiter.Allow` enforces sliding-window request limits at `internal/server/rate_limiter.go:55-92`; `RecordFailure` locks after max failures at lines `95-109`.
- `clientIP` trusts `X-Forwarded-For`/`X-Real-IP` only when `trustProxyHeaders` accepts loopback or configured trusted proxies at `internal/server/rate_limiter.go:180-215`.
- Tests cover request limit, lockout, lockout expiry, and trusted/untrusted XFF behavior at `internal/server/rate_limit_integration_test.go:49-143` and `377-434`.

### Offline admin reset invalidates existing web sessions and MFA/passkeys

Evidence:
- Reset refuses uninitialized data and requires the server lock in `internal/manage/admin_user.go:23-36` before opening the store and calling `ResetAdminUser` at lines `38-47`.
- Store reset validates password strength, bcrypt-hashes the new password, requires initialized config, then deletes `admin_sessions`, `admin_auth_challenges`, `admin_passkeys`, `admin_totp_recovery_codes`, and `admin_users` before inserting the replacement admin at `internal/server/admin_store.go:597-660`.

## Risky assumptions

- Session theft protection binds only User-Agent, not IP: `RequireAuth` rejects UA mismatch at `internal/server/auth_middleware.go:114-120`, but does not compare `session.IP` stored at `internal/server/admin_store.go:1371-1373`. This is likely intentional to avoid breaking mobile/proxy clients; it means stolen tokens from the same UA remain usable until expiry/session deletion.
- `generateUUID` ignores `rand.Read` errors at `internal/server/admin_store.go:71-78`. If the OS CSPRNG failed, IDs/API keys/challenges generated from this helper could degrade. Go’s `crypto/rand` failure is rare, but this is a security-sensitive helper.
- `GetSession` logs and returns nil on DB read/parse errors at `internal/server/admin_store.go:1411-1413`, causing fail-closed `401` from `RequireAuth` rather than a distinguishable `5xx`. This is safe for auth bypass, but operationally hides storage corruption as an auth failure.
- TOTP secrets are stored plaintext in `admin_users.totp_secret` (`internal/server/admin_models.go:30-31`, persisted at `internal/server/admin_security_store.go:293`). That is normal for TOTP validation but means DB disclosure compromises the second factor; recovery codes are bcrypt-hashed at `internal/server/admin_security_store.go:402-417`.
- Cookie `Secure` is intentionally false on direct HTTP (`internal/server/auth_middleware.go:175-184`, asserted at `internal/server/admin_api_test.go:938-939`). If operators expose the management plane over plain HTTP, session cookies and bearer tokens are transport-exposed; TLS/reverse proxy configuration is therefore a deployment security boundary, not enforced by auth code.

## Follow-up checks for Main

1. Decide whether to treat the unthrottled `/api/auth/mfa/verify` path as a fix-now issue; if so, add per-IP/per-challenge attempt limiting and ensure successful password does not fully reset abuse state until MFA completes.
2. Consider returning a less enumerating passkey-begin response when no credential exists, or accept the low-severity enrollment leak as product behavior.
3. Consider replacing `--init-admin-password` / `--password` secret flags with stdin prompt, env var, or file descriptor support while preserving non-interactive deployments.
4. Review whether `generateUUID` should return an error or panic on `crypto/rand` failure for security-sensitive IDs and API key material.
