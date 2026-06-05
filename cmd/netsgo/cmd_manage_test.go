package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"netsgo/internal/server"

	"github.com/spf13/cobra"
)

func TestManageResetAdminUserCommand(t *testing.T) {
	dataDir := t.TempDir()
	store, err := server.NewAdminStore(filepath.Join(dataDir, "server", server.ServerDBFileName))
	if err != nil {
		t.Fatalf("NewAdminStore failed: %v", err)
	}
	if err := store.Initialize("admin", "Admin1234", "https://example.com", nil); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	_ = store.Close()

	var output bytes.Buffer
	cmd := &cobra.Command{
		Use:  "reset-admin-user",
		RunE: runResetAdminUserCommand,
	}
	addResetAdminUserFlags(cmd)
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{
		"--data-dir", dataDir,
		"--username", "root",
		"--password", "NewPass123",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("reset admin user command failed: %v", err)
	}
	if !strings.Contains(output.String(), `admin user reset to "root"`) {
		t.Fatalf("unexpected command output: %q", output.String())
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
