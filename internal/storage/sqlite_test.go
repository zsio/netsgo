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
