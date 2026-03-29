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
		t.Fatalf("注册离线 client 失败: %v", err)
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
		t.Fatal("更新前 old.example.com 应被既有 HTTP 隧道声明")
	}

	body := []byte(`{"local_ip":"192.168.1.50","local_port":8080,"remote_port":19090,"domain":"new.example.com"}`)
	resp := doMuxRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/offline-http", clientID), token, body)
	if resp.Code != http.StatusOK {
		t.Fatalf("离线 HTTP update 期望 200，得到 %d, body=%s", resp.Code, resp.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析 update 响应失败: %v", err)
	}
	if success, _ := payload["success"].(bool); !success {
		t.Fatalf("update 响应应返回 success=true，得到 %v", payload)
	}

	stored, exists := s.store.GetTunnel(clientID, "offline-http")
	if !exists {
		t.Fatal("update 后 store 中的 HTTP 隧道不应丢失")
	}
	if stored.LocalIP != "192.168.1.50" {
		t.Fatalf("update 后 LocalIP 期望 192.168.1.50，得到 %s", stored.LocalIP)
	}
	if stored.LocalPort != 8080 {
		t.Fatalf("update 后 LocalPort 期望 8080，得到 %d", stored.LocalPort)
	}
	if stored.RemotePort != 0 {
		t.Fatalf("HTTP 隧道 update 后 RemotePort 应归零，得到 %d", stored.RemotePort)
	}
	if stored.Domain != "new.example.com" {
		t.Fatalf("update 后 Domain 期望 new.example.com，得到 %s", stored.Domain)
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("离线 running HTTP 隧道 update 后应保持 running/offline，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
	}

	if err := checkDomainConflict("old.example.com", "", "", s); err != nil {
		t.Fatalf("update 后旧域名应已释放，得到 %v", err)
	}
	if err := checkDomainConflict("new.example.com", "", "", s); err == nil {
		t.Fatal("update 后新域名应被声明")
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
		t.Fatalf("离线 HTTP pause 期望 200，得到 %d, body=%s", resp.Code, resp.Body.String())
	}

	stored, exists := s.store.GetTunnel(clientID, "offline-http")
	if !exists {
		t.Fatal("pause 后 store 中的 HTTP 隧道不应丢失")
	}
	if stored.DesiredState != protocol.ProxyDesiredStatePaused || stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("pause 后 store 状态期望 paused/idle，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Domain != "pause.example.com" {
		t.Fatalf("pause 后 Domain 应保留，得到 %s", stored.Domain)
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
		t.Fatalf("离线 HTTP delete 期望 204，得到 %d, body=%s", resp.Code, resp.Body.String())
	}

	if _, exists := s.store.GetTunnel(clientID, "offline-http"); exists {
		t.Fatal("delete 后 store 中的 HTTP 隧道应被移除")
	}
	if err := checkDomainConflict("delete.example.com", "", "", s); err != nil {
		t.Fatalf("delete 后域名应已释放，得到 %v", err)
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
		t.Fatalf("离线 HTTP resume 期望 200，得到 %d, body=%s", resp.Code, resp.Body.String())
	}

	stored, exists := s.store.GetTunnel(clientID, "offline-http")
	if !exists {
		t.Fatal("离线 resume 后 store 记录不应丢失")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning {
		t.Fatalf("离线 resume 后 desired_state 期望 running，得到 %s", stored.DesiredState)
	}
	if stored.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("离线 resume 后 runtime_state 期望 offline，得到 %s", stored.RuntimeState)
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
		t.Fatalf("离线 HTTP stop 期望 200，得到 %d, body=%s", resp.Code, resp.Body.String())
	}

	stored, exists := s.store.GetTunnel(clientID, "offline-http")
	if !exists {
		t.Fatal("离线 stop 后 store 记录不应丢失")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateStopped {
		t.Fatalf("离线 stop 后 desired_state 期望 stopped，得到 %s", stored.DesiredState)
	}
	if stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("离线 stop 后 runtime_state 期望 idle，得到 %s", stored.RuntimeState)
	}
}

func TestLifecycle_ClientDisconnect_DoesNotRewriteStoreState(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
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
		t.Fatal("等待 live client 超时")
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
		t.Fatal("断连应成功失效当前逻辑会话")
	}

	stored, exists := s.store.GetTunnel(authResp.ClientID, "active-http")
	if !exists {
		t.Fatal("断连后 store 中的 HTTP 隧道记录不应丢失")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("client 断连不应改写 store 目标状态，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Domain != "keep-active.example.com" {
		t.Fatalf("client 断连后 Domain 应保留，得到 %s", stored.Domain)
	}
}
