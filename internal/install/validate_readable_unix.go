//go:build !windows

package install

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func isReadableByUser(info os.FileInfo, username string) (bool, error) {
	account, err := user.Lookup(username)
	if err != nil {
		return false, err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return false, err
	}
	if uid == 0 {
		return true, nil
	}
	gids, err := account.GroupIds()
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("不支持的文件 stat 类型")
	}
	mode := info.Mode().Perm()
	if int(stat.Uid) == uid && mode&0o400 != 0 {
		return true, nil
	}
	for _, gid := range gids {
		parsed, err := strconv.Atoi(gid)
		if err != nil {
			continue
		}
		if int(stat.Gid) == parsed && mode&0o040 != 0 {
			return true, nil
		}
	}
	return mode&0o004 != 0, nil
}
