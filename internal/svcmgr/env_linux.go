//go:build linux

package svcmgr

import (
	"os"
	"strconv"
)

func repairEnvFileOwnership(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	account, err := lookupSystemUser(SystemUser)
	if err != nil {
		if isUnknownUser(err) {
			return nil
		}
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}

	if err := os.Chown(path, os.Getuid(), gid); err != nil {
		return err
	}
	return os.Chmod(path, 0o640)
}
