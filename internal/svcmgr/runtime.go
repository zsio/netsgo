package svcmgr

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

type runtimeOwnershipOps struct {
	lookup func(string) (*user.User, error)
	lstat  func(string) (os.FileInfo, error)
	chown  func(string, int, int) error
	chmod  func(string, os.FileMode) error
}

func repairClientRuntimeOwnership(layout ServiceLayout, ops runtimeOwnershipOps) error {
	account, err := ops.lookup(SystemUser)
	if err != nil {
		if isUnknownUser(err) {
			return nil
		}
		return err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	if err := secureRuntimeDir(layout.RuntimeDir, uid, gid, ops); err != nil {
		return err
	}
	for _, path := range clientRuntimeStatePaths(layout) {
		if err := secureRuntimeFile(path, uid, gid, ops); err != nil {
			return err
		}
	}
	return nil
}

func CheckClientRuntimeState(layout ServiceLayout) error {
	return checkClientRuntimeState(layout, os.Lstat)
}

func checkClientRuntimeState(layout ServiceLayout, lstat func(string) (os.FileInfo, error)) error {
	if err := checkRuntimeDir(layout.RuntimeDir, lstat); err != nil {
		return err
	}
	for _, path := range clientRuntimeStatePaths(layout) {
		if err := checkRuntimeFile(path, lstat); err != nil {
			return err
		}
	}
	return nil
}

func checkRuntimeDir(path string, lstat func(string) (os.FileInfo, error)) error {
	info, err := lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to use symlinked runtime directory: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("runtime path is not a directory: %s", path)
	}
	return nil
}

func checkRuntimeFile(path string, lstat func(string) (os.FileInfo, error)) error {
	info, err := lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to use symlinked runtime file: %s", path)
	}
	if info.IsDir() {
		return fmt.Errorf("runtime state path is a directory: %s", path)
	}
	return nil
}

func secureRuntimeDir(path string, uid, gid int, ops runtimeOwnershipOps) error {
	info, err := ops.lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to repair symlinked runtime directory: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("runtime path is not a directory: %s", path)
	}
	if err := ops.chown(path, uid, gid); err != nil {
		return err
	}
	return ops.chmod(path, 0o750)
}

func secureRuntimeFile(path string, uid, gid int, ops runtimeOwnershipOps) error {
	info, err := ops.lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to repair symlinked runtime file: %s", path)
	}
	if info.IsDir() {
		return fmt.Errorf("runtime state path is a directory: %s", path)
	}
	if err := ops.chown(path, uid, gid); err != nil {
		return err
	}
	return ops.chmod(path, 0o600)
}

func clientRuntimeStatePaths(layout ServiceLayout) []string {
	dbPath := filepath.Join(layout.RuntimeDir, "netsgo.db")
	return []string{
		dbPath,
		dbPath + "-wal",
		dbPath + "-shm",
		filepath.Join(layout.RuntimeDir, "client.json"),
	}
}
