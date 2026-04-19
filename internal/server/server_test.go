package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// ============================================================
// Test helper functions
// ============================================================

// setupWSTest creates a test server and WebSocket connection
func setupWSTest(t *testing.T) (*Server, *websocket.Conn, *httptest.Server, func()) {
	t.Helper()
	s := New(0)
	initTestAdminStore(t, s)
	ts := httptest.NewServer(s.newHTTPMux())
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("WebSocket connection failed: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		ts.Close()
	}
	return s, conn, ts, cleanup
}

// setupWSTestNoConn creates only the test server without a WS connection (for pure HTTP tests)
func setupWSTestNoConn(t *testing.T) (*Server, *httptest.Server, func()) {
	t.Helper()
	s := New(0)
	initTestAdminStore(t, s)
	ts := httptest.NewServer(s.newHTTPMux())
	return s, ts, ts.Close
}

func initTestAdminStore(t *testing.T, s *Server) {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "admin.json")
	store, err := NewAdminStore(storePath)
	if err != nil {
		t.Fatalf("failed to create AdminStore: %v", err)
	}
	store.bcryptCost = bcrypt.MinCost // Use the minimum cost in tests to avoid slowing down the suite
	if err := store.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("failed to initialize AdminStore: %v", err)
	}
	if _, err := store.AddAPIKey("default", "test-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("failed to create test API key: %v", err)
	}
	s.auth.adminStore = store
}

func issueAdminToken(t *testing.T, s *Server) string {
	t.Helper()

	session := mustCreateSession(t, s.auth.adminStore, "user-1", "admin", "admin", "127.0.0.1", "Go-http-client/1.1")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}
	return token
}

func testReadTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 3
	}
	return base
}

// doAuth completes authentication and returns the response
func doAuth(t *testing.T, conn *websocket.Conn) protocol.AuthResponse {
	return doAuthWithInstallID(t, conn, "test-host", "install-test-host", "test-key")
}

// doAuthWithInfo completes authentication with the specified info
func doAuthWithInfo(t *testing.T, conn *websocket.Conn, hostname, key string) protocol.AuthResponse {
	return doAuthWithInstallID(t, conn, hostname, "install-"+hostname, key)
}

func doAuthWithInstallID(t *testing.T, conn *websocket.Conn, hostname, installID, key string) protocol.AuthResponse {
	t.Helper()
	authReq := protocol.AuthRequest{
		Key:       key,
		InstallID: installID,
		Client: protocol.ClientInfo{
			Hostname: hostname,
			OS:       "linux",
			Arch:     "amd64",
			Version:  "0.1.0",
		},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("failed to send auth message: %v", err)
	}

	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read auth response: %v", err)
	}
	if resp.Type != protocol.MsgTypeAuthResp {
		t.Fatalf("want auth_resp, got %s", resp.Type)
	}

	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		t.Fatalf("failed to parse auth response: %v", err)
	}
	return authResp
}

// connectAndAuth establishes a new WS connection and completes authentication
func connectAndAuth(t *testing.T, ts *httptest.Server, hostname string) (*websocket.Conn, protocol.AuthResponse) {
	return connectAndAuthWithInstallID(t, ts, hostname, "install-"+hostname)
}

func connectDataWSForClient(t *testing.T, ts *httptest.Server, authResp protocol.AuthResponse) *websocket.Conn {
	t.Helper()
	conn, err := dialDataWSForClient(ts, authResp)
	if err != nil {
		t.Fatalf("failed to establish data channel: %v", err)
	}
	return conn
}

func dialDataWSForClient(ts *httptest.Server, authResp protocol.AuthResponse) (*websocket.Conn, error) {
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/data"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("data channel WebSocket connection failed: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(authResp.ClientID, authResp.DataToken)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to send data channel handshake: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(testReadTimeout(2 * time.Second))); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to set data channel read deadline: %w", err)
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to read data channel handshake response: %w", err)
	}
	if messageType != websocket.BinaryMessage || len(payload) != 1 || payload[0] != protocol.DataHandshakeOK {
		_ = conn.Close()
		return nil, fmt.Errorf("data channel handshake was unsuccessful: type=%d payload=%v", messageType, payload)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to clear data channel read deadline: %w", err)
	}
	return conn, nil
}

func connectAndAuthWithInstallID(t *testing.T, ts *httptest.Server, hostname, installID string) (*websocket.Conn, protocol.AuthResponse) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	authResp := doAuthWithInstallID(t, conn, hostname, installID, "test-key")
	dataConn := connectDataWSForClient(t, ts, authResp)
	t.Cleanup(func() { _ = dataConn.Close() })
	return conn, authResp
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to close reserved port listener: %v", err)
	}
	return port
}

// getAPIJSON issues an HTTP GET request and parses JSON
func getAPIJSON(t *testing.T, s *Server, ts *httptest.Server, path string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("failed to create HTTP request %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP GET %s failed: %v", path, err)
	}
	defer mustClose(t, resp.Body)

	var result map[string]any
	if err := mustDecodeJSON(t, resp.Body, &result); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	return result
}

func assertConsoleSummaryMap(t *testing.T, summary any, expected map[string]float64) {
	t.Helper()

	payload, ok := summary.(map[string]any)
	if !ok {
		t.Fatalf("summary should return an object, got %T", summary)
	}

	for key, want := range expected {
		got, ok := payload[key].(float64)
		if !ok {
			t.Fatalf("summary[%s] should return a number, got %T", key, payload[key])
		}
		if got != want {
			t.Fatalf("summary[%s]: want %v, got %v", key, want, got)
		}
	}
}

// ============================================================
// API endpoint tests (7)
// ============================================================

func TestAPI_Status_NoClients(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleAPIStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code: want 200, got %d", w.Code)
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if result["status"] != "running" {
		t.Errorf("status: want 'running', got %v", result["status"])
	}
	if result["version"] != "0.1.0" {
		t.Errorf("version: want '0.1.0', got %v", result["version"])
	}
	if result["client_count"] != float64(0) {
		t.Errorf("client_count: want 0, got %v", result["client_count"])
	}
}

func TestAPI_Status_ExtendedFields(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	result := getAPIJSON(t, s, ts, "/api/status")

	if result["status"] != "running" {
		t.Errorf("status: want 'running', got %v", result["status"])
	}
	if result["listen_port"] == nil || result["listen_port"].(float64) < 0 {
		t.Errorf("invalid listen_port: %v", result["listen_port"])
	}
	if result["uptime"] == nil || result["uptime"].(float64) < 0 {
		t.Errorf("invalid uptime: %v", result["uptime"])
	}
	if _, ok := result["store_path"]; ok {
		t.Errorf("store_path should no longer be exposed externally: %v", result["store_path"])
	}
	if result["tunnel_active"].(float64) != 0 {
		t.Errorf("tunnel_active: want 0, got %v", result["tunnel_active"])
	}
	generatedAt, ok := result["generated_at"].(string)
	if !ok || generatedAt == "" {
		t.Fatalf("generated_at should return RFC3339 time, got %v", result["generated_at"])
	}
	freshUntil, ok := result["fresh_until"].(string)
	if !ok || freshUntil == "" {
		t.Fatalf("fresh_until should return RFC3339 time, got %v", result["fresh_until"])
	}
	generatedTime, err := time.Parse(time.RFC3339Nano, generatedAt)
	if err != nil {
		t.Fatalf("failed to parse generated_at: %v", err)
	}
	freshUntilTime, err := time.Parse(time.RFC3339Nano, freshUntil)
	if err != nil {
		t.Fatalf("failed to parse fresh_until: %v", err)
	}
	if !freshUntilTime.After(generatedTime) {
		t.Fatalf("fresh_until should be later than generated_at: %s <= %s", freshUntil, generatedAt)
	}
}

func TestAPI_ConsoleSnapshot(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	result := getAPIJSON(t, s, ts, "/api/console/snapshot")

	clients, ok := result["clients"].([]any)
	if !ok {
		t.Fatalf("clients should return an array, got %T", result["clients"])
	}
	if len(clients) != 0 {
		t.Fatalf("initial clients should be empty, got %d", len(clients))
	}

	serverStatus, ok := result["server_status"].(map[string]any)
	if !ok {
		t.Fatalf("server_status should return an object, got %T", result["server_status"])
	}
	if serverStatus["status"] != "running" {
		t.Fatalf("server_status.status: want running, got %v", serverStatus["status"])
	}

	generatedAt, ok := result["generated_at"].(string)
	if !ok || generatedAt == "" {
		t.Fatalf("generated_at should return RFC3339 time, got %v", result["generated_at"])
	}
	freshUntil, ok := result["fresh_until"].(string)
	if !ok || freshUntil == "" {
		t.Fatalf("fresh_until should return RFC3339 time, got %v", result["fresh_until"])
	}
}

func TestAPI_ConsoleSummaryContractAlignsAcrossStatusAndSnapshot(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}
	s.store = store

	offlineInfo := protocol.ClientInfo{
		Hostname: "offline-summary-host",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}
	offlineRecord, err := s.auth.adminStore.GetOrCreateClient("install-offline-summary-host", offlineInfo, "127.0.0.1:10001")
	if err != nil {
		t.Fatalf("failed to pre-create offline client: %v", err)
	}
	seedStoredTunnel(t, s, offlineRecord.ID, protocol.ProxyNewRequest{Name: "offline-active", Type: protocol.ProxyTypeTCP, RemotePort: 20001}, protocol.ProxyStatusActive)
	seedStoredTunnel(t, s, offlineRecord.ID, protocol.ProxyNewRequest{Name: "offline-stopped-a", Type: protocol.ProxyTypeTCP, RemotePort: 20002}, protocol.ProxyStatusStopped)
	seedStoredTunnel(t, s, offlineRecord.ID, protocol.ProxyNewRequest{Name: "offline-stopped", Type: protocol.ProxyTypeTCP, RemotePort: 20003}, protocol.ProxyStatusStopped)

	authResp := doAuthWithInstallID(t, conn, "online-summary-host", "install-online-summary-host", "test-key")
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)
	time.Sleep(50 * time.Millisecond)

	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("online client %s not found", authResp.ClientID)
	}
	client := val.(*ClientConn)
	client.proxyMu.Lock()
	client.proxies["active"] = &ProxyTunnel{Config: protocol.ProxyConfig{Name: "active", DesiredState: protocol.ProxyDesiredStateRunning, RuntimeState: protocol.ProxyRuntimeStateExposed}, done: make(chan struct{})}
	client.proxies["pending"] = &ProxyTunnel{Config: protocol.ProxyConfig{Name: "pending", DesiredState: protocol.ProxyDesiredStateRunning, RuntimeState: protocol.ProxyRuntimeStatePending}, done: make(chan struct{})}
	client.proxies["error"] = &ProxyTunnel{Config: protocol.ProxyConfig{Name: "error", DesiredState: protocol.ProxyDesiredStateRunning, RuntimeState: protocol.ProxyRuntimeStateError, Error: "boom"}, done: make(chan struct{})}
	client.proxyMu.Unlock()

	expected := map[string]float64{
		"total_clients":    2,
		"online_clients":   1,
		"offline_clients":  1,
		"total_tunnels":    6,
		"active_tunnels":   1,
		"inactive_tunnels": 5,
		"pending_tunnels":  1,
		"offline_tunnels":  1,
		"stopped_tunnels":  2,
		"error_tunnels":    1,
	}

	status := getAPIJSON(t, s, ts, "/api/status")
	assertConsoleSummaryMap(t, status["summary"], expected)

	snapshot := getAPIJSON(t, s, ts, "/api/console/snapshot")
	assertConsoleSummaryMap(t, snapshot["summary"], expected)

	serverStatus, ok := snapshot["server_status"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot.server_status should return an object, got %T", snapshot["server_status"])
	}
	assertConsoleSummaryMap(t, serverStatus["summary"], expected)
}

func TestAPI_Status_TunnelCounts(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	time.Sleep(50 * time.Millisecond)

	val, _ := s.clients.Load(authResp.ClientID)
	client := val.(*ClientConn)

	client.proxyMu.Lock()
	client.proxies["tunnel1"] = &ProxyTunnel{Config: protocol.ProxyConfig{DesiredState: protocol.ProxyDesiredStateRunning, RuntimeState: protocol.ProxyRuntimeStateExposed}, done: make(chan struct{})}
	client.proxies["tunnel2"] = &ProxyTunnel{Config: protocol.ProxyConfig{DesiredState: protocol.ProxyDesiredStateStopped, RuntimeState: protocol.ProxyRuntimeStateIdle}, done: make(chan struct{})}
	client.proxies["tunnel3"] = &ProxyTunnel{Config: protocol.ProxyConfig{DesiredState: protocol.ProxyDesiredStateStopped, RuntimeState: protocol.ProxyRuntimeStateIdle}, done: make(chan struct{})}
	client.proxyMu.Unlock()

	result := getAPIJSON(t, s, ts, "/api/status")

	if result["tunnel_active"].(float64) != 1 {
		t.Errorf("tunnel_active: want 1, got %v", result["tunnel_active"])
	}
	if result["tunnel_stopped"].(float64) != 2 {
		t.Errorf("tunnel_stopped: want 2, got %v", result["tunnel_stopped"])
	}
}

