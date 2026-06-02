package server

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"netsgo/internal/storage"
	"netsgo/pkg/protocol"
)

func TestOpenServerDBCreatesExpectedTables(t *testing.T) {
	db, err := openServerDB(filepath.Join(t.TempDir(), "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

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

	for _, column := range []string{"initialized", "jwt_secret"} {
		if !sqliteTableColumnExists(t, db, "server_config", column) {
			t.Fatalf("expected server_config.%s to exist", column)
		}
	}

	if got := countTunnelRegisteredClientFKs(t, db); got != 0 {
		t.Fatalf("tunnels.client_id should not reference registered_clients, got %d FK(s)", got)
	}
}

func TestOpenServerDBMigratesEmptyDatabaseToExpectedSchema(t *testing.T) {
	db, err := openServerDB(filepath.Join(t.TempDir(), "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	wantTables := map[string][]sqliteColumn{
		"schema_migrations": {
			{name: "name", typ: "TEXT", notNull: false, primaryKey: true},
			{name: "applied_at", typ: "TEXT", notNull: true},
		},
		"server_config": {
			{name: "id", typ: "INTEGER", primaryKey: true},
			{name: "initialized", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "jwt_secret", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "server_addr", typ: "TEXT", notNull: true, defaultValue: "''"},
		},
		"allowed_ports": {
			{name: "id", typ: "INTEGER", primaryKey: true},
			{name: "start_port", typ: "INTEGER", notNull: true},
			{name: "end_port", typ: "INTEGER", notNull: true},
		},
		"admin_users": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "username", typ: "TEXT", notNull: true},
			{name: "password_hash", typ: "TEXT", notNull: true},
			{name: "role", typ: "TEXT", notNull: true},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "last_login", typ: "TEXT"},
		},
		"api_keys": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "name", typ: "TEXT", notNull: true},
			{name: "key_hash", typ: "TEXT", notNull: true},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "expires_at", typ: "TEXT"},
			{name: "is_active", typ: "INTEGER", notNull: true},
			{name: "max_uses", typ: "INTEGER", notNull: true},
			{name: "use_count", typ: "INTEGER", notNull: true},
		},
		"api_key_permissions": {
			{name: "api_key_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "permission", typ: "TEXT", notNull: true, primaryKey: true},
		},
		"registered_clients": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "install_id", typ: "TEXT", notNull: true},
			{name: "display_name", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "hostname", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "os", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "arch", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "version", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "public_ipv4", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "public_ipv6", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "egress_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "last_seen", typ: "TEXT", notNull: true},
			{name: "last_ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "last_capabilities", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
		},
		"client_stats": {
			{name: "client_id", typ: "TEXT", primaryKey: true},
			{name: "cpu_usage", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "mem_total", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "mem_used", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "mem_usage", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "disk_total", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "disk_used", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "disk_usage", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "net_sent", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "net_recv", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "net_sent_speed", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "net_recv_speed", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "uptime", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "process_uptime", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "os_install_time", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "num_cpu", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "app_mem_used", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "app_mem_sys", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "public_ipv4", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "public_ipv6", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "updated_at", typ: "TEXT"},
			{name: "fresh_until", typ: "TEXT"},
		},
		"client_disk_partitions": {
			{name: "client_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "path", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "used", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "total", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"client_tokens": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "token_hash", typ: "TEXT", notNull: true},
			{name: "install_id", typ: "TEXT", notNull: true},
			{name: "key_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "last_active_at", typ: "TEXT", notNull: true},
			{name: "last_ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "is_revoked", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"admin_sessions": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "user_id", typ: "TEXT", notNull: true},
			{name: "username", typ: "TEXT", notNull: true},
			{name: "role", typ: "TEXT", notNull: true},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "expires_at", typ: "TEXT", notNull: true},
			{name: "ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "user_agent", typ: "TEXT", notNull: true, defaultValue: "''"},
		},
		"tunnels": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "name", typ: "TEXT", notNull: true},
			{name: "client_id", typ: "TEXT", notNull: true},
			{name: "type", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "local_ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "local_port", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "remote_port", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "domain", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "hostname", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "binding", typ: "TEXT", notNull: true, defaultValue: "'client_id'"},
			{name: "revision", typ: "INTEGER", notNull: true, defaultValue: "1"},
			{name: "topology", typ: "TEXT", notNull: true},
			{name: "owner_client_id", typ: "TEXT", notNull: true},
			{name: "ingress_location", typ: "TEXT", notNull: true},
			{name: "ingress_client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_type", typ: "TEXT", notNull: true},
			{name: "ingress_config", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "ingress_bind_ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_port", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "ingress_domain", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_path", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_location", typ: "TEXT", notNull: true},
			{name: "target_client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_type", typ: "TEXT", notNull: true},
			{name: "target_config", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "target_host", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_port", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "target_path", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_resource_key", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "transport_policy", typ: "TEXT", notNull: true},
			{name: "actual_transport", typ: "TEXT", notNull: true, defaultValue: "'unknown'"},
			{name: "p2p_state", typ: "TEXT", notNull: true, defaultValue: "'idle'"},
			{name: "p2p_error", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "p2p_session_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "egress_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "desired_state", typ: "TEXT", notNull: true},
			{name: "runtime_state", typ: "TEXT", notNull: true},
			{name: "error", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "created_by_user_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "updated_at", typ: "TEXT", notNull: true},
		},
		"traffic_buckets": {
			{name: "tunnel_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "owner_client_id", typ: "TEXT", notNull: true},
			{name: "ingress_client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "topology", typ: "TEXT", notNull: true},
			{name: "transport", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "tunnel_name", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "tunnel_type", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "resolution", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "bucket_start", typ: "INTEGER", notNull: true, primaryKey: true},
			{name: "ingress_bytes", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "egress_bytes", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"tunnel_resource_locks": {
			{name: "resource_key", typ: "TEXT", primaryKey: true},
			{name: "tunnel_id", typ: "TEXT", notNull: true},
			{name: "resource_kind", typ: "TEXT", notNull: true},
			{name: "client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "created_at", typ: "TEXT", notNull: true},
		},
	}
	assertSQLiteTables(t, db, wantTables)

	wantIndexes := map[string][]sqliteIndex{
		"schema_migrations": {{name: "sqlite_autoindex_schema_migrations_1", unique: true, columns: []string{"name"}}},
		"admin_users":       {{name: "sqlite_autoindex_admin_users_1", unique: true, columns: []string{"id"}}, {name: "sqlite_autoindex_admin_users_2", unique: true, columns: []string{"username"}}},
		"api_keys":          {{name: "sqlite_autoindex_api_keys_1", unique: true, columns: []string{"id"}}},
		"api_key_permissions": {
			{name: "sqlite_autoindex_api_key_permissions_1", unique: true, columns: []string{"api_key_id", "permission"}},
		},
		"registered_clients": {
			{name: "sqlite_autoindex_registered_clients_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_registered_clients_2", unique: true, columns: []string{"install_id"}},
		},
		"client_stats":           {{name: "sqlite_autoindex_client_stats_1", unique: true, columns: []string{"client_id"}}},
		"client_disk_partitions": {{name: "sqlite_autoindex_client_disk_partitions_1", unique: true, columns: []string{"client_id", "path"}}},
		"client_tokens": {
			{name: "idx_client_tokens_install_active", unique: false, columns: []string{"install_id", "is_revoked", "last_active_at"}},
			{name: "sqlite_autoindex_client_tokens_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_client_tokens_2", unique: true, columns: []string{"token_hash"}},
		},
		"admin_sessions": {
			{name: "idx_admin_sessions_expires", unique: false, columns: []string{"expires_at"}},
			{name: "idx_admin_sessions_user", unique: false, columns: []string{"user_id"}},
			{name: "sqlite_autoindex_admin_sessions_1", unique: true, columns: []string{"id"}},
		},
		"tunnels": {
			{name: "idx_tunnels_hostname", unique: false, columns: []string{"hostname"}},
			{name: "idx_tunnels_ingress_client", unique: false, columns: []string{"ingress_client_id"}},
			{name: "idx_tunnels_ingress_domain", unique: false, columns: []string{"ingress_domain"}},
			{name: "idx_tunnels_ingress_port", unique: false, columns: []string{"ingress_location", "ingress_client_id", "ingress_type", "ingress_bind_ip", "ingress_port"}},
			{name: "idx_tunnels_owner", unique: false, columns: []string{"owner_client_id", "created_at"}},
			{name: "idx_tunnels_runtime_state", unique: false, columns: []string{"runtime_state"}},
			{name: "idx_tunnels_target_client", unique: false, columns: []string{"target_client_id"}},
			{name: "idx_tunnels_target_resource", unique: false, columns: []string{"target_location", "target_client_id", "target_type", "target_resource_key"}},
			{name: "idx_tunnels_topology", unique: false, columns: []string{"topology"}},
			{name: "sqlite_autoindex_tunnels_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_tunnels_2", unique: true, columns: []string{"client_id", "name"}},
			{name: "sqlite_autoindex_tunnels_3", unique: true, columns: []string{"owner_client_id", "name"}},
		},
		"traffic_buckets": {
			{name: "idx_traffic_compat_query", unique: false, columns: []string{"client_id", "tunnel_name", "resolution", "bucket_start"}},
			{name: "idx_traffic_ingress_query", unique: false, columns: []string{"ingress_client_id", "resolution", "bucket_start"}},
			{name: "idx_traffic_owner_query", unique: false, columns: []string{"owner_client_id", "resolution", "bucket_start"}},
			{name: "idx_traffic_target_query", unique: false, columns: []string{"target_client_id", "resolution", "bucket_start"}},
			{name: "sqlite_autoindex_traffic_buckets_1", unique: true, columns: []string{"tunnel_id", "transport", "resolution", "bucket_start"}},
		},
		"tunnel_resource_locks": {
			{name: "idx_tunnel_resource_locks_client", unique: false, columns: []string{"client_id"}},
			{name: "idx_tunnel_resource_locks_tunnel", unique: false, columns: []string{"tunnel_id"}},
			{name: "sqlite_autoindex_tunnel_resource_locks_1", unique: true, columns: []string{"resource_key"}},
		},
	}
	assertSQLiteIndexes(t, db, wantIndexes)

	wantMigrationNames := []string{
		"001_server_runtime_schema",
		"002_rebuild_tunnels_without_registered_client_fk",
		"003_tunnel_stable_id",
		"004_tunnel_created_at",
		"005_unified_tunnel_storage",
	}
	if got := appliedMigrationNames(t, db); !reflect.DeepEqual(got, wantMigrationNames) {
		t.Fatalf("applied migrations = %#v, want %#v", got, wantMigrationNames)
	}
	if got := countTunnelRegisteredClientFKs(t, db); got != 0 {
		t.Fatalf("tunnels.client_id should not reference registered_clients, got %d FK(s)", got)
	}
}

func TestServerMigrationsLoadsEmbeddedFiles(t *testing.T) {
	migrations, err := serverMigrations()
	if err != nil {
		t.Fatalf("serverMigrations() error = %v", err)
	}

	var gotNames []string
	for _, migration := range migrations {
		gotNames = append(gotNames, migration.Name)
		if migration.Description == "" {
			t.Fatalf("migration %s should have Description", migration.Name)
		}
		if migration.CreatedAt == "" {
			t.Fatalf("migration %s should have CreatedAt", migration.Name)
		}
		if migration.Up == "" {
			t.Fatalf("migration %s should have Up SQL", migration.Name)
		}
	}
	wantNames := []string{
		"001_server_runtime_schema",
		"002_rebuild_tunnels_without_registered_client_fk",
		"003_tunnel_stable_id",
		"004_tunnel_created_at",
		"005_unified_tunnel_storage",
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("migration names = %#v, want %#v", gotNames, wantNames)
	}
}

func TestLoadMigrationsRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name    string
		files   fstest.MapFS
		wantErr string
	}{
		{
			name: "bad file name",
			files: fstest.MapFS{
				"migrations/1_bad.sql": {Data: []byte(validMigrationSQL("001_bad"))},
			},
			wantErr: "invalid migration file name",
		},
		{
			name: "name mismatch",
			files: fstest.MapFS{
				"migrations/001_good.sql": {Data: []byte(validMigrationSQL("001_other"))},
			},
			wantErr: "must match file name stem",
		},
		{
			name: "missing up",
			files: fstest.MapFS{
				"migrations/001_missing_up.sql": {Data: []byte(`-- Name: 001_missing_up
-- Description: Missing up.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Down:
`)},
			},
			wantErr: "-- Down: before -- Up",
		},
		{
			name: "duplicate version",
			files: fstest.MapFS{
				"migrations/001_first.sql":  {Data: []byte(validMigrationSQL("001_first"))},
				"migrations/001_second.sql": {Data: []byte(validMigrationSQL("001_second"))},
			},
			wantErr: "duplicate migration version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadMigrations(tt.files, "migrations")
			if err == nil {
				t.Fatal("loadMigrations() error = nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("loadMigrations() error = %q, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseMigrationFileAcceptsFlexibleSectionAndHeaderWhitespace(t *testing.T) {
	migration, err := parseMigrationFile("001_flexible_header.sql", `--  Name: 001_flexible_header
--	Description: Test flexible migration formatting.
-- CreatedAt: 2026-05-15T00:00:00Z

   -- Up:
SELECT 1;

	-- Down:
SELECT 0;
`)
	if err != nil {
		t.Fatalf("parseMigrationFile() error = %v", err)
	}
	if migration.Name != "001_flexible_header" {
		t.Fatalf("migration.Name = %q", migration.Name)
	}
	if migration.Up != "SELECT 1;" {
		t.Fatalf("migration.Up = %q", migration.Up)
	}
	if migration.Down != "SELECT 0;" {
		t.Fatalf("migration.Down = %q", migration.Down)
	}
}

func TestParseMigrationFileAllowsEmptyDownSQL(t *testing.T) {
	migration, err := parseMigrationFile("001_empty_down.sql", `-- Name: 001_empty_down
-- Description: Empty down SQL.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
SELECT 1;

-- Down:
`)
	if err != nil {
		t.Fatalf("parseMigrationFile() error = %v", err)
	}
	if migration.Down != "" {
		t.Fatalf("migration.Down = %q, want empty", migration.Down)
	}
}

func TestParseMigrationFileAcceptsUpAtFileStart(t *testing.T) {
	_, err := parseMigrationFile("001_no_header.sql", `-- Up:
SELECT 1;

-- Down:
`)
	if err == nil {
		t.Fatal("parseMigrationFile() error = nil")
	}
	if !strings.Contains(err.Error(), "missing Name header") {
		t.Fatalf("parseMigrationFile() error = %q, want missing Name header", err)
	}
}

func TestParseMigrationFileRejectsBareHeaderFields(t *testing.T) {
	_, err := parseMigrationFile("001_bare_header.sql", `Name: 001_bare_header
-- Description: Bare header.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
SELECT 1;

-- Down:
`)
	if err == nil {
		t.Fatal("parseMigrationFile() error = nil")
	}
	if !strings.Contains(err.Error(), "invalid header line") {
		t.Fatalf("parseMigrationFile() error = %q, want invalid header line", err)
	}
}

func TestOpenServerDBSkipsAppliedEmbeddedMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("first openServerDB() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	db, err = openServerDB(path)
	if err != nil {
		t.Fatalf("second openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations failed: %v", err)
	}
	if count != 5 {
		t.Fatalf("schema_migrations count = %d, want 5", count)
	}
}

func TestOpenServerDBRebuildsOldTunnelsFKSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	oldDB, err := storage.Open(path, []storage.Migration{{
		Name: "001_server_runtime_schema",
		Up: `
CREATE TABLE registered_clients (
	id TEXT PRIMARY KEY
);
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
INSERT INTO registered_clients (id) VALUES ('client-existing');
INSERT INTO tunnels (client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, desired_state, runtime_state, error, hostname, binding)
VALUES ('client-existing', 'existing', 'tcp', '127.0.0.1', 80, 18080, '', 100, 200, 'running', 'exposed', '', 'host-existing', 'client_id');
`,
	}})
	if err != nil {
		t.Fatalf("create old schema failed: %v", err)
	}
	if got := countTunnelRegisteredClientFKs(t, oldDB); got != 1 {
		t.Fatalf("old schema should have registered_clients FK, got %d", got)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatalf("oldDB.Close() error = %v", err)
	}

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	if got := countTunnelRegisteredClientFKs(t, db); got != 0 {
		t.Fatalf("migration should remove registered_clients FK, got %d", got)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	store, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("NewTunnelStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, ok := store.GetTunnel("client-existing", "existing"); !ok {
		t.Fatal("existing tunnel should survive FK rebuild")
	}
	if err := store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "orphan", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 81, RemotePort: 18081},
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		ClientID:        "client-without-registered-row",
		Hostname:        "host-orphan",
		Binding:         TunnelBindingClientID,
	}); err != nil {
		t.Fatalf("AddTunnel without registered client should succeed after FK rebuild: %v", err)
	}
}

func TestOpenServerDBPreservesLegacyTrafficWithoutTunnelMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	oldDB, err := storage.Open(path, []storage.Migration{{
		Name: "001_server_runtime_schema",
		Up: `
CREATE TABLE registered_clients (
	id TEXT PRIMARY KEY,
	install_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE tunnels (
	id TEXT NOT NULL DEFAULT '',
	client_id TEXT NOT NULL,
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
	created_at TEXT NOT NULL,
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
INSERT INTO registered_clients (id, install_id) VALUES ('client-existing', 'install-existing');
INSERT INTO traffic_buckets (client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes)
VALUES ('client-existing', 'deleted-tunnel', 'tcp', 'minute', 1700000000, 123, 456);
`,
	}})
	if err != nil {
		t.Fatalf("create old schema failed: %v", err)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatalf("oldDB.Close() error = %v", err)
	}

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var tunnelID, ownerClientID, targetClientID, topology, transport string
	var ingressBytes, egressBytes int64
	if err := db.QueryRow(`SELECT tunnel_id, owner_client_id, target_client_id, topology, transport, ingress_bytes, egress_bytes FROM traffic_buckets WHERE client_id = ? AND tunnel_name = ?`,
		"client-existing", "deleted-tunnel").Scan(&tunnelID, &ownerClientID, &targetClientID, &topology, &transport, &ingressBytes, &egressBytes); err != nil {
		t.Fatalf("query migrated orphan traffic: %v", err)
	}
	if tunnelID != "legacy:client-existing:deleted-tunnel:tcp" {
		t.Fatalf("synthetic tunnel_id = %q", tunnelID)
	}
	if ownerClientID != "client-existing" || targetClientID != "client-existing" || topology != "server_expose" || transport != "server_relay" {
		t.Fatalf("migrated metadata mismatch: owner=%q target=%q topology=%q transport=%q", ownerClientID, targetClientID, topology, transport)
	}
	if ingressBytes != 123 || egressBytes != 456 {
		t.Fatalf("migrated bytes mismatch: ingress=%d egress=%d", ingressBytes, egressBytes)
	}
}

func TestOpenServerDBDoesNotCreateJsonFiles(t *testing.T) {
	root := t.TempDir()
	db, err := openServerDB(filepath.Join(root, "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, name := range []string{"admin.json", "tunnels.json", "traffic.json"} {
		if pathExists(filepath.Join(root, "server", name)) {
			t.Fatalf("%s should not be created by SQLite storage", name)
		}
	}
}

func TestOpenServerDBRejectsInitializedConfigWithoutJWTSecret(t *testing.T) {
	db, err := openServerDB(filepath.Join(t.TempDir(), "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr) VALUES (1, 1, '', 'https://example.com')`); err == nil {
		t.Fatal("initialized server_config without jwt_secret should be rejected")
	}
	if _, err := db.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr) VALUES (1, 2, 'secret', 'https://example.com')`); err == nil {
		t.Fatal("non-boolean initialized value should be rejected")
	}
	if _, err := db.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr) VALUES (1, 1, 'secret', 'https://example.com')`); err != nil {
		t.Fatalf("valid initialized server_config should be accepted: %v", err)
	}
}

func sqliteTableExists(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, table string) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	return err == nil && name == table
}

func sqliteTableColumnExists(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, table, column string) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&name)
	return err == nil && name == column
}

func countTunnelRegisteredClientFKs(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list('tunnels') WHERE "table" = 'registered_clients'`).Scan(&count); err != nil {
		t.Fatalf("query tunnels foreign keys failed: %v", err)
	}
	return count
}

type sqliteColumn struct {
	name         string
	typ          string
	notNull      bool
	defaultValue string
	primaryKey   bool
}

type sqliteIndex struct {
	name    string
	unique  bool
	columns []string
}

func assertSQLiteTables(t *testing.T, db *sql.DB, want map[string][]sqliteColumn) {
	t.Helper()
	gotTables := sqliteUserTables(t, db)
	wantTableNames := sortedKeys(want)
	if !reflect.DeepEqual(gotTables, wantTableNames) {
		t.Fatalf("sqlite tables = %#v, want %#v", gotTables, wantTableNames)
	}

	for table, wantColumns := range want {
		gotColumns := sqliteColumns(t, db, table)
		if !reflect.DeepEqual(gotColumns, wantColumns) {
			t.Fatalf("sqlite columns for %s = %#v, want %#v", table, gotColumns, wantColumns)
		}
	}
}

func assertSQLiteIndexes(t *testing.T, db *sql.DB, want map[string][]sqliteIndex) {
	t.Helper()
	for table, wantIndexes := range want {
		gotIndexes := sqliteIndexes(t, db, table)
		if !reflect.DeepEqual(gotIndexes, wantIndexes) {
			t.Fatalf("sqlite indexes for %s = %#v, want %#v", table, gotIndexes, wantIndexes)
		}
	}
}

func sqliteUserTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("query sqlite tables: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan sqlite table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite tables: %v", err)
	}
	return tables
}

func sqliteColumns(t *testing.T, db *sql.DB, table string) []sqliteColumn {
	t.Helper()
	rows, err := db.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, ''), pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatalf("query sqlite columns for %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var columns []sqliteColumn
	for rows.Next() {
		var column sqliteColumn
		var notNull int
		var primaryKey int
		if err := rows.Scan(&column.name, &column.typ, &notNull, &column.defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan sqlite column for %s: %v", table, err)
		}
		column.notNull = notNull == 1
		column.primaryKey = primaryKey > 0
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite columns for %s: %v", table, err)
	}
	return columns
}

func sqliteIndexes(t *testing.T, db *sql.DB, table string) []sqliteIndex {
	t.Helper()
	rows, err := db.Query(`SELECT name, "unique" FROM pragma_index_list(?) ORDER BY name`, table)
	if err != nil {
		t.Fatalf("query sqlite indexes for %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var indexes []sqliteIndex
	for rows.Next() {
		var index sqliteIndex
		var unique int
		if err := rows.Scan(&index.name, &unique); err != nil {
			t.Fatalf("scan sqlite index for %s: %v", table, err)
		}
		index.unique = unique == 1
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite indexes for %s: %v", table, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close sqlite indexes for %s: %v", table, err)
	}
	for i := range indexes {
		indexes[i].columns = sqliteIndexColumns(t, db, indexes[i].name)
	}
	return indexes
}

func sqliteIndexColumns(t *testing.T, db *sql.DB, indexName string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, indexName)
	if err != nil {
		t.Fatalf("query sqlite index columns for %s: %v", indexName, err)
	}
	defer func() { _ = rows.Close() }()

	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan sqlite index column for %s: %v", indexName, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite index columns for %s: %v", indexName, err)
	}
	return columns
}

func appliedMigrationNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatalf("query applied migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan applied migration: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate applied migrations: %v", err)
	}
	return names
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	return true
}

func validMigrationSQL(name string) string {
	return `-- Name: ` + name + `
-- Description: Test migration.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
SELECT 1;

-- Down:
`
}
