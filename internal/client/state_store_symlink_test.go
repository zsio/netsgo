package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckClientTokenClearRejectsSymlinkedDatabaseSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client", clientDBFileName)
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	if err := store.Save(persistedState{InstallID: "client-install", Token: "tk-old"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	target := filepath.Join(dir, "target.wal")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(target, path+"-wal"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if err := CheckClientTokenClear(path); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("CheckClientTokenClear() error = %v, want symlink rejection", err)
	}
}

func TestCheckClientTokenClearAllowsMalformedLegacyJSONWhenDatabaseIdentityExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client", clientDBFileName)
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	if err := store.Save(persistedState{InstallID: "client-install", Token: "tk-old"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	legacyPath := filepath.Join(dir, "client", "client.json")
	if err := os.WriteFile(legacyPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := CheckClientTokenClear(path); err != nil {
		t.Fatalf("CheckClientTokenClear() error = %v", err)
	}
}

func TestClearClientTokenRejectsSymlinkedDatabase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	store, err := newClientStateStore(target)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	if err := store.Save(persistedState{InstallID: "client-target", Token: "tk-target"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	path := filepath.Join(dir, "client", clientDBFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, _, err := ClearClientToken(path); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ClearClientToken() error = %v, want symlink rejection", err)
	}
	got, ok, err := LoadClientIdentity(target)
	if err != nil {
		t.Fatalf("LoadClientIdentity() error = %v", err)
	}
	if !ok || got.Token != "tk-target" {
		t.Fatalf("symlink target token changed, state=%+v ok=%v", got, ok)
	}
}

func TestClearClientTokenRejectsSymlinkedDatabaseSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client", clientDBFileName)
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	if err := store.Save(persistedState{InstallID: "client-install", Token: "tk-old"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	target := filepath.Join(dir, "target.wal")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	walPath := path + "-wal"
	_ = os.Remove(walPath)
	if err := os.Symlink(target, walPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, _, err := ClearClientToken(path); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ClearClientToken() error = %v, want symlink rejection", err)
	}
	if info, err := os.Lstat(walPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("wal symlink should be unchanged, info=%v err=%v", info, err)
	}
	if err := os.Remove(walPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	got, ok, err := LoadClientIdentity(path)
	if err != nil {
		t.Fatalf("LoadClientIdentity() error = %v", err)
	}
	if !ok || got.Token != "tk-old" {
		t.Fatalf("sidecar symlink should not clear token, state=%+v ok=%v", got, ok)
	}
}

func TestClearClientTokenRejectsSymlinkedLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	writeLegacyClientState(t, target, persistedState{InstallID: "client-target", Token: "tk-target"})
	dbPath := filepath.Join(dir, "client", clientDBFileName)
	legacyPath := filepath.Join(dir, "client", "client.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Symlink(target, legacyPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, _, err := ClearClientToken(dbPath); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ClearClientToken() error = %v, want symlink rejection", err)
	}
	if info, err := os.Lstat(legacyPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("legacy symlink should be unchanged, info=%v err=%v", info, err)
	}
	got := readLegacyClientState(t, target)
	if got.Token != "tk-target" {
		t.Fatalf("symlink target token changed, state=%+v", got)
	}
}

func TestClearClientTokenRejectsSymlinkedLegacyJSONBeforeDatabaseClear(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "client", clientDBFileName)
	store, err := newClientStateStore(dbPath)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	if err := store.Save(persistedState{InstallID: "client-install", Token: "tk-old"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	target := filepath.Join(dir, "target.json")
	writeLegacyClientState(t, target, persistedState{InstallID: "client-target", Token: "tk-target"})
	legacyPath := filepath.Join(dir, "client", "client.json")
	if err := os.Symlink(target, legacyPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, _, err := ClearClientToken(dbPath); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ClearClientToken() error = %v, want symlink rejection", err)
	}
	got, ok, err := LoadClientIdentity(dbPath)
	if err != nil {
		t.Fatalf("LoadClientIdentity() error = %v", err)
	}
	if !ok || got.Token != "tk-old" {
		t.Fatalf("SQLite token should not be cleared after legacy symlink rejection, state=%+v ok=%v", got, ok)
	}
}
