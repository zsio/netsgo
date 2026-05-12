//go:build linux

package svcmgr

import (
	"os"
	"syscall"
	"testing"
)

func assertEnvFileGroup(t *testing.T, path string, wantGID int) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s stat type = %T, want *syscall.Stat_t", path, info.Sys())
	}
	if int(stat.Gid) != wantGID {
		t.Fatalf("%s group = %d, want %d", path, stat.Gid, wantGID)
	}
}
