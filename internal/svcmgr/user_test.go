package svcmgr

import (
	"runtime"
	"testing"
)

func TestUserExists(t *testing.T) {
	exists, err := UserExists("root")
	if err != nil {
		t.Fatalf("UserExists(root) should not return an error: %v", err)
	}
	if !exists {
		t.Fatal("root user should exist")
	}

	exists, err = UserExists("netsgo-user-should-not-exist-xyz")
	if err != nil {
		t.Fatalf("UserExists(nonexistent) should not return an error: %v", err)
	}
	if exists {
		t.Fatal("a random nonexistent user should not exist")
	}
}

func TestEnsureUserStub(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("covered by the real implementation on Linux")
	}

	if err := EnsureUser("netsgo"); err != ErrUnsupportedPlatform {
		t.Fatalf("non-Linux platforms should return ErrUnsupportedPlatform, got %v", err)
	}
}
