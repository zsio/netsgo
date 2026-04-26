package server

import (
	"path/filepath"
	"testing"
)

func TestServerGetStorePath_DerivesFromDataDir(t *testing.T) {
	dataDir := t.TempDir()
	s := New(0)
	s.DataDir = dataDir

	want := filepath.Join(dataDir, "server", "tunnels.json")
	if got := s.getStorePath(); got != want {
		t.Fatalf("getStorePath() = %q, want %q", got, want)
	}
}

func TestServerInitStore_UsesDataDirLayout(t *testing.T) {
	dataDir := t.TempDir()
	s := New(0)
	s.DataDir = dataDir

	if err := s.initStore(); err != nil {
		t.Fatalf("initStore() error = %v", err)
	}

	if got, want := s.store.path, filepath.Join(dataDir, "server", "tunnels.json"); got != want {
		t.Fatalf("store.path = %q, want %q", got, want)
	}
	if got, want := s.auth.adminStore.path, filepath.Join(dataDir, "server", serverDBFileName); got != want {
		t.Fatalf("adminStore.path = %q, want %q", got, want)
	}
	if err := s.auth.adminStore.Close(); err != nil {
		t.Fatalf("adminStore.Close() error = %v", err)
	}
	if got, want := s.trafficStore.path, filepath.Join(dataDir, "server", "traffic.json"); got != want {
		t.Fatalf("trafficStore.path = %q, want %q", got, want)
	}
}
