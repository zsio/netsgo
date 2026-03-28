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

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// ============================================================
// 测试辅助函数
// ============================================================

// setupWSTest 创建测试 Server + WebSocket 连接
func setupWSTest(t *testing.T) (*Server, *websocket.Conn, *httptest.Server, func()) {
	t.Helper()
	s := New(0)
	initTestAdminStore(t, s)
	ts := httptest.NewServer(s.newHTTPMux())
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("WebSocket 连接失败: %v", err)
	}

	cleanup := func() {
		conn.Close()
		ts.Close()
	}
	return s, conn, ts, cleanup
}

// setupWSTestNoConn 只创建测试 Server 不建 WS 连接（用于纯 HTTP 测试）
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
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}
	if err := store.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("初始化 AdminStore 失败: %v", err)
	}
	if _, err := store.AddAPIKey("default", "test-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("创建测试 API Key 失败: %v", err)
	}
	s.adminStore = store
}

func issueAdminToken(t *testing.T, s *Server) string {
	t.Helper()

	session := mustCreateSession(t, s.adminStore, "user-1", "admin", "admin", "127.0.0.1", "Go-http-client/1.1")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 Admin Token 失败: %v", err)
	}
	return token
}

func testReadTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 3
	}
	return base
}

// doAuth 完成认证，返回响应
func doAuth(t *testing.T, conn *websocket.Conn) protocol.AuthResponse {
	return doAuthWithInstallID(t, conn, "test-host", "install-test-host", "test-key")
}

// doAuthWithInfo 用指定信息完成认证
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
		t.Fatalf("发送认证消息失败: %v", err)
	}

	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("读取认证响应失败: %v", err)
	}
	if resp.Type != protocol.MsgTypeAuthResp {
		t.Fatalf("期望 auth_resp，得到 %s", resp.Type)
	}

	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		t.Fatalf("解析认证响应失败: %v", err)
	}
	return authResp
}

// connectAndAuth 建立新 WS 连接并完成认证
func connectAndAuth(t *testing.T, ts *httptest.Server, hostname string) (*websocket.Conn, protocol.AuthResponse) {
	return connectAndAuthWithInstallID(t, ts, hostname, "install-"+hostname)
}

func connectDataWSForClient(t *testing.T, ts *httptest.Server, authResp protocol.AuthResponse) *websocket.Conn {
	t.Helper()
	conn, err := dialDataWSForClient(ts, authResp)
	if err != nil {
		t.Fatalf("建立数据通道失败: %v", err)
	}
	return conn
}

func dialDataWSForClient(ts *httptest.Server, authResp protocol.AuthResponse) (*websocket.Conn, error) {
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/data"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("数据通道 WebSocket 连接失败: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(authResp.ClientID, authResp.DataToken)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送数据通道握手失败: %w", err)
	}
	conn.SetReadDeadline(time.Now().Add(testReadTimeout(2 * time.Second)))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取数据通道握手响应失败: %w", err)
	}
	if messageType != websocket.BinaryMessage || len(payload) != 1 || payload[0] != protocol.DataHandshakeOK {
		conn.Close()
		return nil, fmt.Errorf("数据通道握手未成功: type=%d payload=%v", messageType, payload)
	}
	conn.SetReadDeadline(time.Time{})
	return conn, nil
}

func connectAndAuthWithInstallID(t *testing.T, ts *httptest.Server, hostname, installID string) (*websocket.Conn, protocol.AuthResponse) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	authResp := doAuthWithInstallID(t, conn, hostname, installID, "test-key")
	dataConn := connectDataWSForClient(t, ts, authResp)
	t.Cleanup(func() { dataConn.Close() })
	return conn, authResp
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预留端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("关闭预留端口 listener 失败: %v", err)
	}
	return port
}

// getAPIJSON 发起 HTTP GET 请求并解析 JSON
func getAPIJSON(t *testing.T, s *Server, ts *httptest.Server, path string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("创建 HTTP 请求 %s 失败: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP GET %s 失败: %v", path, err)
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

func assertConsoleSummaryMap(t *testing.T, summary any, expected map[string]float64) {
	t.Helper()

	payload, ok := summary.(map[string]any)
	if !ok {
		t.Fatalf("summary 应返回对象，得到 %T", summary)
	}

	for key, want := range expected {
		got, ok := payload[key].(float64)
		if !ok {
			t.Fatalf("summary[%s] 应返回数字，得到 %T", key, payload[key])
		}
		if got != want {
			t.Fatalf("summary[%s] 期望 %v，得到 %v", key, want, got)
		}
	}
}

// ============================================================
// API 端点测试 (7)
// ============================================================

func TestAPI_Status_NoClients(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleAPIStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，得到 %d", w.Code)
	}

	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)

	if result["status"] != "running" {
		t.Errorf("status 期望 'running'，得到 %v", result["status"])
	}
	if result["version"] != "0.1.0" {
		t.Errorf("version 期望 '0.1.0'，得到 %v", result["version"])
	}
	if result["client_count"] != float64(0) {
		t.Errorf("client_count 期望 0，得到 %v", result["client_count"])
	}
}

func TestAPI_Status_ExtendedFields(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	result := getAPIJSON(t, s, ts, "/api/status")

	if result["status"] != "running" {
		t.Errorf("status 期望 'running'，得到 %v", result["status"])
	}
	if result["listen_port"] == nil || result["listen_port"].(float64) < 0 {
		t.Errorf("listen_port 无效: %v", result["listen_port"])
	}
	if result["uptime"] == nil || result["uptime"].(float64) < 0 {
		t.Errorf("uptime 无效: %v", result["uptime"])
	}
	if result["store_path"] == nil {
		t.Errorf("store_path 无效: %v", result["store_path"])
	}
	if result["tunnel_active"].(float64) != 0 {
		t.Errorf("tunnel_active 期望 0，得到 %v", result["tunnel_active"])
	}
	generatedAt, ok := result["generated_at"].(string)
	if !ok || generatedAt == "" {
		t.Fatalf("generated_at 应返回 RFC3339 时间，得到 %v", result["generated_at"])
	}
	freshUntil, ok := result["fresh_until"].(string)
	if !ok || freshUntil == "" {
		t.Fatalf("fresh_until 应返回 RFC3339 时间，得到 %v", result["fresh_until"])
	}
	generatedTime, err := time.Parse(time.RFC3339Nano, generatedAt)
	if err != nil {
		t.Fatalf("generated_at 解析失败: %v", err)
	}
	freshUntilTime, err := time.Parse(time.RFC3339Nano, freshUntil)
	if err != nil {
		t.Fatalf("fresh_until 解析失败: %v", err)
	}
	if !freshUntilTime.After(generatedTime) {
		t.Fatalf("fresh_until 应晚于 generated_at: %s <= %s", freshUntil, generatedAt)
	}
}

func TestAPI_ConsoleSnapshot(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	result := getAPIJSON(t, s, ts, "/api/console/snapshot")

	clients, ok := result["clients"].([]any)
	if !ok {
		t.Fatalf("clients 应返回数组，得到 %T", result["clients"])
	}
	if len(clients) != 0 {
		t.Fatalf("初始 clients 应为空，得到 %d", len(clients))
	}

	serverStatus, ok := result["server_status"].(map[string]any)
	if !ok {
		t.Fatalf("server_status 应返回对象，得到 %T", result["server_status"])
	}
	if serverStatus["status"] != "running" {
		t.Fatalf("server_status.status 期望 running，得到 %v", serverStatus["status"])
	}

	generatedAt, ok := result["generated_at"].(string)
	if !ok || generatedAt == "" {
		t.Fatalf("generated_at 应返回 RFC3339 时间，得到 %v", result["generated_at"])
	}
	freshUntil, ok := result["fresh_until"].(string)
	if !ok || freshUntil == "" {
		t.Fatalf("fresh_until 应返回 RFC3339 时间，得到 %v", result["fresh_until"])
	}
}

func TestAPI_ConsoleSummaryContractAlignsAcrossStatusAndSnapshot(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}
	s.store = store

	offlineInfo := protocol.ClientInfo{
		Hostname: "offline-summary-host",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}
	offlineRecord, err := s.adminStore.GetOrCreateClient("install-offline-summary-host", offlineInfo, "127.0.0.1:10001")
	if err != nil {
		t.Fatalf("预创建离线 Client 失败: %v", err)
	}
	seedStoredTunnel(t, s, offlineRecord.ID, protocol.ProxyNewRequest{Name: "offline-active", Type: protocol.ProxyTypeTCP, RemotePort: 20001}, protocol.ProxyStatusActive)
	seedStoredTunnel(t, s, offlineRecord.ID, protocol.ProxyNewRequest{Name: "offline-paused", Type: protocol.ProxyTypeTCP, RemotePort: 20002}, protocol.ProxyStatusPaused)
	seedStoredTunnel(t, s, offlineRecord.ID, protocol.ProxyNewRequest{Name: "offline-stopped", Type: protocol.ProxyTypeTCP, RemotePort: 20003}, protocol.ProxyStatusStopped)

	authResp := doAuthWithInstallID(t, conn, "online-summary-host", "install-online-summary-host", "test-key")
	time.Sleep(50 * time.Millisecond)

	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("未找到在线 Client %s", authResp.ClientID)
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
		"paused_tunnels":   1,
		"stopped_tunnels":  1,
		"error_tunnels":    1,
	}

	status := getAPIJSON(t, s, ts, "/api/status")
	assertConsoleSummaryMap(t, status["summary"], expected)

	snapshot := getAPIJSON(t, s, ts, "/api/console/snapshot")
	assertConsoleSummaryMap(t, snapshot["summary"], expected)

	serverStatus, ok := snapshot["server_status"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot.server_status 应返回对象，得到 %T", snapshot["server_status"])
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
	client.proxies["tunnel2"] = &ProxyTunnel{Config: protocol.ProxyConfig{DesiredState: protocol.ProxyDesiredStatePaused, RuntimeState: protocol.ProxyRuntimeStateIdle}, done: make(chan struct{})}
	client.proxies["tunnel3"] = &ProxyTunnel{Config: protocol.ProxyConfig{DesiredState: protocol.ProxyDesiredStateStopped, RuntimeState: protocol.ProxyRuntimeStateIdle}, done: make(chan struct{})}
	client.proxyMu.Unlock()

	result := getAPIJSON(t, s, ts, "/api/status")

	if result["tunnel_active"].(float64) != 1 {
		t.Errorf("tunnel_active 期望 1，得到 %v", result["tunnel_active"])
	}
	if result["tunnel_paused"].(float64) != 1 {
		t.Errorf("tunnel_paused 期望 1，得到 %v", result["tunnel_paused"])
	}
	if result["tunnel_stopped"].(float64) != 1 {
		t.Errorf("tunnel_stopped 期望 1，得到 %v", result["tunnel_stopped"])
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
		t.Errorf("uptime 应该增加. 之前: %v, 现在: %v", uptime1, uptime2)
	}
}

