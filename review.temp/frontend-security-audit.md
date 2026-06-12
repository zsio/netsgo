# Frontend Security Audit

## Scope
Audited the frontend web UI called out in the assignment: API wrapper, auth/router guards, event-stream helper, login/admin/dashboard routes, hooks, custom components rendering user/server data, `web/index.html`, and `web/vite.config.ts`. No source code was modified.

## Files inspected
- `web/src/lib/api.ts`
- `web/src/stores/auth-store.ts`
- `web/src/lib/auth.ts`
- `web/src/lib/router.ts`
- `web/src/routes/__root.tsx`
- `web/src/routes/index.tsx`
- `web/src/routes/login.tsx`
- `web/src/routes/dashboard.tsx`
- `web/src/routes/admin.tsx`
- `web/src/routes/admin/config.tsx`
- `web/src/routes/admin/security.tsx`
- `web/src/routes/dashboard/clients.$clientId.tsx`
- `web/src/routes/dashboard/index.tsx`
- `web/src/hooks/use-event-stream.ts`
- `web/src/hooks/use-admin-config.ts`
- `web/src/hooks/use-admin-keys.ts`
- `web/src/hooks/use-admin-security.ts`
- `web/src/hooks/use-clients.ts`
- `web/src/hooks/use-server-status.ts`
- `web/src/hooks/use-tunnel-mutations.ts`
- `web/src/hooks/use-client-traffic.ts`
- `web/src/components/custom/client/AddClientDialog.tsx`
- `web/src/components/custom/client/ShikiCodeBlock.tsx`
- `web/src/components/custom/client/client-install-commands.ts`
- `web/src/components/custom/client/client-service-address.ts`
- `web/src/components/custom/client/ClientSidebar.tsx`
- `web/src/components/custom/client/ClientHeader.tsx`
- `web/src/components/custom/client/ClientInfoCard.tsx`
- `web/src/components/custom/dashboard/DashboardClientTable.tsx`
- `web/src/components/custom/dashboard/DashboardTunnelTable.tsx`
- `web/src/components/custom/dashboard/ServerInfoCard.tsx`
- `web/src/components/custom/tunnel/TunnelDialog.tsx`
- `web/src/components/custom/tunnel/TunnelListTable.tsx`
- `web/src/components/custom/tunnel/TunnelTable.tsx`
- `web/src/components/custom/common/VersionUpdateIndicator.tsx`
- `web/src/components/custom/common/CopyButton.tsx`
- `web/src/components/custom/common/CopyableIpLine.tsx`
- `web/src/components/custom/layout/ErrorFallback.tsx`
- `web/src/components/ui/chart.tsx`
- `web/src/components/ui/sidebar.tsx`
- `web/src/lib/server-address.ts`
- `web/src/lib/tunnel-model.ts`
- `web/src/lib/query-client.ts`
- `web/src/main.tsx`
- `web/index.html`
- `web/vite.config.ts`

## Confirmed findings

### F-01: Client-side route guard trusts localStorage authentication state, allowing forged UI access and opening same-origin authenticated SSE/API activity when cookies are still valid
- **Evidence:** `web/src/stores/auth-store.ts:14-26` persists `{ user, isAuthenticated }` under `netsgo-auth` via zustand persist. `getStoredAuthState` reads `window.localStorage.getItem(AUTH_STORAGE_KEY)` at `web/src/stores/auth-store.ts:39-55` and returns `parsed.state?.isAuthenticated ?? false` without server validation. `requireConsoleAuth` grants dashboard access solely from that value at `web/src/lib/auth.ts:4-10`. The root layout starts the SSE hook globally at `web/src/routes/__root.tsx:6-9`; `useEventStream` connects when the store says authenticated and the route is not `/login` at `web/src/hooks/use-event-stream.ts:423-429`, then sends same-origin cookies to `/api/events` at `web/src/hooks/use-event-stream.ts:448-455`.
- **Exploit preconditions:** Attacker must be able to run JavaScript in the console origin (e.g. XSS, malicious extension, or local console access) or otherwise write `localStorage`. If the victim has a valid httpOnly session cookie, forged `netsgo-auth` state unlocks the route guard and initiates authenticated API/SSE traffic. If no valid cookie exists, API calls receive 401 and the API wrapper/SSE logout paths clear state (`web/src/lib/api.ts:117-121`, `web/src/hooks/use-event-stream.ts:457-461`).
- **Impact:** The frontend treats localStorage as authorization state. This is not a backend authorization bypass, but it weakens frontend security boundaries: any XSS/local script can flip the UI into an authenticated state, trigger `/api/events`, and display any data the still-valid cookie authorizes. It also means logout/session revocation is only discovered reactively on the next failing API/SSE call.
- **Notes:** The comment at `web/src/stores/auth-store.ts:30-33` correctly says the JWT is no longer stored in JS-readable storage; the issue is the persisted auth boolean still drives route access.

