package server

import (
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
	IngressBPS  int64                  `json:"ingress_bps"`
	EgressBPS   int64                  `json:"egress_bps"`
	LastSeen    *time.Time             `json:"last_seen,omitempty"`
	LastIP      string                 `json:"last_ip,omitempty"`
}

type serverStatusView struct {
	Status           string                     `json:"status"`
	ClientCount      int                        `json:"client_count"`
	Summary          consoleSummaryView         `json:"summary"`
	Version          string                     `json:"version"`
	UpdateCapability *protocol.UpdateCapability `json:"update_capability"`
	ListenPort       int                        `json:"listen_port"`
	Uptime           int64                      `json:"uptime"`
	SystemUptime     int64                      `json:"system_uptime"`
	OSInstallTime    int64                      `json:"os_install_time,omitempty"`
	TunnelActive     int                        `json:"tunnel_active"`
	TunnelStopped    int                        `json:"tunnel_stopped"`
	ServerAddr       string                     `json:"server_addr"`
	AllowedPorts     []PortRange                `json:"allowed_ports"`
	OSArch           string                     `json:"os_arch"`
	GoVersion        string                     `json:"go_version"`
	Hostname         string                     `json:"hostname"`
	IPAddress        string                     `json:"ip_address"`
	CPUUsage         float64                    `json:"cpu_usage"`
	CPUCores         int                        `json:"cpu_cores"`
	MemUsed          uint64                     `json:"mem_used"`
	MemTotal         uint64                     `json:"mem_total"`
	AppMemUsed       uint64                     `json:"app_mem_used"`
	AppMemSys        uint64                     `json:"app_mem_sys"`
	DiskUsed         uint64                     `json:"disk_used"`
	DiskTotal        uint64                     `json:"disk_total"`
	DiskPartitions   []protocol.DiskPartition   `json:"disk_partitions"`
	GoroutineCount   int                        `json:"goroutine_count"`
	PublicIPv4       string                     `json:"public_ipv4,omitempty"`
	PublicIPv6       string                     `json:"public_ipv6,omitempty"`
	GeneratedAt      time.Time                  `json:"generated_at"`
	FreshUntil       time.Time                  `json:"fresh_until"`
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
	encodeJSON(w, http.StatusOK, s.collectConsoleStatus())
}

