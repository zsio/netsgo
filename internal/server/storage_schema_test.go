package server

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	defer db.Close()

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