### F-02: Update release links use backend-supplied `href` without protocol/host validation
- **Evidence:** `VersionUpdateContent` sets `const releaseHref = data.release_url || 'https://github.com/zsio/netsgo/releases';` at `web/src/components/custom/common/VersionUpdateIndicator.tsx:47-49`, then renders it directly into `<a href={releaseHref} target="_blank" rel="noreferrer">` at `web/src/components/custom/common/VersionUpdateIndicator.tsx:85-89`. The data is fetched from `/api/version/check` or `/api/clients/.../version/check` via `web/src/hooks/use-version-check.ts:31-36` and `web/src/hooks/use-version-check.ts:39-58`.
- **Exploit preconditions:** Attacker must control or tamper with the version-check response (compromised update endpoint, compromised backend, network/TLS termination compromise, or malicious local deployment config causing the backend to return attacker data).
- **Impact:** The console can present an attacker-controlled external link in a trusted update dialog. `target="_blank" rel="noreferrer"` prevents opener/referrer leakage, so this is not tabnabbing, but it still enables phishing/malware redirection from an admin security-sensitive flow. If `javascript:` URLs are accepted by the browser in this context, clicking could execute script in the page context; this needs browser/runtime verification because React usually passes the string through as an href attribute.

### F-03: Backend-supplied upgrade commands are displayed and copied verbatim in a trusted admin flow
- **Evidence:** For service installs, `VersionUpdateContent` renders `data.commands.command` as text at `web/src/components/custom/common/VersionUpdateIndicator.tsx:68-73` and copies the exact same backend-provided string via `CopyButton value={data.commands.command}` at `web/src/components/custom/common/VersionUpdateIndicator.tsx:73-76`. The update data is obtained by `useVersionCheck`/`useForceVersionCheck` from `/api/version/check` or `/api/clients/${id}/version/check` at `web/src/hooks/use-version-check.ts:31-58`.
- **Exploit preconditions:** Attacker controls or tampers with version-check command data. Admin must copy/run the command on the server or client host.
- **Impact:** Remote command execution on the host where the admin pastes the command. This is a supply-chain/trust-boundary risk in the frontend UI flow: the frontend does not constrain the command to an expected static installer/upgrade pattern before presenting a one-click copy affordance.
- **Notes:** React text rendering prevents XSS from the command string itself; the risk is social/operational command execution, not DOM injection.

### F-04: Quick-start install command executes a remote script and embeds a live client key in copied shell/YAML output
- **Evidence:** `INSTALL_SCRIPT_URL` is a hardcoded remote script URL at `web/src/components/custom/client/client-install-commands.ts:1`. `AddClientDialog` generates a new raw API key and stores it in component state at `web/src/components/custom/client/AddClientDialog.tsx:145-168`, specifically `setGeneratedKey(data.raw_key)` at line 156. It then builds `curl -fsSL ${INSTALL_SCRIPT_URL} | sh -s -- ... --key ${shellQuote(generatedKey)}` at `web/src/components/custom/client/AddClientDialog.tsx:192-199`, Docker env output with `NETSGO_KEY` at `web/src/components/custom/client/AddClientDialog.tsx:201-213`, and Compose YAML with `NETSGO_KEY` at `web/src/components/custom/client/AddClientDialog.tsx:215-236`. The UI renders/copies these values at `web/src/components/custom/client/AddClientDialog.tsx:359-379` and `web/src/components/custom/client/AddClientDialog.tsx:403-423`.
- **Exploit preconditions:** Admin uses the quick-start/copy flow. Attacker compromises `https://netsgo.zs.uy/install.sh`, DNS/TLS for that host, the admin clipboard, shell history, or a place where copied commands/YAML are stored. The generated key has `permissions: ['connect']` and admin-selected max uses/expiry at `web/src/components/custom/client/AddClientDialog.tsx:145-153`.
- **Impact:** Client enrollment key disclosure or arbitrary code execution on the client host when the pasted command is run. The frontend does quote shell/YAML variables (`shellQuote` at `web/src/components/custom/client/AddClientDialog.tsx:50-52`, `yamlDoubleQuote` at lines 54-56), which mitigates command injection from the key/server URL; the remaining risk is the trusted remote-script and secret-in-command UX.