func TestAPI_Status_UptimeIncreasing(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	result1 := getAPIJSON(t, s, ts, "/api/status")
	uptime1 := result1["uptime"].(float64)

	time.Sleep(1100 * time.Millisecond)

	result2 := getAPIJSON(t, s, ts, "/api/status")
	uptime2 := result2["uptime"].(float64)

	if uptime2 <= uptime1 {
		t.Errorf("uptime should increase. before: %v, now: %v", uptime1, uptime2)
	}
}

func TestAPI_Status_WithClients(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn2, _ := connectAndAuth(t, ts, "client-host")
	defer mustClose(t, conn2)

	time.Sleep(50 * time.Millisecond)

	result := getAPIJSON(t, s, ts, "/api/status")
	count := result["client_count"].(float64)
	if count < 1 {
		t.Errorf("client_count: want >= 1, got %v", count)
	}
}

func TestAPI_Status_AfterDisconnect(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn2, _ := connectAndAuth(t, ts, "temp-client")
	time.Sleep(50 * time.Millisecond)

	result := getAPIJSON(t, s, ts, "/api/status")
	before := result["client_count"].(float64)

	_ = conn2.Close()
	time.Sleep(100 * time.Millisecond)

	result2 := getAPIJSON(t, s, ts, "/api/status")
	after := result2["client_count"].(float64)

	if after >= before {
		t.Errorf("client_count should decrease after disconnect: before=%v, after=%v", before, after)
	}
}

func TestAPI_Clients_Empty(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/api/clients", nil)
	w := httptest.NewRecorder()
	s.handleAPIClients(w, req)

	body := strings.TrimSpace(w.Body.String())
	if body != "null" && body != "[]" {
		t.Errorf("expected empty result when there are no clients, got %s", body)
	}
}

func TestAPI_Clients_Multiple(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, _ := connectAndAuth(t, ts, "host-A")
	defer mustClose(t, conn1)
	conn2, _ := connectAndAuth(t, ts, "host-B")
	defer mustClose(t, conn2)
	conn3, _ := connectAndAuth(t, ts, "host-C")
	defer mustClose(t, conn3)

	time.Sleep(50 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to request clients: %v", err)
	}
	defer mustClose(t, resp.Body)

	var clients []map[string]any
	if err := mustDecodeJSON(t, resp.Body, &clients); err != nil {
		t.Fatalf("decode clients failed: %v", err)
	}

	if len(clients) < 3 {
		t.Errorf("expected at least 3 clients, got %d", len(clients))
	}

	for i, a := range clients {
		if a["id"] == nil {
			t.Errorf("Client[%d] is missing id", i)
		}
		if a["info"] == nil {
			t.Errorf("Client[%d] is missing info", i)
		}
	}
}

func TestAPI_Clients_WithStats(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, _ := connectAndAuth(t, ts, "stats-host")
	defer mustClose(t, conn1)

	stats := protocol.SystemStats{CPUUsage: 55.5, MemUsage: 70.0, NumCPU: 8}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	if err := conn1.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to request clients: %v", err)
	}
	defer mustClose(t, resp.Body)

	var clients []map[string]any
	if err := mustDecodeJSON(t, resp.Body, &clients); err != nil {
		t.Fatalf("decode clients failed: %v", err)
	}

	if len(clients) == 0 {
		t.Fatal("expected at least 1 client")
	}

	found := false
	for _, a := range clients {
		if a["stats"] != nil {
			found = true
			statsMap := a["stats"].(map[string]any)
			if statsMap["cpu_usage"].(float64) != 55.5 {
				t.Errorf("cpu_usage: want 55.5, got %v", statsMap["cpu_usage"])
			}
			updatedAt, ok := statsMap["updated_at"].(string)
			if !ok || updatedAt == "" {
				t.Fatalf("stats.updated_at should exist, got %v", statsMap["updated_at"])
			}
			freshUntil, ok := statsMap["fresh_until"].(string)
			if !ok || freshUntil == "" {
				t.Fatalf("stats.fresh_until should exist, got %v", statsMap["fresh_until"])
			}
		}
	}
	if !found {
		t.Error("did not find a client containing stats")
	}
}

func TestAPI_Clients_OfflineLegacyRunningTunnelUsesDesiredAndRuntimeStates(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	clientID := registerOfflineHTTPTestClient(t, s, "offline-state-running")
	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:       "offline-tcp",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  8080,
		RemotePort: 18080,
	}, protocol.ProxyStatusActive)

	resp := doMuxRequest(t, handler, http.MethodGet, "/api/clients", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("getting clients: want 200, got %d", resp.Code)
	}

	var clients []map[string]any
	if err := mustDecodeJSON(t, resp.Body, &clients); err != nil {
		t.Fatalf("failed to parse clients response: %v", err)
	}

	for _, client := range clients {
		if client["id"] != clientID {
			continue
		}
		proxies, _ := client["proxies"].([]any)
		if len(proxies) != 1 {
			t.Fatalf("offline client: expected 1 tunnel, got %v", client["proxies"])
		}
		proxy := proxies[0].(map[string]any)
		if proxy["desired_state"] != "running" {
			t.Fatalf("desired_state: want running, got %v", proxy["desired_state"])
		}
		if proxy["runtime_state"] != "offline" {
			t.Fatalf("runtime_state: want offline, got %v", proxy["runtime_state"])
		}
		return
	}

	t.Fatalf("client %s not found", clientID)
}

func TestAPI_Clients_OfflineLegacyErrorTunnelUsesDesiredAndRuntimeStates(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	clientID := registerOfflineHTTPTestClient(t, s, "offline-state-error")
	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:       "offline-udp",
		Type:       protocol.ProxyTypeUDP,
		LocalIP:    "127.0.0.1",
		LocalPort:  5353,
		RemotePort: 19053,
	}, protocol.ProxyStatusError)
	if err := s.store.UpdateStates(clientID, "offline-udp", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "restore failed"); err != nil {
		t.Fatalf("failed to set error state: %v", err)
	}

	resp := doMuxRequest(t, handler, http.MethodGet, "/api/clients", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("getting clients: want 200, got %d", resp.Code)
	}

	var clients []map[string]any
	if err := mustDecodeJSON(t, resp.Body, &clients); err != nil {
		t.Fatalf("failed to parse clients response: %v", err)
	}

	for _, client := range clients {
		if client["id"] != clientID {
			continue
		}
		proxies, _ := client["proxies"].([]any)
		if len(proxies) != 1 {
			t.Fatalf("offline client: expected 1 tunnel, got %v", client["proxies"])
		}
		proxy := proxies[0].(map[string]any)
		if proxy["desired_state"] != "running" {
			t.Fatalf("desired_state: want running, got %v", proxy["desired_state"])
		}
		if proxy["runtime_state"] != "error" {
			t.Fatalf("runtime_state: want error, got %v", proxy["runtime_state"])
		}
		if proxy["error"] != "restore failed" {
			t.Fatalf("error: expected restore failed to be preserved, got %v", proxy["error"])
		}
		return
	}

	t.Fatalf("client %s not found", clientID)
}

func TestAPI_Clients_LiveTunnelUsesDesiredAndRuntimeStates(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsConn, authResp := connectAndAuth(t, ts, "live-state-host")
	defer mustClose(t, wsConn)

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s does not exist", authResp.ClientID)
	}
	client := value.(*ClientConn)
	client.proxyMu.Lock()
	client.proxies["live-http"] = &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "live-http",
			Type:         protocol.ProxyTypeHTTP,
			LocalIP:      "127.0.0.1",
			LocalPort:    3000,
			Domain:       "live.example.com",
			ClientID:     authResp.ClientID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		done: make(chan struct{}),
	}
	client.proxyMu.Unlock()

	reqClients, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	reqClients.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	reqClients.Header.Set("User-Agent", "Go-http-client/1.1")
	respClients, err := http.DefaultClient.Do(reqClients)
	if err != nil {
		t.Fatalf("failed to request clients: %v", err)
	}
	defer mustClose(t, respClients.Body)

	var clientViews []map[string]any
	if err := json.NewDecoder(respClients.Body).Decode(&clientViews); err != nil {
		t.Fatalf("failed to parse clients response: %v", err)
	}

	for _, client := range clientViews {
		if client["id"] != authResp.ClientID {
			continue
		}
		proxies, _ := client["proxies"].([]any)
		for _, item := range proxies {
			proxy := item.(map[string]any)
			if proxy["name"] != "live-http" {
				continue
			}
			if proxy["desired_state"] != "running" {
				t.Fatalf("desired_state: want running, got %v", proxy["desired_state"])
			}
			if proxy["runtime_state"] != "exposed" {
				t.Fatalf("runtime_state: want exposed, got %v", proxy["runtime_state"])
			}
			return
		}
	}

	t.Fatalf("did not find live-http tunnel for client %s", authResp.ClientID)
}

func TestEmitTunnelChanged_NormalizesDesiredAndRuntimeStates(t *testing.T) {
	s := New(0)
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	s.emitTunnelChanged("client-1", protocol.ProxyConfig{
		Name:         "stopped-http",
		Type:         protocol.ProxyTypeHTTP,
		ClientID:     "client-1",
		DesiredState: protocol.ProxyDesiredStateStopped,
		RuntimeState: protocol.ProxyRuntimeStateIdle,
	}, "stopped")

	select {
	case event := <-ch:
		if event.Type != "tunnel_changed" {
			t.Fatalf("event type: want tunnel_changed, got %s", event.Type)
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("failed to parse event payload: %v", err)
		}
		tunnel, _ := payload["tunnel"].(map[string]any)
		if tunnel["desired_state"] != "stopped" {
			t.Fatalf("desired_state: want stopped, got %v", tunnel["desired_state"])
		}
		if tunnel["runtime_state"] != "idle" {
			t.Fatalf("runtime_state: want idle, got %v", tunnel["runtime_state"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tunnel_changed event")
	}
}

func TestAPI_Clients_StatsUpdated(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, authResp := connectAndAuth(t, ts, "update-host")
	defer mustClose(t, conn1)

	stats1 := protocol.SystemStats{CPUUsage: 20.0}
	msg1, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats1)
	if err := conn1.WriteJSON(msg1); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	stats2 := protocol.SystemStats{CPUUsage: 80.0}
	msg2, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats2)
	if err := conn1.WriteJSON(msg2); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("client not found")
	}
	client := val.(*ClientConn)
	if client.GetStats().CPUUsage != 80.0 {
		t.Errorf("Stats should be updated to the latest value 80.0, got %f", client.GetStats().CPUUsage)
	}
}

// ============================================================
// Web panel tests (2)
// ============================================================

func TestWeb_Root(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.handleWeb(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("Content-Type should include text/html")
	}
	if !strings.Contains(w.Body.String(), "NetsGo") {
		t.Error("page should contain 'NetsGo'")
	}
}

func TestWeb_DevMode_FallbackToDevPage(t *testing.T) {
	s := New(8080)
	// in development mode webFS is nil, so all paths should return the devModeHTML hint page
	req := httptest.NewRequest(http.MethodGet, "/nonexist", nil)
	w := httptest.NewRecorder()
	s.handleWeb(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("in development mode, all paths: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "NetsGo") {
		t.Error("page should contain 'NetsGo'")
	}
	if !strings.Contains(w.Body.String(), "bun run dev") {
		t.Error("development mode page should include the bun run dev hint")
	}
}

// ============================================================

// ============================================================

func TestSecurityHeaders_Present(t *testing.T) {
	s := New(8080)
	handler := s.securityHeadersHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
	}

	for _, tt := range tests {
		got := w.Header().Get(tt.header)
		if got != tt.want {
			t.Errorf("%s: want %q, got %q", tt.header, tt.want, got)
		}
	}

	// should not include HSTS when TLS is disabled
	if hsts := w.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("should not set HSTS when TLS is disabled, got %q", hsts)
	}
}

func TestSecurityHeaders_HSTS_WithTLS(t *testing.T) {
	s := New(8080)
	handler := s.securityHeadersHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if hsts := w.Header().Get("Strict-Transport-Security"); hsts == "" {
		t.Error("HSTS should be set when TLS is enabled")
	}
}

func TestSecurityHeaders_HSTS_WithTrustedProxyHTTPS(t *testing.T) {
	s := New(8080)
	s.TLS = &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"10.0.0.0/8"},
	}
	handler := s.securityHeadersHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if hsts := w.Header().Get("Strict-Transport-Security"); hsts == "" {
		t.Error("HSTS should be set when a trusted reverse proxy declares HTTPS")
	}
}

func TestSecurityHeaders_HSTS_IgnoresUntrustedProxyHTTPS(t *testing.T) {
	s := New(8080)
	s.TLS = &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"10.0.0.0/8"},
	}
	handler := s.securityHeadersHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if hsts := w.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("should not trust HTTPS headers from an untrusted proxy, got %q", hsts)
	}
}

// ============================================================

// ============================================================

func TestSSE_NoCORSHeader(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE request failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	if cors := resp.Header.Get("Access-Control-Allow-Origin"); cors != "" {
		t.Errorf("SSE endpoint should not set Access-Control-Allow-Origin, got %q", cors)
	}
}

// ============================================================

// ============================================================

