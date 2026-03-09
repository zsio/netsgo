package client

import (
	"netsgo/pkg/protocol"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// CollectSystemStats 采集当前系统的运行状态
func CollectSystemStats() (*protocol.SystemStats, error) {
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

	// 磁盘信息（根目录）
	diskRoot := "/"
	if runtime.GOOS == "windows" {
		diskRoot = "C:"
	}
	diskInfo, err := disk.Usage(diskRoot)
	if err == nil {
		stats.DiskTotal = diskInfo.Total
		stats.DiskUsed = diskInfo.Used
		stats.DiskUsage = diskInfo.UsedPercent
	}

	// 网络 IO（所有网卡累计）
	netIO, err := net.IOCounters(false)
	if err == nil && len(netIO) > 0 {
		stats.NetSent = netIO[0].BytesSent
		stats.NetRecv = netIO[0].BytesRecv
	}

	// 系统运行时间
	uptime, err := host.Uptime()
	if err == nil {
		stats.Uptime = uptime
	}

	return stats, nil
}