### F-05: Multiple legacy endpoint fallbacks interpolate unencoded `clientId` into URLs
- **Evidence:** `ClientHeader` updates display names with ``/api/clients/${client.id}/display-name`` at `web/src/components/custom/client/ClientHeader.tsx:54-59`. `useDeleteClient` calls ``/api/clients/${clientId}`` at `web/src/hooks/use-clients.ts:13-18`. Legacy tunnel fallbacks interpolate `clientId` unencoded at `web/src/hooks/use-tunnel-mutations.ts:85-88`, `web/src/hooks/use-tunnel-mutations.ts:108-115`, `web/src/hooks/use-tunnel-mutations.ts:125-132`, `web/src/hooks/use-tunnel-mutations.ts:142-149`, and `web/src/hooks/use-tunnel-mutations.ts:170-174`. By contrast, the newer `tunnelApi.listByClientRole` uses `encodeURIComponent` for `clientId` at `web/src/lib/api.ts:160-168`, and traffic URLs encode `clientId` at `web/src/hooks/use-client-traffic.ts:43-65`.
- **Exploit preconditions:** Attacker can register or cause display of a client ID containing `/`, `?`, `#`, or path traversal-like segments, and an admin performs the affected action. This depends on backend client ID validation; not confirmed from frontend-only review.
- **Impact:** If backend accepts arbitrary client IDs, the frontend can send mutation requests to unintended API paths or with attacker-controlled query/fragment components. This can become confused-deputy behavior for delete/display-name/tunnel operations. If backend strictly generates opaque safe IDs, this is not exploitable; the frontend nevertheless has inconsistent encoding in security-sensitive calls.

### F-06: Admin password/TOTP/passkey dialogs keep sensitive secrets in React state after close/success in several flows
- **Evidence:** Security forms store current password, new password, MFA code, setup token, TOTP secret, and recovery codes in React state at `web/src/routes/admin/security.tsx:67-82`. Some close handlers only close dialogs and do not clear form data: username/password dialogs call `onClose={() => setAccountDialog(null)}` at `web/src/routes/admin/security.tsx:323-340`; rename/delete passkey dialogs call `onClose={() => setRenamePasskey(null)}` / `setDeletePasskey(null)` at `web/src/routes/admin/security.tsx:376-377`. `handleUsernameSubmit` and `handlePasswordSubmit` close the dialog at `web/src/routes/admin/security.tsx:98-132` without clearing `usernameForm`/`passwordForm` if the response does not require relogin. `disableTOTP` and `regenerateRecoveryCodes` close only `totpAction` at `web/src/routes/admin/security.tsx:163-190`; `recoveryCodes` remain in state until the dialog close handler at `web/src/routes/admin/security.tsx:359-362`.
- **Exploit preconditions:** Attacker needs same-origin JS execution after the admin used these flows, or local access to a browser memory snapshot/devtools/session. React state is not persisted to localStorage.
- **Impact:** Increased dwell time of high-value secrets (current password, new password, MFA/recovery codes, TOTP secret/setup token) in JS heap. This magnifies the impact of any XSS or malicious extension. The normal browser threat model already exposes form values to same-origin JS while typed; the concern is retention after dialogs close/succeed.

## Non-findings / mitigated observations

