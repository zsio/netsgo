package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClientStatePath_DerivesFromDataDir(t *testing.T) {
	dataDir := t.TempDir()
	c := New("ws://localhost:8080", "key")
	c.DataDir = dataDir

	want := filepath.Join(dataDir, "client", clientDBFileName)
	if got := c.statePath(); got != want {
		t.Fatalf("statePath() = %q, want %q", got, want)
	}
}

func TestEnsureInstallID_WritesUnderClientSubdir(t *testing.T) {
	dataDir := t.TempDir()
	c := New("ws://localhost:8080", "key")
	c.DataDir = dataDir

	if err := c.ensureInstallID(); err != nil {
		t.Fatalf("ensureInstallID() error = %v", err)
	}

	path := filepath.Join(dataDir, "client", clientDBFileName)
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	state, ok, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok {
		t.Fatal("expected persisted client state")
	}
	if state.InstallID == "" {
		t.Fatal("persisted InstallID should not be empty")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "client", "client.json")); !os.IsNotExist(err) {
		t.Fatalf("client.json should not exist, stat error = %v", err)
	}
}

func TestEnsureInstallID_MigratesLegacyJSONState(t *testing.T) {
	dataDir := t.TempDir()
	c := New("ws://localhost:8080", "key")
	c.DataDir = dataDir

	legacy := persistedState{
		InstallID:      "client-legacy",
		Token:          "legacy-token",
		TLSFingerprint: "AA:BB:CC",
	}
	legacyPath := filepath.Join(dataDir, "client", "client.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := c.ensureInstallID(); err != nil {
		t.Fatalf("ensureInstallID() error = %v", err)
	}

	if c.InstallID != legacy.InstallID {
		t.Fatalf("InstallID = %q, want %q", c.InstallID, legacy.InstallID)
	}
	if c.Token != legacy.Token {
		t.Fatalf("Token = %q, want %q", c.Token, legacy.Token)
	}
	if c.TLSFingerprint != legacy.TLSFingerprint {
		t.Fatalf("TLSFingerprint = %q, want %q", c.TLSFingerprint, legacy.TLSFingerprint)
	}

	store, err := newClientStateStore(filepath.Join(dataDir, "client", clientDBFileName))
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	got, ok, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok {
		t.Fatal("expected migrated state")
	}
	if got != legacy {
		t.Fatalf("state = %+v, want %+v", got, legacy)
	}
}
