package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClearClientTokenPreservesIdentityAndFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client", clientDBFileName)
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	if err := store.Save(persistedState{
		InstallID:      "client-install",
		Token:          "tk-old",
		TLSFingerprint: "AA:BB",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got, ok, err := ClearClientToken(path)
	if err != nil {
		t.Fatalf("ClearClientToken() error = %v", err)
	}
	if !ok {
		t.Fatal("ClearClientToken should report a saved identity")
	}
	if got.InstallID != "client-install" || got.Token != "" || got.TLSFingerprint != "AA:BB" {
		t.Fatalf("ClearClientToken() = %+v", got)
	}

	reloaded, ok, err := LoadClientIdentity(path)
	if err != nil {
		t.Fatalf("LoadClientIdentity() error = %v", err)
	}
	if !ok || reloaded != got {
		t.Fatalf("reloaded identity = %+v ok=%v, want %+v", reloaded, ok, got)
	}
}

func TestClearClientTokenIgnoresMalformedLegacyJSONWhenDatabaseIdentityExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client", clientDBFileName)
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	if err := store.Save(persistedState{
		InstallID:      "client-install",
		Token:          "tk-old",
		TLSFingerprint: "AA:BB",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	legacyPath := filepath.Join(dir, "client", "client.json")
	if err := os.WriteFile(legacyPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, ok, err := ClearClientToken(path)
	if err != nil {
		t.Fatalf("ClearClientToken() error = %v", err)
	}
	if !ok {
		t.Fatal("ClearClientToken should report the SQLite identity")
	}
	if got.InstallID != "client-install" || got.Token != "" || got.TLSFingerprint != "AA:BB" {
		t.Fatalf("ClearClientToken() = %+v", got)
	}

	reloaded, ok, err := LoadClientIdentity(path)
	if err != nil {
		t.Fatalf("LoadClientIdentity() error = %v", err)
	}
	if !ok || reloaded != got {
		t.Fatalf("reloaded identity = %+v ok=%v, want %+v", reloaded, ok, got)
	}
}

func TestClearClientTokenDoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client", clientDBFileName)
	if _, ok, err := ClearClientToken(path); err != nil || ok {
		t.Fatalf("ClearClientToken() error = %v, ok = %v; want absent identity without error", err, ok)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ClearClientToken should not create %s, stat error = %v", path, err)
	}
}

func TestClearClientTokenClearsLegacyJSONWhenDatabaseMissing(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "client", clientDBFileName)
	legacyPath := filepath.Join(dataDir, "client", "client.json")
	writeLegacyClientState(t, legacyPath, persistedState{
		InstallID:      "client-legacy",
		Token:          "legacy-token",
		TLSFingerprint: "AA:BB:CC",
	})

	got, ok, err := ClearClientToken(dbPath)
	if err != nil {
		t.Fatalf("ClearClientToken() error = %v", err)
	}
	if !ok {
		t.Fatal("ClearClientToken should report legacy identity")
	}
	if got.InstallID != "client-legacy" || got.Token != "" || got.TLSFingerprint != "AA:BB:CC" {
		t.Fatalf("ClearClientToken() = %+v", got)
	}
	reloadedLegacy := readLegacyClientState(t, legacyPath)
	if reloadedLegacy.Token != "" {
		t.Fatalf("legacy token should be cleared on disk, got %q", reloadedLegacy.Token)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("ClearClientToken should not create %s, stat error = %v", dbPath, err)
	}

	c := New("ws://localhost:8080", "key")
	c.DataDir = dataDir
	if err := c.ensureInstallID(); err != nil {
		t.Fatalf("ensureInstallID() error = %v", err)
	}
	if c.InstallID != "client-legacy" {
		t.Fatalf("InstallID = %q, want client-legacy", c.InstallID)
	}
	if c.Token != "" {
		t.Fatalf("legacy token should not be reloaded after reauth clear, got %q", c.Token)
	}
	if c.TLSFingerprint != "AA:BB:CC" {
		t.Fatalf("TLSFingerprint = %q, want AA:BB:CC", c.TLSFingerprint)
	}
}

func writeLegacyClientState(t *testing.T, path string, state persistedState) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func readLegacyClientState(t *testing.T, path string) persistedState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return state
}
