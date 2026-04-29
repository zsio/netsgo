package client

import (
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
