# Persistence and install security audit

## Scope

Audited persistence and local-install attack surface for:

- SQLite schema/migrations: `internal/server/storage_schema.go`, `internal/server/migrations/*`, `internal/storage/sqlite.go`
- Server/admin persistence paths: `internal/server/admin_security_store.go`, `internal/server/admin_store.go`, `internal/server/init.go`, `internal/server/server_bootstrap.go`, `internal/server/tls.go`, `internal/server/store.go`
- Client local state: `internal/client/state_store.go`, `internal/client/state.go`, `cmd/netsgo/cmd_client.go`
- Data directory and managed-service paths: `pkg/datadir/datadir.go`, `internal/install/dirs.go`, `internal/svcmgr/{layout,unit,env,binary_linux}.go`, `cmd/netsgo/cmd_manage.go`, `internal/manage/admin_user.go`, `cmd/netsgo/cmd_upgrade.go`, `pkg/updater/*`
- Install/upgrade scripts: `scripts/install.sh`, `scripts/upgrade.sh`, `scripts/common-update.sh`

No project-wide build/test/lint/format/security scanner commands were run. This review used file reads/searches only and wrote this report.

## Confirmed findings

### PI-01: Auto-update cache is under a predictable, attacker-creatable `/tmp` tree used by root scripts

**Severity:** High for local privilege hardening / supply-chain updater safety  
**Affected files:** `scripts/common-update.sh`, copied into `scripts/install.sh` and `scripts/upgrade.sh`

**Evidence:**

- Default cache root is predictable and world-shared: `scripts/common-update.sh:340-342` sets `default_cache_root()` to `${TMPDIR:-/tmp}/netsgo-update-cache`.
- The cache path is tag/platform-derived: `scripts/common-update.sh:344-349` returns `$root/$tag/$platform`, with no ownership, type, or permission validation on `$root` or parents.
- Cache directories are created with plain `mkdir -p`: `scripts/common-update.sh:363-370`, `382-390`, and `406-413` call `mkdir -p "$cache_dir"` before writing files there.
- Root-running install/upgrade scripts use the same helper block and then download/reuse artifacts from that cache: `scripts/install.sh:588-594` and `scripts/upgrade.sh:603-609` compute `cache_dir`, cache release metadata/checksums/archive, and extract the cached archive.
- The script writes through attacker-controllable paths with ordinary redirections/download outputs: `scripts/common-update.sh:177-189` writes `tmp_out="${out}.part.$$"` via `curl -o "$tmp_out"` then `mv "$tmp_out" "$out"`; signature temp files are also created in default temp space via `mktemp` at `scripts/common-update.sh:266-268` and `281-283`.

**Exploit preconditions:** An unprivileged local user on the same host can pre-create or race entries under `/tmp/netsgo-update-cache` before an administrator runs `scripts/install.sh` or `scripts/upgrade.sh` as root. The administrator does not need to set `NETSGO_UPDATE_CACHE_DIR`; the default path is enough.

**Impact:** The signature/checksum flow prevents a straightforward malicious NetsGo binary swap, but the root process still writes, removes, and reuses files inside a directory tree that may be owned and mutable by another local user. That is a local attack surface for symlink/race/path-substitution bugs around `release.json`, `checksums.txt`, signatures, and archives. At minimum, this enables reliable local DoS of installs/upgrades; depending on platform filesystem protections and timing, it can become root file clobbering via `curl -o`/`mv` on attacker-controlled path components.

**Recommendation:** Create a root-owned private cache (`mktemp -d` or `/var/cache/netsgo/update` with owner/mode checks), require every cache path component to be a directory owned by root and not group/world-writable, set mode `0700` or `0750`, and open temporary outputs with no-follow/exclusive semantics where possible. If `NETSGO_UPDATE_CACHE_DIR` is retained, reject unsafe ownership/mode before use.

### PI-02: Local database compromise exposes plaintext MFA seed and setup challenge secrets

**Severity:** Medium / defense-in-depth gap  
**Affected files:** `internal/server/migrations/006_admin_security.sql`, `internal/server/admin_security_store.go`, `internal/storage/sqlite.go`

**Evidence:**

