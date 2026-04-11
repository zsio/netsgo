package server

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"netsgo/pkg/protocol"

	"golang.org/x/crypto/bcrypt"
)

func newProxyValidationTestServer(t *testing.T, port int, serverAddr string, allowedPorts []PortRange) *Server {
	t.Helper()

	s := New(port)

	adminStore, err := NewAdminStore(filepath.Join(t.TempDir(), "admin.json"))
	if err != nil {
		t.Fatalf("failed to create AdminStore: %v", err)
	}
	adminStore.bcryptCost = bcrypt.MinCost // Use the minimum cost in tests to avoid slowing down the suite
	if err := adminStore.Initialize("admin", "password123", serverAddr, allowedPorts); err != nil {
		t.Fatalf("failed to initialize AdminStore: %v", err)
	}
	s.auth.adminStore = adminStore

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
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
				t.Fatal("remote_port=0 should return a validation error")
			}
			if !strings.Contains(err.Error(), "TCP/UDP tunnels require an explicit remote port") {
				t.Fatalf("error should mention that an explicit port is required, got %v", err)
			}
		})
	}
}

func TestValidateProxyRequest_HTTPInvalidDomainReturnsTypedBadRequest(t *testing.T) {
	s := newProxyValidationTestServer(t, 18080, "https://panel.example.com", nil)

	err := s.validateProxyRequest(nil, protocol.ProxyNewRequest{
		Name:      "invalid-domain",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 8080,
		Domain:    "https://bad.example.com",
	})
	if err == nil {
		t.Fatal("an invalid domain should return a validation error")
	}

	var validationErr *proxyRequestValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected proxyRequestValidationError, got %T", err)
	}
	if validationErr.ErrorCode() != protocol.TunnelMutationErrorCodeDomainInvalid {
		t.Fatalf("error_code: want %q, got %q", protocol.TunnelMutationErrorCodeDomainInvalid, validationErr.ErrorCode())
	}
	if validationErr.Field() != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field: want %q, got %q", protocol.TunnelMutationFieldDomain, validationErr.Field())
	}
	if validationErr.StatusCode() != 400 {
		t.Fatalf("status: want 400, got %d", validationErr.StatusCode())
	}
}

func TestValidateProxyRequest_RejectsReservedAndManagementPorts(t *testing.T) {
	s := newProxyValidationTestServer(t, 18080, "https://panel.example.com", []PortRange{{Start: 1, End: 65535}})

	testCases := []struct {
		name        string
		port        int
		expectInErr string
	}{
		{name: "reject port 80", port: 80, expectInErr: "cannot use reserved port"},
		{name: "reject port 443", port: 443, expectInErr: "cannot use reserved port"},
		{name: "reject management port", port: 18080, expectInErr: "management service listen port"},
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
				t.Fatalf("port %d should be rejected", tc.port)
			}
			if !strings.Contains(err.Error(), tc.expectInErr) {
				t.Fatalf("error should contain %q, got %v", tc.expectInErr, err)
			}
		})
	}
}

func TestValidateProxyRequest_RejectsConflictsAcrossRuntimeAndStore(t *testing.T) {
	s := newProxyValidationTestServer(t, 28080, "https://panel.example.com", nil)

	seedStoredTunnel(t, s, "client-store", protocol.ProxyNewRequest{
		Name:       "stored-stopped",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  3000,
		RemotePort: 19090,
	}, protocol.ProxyStatusStopped)

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
		{name: "conflict with stored stopped tunnel", port: 19090},
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
				t.Fatalf("port %d is already occupied by an existing tunnel and should return a conflict", tc.port)
			}
			if !strings.Contains(err.Error(), "already in use by another tunnel") {
				t.Fatalf("error should indicate a port conflict, got %v", err)
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
			DesiredState: protocol.ProxyDesiredStateStopped,
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
	}, protocol.ProxyStatusStopped)

	err := s.validateProxyRequestWithExclusions(client, protocol.ProxyNewRequest{
		Name:       "editable",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  9090,
		RemotePort: 19100,
	}, "editable", client.ID)
	if err != nil {
		t.Fatalf("editing the same tunnel should not treat itself as a port conflict: %v", err)
	}
}