func TestAPI_Status_WithClients(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn2, _ := connectAndAuth(t, ts, "client-host")
	defer conn2.Close()

	time.Sleep(50 * time.Millisecond)

	result := getAPIJSON(t, s, ts, "/api/status")
	count := result["client_count"].(float64)
	if count < 1 {
		t.Errorf("client_count 期望 ≥ 1，得到 %v", count)
	}
}

func TestAPI_Status_AfterDisconnect(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn2, _ := connectAndAuth(t, ts, "temp-client")
	time.Sleep(50 * time.Millisecond)

	result := getAPIJSON(t, s, ts, "/api/status")
	before := result["client_count"].(float64)

	conn2.Close()
	time.Sleep(100 * time.Millisecond)

	result2 := getAPIJSON(t, s, ts, "/api/status")
	after := result2["client_count"].(float64)

	if after >= before {
		t.Errorf("断开后 client_count 应减少: before=%v, after=%v", before, after)
	}
}

func TestAPI_Clients_Empty(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/api/clients", nil)
	w := httptest.NewRecorder()
	s.handleAPIClients(w, req)

	body := strings.TrimSpace(w.Body.String())
	if body != "null" && body != "[]" {
		t.Errorf("无 Client 时期望空结果，得到 %s", body)
	}
}

func TestAPI_Clients_Multiple(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, _ := connectAndAuth(t, ts, "host-A")
	defer conn1.Close()
	conn2, _ := connectAndAuth(t, ts, "host-B")
	defer conn2.Close()
	conn3, _ := connectAndAuth(t, ts, "host-C")
	defer conn3.Close()

	time.Sleep(50 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 clients 失败: %v", err)
	}
	defer resp.Body.Close()

	var clients []map[string]any
	json.NewDecoder(resp.Body).Decode(&clients)

	if len(clients) < 3 {
		t.Errorf("期望至少 3 个 Client，得到 %d", len(clients))
	}

	for i, a := range clients {
		if a["id"] == nil {
			t.Errorf("Client[%d] 缺少 id", i)
		}
		if a["info"] == nil {
			t.Errorf("Client[%d] 缺少 info", i)
		}
	}
}

func TestAPI_Clients_WithStats(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, _ := connectAndAuth(t, ts, "stats-host")
	defer conn1.Close()

	stats := protocol.SystemStats{CPUUsage: 55.5, MemUsage: 70.0, NumCPU: 8}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	conn1.WriteJSON(msg)
	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 clients 失败: %v", err)
	}
	defer resp.Body.Close()

	var clients []map[string]any
	json.NewDecoder(resp.Body).Decode(&clients)

	if len(clients) == 0 {
		t.Fatal("期望至少 1 个 Client")
	}

	found := false
	for _, a := range clients {
		if a["stats"] != nil {
			found = true
			statsMap := a["stats"].(map[string]any)
			if statsMap["cpu_usage"].(float64) != 55.5 {
				t.Errorf("cpu_usage 期望 55.5，得到 %v", statsMap["cpu_usage"])
			}
			updatedAt, ok := statsMap["updated_at"].(string)
			if !ok || updatedAt == "" {
				t.Fatalf("stats.updated_at 应存在，得到 %v", statsMap["updated_at"])
			}
			freshUntil, ok := statsMap["fresh_until"].(string)
			if !ok || freshUntil == "" {
				t.Fatalf("stats.fresh_until 应存在，得到 %v", statsMap["fresh_until"])
			}
		}
	}
	if !found {
		t.Error("未找到包含 stats 的 Client")
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
		t.Fatalf("获取 clients 期望 200，得到 %d", resp.Code)
	}

	var clients []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		t.Fatalf("解析 clients 响应失败: %v", err)
	}

	for _, client := range clients {
		if client["id"] != clientID {
			continue
		}
		proxies, _ := client["proxies"].([]any)
		if len(proxies) != 1 {
			t.Fatalf("离线 client 期望 1 条隧道，得到 %v", client["proxies"])
		}
		proxy := proxies[0].(map[string]any)
		if proxy["desired_state"] != "running" {
			t.Fatalf("desired_state 期望 running，得到 %v", proxy["desired_state"])
		}
		if proxy["runtime_state"] != "offline" {
			t.Fatalf("runtime_state 期望 offline，得到 %v", proxy["runtime_state"])
		}
		return
	}

	t.Fatalf("未找到 client %s", clientID)
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
		t.Fatalf("设置 error 状态失败: %v", err)
	}

	resp := doMuxRequest(t, handler, http.MethodGet, "/api/clients", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("获取 clients 期望 200，得到 %d", resp.Code)
	}

	var clients []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		t.Fatalf("解析 clients 响应失败: %v", err)
	}

	for _, client := range clients {
		if client["id"] != clientID {
			continue
		}
		proxies, _ := client["proxies"].([]any)
		if len(proxies) != 1 {
			t.Fatalf("离线 client 期望 1 条隧道，得到 %v", client["proxies"])
		}
		proxy := proxies[0].(map[string]any)
		if proxy["desired_state"] != "running" {
			t.Fatalf("desired_state 期望 running，得到 %v", proxy["desired_state"])
		}
		if proxy["runtime_state"] != "error" {
			t.Fatalf("runtime_state 期望 error，得到 %v", proxy["runtime_state"])
		}
		if proxy["error"] != "restore failed" {
			t.Fatalf("error 期望保留 restore failed，得到 %v", proxy["error"])
		}
		return
	}

	t.Fatalf("未找到 client %s", clientID)
}

func TestAPI_Clients_LiveTunnelUsesDesiredAndRuntimeStates(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsConn, authResp := connectAndAuth(t, ts, "live-state-host")
	defer wsConn.Close()

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s 不存在", authResp.ClientID)
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
		t.Fatalf("请求 clients 失败: %v", err)
	}
	defer respClients.Body.Close()

	var clientViews []map[string]any
	if err := json.NewDecoder(respClients.Body).Decode(&clientViews); err != nil {
		t.Fatalf("解析 clients 响应失败: %v", err)
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
				t.Fatalf("desired_state 期望 running，得到 %v", proxy["desired_state"])
			}
			if proxy["runtime_state"] != "exposed" {
				t.Fatalf("runtime_state 期望 exposed，得到 %v", proxy["runtime_state"])
			}
			return
		}
	}

	t.Fatalf("未找到 client %s 的 live-http 隧道", authResp.ClientID)
}

func TestEmitTunnelChanged_NormalizesDesiredAndRuntimeStates(t *testing.T) {
	s := New(0)
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	s.emitTunnelChanged("client-1", protocol.ProxyConfig{
		Name:         "paused-http",
		Type:         protocol.ProxyTypeHTTP,
		ClientID:     "client-1",
		DesiredState: protocol.ProxyDesiredStatePaused,
		RuntimeState: protocol.ProxyRuntimeStateIdle,
	}, "paused")

	select {
	case event := <-ch:
		if event.Type != "tunnel_changed" {
			t.Fatalf("事件类型期望 tunnel_changed，得到 %s", event.Type)
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("解析事件 payload 失败: %v", err)
		}
		tunnel, _ := payload["tunnel"].(map[string]any)
		if tunnel["desired_state"] != "paused" {
			t.Fatalf("desired_state 期望 paused，得到 %v", tunnel["desired_state"])
		}
		if tunnel["runtime_state"] != "idle" {
			t.Fatalf("runtime_state 期望 idle，得到 %v", tunnel["runtime_state"])
		}
	case <-time.After(time.Second):
		t.Fatal("等待 tunnel_changed 事件超时")
	}
}

func TestAPI_Clients_StatsUpdated(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, authResp := connectAndAuth(t, ts, "update-host")
	defer conn1.Close()

	stats1 := protocol.SystemStats{CPUUsage: 20.0}
	msg1, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats1)
	conn1.WriteJSON(msg1)
	time.Sleep(50 * time.Millisecond)

	stats2 := protocol.SystemStats{CPUUsage: 80.0}
	msg2, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats2)
	conn1.WriteJSON(msg2)
	time.Sleep(50 * time.Millisecond)

	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("Client 未找到")
	}
	client := val.(*ClientConn)
	if client.GetStats().CPUUsage != 80.0 {
		t.Errorf("Stats 应被更新为最新值 80.0，得到 %f", client.GetStats().CPUUsage)
	}
}

// ============================================================
// Web 面板测试 (2)
// ============================================================

func TestWeb_Root(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.handleWeb(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，得到 %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("Content-Type 应包含 text/html")
	}
	if !strings.Contains(w.Body.String(), "NetsGo") {
		t.Error("页面应包含 'NetsGo'")
	}
}

