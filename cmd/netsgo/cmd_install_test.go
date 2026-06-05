package main

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestRerunInstallWithSudoIfNeededUsesLookedUpPath(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"netsgo", "install", "--client", "--server", "https://panel.example.com", "--key", "sk-test"}
	t.Cleanup(func() {
		os.Args = origArgs
	})

	execErr := errors.New("exec called")
	var gotPath string
	var gotArgv []string

	err := rerunInstallWithSudoIfNeeded(1000, func(file string) (string, error) {
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

func TestRerunInstallWithSudoIfNeededMissingSudoFailsClearly(t *testing.T) {
	calledExec := false
	err := rerunInstallWithSudoIfNeeded(1000, func(file string) (string, error) {
		if file != "sudo" {
			t.Fatalf("expected sudo lookup, got %q", file)
		}
		return "", exec.ErrNotFound
	}, func(argv0 string, argv []string, envv []string) error {
		calledExec = true
		return nil
	})

	if err == nil {
		t.Fatal("expected missing sudo error")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("expected wrapped exec.ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "sudo") || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("expected actionable sudo PATH error, got %v", err)
	}
	if calledExec {
		t.Fatal("exec should not run when sudo is missing")
	}
}
