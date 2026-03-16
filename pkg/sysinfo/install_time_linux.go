package sysinfo

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// GetOSInstallTime 返回 Linux 的安装时间（Unix 时间戳，秒）。
// 优先通过 stat 命令获取根文件系统创建时间 (%W)，
// 回退到 /var/log/installer 或 /root 目录的创建时间。
func GetOSInstallTime() uint64 {
	// 方法1: stat --format=%W / (需要支持 birth time 的文件系统，如 ext4 + kernel 4.11+)
	if out, err := exec.Command("stat", "--format=%W", "/").Output(); err == nil {
		s := strings.TrimSpace(string(out))
		if ts, err := strconv.ParseUint(s, 10, 64); err == nil && ts > 0 {
			return ts
		}
	}

	// 方法2: 检查 /var/log/installer 目录（Debian/Ubuntu 安装器留下的）
	for _, path := range []string{"/var/log/installer", "/root"} {
		var stat syscall.Stat_t
		if err := syscall.Stat(path, &stat); err == nil {
			// 在 Linux 上 Ctim 是 ctime（inode 变更时间），接近安装时间
			if stat.Ctim.Sec > 0 {
				return uint64(stat.Ctim.Sec)
			}
		}
	}

	return 0
}
