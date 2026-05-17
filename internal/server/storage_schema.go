package server

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"netsgo/internal/storage"
)

const serverDBFileName = "netsgo.db"

const ServerDBFileName = serverDBFileName

const serverMigrationDir = "migrations"

var migrationFileNamePattern = regexp.MustCompile(`^\d{3}_[a-z0-9]+(?:_[a-z0-9]+)*\.sql$`)

func openServerDB(path string) (*sql.DB, error) {
	migrations, err := serverMigrations()
	if err != nil {
		return nil, err
	}
	return storage.Open(path, migrations)
}

func serverMigrations() ([]storage.Migration, error) {
	return loadMigrations(serverMigrationFS, serverMigrationDir)
}

func loadMigrations(fsys fs.FS, dir string) ([]storage.Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migration directory %q: %w", dir, err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	var migrations []storage.Migration
	seenNames := make(map[string]struct{})
	seenVersions := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if !migrationFileNamePattern.MatchString(entry.Name()) {
			return nil, fmt.Errorf("invalid migration file name %q", entry.Name())
		}

		raw, err := fs.ReadFile(fsys, path.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration file %q: %w", entry.Name(), err)
		}
		migration, err := parseMigrationFile(entry.Name(), string(raw))
		if err != nil {
			return nil, err
		}

		if _, ok := seenNames[migration.Name]; ok {
			return nil, fmt.Errorf("duplicate migration name %q", migration.Name)
		}
		seenNames[migration.Name] = struct{}{}

		version, err := migrationVersion(migration.Name)
		if err != nil {
			return nil, err
		}
		if existing, ok := seenVersions[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %q in %q and %q", version, existing, entry.Name())
		}
		seenVersions[version] = entry.Name()

		migrations = append(migrations, migration)
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no migration files found in %q", dir)
	}
	return migrations, nil
}

func parseMigrationFile(fileName, content string) (storage.Migration, error) {
	stem := strings.TrimSuffix(fileName, path.Ext(fileName))
	var migration storage.Migration

	header, up, down, err := splitMigrationSections(fileName, content)
	if err != nil {
		return storage.Migration{}, err
	}
	if err := parseMigrationHeader(fileName, header, &migration); err != nil {
		return storage.Migration{}, err
	}
	if migration.Name != stem {
		return storage.Migration{}, fmt.Errorf("migration %q name %q must match file name stem %q", fileName, migration.Name, stem)
	}

	migration.Up = strings.TrimSpace(up)
	migration.Down = strings.TrimSpace(down)
	if migration.Up == "" {
		return storage.Migration{}, fmt.Errorf("migration %q has empty Up SQL", fileName)
	}
	return migration, nil
}

func splitMigrationSections(fileName, content string) (string, string, string, error) {
	const (
		headerSection = "header"
		upSection     = "up"
		downSection   = "down"
	)

	var header, up, down strings.Builder
	section := headerSection
	seenUp := false
	seenDown := false
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "-- Up:":
			if seenUp {
				return "", "", "", fmt.Errorf("migration %q has duplicate -- Up: section", fileName)
			}
			if seenDown {
				return "", "", "", fmt.Errorf("migration %q has -- Up: after -- Down:", fileName)
			}
			seenUp = true
			section = upSection
			continue
		case "-- Down:":
			if seenDown {
				return "", "", "", fmt.Errorf("migration %q has duplicate -- Down: section", fileName)
			}
			if !seenUp {
				return "", "", "", fmt.Errorf("migration %q has -- Down: before -- Up:", fileName)
			}
			seenDown = true
			section = downSection
			continue
		}

		switch section {
		case headerSection:
			header.WriteString(line)
		case upSection:
			up.WriteString(line)
		case downSection:
			down.WriteString(line)
		}
	}
	if !seenUp {
		return "", "", "", fmt.Errorf("migration %q missing -- Up: section", fileName)
	}
	if !seenDown {
		return "", "", "", fmt.Errorf("migration %q missing -- Down: section", fileName)
	}
	return header.String(), up.String(), down.String(), nil
}

func parseMigrationHeader(fileName, header string, migration *storage.Migration) error {
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "--") {
			return fmt.Errorf("migration %q has invalid header line %q", fileName, line)
		}
		headerLine := strings.TrimSpace(strings.TrimPrefix(line, "--"))
		key, value, ok := strings.Cut(headerLine, ":")
		if !ok {
			return fmt.Errorf("migration %q has invalid header line %q", fileName, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "Name":
			migration.Name = value
		case "Description":
			migration.Description = value
		case "CreatedAt":
			migration.CreatedAt = value
		default:
			return fmt.Errorf("migration %q has unknown header field %q", fileName, key)
		}
	}
	if migration.Name == "" {
		return fmt.Errorf("migration %q missing Name header", fileName)
	}
	if migration.Description == "" {
		return fmt.Errorf("migration %q missing Description header", fileName)
	}
	if migration.CreatedAt == "" {
		return fmt.Errorf("migration %q missing CreatedAt header", fileName)
	}
	if _, err := time.Parse(time.RFC3339, migration.CreatedAt); err != nil {
		return fmt.Errorf("migration %q has invalid CreatedAt %q: %w", fileName, migration.CreatedAt, err)
	}
	return nil
}

func migrationVersion(name string) (string, error) {
	if len(name) < 3 {
		return "", fmt.Errorf("migration name %q is too short to contain a version", name)
	}
	version := name[:3]
	for _, digit := range version {
		if digit < '0' || digit > '9' {
			return "", fmt.Errorf("migration name %q must start with a three-digit version", name)
		}
	}
	if len(name) == 3 || name[3] != '_' {
		return "", fmt.Errorf("migration name %q must start with a three-digit version", name)
	}
	return version, nil
}