- Migration stores TOTP secrets directly in `admin_users.totp_secret`: `internal/server/migrations/006_admin_security.sql:14-15` adds `totp_enabled` and `totp_secret TEXT NOT NULL DEFAULT ''`.
- Setup challenge stores the generated TOTP secret and provisioning URL as JSON in `admin_auth_challenges.session_json`: `internal/server/admin_security_store.go:236-243` marshals `"secret": key.Secret()` and `"url": key.URL()` and passes that string to `StoreAuthChallenge`.
- Confirm persists the raw secret: `internal/server/admin_security_store.go:293-294` executes `UPDATE admin_users SET totp_enabled = 1, totp_secret = ?`.
- Verification uses the raw secret from the DB: `internal/server/admin_security_store.go:117-118` calls `totp.Validate(code, user.TOTPSecret)`.
- SQLite files are permission-restricted but not encrypted: `internal/storage/sqlite.go:140-148` creates the DB with mode `0600`, and `internal/storage/sqlite.go:151-157` chmods DB/WAL/SHM files to `0600`.

**Exploit preconditions:** Attacker can read the server SQLite database or backup/snapshot while running as the `netsgo` service user, root, or through a backup/log collection path with access to `/var/lib/netsgo/server/netsgo.db` or equivalent `--data-dir`.

**Impact:** A database read yields the admin TOTP seed (and pending setup challenge seed while present), allowing generation of valid MFA codes. The database also stores password hashes, sessions, JWT secret, API key hashes, and token hashes; the plaintext TOTP seed is the most directly reusable MFA material.

**Recommendation:** Treat this as accepted only if the threat model says local DB read equals full admin compromise. Otherwise, encrypt TOTP seeds/challenge session JSON with a host-local key protected outside the DB, shorten setup challenge TTLs if not already short enough for the product target, and document that DB backups contain live MFA secrets.

### PI-03: Client service credential is stored in plaintext in both env file and local SQLite state

**Severity:** Medium  
**Affected files:** `internal/svcmgr/env.go`, `internal/svcmgr/env_linux.go`, `internal/client/state_store.go`, `internal/client/state.go`

**Evidence:**

- Client managed-service env includes raw enrollment key: `internal/svcmgr/env.go:57-64` writes `NETSGO_SERVER` and `NETSGO_KEY` values.
- The env file is mode `0640`: `internal/svcmgr/env.go:139-142` writes with `0o640`; Linux ownership intentionally keeps it readable by the `netsgo` service group: `internal/svcmgr/env_linux.go:29-37` comments that runtime service processes need read access and chmods `0640`.
- Client persistent identity schema stores raw token: `internal/client/state_store.go:42-47` creates `client_identity` with `token TEXT NOT NULL DEFAULT ''`.
- Client save path persists the token unencrypted: `internal/client/state_store.go:82-91` inserts/updates `token = excluded.token`; `internal/client/state.go:78-83` builds persisted state with `Token: c.Token`.
- Client state path is under `<data-dir>/client/netsgo.db`: `internal/client/state.go:14-19`; the default data-dir is `/var/lib/netsgo` under systemd or `$HOME/.local/state/netsgo` otherwise: `pkg/datadir/datadir.go:8-20`.

**Exploit preconditions:** Attacker can read `/etc/netsgo/services/client.env` as a member of the `netsgo` group, read the client DB as the service user/root, or read a user-mode client data directory.

**Impact:** Raw `NETSGO_KEY` may allow initial registration until disabled/expired/max-used. Raw client token allows reconnecting as that install ID until token expiry/revocation, subject to server-side install ID binding. This is mostly local-secret exposure rather than remote SQL/auth bypass.

**Recommendation:** If group-read env is required, keep `NETSGO_KEY` short-lived/single-use and scrub it from env after token exchange where feasible. Consider migrating client token storage to OS keychain/secret service or encrypting it with a machine-local key; document that copying the client DB copies client identity.

### PI-04: Migrations are forward-only and run without a database backup/restore point

**Severity:** Medium operational availability / data-loss risk  
**Affected files:** `internal/storage/sqlite.go`, `internal/server/migrations/*`, `pkg/updater/upgrade.go`

**Evidence:**

- Migration application executes `migration.Up` and records the migration in a transaction: `internal/storage/sqlite.go:226-252` begins a transaction, runs `tx.Exec(migration.Up)`, inserts `schema_migrations`, then commits.
- The migration parser requires a Down section syntactically, but does not require it to contain rollback SQL: `internal/server/storage_schema.go:155-161` only checks that `-- Down:` exists; `internal/server/storage_schema.go:102-107` only rejects empty `Up`.
- Current migrations have empty or partial Down sections: `internal/server/migrations/001_server_runtime_schema.sql:146-147`, `002_rebuild_tunnels_without_registered_client_fk.sql:30-31`, `004_tunnel_created_at.sql:9-10`, `005_unified_tunnel_storage.sql:227`, and `006_admin_security.sql:52-53`; `003_tunnel_stable_id.sql:10-12` only drops the index, not the added column.
- Storage rejects unknown future applied migrations: `internal/storage/sqlite.go:220-223` calls `rejectUnknownAppliedMigrations`; `internal/storage/sqlite.go:257-276` errors on names not in the embedded migration list. This prevents silent downgrades but also means rollback to older binaries is blocked once a newer migration is applied.
- Binary upgrade rollback backs up/restores only `/usr/local/bin/netsgo`: `pkg/updater/upgrade.go:47-67` backs up and replaces the binary, and `pkg/updater/update.go:39-54` restores the binary/restarts services. There is no corresponding SQLite backup before the new binary starts and applies migrations.

