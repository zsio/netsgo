package server

import (
	"fmt"
	"net"
	"net/http"
	"testing"

	"netsgo/pkg/protocol"
)

func TestOfflineManagedTunnel_Create_StoreFirstAcrossTypes(t *testing.T) {
	testCases := []struct {
		name       string
		tunnelType string
		remotePort int
		domain     string
	}{
		{
			name:       "tcp",
			tunnelType: protocol.ProxyTypeTCP,
			remotePort: reserveTCPPort(t),
		},
		{
			name:       "udp",
			tunnelType: protocol.ProxyTypeUDP,
			remotePort: reserveUDPPort(t),
		},
		{
			name:       "http",
			tunnelType: protocol.ProxyTypeHTTP,
			remotePort: 0,
			domain:     "offline-created.example.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, handler, token, cleanup := setupTestServerWithStores(t, true)
			defer cleanup()

			clientID := registerOfflineHTTPTestClient(t, s, "offline-create-"+tc.name)
			body := []byte(fmt.Sprintf(`{"name":"offline-%s","type":"%s","local_ip":"127.0.0.1","local_port":8080,"remote_port":%d,"domain":"%s"}`, tc.name, tc.tunnelType, tc.remotePort, tc.domain))

			resp := doMuxRequest(t, handler, http.MethodPost, fmt.Sprintf("/api/clients/%s/tunnels", clientID), token, body)
			if resp.Code != http.StatusCreated {
				t.Fatalf("Offline %s create expected 201, got %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			var payload map[string]any
			if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
				t.Fatalf("Failed to parse create response: %v", err)
			}
			if success, _ := payload["success"].(bool); !success {
				t.Fatalf("Create response should return success=true, got %v", payload)
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("Offline %s create should have tunnel record in store", tc.tunnelType)
			}
			if stored.DesiredState != protocol.ProxyDesiredStateRunning {
				t.Fatalf("desired_state expected running, got %s", stored.DesiredState)
			}
			if stored.RuntimeState != protocol.ProxyRuntimeStateOffline {
				t.Fatalf("runtime_state expected offline, got %s", stored.RuntimeState)
			}

			switch tc.tunnelType {
			case protocol.ProxyTypeTCP:
				ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", tc.remotePort))
				if err != nil {
					t.Fatalf("Offline TCP create port should not be listened, got %v", err)
				}
				_ = ln.Close()
			case protocol.ProxyTypeUDP:
				conn, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", tc.remotePort))
				if err != nil {
					t.Fatalf("Offline UDP create port should not be listened, got %v", err)
				}
				_ = conn.Close()
			case protocol.ProxyTypeHTTP:
				if err := checkDomainConflict(tc.domain, "", "", s); err == nil {
					t.Fatalf("Offline HTTP create domain %s should be reserved immediately", tc.domain)
				}
			}
		})
	}
}

func TestOfflineManagedTunnel_Update_StoreFirstForTCPAndUDP(t *testing.T) {
	testCases := []struct {
		name       string
		tunnelType string
		oldPort    int
		newPort    int
	}{
		{
			name:       "tcp",
			tunnelType: protocol.ProxyTypeTCP,
			oldPort:    reserveTCPPort(t),
			newPort:    reserveTCPPort(t),
		},
		{
			name:       "udp",
			tunnelType: protocol.ProxyTypeUDP,
			oldPort:    reserveUDPPort(t),
			newPort:    reserveUDPPort(t),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, handler, token, cleanup := setupTestServerWithStores(t, true)
			defer cleanup()

			clientID := registerOfflineHTTPTestClient(t, s, "offline-update-"+tc.name)
			seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
				Name:       "offline-" + tc.name,
				Type:       tc.tunnelType,
				LocalIP:    "127.0.0.1",
				LocalPort:  8080,
				RemotePort: tc.oldPort,
			}, protocol.ProxyStatusActive)

			body := []byte(fmt.Sprintf(`{"local_ip":"192.168.1.50","local_port":9090,"remote_port":%d,"domain":""}`, tc.newPort))
			resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-%s", clientID, tc.name), token, body)
			if resp.Code != http.StatusOK {
				t.Fatalf("Offline %s update expected 200, got %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("Offline %s update should retain record in store", tc.tunnelType)
			}
			if stored.LocalIP != "192.168.1.50" || stored.LocalPort != 9090 || stored.RemotePort != tc.newPort {
				t.Fatalf("Offline %s update fields not written correctly, got %+v", tc.tunnelType, stored)
			}
		})
	}
}

