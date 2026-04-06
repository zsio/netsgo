//go:build !linux

package flock

import (
	"path/filepath"
	"testing"
)

func TestTryLock_StubReturnsUnlock(t *testing.T) {
	unlock, err := TryLock(filepath.Join(t.TempDir(), "server.lock"))
	if err != nil {
		t.Fatalf("TryLock() error = %v", err)
	}
	if unlock == nil {
		t.Fatal("TryLock() should return a non-nil unlock func")
	}

	unlock()
}
