# Supply-chain / release / update audit

## Scope

Audited the requested supply-chain and release/update path: Go module manifests, Bun package manifests/locks, Tauri desktop Cargo/config, Dockerfile, release workflow, GoReleaser config, release-index builder, signing/update scripts, updater code, release-signing code, and desktop sidecar launch/build path. I did not run project-wide build/test/lint/format/security scanners.

## Files inspected

- `go.mod` (lines 1-77)
- `web/package.json` (lines 1-60) and selected `web/bun.lock` entries
- `desktop/package.json` (lines 1-30), selected `desktop/bun.lock` entries, `desktop/src-tauri/Cargo.toml` (lines 1-26), selected `desktop/src-tauri/Cargo.lock` entries, `desktop/src-tauri/tauri.conf.json` (lines 1-49), `desktop/src-tauri/capabilities/default.json` (lines 1-11), `desktop/src-tauri/src/lib.rs` (lines 1-620), `desktop/src-tauri/build.rs` (lines 1-3)
- `Dockerfile` (lines 1-79)
- `.github/workflows/release.yml` (lines 1-692)
- `.goreleaser.yaml` (lines 1-62)
- `scripts/release-index.mjs` (lines 1-218)
- `scripts/common-update.sh` (lines 1-478), `scripts/install.sh` (lines 1-608), `scripts/upgrade.sh` (lines 1-619)
- `scripts/sign-macos-app.sh` (lines 1-43), `scripts/package-macos-dmg.sh` (lines 1-44), `scripts/build-desktop-sidecar.sh` (lines 1-60), `scripts/validate-release-tag.sh` (lines 1-23), `scripts/validate-beta-increment.sh` (lines 1-39), `scripts/generate-standalone-update-scripts.mjs` (lines 1-35), `scripts/embed-release-public-keys.sh` (lines 1-42)
- `pkg/updater/release_index.go` (lines 1-100), `pkg/updater/check.go` (lines 1-176), `pkg/updater/upgrade.go` (lines 1-87), `pkg/updater/replace.go` (lines 1-117), relevant updater tests
- `internal/releasesign/sign.go` (lines 1-41), `cmd/netsgo-release-sign/main.go` (lines 1-329), relevant signing tests
- `internal/server/version_api.go` (lines 1-138), `internal/server/version_cache.go` (lines 1-95)
- `README.md` / `README.zh-CN.md` install/upgrade command evidence

## Confirmed findings

### 1. Update-check metadata is accepted from the network without signing or URL validation, enabling false update prompts / release-detail redirection if the release-index branch or transport endpoint is compromised

**Evidence**

- Runtime update checks fetch `latest.json` directly from CNB then GitHub raw content: `pkg/updater/release_index.go:14-15`.
- The server uses those defaults for update checks: `internal/server/version_api.go:32-34`.
- The parser only validates schema/project/channel tag syntax and rejects unknown channel names; it does not validate `generated_at`, `release_urls`, provider names, URL hosts, or any signature over the index: `pkg/updater/release_index.go:39-69`.
- `ReleaseChannel` includes `ReleaseURLs []ProviderURL`, and `ProviderURL` includes arbitrary `URL` / `RequiresAuth` fields: `pkg/updater/release_index.go:28-37`; these fields are decoded but not constrained in `ParseReleaseIndex`.
- `FetchReleaseIndex` accepts the first HTTP 200 body that parses: `pkg/updater/release_index.go:72-95`.
- The update recommendation then trusts this parsed index to choose a latest version and build an upgrade command: `pkg/updater/check.go:75-95` and `pkg/updater/check.go:99-102`.
- The release workflow publishes `release-index` by force-pushing a worktree to that branch: `.github/workflows/release.yml:515-530`; release detail URLs are generated as raw `release-index` branch URLs in `scripts/release-index.mjs:81-87` and `scripts/release-index.mjs:193-201`.

**Exploit preconditions**