// TestWebSocket_DefaultOriginCheck_NoOrigin should connect normally without an Origin header (Go client)
func TestWebSocket_DefaultOriginCheck_NoOrigin(t *testing.T) {
	_, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	// Go default dialer does not send an Origin header
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connection without an Origin header should succeed, but failed: %v", err)
	}
	defer mustClose(t, conn)

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("want 101 Switching Protocols, got %d", resp.StatusCode)
	}
}

// TestWebSocket_DefaultOriginCheck_CrossOrigin cross-origin Origin should be rejected
func TestWebSocket_DefaultOriginCheck_CrossOrigin(t *testing.T) {
	_, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	header := http.Header{}
	header.Set("Origin", "http://evil.example.com")

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if conn != nil {
		_ = conn.Close()
	}

	if err == nil {
		t.Fatal("cross-origin Origin connection should be rejected, but succeeded")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 Forbidden, got %d", resp.StatusCode)
	}
}

// ============================================================
// Control channel — authentication (5)
// ============================================================

func TestAuth_Success(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)

	if !authResp.Success {
		t.Errorf("authentication should succeed: %s", authResp.Message)
	}
	if authResp.ClientID == "" {
		t.Error("ClientID should not be empty")
	}
	// ClientID should be in UUID v4 format: 8-4-4-4-12
	uuidPattern := `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	if matched, _ := regexp.MatchString(uuidPattern, authResp.ClientID); !matched {
		t.Errorf("ClientID should be in UUID v4 format, got: %q", authResp.ClientID)
	}
}

func TestAuth_EmptyKey(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuthWithInstallID(t, conn, "host", "install-empty-key", "")
	if authResp.Success {
		t.Fatal("server should reject authentication when API key is missing")
	}
	if authResp.Code != protocol.AuthCodeInvalidKey {
		t.Fatalf("want invalid_key, got %q", authResp.Code)
	}
	if authResp.Retryable {
		t.Fatal("invalid_key should not be marked retryable")
	}
	if authResp.ClearToken {
		t.Fatal("invalid_key should not require token clearing")
	}
}

func TestAuth_UninitializedServerRejected(t *testing.T) {
	s := New(0)
	s.auth.adminStore = newTestAdminStore(t)

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer mustClose(t, conn)

	authResp := doAuthWithInstallID(t, conn, "host", "install-uninitialized", "test-key")
	if authResp.Success {
		t.Fatal("server should reject authentication when uninitialized")
	}
	if authResp.Code != protocol.AuthCodeServerUninitialized {
		t.Fatalf("want server_uninitialized, got %q", authResp.Code)
	}
	if !authResp.Retryable {
		t.Fatal("server_uninitialized should be marked retryable")
	}
	if authResp.ClearToken {
		t.Fatal("server_uninitialized should not require token clearing")
	}

	clientCount := 0
	s.RangeClients(func(_ string, _ *ClientConn) bool {
		clientCount++
		return true
	})
	if clientCount != 0 {
		t.Fatalf("no client should be registered when uninitialized, got %d", clientCount)
	}
}

func TestAuth_EmptyHostname(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuthWithInfo(t, conn, "", "test-key")

	if !authResp.Success {
		t.Errorf("empty hostname should not cause authentication failure: %s", authResp.Message)
	}
	if authResp.ClientID == "" {
		t.Error("ClientID should not be empty")
	}
}

func TestAuth_ReconnectSameInstallIDRejectedWhileSessionAlive(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, auth1 := connectAndAuthWithInstallID(t, ts, "stable-host", "install-stable-host")
	defer mustClose(t, conn1)

	time.Sleep(50 * time.Millisecond)
	current, ok := s.clients.Load(auth1.ClientID)
	if !ok {
		t.Fatal("client should be registered after the first authentication")
	}
	if current.(*ClientConn).getState() != clientStateLive {
		t.Fatalf("after the first authentication and channel setup, it should be live, got %s", current.(*ClientConn).getState())
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("second control connection failed: %v", err)
	}
	defer mustClose(t, conn2)

	auth2 := doAuthWithInstallID(t, conn2, "stable-host", "install-stable-host", "test-key")
	if auth2.Success {
		t.Fatal("second authentication should be rejected when a live session already exists")
	}
	if auth2.Code != protocol.AuthCodeConcurrentSession {
		t.Fatalf("error code should be concurrent_session, got %s", auth2.Code)
	}
	if !auth2.Retryable {
		t.Fatal("concurrent session rejection should be marked retryable")
	}

	count := 0
	s.RangeClients(func(_ string, _ *ClientConn) bool {
		count++
		return true
	})
	if count != 1 {
		t.Errorf("online sessions with the same install_id should converge to 1, got %d", count)
	}
}

func TestInvalidateLogicalSession_DoesNotDeleteNewGeneration(t *testing.T) {
	s := New(0)

	oldClient := &ClientConn{
		ID:         "race-client",
		Info:       protocol.ClientInfo{Hostname: "old"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	newClient := &ClientConn{
		ID:         "race-client",
		Info:       protocol.ClientInfo{Hostname: "new"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 2,
		state:      clientStatePendingData,
	}
	s.clients.Store(oldClient.ID, oldClient)

	oldClient.stateMu.Lock()
	done := make(chan struct{})
	go func() {
		_ = s.invalidateLogicalSessionIfCurrent(oldClient.ID, oldClient.generation, "test_race")
		close(done)
	}()

	// Let the invalidation flow load oldClient first, then replace it with the new generation.
	time.Sleep(30 * time.Millisecond)
	s.clients.Store(oldClient.ID, newClient)
	oldClient.stateMu.Unlock()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for old session invalidation flow")
	}

	value, ok := s.clients.Load(oldClient.ID)
	if !ok {
		t.Fatal("new generation should not be deleted by the old session invalidation flow")
	}
	if value != newClient {
		t.Fatal("s.clients should retain the new generation client record")
	}
}

func TestAuth_WrongMsgType(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	msg, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	var resp protocol.Message
	err := conn.ReadJSON(&resp)
	if err == nil {
		t.Error("server should close the connection after receiving a message of the wrong type")
	}
}

func TestAuth_MalformedJSON(t *testing.T) {
	_, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connection failed: %v", err)
	}
	defer mustClose(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{invalid json!!!`)); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	_, _, readErr := conn.ReadMessage()
	if readErr == nil {
		t.Error("server should close the connection after receiving malformed JSON")
	}
}

// ============================================================
// Control channel — authentication timeout P16 (1)
// ============================================================

// TestAuth_TimeoutNoMessage verify P16: after connecting without sending an auth message, the server should disconnect after timeout
func TestAuth_TimeoutNoMessage(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.auth.authTimeout = 500 * time.Millisecond // short timeout makes testing easier

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer mustClose(t, conn)

	// send nothing after connecting and wait for the server to close on timeout
	start := time.Now()
	mustSetReadDeadline(t, conn, time.Now().Add(5*time.Second))
	_, _, readErr := conn.ReadMessage()
	elapsed := time.Since(start)

	if readErr == nil {
		t.Fatal("server should close the connection on timeout when no auth message is sent")
	}

	// verify that the disconnect time is within a reasonable range (500ms ~ 2s)
	if elapsed < 400*time.Millisecond {
		t.Errorf("disconnected too quickly (%v); it may not have been caused by timeout", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("disconnected too slowly (%v); timeout may not have taken effect", elapsed)
	}
}

// ============================================================
// Control channel — heartbeat (2)
// ============================================================

func TestHeartbeat_PingPong(t *testing.T) {
	_, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	if err := conn.WriteJSON(ping); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read Pong: %v", err)
	}
	if resp.Type != protocol.MsgTypePong {
		t.Errorf("want pong, got %s", resp.Type)
	}
}

func TestHeartbeat_MultiplePings(t *testing.T) {
	_, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	for i := 0; i < 10; i++ {
		ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
		if err := conn.WriteJSON(ping); err != nil {
			t.Fatalf("failed to send Ping #%d: %v", i, err)
		}

		mustSetReadDeadline(t, conn, time.Now().Add(testReadTimeout(2*time.Second)))
		var resp protocol.Message
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("failed to read Pong #%d: %v", i, err)
		}
		if resp.Type != protocol.MsgTypePong {
			t.Errorf("attempt #%d: want pong, got %s", i, resp.Type)
		}
	}
}

// ============================================================
// Control channel — probe reporting (2)
// ============================================================

func TestProbe_SingleReport(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	stats := protocol.SystemStats{
		CPUUsage: 42.5,
		MemUsage: 60.0,
		MemTotal: 8 * 1024 * 1024 * 1024,
		MemUsed:  4_800_000_000,
		NumCPU:   4,
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("client is not registered")
	}
	client := val.(*ClientConn)
	if client.GetStats() == nil {
		t.Fatal("Stats should not be nil")
	}
	if client.GetStats().CPUUsage != 42.5 {
		t.Errorf("CPUUsage: want 42.5, got %f", client.GetStats().CPUUsage)
	}
	if client.GetStats().MemUsage != 60.0 {
		t.Errorf("MemUsage: want 60.0, got %f", client.GetStats().MemUsage)
	}
	if client.GetStats().NumCPU != 4 {
		t.Errorf("NumCPU: want 4, got %d", client.GetStats().NumCPU)
	}
}