func (s *Server) handleAPIConsoleSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot := s.collectSnapshot()
	log.Printf("🔎 console_snapshot clients=%d tunnels=%s", len(snapshot.Clients), summarizeSnapshotTunnelStates(snapshot.Clients))
	encodeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleAPIClients(w http.ResponseWriter, r *http.Request) {
	encodeJSON(w, http.StatusOK, s.collectClientViews())
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
				IngressBPS:  registered.IngressBPS,
				EgressBPS:   registered.EgressBPS,
				LastSeen:    &lastSeen,
				LastIP:      registered.LastIP,
			}
			if s.store != nil {
				proxies, _, err := s.storedProxyViewsForClient(registered.ID)
				if err != nil {
					log.Printf("⚠️ failed to load tunnels for client %s: %v", registered.ID, err)
				}
				view.Proxies = proxies
			}
			views[registered.ID] = view
		}
	}

	s.clients.Range(func(_, value any) bool {
		client := value.(*ClientConn)
		if !client.isLive() {
			return true
		}
		proxies, seen, err := s.storedProxyViewsForClient(client.ID)
		if err != nil {
			log.Printf("⚠️ failed to load tunnels for live client %s: %v", client.ID, err)
			proxies = []protocol.ProxyConfig{}
			seen = map[string]struct{}{}
		}
		configs := client.ProxyConfigsSnapshot()
		for _, config := range configs {
			key := proxyConfigViewKey(config)
			if _, exists := seen[key]; exists {
				continue
			}
			proxies = append(proxies, proxyConfigForClientView(config, true))
		}
		sort.Slice(proxies, func(i, j int) bool {
			if !proxies[i].CreatedAt.Equal(proxies[j].CreatedAt) {
				return proxies[i].CreatedAt.After(proxies[j].CreatedAt)
			}
			return proxies[i].Name < proxies[j].Name
		})

		view, ok := views[client.ID]
		if !ok {
			view = clientView{
				ID:      client.ID,
				Info:    client.GetInfo(),
				Proxies: []protocol.ProxyConfig{},
			}
		}
		settings := client.GetBandwidthSettings()
		now := time.Now()
		view.Info = client.GetInfo()
		if liveStats := client.GetStats(); liveStats != nil {
			view.Stats = liveStats
		}
		view.Proxies = proxies
		view.Online = true
		view.IngressBPS = settings.IngressBPS
		view.EgressBPS = settings.EgressBPS
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

func (s *Server) storedProxyViewsForClient(clientID string) ([]protocol.ProxyConfig, map[string]struct{}, error) {
	seen := map[string]struct{}{}
	if s.store == nil {
		return []protocol.ProxyConfig{}, seen, nil
	}
	stored, err := s.store.GetTunnelsByClientID(clientID)
	if err != nil {
		return nil, seen, err
	}
	proxies := make([]protocol.ProxyConfig, 0, len(stored))
	for _, tunnel := range stored {
		config := s.storedTunnelViewConfig(tunnel)
		seen[proxyConfigViewKey(config)] = struct{}{}
		proxies = append(proxies, config)
	}
	return proxies, seen, nil
}

func (s *Server) storedTunnelViewConfig(stored StoredTunnel) protocol.ProxyConfig {
	config := storedTunnelToProxyConfig(stored)
	spec := specFromStoredTunnel(stored, s)
	setProxyConfigStates(&config, spec.DesiredState, runtimeStateForProxyConfig(spec.RuntimeState), spec.Error)
	config.ActualTransport = spec.ActualTransport
	config.TransportPolicy = spec.TransportPolicy
	if len(spec.Issues) > 0 {
		config.Issues = append([]protocol.TunnelIssue(nil), spec.Issues...)
	}
	config.Capabilities = spec.Capabilities
	config.P2P = &protocol.P2PState{State: spec.P2P.State, Error: spec.P2P.Error, SessionID: spec.P2P.SessionID}
	config.Participants = &protocol.TunnelParticipants{
		Ingress: protocol.ParticipantRuntime{
			ClientID: spec.Participants.Ingress.ClientID,
			Role:     spec.Participants.Ingress.Role,
			State:    spec.Participants.Ingress.State,
			Revision: spec.Participants.Ingress.Revision,
			Error:    spec.Participants.Ingress.Error,
		},
		Target: protocol.ParticipantRuntime{
			ClientID: spec.Participants.Target.ClientID,
			Role:     spec.Participants.Target.Role,
			State:    spec.Participants.Target.State,
			Revision: spec.Participants.Target.Revision,
			Error:    spec.Participants.Target.Error,
		},
	}
	config.Transport = &protocol.TransportRuntime{
		Policy:   spec.Transport.Policy,
		Actual:   spec.Transport.Actual,
		P2PState: spec.Transport.P2PState,
		P2PError: spec.Transport.P2PError,
	}
	return config
}

func summarizeSnapshotTunnelStates(clients []clientView) string {
	var parts []string
	for _, client := range clients {
		for _, tunnel := range client.Proxies {
			parts = append(parts, fmt.Sprintf("%s/%s:%s", client.ID, tunnel.Name, tunnel.RuntimeState))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func proxyConfigViewKey(config protocol.ProxyConfig) string {
	if config.ID != "" {
		return config.ID
	}
	return config.Name
}

func (s *Server) serverStatusLoop() {
	go s.refreshPublicIPs()

	status := s.collectServerStatus()
	s.cachedStatusMu.Lock()
	s.cachedStatus = &status
	s.cachedStatusMu.Unlock()

	statusTicker := time.NewTicker(10 * time.Second)
	defer statusTicker.Stop()

	publicIPTicker := time.NewTicker(2 * time.Hour)
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
	changed := false
	if ipv4 != "" {
		changed = s.publicIPv4 != ipv4
		s.publicIPv4 = ipv4
	}
	if ipv6 != "" {
		changed = changed || s.publicIPv6 != ipv6
		s.publicIPv6 = ipv6
	}
	s.publicIPMu.Unlock()
	if ipv4 != "" || ipv6 != "" {
		log.Printf("🌐 Public IP refreshed: IPv4=%s IPv6=%s", ipv4, ipv6)
	}
	if changed {
		status := s.collectServerStatus()
		s.cachedStatusMu.Lock()
		s.cachedStatus = &status
		s.cachedStatusMu.Unlock()
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
	tunnelStopped := 0

	s.clients.Range(func(_, value any) bool {
		clientCount++
		a := value.(*ClientConn)
		a.RangeProxies(func(_ string, t *ProxyTunnel) bool {
			desiredState := canonicalDesiredState(t.Config.DesiredState)
			switch {
			case isTunnelExposed(t.Config):
				tunnelActive++
			case desiredState == protocol.ProxyDesiredStateStopped && t.Config.RuntimeState == protocol.ProxyRuntimeStateIdle:
				tunnelStopped++
			}
			return true
		})
		return true
	})

	serverAddr := ""
	var allowedPorts []PortRange
	if s.auth.adminStore != nil {
		// Non-authoritative status display: degrade this optional metadata
		// instead of failing the whole server status response.
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
		Status:           "running",
		ClientCount:      clientCount,
		Version:          buildversion.Current,
		UpdateCapability: s.serverUpdateCapability(now),
		ListenPort:       s.Port,
		Uptime:           int64(time.Since(s.startTime).Seconds()),
		SystemUptime:     int64(sysUptime),
		OSInstallTime:    osInstallTime,
		TunnelActive:     tunnelActive,
		TunnelStopped:    tunnelStopped,
		ServerAddr:       serverAddr,
		AllowedPorts:     allowedPorts,
		OSArch:           osArch,
		GoVersion:        goVersion,
		Hostname:         hostname,
		IPAddress:        ipAddr,
		CPUUsage:         cpuUsage,
		CPUCores:         cpuCores,
		MemUsed:          memUsed,
		MemTotal:         memTotal,
		AppMemUsed:       appMemUsed,
		AppMemSys:        appMemSys,
		DiskUsed:         diskUsed,
		DiskTotal:        diskTotal,
		DiskPartitions:   diskPartitions,
		GoroutineCount:   goroutines,
		PublicIPv4:       pubV4,
		PublicIPv6:       pubV6,
		GeneratedAt:      now,
		FreshUntil:       now.Add(serverStatusFreshnessWindow),
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
