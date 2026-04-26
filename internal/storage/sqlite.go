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
