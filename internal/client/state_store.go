package client

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"netsgo/internal/storage"
	"netsgo/pkg/fileutil"
)

const clientDBFileName = "netsgo.db"
const legacyClientJSONFileName = "client.json"

const ClientDBFileName = clientDBFileName

type ClientIdentity struct {
	InstallID      string `json:"install_id"`
	Token          string `json:"token,omitempty"`
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
}

type persistedState = ClientIdentity

type clientStateStore struct {
	path      string
	db        *sql.DB
	closeOnce sync.Once
	closeErr  error
}

func newClientStateStore(path string) (*clientStateStore, error) {
	db, err := storage.Open(path, clientMigrations())
	if err != nil {
		return nil, err
	}
	return &clientStateStore{path: path, db: db}, nil
}

func clientMigrations() []storage.Migration {
	return []storage.Migration{{
		Name: "001_client_identity",
		Up: `
CREATE TABLE client_identity (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	install_id TEXT NOT NULL,
	token TEXT NOT NULL DEFAULT '',
	tls_fingerprint TEXT NOT NULL DEFAULT ''
);
`,
	}}
}

func (s *clientStateStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

func (s *clientStateStore) Load() (persistedState, bool, error) {
	var state persistedState
	err := s.db.QueryRow(`SELECT install_id, token, tls_fingerprint FROM client_identity WHERE id = 1`).Scan(
		&state.InstallID,
		&state.Token,
		&state.TLSFingerprint,
	)
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
		state.InstallID,
		state.Token,
		state.TLSFingerprint,
	)
	if err != nil {
		return fmt.Errorf("save client identity: %w", err)
	}
	return nil
}

func LoadClientIdentity(path string) (ClientIdentity, bool, error) {
	db, err := storage.OpenReadOnly(path)
	if err != nil {
		return ClientIdentity{}, false, err
	}
	store := &clientStateStore{path: path, db: db}
	defer func() { _ = store.Close() }()
	hasIdentity, err := storage.TableExists(db, "client_identity")
	if err != nil {
		return ClientIdentity{}, false, err
	}
	if !hasIdentity {
		return ClientIdentity{}, false, nil
	}
	return store.Load()
}

func CheckClientTokenClear(path string) error {
	legacyPath := legacyClientStatePath(path)
	legacyExists, err := usableClientStateFile(legacyPath, "legacy client identity")
	if err != nil {
		return err
	}
	found, err := checkClientDBTokenClear(path)
	if err != nil {
		return err
	}
	if found || !legacyExists {
		return nil
	}
	return checkLegacyClientToken(legacyPath)
}

func ClearClientToken(path string) (ClientIdentity, bool, error) {
	legacyPath := legacyClientStatePath(path)
	if _, err := usableClientStateFile(legacyPath, "legacy client identity"); err != nil {
		return ClientIdentity{}, false, err
	}
	state, found, err := clearClientDBToken(path)
	if err != nil {
		return ClientIdentity{}, false, err
	}
	legacyState, legacyFound, err := clearLegacyClientToken(legacyPath)
	if err != nil {
		if found {
			return state, true, nil
		}
		return ClientIdentity{}, false, err
	}
	if found {
		return state, true, nil
	}
	return legacyState, legacyFound, nil
}

func checkClientDBTokenClear(path string) (bool, error) {
	exists, err := usableClientDBFiles(path)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	_, found, err := LoadClientIdentity(path)
	if err != nil {
		return false, fmt.Errorf("inspect client identity database: %w", err)
	}
	return found, nil
}

func clearClientDBToken(path string) (ClientIdentity, bool, error) {
	exists, err := usableClientDBFiles(path)
	if err != nil {
		return ClientIdentity{}, false, err
	}
	if !exists {
		return ClientIdentity{}, false, nil
	}

	store, err := newClientStateStore(path)
	if err != nil {
		return ClientIdentity{}, false, fmt.Errorf("open client identity database: %w", err)
	}
	defer func() { _ = store.Close() }()

	state, ok, err := store.Load()
	if err != nil {
		return ClientIdentity{}, false, fmt.Errorf("load client identity: %w", err)
	}
	if !ok {
		return ClientIdentity{}, false, nil
	}
	state.Token = ""
	if err := store.Save(state); err != nil {
		return ClientIdentity{}, false, fmt.Errorf("save client identity without token: %w", err)
	}
	return state, true, nil
}

func usableClientDBFiles(path string) (bool, error) {
	exists, err := usableClientStateFile(path, "client identity database")
	if err != nil || !exists {
		return exists, err
	}
	for _, sidecarPath := range []string{path + "-wal", path + "-shm"} {
		if _, err := usableClientStateFile(sidecarPath, "client identity database sidecar"); err != nil {
			return false, err
		}
	}
	return true, nil
}

func checkLegacyClientToken(path string) error {
	_, err := readLegacyClientIdentity(path)
	return err
}

func readLegacyClientIdentity(path string) (ClientIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClientIdentity{}, fmt.Errorf("read legacy client identity: %w", err)
	}

	var state ClientIdentity
	if err := json.Unmarshal(data, &state); err != nil {
		return ClientIdentity{}, fmt.Errorf("decode legacy client identity: %w", err)
	}
	return state, nil
}

func clearLegacyClientToken(path string) (ClientIdentity, bool, error) {
	exists, err := usableClientStateFile(path, "legacy client identity")
	if err != nil {
		return ClientIdentity{}, false, err
	}
	if !exists {
		return ClientIdentity{}, false, nil
	}

	state, err := readLegacyClientIdentity(path)
	if err != nil {
		return ClientIdentity{}, false, err
	}
	state.Token = ""
	updated, err := json.Marshal(state)
	if err != nil {
		return ClientIdentity{}, false, fmt.Errorf("encode legacy client identity: %w", err)
	}
	if err := fileutil.AtomicWriteFile(path, updated, 0o600); err != nil {
		return ClientIdentity{}, false, fmt.Errorf("write legacy client identity without token: %w", err)
	}
	return state, true, nil
}

func legacyClientStatePath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), legacyClientJSONFileName)
}

func usableClientStateFile(path, label string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("refusing to use symlinked %s: %s", label, path)
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s is a directory: %s", label, path)
	}
	return true, nil
}
