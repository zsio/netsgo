//go:build !windows

package install

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"netsgo/internal/svcmgr"
)

func TestEnsureManagedRoleDirsWithRootSecuresParentRuntimeAndLocks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "netsgo")
	if err := os.MkdirAll(root, 0o777); err != nil {
		t.Fatalf("create loose root dir: %v", err)
	}

	current := &user.User{
		Uid: strconv.Itoa(os.Getuid()),
		Gid: strconv.Itoa(os.Getgid()),
	}
	if err := ensureManagedRoleDirsWithRoot(root, svcmgr.RoleServer, func(string) (*user.User, error) {
		return current, nil
	}); err != nil {
		t.Fatalf("ensureManagedRoleDirsWithRoot() error = %v", err)
	}

	for _, dir := range []string{
		root,
		filepath.Join(root, "server"),
		filepath.Join(root, "locks"),
	} {
		assertDirMode(t, dir, 0o750)
		assertOwner(t, dir, os.Getuid(), os.Getgid())
	}
}

func TestEnsureManagedRoleDirsWithRootChownsExistingRuntimeData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "netsgo")
	runtimeDir := filepath.Join(root, "server")
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		t.Fatalf("create runtime dir: %v", err)
	}
	existingFiles := []string{
		filepath.Join(runtimeDir, "netsgo.db"),
		filepath.Join(runtimeDir, "netsgo.db-wal"),
		filepath.Join(runtimeDir, "netsgo.db-shm"),
	}
	for _, existing := range existingFiles {
		if err := os.WriteFile(existing, []byte("sqlite"), 0o600); err != nil {
			t.Fatalf("write existing runtime file %s: %v", existing, err)
		}
	}

	current := &user.User{
		Uid: strconv.Itoa(os.Getuid()),
		Gid: strconv.Itoa(os.Getgid()),
	}
	if err := ensureManagedRoleDirsWithRoot(root, svcmgr.RoleServer, func(string) (*user.User, error) {
		return current, nil
	}); err != nil {
		t.Fatalf("ensureManagedRoleDirsWithRoot() error = %v", err)
	}

	for _, existing := range existingFiles {
		assertOwner(t, existing, os.Getuid(), os.Getgid())
	}
}

func TestEnsureManagedRoleDirsWithRootChownsExistingLockFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "netsgo")
	lockFile := filepath.Join(root, "locks", "server.lock")
	if err := os.MkdirAll(filepath.Dir(lockFile), 0o750); err != nil {
		t.Fatalf("create locks dir: %v", err)
	}
	if err := os.WriteFile(lockFile, []byte("lock"), 0o600); err != nil {
		t.Fatalf("write existing lock file: %v", err)
	}

	current := &user.User{
		Uid: strconv.Itoa(os.Getuid()),
		Gid: strconv.Itoa(os.Getgid()),
	}
	if err := ensureManagedRoleDirsWithRoot(root, svcmgr.RoleServer, func(string) (*user.User, error) {
		return current, nil
	}); err != nil {
		t.Fatalf("ensureManagedRoleDirsWithRoot() error = %v", err)
	}

	assertOwner(t, lockFile, os.Getuid(), os.Getgid())
}

func TestEnsureManagedRoleDirsWithRootRejectsSymlinkRuntimeEntries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "netsgo")
	runtimeDir := filepath.Join(root, "server")
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		t.Fatalf("create runtime dir: %v", err)
	}
	target := filepath.Join(t.TempDir(), "outside-db")
	if err := os.WriteFile(target, []byte("sqlite"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	link := filepath.Join(runtimeDir, "netsgo.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	current := &user.User{
		Uid: strconv.Itoa(os.Getuid()),
		Gid: strconv.Itoa(os.Getgid()),
	}
	err := ensureManagedRoleDirsWithRoot(root, svcmgr.RoleServer, func(string) (*user.User, error) {
		return current, nil
	})
	if err == nil {
		t.Fatal("ensureManagedRoleDirsWithRoot() error = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "refusing to chown symlink") {
		t.Fatalf("ensureManagedRoleDirsWithRoot() error = %q, want symlink rejection", err)
	}
}

func TestEnsureManagedRoleDirsWithRootRejectsSymlinkManagedDirsBeforeChmod(t *testing.T) {
	root := filepath.Join(t.TempDir(), "netsgo")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("create root dir: %v", err)
	}
	target := filepath.Join(t.TempDir(), "outside-runtime")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("create symlink target: %v", err)
	}
	runtimeDir := filepath.Join(root, "server")
	if err := os.Symlink(target, runtimeDir); err != nil {
		t.Fatalf("create runtime symlink: %v", err)
	}

	current := &user.User{
		Uid: strconv.Itoa(os.Getuid()),
		Gid: strconv.Itoa(os.Getgid()),
	}
	err := ensureManagedRoleDirsWithRoot(root, svcmgr.RoleServer, func(string) (*user.User, error) {
		return current, nil
	})
	if err == nil {
		t.Fatal("ensureManagedRoleDirsWithRoot() error = nil, want symlinked directory rejection")
	}
	if !strings.Contains(err.Error(), "refusing to secure symlinked managed directory") {
		t.Fatalf("ensureManagedRoleDirsWithRoot() error = %q, want symlinked directory rejection", err)
	}
	assertDirMode(t, target, 0o700)
}

func assertDirMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func assertOwner(t *testing.T, path string, wantUID, wantGID int) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s stat type = %T, want *syscall.Stat_t", path, info.Sys())
	}
	if int(stat.Uid) != wantUID || int(stat.Gid) != wantGID {
		t.Fatalf("%s owner = %d:%d, want %d:%d", path, stat.Uid, stat.Gid, wantUID, wantGID)
	}
}
