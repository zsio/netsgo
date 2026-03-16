package client

import (
	"regexp"
	"runtime"
	"strings"
	"time"

	"netsgo/pkg/protocol"
	"netsgo/pkg/sysinfo"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

// reDiskBase extracts the base physical disk identifier from device paths.
// macOS APFS: /dev/disk3s1s1 → "disk3"
// Linux SCSI: /dev/sda1 → "sda"
// Linux NVMe: /dev/nvme0n1p1 → "nvme0n1"
// Windows: C: → "C:"
var reDiskBase = regexp.MustCompile(`(disk\d+|sd[a-z]+|nvme\d+n\d+|[A-Z]:)`)

func baseDiskName(device string) string {
	m := reDiskBase.FindString(device)
	if m != "" {
		return m
	}
	return device
}

// CollectSystemStats 采集当前系统的运行状态
// processStart 为程序启动时间，用于计算 NetsGo 进程运行时长。
func CollectSystemStats(processStart time.Time) (*protocol.SystemStats, error) {
	stats := &protocol.SystemStats{
		NumCPU: runtime.NumCPU(),
	}

	// CPU 使用率（采样 1 秒）
	cpuPercent, err := cpu.Percent(1*time.Second, false)
	if err == nil && len(cpuPercent) > 0 {
		stats.CPUUsage = cpuPercent[0]
	}

	// 内存信息
	memInfo, err := mem.VirtualMemory()
	if err == nil {
		stats.MemTotal = memInfo.Total
		stats.MemUsed = memInfo.Used
		stats.MemUsage = memInfo.UsedPercent
	}

	// 磁盘信息 — 聚合所有物理分区，APFS 按物理磁盘去重
	partitions, err := disk.Partitions(false)
	if err == nil {
		seenDevices := map[string]bool{}
		for _, p := range partitions {
			switch p.Fstype {
			case "tmpfs", "devtmpfs", "devfs", "squashfs", "overlay", "proc", "sysfs",
				"cgroup", "cgroup2", "pstore", "securityfs", "debugfs", "tracefs", "autofs":
				continue
			}
			if strings.HasPrefix(p.Fstype, "fuse.") {
				continue
			}
			dedupKey := p.Device
			if p.Fstype == "apfs" {
				dedupKey = baseDiskName(p.Device)
			}
			if seenDevices[dedupKey] {
				continue
			}
			d, err := disk.Usage(p.Mountpoint)
			if err == nil && d.Total > 0 {
				seenDevices[dedupKey] = true
				stats.DiskPartitions = append(stats.DiskPartitions, protocol.DiskPartition{
					Path:  p.Mountpoint,
					Used:  d.Used,
					Total: d.Total,
				})
				stats.DiskUsed += d.Used
				stats.DiskTotal += d.Total
			}
		}
	}

	// Fallback: 如果没有获取到任何有效分区
	if len(stats.DiskPartitions) == 0 {
		diskRoot := "/"
		if runtime.GOOS == "windows" {
			diskRoot = "C:"
		}
		diskInfo, err := disk.Usage(diskRoot)
		if err == nil {
			stats.DiskTotal = diskInfo.Total
			stats.DiskUsed = diskInfo.Used
			stats.DiskPartitions = append(stats.DiskPartitions, protocol.DiskPartition{
				Path:  diskRoot,
				Used:  diskInfo.Used,
				Total: diskInfo.Total,
			})
		}
	}

	// 计算聚合使用率
	if stats.DiskTotal > 0 {
		stats.DiskUsage = float64(stats.DiskUsed) / float64(stats.DiskTotal) * 100
	}

	// 网络 IO（所有网卡累计）
	netIO, err := psnet.IOCounters(false)
	if err == nil && len(netIO) > 0 {
		stats.NetSent = netIO[0].BytesSent
		stats.NetRecv = netIO[0].BytesRecv
	}

	// 系统运行时间
	uptime, err := host.Uptime()
	if err == nil {
		stats.Uptime = uptime
	}

	// 程序运行时间
	if !processStart.IsZero() {
		stats.ProcessUptime = uint64(time.Since(processStart).Seconds())
	}

	// 系统安装时间
	stats.OSInstallTime = sysinfo.GetOSInstallTime()

	// 程序（NetsGo Client）自身内存占用
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	stats.AppMemUsed = m.Alloc
	stats.AppMemSys = m.Sys

	return stats, nil
}
