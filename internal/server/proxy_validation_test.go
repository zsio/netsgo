package server

import (
	"path/filepath"
	"strings"
	"testing"

	"netsgo/pkg/protocol"
)

func newProxyValidationTestServer(t *testing.T, port int, serverAddr string, allowedPorts []PortRange) *Server {
	t.Helper()

	s := New(port)

	adminStore, err := NewAdminStore(filepath.Join(t.TempDir(), "admin.json"))
	if err != nil {
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}
	if err := adminStore.Initialize("admin", "password123", serverAddr, allowedPorts); err != nil {
		t.Fatalf("初始化 AdminStore 失败: %v", err)
	}
	s.adminStore = adminStore

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}
	s.store = store

	return s
}

func TestValidateProxyRequest_TCPUDPRequireExplicitRemotePort(t *testing.T) {
	s := newProxyValidationTestServer(t, 18080, "https://panel.example.com", nil)

	testCases := []struct {
		name string
		typ  string
	}{
		{name: "tcp", typ: protocol.ProxyTypeTCP},
		{name: "udp", typ: protocol.ProxyTypeUDP},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.validateProxyRequest(nil, protocol.ProxyNewRequest{
				Name:       "missing-port",
				Type:       tc.typ,
				LocalIP:    "127.0.0.1",
				LocalPort:  8080,
				RemotePort: 0,
			})
			if err == nil {
				t.Fatal("remote_port=0 时应返回校验错误")
			}
			if !strings.Contains(err.Error(), "必须填写明确的公网端口") {
				t.Fatalf("错误信息应提示必须填写明确端口，得到 %v", err)
			}
		})
	}
}

func TestValidateProxyRequest_RejectsReservedAndManagementPorts(t *testing.T) {
	s := newProxyValidationTestServer(t, 18080, "https://panel.example.com", []PortRange{{Start: 1, End: 65535}})

	testCases := []struct {
		name        string
		port        int
		expectInErr string
	}{
		{name: "reject port 80", port: 80, expectInErr: "不能使用保留端口"},
		{name: "reject port 443", port: 443, expectInErr: "不能使用保留端口"},
		{name: "reject management port", port: 18080, expectInErr: "管理服务监听端口"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.validateProxyRequest(nil, protocol.ProxyNewRequest{
				Name:       "bad-port",
				Type:       protocol.ProxyTypeTCP,
				LocalIP:    "127.0.0.1",
				LocalPort:  8080,
				RemotePort: tc.port,
			})
			if err == nil {
				t.Fatalf("端口 %d 应被拒绝", tc.port)
			}
			if !strings.Contains(err.Error(), tc.expectInErr) {
				t.Fatalf("错误信息应包含 %q，得到 %v", tc.expectInErr, err)
			}
		})
	}
}

func TestValidateProxyRequest_RejectsConflictsAcrossRuntimeAndStore(t *testing.T) {
	s := newProxyValidationTestServer(t, 28080, "https://panel.example.com", nil)

	seedStoredTunnel(t, s, "client-store", protocol.ProxyNewRequest{
		Name:       "stored-paused",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  3000,
		RemotePort: 19090,
	}, protocol.ProxyStatusPaused)

	liveClient := &ClientConn{
		ID:      "client-live",
		proxies: make(map[string]*ProxyTunnel),
	}
	liveClient.proxies["runtime-error"] = &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "runtime-error",
			Type:         protocol.ProxyTypeUDP,
			LocalIP:      "127.0.0.1",
			LocalPort:    5353,
			RemotePort:   19091,
			ClientID:     "client-live",
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateError,
		},
	}
	s.clients.Store(liveClient.ID, liveClient)

	testCases := []struct {
		name string
		port int
	}{
		{name: "conflict with stored paused tunnel", port: 19090},
		{name: "conflict with runtime error tunnel", port: 19091},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.validateProxyRequest(nil, protocol.ProxyNewRequest{
				Name:       "new-tunnel",
				Type:       protocol.ProxyTypeTCP,
				LocalIP:    "127.0.0.1",
				LocalPort:  8080,
				RemotePort: tc.port,
			})
			if err == nil {
				t.Fatalf("端口 %d 已被已有隧道占用，应返回冲突", tc.port)
			}
			if !strings.Contains(err.Error(), "已被隧道占用") {
				t.Fatalf("错误信息应提示端口冲突，得到 %v", err)
			}
		})
	}
}

func TestValidateProxyRequestWithExclusions_AllowsUpdatingSameTunnelPort(t *testing.T) {
	s := newProxyValidationTestServer(t, 38080, "https://panel.example.com", nil)

	client := &ClientConn{
		ID:      "client-edit",
		proxies: make(map[string]*ProxyTunnel),
	}
	client.proxies["editable"] = &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "editable",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   19100,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStatePaused,
			RuntimeState: protocol.ProxyRuntimeStateIdle,
		},
	}
	s.clients.Store(client.ID, client)

	seedStoredTunnel(t, s, client.ID, protocol.ProxyNewRequest{
		Name:       "editable",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  8080,
		RemotePort: 19100,
	}, protocol.ProxyStatusPaused)

	err := s.validateProxyRequestWithExclusions(client, protocol.ProxyNewRequest{
		Name:       "editable",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  9090,
		RemotePort: 19100,
	}, "editable", client.ID)
	if err != nil {
		t.Fatalf("编辑同一条隧道时不应把自己判成端口冲突: %v", err)
	}
}
