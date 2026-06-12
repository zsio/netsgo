# Static vulnerability tooling audit

## Scope

Scanner runs performed by Main during the security review. These are tool findings, not source-code fixes. Source code was not modified.

## Commands run

- `go version`
- `GOPROXY=https://goproxy.cn,direct go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
- `bun audit` in `web/`
- `bun audit` in `desktop/`
- `cargo install cargo-audit --locked && cargo audit` in `desktop/src-tauri/`
- `GOPROXY=https://goproxy.cn,direct go run github.com/securego/gosec/v2/cmd/gosec@latest ./...`

Earlier `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` with the default Go proxy failed because `proxy.golang.org` timed out over IPv6; the scan was rerun with `goproxy.cn` and completed.

## Environment evidence

- `go version` returned `go version go1.26.3 darwin/arm64`.

## govulncheck results

Command result: non-zero exit because reachable standard-library vulnerabilities were found.

Confirmed reachable vulnerabilities:

1. `GO-2026-5039` — `net/textproto`: arbitrary inputs are included in errors without escaping.
   - Found in standard library `net/textproto@go1.26.3`.
   - Fixed in `net/textproto@go1.26.4`.
   - Example trace: `internal/server/server_bootstrap.go:182:27` calls `http.Server.Serve`, eventually reaching `textproto.Reader.ReadMIMEHeader`.
   - Security relevance: public HTTP listener parses attacker-controlled request headers. This should be resolved by building/running with Go `1.26.4+` once available in the project toolchain.

2. `GO-2026-5037` — `crypto/x509`: inefficient candidate hostname parsing.
   - Found in standard library `crypto/x509@go1.26.3`.
   - Fixed in `crypto/x509@go1.26.4`.
   - Example traces:
     - `internal/server/server_bootstrap.go:182:27` -> `http.Server.Serve` -> certificate verification.
     - `internal/client/client.go:540:29` -> `websocket.Dialer.Dial` -> `x509.Certificate.VerifyHostname`.
     - `internal/tui/tui.go:273:21` -> formatting path that reaches `x509.HostnameError.Error`.
   - Security relevance: client TLS verification and server TLS paths can parse attacker-influenced certificate/hostname data. Upgrade Go toolchain/runtime to fixed standard library.

Additional govulncheck note:

- The scan also found 1 vulnerability in imported packages and 0 vulnerabilities in required modules that appeared reachable. The summarized tool output did not include details for the non-called package vulnerability; rerun with `-show verbose` if package-level triage is needed.

## Bun audit — web

Command result: non-zero exit. `web/package.json` is private, but dependency graph includes production dependencies and dev/design tooling. `bun audit` found 32 advisories: 6 high, 25 moderate, 1 low.

High-severity advisories reported:

- `fast-uri <=3.1.1` via `eslint › @eslint/eslintrc › ajv › fast-uri` and `shadcn › @modelcontextprotocol/sdk › ajv › fast-uri`:
  - Host confusion via percent-encoded authority delimiters: `GHSA-v39h-62p7-jpjc`.
  - Path traversal via percent-encoded dot segments: `GHSA-q3j6-qgpj-74h6`.
- `flatted <=3.4.1` via `eslint › file-entry-cache › flat-cache › flatted`:
  - Prototype pollution in `parse()`: `GHSA-rf6f-7fwh-wjgh`.
- `path-to-regexp >=8.0.0 <8.4.0` via `shadcn › msw › path-to-regexp`:
  - DoS via sequential optional groups: `GHSA-j3q9-mxjg-w52f`.
- `picomatch <2.3.2` via `vite › tinyglobby › fdir › picomatch`, `shadcn › @dotenvx/dotenvx › picomatch`, and TypeScript ESLint paths:
  - ReDoS via extglob quantifiers: `GHSA-c2c7-rcm5-vvqj`.

Moderate/low advisories also included `qs`, `@hono/node-server`, `ip-address`, `brace-expansion`, `postcss`, `hono`, and `picomatch` method-injection issues.

Triage:

- Many findings flow through `shadcn`, `eslint`, `typescript-eslint`, `msw`, and Vite/dev tooling. They are not automatically production-exploitable in the embedded web UI, but they are real build/dev supply-chain risk and should be upgraded or removed where unused.
- `postcss <8.5.10` also flows through `vite` and can matter during build-time CSS processing if untrusted CSS is ever processed.
- `shadcn` appears as a runtime dependency in `web/package.json` rather than a dev-only CLI dependency; that broadens the installed production dependency graph unless bundling/tree-shaking removes it completely. Consider moving/removing it after verifying it is only used for component generation.

## Bun audit — desktop

Command result: success. `desktop/` reported: `No vulnerabilities found`.

## cargo-audit — desktop/src-tauri

Command result: completed with warnings and no blocking vulnerability failure in the visible summary. `cargo-audit` installed `cargo-audit v0.22.2`, fetched RustSec advisory DB, scanned 493 crates, and reported 17 allowed warnings.

Warnings:

- Unmaintained GTK3 gtk-rs bindings:
  - `atk 0.18.2` — `RUSTSEC-2024-0413`.
  - `atk-sys 0.18.2` — `RUSTSEC-2024-0416`.
  - `gdk 0.18.2` — `RUSTSEC-2024-0412`.
  - `gdk-sys 0.18.2` — `RUSTSEC-2024-0418`.
  - `gdkwayland-sys 0.18.2` — `RUSTSEC-2024-0411`.
  - `gdkx11 0.18.2` — `RUSTSEC-2024-0417`.
  - `gdkx11-sys 0.18.2` — `RUSTSEC-2024-0414`.
  - `gtk 0.18.2` — `RUSTSEC-2024-0415`.
  - `gtk-sys 0.18.2` — `RUSTSEC-2024-0420`.
  - `gtk3-macros 0.18.2` — `RUSTSEC-2024-0419`.