- Attacker can alter the `release-index` branch/content, compromise one of the raw update endpoints, or otherwise feed a forged `latest.json` to a server performing version checks.
- This does not by itself bypass binary verification in `install.sh` / `upgrade.sh`; those scripts independently download `checksums.txt` and require a valid embedded-key signature before extracting an archive (`scripts/common-update.sh:382-403`, `scripts/common-update.sh:406-427`).

**Impact**

- Remote false-positive update notification and operator social-engineering path: the authenticated UI can be made to report an attacker-selected higher semver release and show the one-line root upgrade command (`pkg/updater/check.go:99-102`).
- If future code starts consuming `ReleaseURLs` from `ReleaseChannel`, the parser currently permits arbitrary providers/URLs. The shell scripts have an allowlist for release-detail file downloads (`scripts/common-update.sh:167-180`), but the Go update-check path has no equivalent validation.

**Why this is still security-relevant even though archives are signed**

- The signed manifest protects downloaded release assets, not the update-check metadata currently used to prompt administrators. A compromised index can induce root operators to run a network-fetched shell script unnecessarily, or suppress/alter update visibility.

### 2. Official install/upgrade UX executes an unsigned bootstrap script from `netsgo.zs.uy`; binary artifacts are signed, but the first code executed as root is not authenticated by the embedded release key

**Evidence**

- README installation command is `curl -fsSL https://netsgo.zs.uy/install.sh | sh`: `README.md:59-63` and `README.zh-CN.md:59-63`.
- README upgrade command is `curl -fsSL https://netsgo.zs.uy/upgrade.sh | sh -s -- -y`: `README.md:65-69` and `README.zh-CN.md:65-69`.
- The product itself recommends the same upgrade bootstrap command: `pkg/updater/check.go:99-102`; the frontend tests assert it as the rendered upgrade command (`web/src/components/custom/common/VersionUpdateIndicator.test.tsx:72-81`).
- The standalone script contains embedded release public keys and verifies `checksums.txt` signatures before extracting/running the downloaded `netsgo` binary: `scripts/common-update.sh:8-16`, `scripts/common-update.sh:291-309`, `scripts/common-update.sh:382-403`, `scripts/common-update.sh:406-427`.
- However, the bootstrap shell script itself is fetched from `https://netsgo.zs.uy/...` and immediately piped to `sh` before any signature/key check can run (`README.md:62`, `README.md:68`, `pkg/updater/check.go:101`).

**Exploit preconditions**

- Attacker compromises `netsgo.zs.uy`, its hosting/CDN/DNS/TLS termination, or a trusted publishing path that controls `install.sh` / `upgrade.sh` served from that host.
- User follows documented install/upgrade instructions, commonly as root or with enough privileges to install/replace `/usr/local/bin/netsgo` and manage systemd services.

**Impact**

- Full remote code execution with the privileges of the shell running the one-liner. The later `checksums.txt` signature verification cannot protect against malicious code injected into the bootstrap script before it reaches those verification steps.

**Existing mitigating controls**

- Once the authentic script is executing, release asset downloads are constrained to hard-coded GitHub/CNB URL prefixes (`scripts/common-update.sh:167-180`) and checksums are signature-verified using embedded Ed25519/OpenSSH public keys (`scripts/common-update.sh:262-309`).

### 3. Release workflow third-party actions are tag-pinned instead of commit-SHA pinned, so a compromised action tag can run with release/publish secrets and write permissions

**Evidence**

- Global workflow permissions are `contents: read` (`.github/workflows/release.yml:8-10`), but release/publish jobs elevate permissions:
  - `binaries` has `contents: write`: `.github/workflows/release.yml:227-231`.
  - `publish-release-index` has `contents: write`: `.github/workflows/release.yml:292-296`.
  - `docker` has `packages: write`: `.github/workflows/release.yml:555-560`.
  - `scan-docker` has `security-events: write`: `.github/workflows/release.yml:655-660`.
