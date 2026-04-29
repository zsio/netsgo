package install

import (
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"netsgo/internal/svcmgr"
)

type userLookupFunc func(string) (*user.User, error)

func ensureManagedServerDirs() error {
	return ensureManagedRoleDirsWithRoot(svcmgr.ManagedDataDir, svcmgr.RoleServer, user.Lookup)
}

func ensureManagedClientDirs() error {
	return ensureManagedRoleDirsWithRoot(svcmgr.ManagedDataDir, svcmgr.RoleClient, user.Lookup)
}

func ensureManagedRoleDirsWithRoot(root string, role svcmgr.Role, lookup userLookupFunc) error {
	runtimeDir := filepath.Join(root, string(role))
	locksDir := filepath.Join(root, "locks")

	for _, dir := range []string{root, runtimeDir, locksDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o750); err != nil {
			return err
		}
	}

	account, err := lookup(svcmgr.SystemUser)
	if err != nil {
		return nil
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}

	if err := os.Chown(root, uid, gid); err != nil {
		return err
	}
	if err := chownTree(runtimeDir, uid, gid); err != nil {
		return err
	}
	return chownTree(locksDir, uid, gid)
}

func chownTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, uid, gid)
	})
}
