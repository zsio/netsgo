package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func registerOfflineHTTPTestClient(t *testing.T, s *Server, hostname string) string {
	t.Helper()

	record, err := s.auth.adminStore.GetOrCreateClient(
		"install-"+hostname,
		protocol.ClientInfo{
			Hostname: hostname,
			OS:       "linux",
			Arch:     "amd64",
			Version:  "test",
		},
		"127.0.0.1:12345",
	)
	if err != nil {
		t.Fatalf("failed to register offline client: %v", err)
	}
	return record.ID
}

func TestOfflineHTTPTunnel_Update_StoreFirst(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	clientID := registerOfflineHTTPTestClient(t, s, "offline-update")
	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:      "offline-http",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "old.example.com",
	}, protocol.ProxyStatusActive)

	if err := checkDomainConflict("old.example.com", "", "", s); err == nil {
		t.Fatal("before update, old.example.com should be claimed by the existing HTTP tunnel")
	}

	body := []byte(`{"local_ip":"192.168.1.50","local_port":8080,"remote_port":19090,"domain":"new.example.com"}`)
	resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-http", clientID), token, body)
	if resp.Code != http.StatusOK {
		t.Fatalf("offline HTTP update: want 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to parse update response: %v", err)
	}
	if success, _ := payload["success"].(bool); !success {
		t.Fatalf("update response should return success=true, got %v", payload)
	}

	stored, exists := s.store.GetTunnel(clientID, "offline-http")
	if !exists {
		t.Fatal("HTTP tunnel should still exist in the store after update")
	}
	if stored.LocalIP != "192.168.1.50" {
		t.Fatalf("LocalIP after update: want 192.168.1.50, got %s", stored.LocalIP)
	}
	if stored.LocalPort != 8080 {
		t.Fatalf("LocalPort after update: want 8080, got %d", stored.LocalPort)
	}
	if stored.RemotePort != 0 {
		t.Fatalf("RemotePort should be zeroed after HTTP tunnel update, got %d", stored.RemotePort)
	}
	if stored.Domain != "new.example.com" {
		t.Fatalf("Domain after update: want new.example.com, got %s", stored.Domain)
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("offline running HTTP tunnel should remain running/offline after update, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}

	if err := checkDomainConflict("old.example.com", "", "", s); err != nil {
		t.Fatalf("old domain should be released after update, got %v", err)
	}
	if err := checkDomainConflict("new.example.com", "", "", s); err == nil {
		t.Fatal("new domain should be claimed after update")
	}
}

func TestOfflineHTTPTunnel_Pause_StoreFirst(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	clientID := registerOfflineHTTPTestClient(t, s, "offline-pause")
	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:      "offline-http",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "pause.example.com",
	}, protocol.ProxyStatusActive)

	resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-http/pause", clientID), token, []byte(`{}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("offline HTTP pause: want 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	stored, exists := s.store.GetTunnel(clientID, "offline-http")
	if !exists {
		t.Fatal("HTTP tunnel should still exist in the store after pause")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateStopped || stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("store state after pause: want stopped/idle, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Domain != "pause.example.com" {
		t.Fatalf("Domain should be preserved after pause, got %s", stored.Domain)
	}
}

func TestOfflineHTTPTunnel_Delete_StoreFirst(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	clientID := registerOfflineHTTPTestClient(t, s, "offline-delete")
	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:      "offline-http",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "delete.example.com",
	}, protocol.ProxyStatusActive)

	resp := doMuxRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/clients/%s/tunnels/offline-http", clientID), token, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("offline HTTP delete: want 204, got %d, body=%s", resp.Code, resp.Body.String())
	}

	if _, exists := s.store.GetTunnel(clientID, "offline-http"); exists {
		t.Fatal("HTTP tunnel should be removed from the store after delete")
	}
	if err := checkDomainConflict("delete.example.com", "", "", s); err != nil {
		t.Fatalf("domain should be released after delete, got %v", err)
	}
}

func TestOfflineHTTPTunnel_Resume_StoreFirst(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	clientID := registerOfflineHTTPTestClient(t, s, "offline-resume")
	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:      "offline-http",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "resume.example.com",
	}, protocol.ProxyStatusPaused)

	resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-http/resume", clientID), token, []byte(`{}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("offline HTTP resume: want 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	stored, exists := s.store.GetTunnel(clientID, "offline-http")
	if !exists {
		t.Fatal("store record should still exist after offline resume")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning {
		t.Fatalf("desired_state after offline resume: want running, got %s", stored.DesiredState)
	}
	if stored.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("runtime_state after offline resume: want offline, got %s", stored.RuntimeState)
	}
}

func TestOfflineHTTPTunnel_Stop_StoreFirst(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	clientID := registerOfflineHTTPTestClient(t, s, "offline-stop")
	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:      "offline-http",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "stop.example.com",
	}, protocol.ProxyStatusActive)

	resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-http/stop", clientID), token, []byte(`{}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("offline HTTP stop: want 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	stored, exists := s.store.GetTunnel(clientID, "offline-http")
	if !exists {
		t.Fatal("store record should still exist after offline stop")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateStopped {
		t.Fatalf("desired_state after offline stop: want stopped, got %s", stored.DesiredState)
	}
	if stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("runtime_state after offline stop: want idle, got %s", stored.RuntimeState)
	}
}

func TestLifecycle_ClientDisconnect_DoesNotRewriteStoreState(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}
	s.store = store

	wsConn, authResp := connectAndAuth(t, ts, "disconnect-http-store")
	defer wsConn.Close()

	deadline := time.Now().Add(2 * time.Second)
	var liveClient *ClientConn
	for time.Now().Before(deadline) {
		if value, ok := s.clients.Load(authResp.ClientID); ok {
			client := value.(*ClientConn)
			if client.getState() == clientStateLive {
				liveClient = client
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if liveClient == nil {
		t.Fatal("timed out waiting for live client")
	}

	liveClient.proxyMu.Lock()
	liveClient.proxies["active-http"] = &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "active-http",
			Type:         protocol.ProxyTypeHTTP,
			LocalIP:      "127.0.0.1",
			LocalPort:    3000,
			Domain:       "keep-active.example.com",
			ClientID:     authResp.ClientID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		done: make(chan struct{}),
	}
	liveClient.proxyMu.Unlock()

	mustAddStableTunnel(t, s.store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:      "active-http",
			Type:      protocol.ProxyTypeHTTP,
			LocalIP:   "127.0.0.1",
			LocalPort: 3000,
			Domain:    "keep-active.example.com",
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		ClientID:     authResp.ClientID,
		Hostname:     "disconnect-http-store",
	})

	if !s.invalidateLogicalSessionIfCurrent(authResp.ClientID, liveClient.generation, "test_disconnect") {
		t.Fatal("disconnect should successfully invalidate the current logical session")
	}

	stored, exists := s.store.GetTunnel(authResp.ClientID, "active-http")
	if !exists {
		t.Fatal("HTTP tunnel record in the store should remain after disconnect")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("client disconnect should not rewrite the store target state, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Domain != "keep-active.example.com" {
		t.Fatalf("Domain should be preserved after client disconnect, got %s", stored.Domain)
	}
}