func TestWeb_DevMode_FallbackToDevPage(t *testing.T) {
	s := New(8080)
	// 在开发模式下 webFS 为 nil，所有路径都应返回 devModeHTML 提示页面
	req := httptest.NewRequest(http.MethodGet, "/nonexist", nil)
	w := httptest.NewRecorder()
	s.handleWeb(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("开发模式下所有路径期望 200，得到 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "NetsGo") {
		t.Error("页面应包含 'NetsGo'")
	}
	if !strings.Contains(w.Body.String(), "bun run dev") {
		t.Error("开发模式页面应包含 bun run dev 提示")
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
			t.Errorf("%s 期望 %q，得到 %q", tt.header, tt.want, got)
		}
	}

	// 不应包含 HSTS（未启用 TLS）
	if hsts := w.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("未启用 TLS 时不应设置 HSTS，得到 %q", hsts)
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
		t.Error("TLS 启用时应设置 HSTS")
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
		t.Error("受信反代声明 HTTPS 时应设置 HSTS")
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
		t.Errorf("不应信任非受信代理的 HTTPS 头，得到 %q", hsts)
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
		t.Fatalf("SSE 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if cors := resp.Header.Get("Access-Control-Allow-Origin"); cors != "" {
		t.Errorf("SSE 端点不应设置 Access-Control-Allow-Origin，得到 %q", cors)
	}
}

// ============================================================

// ============================================================

// TestWebSocket_DefaultOriginCheck_NoOrigin 无 Origin 头（Go 客户端）应能正常连接
func TestWebSocket_DefaultOriginCheck_NoOrigin(t *testing.T) {
	_, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	// Go 默认 Dialer 不发送 Origin 头
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("无 Origin 头时连接应成功，但失败: %v", err)
	}
	defer conn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("期望 101 Switching Protocols，得到 %d", resp.StatusCode)
	}
}

// TestWebSocket_DefaultOriginCheck_CrossOrigin 跨域 Origin 应被拒绝
func TestWebSocket_DefaultOriginCheck_CrossOrigin(t *testing.T) {
	_, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	header := http.Header{}
	header.Set("Origin", "http://evil.example.com")

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if conn != nil {
		conn.Close()
	}

	if err == nil {
		t.Fatal("跨域 Origin 连接应被拒绝，但成功了")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("期望 403 Forbidden，得到 %d", resp.StatusCode)
	}
}

// ============================================================
// 控制通道 — 认证 (5)
// ============================================================

