package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientStateStoreRoundTrip(t *testing.T) {
	store, err := newClientStateStore(filepath.Join(t.TempDir(), "client", clientDBFileName))
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	state := persistedState{InstallID: "client-install", Token: "tk-test", TLSFingerprint: "AA:BB"}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, ok, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok {
		t.Fatal("expected saved state")
	}
	if got != state {
		t.Fatalf("state = %+v, want %+v", got, state)
	}
}

func TestClientStateStoreRejectsEmptyInstallID(t *testing.T) {
	store, err := newClientStateStore(filepath.Join(t.TempDir(), "client", clientDBFileName))
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Save(persistedState{}); err == nil {
		t.Fatal("Save should reject an empty install_id")
	}
}

func TestClientStateStoreDoesNotCreateJsonFile(t *testing.T) {
	dir := t.TempDir()
	store, err := newClientStateStore(filepath.Join(dir, "client", clientDBFileName))
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Save(persistedState{InstallID: "client-install"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "client", "client.json")); !os.IsNotExist(err) {
		t.Fatalf("client.json should not exist, stat error = %v", err)
	}
}

func TestLoadClientIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client", clientDBFileName)
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	if err := store.Save(persistedState{InstallID: "client-install", Token: "tk-test", TLSFingerprint: "AA:BB"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got, ok, err := LoadClientIdentity(path)
	if err != nil {
		t.Fatalf("LoadClientIdentity() error = %v", err)
	}
	if !ok {
		t.Fatal("expected saved identity")
	}
	if got.InstallID != "client-install" || got.Token != "tk-test" || got.TLSFingerprint != "AA:BB" {
		t.Fatalf("LoadClientIdentity() = %+v", got)
	}
}

func TestLoadClientIdentityDoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client", clientDBFileName)
	if _, ok, err := LoadClientIdentity(path); err == nil || ok {
		t.Fatalf("LoadClientIdentity() error = %v, ok = %v; want missing-file error", err, ok)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("LoadClientIdentity should not create %s, stat error = %v", path, err)
	}
}
