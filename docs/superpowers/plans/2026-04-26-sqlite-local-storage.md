# SQLite Local Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace NetsGo's local JSON-file persistence with SQLite for server/client runtime data, and remove the install/manage JSON service manifest.

**Architecture:** Server runtime state lives in `<data-dir>/server/netsgo.db`; client runtime identity lives in `<data-dir>/client/netsgo.db`. `netsgo install/manage` uses systemd unit files, env files, fixed paths, and systemd active/enabled state as the source of truth instead of `/etc/netsgo/services/*.json`.

**Tech Stack:** Go 1.25, `database/sql`, `modernc.org/sqlite`, SQLite WAL mode, existing Go tests, Bun/Vite for embedded frontend verification.

---

## Scope Check

This is one feature because the storage paths, install detection, startup initialization, and tests all depend on the same persistence model. It should be implemented in the dedicated worktree at `/Users/zs/code/netsgo/.worktrees/sqlite-storage-redesign` on branch `codex/sqlite-storage-redesign`.

This plan intentionally does not migrate old JSON files. NetsGo has not shipped yet, so the new code should only create and read the new SQLite layout.

## File Structure

Create:
- `internal/storage/sqlite.go`: shared SQLite open, pragmas, and migration runner.
- `internal/storage/sqlite_test.go`: shared SQLite utility tests.
- `internal/server/storage_schema.go`: server DB schema, server DB open helper, server store constructor glue.
- `internal/server/storage_schema_test.go`: server schema tests.
- `internal/client/state_store.go`: client SQLite identity store.
- `internal/client/state_store_test.go`: client SQLite identity store tests.
- `internal/svcmgr/layout.go`: service role constants and fixed service layout helpers replacing JSON `ServiceSpec`.
- `internal/svcmgr/layout_test.go`: service layout tests.

Modify:
- `go.mod`, `go.sum`: add `modernc.org/sqlite`.
- `internal/server/admin_store.go`: replace JSON-backed `AdminStore` internals with SQLite tables and transactions.
- `internal/server/admin_store_test.go`: update paths and add DB transaction assertions.
- `internal/server/store.go`: replace JSON-backed `TunnelStore` internals with SQLite table access.
- `internal/server/store_test.go`: remove legacy JSON tests and add SQLite persistence assertions.
- `internal/server/traffic_store.go`: replace JSON snapshot persistence with SQLite bucket upsert/query.
- `internal/server/traffic_store_test.go`: update DB-backed traffic tests.
- `internal/server/server_bootstrap.go`: open one server DB and construct admin/tunnel/traffic stores from it.
- `internal/server/data_paths_test.go`: assert new `netsgo.db` path.
- `internal/server/init.go`: initialize server via SQLite admin store.
- `cmd/netsgo/cmd_server.go`: read initialization state from SQLite.
- `internal/client/state.go`: keep client methods but delegate persistence to `state_store.go`.
- `internal/client/state_path_test.go`, `internal/client/client_tls_test.go`: update from `client.json` to `netsgo.db`.
- `internal/svcmgr/spec.go`: remove JSON read/write responsibilities; keep no JSON manifest API.
- `internal/svcmgr/state.go`: inspect unit/env/data paths without spec file.
- `internal/svcmgr/unit.go`: accept `ServiceLayout` and parse unit metadata needed by inspect.
- `internal/svcmgr/env.go`: accept `ServiceLayout`.
- `internal/svcmgr/*_test.go`: remove spec tests and update state/unit/env tests.
- `internal/install/server.go`, `internal/install/client.go`, `internal/install/service_flow.go`: stop writing service JSON specs.
- `internal/install/*_test.go`: update install expectations.
- `internal/manage/server.go`, `internal/manage/client.go`, `internal/manage/uninstall_all.go`: stop reading service JSON specs; derive paths from layout and env/unit.
- `internal/manage/*_test.go`: update manage expectations.
- `docs/setup-manage-impl.md`, `docs/setup-manage-plan.md`: replace old JSON storage descriptions with SQLite and unit/env-derived manage state.

---

### Task 1: Shared SQLite Foundation

**Files:**
- Create: `internal/storage/sqlite.go`
- Create: `internal/storage/sqlite_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write the failing shared storage tests**

Create `internal/storage/sqlite_test.go`:

```go
package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenCreatesParentDirectoryAndAppliesPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "netsgo.db")
	db, err := Open(path, []Migration{{
		Name: "001_create_widgets",
		Up:   `CREATE TABLE widgets (id TEXT PRIMARY KEY, name TEXT NOT NULL);`,
	}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	assertPragmaValue(t, db, "journal_mode", "wal")
	assertPragmaValue(t, db, "foreign_keys", "1")

	if _, err := db.Exec(`INSERT INTO widgets (id, name) VALUES ('w1', 'Widget')`); err != nil {
		t.Fatalf("insert into migrated table failed: %v", err)
	}
}

func TestOpenRunsMigrationsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo.db")
	migrations := []Migration{{
		Name: "001_create_counter",
		Up:   `CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL); INSERT INTO counter (id, value) VALUES (1, 1);`,
	}}

	db1, err := Open(path, migrations)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("db1.Close() error = %v", err)
	}

	db2, err := Open(path, migrations)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer db2.Close()

	var value int
	if err := db2.QueryRow(`SELECT value FROM counter WHERE id = 1`).Scan(&value); err != nil {
		t.Fatalf("query migrated row failed: %v", err)
	}
	if value != 1 {
		t.Fatalf("migration should run once, value = %d", value)
	}
}

func assertPragmaValue(t *testing.T, db *sql.DB, name, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`PRAGMA ` + name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s failed: %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %q, want %q", name, got, want)
	}
}
```

- [ ] **Step 2: Run the new tests and verify they fail**

Run:

```bash
go test ./internal/storage -run 'TestOpen' -count=1
```

Expected: FAIL because `internal/storage` and `Open` do not exist.

- [ ] **Step 3: Add the SQLite driver dependency**

Run:

```bash
go get modernc.org/sqlite
```

Expected: `go.mod` and `go.sum` include `modernc.org/sqlite` and its transitive dependencies.

- [ ] **Step 4: Implement the shared SQLite opener**

Create `internal/storage/sqlite.go`:

```go
package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Migration struct {
	Name string
	Up   string
}

