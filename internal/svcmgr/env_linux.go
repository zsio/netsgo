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

	// Keep service env files owned by the installer (normally root) and
	// readable by the netsgo service group. Runtime service processes need
	// read access to detect service-mode installs for upgrade guidance, while
	// the service user must not be able to rewrite credentials such as
	// NETSGO_KEY.
	if err := os.Chown(path, os.Getuid(), gid); err != nil {
		return err
	}
	return os.Chmod(path, 0o640)
}
