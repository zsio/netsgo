package server

import (
	"path/filepath"
	"testing"
)

func TestServerGetStorePath_DerivesFromDataDir(t *testing.T) {
	dataDir := t.TempDir()
	s := New(0)
	s.DataDir = dataDir

	want := filepath.Join(dataDir, "server", serverDBFileName)
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
	t.Cleanup(s.cleanupFailedStartup)

	if got, want := s.store.path, filepath.Join(dataDir, "server", serverDBFileName); got != want {
		t.Fatalf("store.path = %q, want %q", got, want)
	}
	if got, want := s.auth.adminStore.path, filepath.Join(dataDir, "server", serverDBFileName); got != want {
		t.Fatalf("adminStore.path = %q, want %q", got, want)
	}
	if err := s.auth.adminStore.Close(); err != nil {
		t.Fatalf("adminStore.Close() error = %v", err)
	}
	if got, want := s.trafficStore.path, filepath.Join(dataDir, "server", serverDBFileName); got != want {
		t.Fatalf("trafficStore.path = %q, want %q", got, want)
	}
	if pathExists(filepath.Join(dataDir, "server", "tunnels.json")) {
		t.Fatal("initStore should not create tunnels.json for tunnel storage")
	}
	if pathExists(filepath.Join(dataDir, "server", "traffic.json")) {
		t.Fatal("initStore should not create traffic.json for traffic storage")
	}
}

func TestServerInitStore_SharesSingleDBHandle(t *testing.T) {
	dataDir := t.TempDir()
	s := New(0)
	s.DataDir = dataDir

	if err := s.initStore(); err != nil {
		t.Fatalf("initStore() error = %v", err)
	}
	t.Cleanup(s.cleanupFailedStartup)

	if s.store.db != s.auth.adminStore.db {
		t.Fatal("tunnel and admin stores should share one server DB handle")
	}
	if s.store.db != s.trafficStore.db {
		t.Fatal("tunnel and traffic stores should share one server DB handle")
	}
	if s.store.db != s.activityStore.db {
		t.Fatal("tunnel and activity stores should share one server DB handle")
	}
	if s.store.activityStore != s.activityStore || s.auth.adminStore.activityStore != s.activityStore {
		t.Fatal("domain stores should share the server activity writer")
	}
	if s.store.trafficStore != s.trafficStore {
		t.Fatal("tunnel store should coordinate migrations with the server traffic store")
	}
	if got := s.trafficStore.accumulator.Load(); got != s.trafficAccumulator {
		t.Fatalf("traffic store accumulator = %p, want server accumulator %p", got, s.trafficAccumulator)
	}
}
