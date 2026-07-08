package svcmgr

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type runtimeOwnershipCall struct {
	op   string
	path string
	uid  int
	gid  int
	mode os.FileMode
}

func TestRepairClientRuntimeOwnershipSecuresStateFiles(t *testing.T) {
	layout := NewLayout(RoleClient)
	layout.RuntimeDir = filepath.Join(t.TempDir(), "client")
	if err := os.MkdirAll(layout.RuntimeDir, 0o777); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	dbPath := filepath.Join(layout.RuntimeDir, "netsgo.db")
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", filepath.Join(layout.RuntimeDir, "client.json")} {
		if err := os.WriteFile(path, []byte("state"), 0o666); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	var calls []runtimeOwnershipCall

	if err := repairClientRuntimeOwnership(layout, testRuntimeOwnershipOps(&calls)); err != nil {
		t.Fatalf("repairClientRuntimeOwnership() error = %v", err)
	}

	assertRuntimeOwnershipCall(t, calls, "chown", layout.RuntimeDir, 123, 456, 0)
	assertRuntimeOwnershipCall(t, calls, "chmod", layout.RuntimeDir, 0, 0, 0o750)
	for _, path := range clientRuntimeStatePaths(layout) {
		assertRuntimeOwnershipCall(t, calls, "chown", path, 123, 456, 0)
		assertRuntimeOwnershipCall(t, calls, "chmod", path, 0, 0, 0o600)
	}
}

func TestRepairClientRuntimeOwnershipRejectsSymlinkedStateFile(t *testing.T) {
	layout := NewLayout(RoleClient)
	layout.RuntimeDir = t.TempDir()
	target := filepath.Join(layout.RuntimeDir, "target")
	if err := os.WriteFile(target, []byte("state"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(target, filepath.Join(layout.RuntimeDir, "client.json")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	var calls []runtimeOwnershipCall

	err := repairClientRuntimeOwnership(layout, testRuntimeOwnershipOps(&calls))
	if err == nil || !strings.Contains(err.Error(), "symlinked runtime file") {
		t.Fatalf("repairClientRuntimeOwnership() error = %v, want symlink rejection", err)
	}
}

func TestCheckClientRuntimeStateRejectsSymlinkedRuntimeDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	layout := NewLayout(RoleClient)
	layout.RuntimeDir = filepath.Join(dir, "client")
	if err := os.Symlink(target, layout.RuntimeDir); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	err := CheckClientRuntimeState(layout)
	if err == nil || !strings.Contains(err.Error(), "symlinked runtime directory") {
		t.Fatalf("CheckClientRuntimeState() error = %v, want symlink rejection", err)
	}
}

func TestCheckClientRuntimeStateRejectsSymlinkedStateFile(t *testing.T) {
	layout := NewLayout(RoleClient)
	layout.RuntimeDir = t.TempDir()
	target := filepath.Join(layout.RuntimeDir, "target")
	if err := os.WriteFile(target, []byte("state"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(target, filepath.Join(layout.RuntimeDir, "client.json")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	err := CheckClientRuntimeState(layout)
	if err == nil || !strings.Contains(err.Error(), "symlinked runtime file") {
		t.Fatalf("CheckClientRuntimeState() error = %v, want symlink rejection", err)
	}
}

func testRuntimeOwnershipOps(calls *[]runtimeOwnershipCall) runtimeOwnershipOps {
	return runtimeOwnershipOps{
		lookup: func(string) (*user.User, error) {
			return &user.User{
				Username: SystemUser,
				Uid:      strconv.Itoa(123),
				Gid:      strconv.Itoa(456),
			}, nil
		},
		lstat: os.Lstat,
		chown: func(path string, uid, gid int) error {
			*calls = append(*calls, runtimeOwnershipCall{op: "chown", path: path, uid: uid, gid: gid})
			return nil
		},
		chmod: func(path string, mode os.FileMode) error {
			*calls = append(*calls, runtimeOwnershipCall{op: "chmod", path: path, mode: mode})
			return nil
		},
	}
}

func assertRuntimeOwnershipCall(t *testing.T, calls []runtimeOwnershipCall, op, path string, uid, gid int, mode os.FileMode) {
	t.Helper()
	for _, call := range calls {
		if call.op == op && call.path == path && call.uid == uid && call.gid == gid && call.mode == mode {
			return
		}
	}
	t.Fatalf("runtime ownership call %s %s uid=%d gid=%d mode=%v not found in %#v", op, path, uid, gid, mode, calls)
}