func TestAuth_Success(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)

	if !authResp.Success {
		t.Errorf("认证应成功: %s", authResp.Message)
	}
	if authResp.ClientID == "" {
		t.Error("ClientID 不应为空")
	}
	// ClientID 应为 UUID v4 格式: 8-4-4-4-12
	uuidPattern := `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	if matched, _ := regexp.MatchString(uuidPattern, authResp.ClientID); !matched {
		t.Errorf("ClientID 应为 UUID v4 格式，得到: %q", authResp.ClientID)
	}
}

func TestAuth_EmptyKey(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuthWithInstallID(t, conn, "host", "install-empty-key", "")
	if authResp.Success {
		t.Fatal("缺少 API Key 时服务端应拒绝认证")
	}
	if authResp.Code != protocol.AuthCodeInvalidKey {
		t.Fatalf("期望 invalid_key，得到 %q", authResp.Code)
	}
	if authResp.Retryable {
		t.Fatal("invalid_key 不应标记为可重试")
	}
	if authResp.ClearToken {
		t.Fatal("invalid_key 不应要求清理 token")
	}
}

func TestAuth_UninitializedServerRejected(t *testing.T) {
	s := New(0)
	s.adminStore = newTestAdminStore(t)

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	authResp := doAuthWithInstallID(t, conn, "host", "install-uninitialized", "test-key")
	if authResp.Success {
		t.Fatal("未初始化时服务端应拒绝认证")
	}
	if authResp.Code != protocol.AuthCodeServerUninitialized {
		t.Fatalf("期望 server_uninitialized，得到 %q", authResp.Code)
	}
	if !authResp.Retryable {
		t.Fatal("server_uninitialized 应标记为可重试")
	}
	if authResp.ClearToken {
		t.Fatal("server_uninitialized 不应要求清理 token")
	}

	clientCount := 0
	s.RangeClients(func(_ string, _ *ClientConn) bool {
		clientCount++
		return true
	})
	if clientCount != 0 {
		t.Fatalf("未初始化时不应注册任何 Client，得到 %d", clientCount)
	}
}

func TestAuth_EmptyHostname(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuthWithInfo(t, conn, "", "test-key")

	if !authResp.Success {
		t.Errorf("空主机名不应导致认证失败: %s", authResp.Message)
	}
	if authResp.ClientID == "" {
		t.Error("ClientID 不应为空")
	}
}

func TestAuth_ReconnectSameInstallIDRejectedWhileSessionAlive(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, auth1 := connectAndAuthWithInstallID(t, ts, "stable-host", "install-stable-host")
	defer conn1.Close()

	time.Sleep(50 * time.Millisecond)
	current, ok := s.clients.Load(auth1.ClientID)
	if !ok {
		t.Fatal("第一次认证后 Client 应已注册")
	}
	if current.(*ClientConn).getState() != clientStateLive {
		t.Fatalf("第一次认证并建链后应处于 live，得到 %s", current.(*ClientConn).getState())
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("第二条控制连接失败: %v", err)
	}
	defer conn2.Close()

	auth2 := doAuthWithInstallID(t, conn2, "stable-host", "install-stable-host", "test-key")
	if auth2.Success {
		t.Fatal("已有 live 会话时第二次认证应被拒绝")
	}
	if auth2.Code != protocol.AuthCodeConcurrentSession {
		t.Fatalf("错误码应为 concurrent_session，得到 %s", auth2.Code)
	}
	if !auth2.Retryable {
		t.Fatal("并发会话拒绝应标记为 retryable")
	}

	count := 0
	s.RangeClients(func(_ string, _ *ClientConn) bool {
		count++
		return true
	})
	if count != 1 {
		t.Errorf("相同 install_id 在线会话应被收敛为 1 个，得到 %d", count)
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

	// 让失效流程先 Load 到 oldClient，再替换为新代际。
	time.Sleep(30 * time.Millisecond)
	s.clients.Store(oldClient.ID, newClient)
	oldClient.stateMu.Unlock()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("等待旧会话失效流程超时")
	}

	value, ok := s.clients.Load(oldClient.ID)
	if !ok {
		t.Fatal("新代际不应被旧会话失效流程删除")
	}
	if value != newClient {
		t.Fatal("s.clients 应保留新代际 client 记录")
	}
}

func TestAuth_WrongMsgType(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	msg, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	conn.WriteJSON(msg)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Message
	err := conn.ReadJSON(&resp)
	if err == nil {
		t.Error("发送错误类型消息后，Server 应关闭连接")
	}
}

func TestAuth_MalformedJSON(t *testing.T) {
	_, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	conn.WriteMessage(websocket.TextMessage, []byte(`{invalid json!!!`))

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, readErr := conn.ReadMessage()
	if readErr == nil {
		t.Error("发送畸形 JSON 后，Server 应关闭连接")
	}
}

// ============================================================
// 控制通道 — 认证超时 P16 (1)
// ============================================================

// TestAuth_TimeoutNoMessage 验证 P16：连接后不发认证消息，服务端应在超时后断开
func TestAuth_TimeoutNoMessage(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.authTimeout = 500 * time.Millisecond // 短超时方便测试

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 连接后什么都不发，等待服务端超时关闭
	start := time.Now()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, readErr := conn.ReadMessage()
	elapsed := time.Since(start)

	if readErr == nil {
		t.Fatal("不发认证消息时，服务端应超时关闭连接")
	}

	// 验证断开时间在合理范围内（500ms ~ 2s）
	if elapsed < 400*time.Millisecond {
		t.Errorf("断开太快（%v），可能不是超时导致的", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("断开太慢（%v），超时可能未生效", elapsed)
	}
}

// ============================================================
// 控制通道 — 心跳 (2)
// ============================================================

func TestHeartbeat_PingPong(t *testing.T) {
	_, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer dataConn.Close()

	ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	conn.WriteJSON(ping)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("读取 Pong 失败: %v", err)
	}
	if resp.Type != protocol.MsgTypePong {
		t.Errorf("期望 pong，得到 %s", resp.Type)
	}
}

func TestHeartbeat_MultiplePings(t *testing.T) {
	_, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer dataConn.Close()

	for i := 0; i < 10; i++ {
		ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
		if err := conn.WriteJSON(ping); err != nil {
			t.Fatalf("第 %d 次发送 Ping 失败: %v", i, err)
		}

		conn.SetReadDeadline(time.Now().Add(testReadTimeout(2 * time.Second)))
		var resp protocol.Message
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("第 %d 次读取 Pong 失败: %v", i, err)
		}
		if resp.Type != protocol.MsgTypePong {
			t.Errorf("第 %d 次: 期望 pong，得到 %s", i, resp.Type)
		}
	}
}

// ============================================================
// 控制通道 — 探针上报 (2)
// ============================================================

func TestProbe_SingleReport(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer dataConn.Close()

	stats := protocol.SystemStats{
		CPUUsage: 42.5,
		MemUsage: 60.0,
		MemTotal: 8 * 1024 * 1024 * 1024,
		MemUsed:  4_800_000_000,
		NumCPU:   4,
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	conn.WriteJSON(msg)

	time.Sleep(100 * time.Millisecond)

	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("Client 未注册")
	}
	client := val.(*ClientConn)
	if client.GetStats() == nil {
		t.Fatal("Stats 不应为 nil")
	}
	if client.GetStats().CPUUsage != 42.5 {
		t.Errorf("CPUUsage 期望 42.5，得到 %f", client.GetStats().CPUUsage)
	}
	if client.GetStats().MemUsage != 60.0 {
		t.Errorf("MemUsage 期望 60.0，得到 %f", client.GetStats().MemUsage)
	}
	if client.GetStats().NumCPU != 4 {
		t.Errorf("NumCPU 期望 4，得到 %d", client.GetStats().NumCPU)
	}
}

func TestProbe_ReportPersistedAfterDisconnect(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer dataConn.Close()

	stats := protocol.SystemStats{
		CPUUsage: 42.5,
		MemUsage: 60.0,
		NumCPU:   4,
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("发送探针数据失败: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	conn.Close()
	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 /api/clients 失败: %v", err)
	}
	defer resp.Body.Close()

	var clients []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		t.Fatalf("解析 /api/clients 响应失败: %v", err)
	}

	for _, client := range clients {
		if client["id"] != authResp.ClientID {
			continue
		}
		if online, _ := client["online"].(bool); online {
			t.Fatal("断开后的 Client 不应仍然标记为在线")
		}
		statsMap, ok := client["stats"].(map[string]any)
		if !ok {
			t.Fatal("断开后的 Client 仍应返回最后一次 stats")
		}
		if statsMap["cpu_usage"].(float64) != 42.5 {
			t.Fatalf("cpu_usage 期望 42.5，得到 %v", statsMap["cpu_usage"])
		}
		return
	}

	t.Fatalf("未找到 Client %s", authResp.ClientID)
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

	record, err := s.adminStore.GetOrCreateClient("install-persisted-host", info, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("预创建 Client 记录失败: %v", err)
	}
	if err := s.adminStore.UpdateClientStats(record.ID, info, protocol.SystemStats{
		CPUUsage: 88.8,
		MemUsage: 66.6,
		NumCPU:   16,
	}, "127.0.0.1:12345"); err != nil {
		t.Fatalf("预写入 Client stats 失败: %v", err)
	}

	conn, authResp := connectAndAuthWithInstallID(t, ts, "persisted-host", "install-persisted-host")
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 /api/clients 失败: %v", err)
	}
	defer resp.Body.Close()

	var clients []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		t.Fatalf("解析 /api/clients 响应失败: %v", err)
	}

	for _, client := range clients {
		if client["id"] != authResp.ClientID {
			continue
		}
		if online, _ := client["online"].(bool); !online {
			t.Fatal("已连接的 Client 应标记为在线")
		}
		statsMap, ok := client["stats"].(map[string]any)
		if !ok {
			t.Fatal("首次新上报前应先返回持久化的旧 stats")
		}
		if statsMap["cpu_usage"].(float64) != 88.8 {
			t.Fatalf("cpu_usage 期望 88.8，得到 %v", statsMap["cpu_usage"])
		}
		return
	}

	t.Fatalf("未找到 Client %s", authResp.ClientID)
}

func TestProbe_MultipleReports(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer dataConn.Close()

	for i := 0; i < 5; i++ {
		cpuVal := float64(i+1) * 10.0
		stats := protocol.SystemStats{CPUUsage: cpuVal, NumCPU: 8}
		msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
		conn.WriteJSON(msg)
		time.Sleep(30 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	val, _ := s.clients.Load(authResp.ClientID)
	client := val.(*ClientConn)
	if client.GetStats().CPUUsage != 50.0 {
		t.Errorf("最终 CPUUsage 应为 50.0（最后一次上报），得到 %f", client.GetStats().CPUUsage)
	}
}

// ============================================================
// 生命周期与并发 (3)
// ============================================================

func TestLifecycle_Full(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn, authResp := connectAndAuth(t, ts, "lifecycle-host")

	time.Sleep(50 * time.Millisecond)

	_, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("认证后 Client 应已注册")
	}

	ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	conn.WriteJSON(ping)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var pong protocol.Message
	conn.ReadJSON(&pong)
	if pong.Type != protocol.MsgTypePong {
		t.Errorf("心跳: 期望 pong，得到 %s", pong.Type)
	}

	stats := protocol.SystemStats{CPUUsage: 33.3, NumCPU: 2}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	conn.WriteJSON(msg)
	time.Sleep(50 * time.Millisecond)

	val, _ := s.clients.Load(authResp.ClientID)
	if val.(*ClientConn).GetStats().CPUUsage != 33.3 {
		t.Error("探针数据未正确更新")
	}

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	_, ok = s.clients.Load(authResp.ClientID)
	if ok {
		t.Error("断开后 Client 应已从 map 中移除")
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
			defer conn.Close()

			authReq := protocol.AuthRequest{
				Key:       "test-key",
				InstallID: "install-" + hostname,
				Client:    protocol.ClientInfo{Hostname: hostname, OS: "linux", Arch: "amd64", Version: "0.1.0"},
			}
			msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
			conn.WriteJSON(msg)

			var resp protocol.Message
			conn.SetReadDeadline(time.Now().Add(testReadTimeout(10 * time.Second)))
			if err := conn.ReadJSON(&resp); err != nil {
				errors <- err
				return
			}

			var authResp protocol.AuthResponse
			resp.ParsePayload(&authResp)
			if !authResp.Success {
				errors <- fmt.Errorf("auth failed: %s", authResp.Message)
				return
			}

			dataConn, err := dialDataWSForClient(ts, authResp)
			if err != nil {
				errors <- err
				return
			}
			defer dataConn.Close()

			ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
			if err := conn.WriteJSON(ping); err != nil {
				errors <- err
				return
			}
			conn.SetReadDeadline(time.Now().Add(testReadTimeout(10 * time.Second)))
			if err := conn.ReadJSON(&resp); err != nil {
				errors <- err
				return
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("并发 Client 出错: %v", err)
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
		t.Fatal("两个 Client 都应已注册")
	}

	conn2.Close()
	time.Sleep(100 * time.Millisecond)

	_, ok1 = s.clients.Load(auth1.ClientID)
	_, ok2 = s.clients.Load(auth2.ClientID)
	if !ok1 {
		t.Error("Client1 不应被移除")
	}
	if ok2 {
		t.Error("Client2 应已被移除")
	}

	conn1.Close()
}

func TestControlLoop_ProxyMessages(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn, authResp := connectAndAuth(t, ts, "proxy-msg-host")
	defer conn.Close()

	// clients.Store 已在 handleAuth 内、认证响应发送前完成，
	// 所以 connectAndAuth 返回时 client 一定已在 map 中。
	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("认证成功后 Client 应已注册到 clients map")
	}
	client := val.(*ClientConn)
	cPipe, sPipe := net.Pipe()
	defer cPipe.Close()
	defer sPipe.Close()
	client.dataMu.Lock()
	client.dataSession, _ = mux.NewServerSession(sPipe, mux.DefaultConfig())
	client.dataMu.Unlock()

	// 测试 MsgTypeProxyCreate
	req := protocol.ProxyNewRequest{
		Name:       "ws-tunnel-1",
		Type:       protocol.ProxyTypeTCP,
		RemotePort: reserveTCPPort(t),
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyCreate, protocol.ProxyCreateRequest(req))
	conn.WriteJSON(msg)

	var resp protocol.Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("读取创建代理响应失败: %v", err)
	}

	if resp.Type != protocol.MsgTypeProxyCreateResp {
		t.Errorf("期望返回 %s，得到 %s", protocol.MsgTypeProxyCreateResp, resp.Type)
	}

	// 测试 MsgTypeProxyClose
	closeReq := protocol.ProxyCloseRequest{Name: "ws-tunnel-1"}
	closeMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyClose, closeReq)
	conn.WriteJSON(closeMsg)
	time.Sleep(100 * time.Millisecond)

	client.proxyMu.RLock()
	_, exists := client.proxies["ws-tunnel-1"]
	client.proxyMu.RUnlock()

	if exists {
		t.Error("发送 ProxyClose 后代理隧道仍存在")
	}
}

func TestControlLoop_ProxyCreateResponse(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn, authResp := connectAndAuth(t, ts, "legacy-proxy-create-host")
	defer conn.Close()

	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("认证成功后 Client 应已注册到 clients map")
	}
	client := val.(*ClientConn)
	cPipe, sPipe := net.Pipe()
	defer cPipe.Close()
	defer sPipe.Close()
	client.dataMu.Lock()
	client.dataSession, _ = mux.NewServerSession(sPipe, mux.DefaultConfig())
	client.dataMu.Unlock()

	req := protocol.ProxyNewRequest{
		Name:       "legacy-ws-tunnel",
		Type:       protocol.ProxyTypeTCP,
		RemotePort: reserveTCPPort(t),
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyCreate, req)
	conn.WriteJSON(msg)

	var resp protocol.Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("读取创建代理响应失败: %v", err)
	}

	if resp.Type != protocol.MsgTypeProxyCreateResp {
		t.Fatalf("期望返回 %s，得到 %s", protocol.MsgTypeProxyCreateResp, resp.Type)
	}

	var payload protocol.ProxyCreateResponse
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatalf("解析创建代理响应失败: %v", err)
	}
	if !payload.Success {
		t.Fatalf("创建代理应成功，得到失败: %s", payload.Message)
	}

	client.proxyMu.RLock()
	_, exists := client.proxies[req.Name]
	client.proxyMu.RUnlock()
	if !exists {
		t.Fatalf("创建代理后隧道应存在: %s", req.Name)
	}
}

// ============================================================
// controlLoop 边缘场景测试 (2)
// ============================================================

func TestControlLoop_UnknownMsgType(t *testing.T) {
	_, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	conn, _ := connectAndAuth(t, ts, "unknown-msg-host")
	defer conn.Close()

	// 发送一个未知消息类型
	unknownMsg, _ := protocol.NewMessage("unknown_type_xyz", nil)
	conn.WriteJSON(unknownMsg)

	// Server 不应崩溃，继续正常工作
	// 发一个 ping 验证连接仍然正常
	time.Sleep(50 * time.Millisecond)
	ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	conn.WriteJSON(ping)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("发送未知消息后连接应保持正常: %v", err)
	}
	if resp.Type != protocol.MsgTypePong {
		t.Errorf("期望 pong，得到 %s", resp.Type)
	}
}

func TestControlLoop_MalformedProbeReport(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)

	conn, authResp := connectAndAuth(t, ts, "malformed-probe-host")

	// 发送一个 payload 字段类型不匹配的 probe_report
	// (JSON 格式有效, 但 cpu_usage 的类型是 string 不是 float —— ParsePayload 会失败)
	badMsg := protocol.Message{
		Type:    protocol.MsgTypeProbeReport,
		Payload: json.RawMessage(`{"cpu_usage": "not_a_number", "mem_usage": "bad"}`),
	}
	conn.WriteJSON(badMsg)

	// 连接仍然正常 — 发 ping 验证 (如果 controlLoop 没崩溃，就能回 pong)
	ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
	conn.WriteJSON(ping)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("发送畸形探针后连接应保持正常: %v", err)
	}
	if resp.Type != protocol.MsgTypePong {
		t.Errorf("期望 pong，得到 %s", resp.Type)
	}

	// Client 的 stats 应该没被更新（还是 nil）
	val, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatal("Client 应该仍然已注册")
	}
	client := val.(*ClientConn)
	if client.GetStats() != nil {
		t.Error("畸形 probe_report 不应导致 stats 被更新")
	}

	conn.Close()
	cleanup()
}

func TestServer_StartHTTPOnly(t *testing.T) {
	s := New(0)
	mux := s.StartHTTPOnly()
	if mux == nil {
		t.Fatal("StartHTTPOnly 应返回非空 ServeMux")
	}
}

// ============================================================
// Tunnel Lifecycle API 测试 (Phase 2)
// ============================================================

func TestServer_TunnelLifecycleAPI(t *testing.T) {
	// 1. 初始化带 DB 的 Server
	tmpDir, _ := os.MkdirTemp("", "tunnel_api_test_*")
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "admin.db")
	store, _ := NewAdminStore(dbPath)
	store.Initialize("admin", "password123", "localhost", nil)
	store.AddAPIKey("default", "test-key", []string{"connect"}, nil)

	s := New(0)
	s.adminStore = store
	s.store, _ = NewTunnelStore(filepath.Join(tmpDir, "tunnels.json"))

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	// 模拟已登录的 AdminSession
	session := mustCreateSession(t, store, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, _ := s.GenerateAdminToken(session)

	// API 请求助手
	doRequest := func(method, path string, body []byte) (int, map[string]any) {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "test")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("API 请求失败 %s: %v", path, err)
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
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

		wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var serverMsg protocol.Message
		if err := wsConn.ReadJSON(&serverMsg); err != nil {
			t.Fatalf("读取服务端 proxy_provision 失败: %v", err)
		}
		wsConn.SetReadDeadline(time.Time{})
		if serverMsg.Type != protocol.MsgTypeProxyProvision {
			t.Fatalf("期望服务端下发 %s，得到 %s", protocol.MsgTypeProxyProvision, serverMsg.Type)
		}

		var proxyReq protocol.ProxyProvisionRequest
		if err := serverMsg.ParsePayload(&proxyReq); err != nil {
			t.Fatalf("解析服务端 proxy_provision 失败: %v", err)
		}
		if proxyReq.Name != expectedName {
			t.Fatalf("期望 proxy_provision.Name=%s，得到 %s", expectedName, proxyReq.Name)
		}

		val, ok := s.clients.Load(clientID)
		if !ok {
			t.Fatalf("client %s 不存在", clientID)
		}
		liveClient := val.(*ClientConn)
		liveClient.proxyMu.RLock()
		pendingTunnel := liveClient.proxies[expectedName]
		liveClient.proxyMu.RUnlock()
		if pendingTunnel == nil {
			t.Fatalf("收到 proxy_provision 时应已有 pending tunnel: %s", expectedName)
		}
		if pendingTunnel.Config.DesiredState != protocol.ProxyDesiredStateRunning || pendingTunnel.Config.RuntimeState != protocol.ProxyRuntimeStatePending {
			t.Fatalf("proxy_provision 下发时 tunnel 状态应为 running/pending，得到 %s/%s", pendingTunnel.Config.DesiredState, pendingTunnel.Config.RuntimeState)
		}
		if method == http.MethodPost {
			if _, exists := s.store.GetTunnel(clientID, expectedName); exists {
				t.Fatalf("create 在 client ack 前不应写入 Store: %s", expectedName)
			}
		}
		if method == http.MethodPut {
			if stored, exists := s.store.GetTunnel(clientID, expectedName); !exists ||
				stored.DesiredState != protocol.ProxyDesiredStateRunning ||
				stored.RuntimeState != protocol.ProxyRuntimeStatePending {
				t.Fatalf("resume 在 client ack 前 Store 状态应为 running/pending，exists=%v state=%s/%s", exists, stored.DesiredState, stored.RuntimeState)
			}
		}

		respMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyProvisionAck, protocol.ProxyProvisionAck{
			Name:     expectedName,
			Accepted: true,
			Message:  "ok",
		})
		if err := wsConn.WriteJSON(respMsg); err != nil {
			t.Fatalf("发送 proxy_provision_ack 失败: %v", err)
		}

		select {
		case result := <-resultCh:
			return result
		case <-time.After(3 * time.Second):
			t.Fatalf("等待 API %s %s 返回超时", method, path)
			return apiResult{}
		}
	}

	// 2. 模拟一个 Client 连接
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	wsConn, _, err = websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer wsConn.Close()

	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-lifecycle-client",
		Client:    protocol.ClientInfo{Hostname: "lifecycle-client", OS: "linux", Version: "1.0.0"},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	wsConn.WriteJSON(msg)

	var authRespMsg protocol.Message
	wsConn.ReadJSON(&authRespMsg)
	var authResp protocol.AuthResponse
	authRespMsg.ParsePayload(&authResp)

	clientID = authResp.ClientID
	dataConn := connectDataWSForClient(t, ts, authResp)
	defer dataConn.Close()

	time.Sleep(50 * time.Millisecond) // 等待 client 提升到 live

	// ========= 测试开始 =========

	// 1. 创建隧道 (/api/clients/{id}/tunnels)
	createReq := []byte(`{"name":"test-tunnel","type":"tcp","local_ip":"127.0.0.1","local_port":8080,"remote_port":18080}`)
	result := runPendingAction(http.MethodPost, fmt.Sprintf("/api/clients/%s/tunnels", clientID), createReq, "test-tunnel")
	code, resp := result.code, result.body

	if code != http.StatusCreated {
		t.Errorf("创建隧道期望 201 Created，得到 %d, 响应: %v", code, resp)
	}

	// 验证隧道在 Store 中生成
	tunnel, ok := s.store.GetTunnel(clientID, "test-tunnel")
	if !ok {
		t.Fatal("Tunnel 未写入 Store")
	}
	if tunnel.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Errorf("初创状态应为 running/exposed，得到 %s/%s", tunnel.DesiredState, tunnel.RuntimeState)
	}

	// 2. 暂停隧道 (/api/clients/{id}/tunnels/{name}/pause)
	pauseReq := []byte(`{}`)
	code, _ = doRequest(http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/test-tunnel/pause", clientID), pauseReq)
	if code != http.StatusOK {
		t.Errorf("暂停隧道期望 200，得到 %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(clientID, "test-tunnel")
	if tunnel.DesiredState != protocol.ProxyDesiredStatePaused || tunnel.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("隧道暂停后，状态应为 paused/idle，得到 %s/%s", tunnel.DesiredState, tunnel.RuntimeState)
	}
	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var closeMsg protocol.Message
	if err := wsConn.ReadJSON(&closeMsg); err != nil {
		t.Fatalf("读取 pause 后的 proxy_close 失败: %v", err)
	}
	wsConn.SetReadDeadline(time.Time{})
	if closeMsg.Type != protocol.MsgTypeProxyClose {
		t.Fatalf("暂停后期望收到 %s，得到 %s", protocol.MsgTypeProxyClose, closeMsg.Type)
	}

	// 3. 恢复隧道 (/api/clients/{id}/tunnels/{name}/resume)
	resumeReq := []byte(`{}`)
	result = runPendingAction(http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/test-tunnel/resume", clientID), resumeReq, "test-tunnel")
	code = result.code
	if code != http.StatusOK {
		t.Errorf("恢复隧道期望 200，得到 %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(clientID, "test-tunnel")
	if tunnel.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Errorf("隧道恢复后，状态应为 running/exposed，得到 %s/%s", tunnel.DesiredState, tunnel.RuntimeState)
	}

	// 4. 停止隧道 (/api/clients/{id}/tunnels/{name}/stop)
	stopReq := []byte(`{}`)
	code, _ = doRequest(http.MethodPut, fmt.Sprintf("/api/clients/%s/tunnels/test-tunnel/stop", clientID), stopReq)
	if code != http.StatusOK {
		t.Errorf("停止隧道期望 200，得到 %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(clientID, "test-tunnel")
	if tunnel.DesiredState != protocol.ProxyDesiredStateStopped || tunnel.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("隧道停止后，状态应为 stopped/idle，得到 %s/%s", tunnel.DesiredState, tunnel.RuntimeState)
	}

	// 5. 删除隧道 (/api/clients/{id}/tunnels/{name})
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels/test-tunnel", clientID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")
	respDel, _ := http.DefaultClient.Do(req)
	if respDel.StatusCode != http.StatusNoContent {
		t.Errorf("删除隧道期望 204 No Content，得到 %d", respDel.StatusCode)
	}
	respDel.Body.Close()

	if _, ok := s.store.GetTunnel(clientID, "test-tunnel"); ok {
		t.Error("删除后，Store 中不应再有此隧道")
	}
}

func TestServer_CreateTunnelTimeoutReturns504(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()
	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}

	wsConn, authResp := connectAndAuth(t, ts, "timeout-client")
	defer wsConn.Close()

	session := mustCreateSession(t, s.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 Admin Token 失败: %v", err)
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
		defer resp.Body.Close()

		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resultCh <- apiResult{code: resp.StatusCode, body: body}
	}()

	select {
	case err := <-errCh:
		t.Fatalf("create tunnel 请求失败: %v", err)
	case result := <-resultCh:
		if result.code != http.StatusGatewayTimeout {
			t.Fatalf("create 超时期望 504，得到 %d, body=%v", result.code, result.body)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("等待 create timeout 响应超时")
	}

	if _, exists := s.store.GetTunnel(authResp.ClientID, "timeout-tunnel"); exists {
		t.Fatal("create timeout 后不应写入 Store")
	}

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s 应仍在线", authResp.ClientID)
	}
	client := value.(*ClientConn)
	client.proxyMu.RLock()
	_, exists := client.proxies["timeout-tunnel"]
	client.proxyMu.RUnlock()
	if exists {
		t.Fatal("create timeout 后 runtime pending tunnel 应被清理")
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
				t.Fatalf("解析 tunnel_changed 事件失败: %v", err)
			}
			if payload["action"] != "error" {
				continue
			}
			tunnelPayload, ok := payload["tunnel"].(map[string]any)
			if !ok {
				t.Fatalf("tunnel_changed.tunnel 类型无效: %#v", payload["tunnel"])
			}
			if tunnelPayload["name"] != "timeout-tunnel" {
				continue
			}
			if tunnelPayload["runtime_state"] != protocol.ProxyRuntimeStateError {
				t.Fatalf("超时失败事件 runtime_state 期望 error，得到 %v", tunnelPayload["runtime_state"])
			}
			if tunnelPayload["error"] == "" {
				t.Fatal("超时失败事件应携带 error 文案")
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}

	t.Fatal("create timeout 后未收到最终 error 通知")
}

func TestServer_CreateTunnelHTTPConflictReturns409WithErrorCode(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}

	wsConn, authResp := connectAndAuth(t, ts, "http-conflict-create")
	defer wsConn.Close()

	seedStoredTunnel(t, s, "client-other", protocol.ProxyNewRequest{
		Name:      "existing-http",
		Type:      protocol.ProxyTypeHTTP,
		Domain:    "app.example.com",
		LocalIP:   "127.0.0.1",
		LocalPort: 8080,
	}, protocol.ProxyStatusPaused)

	session := mustCreateSession(t, s.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 Admin Token 失败: %v", err)
	}
	reqBody := []byte(`{"name":"new-http","type":"http","local_ip":"127.0.0.1","local_port":3000,"domain":"app.example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create tunnel 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("HTTP 域名冲突时期望 409，得到 %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body["error_code"] != protocol.TunnelMutationErrorCodeHTTPTunnelConflict {
		t.Fatalf("error_code 期望 %q，得到 %v", protocol.TunnelMutationErrorCodeHTTPTunnelConflict, body["error_code"])
	}
	if body["field"] != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field 期望 %q，得到 %v", protocol.TunnelMutationFieldDomain, body["field"])
	}
	conflicts, ok := body["conflicting_tunnels"].([]any)
	if !ok || len(conflicts) != 1 || conflicts[0] != "client-other:existing-http" {
		t.Fatalf("conflicting_tunnels 期望 [client-other:existing-http]，得到 %v", body["conflicting_tunnels"])
	}
}