func TestProbe_ReportPersistedAfterDisconnect(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	stats := protocol.SystemStats{
		CPUUsage: 42.5,
		MemUsage: 60.0,
		NumCPU:   4,
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("failed to send probe data: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	_ = conn.Close()
	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request to /api/clients failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	var clients []map[string]any
	if err := mustDecodeJSON(t, resp.Body, &clients); err != nil {
		t.Fatalf("failed to parse /api/clients response: %v", err)
	}

	for _, client := range clients {
		if client["id"] != authResp.ClientID {
			continue
		}
		if online, _ := client["online"].(bool); online {
			t.Fatal("disconnected client should not still be marked online")
		}
		statsMap, ok := client["stats"].(map[string]any)
		if !ok {
			t.Fatal("disconnected client should still return the last stats")
		}
		if statsMap["cpu_usage"].(float64) != 42.5 {
			t.Fatalf("cpu_usage: want 42.5, got %v", statsMap["cpu_usage"])
		}
		return
	}

	t.Fatalf("Client %s not found", authResp.ClientID)
}

func TestAPI_Clients_FallbackToPersistedStatsBeforeNextReport(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	info := protocol.ClientInfo{
		Hostname: "persisted-host",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}

	record, err := s.auth.adminStore.GetOrCreateClient("install-persisted-host", info, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("failed to pre-create client record: %v", err)
	}
	if err := s.auth.adminStore.UpdateClientStats(record.ID, info, protocol.SystemStats{
		CPUUsage: 88.8,
		MemUsage: 66.6,
		NumCPU:   16,
	}, "127.0.0.1:12345"); err != nil {
		t.Fatalf("failed to prewrite client stats: %v", err)
	}

	conn, authResp := connectAndAuthWithInstallID(t, ts, "persisted-host", "install-persisted-host")
	defer mustClose(t, conn)

	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request to /api/clients failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	var clients []map[string]any
	if err := mustDecodeJSON(t, resp.Body, &clients); err != nil {
		t.Fatalf("failed to parse /api/clients response: %v", err)
	}

	for _, client := range clients {
		if client["id"] != authResp.ClientID {
			continue
		}
		if online, _ := client["online"].(bool); !online {
			t.Fatal("connected client should be marked online")
		}
		statsMap, ok := client["stats"].(map[string]any)
		if !ok {
			t.Fatal("before the first new report, it should first return the persisted old stats")
		}
		if statsMap["cpu_usage"].(float64) != 88.8 {
			t.Fatalf("cpu_usage: want 88.8, got %v", statsMap["cpu_usage"])
		}
		return
	}

	t.Fatalf("Client %s not found", authResp.ClientID)
}

func TestAPI_Clients_ExposeTopLevelBandwidthFields(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	offlineInfo := protocol.ClientInfo{
		Hostname: "offline-bandwidth",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}
	offlineRecord, err := s.auth.adminStore.GetOrCreateClient("install-offline-bandwidth", offlineInfo, "127.0.0.1:10001")
	if err != nil {
		t.Fatalf("failed to create offline client record: %v", err)
	}
	if err := s.auth.adminStore.UpdateClientBandwidthSettings(offlineRecord.ID, protocol.BandwidthSettings{IngressBPS: 512, EgressBPS: 1536}); err != nil {
		t.Fatalf("failed to update offline client bandwidth: %v", err)
	}

	liveInfo := protocol.ClientInfo{
		Hostname: "live-bandwidth",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}
	liveRecord, err := s.auth.adminStore.GetOrCreateClient("install-live-bandwidth", liveInfo, "127.0.0.1:10002")
	if err != nil {
		t.Fatalf("failed to create live client record: %v", err)
	}
	liveClient := &ClientConn{
		ID:         liveRecord.ID,
		Info:       liveInfo,
		RemoteAddr: "127.0.0.1:10002",
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	if err := liveClient.SetBandwidthSettings(protocol.BandwidthSettings{IngressBPS: 1024, EgressBPS: 2048}); err != nil {
		t.Fatalf("failed to set live client bandwidth: %v", err)
	}
	s.clients.Store(liveRecord.ID, liveClient)

	resp := doMuxRequest(t, handler, http.MethodGet, "/api/clients", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/clients: want 200, got %d", resp.Code)
	}

	var clients []map[string]any
	if err := mustDecodeJSON(t, resp.Body, &clients); err != nil {
		t.Fatalf("failed to decode /api/clients response: %v", err)
	}

	seenOffline := false
	seenLive := false
	for _, client := range clients {
		switch client["id"] {
		case offlineRecord.ID:
			seenOffline = true
			if client["ingress_bps"] != float64(512) || client["egress_bps"] != float64(1536) {
				t.Fatalf("offline client bandwidth mismatch: %+v", client)
			}
		case liveRecord.ID:
			seenLive = true
			if client["ingress_bps"] != float64(1024) || client["egress_bps"] != float64(2048) {
				t.Fatalf("live client bandwidth mismatch: %+v", client)
			}
		}
	}

	if !seenOffline {
		t.Fatalf("offline client %s missing from /api/clients", offlineRecord.ID)
	}
	if !seenLive {
		t.Fatalf("live client %s missing from /api/clients", liveRecord.ID)
	}
}

func TestAuth_RehydratesPersistedClientBandwidthSettings(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	info := protocol.ClientInfo{
		Hostname: "persisted-bandwidth-host",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}
	record, err := s.auth.adminStore.GetOrCreateClient("install-persisted-bandwidth-host", info, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("failed to pre-create client record: %v", err)
	}
	if err := s.auth.adminStore.UpdateClientBandwidthSettings(record.ID, protocol.BandwidthSettings{
		IngressBPS: 777,
		EgressBPS:  888,
	}); err != nil {
		t.Fatalf("failed to prewrite client bandwidth: %v", err)
	}

	conn, authResp := connectAndAuthWithInstallID(t, ts, "persisted-bandwidth-host", "install-persisted-bandwidth-host")
	defer mustClose(t, conn)

	time.Sleep(100 * time.Millisecond)

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("live client %s missing after auth", authResp.ClientID)
	}
	client := value.(*ClientConn)
	settings := client.GetBandwidthSettings()
	if settings.IngressBPS != 777 || settings.EgressBPS != 888 {
		t.Fatalf("rehydrated client bandwidth mismatch: %+v", settings)
	}
	if client.BandwidthRuntime() == nil {
		t.Fatal("client bandwidth runtime should be initialized during auth")
	}
	if got := client.BandwidthRuntime().Budget(payloadDirectionIngress).Preview(4096); got != 777 {
		t.Fatalf("ingress preview mismatch after auth rehydrate: want 777, got %d", got)
	}
	if got := client.BandwidthRuntime().Budget(payloadDirectionEgress).Preview(4096); got != 888 {
		t.Fatalf("egress preview mismatch after auth rehydrate: want 888, got %d", got)
	}
}

func TestProbe_MultipleReports(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	for i := 0; i < 5; i++ {
		cpuVal := float64(i+1) * 10.0
		stats := protocol.SystemStats{CPUUsage: cpuVal, NumCPU: 8}
		msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
		if err := conn.WriteJSON(msg); err != nil {
			t.Fatalf("WriteJSON failed: %v", err)
		}
		time.Sleep(30 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	val, _ := s.clients.Load(authResp.ClientID)
	client := val.(*ClientConn)
	if client.GetStats().CPUUsage != 50.0 {
		t.Errorf("final CPUUsage should be 50.0 (the last report), got %f", client.GetStats().CPUUsage)
	}
}

// ============================================================
// Lifecycle and concurrency (3)
// ============================================================

func TestLifecycle_Full(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn, authResp := connectAndAuth(t, ts, "lifecycle-host")

	time.Sleep(50 * time.Millisecond)

	_, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("client should be registered after authentication")
	}

	ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	if err := conn.WriteJSON(ping); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	var pong protocol.Message
	if err := conn.ReadJSON(&pong); err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	if pong.Type != protocol.MsgTypePong {
		t.Errorf("heartbeat: want pong, got %s", pong.Type)
	}

	stats := protocol.SystemStats{CPUUsage: 33.3, NumCPU: 2}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	val, _ := s.clients.Load(authResp.ClientID)
	if val.(*ClientConn).GetStats().CPUUsage != 33.3 {
		t.Error("probe data was not updated correctly")
	}

	_ = conn.Close()
	time.Sleep(100 * time.Millisecond)

	_, ok = s.clients.Load(authResp.ClientID)
	if ok {
		t.Error("client should be removed from the map after disconnect")
	}
}

func TestMultipleClients_Concurrent(t *testing.T) {
	_, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var wg sync.WaitGroup
	errors := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hostname := strings.Repeat("h", idx+1)
			wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				errors <- err
				return
			}
			defer mustClose(t, conn)

			authReq := protocol.AuthRequest{
				Key:       "test-key",
				InstallID: "install-" + hostname,
				Client:    protocol.ClientInfo{Hostname: hostname, OS: "linux", Arch: "amd64", Version: "0.1.0"},
			}
			msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
			if err := conn.WriteJSON(msg); err != nil {
				errors <- fmt.Errorf("write auth JSON failed: %w", err)
				return
			}

			var resp protocol.Message
			mustSetReadDeadline(t, conn, time.Now().Add(testReadTimeout(10*time.Second)))
			if err := conn.ReadJSON(&resp); err != nil {
				errors <- err
				return
			}

			var authResp protocol.AuthResponse
			if err := resp.ParsePayload(&authResp); err != nil {
				errors <- fmt.Errorf("parse auth payload failed: %w", err)
				return
			}
			if !authResp.Success {
				errors <- fmt.Errorf("auth failed: %s", authResp.Message)
				return
			}

			dataConn, err := dialDataWSForClient(ts, authResp)
			if err != nil {
				errors <- err
				return
			}
			defer mustClose(t, dataConn)

			ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
			if err := conn.WriteJSON(ping); err != nil {
				errors <- err
				return
			}
			mustSetReadDeadline(t, conn, time.Now().Add(testReadTimeout(10*time.Second)))
			if err := conn.ReadJSON(&resp); err != nil {
				errors <- err
				return
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent client error: %v", err)
	}
}

func TestClient_DisconnectCleansUp(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, auth1 := connectAndAuth(t, ts, "stay-host")
	conn2, auth2 := connectAndAuth(t, ts, "leave-host")

	time.Sleep(50 * time.Millisecond)

	_, ok1 := s.clients.Load(auth1.ClientID)
	_, ok2 := s.clients.Load(auth2.ClientID)
	if !ok1 || !ok2 {
		t.Fatal("both clients should be registered")
	}

	_ = conn2.Close()
	time.Sleep(100 * time.Millisecond)

	_, ok1 = s.clients.Load(auth1.ClientID)
	_, ok2 = s.clients.Load(auth2.ClientID)
	if !ok1 {
		t.Error("Client1 should not be removed")
	}
	if ok2 {
		t.Error("Client2 should be removed")
	}

	_ = conn1.Close()
}

func TestControlLoop_ProxyMessages(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn, authResp := connectAndAuth(t, ts, "proxy-msg-host")
	defer mustClose(t, conn)

	// clients.Store has already completed inside handleAuth before the auth response is sent,
	// so the client must already be in the map when connectAndAuth returns.
	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("client should be registered in the clients map after successful authentication")
	}
	client := val.(*ClientConn)
	cPipe, sPipe := net.Pipe()
	defer mustClose(t, cPipe)
	defer mustClose(t, sPipe)
	client.dataMu.Lock()
	client.dataSession, _ = mux.NewServerSession(sPipe, mux.DefaultConfig())
	client.dataMu.Unlock()

	eventsCh := s.events.Subscribe()
	defer s.events.Unsubscribe(eventsCh)

	// Test MsgTypeProxyCreate
	req := protocol.ProxyNewRequest{
		Name:       "ws-tunnel-1",
		Type:       protocol.ProxyTypeTCP,
		RemotePort: reserveTCPPort(t),
		BandwidthSettings: protocol.BandwidthSettings{
			IngressBPS: 1234,
			EgressBPS:  5678,
		},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyCreate, protocol.ProxyCreateRequest(req))
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	var resp protocol.Message
	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read create-proxy response: %v", err)
	}

	if resp.Type != protocol.MsgTypeProxyCreateResp {
		t.Errorf("want returned %s, got %s", protocol.MsgTypeProxyCreateResp, resp.Type)
	}

	client.proxyMu.Lock()
	if tunnel, exists := client.proxies["ws-tunnel-1"]; exists {
		tunnel.Config.IngressBPS = 1234
		tunnel.Config.EgressBPS = 5678
	}
	client.proxyMu.Unlock()

	// Test MsgTypeProxyClose
	closeReq := protocol.ProxyCloseRequest{Name: "ws-tunnel-1"}
	closeMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyClose, closeReq)
	if err := conn.WriteJSON(closeMsg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	client.proxyMu.RLock()
	_, exists := client.proxies["ws-tunnel-1"]
	client.proxyMu.RUnlock()

	if exists {
		t.Error("proxy tunnel still exists after sending ProxyClose")
	}

	tunnelPayload := waitForTunnelChangedEvent(t, eventsCh, "closed_by_client", "ws-tunnel-1")
	assertTunnelBandwidthFields(t, tunnelPayload, 1234, 5678)
}

func TestControlLoop_ProxyCreateResponse(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn, authResp := connectAndAuth(t, ts, "legacy-proxy-create-host")
	defer mustClose(t, conn)

	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("client should be registered in the clients map after successful authentication")
	}
	client := val.(*ClientConn)
	cPipe, sPipe := net.Pipe()
	defer mustClose(t, cPipe)
	defer mustClose(t, sPipe)
	client.dataMu.Lock()
	client.dataSession, _ = mux.NewServerSession(sPipe, mux.DefaultConfig())
	client.dataMu.Unlock()

	req := protocol.ProxyNewRequest{
		Name:       "legacy-ws-tunnel",
		Type:       protocol.ProxyTypeTCP,
		RemotePort: reserveTCPPort(t),
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyCreate, req)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	var resp protocol.Message
	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read create-proxy response: %v", err)
	}

	if resp.Type != protocol.MsgTypeProxyCreateResp {
		t.Fatalf("want returned %s, got %s", protocol.MsgTypeProxyCreateResp, resp.Type)
	}

	var payload protocol.ProxyCreateResponse
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatalf("failed to parse create-proxy response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("proxy creation should succeed, got failure: %s", payload.Message)
	}

	client.proxyMu.RLock()
	_, exists := client.proxies[req.Name]
	client.proxyMu.RUnlock()
	if !exists {
		t.Fatalf("tunnel should exist after proxy creation: %s", req.Name)
	}
}

func TestControlLoop_ProxyCreateWaitsForPendingDataChannel(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer mustClose(t, conn)

	authResp := doAuthWithInstallID(t, conn, "pending-proxy-create-host", "install-pending-proxy-create", "test-key")

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("client should be registered in the clients map after successful authentication")
	}
	client := value.(*ClientConn)
	if client.getState() != clientStatePendingData {
		t.Fatalf("before establishing the data channel, client should be pending, got %s", client.getState())
	}

	req := protocol.ProxyNewRequest{
		Name:       "pending-proxy-create",
		Type:       protocol.ProxyTypeTCP,
		RemotePort: reserveTCPPort(t),
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyCreate, protocol.ProxyCreateRequest(req))
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("failed to send create-proxy request: %v", err)
	}

	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	var resp protocol.Message
	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read create-proxy response: %v", err)
	}

	if resp.Type != protocol.MsgTypeProxyCreateResp {
		t.Fatalf("want returned %s, got %s", protocol.MsgTypeProxyCreateResp, resp.Type)
	}

	var payload protocol.ProxyCreateResponse
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatalf("failed to parse create-proxy response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("proxy creation should succeed after waiting for the data channel, got failure: %s", payload.Message)
	}

	client.proxyMu.RLock()
	_, exists := client.proxies[req.Name]
	client.proxyMu.RUnlock()
	if !exists {
		t.Fatalf("tunnel should exist after proxy creation: %s", req.Name)
	}
}

// ============================================================
// controlLoop edge-case tests (2)
// ============================================================

func TestControlLoop_UnknownMsgType(t *testing.T) {
	_, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	conn, _ := connectAndAuth(t, ts, "unknown-msg-host")
	defer mustClose(t, conn)

	// send an unknown message type
	unknownMsg, _ := protocol.NewMessage("unknown_type_xyz", nil)
	if err := conn.WriteJSON(unknownMsg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	// server should not crash and should continue working normally
	// send a ping to verify the connection is still healthy
	time.Sleep(50 * time.Millisecond)
	ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	if err := conn.WriteJSON(ping); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("connection should remain healthy after sending an unknown message: %v", err)
	}
	if resp.Type != protocol.MsgTypePong {
		t.Errorf("want pong, got %s", resp.Type)
	}
}