- Other unmaintained crates:
  - `proc-macro-error 1.0.4` — `RUSTSEC-2024-0370`.
  - `unic-char-property 0.9.0` — `RUSTSEC-2025-0081`.
  - `unic-char-range 0.9.0` — `RUSTSEC-2025-0075`.
  - `unic-common 0.9.0` — `RUSTSEC-2025-0080`.
  - `unic-ucd-ident 0.9.0` — `RUSTSEC-2025-0100`.
  - `unic-ucd-version 0.9.0` — `RUSTSEC-2025-0098`.
- Unsound crate:
  - `glib 0.18.5` — `RUSTSEC-2024-0429`, unsoundness in `Iterator` and `DoubleEndedIterator` impls for `glib::VariantStrIter`.

Triage:

- These are desktop/Tauri transitive dependencies, not Go server/client runtime dependencies.
- The `glib` unsoundness is more concrete than “unmaintained”; desktop dependency upgrades should track the Tauri/GTK stack that removes or updates affected crates.

## gosec results

Command result: non-zero exit. Gosec scanned 127 files and reported 133 issues.

Important caveat:

- The command traversed `web/node_modules/flatted/golang/pkg/flatted` because local `web/node_modules` exists and contains Go files. That is outside first-party Go code and should be excluded in a follow-up scanner configuration. The first-party findings below remain useful.

First-party patterns reported:

1. `G402` TLS verification bypass.
   - `internal/install/client.go:290`: `InsecureSkipVerify: skipVerify`.
   - `internal/client/client.go:333`: `InsecureSkipVerify: c.TLSSkipVerify`.
   - This matches the manual crypto finding: TLS skip is a deliberate feature but creates first-connection MITM risk, partially mitigated by fingerprint TOFU.

2. `G122` filesystem TOCTOU in recursive chown.
   - `internal/install/dirs.go:63`: `filepath.WalkDir` callback calls `os.Chown(path, uid, gid)`.
   - Security relevance: root-run install can follow attacker-influenced symlink/path changes if `/var/lib/netsgo` or descendants are writable/raceable before ownership hardening.

3. `G124` cookie secure-attribute warning.
   - `internal/server/auth_middleware.go:176` and `:189`.
   - Triage: cookies set `HttpOnly`, `SameSite=Strict`, `/api`, and `Secure` only when the request is HTTPS/trusted-proxy HTTPS. Gosec cannot model that conditional. This is not a code-confirmed missing flag, but it highlights deployment risk when management is exposed over plain HTTP or reverse proxy headers are misconfigured.

4. `G115` integer conversion warnings.
   - Examples: protocol header length casts in `pkg/protocol/data_channel.go:23,27`, `pkg/protocol/stream_header.go:68`, `pkg/protocol/stream_header_helpers.go:36`, `pkg/mux/udp_frame.go:25`; runtime revision casts in `internal/server/server_expose_unified.go:82,96`, `internal/server/control_loop.go:199`, `internal/client/unified_tunnel.go:764`; counter casts in `internal/server/http_tunnel_proxy.go:26`, `internal/server/bandwidth.go:457,467`, `internal/server/udp_proxy.go:375`; uptime casts in `internal/server/console_api.go:455,464`, `pkg/sysinfo/install_time_darwin.go:22`.
   - Triage: several are likely false positives because code already bounds lengths (`MaxUDPPayload`, data-stream max header len), `int` read counts cannot be negative on successful reads, and revisions should be positive. Still worth adding explicit checked conversions to document invariants and quiet scanners.

5. `G702`/`G204` command execution warnings.
   - `syscall.Exec` wrappers in `cmd/netsgo/exec_unix.go:8`, `internal/install/exec_unix.go:8`, `internal/manage/exec_unix.go:8`; `journalctl` exec in `internal/manage/server.go:91`; release signer `ssh-keygen` in `cmd/netsgo-release-sign/main.go:213`.
   - Triage: inspected paths use argument arrays, not shell strings. Privileged re-exec is intentional. Risk remains local PATH/privilege boundary for resolving `sudo` and root-run workflows.

6. `G703` path traversal taint warnings in release signer.
   - `cmd/netsgo-release-sign/main.go:156,178,185,207,210,217` for user-supplied file paths and temporary key files.
   - Triage: release signing utility intentionally reads/writes caller-selected paths; not remotely reachable. Still useful to constrain outputs in CI and avoid running it with untrusted path arguments.

7. `G706` log injection warnings.
   - Many logs in `internal/server/control_auth.go`, `internal/server/control_loop.go`, and `internal/server/admin_store.go` use client-supplied or protocol-supplied fields. This matches the manual injection finding about CR/LF/control-character log injection.

## Missing scanner coverage

- `shellcheck`, `hadolint`, `trivy`, `osv-scanner`, `semgrep`, `gitleaks`, `staticcheck`, and preinstalled `gosec` were not present locally when checked. `gosec` was run via `go run`.
- Secret regex search was performed with the repository search tool and found expected examples/dev/test values plus ignored local `sessions/` transcripts; no committed production private key material was confirmed from that search.

## Follow-up

1. Upgrade Go to a standard library version containing fixes for `GO-2026-5039` and `GO-2026-5037`.
2. Move/remove `shadcn` from web runtime dependencies if it is generator-only, then rerun `bun audit`.
3. Add scanner config to exclude `web/node_modules`, generated build outputs, and local harness/session directories.
4. Add explicit checked numeric conversions at protocol/runtime boundaries so security-critical invariants are executable and scanner-readable.
5. Treat the `G122` chown walk as a real local hardening item for root-run installs.
