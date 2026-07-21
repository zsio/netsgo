package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const privateFileMode = 0o600

type Migration struct {
	Name        string
	Description string
	CreatedAt   string
	Up          string
	Down        string
}

func Open(path string, migrations []Migration) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}
	if err := ensurePrivateSQLiteFile(path); err != nil {
		return nil, err
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
	if err := chmodSQLiteFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := applyMigrations(db, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := chmodSQLiteFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenReadOnly opens an existing SQLite database without creating the file,
// changing pragmas with write side effects, or applying migrations.
func OpenReadOnly(path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path must not be empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("sqlite path is a directory: %s", path)
	}

	db, err := sql.Open("sqlite", ReadOnlyDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	if err := configureReadOnly(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// ReadOnlyDSN returns a SQLite DSN that refuses writes and file creation.
func ReadOnlyDSN(path string) string {
	return readOnlyDSNForOS(path, runtime.GOOS)
}

func readOnlyDSNForOS(path, goos string) string {
	u := url.URL{Scheme: "file", Path: path}
	if goos == "windows" {
		u = windowsFileURL(path)
	} else {
		u.Path = filepath.ToSlash(path)
	}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	return u.String()
}

func windowsFileURL(path string) url.URL {
	slashPath := strings.ReplaceAll(path, `\`, `/`)
	if trimmed, ok := strings.CutPrefix(slashPath, "//"); ok {
		host, rest, ok := strings.Cut(trimmed, "/")
		if ok {
			return url.URL{Scheme: "file", Host: host, Path: "/" + rest}
		}
	}
	if len(slashPath) >= 2 && slashPath[1] == ':' && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return url.URL{Scheme: "file", Path: slashPath}
}

// TableExists checks sqlite_master without creating schema artifacts.
func TableExists(db *sql.DB, tableName string) (bool, error) {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, tableName).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func ensurePrivateSQLiteFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, privateFileMode)
	if err != nil {
		return fmt.Errorf("create sqlite database file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close sqlite database file: %w", err)
	}
	return chmodSQLiteFiles(path)
}

func chmodSQLiteFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := chmodIfExists(candidate, privateFileMode); err != nil {
			return err
		}
	}
	return nil
}

func chmodIfExists(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("secure sqlite file %s: %w", path, err)
	}
	return nil
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

func configureReadOnly(db *sql.DB) error {
	statements := []string{
		`PRAGMA query_only = ON;`,
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA busy_timeout = 5000;`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("configure sqlite read-only %q: %w", statement, err)
		}
	}
	return nil
}

func applyMigrations(db *sql.DB, migrations []Migration) error {
	return ApplyMigrations(db, migrations)
}

// ApplyMigrations applies strict migrations using the primary schema_migrations ledger.
func ApplyMigrations(db *sql.DB, migrations []Migration) error {
	return applyMigrationsToTable(db, "schema_migrations", migrations, true)
}

// ApplyCompatibleMigrations applies additive, backward-compatible migrations in
// a separate ledger. Unknown rows are intentionally tolerated so an older
// binary can open a database previously used by a newer binary.
func ApplyCompatibleMigrations(db *sql.DB, table string, migrations []Migration) error {
	return applyMigrationsToTable(db, table, migrations, false)
}

func applyMigrationsToTable(db *sql.DB, table string, migrations []Migration, rejectUnknown bool) error {
	if db == nil {
		return fmt.Errorf("sqlite database must not be nil")
	}
	tableName, err := sqliteIdentifier(table)
	if err != nil {
		return err
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	);`, tableName)); err != nil {
		return fmt.Errorf("create migration table %q: %w", table, err)
	}

	knownMigrations := make(map[string]struct{}, len(migrations))
	for _, migration := range migrations {
		if migration.Name == "" {
			return fmt.Errorf("sqlite migration name must not be empty")
		}
		if migration.Up == "" {
			return fmt.Errorf("sqlite migration %q has empty SQL", migration.Name)
		}
		if _, ok := knownMigrations[migration.Name]; ok {
			return fmt.Errorf("sqlite migration %q is duplicated", migration.Name)
		}
		knownMigrations[migration.Name] = struct{}{}
	}
	if rejectUnknown && len(migrations) > 0 {
		if err := rejectUnknownAppliedMigrations(db, tableName, knownMigrations); err != nil {
			return err
		}
	}

	for _, migration := range migrations {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %q: %w", migration.Name, err)
		}
		applied, err := migrationApplied(tx, tableName, migration.Name)
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
		if _, err := tx.Exec(fmt.Sprintf(`INSERT INTO %s (name, applied_at) VALUES (?, ?)`, tableName), migration.Name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %q: %w", migration.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %q: %w", migration.Name, err)
		}
	}
	return nil
}

func sqliteIdentifier(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("sqlite migration table name must not be empty")
	}
	for i := range len(name) {
		char := name[i]
		if (char >= 'a' && char <= 'z') || char == '_' || (i > 0 && char >= '0' && char <= '9') {
			continue
		}
		return "", fmt.Errorf("invalid sqlite migration table name %q", name)
	}
	return `"` + name + `"`, nil
}

func rejectUnknownAppliedMigrations(db *sql.DB, tableName string, knownMigrations map[string]struct{}) error {
	rows, err := db.Query(fmt.Sprintf(`SELECT name FROM %s ORDER BY name`, tableName))
	if err != nil {
		return fmt.Errorf("query applied migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan applied migration: %w", err)
		}
		if _, ok := knownMigrations[name]; !ok {
			return fmt.Errorf("sqlite database has unknown applied migration %q", name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate applied migrations: %w", err)
	}
	return nil
}

func migrationApplied(tx *sql.Tx, tableName, name string) (bool, error) {
	var existing string
	err := tx.QueryRow(fmt.Sprintf(`SELECT name FROM %s WHERE name = ?`, tableName), name).Scan(&existing)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query migration %q: %w", name, err)
	}
	return true, nil
}