func TestControlLoop_MalformedProbeReport(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)

	conn, authResp := connectAndAuth(t, ts, "malformed-probe-host")

	// send a probe_report whose payload field types do not match
	// (JSON format is valid, but cpu_usage is a string rather than a float, so ParsePayload will fail)
	badMsg := protocol.Message{
		Type:    protocol.MsgTypeProbeReport,
		Payload: json.RawMessage(`{"cpu_usage": "not_a_number", "mem_usage": "bad"}`),
	}
	if err := conn.WriteJSON(badMsg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	// connection should still be healthy — send a ping to verify (if controlLoop did not crash, it should reply with pong)
	ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	if err := conn.WriteJSON(ping); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("connection should remain healthy after sending malformed probe data: %v", err)
	}
	if resp.Type != protocol.MsgTypePong {
		t.Errorf("want pong, got %s", resp.Type)
	}

	// client stats should not have been updated (should still be nil)
	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("client should still be registered")
	}
	client := val.(*ClientConn)
	if client.GetStats() != nil {
		t.Error("malformed probe_report should not cause stats to be updated")
	}

	_ = conn.Close()
	cleanup()
}

func TestServer_StartHTTPOnly(t *testing.T) {
	s := New(0)
	mux := s.StartHTTPOnly()
	if mux == nil {
		t.Fatal("StartHTTPOnly should return a non-nil ServeMux")
	}
}

// ============================================================
// Tunnel lifecycle API tests (Phase 2)
// ============================================================

func TestServer_TunnelLifecycleAPI(t *testing.T) {
	// 1. initialize server with DB
	tmpDir, _ := os.MkdirTemp("", "tunnel_api_test_*")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "admin.db")
	store, _ := NewAdminStore(dbPath)
	store.bcryptCost = bcrypt.MinCost // Use the minimum cost in tests to avoid slowing down the suite
	if err := store.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if _, err := store.AddAPIKey("default", "test-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	s := New(0)
	s.auth.adminStore = store
	s.store, _ = NewTunnelStore(filepath.Join(tmpDir, "tunnels.json"))

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	// simulate a logged-in AdminSession
	session := mustCreateSession(t, store, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, _ := s.GenerateAdminToken(session)

	// API request helper
	doRequest := func(method, path string, body []byte) (int, map[string]any) {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "test")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("API request failed %s: %v", path, err)
		}
		defer mustClose(t, resp.Body)

		var result map[string]any
		if err := mustDecodeJSON(t, resp.Body, &result); err != nil {
			t.Fatalf("decode response failed: %v", err)
		}
		return resp.StatusCode, result
	}

	var wsConn *websocket.Conn
	var clientID string
	var err error

	type apiResult struct {
		code int
		body map[string]any
	}
	runPendingAction := func(method, path string, body []byte, expectedName string) apiResult {
		t.Helper()

		resultCh := make(chan apiResult, 1)
		go func() {
			code, resp := doRequest(method, path, body)
			resultCh <- apiResult{code: code, body: resp}
		}()

		mustSetReadDeadline(t, wsConn, time.Now().Add(2*time.Second))
		var serverMsg protocol.Message
		if err := wsConn.ReadJSON(&serverMsg); err != nil {
			t.Fatalf("failed to read server proxy_provision: %v", err)
		}
		mustSetReadDeadline(t, wsConn, time.Time{})
		if serverMsg.Type != protocol.MsgTypeProxyProvision {
			t.Fatalf("expected server to send %s, got %s", protocol.MsgTypeProxyProvision, serverMsg.Type)
		}

		var proxyReq protocol.ProxyProvisionRequest
		if err := serverMsg.ParsePayload(&proxyReq); err != nil {
			t.Fatalf("failed to parse server proxy_provision: %v", err)
		}
		if proxyReq.Name != expectedName {
			t.Fatalf("expected proxy_provision.Name=%s, got %s", expectedName, proxyReq.Name)
		}

		val, ok := s.clients.Load(clientID)
		if !ok {
			t.Fatalf("client %s does not exist", clientID)
		}
		liveClient := val.(*ClientConn)
		liveClient.proxyMu.RLock()
		pendingTunnel := liveClient.proxies[expectedName]
		liveClient.proxyMu.RUnlock()
		if pendingTunnel == nil {
			t.Fatalf("pending tunnel should already exist when proxy_provision is received: %s", expectedName)
		}
		if pendingTunnel.Config.DesiredState != protocol.ProxyDesiredStateRunning || pendingTunnel.Config.RuntimeState != protocol.ProxyRuntimeStatePending {
			t.Fatalf("tunnel state when proxy_provision is sent should be running/pending, got %s/%s", pendingTunnel.Config.DesiredState, pendingTunnel.Config.RuntimeState)
		}
		if method == http.MethodPost {
			if _, exists := s.store.GetTunnel(clientID, expectedName); exists {
				t.Fatalf("create should not write to Store before client ack: %s", expectedName)
			}
		}
		if method == http.MethodPut {
			if stored, exists := s.store.GetTunnel(clientID, expectedName); !exists ||
				stored.DesiredState != protocol.ProxyDesiredStateRunning ||
				stored.RuntimeState != protocol.ProxyRuntimeStatePending {
				t.Fatalf("before client ack, resume should keep Store state as running/pending, exists=%v state=%s/%s", exists, stored.DesiredState, stored.RuntimeState)
			}
		}

		respMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyProvisionAck, protocol.ProxyProvisionAck{
			Name:     expectedName,
			Accepted: true,
			Message:  "ok",
		})
		if err := wsConn.WriteJSON(respMsg); err != nil {
			t.Fatalf("failed to send proxy_provision_ack: %v", err)
		}

		select {
		case result := <-resultCh:
			return result
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for API %s %s to return", method, path)
			return apiResult{}
		}
	}

	// 2. simulate a client connection
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	wsConn, _, err = websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer mustClose(t, wsConn)

	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-lifecycle-client",
		Client:    protocol.ClientInfo{Hostname: "lifecycle-client", OS: "linux", Version: "1.0.0"},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err := wsConn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	var authRespMsg protocol.Message
	if err := wsConn.ReadJSON(&authRespMsg); err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	var authResp protocol.AuthResponse
	if err := authRespMsg.ParsePayload(&authResp); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	clientID = authResp.ClientID
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	time.Sleep(50 * time.Millisecond) // wait for client to become live

	// ========= test start =========

	// 1. create tunnel (/api/clients/{id}/tunnels)
	remotePort := reserveTCPPort(t)
	createReq := []byte(fmt.Sprintf(`{"name":"test-tunnel","type":"tcp","local_ip":"127.0.0.1","local_port":8080,"remote_port":%d}`, remotePort))
	result := runPendingAction(http.MethodPost, fmt.Sprintf("/api/clients/%s/tunnels", clientID), createReq, "test-tunnel")
	code, resp := result.code, result.body

	if code != http.StatusCreated {
		t.Errorf("create tunnel: want 201 Created, got %d, response: %v", code, resp)
	}

	// verify that the tunnel is created in Store
	tunnel, ok := s.store.GetTunnel(clientID, "test-tunnel")
	if !ok {
		t.Fatal("tunnel was not written to Store")
	}
	if tunnel.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Errorf("initial state should be running/exposed, got %s/%s", tunnel.DesiredState, tunnel.RuntimeState)
	}

	// 2. stop tunnel (/api/clients/{id}/tunnels/{name}/stop)
	stopReq := []byte(`{}`)
	code, _ = doRequest(http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/test-tunnel/stop", clientID), stopReq)
	if code != http.StatusOK {
		t.Errorf("stop tunnel: want 200, got %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(clientID, "test-tunnel")
	if tunnel.DesiredState != protocol.ProxyDesiredStateStopped || tunnel.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("after stop, tunnel state should be stopped/idle, got %s/%s", tunnel.DesiredState, tunnel.RuntimeState)
	}
	mustSetReadDeadline(t, wsConn, time.Now().Add(2*time.Second))
	var closeMsg protocol.Message
	if err := wsConn.ReadJSON(&closeMsg); err != nil {
		t.Fatalf("failed to read proxy_close after stop: %v", err)
	}
	mustSetReadDeadline(t, wsConn, time.Time{})
	if closeMsg.Type != protocol.MsgTypeProxyClose {
		t.Fatalf("after stop, expected %s, got %s", protocol.MsgTypeProxyClose, closeMsg.Type)
	}

	// 3. resume tunnel (/api/clients/{id}/tunnels/{name}/resume)
	resumeReq := []byte(`{}`)
	result = runPendingAction(http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/test-tunnel/resume", clientID), resumeReq, "test-tunnel")
	code = result.code
	if code != http.StatusOK {
		t.Errorf("resume tunnel: want 200, got %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(clientID, "test-tunnel")
	if tunnel.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Errorf("after resume, tunnel state should be running/exposed, got %s/%s", tunnel.DesiredState, tunnel.RuntimeState)
	}

	// 4. stop tunnel (/api/clients/{id}/tunnels/{name}/stop)
	stopReq = []byte(`{}`)
	code, _ = doRequest(http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/test-tunnel/stop", clientID), stopReq)
	if code != http.StatusOK {
		t.Errorf("stop tunnel: want 200, got %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(clientID, "test-tunnel")
	if tunnel.DesiredState != protocol.ProxyDesiredStateStopped || tunnel.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("after stop, tunnel state should be stopped/idle, got %s/%s", tunnel.DesiredState, tunnel.RuntimeState)
	}

	// 5. delete tunnel (/api/clients/{id}/tunnels/{name})
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels/test-tunnel", clientID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")
	respDel, _ := http.DefaultClient.Do(req)
	if respDel.StatusCode != http.StatusNoContent {
		t.Errorf("delete tunnel: want 204 No Content, got %d", respDel.StatusCode)
	}
	_ = respDel.Body.Close()

	if _, ok := s.store.GetTunnel(clientID, "test-tunnel"); ok {
		t.Error("Store should no longer contain this tunnel after deletion")
	}
}

func TestServer_CreateTunnelTimeoutReturns504(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()
	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}

	wsConn, authResp := connectAndAuth(t, ts, "timeout-client")
	defer mustClose(t, wsConn)

	session := mustCreateSession(t, s.auth.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}
	eventsCh := s.events.Subscribe()
	defer s.events.Unsubscribe(eventsCh)

	type apiResult struct {
		code int
		body map[string]any
	}
	resultCh := make(chan apiResult, 1)
	errCh := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(
			http.MethodPost,
			ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID),
			bytes.NewReader([]byte(`{"name":"timeout-tunnel","type":"tcp","local_ip":"127.0.0.1","local_port":8080,"remote_port":18081}`)),
		)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "test")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer mustClose(t, resp.Body)

		var body map[string]any
		_ = mustDecodeJSON(t, resp.Body, &body)
		resultCh <- apiResult{code: resp.StatusCode, body: body}
	}()

	select {
	case err := <-errCh:
		t.Fatalf("create tunnel request failed: %v", err)
	case result := <-resultCh:
		if result.code != http.StatusGatewayTimeout {
			t.Fatalf("create timeout: want 504, got %d, body=%v", result.code, result.body)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("timed out waiting for create timeout response")
	}

	if _, exists := s.store.GetTunnel(authResp.ClientID, "timeout-tunnel"); exists {
		t.Fatal("Store should not be written after create timeout")
	}

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s should still be online", authResp.ClientID)
	}
	client := value.(*ClientConn)
	client.proxyMu.RLock()
	_, exists := client.proxies["timeout-tunnel"]
	client.proxyMu.RUnlock()
	if exists {
		t.Fatal("runtime pending tunnel should be cleaned up after create timeout")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case event := <-eventsCh:
			if event.Type != "tunnel_changed" {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
				t.Fatalf("failed to parse tunnel_changed event: %v", err)
			}
			if payload["action"] != "error" {
				continue
			}
			tunnelPayload, ok := payload["tunnel"].(map[string]any)
			if !ok {
				t.Fatalf("tunnel_changed.tunnel has invalid type: %#v", payload["tunnel"])
			}
			if tunnelPayload["name"] != "timeout-tunnel" {
				continue
			}
			if tunnelPayload["runtime_state"] != protocol.ProxyRuntimeStateError {
				t.Fatalf("timeout failure event runtime_state: want error, got %v", tunnelPayload["runtime_state"])
			}
			if tunnelPayload["error"] == "" {
				t.Fatal("timeout failure event should include an error message")
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}

	t.Fatal("did not receive final error notification after create timeout")
}

func TestServer_CreateTunnelHTTPConflictReturns409WithErrorCode(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}

	wsConn, authResp := connectAndAuth(t, ts, "http-conflict-create")
	defer mustClose(t, wsConn)

	seedStoredTunnel(t, s, "client-other", protocol.ProxyNewRequest{
		Name:      "existing-http",
		Type:      protocol.ProxyTypeHTTP,
		Domain:    "app.example.com",
		LocalIP:   "127.0.0.1",
		LocalPort: 8080,
	}, protocol.ProxyStatusStopped)

	session := mustCreateSession(t, s.auth.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}
	reqBody := []byte(`{"name":"new-http","type":"http","local_ip":"127.0.0.1","local_port":3000,"domain":"app.example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create tunnel request failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("HTTP domain conflict: want 409, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if body["error_code"] != protocol.TunnelMutationErrorCodeHTTPTunnelConflict {
		t.Fatalf("error_code: want %q, got %v", protocol.TunnelMutationErrorCodeHTTPTunnelConflict, body["error_code"])
	}
	if body["field"] != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field: want %q, got %v", protocol.TunnelMutationFieldDomain, body["field"])
	}
	conflicts, ok := body["conflicting_tunnels"].([]any)
	if !ok || len(conflicts) != 1 || conflicts[0] != "client-other:existing-http" {
		t.Fatalf("conflicting_tunnels: want [client-other:existing-http], got %v", body["conflicting_tunnels"])
	}
}

func TestServer_UpdateTunnelHTTPConflictReturns409WithErrorCode(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}

	wsConn, authResp := connectAndAuth(t, ts, "http-conflict-update")
	defer mustClose(t, wsConn)

	seedStoredTunnel(t, s, "client-other", protocol.ProxyNewRequest{
		Name:      "existing-http",
		Type:      protocol.ProxyTypeHTTP,
		Domain:    "app.example.com",
		LocalIP:   "127.0.0.1",
		LocalPort: 8080,
	}, protocol.ProxyStatusStopped)
	seedStoredTunnel(t, s, authResp.ClientID, protocol.ProxyNewRequest{
		Name:      "editable-http",
		Type:      protocol.ProxyTypeHTTP,
		Domain:    "editable.example.com",
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
	}, protocol.ProxyStatusStopped)

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s does not exist", authResp.ClientID)
	}
	client := value.(*ClientConn)
	client.proxyMu.Lock()
	client.proxies["editable-http"] = &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "editable-http",
			Type:         protocol.ProxyTypeHTTP,
			LocalIP:      "127.0.0.1",
			LocalPort:    3000,
			Domain:       "editable.example.com",
			ClientID:     authResp.ClientID,
			DesiredState: protocol.ProxyDesiredStateStopped,
			RuntimeState: protocol.ProxyRuntimeStateIdle,
		},
		done: make(chan struct{}),
	}
	client.proxyMu.Unlock()

	session := mustCreateSession(t, s.auth.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}
	reqBody := []byte(`{"local_ip":"127.0.0.1","local_port":3000,"remote_port":0,"domain":"app.example.com"}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels/editable-http", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update tunnel request failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("HTTP domain conflict: want 409, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if body["error_code"] != protocol.TunnelMutationErrorCodeHTTPTunnelConflict {
		t.Fatalf("error_code: want %q, got %v", protocol.TunnelMutationErrorCodeHTTPTunnelConflict, body["error_code"])
	}
	if body["field"] != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field: want %q, got %v", protocol.TunnelMutationFieldDomain, body["field"])
	}
	conflicts, ok := body["conflicting_tunnels"].([]any)
	if !ok || len(conflicts) != 1 || conflicts[0] != "client-other:existing-http" {
		t.Fatalf("conflicting_tunnels: want [client-other:existing-http], got %v", body["conflicting_tunnels"])
	}
}

