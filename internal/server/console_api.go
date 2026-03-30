package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"netsgo/pkg/netutil"
	"netsgo/pkg/protocol"
	"netsgo/pkg/sysinfo"
	buildversion "netsgo/pkg/version"
)

type clientView struct {
	ID          string                 `json:"id"`
	DisplayName string                 `json:"display_name,omitempty"`
	Info        protocol.ClientInfo    `json:"info"`
	Stats       *protocol.SystemStats  `json:"stats,omitempty"`
	Proxies     []protocol.ProxyConfig `json:"proxies"`
	Online      bool                   `json:"online"`
	LastSeen    *time.Time             `json:"last_seen,omitempty"`
	LastIP      string                 `json:"last_ip,omitempty"`
}

type serverStatusView struct {
	Status         string                   `json:"status"`
	ClientCount    int                      `json:"client_count"`
	Summary        consoleSummaryView       `json:"summary"`
	Version        string                   `json:"version"`
	ListenPort     int                      `json:"listen_port"`
	Uptime         int64                    `json:"uptime"`
	SystemUptime   int64                    `json:"system_uptime"`
	OSInstallTime  int64                    `json:"os_install_time,omitempty"`
	StorePath      string                   `json:"store_path"`
	TunnelActive   int                      `json:"tunnel_active"`
	TunnelPaused   int                      `json:"tunnel_paused"`
	TunnelStopped  int                      `json:"tunnel_stopped"`
	ServerAddr     string                   `json:"server_addr"`
	AllowedPorts   []PortRange              `json:"allowed_ports"`
	OSArch         string                   `json:"os_arch"`
	GoVersion      string                   `json:"go_version"`
	Hostname       string                   `json:"hostname"`
	IPAddress      string                   `json:"ip_address"`
	CPUUsage       float64                  `json:"cpu_usage"`
	CPUCores       int                      `json:"cpu_cores"`
	MemUsed        uint64                   `json:"mem_used"`
	MemTotal       uint64                   `json:"mem_total"`
	AppMemUsed     uint64                   `json:"app_mem_used"`
	AppMemSys      uint64                   `json:"app_mem_sys"`
	DiskUsed       uint64                   `json:"disk_used"`
	DiskTotal      uint64                   `json:"disk_total"`
	DiskPartitions []protocol.DiskPartition `json:"disk_partitions"`
	GoroutineCount int                      `json:"goroutine_count"`
	PublicIPv4     string                   `json:"public_ipv4,omitempty"`
	PublicIPv6     string                   `json:"public_ipv6,omitempty"`
	GeneratedAt    time.Time                `json:"generated_at"`
	FreshUntil     time.Time                `json:"fresh_until"`
}

type consoleSnapshot struct {
	Clients      []clientView       `json:"clients"`
	Summary      consoleSummaryView `json:"summary"`
	ServerStatus serverStatusView   `json:"server_status"`
	GeneratedAt  time.Time          `json:"generated_at"`
	FreshUntil   time.Time          `json:"fresh_until"`
}

const (
	clientStatsFreshnessWindow  = 20 * time.Second
	serverStatusFreshnessWindow = 20 * time.Second
	snapshotFreshnessWindow     = 15 * time.Second
)

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.collectConsoleStatus())
}

func (s *Server) handleAPIConsoleSnapshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.collectSnapshot())
}

func (s *Server) handleAPIClients(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.collectClientViews())
}

func (s *Server) collectSnapshot() consoleSnapshot {
	now := time.Now()
	data := s.collectConsoleData()
	status := s.getCachedServerStatus()
	status.Summary = data.Summary
	return consoleSnapshot{
		Clients:      data.Clients,
		Summary:      data.Summary,
		ServerStatus: status,
		GeneratedAt:  now,
		FreshUntil:   now.Add(snapshotFreshnessWindow),
	}
}

func (s *Server) collectConsoleStatus() serverStatusView {
	status := s.getCachedServerStatus()
	status.Summary = summarizeConsoleClients(s.collectClientViews())
	return status
}