**Exploit/failure preconditions:** An upgrade starts a new binary that applies a bad or incompatible migration, or an operator force-downgrades after a newer schema was applied.

**Impact:** If a migration corrupts or semantically changes data, the built-in rollback path can restore the old binary but not the old database. Because old binaries reject unknown applied migrations, rollback may leave services unable to start even though the binary was restored. This is not a remote exploit by itself, but it increases recovery risk for a security-sensitive store containing admin/session/client state.

**Recommendation:** Before starting upgraded services, create a private, mode-`0600` SQLite backup including WAL state (or use SQLite backup API) and tie it to the upgrade transaction. Document downgrade behavior, and either implement real Down migrations or keep explicit backup/restore as the supported rollback.

### PI-05: Migration 005 can fail on legacy data values not normalized into new CHECK constraints

**Severity:** Medium availability / migration compatibility  
**Affected files:** `internal/server/migrations/005_unified_tunnel_storage.sql`

**Evidence:**

- New `tunnels` table constrains `desired_state` to `running|stopped` and `runtime_state` to `pending|active|offline|idle|error`: `internal/server/migrations/005_unified_tunnel_storage.sql:55-69`.
- Legacy migration maps only selected values: `internal/server/migrations/005_unified_tunnel_storage.sql:119-120` converts `desired_state = 'paused'` to `stopped`, otherwise copies legacy `desired_state`; it converts only `runtime_state = 'exposed'` to `active`, otherwise copies legacy `runtime_state`.
- The original `001` schema stores both values as unconstrained text: `internal/server/migrations/001_server_runtime_schema.sql:126-127` defines `desired_state TEXT NOT NULL` and `runtime_state TEXT NOT NULL` without CHECK constraints.

**Exploit/failure preconditions:** A legacy DB contains unexpected `desired_state` or `runtime_state` strings, whether from older bugs, manual edits, or a partially corrupt DB.

**Impact:** Migration 005 fails when inserting into the new constrained table. Since migrations run during DB open (`internal/storage/sqlite.go:54-57`), service startup fails until the DB is manually repaired. Transactional DDL should prevent the failed migration from being committed, but no backup is taken and startup is still denied.

**Recommendation:** Add preflight validation with actionable errors before table rebuild, or map all unknown legacy values to safe defaults (`stopped`/`error`) while preserving the original error text for audit.

### PI-06: Managed service units/env rendering do not escape paths or environment values

**Severity:** Low to Medium depending on who controls install inputs  
**Affected files:** `internal/svcmgr/unit.go`, `internal/svcmgr/env.go`

**Evidence:**

- Systemd unit `ExecStart` is rendered by simple string interpolation: `internal/svcmgr/unit.go:66-68` returns `"%s %s --data-dir %s"`; `internal/svcmgr/unit.go:76-93` inserts `EnvironmentFile=%s` and `ExecStart=%s` without systemd escaping.
- Env file writer emits raw `KEY=value` lines: `internal/svcmgr/env.go:131-136` writes key, `=`, raw value, newline. It rejects only keys with `NETSGO_INIT_`: `internal/svcmgr/env.go:119-127`; it does not reject or quote newlines in values.
- Server env values include paths and server address: `internal/svcmgr/env.go:31-54`; client env values include server URL/key/fingerprint: `internal/svcmgr/env.go:57-71`.

**Exploit preconditions:** Attacker can influence install-time values written into service files (for example, a malicious local operator input or automation variable). This is not exposed directly to unauthenticated remote users in the inspected paths.

**Impact:** Whitespace/newline/control characters in values can produce broken units/env files or unintended additional env lines. Because service files are written as root during managed install, this is a local configuration injection/DoS risk and a maintainability hazard.

**Recommendation:** Validate service env values for `\n`/`\r` and other systemd-env metacharacters, quote according to systemd environment-file rules, and use systemd-safe escaping or fixed paths for `ExecStart`/`EnvironmentFile`.

## Non-findings / positive controls

