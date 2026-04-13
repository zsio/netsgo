package server

import (
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"netsgo/pkg/protocol"
)

func TestServer_CreateTunnel_TCPWithoutRemotePortReturns400(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsConn, authResp := connectAndAuth(t, ts, "missing-remote-port")
	defer mustClose(t, wsConn)

	reqBody := []byte(`{"name":"tcp-missing-port","type":"tcp","local_ip":"127.0.0.1","local_port":8080}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	req.Header.Set("User-Agent", "Go-http-client/1.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create tunnel request failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when remote_port missing, got %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
		t.Fatalf("parse response failed: %v", err)
	}
	if success, _ := payload["success"].(bool); success {
		t.Fatalf("should not return success=true when remote_port missing, got %v", payload)
	}
	if payload["field"] != protocol.TunnelMutationFieldRemotePort {
		t.Fatalf("field expected %q when remote_port missing, got %v", protocol.TunnelMutationFieldRemotePort, payload["field"])
	}
}

func TestServer_UpdateErrorHTTPTunnel_RestartFailureReturnsError(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("create TunnelStore failed: %v", err)
	}
	s.store = store

	wsConn, authResp := connectAndAuth(t, ts, "http-update-restart-fail")
	defer mustClose(t, wsConn)

	seedStoredTunnel(t, s, authResp.ClientID, protocol.ProxyNewRequest{
		Name:      "broken-http",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "broken.example.com",
	}, protocol.ProxyStatusError)

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s does not exist", authResp.ClientID)
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
		t.Fatalf("update tunnel request failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode < 400 {
		t.Fatalf("api must return failure when auto-restart failed, got %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
		t.Fatalf("parse response failed: %v", err)
	}
	if success, _ := payload["success"].(bool); success {
		t.Fatalf("should not return success=true when auto-restart failed, got %v", payload)
	}
	if _, ok := payload["error"].(string); !ok {
		t.Fatalf("should return error message when auto-restart failed, got %v", payload)
	}

	stored, exists := s.store.GetTunnel(authResp.ClientID, "broken-http")
	if !exists {
		t.Fatal("store record should not be lost after auto-restart failure")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("store status should remain running/error after auto-restart failure, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Domain != "fixed.example.com" {
		t.Fatalf("should retain new config after auto-restart failure, got %s", stored.Domain)
	}
}
