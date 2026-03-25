package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"netsgo/pkg/protocol"
)

func TestServer_CreateTunnel_TCPWithoutRemotePortReturns409(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsConn, authResp := connectAndAuth(t, ts, "missing-remote-port")
	defer wsConn.Close()

	reqBody := []byte(`{"name":"tcp-missing-port","type":"tcp","local_ip":"127.0.0.1","local_port":8080}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	req.Header.Set("User-Agent", "Go-http-client/1.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create tunnel 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("缺少 remote_port 时期望 409，得到 %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if success, _ := payload["success"].(bool); success {
		t.Fatalf("缺少 remote_port 时不应返回 success=true，得到 %v", payload)
	}
}

func TestServer_UpdateErrorHTTPTunnel_RestartFailureReturnsError(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}
	s.store = store

	wsConn, authResp := connectAndAuth(t, ts, "http-update-restart-fail")
	defer wsConn.Close()

	seedStoredTunnel(t, s, authResp.ClientID, protocol.ProxyNewRequest{
		Name:      "broken-http",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "broken.example.com",
	}, protocol.ProxyStatusError)

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s 不存在", authResp.ClientID)
	}
	client := value.(*ClientConn)
	client.proxyMu.Lock()
	client.proxies["broken-http"] = &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "broken-http",
			Type:         protocol.ProxyTypeHTTP,
			LocalIP:      "127.0.0.1",
			LocalPort:    3000,
			Domain:       "broken.example.com",
			ClientID:     authResp.ClientID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateError,
			Error:        "original failure",
		},
		done: make(chan struct{}),
	}
	client.proxyMu.Unlock()

	client.dataMu.Lock()
	if client.dataSession != nil {
		_ = client.dataSession.Close()
	}
	client.dataSession = nil
	client.dataMu.Unlock()

	reqBody := []byte(`{"local_ip":"127.0.0.1","local_port":8081,"remote_port":0,"domain":"fixed.example.com"}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels/broken-http", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	req.Header.Set("User-Agent", "Go-http-client/1.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update tunnel 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 400 {
		t.Fatalf("自动重启失败时接口必须返回失败，得到 %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if success, _ := payload["success"].(bool); success {
		t.Fatalf("自动重启失败时不应返回 success=true，得到 %v", payload)
	}
	if _, ok := payload["error"].(string); !ok {
		t.Fatalf("自动重启失败时应返回错误信息，得到 %v", payload)
	}

	stored, exists := s.store.GetTunnel(authResp.ClientID, "broken-http")
	if !exists {
		t.Fatal("自动重启失败后 store 记录不应丢失")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("自动重启失败后 store 状态应保持 running/error，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Domain != "fixed.example.com" {
		t.Fatalf("自动重启失败后应保留新配置，得到 %s", stored.Domain)
	}
}