- **Parameterized SQL dominates inspected persistence paths.** Admin lookups and mutations use placeholders for user input, e.g. admin login query `WHERE username = ?` at `internal/server/admin_store.go:579-584`, allowed port inserts at `internal/server/admin_store.go:507-517`, TOTP/user updates at `internal/server/admin_security_store.go:293-304`, passkey inserts at `internal/server/admin_security_store.go:606-608`, and client token operations at `internal/server/admin_store.go:1821-1837` and `1873-1879`. The main dynamic SQL helpers concatenate constant column lists/where fragments controlled by code, e.g. `adminUserSelectColumns()` is a fixed string at `internal/server/admin_store.go:553-555`, and `loadRegisteredClient` receives callsite-fixed `WHERE install_id = ?` at `internal/server/admin_store.go:779-780`.
- **SQLite DB/WAL/SHM file permissions are intentionally private.** `internal/storage/sqlite.go:30-33` creates parent dirs and DB, `internal/storage/sqlite.go:140-148` creates DB file mode `0600`, and `internal/storage/sqlite.go:151-157` chmods DB/WAL/SHM to `0600` before and after migrations.
- **Read-only init/state checks do not create or migrate DBs.** `internal/storage/sqlite.go:65-92` opens read-only with mode `ro`; `internal/server/init.go:30-46` treats missing DB/schema as uninitialized without creating state; client identity read-only load uses `storage.OpenReadOnly` at `internal/client/state_store.go:98-105`.
- **Release downloads are constrained and verified.** Official URL allowlist is in `scripts/common-update.sh:167-175`; release detail schema validates project/version/assets at `scripts/common-update.sh:224-239`; checksums are verified at `scripts/common-update.sh:242-258`; signatures are verified via embedded Ed25519/OpenSSH public keys at `scripts/common-update.sh:261-309`; scripts verify the extracted binary version before install/upgrade at `scripts/install.sh:596-599` and `scripts/upgrade.sh:611-614`.
- **Managed runtime directories are tightened.** `internal/install/dirs.go:23-34` creates root/runtime/locks dirs with `0750`, and `internal/install/dirs.go:49-55` chowns root, runtime dir, and locks dir to the service account when it exists.
- **Auto-generated TLS private key is private.** `internal/server/tls.go:156-165` creates the auto TLS directory `0700` and writes certificate/key files `0600`.
- **Reset-admin command avoids live DB writes.** `internal/manage/admin_user.go:23-28` takes the server lock and reports that the server must be stopped before resetting admin credentials; managed default path reruns under sudo via `cmd/netsgo/cmd_manage.go:82-86`.

## Risky assumptions

- The design effectively treats local read access to `/var/lib/netsgo/server/netsgo.db` as full admin compromise because JWT secret, TOTP seed, session records, passkey credential JSON, API key hashes, client token hashes, and recovery-code hashes are all stored there.
- The service user/group boundary is trusted. `client.env` is intentionally group-readable (`0640`) so members of the `netsgo` group can read `NETSGO_KEY`.
- SQLite DDL in multi-statement migrations is assumed to be safely transactional under the `modernc.org/sqlite` driver because migrations are executed inside `tx.Exec(migration.Up)` (`internal/storage/sqlite.go:226-245`). This should be explicitly tested for every rebuild-style migration.
- The release-index trust model depends on embedded public keys in the shell scripts staying synchronized with release signing keys (`scripts/common-update.sh:8-16`) and assumes one valid signature mechanism (`openssl` or `ssh-keygen`) is available.
- Install/upgrade scripts assume the local root execution environment is trustworthy. Environment variables such as `NETSGO_UPDATE_CACHE_DIR`, `NETSGO_INSTALL_TTY`, `NETSGO_UPGRADE_TTY`, and `NETSGO_INSTALLED_BIN` influence behavior in root-run scripts (`scripts/common-update.sh:344-349`, `scripts/install.sh:503-506`, `scripts/upgrade.sh:510-516`, `scripts/upgrade.sh:560-563`).

## Follow-up checks for Main

1. Add targeted tests for unsafe cache roots: pre-create `/tmp/netsgo-update-cache` as another user/with world-writable mode or symlinked components and assert install/upgrade helpers reject it before any download/write.
2. Add migration compatibility fixtures with unexpected legacy `desired_state`/`runtime_state` values to confirm migration 005 either fails with a clear preflight error or normalizes safely.
3. Add an upgrade rollback integration/unit test proving that DB schema changes are backed up/restored or that downgrade is explicitly blocked with a recovery path.
4. Add env/unit rendering tests for newline/whitespace in `NETSGO_SERVER`, `NETSGO_KEY`, TLS paths, and data-dir values.
5. Decide whether plaintext TOTP seeds and client tokens are acceptable under the local threat model; if accepted, document backup-handling requirements and group membership implications.