- The workflow uses mutable action tags rather than immutable SHAs, including examples:
  - `actions/checkout@v6`: `.github/workflows/release.yml:19`, `34`, `91`, `213`, `235`, `277`, `305`, `570`, `663`.
  - `oven-sh/setup-bun@v2`: `.github/workflows/release.yml:41`, `98`, `327`.
  - `actions/cache@v5`: `.github/workflows/release.yml:42`.
  - `dtolnay/rust-toolchain@stable`: `.github/workflows/release.yml:95`.
  - `goreleaser/goreleaser-action@v6`: `.github/workflows/release.yml:252`.
  - Docker actions at `.github/workflows/release.yml:577-580`, `584-589`, `607-636`, and Trivy/SARIF upload at `.github/workflows/release.yml:679-688`.
- Secrets exposed to release steps include `NETSGO_RELEASE_SIGNING_KEY_PEM` in `binaries` and `publish-release-index`: `.github/workflows/release.yml:248-258` and `.github/workflows/release.yml:348-381`; CNB mirror credentials at `.github/workflows/release.yml:272-275` and `.github/workflows/release.yml:300-303`; DockerHub/CNB publishing credentials at `.github/workflows/release.yml:563-568`.

**Exploit preconditions**

- An upstream action maintainer account/repository or tag is compromised, or an action tag is moved to malicious code.
- A release tag push triggers this workflow.

**Impact**

- In jobs with elevated permissions/secrets, malicious action code can steal `NETSGO_RELEASE_SIGNING_KEY_PEM`, publish tampered GitHub releases, force-push `release-index`, push Docker images, or exfiltrate CNB/DockerHub credentials. The signing key exposure is especially high-impact because it lets an attacker produce checksums accepted by `install.sh` / `upgrade.sh` (`scripts/common-update.sh:262-309`).

### 4. macOS desktop artifacts are ad-hoc signed and not notarized; release DMGs are uploaded as trusted desktop installers but provide no Apple identity assurance

**Evidence**

- Release workflow explicitly sets `CODESIGN_IDENTITY: "-"` before signing macOS bundles: `.github/workflows/release.yml:121-127`.
- `scripts/sign-macos-app.sh` uses that identity for each executable and then the app bundle: `scripts/sign-macos-app.sh:4-6`, `scripts/sign-macos-app.sh:28-36`. In `codesign`, `-` is ad-hoc signing, not Developer ID signing.
- The DMG packaging script creates a UDZO DMG from the app folder but performs no DMG signing or notarization: `scripts/package-macos-dmg.sh:31-41`.
- The release workflow uploads `.dmg` assets to GitHub release assets after checksum/signature generation: `.github/workflows/release.yml:360-381` and includes DMGs in the Docker/release-index artifact set (`scripts/release-index.mjs:49-56`, `.github/workflows/release.yml:493-494`).

**Exploit preconditions**

- User downloads/installs macOS desktop releases outside a strongly verified channel, or a mirror/release asset is tampered with and the user does not manually verify checksums/signatures.

**Impact**

- Users receive no Apple Developer ID identity or notarization guarantee. Gatekeeper/user prompts may not provide a reliable publisher identity, and enterprise/macOS security policy may treat the app as untrusted. The project-level checksum signatures protect users only if they manually verify them; there is no automatic desktop updater verification path in the Tauri config (see non-findings).

### 5. Desktop sidecar path resolution can execute an untrusted `binaries/netsgo-*` from the current working directory in dev/fallback scenarios

**Evidence**

- The desktop app launches a sidecar binary from `resolve_sidecar_path()` and passes it user-controlled connection parameters as CLI args/env (`desktop/src-tauri/src/lib.rs:418-465`).
- `resolve_sidecar_path` returns the first existing path from `sidecar_path_candidates`: `desktop/src-tauri/src/lib.rs:492-503`.
- Candidate paths include packaged executable directory paths first, but then include current working directory fallbacks: `cwd.join(&relative)`, `cwd.join("src-tauri").join(&relative)`, and finally `PathBuf::from(relative)`: `desktop/src-tauri/src/lib.rs:512-529`.
- The relative filename is derived from `binaries/netsgo-<target>`: `desktop/src-tauri/src/lib.rs:540-546`.
- The sidecar is built into `desktop/src-tauri/binaries/netsgo-${target_triple}` by the release script: `scripts/build-desktop-sidecar.sh:48-53`.

