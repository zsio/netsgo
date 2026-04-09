package svcmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentBinaryPath(t *testing.T) {
	path, err := CurrentBinaryPath()
	if err != nil {
		t.Fatalf("CurrentBinaryPath() should not return an error: %v", err)
	}
	if path == "" {
		t.Fatal("CurrentBinaryPath() should not be empty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("CurrentBinaryPath() should return an existing path: %v", err)
	}
}

func TestIsBinaryInstalledAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo")
	if isBinaryInstalledAt(path) {
		t.Fatal("a nonexistent path should not be considered installed")
	}

	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to write test binary: %v", err)
	}
	if !isBinaryInstalledAt(path) {
		t.Fatal("an existing executable file should be considered installed")
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("failed to change test permissions: %v", err)
	}
	if isBinaryInstalledAt(path) {
		t.Fatal("a non-executable file should not be considered installed")
	}
}