func (s *Server) collectClientViews() []clientView {
	views := make(map[string]clientView)

	if s.auth.adminStore != nil {
		for _, registered := range s.auth.adminStore.GetRegisteredClients() {
			lastSeen := registered.LastSeen
			view := clientView{
				ID:          registered.ID,
				DisplayName: registered.DisplayName,
				Info:        registered.Info,
				Stats:       registered.Stats,
				Proxies:     []protocol.ProxyConfig{},
				Online:      false,
				LastSeen:    &lastSeen,
				LastIP:      registered.LastIP,
			}
			if s.store != nil {
				stored := s.store.GetTunnelsByClientID(registered.ID)
				view.Proxies = make([]protocol.ProxyConfig, 0, len(stored))
				for _, tunnel := range stored {
					view.Proxies = append(view.Proxies, proxyConfigForClientView(storedTunnelToProxyConfig(tunnel), false))
				}
			}
			views[registered.ID] = view
		}
	}

	s.clients.Range(func(_, value any) bool {
		client := value.(*ClientConn)
		if !client.isLive() {
			return true
		}
		proxies := make([]protocol.ProxyConfig, 0)
		client.RangeProxies(func(_ string, tunnel *ProxyTunnel) bool {
			proxies = append(proxies, proxyConfigForClientView(tunnel.Config, true))
			return true
		})
		sort.Slice(proxies, func(i, j int) bool { return proxies[i].Name < proxies[j].Name })

		view, ok := views[client.ID]
		if !ok {
			view = clientView{
				ID:      client.ID,
				Info:    client.GetInfo(),
				Proxies: []protocol.ProxyConfig{},
			}
		}
		now := time.Now()
		view.Info = client.GetInfo()
		if liveStats := client.GetStats(); liveStats != nil {
			view.Stats = liveStats
		}
		view.Proxies = proxies
		view.Online = true
		view.LastSeen = &now
		view.LastIP = remoteIP(client.RemoteAddr)
		views[client.ID] = view
		return true
	})

	clients := make([]clientView, 0, len(views))
	for _, client := range views {
		if client.Proxies == nil {
			client.Proxies = []protocol.ProxyConfig{}
		}
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].Info.Hostname < clients[j].Info.Hostname })

	return clients
}

func (s *Server) serverStatusLoop() {
	go s.refreshPublicIPs()

	status := s.collectServerStatus()
	s.cachedStatusMu.Lock()
	s.cachedStatus = &status
	s.cachedStatusMu.Unlock()

	statusTicker := time.NewTicker(10 * time.Second)
	defer statusTicker.Stop()

	publicIPTicker := time.NewTicker(5 * time.Minute)
	defer publicIPTicker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-statusTicker.C:
			status := s.collectServerStatus()
			s.cachedStatusMu.Lock()
			s.cachedStatus = &status
			s.cachedStatusMu.Unlock()
		case <-publicIPTicker.C:
			s.refreshPublicIPs()
		}
	}
}

func (s *Server) refreshPublicIPs() {
	ipv4, ipv6 := netutil.FetchPublicIPs()
	s.publicIPMu.Lock()
	if ipv4 != "" {
		s.publicIPv4 = ipv4
	}
	if ipv6 != "" {
		s.publicIPv6 = ipv6
	}
	s.publicIPMu.Unlock()
	if ipv4 != "" || ipv6 != "" {
		log.Printf("🌐 公网 IP 已刷新: IPv4=%s IPv6=%s", ipv4, ipv6)
	}
}

func (s *Server) getCachedServerStatus() serverStatusView {
	s.cachedStatusMu.RLock()
	defer s.cachedStatusMu.RUnlock()
	if s.cachedStatus != nil {
		return *s.cachedStatus
	}
	return s.collectServerStatus()
}