**Exploit preconditions**

- Packaged sidecar candidates next to the app are absent/inaccessible, or a developer/test build is launched from an attacker-controlled/current working directory containing `binaries/netsgo-<target>` or `src-tauri/binaries/netsgo-<target>`.
- User triggers client connection start in the desktop app.

**Impact**

- The desktop app can execute attacker-supplied code under the user account, and if a key is supplied it is passed to that process via `NETSGO_KEY` (`desktop/src-tauri/src/lib.rs:443-461`). This is most plausible for dev/broken packaging contexts because packaged app paths are tried first; I did not confirm a normal packaged release can be forced past the packaged candidates.

## Non-findings / mitigating observations

- **Go modules are checksum-locked and no local replace directives were found in `go.mod`.** Direct dependencies are declared in `go.mod:5-21`, indirects in `go.mod:23-77`; no `replace` directive was present in inspected `go.mod`. This reduces accidental local-path dependency injection risk for Go builds.
- **Bun installs are lockfile-enforced in CI.** Web release build uses `bun install --frozen-lockfile` (`.github/workflows/release.yml:48-53`) and desktop uses the same for `desktop` (`.github/workflows/release.yml:98-101`). The workflow rejects non-Bun lockfiles in frontend workspaces (`.github/workflows/release.yml:35-40`).
- **Bun lockfiles include integrity data for many packages.** Example entries: web `lucide-react` includes sha512 integrity at `web/bun.lock:1034`, web `vite` is locked to `7.3.3` at `web/bun.lock:1394`, desktop `@tauri-apps/cli` is locked to `2.11.1` with platform optional deps at `desktop/bun.lock:181-183`. The package manifests still use broad semver ranges (`web/package.json:20-42`, `web/package.json:45-58`, `desktop/package.json:14-28`), so reproducibility depends on honoring the locks.
- **No npm/git/path dependencies were observed in package manifests.** `web/package.json:14-58` and `desktop/package.json:13-28` list registry package names/semver ranges, not `git+`, `file:`, or remote tarball specifiers.
- **Rust dependencies are lockfile-backed.** `desktop/src-tauri/Cargo.lock:1-15` shows crates.io source plus checksums; `Cargo.toml` uses registry versions for Tauri/serde deps (`desktop/src-tauri/Cargo.toml:17-25`). I did not see local path/git dependencies in `Cargo.toml`.
- **Tauri CSP is restrictive and no Tauri updater is configured.** CSP uses `default-src 'self'`, `script-src 'self'`, object blocking, and limited IPC/connect sources (`desktop/src-tauri/tauri.conf.json:21-32`). Bundle config contains only `externalBin` and icons (`desktop/src-tauri/tauri.conf.json:35-48`); no Tauri updater endpoint/public key was present in inspected config.
- **Tauri capabilities do not grant arbitrary shell execution through frontend APIs.** Default capability includes `core:default`, `core:path:allow-resolve-directory`, and `opener:default` only (`desktop/src-tauri/capabilities/default.json:6-10`). The Rust backend invokes `tauri_plugin_shell` directly for the packaged sidecar (`desktop/src-tauri/src/lib.rs:457-465`), not via frontend shell permissions.
- **Release asset manifest signing is implemented with Ed25519 and OpenSSH signatures.** Signing key is parsed as PKCS#8 Ed25519 (`internal/releasesign/sign.go:10-23`); raw signatures use Ed25519 (`internal/releasesign/sign.go:26-28`); release signer writes `.sig` and `.sshsig` (`cmd/netsgo-release-sign/main.go:173-188`, `cmd/netsgo-release-sign/main.go:191-220`). The workflow verifies embedded public keys match the private signing secret before release (`.github/workflows/release.yml:248-251`).
- **Release index builder verifies local file hashes before writing release details.** It parses `checksums.txt`, stats signature files, recomputes each artifact SHA-256, and rejects mismatches (`scripts/release-index.mjs:120-150`). It also requires at least one installable asset and signature URL entries (`scripts/release-index.mjs:160-187`).
- **Install/upgrade scripts constrain release asset URLs once the authentic script is running.** `official_url_allowed` only permits hard-coded GitHub/CNB release/raw prefixes (`scripts/common-update.sh:167-180`), and all release-detail referenced files are downloaded through `download_official` (`scripts/common-update.sh:208-222`, `scripts/common-update.sh:399-421`).
- **Upgrade script prevents unsigned downgrades by default.** It compares installed vs target versions and requires `-f` for downgrades (`scripts/upgrade.sh:589-598`), then verifies downloaded binary version exactly matches the selected tag (`scripts/upgrade.sh:611-614`).
- **Docker runtime is distroless nonroot.** Final stage uses `gcr.io/distroless/static-debian12:nonroot` (`Dockerfile:51`), copies only the built `netsgo` binary (`Dockerfile:55`), exposes 9527 (`Dockerfile:57`), and starts `netsgo server` (`Dockerfile:61-62`). This is materially better than running in the build image as root.
- **Docker release includes a Trivy gate for stable tags.** The release workflow scans the GHCR image and sets exit code 1 for non-prerelease tags (`.github/workflows/release.yml:669-687`). This is a scanner in CI; I did not run it.
- **GoReleaser uses reproducibility-oriented flags.** Go builds set `CGO_ENABLED=0`, `-trimpath`, commit timestamp, and ldflags for version metadata (`.goreleaser.yaml:8-39`).