func Open(path string, migrations []Migration) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	if err := configure(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := applyMigrations(db, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func configure(db *sql.DB) error {
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA synchronous = NORMAL;`,
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA busy_timeout = 5000;`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("configure sqlite %q: %w", statement, err)
		}
	}
	return nil
}

func applyMigrations(db *sql.DB, migrations []Migration) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	);`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, migration := range migrations {
		if migration.Name == "" {
			return fmt.Errorf("sqlite migration name must not be empty")
		}
		if migration.Up == "" {
			return fmt.Errorf("sqlite migration %q has empty SQL", migration.Name)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %q: %w", migration.Name, err)
		}
		applied, err := migrationApplied(tx, migration.Name)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if applied {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit already-applied migration %q: %w", migration.Name, err)
			}
			continue
		}
		if _, err := tx.Exec(migration.Up); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %q: %w", migration.Name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, migration.Name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %q: %w", migration.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %q: %w", migration.Name, err)
		}
	}
	return nil
}

func migrationApplied(tx *sql.Tx, name string) (bool, error) {
	var existing string
	err := tx.QueryRow(`SELECT name FROM schema_migrations WHERE name = ?`, name).Scan(&existing)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query migration %q: %w", name, err)
	}
	return true, nil
}
```

- [ ] **Step 5: Run shared storage tests**

Run:

```bash
go test ./internal/storage -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/storage/sqlite.go internal/storage/sqlite_test.go
git commit -m "feat: add sqlite storage foundation"
```

---

### Task 2: Server SQLite Schema

**Files:**
- Create: `internal/server/storage_schema.go`
- Create: `internal/server/storage_schema_test.go`

- [ ] **Step 1: Write schema tests**

Create `internal/server/storage_schema_test.go`:

```go
package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenServerDBCreatesExpectedTables(t *testing.T) {
	db, err := openServerDB(filepath.Join(t.TempDir(), "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer db.Close()

	wantTables := []string{
		"server_config",
		"allowed_ports",
		"admin_users",
		"api_keys",
		"api_key_permissions",
		"registered_clients",
		"client_stats",
		"client_disk_partitions",
		"client_tokens",
		"admin_sessions",
		"tunnels",
		"traffic_buckets",
	}
	for _, table := range wantTables {
		if !sqliteTableExists(t, db, table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}
}

func TestOpenServerDBDoesNotCreateJsonFiles(t *testing.T) {
	root := t.TempDir()
	db, err := openServerDB(filepath.Join(root, "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer db.Close()

	for _, name := range []string{"admin.json", "tunnels.json", "traffic.json"} {
		if pathExists(filepath.Join(root, "server", name)) {
			t.Fatalf("%s should not be created by SQLite storage", name)
		}
	}
}
```

- [ ] **Step 2: Run schema tests and verify they fail**

Run:

```bash
go test ./internal/server -run 'TestOpenServerDB' -count=1
```

Expected: FAIL because `openServerDB` and `sqliteTableExists` do not exist.

- [ ] **Step 3: Implement server schema**

Create `internal/server/storage_schema.go`:

```go
package server

import (
	"database/sql"

	"netsgo/internal/storage"
)

const serverDBFileName = "netsgo.db"

func openServerDB(path string) (*sql.DB, error) {
	return storage.Open(path, serverMigrations())
}

func serverMigrations() []storage.Migration {
	return []storage.Migration{{
		Name: "001_server_runtime_schema",
		Up: `
