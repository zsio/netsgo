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

	want := filepath.Join(dataDir, "client", "client.json")
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

	path := filepath.Join(dataDir, "client", "client.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if state.InstallID == "" {
		t.Fatal("persisted InstallID should not be empty")
	}
}
