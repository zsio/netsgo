package manage

import (
	"path/filepath"
	"strings"
	"testing"

	"netsgo/internal/server"
)

func TestResetAdminPassword(t *testing.T) {
	dataDir := t.TempDir()
	store, err := server.NewAdminStore(filepath.Join(dataDir, "server", server.ServerDBFileName))
	if err != nil {
		t.Fatalf("NewAdminStore failed: %v", err)
	}
	if err := store.Initialize("admin", "Admin1234", "https://example.com", nil); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	_ = store.Close()

	if err := ResetAdminPassword(dataDir, " admin ", "NewPass123"); err != nil {
		t.Fatalf("ResetAdminPassword failed: %v", err)
	}

	reloaded, err := server.NewAdminStore(filepath.Join(dataDir, "server", server.ServerDBFileName))
	if err != nil {
		t.Fatalf("reload admin store: %v", err)
	}
	defer func() { _ = reloaded.Close() }()
	if _, err := reloaded.ValidateAdminPassword("admin", "NewPass123"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}
}

func TestResetAdminPasswordRequiresInitializedData(t *testing.T) {
	err := ResetAdminPassword(t.TempDir(), "admin", "NewPass123")
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected uninitialized data error, got %v", err)
	}
}
