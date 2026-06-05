package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"netsgo/internal/server"
	"netsgo/internal/svcmgr"

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

func TestManageResetAdminUserDefaultDataDirRerunsWithSudo(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"netsgo", "manage", "reset-admin-user", "--username", "root", "--password", "NewPass123"}
	t.Cleanup(func() {
		os.Args = origArgs
	})

	execErr := errors.New("exec called")
	var gotPath string
	var gotArgv []string

	err := rerunManageResetAdminUserWithSudoIfNeeded(svcmgr.ManagedDataDir, 1000, func(file string) (string, error) {
		if file != "sudo" {
			t.Fatalf("expected sudo lookup, got %q", file)
		}
		return "/tmp/custom/sudo", nil
	}, func(argv0 string, argv []string, envv []string) error {
		gotPath = argv0
		gotArgv = append([]string(nil), argv...)
		return execErr
	})

	if !errors.Is(err, execErr) {
		t.Fatalf("expected exec error, got %v", err)
	}
	if gotPath != "/tmp/custom/sudo" {
		t.Fatalf("expected resolved sudo path, got %q", gotPath)
	}
	wantArgv := append([]string{"sudo"}, os.Args...)
	if !reflect.DeepEqual(gotArgv, wantArgv) {
		t.Fatalf("expected argv %v, got %v", wantArgv, gotArgv)
	}
}

func TestManageResetAdminUserCustomDataDirDoesNotRerunWithSudo(t *testing.T) {
	calledLookup := false
	calledExec := false
	err := rerunManageResetAdminUserWithSudoIfNeeded(t.TempDir(), 1000, func(file string) (string, error) {
		calledLookup = true
		return "", exec.ErrNotFound
	}, func(argv0 string, argv []string, envv []string) error {
		calledExec = true
		return nil
	})

	if err != nil {
		t.Fatalf("custom data-dir should not rerun with sudo: %v", err)
	}
	if calledLookup || calledExec {
		t.Fatal("custom data-dir should not look up or exec sudo")
	}
}