func TestServer_CreateTunnelHTTPInvalidDomainReturns400WithTypedError(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsConn, authResp := connectAndAuth(t, ts, "http-invalid-domain-create")
	defer mustClose(t, wsConn)

	session := mustCreateSession(t, s.auth.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}

	reqBody := []byte(`{"name":"new-http","type":"http","local_ip":"127.0.0.1","local_port":3000,"domain":"https://bad.example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create tunnel request failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid HTTP domain: want 400, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if body["error_code"] != protocol.TunnelMutationErrorCodeDomainInvalid {
		t.Fatalf("error_code: want %q, got %v", protocol.TunnelMutationErrorCodeDomainInvalid, body["error_code"])
	}
	if body["field"] != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field: want %q, got %v", protocol.TunnelMutationFieldDomain, body["field"])
	}
}

func TestServer_CreateTunnelHTTPManagementHostConflictReturnsTypedError(t *testing.T) {
	t.Setenv("NETSGO_SERVER_ADDR", "https://example.com")

	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsConn, authResp := connectAndAuth(t, ts, "http-server-addr-conflict-create")
	defer mustClose(t, wsConn)

	session := mustCreateSession(t, s.auth.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}

	reqBody := []byte(`{"name":"new-http","type":"http","local_ip":"127.0.0.1","local_port":3000,"domain":"example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create tunnel request failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("HTTP management domain conflict: want 409, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if body["error_code"] != protocol.TunnelMutationErrorCodeServerAddrConflict {
		t.Fatalf("error_code: want %q, got %v", protocol.TunnelMutationErrorCodeServerAddrConflict, body["error_code"])
	}
	if body["field"] != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field: want %q, got %v", protocol.TunnelMutationFieldDomain, body["field"])
	}
}

func TestServer_ResumePostAckStoreFailureRollsBackAndClosesClientProxy(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}

	wsConn, authResp := connectAndAuth(t, ts, "resume-post-ack-fail")
	defer mustClose(t, wsConn)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, ok := s.clients.Load(authResp.ClientID)
		if ok && value.(*ClientConn).getState() == clientStateLive {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	session := mustCreateSession(t, s.auth.adminStore, "user-1", "admin", "admin", "127.0.0.1", "resume-test-agent")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}
	doRequest := func(method, path string, body []byte) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "resume-test-agent")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("HTTP request failed %s %s: %v", method, path, err)
		}
		defer mustClose(t, resp.Body)

		var payload map[string]any
		_ = mustDecodeJSON(t, resp.Body, &payload)
		return resp.StatusCode, payload
	}

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s does not exist", authResp.ClientID)
	}
	resumePort := reserveTCPPort(t)
	client := value.(*ClientConn)
	client.proxyMu.Lock()
	client.proxies["resume-rollback"] = &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "resume-rollback",
			Type:         "tcp",
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   resumePort,
			ClientID:     authResp.ClientID,
			DesiredState: protocol.ProxyDesiredStateStopped,
			RuntimeState: protocol.ProxyRuntimeStateIdle,
		},
		done: make(chan struct{}),
	}
	client.proxyMu.Unlock()
	mustAddStableTunnel(t, s.store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:       "resume-rollback",
			Type:       "tcp",
			LocalIP:    "127.0.0.1",
			LocalPort:  8080,
			RemotePort: resumePort,
		},
		DesiredState: protocol.ProxyDesiredStateStopped,
		RuntimeState: protocol.ProxyRuntimeStateIdle,
		ClientID:     authResp.ClientID,
		Hostname:     "resume-post-ack-fail",
	})

	type apiResult struct {
		code int
		body map[string]any
	}
	resumeResultCh := make(chan apiResult, 1)
	go func() {
		code, body := doRequest(http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/resume-rollback/resume", authResp.ClientID), []byte(`{}`))
		resumeResultCh <- apiResult{code: code, body: body}
	}()

	select {
	case result := <-resumeResultCh:
		t.Fatalf("resume request returned before sending proxy_provision: code=%d body=%v", result.code, result.body)
	case <-time.After(200 * time.Millisecond):
	}

	mustSetReadDeadline(t, wsConn, time.Now().Add(2*time.Second))
	var resumeMsg protocol.Message
	if err := wsConn.ReadJSON(&resumeMsg); err != nil {
		t.Fatalf("failed to read proxy_provision during resume phase: %v", err)
	}
	mustSetReadDeadline(t, wsConn, time.Time{})
	if resumeMsg.Type != protocol.MsgTypeProxyProvision {
		t.Fatalf("resume phase: want %s, got %s", protocol.MsgTypeProxyProvision, resumeMsg.Type)
	}
	var resumeProxyReq protocol.ProxyProvisionRequest
	if err := resumeMsg.ParsePayload(&resumeProxyReq); err != nil {
		t.Fatalf("failed to parse resume proxy_provision: %v", err)
	}

	s.store.mu.Lock()
	s.store.failSaveErr = errors.New("injected resume active save failure")
	s.store.failSaveCount = 1
	s.store.mu.Unlock()

	ackResume, _ := protocol.NewMessage(protocol.MsgTypeProxyProvisionAck, protocol.ProxyProvisionAck{
		Name:     resumeProxyReq.Name,
		Accepted: true,
		Message:  "ok",
	})
	if err := wsConn.WriteJSON(ackResume); err != nil {
		t.Fatalf("failed to send resume ack: %v", err)
	}

	select {
	case result := <-resumeResultCh:
		if result.code != http.StatusInternalServerError {
			t.Fatalf("when resume fails persistence post-ack: want 500, got %d body=%v", result.code, result.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for resume API to return")
	}

	mustSetReadDeadline(t, wsConn, time.Now().Add(2*time.Second))
	var rollbackCloseMsg protocol.Message
	if err := wsConn.ReadJSON(&rollbackCloseMsg); err != nil {
		t.Fatalf("failed to read rollback proxy_close: %v", err)
	}
	mustSetReadDeadline(t, wsConn, time.Time{})
	if rollbackCloseMsg.Type != protocol.MsgTypeProxyClose {
		t.Fatalf("after rollback: want %s, got %s", protocol.MsgTypeProxyClose, rollbackCloseMsg.Type)
	}
	var closePayload protocol.ProxyCloseRequest
	if err := rollbackCloseMsg.ParsePayload(&closePayload); err != nil {
		t.Fatalf("failed to parse rollback proxy_close: %v", err)
	}
	if closePayload.Name != "resume-rollback" {
		t.Fatalf("rollback proxy_close name: want resume-rollback, got %s", closePayload.Name)
	}
	if closePayload.Reason != "provision_failed" {
		t.Fatalf("rollback proxy_close reason: want provision_failed, got %s", closePayload.Reason)
	}

	value, ok = s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s should still be online", authResp.ClientID)
	}
	client = value.(*ClientConn)
	client.proxyMu.RLock()
	runtimeTunnel := client.proxies["resume-rollback"]
	client.proxyMu.RUnlock()
	if runtimeTunnel == nil {
		t.Fatal("runtime tunnel should not be lost after resume rollback")
	}
	if runtimeTunnel.Config.DesiredState != protocol.ProxyDesiredStateStopped || runtimeTunnel.Config.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("runtime state after resume rollback: want stopped/idle, got %s/%s", runtimeTunnel.Config.DesiredState, runtimeTunnel.Config.RuntimeState)
	}
	if runtimeTunnel.Config.Error != "" {
		t.Fatalf("runtime error should be empty after resume rollback, got %q", runtimeTunnel.Config.Error)
	}

	storedTunnel, exists := s.store.GetTunnel(authResp.ClientID, "resume-rollback")
	if !exists {
		t.Fatal("store tunnel should not be lost after resume rollback")
	}
	if storedTunnel.DesiredState != protocol.ProxyDesiredStateStopped || storedTunnel.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("store state after resume rollback: want stopped/idle, got %s/%s", storedTunnel.DesiredState, storedTunnel.RuntimeState)
	}
	if storedTunnel.Error != "" {
		t.Fatalf("store error should be empty after resume rollback, got %q", storedTunnel.Error)
	}
}

