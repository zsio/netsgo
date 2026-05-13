//go:build linux

package svcmgr

import (
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
	"testing"
)

func TestRepairEnvFileOwnershipPrivileged(t *testing.T) {
	if os.Getenv("NETSGO_PRIVILEGED_ENV_TEST") != "1" {
		t.Skip("set NETSGO_PRIVILEGED_ENV_TEST=1 and run as root to verify service env ownership")
	}
	if os.Geteuid() != 0 {
		t.Skip("privileged env ownership test requires root")
	}

	account, err := user.Lookup(SystemUser)
	if err != nil {
		t.Fatalf("lookup %s user: %v", SystemUser, err)
	}
	wantGID, err := strconv.Atoi(account.Gid)
	if err != nil {
		t.Fatalf("parse %s gid %q: %v", SystemUser, account.Gid, err)
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod temp dir: %v", err)
	}
	path := dir + "/server.env"
	if err := os.WriteFile(path, []byte("NETSGO_PORT=9527\n"), 0o600); err != nil {
		t.Fatalf("write legacy env file: %v", err)
	}
	if err := os.Chown(path, 0, 0); err != nil {
		t.Fatalf("chown legacy env file: %v", err)
	}

	layout := NewLayout(RoleServer)
	layout.EnvPath = path
	if err := RepairEnvFileOwnership(layout); err != nil {
		t.Fatalf("RepairEnvFileOwnership() failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat repaired env file: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("env file permissions = %v, want 0640", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("env stat type = %T, want *syscall.Stat_t", info.Sys())
	}
	if stat.Uid != 0 {
		t.Fatalf("env file uid = %d, want 0", stat.Uid)
	}
	if int(stat.Gid) != wantGID {
		t.Fatalf("env file gid = %d, want %d", stat.Gid, wantGID)
	}

	if output, err := exec.Command("sudo", "-u", SystemUser, "test", "-r", path).CombinedOutput(); err != nil {
		t.Fatalf("%s user should read env file: %v: %s", SystemUser, err, output)
	}
	if err := exec.Command("sudo", "-u", SystemUser, "test", "-w", path).Run(); err == nil {
		t.Fatalf("%s user should not write env file", SystemUser)
	}
}
