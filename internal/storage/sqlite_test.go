package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestOpenRejectsUnknownAppliedMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo.db")
	db, err := Open(path, []Migration{{
		Name: "001_create_counter",
		Up:   `CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL);`,
	}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES ('999_future_schema', '2026-05-15T00:00:00Z')`); err != nil {
		t.Fatalf("insert unknown migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db, err = Open(path, []Migration{{
		Name: "001_create_counter",
		Up:   `CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL);`,
	}})
	if err == nil {
		_ = db.Close()
		t.Fatal("Open() error = nil")
	}
	if !strings.Contains(err.Error(), `unknown applied migration "999_future_schema"`) {
		t.Fatalf("Open() error = %q, want unknown applied migration", err)
	}
}

func TestOpenWithNoMigrationsAllowsUnknownAppliedMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo.db")
	db, err := Open(path, []Migration{{
		Name: "001_create_counter",
		Up:   `CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL);`,
	}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES ('999_future_schema', '2026-05-15T00:00:00Z')`); err != nil {
		t.Fatalf("insert unknown migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db, err = Open(path, nil)
	if err != nil {
		t.Fatalf("Open(path, nil) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestApplyCompatibleMigrationsToleratesUnknownLedgerRows(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "netsgo.db"), nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	migrations := []Migration{{
		Name: "001_add_compatible_value",
		Up:   `CREATE TABLE compatible_value (id INTEGER PRIMARY KEY);`,
	}}
	if err := ApplyCompatibleMigrations(db, "compatible_migrations", migrations); err != nil {
		t.Fatalf("first ApplyCompatibleMigrations() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO compatible_migrations (name, applied_at) VALUES ('999_future_compatible', '2026-07-21T00:00:00Z')`); err != nil {
		t.Fatalf("insert future compatible migration: %v", err)
	}
	if err := ApplyCompatibleMigrations(db, "compatible_migrations", migrations); err != nil {
		t.Fatalf("future compatible ledger row should be tolerated: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM compatible_value`).Scan(&count); err != nil {
		t.Fatalf("query compatible table: %v", err)
	}
}

func TestApplyCompatibleMigrationsRejectsInvalidLedgerName(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "netsgo.db"), nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	err = ApplyCompatibleMigrations(db, "schema_migrations; DROP TABLE schema_migrations", []Migration{{
		Name: "001_invalid_table",
		Up:   `SELECT 1;`,
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid sqlite migration table name") {
		t.Fatalf("ApplyCompatibleMigrations() error = %v", err)
	}
}

func TestOpenRejectsDuplicateMigrationNames(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "netsgo.db"), []Migration{
		{Name: "001_create_counter", Up: `CREATE TABLE counter (id INTEGER PRIMARY KEY);`},
		{Name: "001_create_counter", Up: `CREATE TABLE other_counter (id INTEGER PRIMARY KEY);`},
	})
	if err == nil {
		t.Fatal("Open() error = nil")
	}
	if !strings.Contains(err.Error(), `migration "001_create_counter" is duplicated`) {
		t.Fatalf("Open() error = %q, want duplicated migration", err)
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

func TestOpenReadOnlyDoesNotCreateDatabaseOrRunMigrations(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", "netsgo.db")
	if _, err := OpenReadOnly(missing); !os.IsNotExist(err) {
		t.Fatalf("OpenReadOnly(missing) error = %v, want os.IsNotExist", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("OpenReadOnly should not create missing DB, stat error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "netsgo.db")
	db, err := Open(path, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly(existing) error = %v", err)
	}
	defer func() { _ = ro.Close() }()

	exists, err := TableExists(ro, "widgets")
	if err != nil {
		t.Fatalf("TableExists() error = %v", err)
	}
	if exists {
		t.Fatal("OpenReadOnly should not run migrations or create unrelated tables")
	}
	if _, err := ro.Exec(`CREATE TABLE widgets (id TEXT PRIMARY KEY)`); err == nil {
		t.Fatal("OpenReadOnly should reject write statements")
	}
}

func TestOpenReadOnlyReadsExistingDatabaseAndRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo.db")
	db, err := Open(path, []Migration{{
		Name: "001_create_widgets",
		Up:   `CREATE TABLE widgets (id TEXT PRIMARY KEY, name TEXT NOT NULL); INSERT INTO widgets (id, name) VALUES ('w1', 'Widget');`,
	}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly(existing) error = %v", err)
	}
	defer func() { _ = ro.Close() }()

	var name string
	if err := ro.QueryRow(`SELECT name FROM widgets WHERE id = 'w1'`).Scan(&name); err != nil {
		t.Fatalf("read existing row through read-only DB: %v", err)
	}
	if name != "Widget" {
		t.Fatalf("read-only row name = %q, want Widget", name)
	}
	if _, err := ro.Exec(`INSERT INTO widgets (id, name) VALUES ('w2', 'Other')`); err == nil {
		t.Fatal("OpenReadOnly should reject writes")
	}
}

func TestReadOnlyDSNFormatsWindowsPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "drive absolute path",
			path: `C:\Users\alice\AppData\Local\netsgo\netsgo.db`,
			want: "file:///C:/Users/alice/AppData/Local/netsgo/netsgo.db?mode=ro",
		},
		{
			name: "unc path",
			path: `\\server\share\netsgo\netsgo.db`,
			want: "file://server/share/netsgo/netsgo.db?mode=ro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readOnlyDSNForOS(tt.path, "windows"); got != tt.want {
				t.Fatalf("readOnlyDSNForOS() = %q, want %q", got, tt.want)
			}
		})
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
