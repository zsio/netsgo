package client

import (
	"database/sql"
	"fmt"
	"os"
	"sync"

	"netsgo/internal/storage"
)

const clientDBFileName = "netsgo.db"

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
	if _, err := os.Stat(path); err != nil {
		return ClientIdentity{}, false, err
	}
	store, err := newClientStateStore(path)
	if err != nil {
		return ClientIdentity{}, false, err
	}
	defer func() { _ = store.Close() }()
	return store.Load()
}