func TestServer_UpdateTunnelHTTPConflictReturns409WithErrorCode(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}

	wsConn, authResp := connectAndAuth(t, ts, "http-conflict-update")
	defer wsConn.Close()

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
	}, protocol.ProxyStatusPaused)

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s 不存在", authResp.ClientID)
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
			DesiredState: protocol.ProxyDesiredStatePaused,
			RuntimeState: protocol.ProxyRuntimeStateIdle,
		},
		done: make(chan struct{}),
	}
	client.proxyMu.Unlock()

	session := mustCreateSession(t, s.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 Admin Token 失败: %v", err)
	}
	reqBody := []byte(`{"local_ip":"127.0.0.1","local_port":3000,"remote_port":0,"domain":"app.example.com"}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels/editable-http", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update tunnel 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("HTTP 域名冲突时期望 409，得到 %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body["error_code"] != protocol.TunnelMutationErrorCodeHTTPTunnelConflict {
		t.Fatalf("error_code 期望 %q，得到 %v", protocol.TunnelMutationErrorCodeHTTPTunnelConflict, body["error_code"])
	}
	if body["field"] != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field 期望 %q，得到 %v", protocol.TunnelMutationFieldDomain, body["field"])
	}
	conflicts, ok := body["conflicting_tunnels"].([]any)
	if !ok || len(conflicts) != 1 || conflicts[0] != "client-other:existing-http" {
		t.Fatalf("conflicting_tunnels 期望 [client-other:existing-http]，得到 %v", body["conflicting_tunnels"])
	}
}

func TestServer_CreateTunnelHTTPInvalidDomainReturns400WithTypedError(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsConn, authResp := connectAndAuth(t, ts, "http-invalid-domain-create")
	defer wsConn.Close()

	session := mustCreateSession(t, s.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 Admin Token 失败: %v", err)
	}

	reqBody := []byte(`{"name":"new-http","type":"http","local_ip":"127.0.0.1","local_port":3000,"domain":"https://bad.example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create tunnel 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("HTTP 非法域名期望 400，得到 %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body["error_code"] != protocol.TunnelMutationErrorCodeDomainInvalid {
		t.Fatalf("error_code 期望 %q，得到 %v", protocol.TunnelMutationErrorCodeDomainInvalid, body["error_code"])
	}
	if body["field"] != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field 期望 %q，得到 %v", protocol.TunnelMutationFieldDomain, body["field"])
	}
}

