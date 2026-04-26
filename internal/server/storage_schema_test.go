package server

import (
	"database/sql"
	"errors"
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

	for _, column := range []string{"initialized", "jwt_secret"} {
		if !sqliteTableColumnExists(t, db, "server_config", column) {
			t.Fatalf("expected server_config.%s to exist", column)
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
