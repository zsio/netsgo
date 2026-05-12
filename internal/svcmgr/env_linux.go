//go:build linux

package svcmgr

import (
	"os"
	"strconv"
	"syscall"
)

func chownEnvFileForServiceUser(path string) error {
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

	uid := os.Getuid()
	if info, err := os.Stat(path); err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			uid = int(stat.Uid)
		}
	}
	return os.Chown(path, uid, gid)
}
