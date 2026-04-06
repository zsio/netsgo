//go:build linux

package flock

import (
	"path/filepath"
	"testing"
)

func TestTryLock_DuplicateFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.lock")

	unlock, err := TryLock(path)
	if err != nil {
		t.Fatalf("first TryLock() error = %v", err)
	}
	defer unlock()

	unlock2, err := TryLock(path)
	if err == nil {
		unlock2()
		t.Fatal("second TryLock() should fail while the first lock is held")
	}
}
