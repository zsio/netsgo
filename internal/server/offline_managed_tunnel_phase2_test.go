package server

import (
	"encoding/json"
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
				t.Fatalf("离线 %s create 期望 201，得到 %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			var payload map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				t.Fatalf("解析 create 响应失败: %v", err)
			}
			if success, _ := payload["success"].(bool); !success {
				t.Fatalf("create 响应应返回 success=true，得到 %v", payload)
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("离线 %s create 后 store 中应存在隧道记录", tc.tunnelType)
			}
			if stored.DesiredState != protocol.ProxyDesiredStateRunning {
				t.Fatalf("desired_state 期望 running，得到 %s", stored.DesiredState)
			}
			if stored.RuntimeState != protocol.ProxyRuntimeStateOffline {
				t.Fatalf("runtime_state 期望 offline，得到 %s", stored.RuntimeState)
			}

			switch tc.tunnelType {
			case protocol.ProxyTypeTCP:
				ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", tc.remotePort))
				if err != nil {
					t.Fatalf("离线 TCP create 后端口不应被监听，得到 %v", err)
				}
				_ = ln.Close()
			case protocol.ProxyTypeUDP:
				conn, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", tc.remotePort))
				if err != nil {
					t.Fatalf("离线 UDP create 后端口不应被监听，得到 %v", err)
				}
				_ = conn.Close()
			case protocol.ProxyTypeHTTP:
				if err := checkDomainConflict(tc.domain, "", "", s); err == nil {
					t.Fatalf("离线 HTTP create 后域名 %s 应立即保留", tc.domain)
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
				t.Fatalf("离线 %s update 期望 200，得到 %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("离线 %s update 后 store 中应保留记录", tc.tunnelType)
			}
			if stored.LocalIP != "192.168.1.50" || stored.LocalPort != 9090 || stored.RemotePort != tc.newPort {
				t.Fatalf("离线 %s update 后字段未正确写入，得到 %+v", tc.tunnelType, stored)
			}
		})
	}
}

func TestOfflineManagedTunnel_Pause_StoreFirstForTCPAndUDP(t *testing.T) {
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

			clientID := registerOfflineHTTPTestClient(t, s, "offline-pause-"+tc.name)
			seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
				Name:       "offline-" + tc.name,
				Type:       tc.tunnelType,
				LocalIP:    "127.0.0.1",
				LocalPort:  8080,
				RemotePort: tc.remotePort,
			}, protocol.ProxyStatusActive)

			resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-%s/pause", clientID, tc.name), token, []byte(`{}`))
			if resp.Code != http.StatusOK {
				t.Fatalf("离线 %s pause 期望 200，得到 %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("离线 %s pause 后 store 中应保留记录", tc.tunnelType)
			}
			if stored.DesiredState != protocol.ProxyDesiredStatePaused || stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
				t.Fatalf("离线 %s pause 后状态错误，得到 desired=%s runtime=%s", tc.tunnelType, stored.DesiredState, stored.RuntimeState)
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
			}, protocol.ProxyStatusPaused)

			resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-%s/resume", clientID, tc.name), token, []byte(`{}`))
			if resp.Code != http.StatusOK {
				t.Fatalf("离线 %s resume 期望 200，得到 %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("离线 %s resume 后 store 中应保留记录", tc.tunnelType)
			}
			if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateOffline {
				t.Fatalf("离线 %s resume 后状态错误，得到 desired=%s runtime=%s", tc.tunnelType, stored.DesiredState, stored.RuntimeState)
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
				t.Fatalf("离线 %s stop 期望 200，得到 %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			stored, exists := s.store.GetTunnel(clientID, "offline-"+tc.name)
			if !exists {
				t.Fatalf("离线 %s stop 后 store 中应保留记录", tc.tunnelType)
			}
			if stored.DesiredState != protocol.ProxyDesiredStateStopped || stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
				t.Fatalf("离线 %s stop 后状态错误，得到 desired=%s runtime=%s", tc.tunnelType, stored.DesiredState, stored.RuntimeState)
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
				t.Fatalf("离线 %s delete 期望 204，得到 %d, body=%s", tc.tunnelType, resp.Code, resp.Body.String())
			}

			if _, exists := s.store.GetTunnel(clientID, "offline-"+tc.name); exists {
				t.Fatalf("离线 %s delete 后 store 记录应被删除", tc.tunnelType)
			}
		})
	}
}
