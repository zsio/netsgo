package manage

import (
	"path/filepath"
	"strings"
	"testing"

	"netsgo/internal/server"
	"netsgo/pkg/flock"
)

func TestResetAdminUser(t *testing.T) {
	dataDir := t.TempDir()
	store, err := server.NewAdminStore(filepath.Join(dataDir, "server", server.ServerDBFileName))
	if err != nil {
		t.Fatalf("NewAdminStore failed: %v", err)
	}
	if err := store.Initialize("admin", "Admin1234", "https://example.com", nil); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	_ = store.Close()

	if err := ResetAdminUser(dataDir, " root ", "NewPass123"); err != nil {
		t.Fatalf("ResetAdminUser failed: %v", err)
	}

	reloaded, err := server.NewAdminStore(filepath.Join(dataDir, "server", server.ServerDBFileName))
	if err != nil {
		t.Fatalf("reload admin store: %v", err)
	}
	defer func() { _ = reloaded.Close() }()
	if _, err := reloaded.ValidateAdminPassword("admin", "Admin1234"); err == nil {
		t.Fatal("old admin user should no longer work")
	}
	if _, err := reloaded.ValidateAdminPassword("root", "NewPass123"); err != nil {
		t.Fatalf("new admin user should work: %v", err)
	}
}

func TestResetAdminUserRequiresInitializedData(t *testing.T) {
	err := ResetAdminUser(t.TempDir(), "admin", "NewPass123")
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected uninitialized data error, got %v", err)
	}
}

func TestResetAdminUserRefusesWhenServerLockHeld(t *testing.T) {
	dataDir := t.TempDir()
	store, err := server.NewAdminStore(filepath.Join(dataDir, "server", server.ServerDBFileName))
	if err != nil {
		t.Fatalf("NewAdminStore failed: %v", err)
	}
	if err := store.Initialize("admin", "Admin1234", "https://example.com", nil); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	_ = store.Close()

	unlock, err := flock.TryLock(filepath.Join(dataDir, "locks", "server.lock"))
	if err != nil {
		t.Fatalf("hold server lock: %v", err)
	}
	defer unlock()

	err = ResetAdminUser(dataDir, "root", "NewPass123")
	if err == nil || !strings.Contains(err.Error(), "stop the server") {
		t.Fatalf("expected server lock error, got %v", err)
	}

	reloaded, err := server.NewAdminStore(filepath.Join(dataDir, "server", server.ServerDBFileName))
	if err != nil {
		t.Fatalf("reload admin store: %v", err)
	}
	defer func() { _ = reloaded.Close() }()
	if _, err := reloaded.ValidateAdminPassword("admin", "Admin1234"); err != nil {
		t.Fatalf("existing admin should be unchanged: %v", err)
	}
}