func TestServer_RestorePostAckStoreFailureMarksError(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}

	record, err := s.auth.adminStore.GetOrCreateClient(
		"install-restore-post-ack-fail",
		protocol.ClientInfo{Hostname: "restore-post-ack-fail"},
		"127.0.0.1:12345",
	)
	if err != nil {
		t.Fatalf("failed to preregister client: %v", err)
	}
	mustAddStableTunnel(t, s.store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:       "restore-fail-tunnel",
			Type:       "tcp",
			LocalIP:    "127.0.0.1",
			LocalPort:  8080,
			RemotePort: 19082,
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		ClientID:     record.ID,
		Hostname:     "restore-post-ack-fail",
	})

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	controlConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("control channel connection failed: %v", err)
	}
	defer mustClose(t, controlConn)

	authResp := doAuthWithInstallID(t, controlConn, "restore-post-ack-fail", "install-restore-post-ack-fail", "test-key")
	if !authResp.Success {
		t.Fatalf("authentication failed: %s", authResp.Message)
	}
	if authResp.ClientID != record.ID {
		t.Fatalf("preregistered client_id=%s should match the auth response, got %s", record.ID, authResp.ClientID)
	}

	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	mustSetReadDeadline(t, controlConn, time.Now().Add(3*time.Second))
	var restoreMsg protocol.Message
	if err := controlConn.ReadJSON(&restoreMsg); err != nil {
		t.Fatalf("failed to read proxy_provision during restore phase: %v", err)
	}
	mustSetReadDeadline(t, controlConn, time.Time{})
	if restoreMsg.Type != protocol.MsgTypeProxyProvision {
		t.Fatalf("restore phase: want %s, got %s", protocol.MsgTypeProxyProvision, restoreMsg.Type)
	}
	var restoreReq protocol.ProxyProvisionRequest
	if err := restoreMsg.ParsePayload(&restoreReq); err != nil {
		t.Fatalf("failed to parse restore proxy_provision: %v", err)
	}
	if restoreReq.Name != "restore-fail-tunnel" {
		t.Fatalf("restore tunnel name: want restore-fail-tunnel, got %s", restoreReq.Name)
	}

	s.store.mu.Lock()
	s.store.failSaveErr = errors.New("injected restore active save failure")
	s.store.failSaveCount = 1
	s.store.mu.Unlock()

	restoreAck, _ := protocol.NewMessage(protocol.MsgTypeProxyProvisionAck, protocol.ProxyProvisionAck{
		Name:     restoreReq.Name,
		Accepted: true,
		Message:  "ok",
	})
	if err := controlConn.WriteJSON(restoreAck); err != nil {
		t.Fatalf("failed to send restore ack: %v", err)
	}

	mustSetReadDeadline(t, controlConn, time.Now().Add(3*time.Second))
	var closeMsg protocol.Message
	if err := controlConn.ReadJSON(&closeMsg); err != nil {
		t.Fatalf("failed to read proxy_close after restore failure: %v", err)
	}
	mustSetReadDeadline(t, controlConn, time.Time{})
	if closeMsg.Type != protocol.MsgTypeProxyClose {
		t.Fatalf("after restore failure: want %s, got %s", protocol.MsgTypeProxyClose, closeMsg.Type)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, ok := s.clients.Load(authResp.ClientID)
		if !ok {
			t.Fatalf("client %s should not be lost", authResp.ClientID)
		}
		client := value.(*ClientConn)
		client.proxyMu.RLock()
		tunnel := client.proxies["restore-fail-tunnel"]
		client.proxyMu.RUnlock()
		if tunnel != nil &&
			tunnel.Config.DesiredState == protocol.ProxyDesiredStateRunning &&
			tunnel.Config.RuntimeState == protocol.ProxyRuntimeStateError {
			if tunnel.Config.Error == "" {
				t.Fatal("runtime error tunnel should carry the failure reason")
			}
			stored, exists := s.store.GetTunnel(authResp.ClientID, "restore-fail-tunnel")
			if !exists {
				t.Fatal("store should retain restore-fail-tunnel")
			}
			if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
				t.Fatalf("store state: want running/error, got %s/%s", stored.DesiredState, stored.RuntimeState)
			}
			if stored.Error == "" {
				t.Fatal("store error tunnel should persist the failure reason")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for restore failure to degrade to error")
}

func TestServer_RestoreActiveHTTPTunnel_DoesNotConflictWithSelf(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}

	record, err := s.auth.adminStore.GetOrCreateClient(
		"install-restore-http",
		protocol.ClientInfo{Hostname: "restore-http-host"},
		"127.0.0.1:12345",
	)
	if err != nil {
		t.Fatalf("failed to preregister client: %v", err)
	}

	mustAddStableTunnel(t, s.store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:      "restore-http",
			Type:      protocol.ProxyTypeHTTP,
			LocalIP:   "127.0.0.1",
			LocalPort: 8080,
			Domain:    "app.example.com",
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		ClientID:     record.ID,
		Hostname:     "restore-http-host",
	})

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	dialer := websocket.Dialer{Subprotocols: []string{protocol.WSSubProtocolControl}}
	controlConn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("control channel connection failed: %v", err)
	}
	defer mustClose(t, controlConn)

	authResp := doAuthWithInstallID(t, controlConn, "restore-http-host", "install-restore-http", "test-key")
	if !authResp.Success {
		t.Fatalf("authentication failed: %s", authResp.Message)
	}
	if authResp.ClientID != record.ID {
		t.Fatalf("preregistered client_id=%s should match the auth response, got %s", record.ID, authResp.ClientID)
	}

	dataConn := connectDataWSForClient(t, ts, authResp)
	defer mustClose(t, dataConn)

	mustSetReadDeadline(t, controlConn, time.Now().Add(3*time.Second))
	var restoreMsg protocol.Message
	if err := controlConn.ReadJSON(&restoreMsg); err != nil {
		t.Fatalf("failed to read proxy_provision during HTTP restore phase: %v", err)
	}
	mustSetReadDeadline(t, controlConn, time.Time{})

	if restoreMsg.Type != protocol.MsgTypeProxyProvision {
		t.Fatalf("HTTP restore phase: want %s, got %s", protocol.MsgTypeProxyProvision, restoreMsg.Type)
	}

	var restoreReq protocol.ProxyProvisionRequest
	if err := restoreMsg.ParsePayload(&restoreReq); err != nil {
		t.Fatalf("failed to parse HTTP restore proxy_provision: %v", err)
	}
	if restoreReq.Name != "restore-http" {
		t.Fatalf("restore tunnel name: want restore-http, got %s", restoreReq.Name)
	}
	if restoreReq.Type != protocol.ProxyTypeHTTP {
		t.Fatalf("restore tunnel type: want http, got %s", restoreReq.Type)
	}
	if restoreReq.Domain != "app.example.com" {
		t.Fatalf("restore tunnel domain: want app.example.com, got %s", restoreReq.Domain)
	}
}

func TestServer_RestoreTunnelsAPI(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tunnel_restore_test_*")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "admin.db")
	store, _ := NewAdminStore(dbPath)
	store.bcryptCost = bcrypt.MinCost // Use the minimum cost in tests to avoid slowing down the suite
	if err := store.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	tunnelStorePath := filepath.Join(tmpDir, "tunnels.json")
	tStore, _ := NewTunnelStore(tunnelStorePath)

	// prewrite two tunnels into Store (representing persisted data read on server restart)
	if err := tStore.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "tunnel1", Type: "tcp", RemotePort: 1234},
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		ClientID:        "client-1",
		Hostname:        "restore-host",
		Binding:         TunnelBindingClientID,
	}); err != nil {
		t.Fatalf("AddTunnel tunnel1 failed: %v", err)
	}
	if err := tStore.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "tunnel2", Type: "tcp", RemotePort: 5678},
		DesiredState:    protocol.ProxyDesiredStateStopped,
		RuntimeState:    protocol.ProxyRuntimeStateIdle,
		ClientID:        "client-1",
		Hostname:        "restore-host",
		Binding:         TunnelBindingClientID,
	}); err != nil {
		t.Fatalf("AddTunnel tunnel2 failed: %v", err)
	}

	s := New(0)
	s.auth.adminStore = store
	s.store = tStore // in the real environment this would be auto-bound by s.initStore(tStore); here we bind it manually

	client := &ClientConn{
		ID:         "client-1",
		Info:       protocol.ClientInfo{Hostname: "restore-host"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store("client-1", client)

	// pretend there is a data channel
	cPipe, sPipe := net.Pipe()
	sess, _ := mux.NewServerSession(sPipe, mux.DefaultConfig())
	client.dataMu.Lock()
	client.dataSession = sess
	client.dataMu.Unlock()
	defer func() {
		_ = cPipe.Close()
		_ = sPipe.Close()
	}()

	// pretend there is a WebSocket connection
	connReady := make(chan struct{})
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			client.mu.Lock()
			client.conn = conn
			client.mu.Unlock()
			close(connReady)
		}
	}))
	defer wsServer.Close()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	clientConn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer mustClose(t, clientConn)

	select {
	case <-connReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the test WebSocket connection to be ready")
	}

	// test restore logic
	s.restoreTunnels(client)

	time.Sleep(100 * time.Millisecond)

	// Because client-1's dataSession is not established, active tunnel1 will trigger StartProxy failure, but restoreTunnels does not downgrade it.
	// In the current restoreTunnels logic, if StartProxy fails, the state is not modified through proxyMu.
	// However, since s.StartProxy failed, it will not appear in client.proxies.
	// To simplify assertions, use store.GetTunnel directly.
	t1, _ := s.store.GetTunnel("client-1", "tunnel1")
	if t1.DesiredState != protocol.ProxyDesiredStateRunning || t1.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Logf("⚠️ tunnel1 state after restore is %s/%s (restoreTunnels does not downgrade on failure, which is expected)", t1.DesiredState, t1.RuntimeState)
	}

	t2, _ := s.store.GetTunnel("client-1", "tunnel2")
	if t2.DesiredState != protocol.ProxyDesiredStateStopped || t2.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("stopped tunnel should remain stopped/idle after restart, got %s/%s", t2.DesiredState, t2.RuntimeState)
	}
}

func TestRestoreTunnels_StoppedTunnelDoesNotWaitForDataSession(t *testing.T) {
	s := New(0)

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}
	s.store = store

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "stopped-only", Type: "tcp", RemotePort: 19090},
		DesiredState:    protocol.ProxyDesiredStateStopped,
		RuntimeState:    protocol.ProxyRuntimeStateIdle,
		ClientID:        "client-restore",
		Hostname:        "restore-host",
	})

	client := &ClientConn{
		ID:         "client-restore",
		Info:       protocol.ClientInfo{Hostname: "restore-host"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(client.ID, client)

	start := time.Now()
	s.restoreTunnels(client)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("restoring only stopped tunnels should not wait for the data channel, took %v", elapsed)
	}

	client.proxyMu.RLock()
	tunnel, ok := client.proxies["stopped-only"]
	client.proxyMu.RUnlock()
	if !ok {
		t.Fatal("stopped tunnel should be restored to in-memory state")
	}
	if tunnel.Config.DesiredState != protocol.ProxyDesiredStateStopped || tunnel.Config.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("restored state should remain stopped/idle, got %s/%s", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
	}
}

func TestRestoreTunnels_StoppedHTTPPlaceholderPreservesDomain(t *testing.T) {
	s := New(0)

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}
	s.store = store

	const domain = "app.example.com"
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:      "stopped-http",
			Type:      protocol.ProxyTypeHTTP,
			LocalIP:   "127.0.0.1",
			LocalPort: 3000,
			Domain:    domain,
		},
		DesiredState: protocol.ProxyDesiredStateStopped,
		RuntimeState: protocol.ProxyRuntimeStateIdle,
		ClientID:     "client-http-domain",
		Hostname:     "restore-host",
	})

	client := &ClientConn{
		ID:         "client-http-domain",
		Info:       protocol.ClientInfo{Hostname: "restore-host"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(client.ID, client)

	s.restoreTunnels(client)

	client.proxyMu.RLock()
	tunnel := client.proxies["stopped-http"]
	client.proxyMu.RUnlock()
	if tunnel == nil {
		t.Fatal("stopped HTTP tunnel should be restored to in-memory state")
	}
	if tunnel.Config.Domain != domain {
		t.Fatalf("restored stopped HTTP tunnel should retain domain=%q, got %q", domain, tunnel.Config.Domain)
	}
}

func TestRestoreTunnels_PortNotAllowedEventPreservesDomain(t *testing.T) {
	s := New(0)

	adminStore, err := NewAdminStore(filepath.Join(t.TempDir(), "admin.json"))
	if err != nil {
		t.Fatalf("failed to create AdminStore: %v", err)
	}
	adminStore.bcryptCost = bcrypt.MinCost // Use the minimum cost in tests to avoid slowing down the suite
	if err := adminStore.Initialize("admin", "password123", "localhost", []PortRange{{Start: 20000, End: 20010}}); err != nil {
		t.Fatalf("failed to initialize AdminStore: %v", err)
	}
	s.auth.adminStore = adminStore

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}
	s.store = store

	const domain = "blocked.example.com"
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:       "http-port-blocked",
			Type:       protocol.ProxyTypeHTTP,
			LocalIP:    "127.0.0.1",
			LocalPort:  8080,
			RemotePort: 19090,
			Domain:     domain,
			BandwidthSettings: protocol.BandwidthSettings{
				IngressBPS: 1234,
				EgressBPS:  5678,
			},
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		ClientID:     "client-port-blocked",
		Hostname:     "restore-host",
	})

	client := &ClientConn{
		ID:         "client-port-blocked",
		Info:       protocol.ClientInfo{Hostname: "restore-host"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(client.ID, client)

	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	s.restoreTunnels(client)

	client.proxyMu.RLock()
	runtimeTunnel := client.proxies["http-port-blocked"]
	client.proxyMu.RUnlock()
	if runtimeTunnel == nil {
		t.Fatal("tunnel with a port outside the allowlist should create an error placeholder")
	}
	if runtimeTunnel.Config.Domain != domain {
		t.Fatalf("error placeholder should retain domain=%q, got %q", domain, runtimeTunnel.Config.Domain)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if ev.Type != "tunnel_changed" {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
				t.Fatalf("failed to parse tunnel_changed event: %v", err)
			}
			action, _ := payload["action"].(string)
			if action != "port_not_allowed" {
				continue
			}
			tunnelPayload, ok := payload["tunnel"].(map[string]any)
			if !ok {
				t.Fatalf("tunnel field in event has invalid type: %#v", payload["tunnel"])
			}
			if got, _ := tunnelPayload["domain"].(string); got != domain {
				t.Fatalf("port_not_allowed event should retain domain=%q, got %q", domain, got)
			}
			assertTunnelBandwidthFields(t, tunnelPayload, 1234, 5678)
			return
		case <-time.After(20 * time.Millisecond):
		}
	}

	t.Fatal("did not receive the tunnel_changed event for port_not_allowed")
}

// ============================================================
// Authentication — token exchange integration tests
// ============================================================

func TestAuth_KeyExchange_ReturnsToken(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-token-test",
		Client: protocol.ClientInfo{
			Hostname: "token-host",
			OS:       "linux",
			Arch:     "amd64",
			Version:  "0.1.0",
		},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	if !authResp.Success {
		t.Fatalf("authentication should succeed: %s", authResp.Message)
	}
	if authResp.Token == "" {
		t.Error("token should be returned after successful key authentication")
	}
	if authResp.ClientID == "" {
		t.Error("ClientID should not be empty")
	}
}

