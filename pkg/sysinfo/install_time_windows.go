package sysinfo

import (
	"golang.org/x/sys/windows/registry"
)

// GetOSInstallTime 返回 Windows 的安装时间（Unix 时间戳，秒）。
// 通过注册表 HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\InstallDate 获取。
func GetOSInstallTime() uint64 {
	key, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return 0
	}
	defer key.Close()

	val, _, err := key.GetIntegerValue("InstallDate")
	if err != nil {
		return 0
	}
	return val
}