func TestOfflineManagedTunnel_Stop_StoreFirstForTCPAndUDP(t *testing.T) {
	testCases := []struct {
		name       string
		tunnelType string
		remotePort int
	}{
		{name: "tcp", tunnelType: protocol.ProxyTypeTCP, remotePort: reserveTCPPort(t)},
		{name: "udp", tunnelType: protocol.ProxyTypeUDP, remotePort: reserveUDPPort(t)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, handler, token, cleanup := setupTestServerWithStores(t, true)
			defer cleanup()

			clientID := registerOfflineHTTPTestClient(t, s, "offline-stop-"+tc.name)
			seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
				Name:       "offline-" + tc.name,
				Type:       tc.tunnelType,
				LocalIP:    "127.0.0.1",
				LocalPort:  8080,
				RemotePort: tc.remotePort,
			}, protocol.ProxyStatusActive)

			resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-%s/stop", clientID, tc.name), token, []byte(`{}`))
			if resp.Code != http.StatusOK {
				t.Fatalf("offline %s stop expected 200, got %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("offline %s stop should retain record in store", tc.tunnelType)
			}
			if stored.DesiredState != protocol.ProxyDesiredStateStopped || stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
				t.Fatalf("offline %s stop state error, got desired=%s runtime=%s", tc.tunnelType, stored.DesiredState, stored.RuntimeState)
			}
		})
	}
}

func TestOfflineManagedTunnel_Resume_StoreFirstForTCPAndUDP(t *testing.T) {
	testCases := []struct {
		name       string
		tunnelType string
		remotePort int
	}{
		{name: "tcp", tunnelType: protocol.ProxyTypeTCP, remotePort: reserveTCPPort(t)},
		{name: "udp", tunnelType: protocol.ProxyTypeUDP, remotePort: reserveUDPPort(t)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, handler, token, cleanup := setupTestServerWithStores(t, true)
			defer cleanup()

			clientID := registerOfflineHTTPTestClient(t, s, "offline-resume-"+tc.name)
			seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
				Name:       "offline-" + tc.name,
				Type:       tc.tunnelType,
				LocalIP:    "127.0.0.1",
				LocalPort:  8080,
				RemotePort: tc.remotePort,
			}, protocol.ProxyStatusStopped)

			resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-%s/resume", clientID, tc.name), token, []byte(`{}`))
			if resp.Code != http.StatusOK {
				t.Fatalf("Offline %s resume expected 200, got %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("Offline %s resume should retain record in store", tc.tunnelType)
			}
			if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateOffline {
				t.Fatalf("Offline %s resume state error, got desired=%s runtime=%s", tc.tunnelType, stored.DesiredState, stored.RuntimeState)
			}
		})
	}
}

func TestOfflineManagedTunnel_Delete_StoreFirstForTCPAndUDP(t *testing.T) {
	testCases := []struct {
		name       string
		tunnelType string
		remotePort int
	}{
		{name: "tcp", tunnelType: protocol.ProxyTypeTCP, remotePort: reserveTCPPort(t)},
		{name: "udp", tunnelType: protocol.ProxyTypeUDP, remotePort: reserveUDPPort(t)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, handler, token, cleanup := setupTestServerWithStores(t, true)
			defer cleanup()

			clientID := registerOfflineHTTPTestClient(t, s, "offline-delete-"+tc.name)
			seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
				Name:       "offline-" + tc.name,
				Type:       tc.tunnelType,
				LocalIP:    "127.0.0.1",
				LocalPort:  8080,
				RemotePort: tc.remotePort,
			}, protocol.ProxyStatusActive)

			resp := doMuxRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/clients/%s/tunnels/offline-%s", clientID, tc.name), token, nil)
			if resp.Code != http.StatusNoContent {
				t.Fatalf("Offline %s delete expected 204, got %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			if _, exists := s.store.GetTunnel(clientID, "offline-"+tc.name); exists {
				t.Fatalf("Offline %s delete store record should be deleted", tc.tunnelType)
			}
		})
	}
}