## Risky assumptions

- The `netsgo.zs.uy` hosting path for `install.sh` / `upgrade.sh` was not present in the requested files. The audit assumes it serves the repository scripts byte-for-byte; if it is generated, proxied, or rewritten, that serving path needs separate review.
- I did not inspect repository branch protection / GitHub environment protection / secret access policies; workflow YAML alone cannot prove who can push `v*` tags or force-update `release-index` outside Actions.
- I did not run vulnerability scanners against Go/Bun/Cargo dependencies. Version-specific CVE conclusions are intentionally not made here.
- The desktop sidecar fallback issue may be limited to dev/broken packaging contexts because packaged executable-directory candidates are tried first (`desktop/src-tauri/src/lib.rs:512-520`). I did not perform a packaged macOS/Windows runtime test.
- I did not inspect generated Tauri bundle metadata under build outputs; source config has no updater block, but generated artifacts may include platform-specific defaults.

## Follow-up checks for Main

1. Verify whether `https://netsgo.zs.uy/install.sh` and `/upgrade.sh` are immutable/static copies of `scripts/install.sh` and `scripts/upgrade.sh`, and whether the hosting path is protected equivalently to release artifacts.
2. Check GitHub repository settings: protected tags for `v*`, protected `main` and `release-index`, required reviews/status checks, and whether Actions can force-push `release-index` only from this workflow.
3. Consider testing `ParseReleaseIndex` with malicious `release_urls` / unknown providers to confirm current acceptance behavior and decide whether Go update checks should reject arbitrary URL/provider metadata.
4. Inspect release artifacts after a real workflow run: confirm `checksums.txt` includes every uploaded CLI and desktop artifact, and confirm desktop artifacts are either documented as manually verified or signed/notarized with platform-native identity.
5. Review CI action pinning policy; high-risk release jobs should pin third-party actions by full commit SHA, especially jobs receiving `NETSGO_RELEASE_SIGNING_KEY_PEM`, CNB credentials, DockerHub token, or `contents: write` / `packages: write`.
6. For desktop, test a packaged app with the sidecar removed/renamed while CWD contains a malicious `binaries/netsgo-<target>` to determine whether fallback execution is reachable outside dev builds.