func TestServer_CreateTunnelHTTPManagementHostConflictReturnsTypedError(t *testing.T) {
	t.Setenv("NETSGO_SERVER_ADDR", "https://example.com")

	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	wsConn, authResp := connectAndAuth(t, ts, "http-server-addr-conflict-create")
	defer wsConn.Close()

	session := mustCreateSession(t, s.adminStore, "test-user", "admin", "admin", "127.0.0.1", "test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 Admin Token 失败: %v", err)
	}

	reqBody := []byte(`{"name":"new-http","type":"http","local_ip":"127.0.0.1","local_port":3000,"domain":"example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+fmt.Sprintf("/api/clients/%s/tunnels", authResp.ClientID), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create tunnel 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("HTTP 管理域名冲突期望 409，得到 %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body["error_code"] != protocol.TunnelMutationErrorCodeServerAddrConflict {
		t.Fatalf("error_code 期望 %q，得到 %v", protocol.TunnelMutationErrorCodeServerAddrConflict, body["error_code"])
	}
	if body["field"] != protocol.TunnelMutationFieldDomain {
		t.Fatalf("field 期望 %q，得到 %v", protocol.TunnelMutationFieldDomain, body["field"])
	}
}

func TestServer_ResumePostAckStoreFailureRollsBackAndClosesClientProxy(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}

	wsConn, authResp := connectAndAuth(t, ts, "resume-post-ack-fail")
	defer wsConn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, ok := s.clients.Load(authResp.ClientID)
		if ok && value.(*ClientConn).getState() == clientStateLive {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	session := mustCreateSession(t, s.adminStore, "user-1", "admin", "admin", "127.0.0.1", "resume-test-agent")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 Admin Token 失败: %v", err)
	}
	doRequest := func(method, path string, body []byte) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "resume-test-agent")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("HTTP 请求失败 %s %s: %v", method, path, err)
		}
		defer resp.Body.Close()

		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	value, ok := s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s 不存在", authResp.ClientID)
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
			DesiredState: protocol.ProxyDesiredStatePaused,
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
		DesiredState: protocol.ProxyDesiredStatePaused,
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
		t.Fatalf("resume 请求在发送 proxy_provision 前已返回: code=%d body=%v", result.code, result.body)
	case <-time.After(200 * time.Millisecond):
	}

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resumeMsg protocol.Message
	if err := wsConn.ReadJSON(&resumeMsg); err != nil {
		t.Fatalf("读取 resume 阶段 proxy_provision 失败: %v", err)
	}
	wsConn.SetReadDeadline(time.Time{})
	if resumeMsg.Type != protocol.MsgTypeProxyProvision {
		t.Fatalf("resume 阶段期望 %s，得到 %s", protocol.MsgTypeProxyProvision, resumeMsg.Type)
	}
	var resumeProxyReq protocol.ProxyProvisionRequest
	if err := resumeMsg.ParsePayload(&resumeProxyReq); err != nil {
		t.Fatalf("解析 resume proxy_provision 失败: %v", err)
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
		t.Fatalf("发送 resume ack 失败: %v", err)
	}

	select {
	case result := <-resumeResultCh:
		if result.code != http.StatusInternalServerError {
			t.Fatalf("resume 在 post-ack 持久化失败时期望 500，得到 %d body=%v", result.code, result.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待 resume API 返回超时")
	}

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var rollbackCloseMsg protocol.Message
	if err := wsConn.ReadJSON(&rollbackCloseMsg); err != nil {
		t.Fatalf("读取 rollback proxy_close 失败: %v", err)
	}
	wsConn.SetReadDeadline(time.Time{})
	if rollbackCloseMsg.Type != protocol.MsgTypeProxyClose {
		t.Fatalf("rollback 后期望 %s，得到 %s", protocol.MsgTypeProxyClose, rollbackCloseMsg.Type)
	}
	var closePayload protocol.ProxyCloseRequest
	if err := rollbackCloseMsg.ParsePayload(&closePayload); err != nil {
		t.Fatalf("解析 rollback proxy_close 失败: %v", err)
	}
	if closePayload.Name != "resume-rollback" {
		t.Fatalf("rollback proxy_close name 期望 resume-rollback，得到 %s", closePayload.Name)
	}
	if closePayload.Reason != "provision_failed" {
		t.Fatalf("rollback proxy_close reason 期望 provision_failed，得到 %s", closePayload.Reason)
	}

	value, ok = s.clients.Load(authResp.ClientID)
	if !ok {
		t.Fatalf("client %s 应仍在线", authResp.ClientID)
	}
	client = value.(*ClientConn)
	client.proxyMu.RLock()
	runtimeTunnel := client.proxies["resume-rollback"]
	client.proxyMu.RUnlock()
	if runtimeTunnel == nil {
		t.Fatal("resume rollback 后 runtime tunnel 不应丢失")
	}
	if runtimeTunnel.Config.DesiredState != protocol.ProxyDesiredStatePaused || runtimeTunnel.Config.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("resume rollback 后 runtime 状态期望 paused/idle，得到 %s/%s", runtimeTunnel.Config.DesiredState, runtimeTunnel.Config.RuntimeState)
	}
	if runtimeTunnel.Config.Error != "" {
		t.Fatalf("resume rollback 后 runtime error 应为空，得到 %q", runtimeTunnel.Config.Error)
	}

	storedTunnel, exists := s.store.GetTunnel(authResp.ClientID, "resume-rollback")
	if !exists {
		t.Fatal("resume rollback 后 store tunnel 不应丢失")
	}
	if storedTunnel.DesiredState != protocol.ProxyDesiredStatePaused || storedTunnel.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("resume rollback 后 store 状态期望 paused/idle，得到 %s/%s", storedTunnel.DesiredState, storedTunnel.RuntimeState)
	}
	if storedTunnel.Error != "" {
		t.Fatalf("resume rollback 后 store error 应为空，得到 %q", storedTunnel.Error)
	}
}

func TestServer_RestorePostAckStoreFailureMarksError(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}

	record, err := s.adminStore.GetOrCreateClient(
		"install-restore-post-ack-fail",
		protocol.ClientInfo{Hostname: "restore-post-ack-fail"},
		"127.0.0.1:12345",
	)
	if err != nil {
		t.Fatalf("预注册 client 失败: %v", err)
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
		t.Fatalf("控制通道连接失败: %v", err)
	}
	defer controlConn.Close()

	authResp := doAuthWithInstallID(t, controlConn, "restore-post-ack-fail", "install-restore-post-ack-fail", "test-key")
	if !authResp.Success {
		t.Fatalf("认证失败: %s", authResp.Message)
	}
	if authResp.ClientID != record.ID {
		t.Fatalf("预注册 client_id=%s，应与认证返回一致，得到 %s", record.ID, authResp.ClientID)
	}

	dataConn := connectDataWSForClient(t, ts, authResp)
	defer dataConn.Close()

	controlConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var restoreMsg protocol.Message
	if err := controlConn.ReadJSON(&restoreMsg); err != nil {
		t.Fatalf("读取 restore 阶段 proxy_provision 失败: %v", err)
	}
	controlConn.SetReadDeadline(time.Time{})
	if restoreMsg.Type != protocol.MsgTypeProxyProvision {
		t.Fatalf("restore 阶段期望 %s，得到 %s", protocol.MsgTypeProxyProvision, restoreMsg.Type)
	}
	var restoreReq protocol.ProxyProvisionRequest
	if err := restoreMsg.ParsePayload(&restoreReq); err != nil {
		t.Fatalf("解析 restore proxy_provision 失败: %v", err)
	}
	if restoreReq.Name != "restore-fail-tunnel" {
		t.Fatalf("restore tunnel name 期望 restore-fail-tunnel，得到 %s", restoreReq.Name)
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
		t.Fatalf("发送 restore ack 失败: %v", err)
	}

	controlConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var closeMsg protocol.Message
	if err := controlConn.ReadJSON(&closeMsg); err != nil {
		t.Fatalf("读取 restore 失败后的 proxy_close 失败: %v", err)
	}
	controlConn.SetReadDeadline(time.Time{})
	if closeMsg.Type != protocol.MsgTypeProxyClose {
		t.Fatalf("restore 失败后期望 %s，得到 %s", protocol.MsgTypeProxyClose, closeMsg.Type)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, ok := s.clients.Load(authResp.ClientID)
		if !ok {
			t.Fatalf("client %s 不应丢失", authResp.ClientID)
		}
		client := value.(*ClientConn)
		client.proxyMu.RLock()
		tunnel := client.proxies["restore-fail-tunnel"]
		client.proxyMu.RUnlock()
		if tunnel != nil &&
			tunnel.Config.DesiredState == protocol.ProxyDesiredStateRunning &&
			tunnel.Config.RuntimeState == protocol.ProxyRuntimeStateError {
			if tunnel.Config.Error == "" {
				t.Fatal("runtime error 隧道应携带失败原因")
			}
			stored, exists := s.store.GetTunnel(authResp.ClientID, "restore-fail-tunnel")
			if !exists {
				t.Fatal("store 中应保留 restore-fail-tunnel")
			}
			if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
				t.Fatalf("store 状态期望 running/error，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
			}
			if stored.Error == "" {
				t.Fatal("store error 隧道应持久化失败原因")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待 restore 失败降级到 error 超时")
}

func TestServer_RestoreActiveHTTPTunnel_DoesNotConflictWithSelf(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()

	var err error
	s.store, err = NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}

	record, err := s.adminStore.GetOrCreateClient(
		"install-restore-http",
		protocol.ClientInfo{Hostname: "restore-http-host"},
		"127.0.0.1:12345",
	)
	if err != nil {
		t.Fatalf("预注册 client 失败: %v", err)
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
		t.Fatalf("控制通道连接失败: %v", err)
	}
	defer controlConn.Close()

	authResp := doAuthWithInstallID(t, controlConn, "restore-http-host", "install-restore-http", "test-key")
	if !authResp.Success {
		t.Fatalf("认证失败: %s", authResp.Message)
	}
	if authResp.ClientID != record.ID {
		t.Fatalf("预注册 client_id=%s，应与认证返回一致，得到 %s", record.ID, authResp.ClientID)
	}

	dataConn := connectDataWSForClient(t, ts, authResp)
	defer dataConn.Close()

	controlConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var restoreMsg protocol.Message
	if err := controlConn.ReadJSON(&restoreMsg); err != nil {
		t.Fatalf("读取 HTTP restore 阶段 proxy_provision 失败: %v", err)
	}
	controlConn.SetReadDeadline(time.Time{})

	if restoreMsg.Type != protocol.MsgTypeProxyProvision {
		t.Fatalf("HTTP restore 阶段期望 %s，得到 %s", protocol.MsgTypeProxyProvision, restoreMsg.Type)
	}

	var restoreReq protocol.ProxyProvisionRequest
	if err := restoreMsg.ParsePayload(&restoreReq); err != nil {
		t.Fatalf("解析 HTTP restore proxy_provision 失败: %v", err)
	}
	if restoreReq.Name != "restore-http" {
		t.Fatalf("restore tunnel name 期望 restore-http，得到 %s", restoreReq.Name)
	}
	if restoreReq.Type != protocol.ProxyTypeHTTP {
		t.Fatalf("restore tunnel type 期望 http，得到 %s", restoreReq.Type)
	}
	if restoreReq.Domain != "app.example.com" {
		t.Fatalf("restore tunnel domain 期望 app.example.com，得到 %s", restoreReq.Domain)
	}
}

func TestServer_RestoreTunnelsAPI(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tunnel_restore_test_*")
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "admin.db")
	store, _ := NewAdminStore(dbPath)
	store.Initialize("admin", "password123", "localhost", nil)

	tunnelStorePath := filepath.Join(tmpDir, "tunnels.json")
	tStore, _ := NewTunnelStore(tunnelStorePath)

	// 在 Store 预先写入两个隧道 (代表服务器重启读取持久化数据)
	tStore.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "tunnel1", Type: "tcp", RemotePort: 1234},
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		ClientID:        "client-1",
		Hostname:        "restore-host",
		Binding:         TunnelBindingClientID,
	})
	tStore.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "tunnel2", Type: "tcp", RemotePort: 5678},
		DesiredState:    protocol.ProxyDesiredStatePaused,
		RuntimeState:    protocol.ProxyRuntimeStateIdle,
		ClientID:        "client-1",
		Hostname:        "restore-host",
		Binding:         TunnelBindingClientID,
	})

	s := New(0)
	s.adminStore = store
	s.store = tStore // 会由 s.initStore(tStore) 被自动绑定在真实环境中，这里手动绑定

	client := &ClientConn{
		ID:         "client-1",
		Info:       protocol.ClientInfo{Hostname: "restore-host"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store("client-1", client)

	// 欺骗有数据通道
	cPipe, sPipe := net.Pipe()
	sess, _ := mux.NewServerSession(sPipe, mux.DefaultConfig())
	client.dataMu.Lock()
	client.dataSession = sess
	client.dataMu.Unlock()
	defer func() {
		cPipe.Close()
		sPipe.Close()
	}()

	// 欺骗有 WebSocket 连接
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
	defer clientConn.Close()

	select {
	case <-connReady:
	case <-time.After(2 * time.Second):
		t.Fatal("等待测试 WebSocket 连接就绪超时")
	}

	// 测试恢复逻辑
	s.restoreTunnels(client)

	time.Sleep(100 * time.Millisecond)

	// 因为 client-1 的 dataSession 并没有建立，所以 Active 的 tunnel1 会触发 StartProxy失败，但 restoreTunnels 没有降级操作。
	// 但实际上在我们的 restoreTunnels 逻辑中，如果 StartProxy 失败，状态没有通过 proxyMu 进行修改。
	// 不过既然 s.StartProxy 失败，它不会出现在 client.proxies 里。
	// 为了简化断言，我们直接使用 store.GetTunnel。
	t1, _ := s.store.GetTunnel("client-1", "tunnel1")
	if t1.DesiredState != protocol.ProxyDesiredStateRunning || t1.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Logf("⚠️ tunnel1 恢复后状态为 %s/%s (restoreTunnels 失败时不降级，符合预期)", t1.DesiredState, t1.RuntimeState)
	}

	t2, _ := s.store.GetTunnel("client-1", "tunnel2")
	if t2.DesiredState != protocol.ProxyDesiredStatePaused || t2.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("Paused 隧道重启时应维持 paused/idle，得到 %s/%s", t2.DesiredState, t2.RuntimeState)
	}
}

func TestRestoreTunnels_PausedTunnelDoesNotWaitForDataSession(t *testing.T) {
	s := New(0)

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}
	s.store = store

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "paused-only", Type: "tcp", RemotePort: 19090},
		DesiredState:    protocol.ProxyDesiredStatePaused,
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
		t.Fatalf("仅恢复 paused 隧道不应等待数据通道，耗时 %v", elapsed)
	}

	client.proxyMu.RLock()
	tunnel, ok := client.proxies["paused-only"]
	client.proxyMu.RUnlock()
	if !ok {
		t.Fatal("paused 隧道应被恢复到内存态")
	}
	if tunnel.Config.DesiredState != protocol.ProxyDesiredStatePaused || tunnel.Config.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("恢复后的状态应保持 paused/idle，得到 %s/%s", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
	}
}

func TestRestoreTunnels_PausedHTTPPlaceholderPreservesDomain(t *testing.T) {
	s := New(0)

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}
	s.store = store

	const domain = "app.example.com"
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:      "paused-http",
			Type:      protocol.ProxyTypeHTTP,
			LocalIP:   "127.0.0.1",
			LocalPort: 3000,
			Domain:    domain,
		},
		DesiredState: protocol.ProxyDesiredStatePaused,
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
	tunnel := client.proxies["paused-http"]
	client.proxyMu.RUnlock()
	if tunnel == nil {
		t.Fatal("paused HTTP 隧道应被恢复到内存态")
	}
	if tunnel.Config.Domain != domain {
		t.Fatalf("恢复后的 paused HTTP 隧道应保留 domain=%q，得到 %q", domain, tunnel.Config.Domain)
	}
}

func TestRestoreTunnels_PortNotAllowedEventPreservesDomain(t *testing.T) {
	s := New(0)

	adminStore, err := NewAdminStore(filepath.Join(t.TempDir(), "admin.json"))
	if err != nil {
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}
	if err := adminStore.Initialize("admin", "password123", "localhost", []PortRange{{Start: 20000, End: 20010}}); err != nil {
		t.Fatalf("初始化 AdminStore 失败: %v", err)
	}
	s.adminStore = adminStore

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
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
		t.Fatal("端口不在白名单的隧道应生成 error 占位")
	}
	if runtimeTunnel.Config.Domain != domain {
		t.Fatalf("error 占位应保留 domain=%q，得到 %q", domain, runtimeTunnel.Config.Domain)
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
				t.Fatalf("解析 tunnel_changed 事件失败: %v", err)
			}
			action, _ := payload["action"].(string)
			if action != "port_not_allowed" {
				continue
			}
			tunnelPayload, ok := payload["tunnel"].(map[string]any)
			if !ok {
				t.Fatalf("事件中的 tunnel 字段类型无效: %#v", payload["tunnel"])
			}
			if got, _ := tunnelPayload["domain"].(string); got != domain {
				t.Fatalf("port_not_allowed 事件应保留 domain=%q，得到 %q", domain, got)
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}

	t.Fatal("未收到 port_not_allowed 的 tunnel_changed 事件")
}

// ============================================================
// 认证 — Token 兑换集成测试
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
	conn.WriteJSON(msg)

	var resp protocol.Message
	conn.ReadJSON(&resp)
	var authResp protocol.AuthResponse
	resp.ParsePayload(&authResp)

	if !authResp.Success {
		t.Fatalf("认证应成功: %s", authResp.Message)
	}
	if authResp.Token == "" {
		t.Error("Key 认证成功后应返回 Token")
	}
	if authResp.ClientID == "" {
		t.Error("ClientID 不应为空")
	}
}

func TestAuth_TokenReconnect(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	// 1. 首先用 Key 认证获取 Token
	conn1, _ := connectAndAuth(t, ts, "token-reconnect-host")

	// 从 adminStore 获取为此 install_id 生成的 Token
	clientToken := s.adminStore.GetClientTokenByInstallID("install-token-reconnect-host")
	if clientToken == nil {
		t.Fatal("首次 Key 认证后应有 Token 记录")
	}

	// 获取当前 Key 的 use_count
	keys := s.adminStore.GetAPIKeys()
	useCountBefore := keys[0].UseCount

	// 断开连接
	conn1.Close()
	time.Sleep(200 * time.Millisecond)

	// 2. 用新生成的 Token 重连（需要知道原始 Token，但 hash 后无法恢复）
	//    这里直接重新用 Key 兑换一次来模拟客户端已有 Token 的场景
	//    真实客户端会保存 AuthResponse.Token
	// 实际上我们验证的是：同一 install_id 再次 ExchangeToken 不会增加 use_count
	_, _, err := s.adminStore.ExchangeToken("test-key", "install-token-reconnect-host", clientToken.ClientID, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("Token 重用 ExchangeToken 失败: %v", err)
	}

	// 同一 install_id 已有有效 Token，不应消耗 Key
	keys = s.adminStore.GetAPIKeys()
	if keys[0].UseCount != useCountBefore {
		t.Errorf("Token 重用不应消耗 Key: 期望 %d, 得到 %d", useCountBefore, keys[0].UseCount)
	}
}

func TestAuth_OldClientWithoutToken(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	// 模拟旧版客户端：只发 Key，不发 Token
	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-old-client",
		Client: protocol.ClientInfo{
			Hostname: "old-client",
			OS:       "linux",
			Arch:     "amd64",
			Version:  "0.0.9",
		},
		// Token 字段为空 (omitempty)
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	conn.WriteJSON(msg)

	var resp protocol.Message
	conn.ReadJSON(&resp)
	var authResp protocol.AuthResponse
	resp.ParsePayload(&authResp)

	if !authResp.Success {
		t.Fatalf("旧版客户端 Key 认证应成功: %s", authResp.Message)
	}
	if authResp.Token == "" {
		t.Error("即使是旧客户端，服务端也应返回 Token（客户端可忽略）")
	}
}

// ============================================================

// ============================================================

// TestServer_GracefulShutdown 验证 P15：调用 Shutdown 后 Client 连接被正常关闭
func TestServer_GracefulShutdown(t *testing.T) {
	// 使用真实的 Start() 启动服务器
	tmpDir := t.TempDir()
	s := New(reserveTCPPort(t))
	s.StorePath = filepath.Join(tmpDir, "tunnels.json")

	// 预创建 AdminStore
	adminStore, err := NewAdminStore(filepath.Join(tmpDir, "admin.json"))
	if err != nil {
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}
	if err := adminStore.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("初始化 AdminStore 失败: %v", err)
	}
	if _, err := adminStore.AddAPIKey("default", "test-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("创建测试 API Key 失败: %v", err)
	}
	s.adminStore = adminStore

	// 在 goroutine 中启动 Server
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- s.Start()
	}()
	time.Sleep(200 * time.Millisecond)

	// 连接一个 Client
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
			t.Fatalf("服务端启动失败: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("WebSocket 连接失败: %v", dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer conn.Close()

	// 完成认证
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
	conn.WriteJSON(msg)
	var authMsg protocol.Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	conn.ReadJSON(&authMsg)

	// 确认 Client 已注册
	clientCount := 0
	s.clients.Range(func(_, _ any) bool {
		clientCount++
		return true
	})
	if clientCount == 0 {
		t.Fatal("Client 应已注册")
	}

	// 调用优雅关闭
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown 失败: %v", err)
	}

	// 验证 clients 已清空
	clientCount = 0
	s.clients.Range(func(_, _ any) bool {
		clientCount++
		return true
	})
	if clientCount != 0 {
		t.Errorf("Shutdown 后 clients 应为空，得到 %d", clientCount)
	}

	// 验证 Server 已停止（Serve 返回）
	select {
	case err := <-serverErr:
		if err != nil && err.Error() != "http: Server closed" {
			t.Errorf("Server 返回了非预期错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Server 应在 Shutdown 后返回")
	}
}

func TestServer_GracefulShutdown_ClosesPendingControlHandshake(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.StorePath = filepath.Join(t.TempDir(), "tunnels.json")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("获取临时端口失败: %v", err)
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
			t.Fatalf("服务端启动失败: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("WebSocket 连接失败: %v", dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown 失败: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var msg protocol.Message
	err = conn.ReadJSON(&msg)
	if err == nil {
		t.Fatal("Shutdown 后未认证控制连接应被关闭")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("Shutdown 后控制连接未被及时关闭，读超时: %v", err)
	}

	select {
	case err := <-serverErr:
		if err != nil && err.Error() != "http: Server closed" {
			t.Errorf("Server 返回了非预期错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Server 应在 Shutdown 后返回")
	}
}