CREATE TABLE server_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	server_addr TEXT NOT NULL DEFAULT ''
);
CREATE TABLE allowed_ports (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	start_port INTEGER NOT NULL,
	end_port INTEGER NOT NULL,
	CHECK (start_port >= 1 AND start_port <= 65535),
	CHECK (end_port >= 1 AND end_port <= 65535),
	CHECK (start_port <= end_port)
);
CREATE TABLE admin_users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	last_login TEXT
);
CREATE TABLE api_keys (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	key_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT,
	is_active INTEGER NOT NULL,
	max_uses INTEGER NOT NULL,
	use_count INTEGER NOT NULL
);
CREATE TABLE api_key_permissions (
	api_key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
	permission TEXT NOT NULL,
	PRIMARY KEY (api_key_id, permission)
);
CREATE TABLE registered_clients (
	id TEXT PRIMARY KEY,
	install_id TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	os TEXT NOT NULL DEFAULT '',
	arch TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	version TEXT NOT NULL DEFAULT '',
	public_ipv4 TEXT NOT NULL DEFAULT '',
	public_ipv6 TEXT NOT NULL DEFAULT '',
	ingress_bps INTEGER NOT NULL DEFAULT 0,
	egress_bps INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	last_seen TEXT NOT NULL,
	last_ip TEXT NOT NULL DEFAULT ''
);
CREATE TABLE client_stats (
	client_id TEXT PRIMARY KEY REFERENCES registered_clients(id) ON DELETE CASCADE,
	cpu_usage REAL NOT NULL DEFAULT 0,
	mem_total INTEGER NOT NULL DEFAULT 0,
	mem_used INTEGER NOT NULL DEFAULT 0,
	mem_usage REAL NOT NULL DEFAULT 0,
	disk_total INTEGER NOT NULL DEFAULT 0,
	disk_used INTEGER NOT NULL DEFAULT 0,
	disk_usage REAL NOT NULL DEFAULT 0,
	net_sent INTEGER NOT NULL DEFAULT 0,
	net_recv INTEGER NOT NULL DEFAULT 0,
	net_sent_speed REAL NOT NULL DEFAULT 0,
	net_recv_speed REAL NOT NULL DEFAULT 0,
	uptime INTEGER NOT NULL DEFAULT 0,
	process_uptime INTEGER NOT NULL DEFAULT 0,
	os_install_time INTEGER NOT NULL DEFAULT 0,
	num_cpu INTEGER NOT NULL DEFAULT 0,
	app_mem_used INTEGER NOT NULL DEFAULT 0,
	app_mem_sys INTEGER NOT NULL DEFAULT 0,
	public_ipv4 TEXT NOT NULL DEFAULT '',
	public_ipv6 TEXT NOT NULL DEFAULT '',
	updated_at TEXT,
	fresh_until TEXT
);
CREATE TABLE client_disk_partitions (
	client_id TEXT NOT NULL REFERENCES registered_clients(id) ON DELETE CASCADE,
	path TEXT NOT NULL,
	used INTEGER NOT NULL DEFAULT 0,
	total INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (client_id, path)
);
CREATE TABLE client_tokens (
	id TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	install_id TEXT NOT NULL,
	key_id TEXT NOT NULL DEFAULT '',
	client_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	last_active_at TEXT NOT NULL,
	last_ip TEXT NOT NULL DEFAULT '',
	is_revoked INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_client_tokens_install_active ON client_tokens(install_id, is_revoked, last_active_at);
CREATE TABLE admin_sessions (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	username TEXT NOT NULL,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_admin_sessions_user ON admin_sessions(user_id);
CREATE INDEX idx_admin_sessions_expires ON admin_sessions(expires_at);
CREATE TABLE tunnels (
	client_id TEXT NOT NULL REFERENCES registered_clients(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT '',
	local_ip TEXT NOT NULL DEFAULT '',
	local_port INTEGER NOT NULL DEFAULT 0,
	remote_port INTEGER NOT NULL DEFAULT 0,
	domain TEXT NOT NULL DEFAULT '',
	ingress_bps INTEGER NOT NULL DEFAULT 0,
	egress_bps INTEGER NOT NULL DEFAULT 0,
	desired_state TEXT NOT NULL,
	runtime_state TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	binding TEXT NOT NULL,
	PRIMARY KEY (client_id, name)
);
CREATE INDEX idx_tunnels_hostname ON tunnels(hostname);
CREATE TABLE traffic_buckets (
	client_id TEXT NOT NULL,
	tunnel_name TEXT NOT NULL,
	tunnel_type TEXT NOT NULL,
	resolution TEXT NOT NULL,
	bucket_start INTEGER NOT NULL,
	ingress_bytes INTEGER NOT NULL DEFAULT 0,
	egress_bytes INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (client_id, tunnel_name, tunnel_type, resolution, bucket_start)
);
CREATE INDEX idx_traffic_query ON traffic_buckets(client_id, tunnel_name, resolution, bucket_start);
`,
	}}
}
```

Add this helper to `internal/server/storage_schema_test.go`:

```go
func sqliteTableExists(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, table string) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	return err == nil && name == table
}
```

Also add `database/sql` to that test file imports.

- [ ] **Step 4: Run schema tests**

Run:

```bash
go test ./internal/server -run 'TestOpenServerDB' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server/storage_schema.go internal/server/storage_schema_test.go
git commit -m "feat: define server sqlite schema"
```

---

### Task 3: SQLite AdminStore

**Files:**
- Modify: `internal/server/admin_store.go`
- Modify: `internal/server/admin_store_test.go`
- Modify: `internal/server/auth_middleware_test.go`
- Modify: `internal/server/server_test.go`
- Modify: `internal/server/test_helpers_test.go`

- [ ] **Step 1: Add a failing AdminStore SQLite persistence test**

Append to `internal/server/admin_store_test.go`:

```go
func TestAdminStore_UsesSQLiteFileAndNoJsonFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netsgo.db")
	store, err := NewAdminStore(path)
	if err != nil {
		t.Fatalf("NewAdminStore failed: %v", err)
	}
	store.bcryptCost = bcrypt.MinCost
	if err := store.Initialize("admin", "Admin1234", "https://example.com", []PortRange{{Start: 10000, End: 10010}}); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SQLite DB should exist at %s: %v", path, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "admin.json")); !os.IsNotExist(err) {
		t.Fatalf("admin.json should not exist, stat error = %v", err)
	}

	reloaded, err := NewAdminStore(path)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if _, err := reloaded.ValidateAdminPassword("admin", "Admin1234"); err != nil {
		t.Fatalf("admin password should survive reload: %v", err)
	}
}
```

- [ ] **Step 2: Run the new AdminStore test and verify it fails**

Run:

```bash
go test ./internal/server -run TestAdminStore_UsesSQLiteFileAndNoJsonFile -count=1
```

Expected: FAIL because `NewAdminStore` still reads and writes JSON.

- [ ] **Step 3: Change AdminStore fields and constructor**

In `internal/server/admin_store.go`, replace the JSON fields:

```go
type AdminStore struct {
	path       string
	db         *sql.DB
	mu         sync.RWMutex
	bcryptCost int

	dummyHashOnce sync.Once
	dummyHash     []byte

	failSaveErr   error
	failSaveCount int
}
```

Update imports to include `database/sql` and remove `encoding/json`, `os`, and `netsgo/pkg/fileutil` when no longer used.

Replace `NewAdminStore` with:

```go
func NewAdminStore(path string) (*AdminStore, error) {
	db, err := openServerDB(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open admin store: %w", err)
	}
	store := &AdminStore{
		path:       path,
		db:         db,
		bcryptCost: bcrypt.DefaultCost,
	}
	if err := store.validateLoadedState(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.CleanExpiredSessions(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to clean expired sessions: %w", err)
	}
	if !store.IsInitialized() {
		log.Printf("⚠️ Service not yet initialized; please use the install or init command to complete initialization")
	}
	return store, nil
}
```

Keep `failSaveErr` behavior by adding:

```go
func (s *AdminStore) maybeFailSave() error {
	if s.failSaveErr != nil && s.failSaveCount > 0 {
		err := s.failSaveErr
		s.failSaveCount--
		if s.failSaveCount == 0 {
			s.failSaveErr = nil
		}
		return err
	}
	return nil
}
```

- [ ] **Step 4: Replace AdminStore JSON snapshots with SQL helpers**

Add these helper signatures in `internal/server/admin_store.go` and use them from existing methods:

```go
func (s *AdminStore) initializedLocked(tx queryer) (bool, error)
func (s *AdminStore) loadServerConfigLocked(tx queryer) (ServerConfig, error)
func (s *AdminStore) replaceAllowedPortsLocked(tx execer, allowedPorts []PortRange) error
func (s *AdminStore) scanRegisteredClient(row scanner) (RegisteredClient, error)
func (s *AdminStore) saveClientStatsLocked(tx *sql.Tx, clientID string, stats *protocol.SystemStats) error
func (s *AdminStore) loadClientStatsLocked(clientID string) (*protocol.SystemStats, error)
```

Add these local interfaces:

```go
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

type scanner interface {
	Scan(dest ...any) error
}
```

Store all `time.Time` values as `time.RFC3339Nano` strings and parse them with:

```go
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, raw)
}

func rollbackUnlessCommitted(tx *sql.Tx, committed *bool) {
	if !*committed {
		_ = tx.Rollback()
	}
}
```

- [ ] **Step 5: Convert initialization and config methods**

Use one SQL transaction in `Initialize`:

```go
tx, err := s.db.Begin()
if err != nil {
	return err
}
defer rollbackUnlessCommitted(tx, &committed)
```

Within that transaction:

```sql
INSERT INTO admin_users (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?);
INSERT INTO server_config (id, server_addr) VALUES (1, ?);
INSERT INTO allowed_ports (start_port, end_port) VALUES (?, ?);
```

`IsInitialized` should return true when `admin_users` has at least one row and `server_config.id = 1` exists.

`UpdateServerConfig` should update `server_config` and replace `allowed_ports` in one transaction.

- [ ] **Step 6: Convert users, sessions, API keys, clients, and tokens**

Keep the existing public method names and return values. Use these SQL mappings:

```sql
-- users
SELECT id, username, password_hash, role, created_at, last_login FROM admin_users WHERE username = ?;
UPDATE admin_users SET last_login = ? WHERE id = ?;

-- sessions
INSERT INTO admin_sessions (id, user_id, username, role, created_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?, ?);
DELETE FROM admin_sessions WHERE user_id = ?;
DELETE FROM admin_sessions WHERE id = ?;
DELETE FROM admin_sessions WHERE expires_at <= ?;

-- API keys
INSERT INTO api_keys (id, name, key_hash, created_at, expires_at, is_active, max_uses, use_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?);
INSERT INTO api_key_permissions (api_key_id, permission) VALUES (?, ?);
UPDATE api_keys SET is_active = ? WHERE id = ?;
UPDATE api_keys SET max_uses = ? WHERE id = ?;
DELETE FROM api_keys WHERE id = ?;

-- clients
INSERT INTO registered_clients (
	id, install_id, display_name, hostname, os, arch, ip, version, public_ipv4, public_ipv6,
	ingress_bps, egress_bps, created_at, last_seen, last_ip
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
UPDATE registered_clients SET hostname = ?, os = ?, arch = ?, ip = ?, version = ?, public_ipv4 = ?, public_ipv6 = ?, last_seen = ?, last_ip = ? WHERE id = ?;
UPDATE registered_clients SET ingress_bps = ?, egress_bps = ? WHERE id = ?;
UPDATE registered_clients SET display_name = ? WHERE id = ?;

-- tokens
INSERT INTO client_tokens (id, token_hash, install_id, key_id, client_id, created_at, last_active_at, last_ip, is_revoked) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0);
UPDATE client_tokens SET is_revoked = 1 WHERE install_id = ? AND is_revoked = 0;
UPDATE client_tokens SET token_hash = ?, last_active_at = ?, last_ip = ?, client_id = ? WHERE id = ?;
UPDATE client_tokens SET last_active_at = ?, last_ip = ? WHERE id = ?;
DELETE FROM client_tokens WHERE is_revoked = 1 OR last_active_at <= ?;
```

Run `maybeFailSave()` immediately before `tx.Commit()` in methods that previously used rollback injection.

- [ ] **Step 7: Run AdminStore tests**

Run:

```bash
go test ./internal/server -run 'TestAdminStore' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/server/admin_store.go internal/server/admin_store_test.go internal/server/auth_middleware_test.go internal/server/server_test.go internal/server/test_helpers_test.go
git commit -m "feat: store admin state in sqlite"
```

---

### Task 4: SQLite TunnelStore

**Files:**
- Modify: `internal/server/store.go`
- Modify: `internal/server/store_test.go`
- Modify: `internal/server/server_test.go`
- Modify: `internal/server/admin_api_test.go`
- Modify: `internal/server/proxy_test.go`
- Modify: `internal/server/http_dispatch_test.go`
- Modify: `internal/server/offline_http_tunnel_test.go`
- Modify: `internal/server/managed_tunnel_phase1_test.go`
- Modify: `internal/server/proxy_validation_test.go`

- [ ] **Step 1: Add failing tunnel SQLite tests**

In `internal/server/store_test.go`, replace corrupted/legacy JSON tests with:

```go
func TestTunnelStore_UsesSQLiteAndNoJsonFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netsgo.db")
	store, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("NewTunnelStore failed: %v", err)
	}
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 18080},
		ClientID:        "client-1",
		Hostname:        "host-1",
	})

	if _, err := os.Stat(filepath.Join(dir, "tunnels.json")); !os.IsNotExist(err) {
		t.Fatalf("tunnels.json should not exist, stat error = %v", err)
	}
	reloaded, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	stored, ok := reloaded.GetTunnel("client-1", "web")
	if !ok {
		t.Fatal("expected reloaded tunnel")
	}
	if stored.RemotePort != 18080 {
		t.Fatalf("RemotePort = %d, want 18080", stored.RemotePort)
	}
}
```

- [ ] **Step 2: Run the new tunnel test and verify it fails**

Run:

```bash
go test ./internal/server -run TestTunnelStore_UsesSQLiteAndNoJsonFile -count=1
```

Expected: FAIL because `TunnelStore` still expects a JSON array.

- [ ] **Step 3: Convert TunnelStore to SQL**

Change `TunnelStore` to:

```go
type TunnelStore struct {
	path string
	db   *sql.DB
	mu   sync.RWMutex

	failSaveErr   error
	failSaveCount int
}
```

Use `openServerDB(path)` in `NewTunnelStore`.

Replace the in-memory slice with direct SQL operations against `tunnels`. Use `normalize()` before inserts and updates.

Core SQL:

```sql
INSERT INTO tunnels (client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, desired_state, runtime_state, error, hostname, binding)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

DELETE FROM tunnels WHERE client_id = ? AND name = ?;

UPDATE tunnels SET desired_state = ?, runtime_state = ?, error = ? WHERE client_id = ? AND name = ?;

UPDATE tunnels SET local_ip = ?, local_port = ?, remote_port = ?, domain = ?, ingress_bps = ?, egress_bps = ? WHERE client_id = ? AND name = ?;

UPDATE tunnels SET hostname = ? WHERE client_id = ? AND hostname <> ?;

SELECT client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, desired_state, runtime_state, error, hostname, binding FROM tunnels WHERE client_id = ? ORDER BY name;
```

Keep `failSaveErr` injection by checking it before mutating SQL in write methods.

- [ ] **Step 4: Run TunnelStore tests**

Run:

```bash
go test ./internal/server -run 'TestTunnelStore' -count=1
```

Expected: PASS after removing JSON-specific legacy/corruption tests and replacing them with SQLite tests.

- [ ] **Step 5: Commit**

```bash
git add internal/server/store.go internal/server/store_test.go internal/server/*_test.go
git commit -m "feat: store tunnels in sqlite"
```

---

### Task 5: SQLite TrafficStore

**Files:**
- Modify: `internal/server/traffic_store.go`
- Modify: `internal/server/traffic_store_test.go`
- Modify: `internal/server/udp_proxy_test.go`
- Modify: `internal/server/data_paths_test.go`

- [ ] **Step 1: Add failing SQLite traffic persistence test**

In `internal/server/traffic_store_test.go`, add:

```go
func TestTrafficStore_UsesSQLiteAndNoJsonFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netsgo.db")
	ts, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("NewTrafficStore failed: %v", err)
	}

	now := time.Now().UTC()
	ts.ApplyDeltas([]TrafficDelta{{
		ClientID: "c1", TunnelName: "web", TunnelType: "http", MinuteStart: minuteFloorUTC(now).Unix(), IngressBytes: 100, EgressBytes: 50,
	}})
	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "traffic.json")); !os.IsNotExist(err) {
		t.Fatalf("traffic.json should not exist, stat error = %v", err)
	}

	reloaded, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	got := reloaded.QueryWithResolution("c1", "web", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	series := mustSingleSeries(t, got, "web")
	if series.Points[0].IngressBytes != 100 || series.Points[0].EgressBytes != 50 {
		t.Fatalf("traffic did not round-trip through SQLite: %+v", series.Points[0])
	}
}
```

- [ ] **Step 2: Run the new traffic test and verify it fails**

Run:

```bash
go test ./internal/server -run TestTrafficStore_UsesSQLiteAndNoJsonFile -count=1
```

Expected: FAIL because `TrafficStore` still persists JSON snapshots.

- [ ] **Step 3: Convert TrafficStore to SQL-backed buckets with pending in-memory deltas**

Change `TrafficStore` fields to:

```go
type TrafficStore struct {
	path string
	db   *sql.DB
	mu   sync.RWMutex

	pendingMinute map[string]TrafficBucket

	failSaveErr   error
	failSaveCount int
}
```

`ApplyDeltas` should merge deltas into `pendingMinute` only. `Flush` should upsert all pending buckets in one transaction:

```sql
INSERT INTO traffic_buckets (client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(client_id, tunnel_name, tunnel_type, resolution, bucket_start)
DO UPDATE SET
	ingress_bytes = ingress_bytes + excluded.ingress_bytes,
	egress_bytes = egress_bytes + excluded.egress_bytes;
```

`QueryWithResolution` should read persisted buckets from SQLite and then merge matching `pendingMinute` buckets before sorting. This preserves the current behavior where recent unflushed traffic appears in API responses.

`Compact` should:

1. Roll up completed minute buckets from SQLite into hour rows.
2. Include flushed data only; pending buckets are still visible through query and flushed within the existing persist loop.
3. Prune minute/hour rows by existing retention constants.

- [ ] **Step 4: Run traffic tests**

Run:

```bash
go test ./internal/server -run 'TestTrafficStore|TestTrafficAPI|TestUDP|TestProxy' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server/traffic_store.go internal/server/traffic_store_test.go internal/server/udp_proxy_test.go internal/server/data_paths_test.go
git commit -m "feat: store traffic history in sqlite"
```

---

### Task 6: Server Bootstrap and Init Paths

**Files:**
- Modify: `internal/server/server_bootstrap.go`
- Modify: `internal/server/init.go`
- Modify: `cmd/netsgo/cmd_server.go`
- Modify: `internal/server/data_paths_test.go`
- Modify: tests that assert `admin.json`, `tunnels.json`, or `traffic.json`

- [ ] **Step 1: Update data path tests first**

Replace `TestServerInitStore_UsesDataDirLayout` assertions in `internal/server/data_paths_test.go` with:

```go
if got, want := s.store.path, filepath.Join(dataDir, "server", "netsgo.db"); got != want {
	t.Fatalf("store.path = %q, want %q", got, want)
}
if got, want := s.auth.adminStore.path, filepath.Join(dataDir, "server", "netsgo.db"); got != want {
	t.Fatalf("adminStore.path = %q, want %q", got, want)
}
if got, want := s.trafficStore.path, filepath.Join(dataDir, "server", "netsgo.db"); got != want {
	t.Fatalf("trafficStore.path = %q, want %q", got, want)
}
```

- [ ] **Step 2: Run data path tests and verify they fail**

Run:

```bash
go test ./internal/server -run TestServerInitStore_UsesDataDirLayout -count=1
```

Expected: FAIL because bootstrap still points at JSON files.

- [ ] **Step 3: Change server DB path and store initialization**

In `internal/server/server_bootstrap.go`, add:

```go
func (s *Server) serverDBPath() string {
	return filepath.Join(s.serverDataDir(), serverDBFileName)
}
```

Change `initStore` to use one path:

```go
func (s *Server) initStore() error {
	path := s.serverDBPath()

	store, err := NewTunnelStore(path)
	if err != nil {
		return err
	}
	s.store = store
	log.Printf("📦 SQLite server store: %s", path)

	adminStore, err := NewAdminStore(path)
	if err != nil {
		return err
	}
	s.auth.adminStore = adminStore

	trafficStore, err := NewTrafficStore(path)
	if err != nil {
		return err
	}
	s.trafficStore = trafficStore

	return nil
}
```

Change `getStorePath` to return `s.serverDBPath()` when no store is present.

- [ ] **Step 4: Update init and command startup**

In `internal/server/init.go`, replace `filepath.Join(dataDir, "server", "admin.json")` with `filepath.Join(dataDir, "server", serverDBFileName)`.

In `cmd/netsgo/cmd_server.go`, replace the preflight `NewAdminStore` path with:

```go
adminStore, err := server.NewAdminStore(filepath.Join(s.DataDir, "server", "netsgo.db"))
```

- [ ] **Step 5: Run server package tests**

Run:

```bash
go test ./internal/server -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/server/server_bootstrap.go internal/server/init.go cmd/netsgo/cmd_server.go internal/server/*_test.go
git commit -m "feat: use unified server sqlite database"
```

---

### Task 7: Client SQLite Identity Store

**Files:**
- Create: `internal/client/state_store.go`
- Create: `internal/client/state_store_test.go`
- Modify: `internal/client/state.go`
- Modify: `internal/client/state_path_test.go`
- Modify: `internal/client/client_tls_test.go`
- Modify: `internal/manage/client.go`

- [ ] **Step 1: Add failing client state store tests**

Create `internal/client/state_store_test.go`:

```go
package client

import (
	"path/filepath"
	"testing"
)

func TestClientStateStoreRoundTrip(t *testing.T) {
	store, err := newClientStateStore(filepath.Join(t.TempDir(), "client", "netsgo.db"))
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer store.Close()

	state := persistedState{InstallID: "client-install", Token: "tk-test", TLSFingerprint: "AA:BB"}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, ok, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok {
		t.Fatal("expected saved state")
	}
	if got != state {
		t.Fatalf("state = %+v, want %+v", got, state)
	}
}

func TestClientStateStoreDoesNotCreateJsonFile(t *testing.T) {
	dir := t.TempDir()
	store, err := newClientStateStore(filepath.Join(dir, "client", "netsgo.db"))
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer store.Close()
	if err := store.Save(persistedState{InstallID: "client-install"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "client", "client.json")); !os.IsNotExist(err) {
		t.Fatalf("client.json should not exist, stat error = %v", err)
	}
}
```

- [ ] **Step 2: Run client state store tests and verify they fail**

Run:

```bash
go test ./internal/client -run TestClientStateStore -count=1
```

Expected: FAIL because `newClientStateStore` does not exist.

- [ ] **Step 3: Implement the client state store**

Create `internal/client/state_store.go`:

```go
package client

import (
	"database/sql"
	"fmt"

	"netsgo/internal/storage"
)

const clientDBFileName = "netsgo.db"

type clientStateStore struct {
	path string
	db   *sql.DB
}

func newClientStateStore(path string) (*clientStateStore, error) {
	db, err := storage.Open(path, []storage.Migration{{
		Name: "001_client_identity",
		Up: `CREATE TABLE client_identity (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			install_id TEXT NOT NULL,
			token TEXT NOT NULL DEFAULT '',
			tls_fingerprint TEXT NOT NULL DEFAULT ''
		);`,
	}})
	if err != nil {
		return nil, err
	}
	return &clientStateStore{path: path, db: db}, nil
}

func (s *clientStateStore) Close() error {
	return s.db.Close()
}

func (s *clientStateStore) Load() (persistedState, bool, error) {
	var state persistedState
	err := s.db.QueryRow(`SELECT install_id, token, tls_fingerprint FROM client_identity WHERE id = 1`).Scan(&state.InstallID, &state.Token, &state.TLSFingerprint)
	if err == sql.ErrNoRows {
		return persistedState{}, false, nil
	}
	if err != nil {
		return persistedState{}, false, fmt.Errorf("load client identity: %w", err)
	}
	return state, true, nil
}

func (s *clientStateStore) Save(state persistedState) error {
	if state.InstallID == "" {
		return fmt.Errorf("install_id must not be empty")
	}
	_, err := s.db.Exec(`INSERT INTO client_identity (id, install_id, token, tls_fingerprint)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			install_id = excluded.install_id,
			token = excluded.token,
			tls_fingerprint = excluded.tls_fingerprint`,
		state.InstallID, state.Token, state.TLSFingerprint)
	if err != nil {
		return fmt.Errorf("save client identity: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Update client state methods**

In `internal/client/state.go`, change `statePath()` to:

```go
func (c *Client) statePath() string {
	root := c.DataDir
	if root == "" {
		root = datadir.DefaultDataDir()
	}
	return filepath.Join(root, "client", clientDBFileName)
}
```

Replace file JSON reads/writes in `ensureInstallID`, `saveToken`, and `saveTLSFingerprint` with `newClientStateStore(c.statePath())`, `Load`, and `Save`. Preserve current behavior:

```go
state := persistedState{
	InstallID:      c.InstallID,
	Token:          c.Token,
	TLSFingerprint: c.TLSFingerprint,
}
```

When saving token, preserve `TLSFingerprint`; when saving fingerprint, preserve `Token`.

- [ ] **Step 5: Update client tests**

In `internal/client/state_path_test.go`, replace path expectations from `client.json` to `netsgo.db`.

In `internal/client/client_tls_test.go`, replace direct JSON writes with:

```go
store, err := newClientStateStore(statePath)
if err != nil {
	t.Fatalf("newClientStateStore() error = %v", err)
}
defer store.Close()
if err := store.Save(persistedState{
	InstallID:      "client-test-install",
	Token:          "tk-test",
	TLSFingerprint: fingerprint,
}); err != nil {
	t.Fatalf("Save() error = %v", err)
}
```

Replace direct JSON reads with:

```go
store, err := newClientStateStore(statePath)
if err != nil {
	t.Fatalf("newClientStateStore() error = %v", err)
}
defer store.Close()
state, ok, err := store.Load()
if err != nil {
	t.Fatalf("Load() error = %v", err)
}
if !ok {
	t.Fatal("expected saved client identity")
}
```

- [ ] **Step 6: Run client tests**

Run:

```bash
go test ./internal/client -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/client/state.go internal/client/state_store.go internal/client/state_store_test.go internal/client/state_path_test.go internal/client/client_tls_test.go
git commit -m "feat: store client identity in sqlite"
```

---

### Task 8: Remove Install/Manage JSON ServiceSpec

**Files:**
- Create: `internal/svcmgr/layout.go`
- Create: `internal/svcmgr/layout_test.go`
- Modify: `internal/svcmgr/spec.go`
- Modify: `internal/svcmgr/state.go`
- Modify: `internal/svcmgr/unit.go`
- Modify: `internal/svcmgr/env.go`
- Modify: `internal/svcmgr/*_test.go`
- Modify: `internal/install/server.go`
- Modify: `internal/install/client.go`
- Modify: `internal/install/service_flow.go`
- Modify: `internal/install/*_test.go`
- Modify: `internal/manage/server.go`
- Modify: `internal/manage/client.go`
- Modify: `internal/manage/uninstall_all.go`
- Modify: `internal/manage/*_test.go`

- [ ] **Step 1: Add failing layout tests**

Create `internal/svcmgr/layout_test.go`:

```go
package svcmgr

import (
	"path/filepath"
	"testing"
)

func TestNewLayout(t *testing.T) {
	server := NewLayout(RoleServer)
	if server.ServiceName != "netsgo-server" {
		t.Fatalf("server.ServiceName = %q", server.ServiceName)
	}
	if server.UnitPath != filepath.Join(SystemdDir, "netsgo-server.service") {
		t.Fatalf("server.UnitPath = %q", server.UnitPath)
	}
	if server.EnvPath != filepath.Join(ServicesDir, "server.env") {
		t.Fatalf("server.EnvPath = %q", server.EnvPath)
	}
	if server.RuntimeDir != filepath.Join(ManagedDataDir, "server") {
		t.Fatalf("server.RuntimeDir = %q", server.RuntimeDir)
	}

	client := NewLayout(RoleClient)
	if client.ServiceName != "netsgo-client" {
		t.Fatalf("client.ServiceName = %q", client.ServiceName)
	}
	if client.RuntimeDir != filepath.Join(ManagedDataDir, "client") {
		t.Fatalf("client.RuntimeDir = %q", client.RuntimeDir)
	}
}
```

- [ ] **Step 2: Run layout tests and verify they fail**

Run:

```bash
go test ./internal/svcmgr -run TestNewLayout -count=1
```

Expected: FAIL because `NewLayout` does not exist.

- [ ] **Step 3: Add ServiceLayout**

Create `internal/svcmgr/layout.go`:

```go
package svcmgr

import "path/filepath"

type Role string

const (
	RoleServer Role = "server"
	RoleClient Role = "client"
)

const (
	ServicesDir    = "/etc/netsgo/services"
	SystemdDir     = "/etc/systemd/system"
	BinaryPath     = "/usr/local/bin/netsgo"
	ManagedDataDir = "/var/lib/netsgo"
	SystemUser     = "netsgo"
	SystemGroup    = "netsgo"
)

type ServiceLayout struct {
	Role        Role
	ServiceName string
	BinaryPath  string
	DataDir     string
	RuntimeDir  string
	UnitPath    string
	EnvPath     string
	RunAsUser   string
	RunAsGroup  string
}

func NewLayout(role Role) ServiceLayout {
	return ServiceLayout{
		Role:        role,
		ServiceName: "netsgo-" + string(role),
		BinaryPath:  BinaryPath,
		DataDir:     ManagedDataDir,
		RuntimeDir:  filepath.Join(ManagedDataDir, string(role)),
		UnitPath:    filepath.Join(SystemdDir, UnitName(role)),
		EnvPath:     filepath.Join(ServicesDir, string(role)+".env"),
		RunAsUser:   SystemUser,
		RunAsGroup:  SystemGroup,
	}
}

func UnitName(role Role) string {
	return "netsgo-" + string(role) + ".service"
}
```

Remove role/constants and JSON read/write functions from `internal/svcmgr/spec.go`. Delete `spec.go` after all references are gone.

- [ ] **Step 4: Update unit/env APIs**

Change unit/env signatures:

```go
func WriteServerUnit(layout ServiceLayout) error
func WriteClientUnit(layout ServiceLayout) error
func WriteServerEnv(layout ServiceLayout, env ServerEnv) error
func WriteClientEnv(layout ServiceLayout, env ClientEnv) error
func ReadServerEnv(layout ServiceLayout) (ServerEnv, error)
func ReadClientEnv(layout ServiceLayout) (ClientEnv, error)
```

Add unit parser:

```go
type UnitInfo struct {
	User            string
	Group           string
	EnvironmentFile string
	ExecStart       string
}

func ReadUnitInfo(unitPath string) (UnitInfo, error)
```

`ReadUnitInfo` should parse `User=`, `Group=`, `EnvironmentFile=`, and `ExecStart=` lines from the unit file.

- [ ] **Step 5: Rewrite install detection without spec files**

Change `Inspect` to call:

```go
func Inspect(role Role) InstallInspection {
	layout := NewLayout(role)
	return InspectWithLayout(layout)
}
```

Implement `InspectWithLayout(layout ServiceLayout)` with these rules:

1. If unit and env do not exist and runtime dir does not exist: `StateNotInstalled`.
2. If server unit/env do not exist but `<runtime-dir>/netsgo.db` exists: `StateHistoricalDataOnly`.
3. If any required unit/env/runtime dir is missing: `StateBroken`.
4. If unit exists, parse it and compare `User`, `Group`, `EnvironmentFile`, and `ExecStart` against `layout`.
5. If env exists, parse it with `ReadServerEnv` or `ReadClientEnv`.
6. If binary is missing or not executable: `StateBroken`.
7. If server runtime DB `<runtime-dir>/netsgo.db` is missing: `StateBroken`.
8. Otherwise: `StateInstalled`.

Change `recoverableServerDataExists` to:

```go
func recoverableServerDataExists(dataDir string) bool {
	return pathExists(filepath.Join(dataDir, "netsgo.db"))
}
```

- [ ] **Step 6: Update install flow**

In `internal/install/service_flow.go`, replace `ServiceSpec` with `ServiceLayout`:

```go
func completeManagedInstall(role svcmgr.Role, deps managedInstallDeps, writeArtifacts func(layout svcmgr.ServiceLayout) error) error {
	if err := deps.EnsureUser(svcmgr.SystemUser); err != nil {
		return err
	}
	if err := deps.EnsureDirs(); err != nil {
		return err
	}
	binaryPath, err := deps.CurrentBinaryPath()
	if err != nil {
		return err
	}
	if err := deps.InstallBinary(binaryPath); err != nil {
		return err
	}
	layout := svcmgr.NewLayout(role)
	if err := writeArtifacts(layout); err != nil {
		return err
	}
	if err := deps.DaemonReload(); err != nil {
		return err
	}
	return deps.EnableAndStart(svcmgr.UnitName(role))
}
```

In `internal/install/server.go` and `internal/install/client.go`, remove `WriteServerSpec` and `WriteClientSpec` dependencies and calls. Keep writing env and unit files.

- [ ] **Step 7: Update manage flow**

In `internal/manage/server.go` and `internal/manage/client.go`:

1. Remove `ReadServerSpec` and `ReadClientSpec` dependencies.
2. Use `svcmgr.NewLayout(role)` for paths.
3. Remove "Spec path" rows from detail output.
4. Compute uninstall paths from `layout.UnitPath`, `layout.EnvPath`, and `layout.RuntimeDir`.
5. Change client identity summary path from `client.json` to SQLite store at `filepath.Join(layout.RuntimeDir, "netsgo.db")`.

In `internal/manage/uninstall_all.go`, remove spec loading and use layouts directly.

- [ ] **Step 8: Run svcmgr/install/manage tests**

Run:

```bash
go test ./internal/svcmgr ./internal/install ./internal/manage -count=1
```

Expected: PASS after updating tests to assert no spec file writes/removals.

- [ ] **Step 9: Commit**

```bash
git add internal/svcmgr internal/install internal/manage
git commit -m "refactor: remove managed service json manifests"
```

---

### Task 9: Update Remaining Tests and Documentation

**Files:**
- Modify: tests under `internal/server`, `internal/client`, `internal/manage`, `internal/svcmgr`
- Modify: `docs/setup-manage-impl.md`
- Modify: `docs/setup-manage-plan.md`

- [ ] **Step 1: Search for stale local JSON storage references**

Run:

```bash
rg -n 'admin\.json|tunnels\.json|traffic\.json|client\.json|server\.json|ReadServerSpec|WriteServerSpec|ReadClientSpec|WriteClientSpec|ServiceSpec|SpecPath' internal cmd docs -g '*.go' -g '*.md'
```

Expected: Only protocol/test fixture references unrelated to local storage remain. Runtime storage and manage spec references should be gone.

- [ ] **Step 2: Update docs**

In `docs/setup-manage-impl.md` and `docs/setup-manage-plan.md`, replace local storage tables with:

```markdown
| Path | Purpose |
| --- | --- |
| `<data-dir>/server/netsgo.db` | Server runtime data: admin users, keys, clients, tokens, sessions, tunnels, traffic buckets, server config |
| `<data-dir>/client/netsgo.db` | Client runtime data: install identity, saved token, TLS fingerprint |
| `/etc/netsgo/services/server.env` | systemd EnvironmentFile for the managed server |
| `/etc/netsgo/services/client.env` | systemd EnvironmentFile for the managed client |
| `/etc/systemd/system/netsgo-server.service` | systemd unit for managed server |
| `/etc/systemd/system/netsgo-client.service` | systemd unit for managed client |
```

Add this policy text:

```markdown
NetsGo does not maintain a separate managed-service JSON manifest. `netsgo manage` derives service state from systemd unit files, env files, fixed managed paths, runtime DB presence, and systemd active/enabled state.
```

- [ ] **Step 3: Run stale-reference search again**

Run:

```bash
rg -n 'admin\.json|tunnels\.json|traffic\.json|client\.json|server\.json|ReadServerSpec|WriteServerSpec|ReadClientSpec|WriteClientSpec|ServiceSpec|SpecPath' internal cmd docs -g '*.go' -g '*.md'
```

Expected: No runtime-storage or managed-service manifest references remain. References to JSON as REST/WebSocket/SSE payload encoding may remain.

- [ ] **Step 4: Commit**

```bash
git add docs/setup-manage-impl.md docs/setup-manage-plan.md internal cmd
git commit -m "docs: document sqlite local storage model"
```

---

### Task 10: Final Verification

**Files:**
- Verify all changed files.

- [ ] **Step 1: Build frontend**

Run:

```bash
cd web && bun run build
```

Expected: PASS and `web/dist` generated.

- [ ] **Step 2: Run Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run web lint**

Run:

```bash
cd web && bun run lint
```

Expected: PASS.

- [ ] **Step 4: Run full build**

Run:

```bash
make build
```

Expected: PASS and `bin/netsgo` generated.

- [ ] **Step 5: Manual smoke checks**

Run:

```bash
tmpdir="$(mktemp -d)"
go run -tags dev ./cmd/netsgo server \
  --data-dir "$tmpdir" \
  --port 0 \
  --init-admin-username admin \
  --init-admin-password Admin1234 \
  --init-server-addr http://localhost \
  --init-allowed-ports 10000-10010
```

Expected: server starts, logs `SQLite server store`, and creates `$tmpdir/server/netsgo.db`. Stop it with Ctrl-C after confirming startup.

Run:

```bash
find "$tmpdir" -name '*.json' -print
```

Expected: no output.

- [ ] **Step 6: Confirm clean worktree**

Check the worktree after verification:

```bash
git status --short
```

Expected: no output.

---

## Self-Review

Spec coverage:
- Server runtime JSON replacement is covered by Tasks 2-6.
- Client runtime JSON replacement is covered by Task 7.
- Managed service JSON manifest removal is covered by Task 8.
- Documentation cleanup is covered by Task 9.
- Verification is covered by Task 10.

Placeholder scan:
- The plan avoids placeholder markers, deferred implementation language, and open-ended edge-case instructions.
- Each task lists exact files, concrete tests, commands, expected outcomes, and commit points.

Type consistency:
- Server DB path uses `serverDBFileName = "netsgo.db"`.
- Client DB path uses `clientDBFileName = "netsgo.db"`.
- Managed service layout uses `ServiceLayout` and replaces `ServiceSpec`.