func (s *Server) collectServerStatus() serverStatusView {
	now := time.Now()
	clientCount := 0
	tunnelActive := 0
	tunnelPaused := 0
	tunnelStopped := 0

	s.clients.Range(func(_, value any) bool {
		clientCount++
		a := value.(*ClientConn)
		a.RangeProxies(func(_ string, t *ProxyTunnel) bool {
			switch {
			case isTunnelExposed(t.Config):
				tunnelActive++
			case t.Config.DesiredState == protocol.ProxyDesiredStatePaused && t.Config.RuntimeState == protocol.ProxyRuntimeStateIdle:
				tunnelPaused++
			case t.Config.DesiredState == protocol.ProxyDesiredStateStopped && t.Config.RuntimeState == protocol.ProxyRuntimeStateIdle:
				tunnelStopped++
			}
			return true
		})
		return true
	})

	serverAddr := ""
	var allowedPorts []PortRange
	if s.auth.adminStore != nil {
		config := s.auth.adminStore.GetServerConfig()
		serverAddr = config.ServerAddr
		allowedPorts = config.AllowedPorts
	}
	if allowedPorts == nil {
		allowedPorts = []PortRange{}
	}

	osArch := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	goVersion := runtime.Version()
	goroutines := runtime.NumGoroutine()
	hostname, _ := os.Hostname()
	ipAddr := netutil.GetOutboundIP()

	s.publicIPMu.RLock()
	pubV4 := s.publicIPv4
	pubV6 := s.publicIPv6
	s.publicIPMu.RUnlock()

	cpuPercents, _ := cpu.Percent(0, false)
	cpuUsage := 0.0
	if len(cpuPercents) > 0 {
		cpuUsage = cpuPercents[0]
	}
	cpuCores, _ := cpu.Counts(true)

	v, _ := mem.VirtualMemory()
	memUsed := uint64(0)
	memTotal := uint64(0)
	if v != nil {
		memUsed = v.Used
		memTotal = v.Total
	}

	var diskPartitions []protocol.DiskPartition
	diskUsed := uint64(0)
	diskTotal := uint64(0)

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
				diskPartitions = append(diskPartitions, protocol.DiskPartition{
					Path:  p.Mountpoint,
					Used:  d.Used,
					Total: d.Total,
				})
				diskUsed += d.Used
				diskTotal += d.Total
			}
		}
	}

	if len(diskPartitions) == 0 {
		d, _ := disk.Usage(filepath.Dir(s.getStorePath()))
		if d == nil {
			d, _ = disk.Usage("/")
		}
		if d != nil {
			diskUsed = d.Used
			diskTotal = d.Total
			diskPartitions = append(diskPartitions, protocol.DiskPartition{
				Path:  d.Path,
				Used:  d.Used,
				Total: d.Total,
			})
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	appMemUsed := m.Alloc
	appMemSys := m.Sys

	sysUptime, _ := host.Uptime()
	osInstallTime := int64(sysinfo.GetOSInstallTime())

	return serverStatusView{
		Status:         "running",
		ClientCount:    clientCount,
		Version:        buildversion.Current,
		ListenPort:     s.Port,
		Uptime:         int64(time.Since(s.startTime).Seconds()),
		SystemUptime:   int64(sysUptime),
		OSInstallTime:  osInstallTime,
		StorePath:      s.getStorePath(),
		TunnelActive:   tunnelActive,
		TunnelPaused:   tunnelPaused,
		TunnelStopped:  tunnelStopped,
		ServerAddr:     serverAddr,
		AllowedPorts:   allowedPorts,
		OSArch:         osArch,
		GoVersion:      goVersion,
		Hostname:       hostname,
		IPAddress:      ipAddr,
		CPUUsage:       cpuUsage,
		CPUCores:       cpuCores,
		MemUsed:        memUsed,
		MemTotal:       memTotal,
		AppMemUsed:     appMemUsed,
		AppMemSys:      appMemSys,
		DiskUsed:       diskUsed,
		DiskTotal:      diskTotal,
		DiskPartitions: diskPartitions,
		GoroutineCount: goroutines,
		PublicIPv4:     pubV4,
		PublicIPv6:     pubV6,
		GeneratedAt:    now,
		FreshUntil:     now.Add(serverStatusFreshnessWindow),
	}
}

var reDiskBase = regexp.MustCompile(`(disk\d+|sd[a-z]+|nvme\d+n\d+|[A-Z]:)`)

func baseDiskName(device string) string {
	m := reDiskBase.FindString(device)
	if m != "" {
		return m
	}
	return device
}

func (s *Server) getStorePath() string {
	if s.store != nil {
		return s.store.path
	}
	return s.StorePath
}