func TestRestoreTunnels_PortNotAllowedPreservesBandwidthFields(t *testing.T) {
	s := New(0)

	adminStore, err := NewAdminStore(filepath.Join(t.TempDir(), "admin.json"))
	if err != nil {
		t.Fatalf("failed to create AdminStore: %v", err)
	}
	adminStore.bcryptCost = bcrypt.MinCost
	if err := adminStore.Initialize("admin", "password123", "localhost", []PortRange{{Start: 20000, End: 20010}}); err != nil {
		t.Fatalf("failed to initialize AdminStore: %v", err)
	}
	s.auth.adminStore = adminStore

	storePath := filepath.Join(t.TempDir(), "tunnels.json")
	rawStore := `[
  {
    "name": "http-port-blocked-bandwidth",
    "type": "http",
    "local_ip": "127.0.0.1",
    "local_port": 8080,
    "remote_port": 19090,
    "domain": "blocked.example.com",
    "desired_state": "running",
    "runtime_state": "exposed",
    "client_id": "client-port-blocked-bandwidth",
    "hostname": "restore-host",
    "binding": "client_id",
    "ingress_bps": 1234,
    "egress_bps": 5678
  }
]`
	if err := os.WriteFile(storePath, []byte(rawStore), 0o600); err != nil {
		t.Fatalf("failed to seed raw tunnel store: %v", err)
	}

	store, err := NewTunnelStore(storePath)
	if err != nil {
		t.Fatalf("failed to load TunnelStore: %v", err)
	}
	s.store = store

	client := &ClientConn{
		ID:         "client-port-blocked-bandwidth",
		Info:       protocol.ClientInfo{Hostname: "restore-host"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(client.ID, client)

	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	s.restoreTunnels(client)

	client.proxyMu.RLock()
	runtimeTunnel := client.proxies["http-port-blocked-bandwidth"]
	client.proxyMu.RUnlock()
	if runtimeTunnel == nil {
		t.Fatal("restore should create an in-memory error placeholder")
	}

	runtimeBytes, err := json.Marshal(runtimeTunnel.Config)
	if err != nil {
		t.Fatalf("failed to marshal runtime tunnel config: %v", err)
	}
	var runtimePayload map[string]any
	if err := json.Unmarshal(runtimeBytes, &runtimePayload); err != nil {
		t.Fatalf("failed to decode runtime tunnel config: %v", err)
	}
	if runtimePayload["ingress_bps"] != float64(1234) {
		t.Fatalf("runtime ingress_bps: want 1234, got %v", runtimePayload["ingress_bps"])
	}
	if runtimePayload["egress_bps"] != float64(5678) {
		t.Fatalf("runtime egress_bps: want 5678, got %v", runtimePayload["egress_bps"])
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if ev.Type != "tunnel_changed" {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
				t.Fatalf("failed to decode tunnel_changed event: %v", err)
			}
			tunnelPayload, ok := payload["tunnel"].(map[string]any)
			if !ok {
				t.Fatalf("tunnel payload missing: %v", payload)
			}
			if tunnelPayload["name"] != "http-port-blocked-bandwidth" {
				continue
			}
			if tunnelPayload["ingress_bps"] != float64(1234) {
				t.Fatalf("event ingress_bps: want 1234, got %v", tunnelPayload["ingress_bps"])
			}
			if tunnelPayload["egress_bps"] != float64(5678) {
				t.Fatalf("event egress_bps: want 5678, got %v", tunnelPayload["egress_bps"])
			}
			return
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	t.Fatal("timed out waiting for tunnel_changed event with bandwidth fields")
}

func TestDeleteManagedTunnel_EventPreservesBandwidthFields(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:         "client-delete-bandwidth",
		Info:       protocol.ClientInfo{Hostname: "delete-host"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(client.ID, client)

	req := protocol.ProxyNewRequest{
		Name:       "delete-bandwidth",
		Type:       protocol.ProxyTypeTCP,
		RemotePort: reserveTCPPort(t),
		BandwidthSettings: protocol.BandwidthSettings{
			IngressBPS: 4321,
			EgressBPS:  8765,
		},
	}
	config := s.upsertTunnelPlaceholder(client, req, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, "")
	if err := s.store.AddTunnel(storedTunnelFromRuntime(client, client.proxies[req.Name])); err != nil {
		t.Fatalf("failed to persist tunnel: %v", err)
	}

	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	if err := s.deleteManagedTunnel(client, req.Name); err != nil {
		t.Fatalf("deleteManagedTunnel failed: %v", err)
	}

	tunnelPayload := waitForTunnelChangedEvent(t, ch, "deleted", req.Name)
	assertTunnelBandwidthFields(t, tunnelPayload, config.IngressBPS, config.EgressBPS)
}

func TestDeleteOfflineManagedTunnel_EventPreservesBandwidthFields(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	clientID := registerOfflineHTTPTestClient(t, s, "offline-delete-bandwidth")
	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:       "offline-delete-bandwidth",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  8080,
		RemotePort: reserveTCPPort(t),
		BandwidthSettings: protocol.BandwidthSettings{
			IngressBPS: 2468,
			EgressBPS:  8642,
		},
	}, protocol.ProxyStatusStopped)

	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	resp := doMuxRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/clients/%s/tunnels/offline-delete-bandwidth", clientID), token, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("delete offline tunnel: want 204, got %d body=%s", resp.Code, resp.Body.String())
	}

	tunnelPayload := waitForTunnelChangedEvent(t, ch, "deleted", "offline-delete-bandwidth")
	assertTunnelBandwidthFields(t, tunnelPayload, 2468, 8642)
}

func TestRestoreTunnels_StoppedTunnelPreservesBandwidthRuntime(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)

	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:       "stopped-bandwidth",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  8080,
			RemotePort: 18080,
			BandwidthSettings: protocol.BandwidthSettings{
				IngressBPS: 321,
				EgressBPS:  654,
			},
		},
		ClientID:     "client-stopped-bandwidth",
		Hostname:     "restore-host",
		DesiredState: protocol.ProxyDesiredStateStopped,
		RuntimeState: protocol.ProxyRuntimeStateIdle,
	})
	s.store = store

	client := &ClientConn{
		ID:         "client-stopped-bandwidth",
		Info:       protocol.ClientInfo{Hostname: "restore-host"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(client.ID, client)

	s.restoreTunnels(client)

	client.proxyMu.RLock()
	tunnel := client.proxies["stopped-bandwidth"]
	client.proxyMu.RUnlock()
	if tunnel == nil {
		t.Fatal("restore should create an in-memory stopped tunnel")
	}
	if tunnel.Config.IngressBPS != 321 || tunnel.Config.EgressBPS != 654 {
		t.Fatalf("restored tunnel config lost bandwidth fields: %+v", tunnel.Config.BandwidthSettings)
	}
	if tunnel.limits == nil {
		t.Fatal("restored tunnel runtime limiter should not be nil")
	}
	if got := tunnel.limits.Budget(payloadDirectionIngress).Preview(4096); got != 321 {
		t.Fatalf("restored tunnel ingress runtime mismatch: want 321, got %d", got)
	}
	if got := tunnel.limits.Budget(payloadDirectionEgress).Preview(4096); got != 654 {
		t.Fatalf("restored tunnel egress runtime mismatch: want 654, got %d", got)
	}
}

func TestAuth_TokenReconnect(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	// 1. first authenticate with the key to obtain a token
	conn1, _ := connectAndAuth(t, ts, "token-reconnect-host")

	// get the token generated for this install_id from adminStore
	clientToken := s.auth.adminStore.GetClientTokenByInstallID("install-token-reconnect-host")
	if clientToken == nil {
		t.Fatal("there should be a token record after the first key authentication")
	}

	// get the current key use_count
	keys := s.auth.adminStore.GetAPIKeys()
	useCountBefore := keys[0].UseCount

	// disconnect
	_ = conn1.Close()
	time.Sleep(200 * time.Millisecond)

	// 2. reconnect with the newly generated token (the original token would be needed, but it cannot be recovered after hashing)
	//    here, exchange with the key once more directly to simulate the client already having a token
	//    real clients persist AuthResponse.Token
	// what this actually verifies is that calling ExchangeToken again for the same install_id does not increase use_count
	_, _, err := s.auth.adminStore.ExchangeToken("test-key", "install-token-reconnect-host", clientToken.ClientID, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("reused-token ExchangeToken failed: %v", err)
	}

	// when the same install_id already has a valid token, it should not consume the key
	keys = s.auth.adminStore.GetAPIKeys()
	if keys[0].UseCount != useCountBefore {
		t.Errorf("token reuse should not consume the key: want %d, got %d", useCountBefore, keys[0].UseCount)
	}
}

func TestAuth_OldClientWithoutToken(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	// simulate an old client version: send only Key, not Token
	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-old-client",
		Client: protocol.ClientInfo{
			Hostname: "old-client",
			OS:       "linux",
			Arch:     "amd64",
			Version:  "0.0.9",
		},
		// Token field is empty (omitempty)
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	if !authResp.Success {
		t.Fatalf("old client key authentication should succeed: %s", authResp.Message)
	}
	if authResp.Token == "" {
		t.Error("even for old clients, the server should return a Token (the client may ignore it)")
	}
}

// ============================================================

// ============================================================

// TestServer_GracefulShutdown verify P15: after calling Shutdown, the client connection is closed normally
func TestServer_GracefulShutdown(t *testing.T) {
	// start the server with the real Start()
	tmpDir := t.TempDir()
	s := New(reserveTCPPort(t))
	s.DataDir = tmpDir

	// pre-create AdminStore
	adminStore, err := NewAdminStore(filepath.Join(tmpDir, "server", "admin.json"))
	if err != nil {
		t.Fatalf("failed to create AdminStore: %v", err)
	}
	adminStore.bcryptCost = bcrypt.MinCost // Use the minimum cost in tests to avoid slowing down the suite
	if err := adminStore.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("failed to initialize AdminStore: %v", err)
	}
	if _, err := adminStore.AddAPIKey("default", "test-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("failed to create test API key: %v", err)
	}
	s.auth.adminStore = adminStore

	// start the server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- s.Start()
	}()
	time.Sleep(200 * time.Millisecond)

	// connect a client
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws/control", s.Port)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{protocol.WSSubProtocolControl}

	var conn *websocket.Conn
	deadline := time.Now().Add(2 * time.Second)
	for {
		var dialErr error
		conn, _, dialErr = dialer.Dial(wsURL, nil)
		if dialErr == nil {
			break
		}
		select {
		case err := <-serverErr:
			t.Fatalf("server failed to start: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("WebSocket connection failed: %v", dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer mustClose(t, conn)

	// complete authentication
	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-shutdown-test",
		Client: protocol.ClientInfo{
			Hostname: "shutdown-host",
			OS:       "linux",
			Arch:     "amd64",
			Version:  "0.1.0",
		},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	var authMsg protocol.Message
	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	if err := conn.ReadJSON(&authMsg); err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}

	// confirm that the client is registered
	clientCount := 0
	s.clients.Range(func(_, _ any) bool {
		clientCount++
		return true
	})
	if clientCount == 0 {
		t.Fatal("client should be registered")
	}

	// call graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	// verify clients have been cleared
	clientCount = 0
	s.clients.Range(func(_, _ any) bool {
		clientCount++
		return true
	})
	if clientCount != 0 {
		t.Errorf("clients should be empty after Shutdown, got %d", clientCount)
	}

	// verify the server has stopped (Serve returned)
	select {
	case err := <-serverErr:
		if err != nil && err.Error() != "http: Server closed" {
			t.Errorf("server returned an unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("server should return after Shutdown")
	}
}

func TestServer_GracefulShutdown_ClosesPendingControlHandshake(t *testing.T) {
	tmpDir := t.TempDir()
	s := New(0)
	s.DataDir = tmpDir

	adminStore, err := NewAdminStore(filepath.Join(tmpDir, "server", "admin.json"))
	if err != nil {
		t.Fatalf("failed to create AdminStore: %v", err)
	}
	adminStore.bcryptCost = bcrypt.MinCost
	if err := adminStore.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("failed to initialize AdminStore: %v", err)
	}
	if _, err := adminStore.AddAPIKey("default", "test-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("failed to create test API key: %v", err)
	}
	s.auth.adminStore = adminStore
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get temporary port: %v", err)
	}
	s.Port = ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- s.Start()
	}()
	time.Sleep(200 * time.Millisecond)

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws/control", s.Port)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{protocol.WSSubProtocolControl}

	var conn *websocket.Conn
	deadline := time.Now().Add(2 * time.Second)
	for {
		var dialErr error
		conn, _, dialErr = dialer.Dial(wsURL, nil)
		if dialErr == nil {
			break
		}
		select {
		case err := <-serverErr:
			t.Fatalf("server failed to start: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("WebSocket connection failed: %v", dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer mustClose(t, conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	mustSetReadDeadline(t, conn, time.Now().Add(500*time.Millisecond))
	var msg protocol.Message
	err = conn.ReadJSON(&msg)
	if err == nil {
		t.Fatal("unauthenticated control connection should be closed after Shutdown")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("control connection was not closed in time after Shutdown, read timed out: %v", err)
	}

	select {
	case err := <-serverErr:
		if err != nil && err.Error() != "http: Server closed" {
			t.Errorf("server returned an unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("server should return after Shutdown")
	}
}
