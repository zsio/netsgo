package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"netsgo/internal/server"

	"github.com/spf13/cobra"
)

func TestManageResetAdminPasswordCommand(t *testing.T) {
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
		Use:  "reset-admin-password",
		RunE: runResetAdminPasswordCommand,
	}
	addResetAdminPasswordFlags(cmd)
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{
		"--data-dir", dataDir,
		"--username", "admin",
		"--password", "NewPass123",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("reset admin password command failed: %v", err)
	}
	if !strings.Contains(output.String(), `admin password reset for "admin"`) {
		t.Fatalf("unexpected command output: %q", output.String())
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
