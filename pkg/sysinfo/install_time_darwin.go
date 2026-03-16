package sysinfo

import (
	"os"
	"syscall"
	"time"
)

// GetOSInstallTime 返回 macOS 的安装时间（Unix 时间戳，秒）。
// 通过 /var/db/.AppleSetupDone 文件的创建时间判断。
func GetOSInstallTime() uint64 {
	info, err := os.Stat("/var/db/.AppleSetupDone")
	if err != nil {
		return 0
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	// Birthtimespec 是文件创建时间 (macOS 支持)
	birthTime := time.Unix(stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec)
	return uint64(birthTime.Unix())
}