- **JWT token is not stored in JS-readable storage in the reviewed frontend.** `api.ts` uses `credentials: 'same-origin'` for all requests at `web/src/lib/api.ts:86-90`, and the only auth storage seen is the persisted user/auth boolean at `web/src/stores/auth-store.ts:14-26` and `web/src/stores/auth-store.ts:30-33`. Searches found no frontend `Authorization`/`Bearer` token storage.
- **React escaping protects most user/server-rendered text.** Client names, hostnames, tunnel names, endpoint labels, errors, recovery codes, and config/tunnel conflicts are rendered as React children/attributes rather than raw HTML in inspected components, e.g. client sidebar labels at `web/src/components/custom/client/ClientSidebar.tsx:180-197`, tunnel row labels at `web/src/components/custom/tunnel/TunnelListTable.tsx:361-400`, and recovery codes at `web/src/routes/admin/security.tsx:959-963`.
- **The Shiki `dangerouslySetInnerHTML` sink appears constrained to highlighter-generated escaped HTML.** `ShikiCodeBlock` calls `highlighter.codeToHtml(code, ...)` at `web/src/components/custom/client/ShikiCodeBlock.tsx:89-98` and injects that output at `web/src/components/custom/client/ShikiCodeBlock.tsx:156-160`. The `code` includes generated install commands with secrets/server address, but Shiki is expected to escape code tokens. This remains a dependency trust assumption, not a confirmed XSS.
- **The chart `dangerouslySetInnerHTML` sink is not fed direct server strings in current uses.** `ChartStyle` injects CSS variables at `web/src/components/ui/chart.tsx:91-110`, but current `ChartContainer` callers use fixed `var(--chart-*)` or deterministic HSL colors from `TrafficChart` (`web/src/components/custom/chart/TrafficChart.tsx:41-56`, `web/src/components/custom/chart/TrafficChart.tsx:161-165`) and `TrafficRateChart` (`web/src/components/custom/chart/TrafficRateChart.tsx:47-52`).
- **SSE payloads are JSON-parsed and type-guarded before mutating query state.** `parseEventPayload` catches bad JSON and requires a guard at `web/src/hooks/use-event-stream.ts:184-190`; event handlers use specific guards before cache updates at `web/src/hooks/use-event-stream.ts:266-388`. No DOM sink is directly reached from raw SSE text in the inspected code.
- **SSE auth failure logs out instead of continuing.** `/api/events` 401 triggers `logout()`, hash navigation to login, and disconnect status at `web/src/hooks/use-event-stream.ts:457-461`.
- **External static links inspected use `target="_blank"` with `rel="noreferrer"`.** Docs link at `web/src/components/custom/client/ClientSidebar.tsx:235-238`, GitHub link at `web/src/components/custom/layout/TopBar.tsx:77-80`, and update release link at `web/src/components/custom/common/VersionUpdateIndicator.tsx:85-89`; opener leakage is mitigated.
- **Server address used in install commands is validated/normalized before use.** `resolveAddClientServiceAddress` tries `normalizeServerAddr` for effective/admin/key/status/browser origins at `web/src/components/custom/client/client-service-address.ts:11-28`; `normalizeServerAddr` only accepts `http:`/`https:`, base URL, no user info, valid host at `web/src/lib/server-address.ts:1-13` and `web/src/lib/server-address.ts:70-140`. Shell and YAML quoting for generated key/address is present at `web/src/components/custom/client/AddClientDialog.tsx:50-56`.
- **Login error rendering is escaped.** Login captures `err.message` at `web/src/routes/login.tsx:58-60` and renders `{error}` as React text at `web/src/routes/login.tsx:250-255`. Error leakage still depends on backend messages, but no XSS sink was found here.
- **Client-side validation is not the only apparent barrier for critical admin/tunnel mutations.** The UI validates ports/server address before submit (`web/src/components/custom/tunnel/TunnelDialog.tsx:273-387`, `web/src/routes/admin/config.tsx:96-122`), but sends mutations to backend endpoints that return field errors processed by `serverFieldError`/`ApiError` (`web/src/components/custom/tunnel/TunnelDialog.tsx:335-368`, `web/src/routes/admin/config.tsx:235-243`). Backend enforcement still needs separate confirmation.

## Risky assumptions

- Backend authorization, CSRF controls, cookie flags (`HttpOnly`, `Secure`, `SameSite`), and client ID format validation were not proven in this frontend-only task. Findings involving cookies/client IDs depend on those backend properties.
- The XSS safety of `ShikiCodeBlock` depends on `@shikijs/core` escaping all code text before returning HTML. No scanner or dynamic payload test was run by this task.
- The `release_url` and `commands.command` trust boundary depends on how `/api/version/check` obtains and validates update metadata. Frontend currently treats returned metadata as trusted.
- `web/vite.config.ts` is dev-server only; `server.host: '0.0.0.0'` and custom `allowedHosts` at `web/vite.config.ts:27-45` are not production settings unless operators expose the dev server. I did not mark this a production vulnerability from frontend evidence alone.
- No project-wide build/test/lint/format/security scanner was run, per assignment constraints.

## Follow-up checks for Main

1. Confirm backend session cookie flags and CSRF posture for cookie-authenticated JSON mutations and `/api/events`.
2. Confirm backend client IDs are generated/validated as URL-safe opaque identifiers; if not, encode every interpolated `clientId` path segment noted in F-05.
3. Trace `/api/version/check` metadata origin and validation. If it can be remote or cached from GitHub/release feeds, constrain `release_url` to expected HTTPS hosts and constrain/canonicalize `commands.command` before exposing copy UX.
4. Dynamically test `VersionUpdateContent` with `release_url: 'javascript:alert(1)'` and with hostile schemes to determine whether browser/React blocks script execution in the rendered anchor.
5. Dynamically test `ShikiCodeBlock` with code containing `<img src=x onerror=alert(1)>`, `</span><script>...`, and CSS-breaking tokens to verify Shiki escaping in the installed version.
6. Consider replacing localStorage-auth route decisions with a server-backed `/api/auth/session` bootstrap or treating localStorage only as a hint while showing a loading state until server validation completes.
7. Clear sensitive admin form states on close/success and minimize retention of TOTP setup/recovery code material after dialogs close.
