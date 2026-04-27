package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
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
	defer func() { _ = db.Close() }()

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
	defer func() { _ = db2.Close() }()

	var value int
	if err := db2.QueryRow(`SELECT value FROM counter WHERE id = 1`).Scan(&value); err != nil {
		t.Fatalf("query migrated row failed: %v", err)
	}
	if value != 1 {
		t.Fatalf("migration should run once, value = %d", value)
	}
}

func TestOpenCreatesPrivateDatabaseFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix owner-only permission bits")
	}

	path := filepath.Join(t.TempDir(), "netsgo.db")
	db, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	assertPrivateFileMode(t, path)
}

func TestOpenTightensExistingDatabaseFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix owner-only permission bits")
	}

	path := filepath.Join(t.TempDir(), "netsgo.db")
	if err := os.WriteFile(path, nil, 0o666); err != nil {
		t.Fatalf("write existing DB file: %v", err)
	}

	db, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	assertPrivateFileMode(t, path)
}

func TestOpenCreatesPrivateSQLiteSidecarFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix owner-only permission bits")
	}

	path := filepath.Join(t.TempDir(), "netsgo.db")
	db, err := Open(path, []Migration{{
		Name: "001_create_widgets",
		Up:   `CREATE TABLE widgets (id TEXT PRIMARY KEY, name TEXT NOT NULL);`,
	}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`INSERT INTO widgets (id, name) VALUES ('w1', 'Widget')`); err != nil {
		t.Fatalf("insert into migrated table failed: %v", err)
	}

	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			assertPrivateFileMode(t, sidecar)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat sidecar %s: %v", sidecar, err)
		}
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

func assertPrivateFileMode(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != privateFileMode {
		t.Fatalf("%s mode = %o, want %o", path, got, privateFileMode)
	}
}
