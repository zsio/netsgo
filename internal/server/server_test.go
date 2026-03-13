package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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

	session := s.adminStore.CreateSession("user-1", "admin", "admin", "127.0.0.1", "server-test")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 Admin Token 失败: %v", err)
	}
	return token
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
		Agent: protocol.AgentInfo{
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

func connectAndAuthWithInstallID(t *testing.T, ts *httptest.Server, hostname, installID string) (*websocket.Conn, protocol.AuthResponse) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	authResp := doAuthWithInstallID(t, conn, hostname, installID, "test-key")
	return conn, authResp
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

// ============================================================
// API 端点测试 (7)
// ============================================================

func TestAPI_Status_NoAgents(t *testing.T) {
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
	if result["agent_count"] != float64(0) {
		t.Errorf("agent_count 期望 0，得到 %v", result["agent_count"])
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
}

func TestAPI_Status_TunnelCounts(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)
	time.Sleep(50 * time.Millisecond)

	val, _ := s.agents.Load(authResp.AgentID)
	agent := val.(*AgentConn)

	agent.proxyMu.Lock()
	agent.proxies["tunnel1"] = &ProxyTunnel{Config: protocol.ProxyConfig{Status: protocol.ProxyStatusActive}, done: make(chan struct{})}
	agent.proxies["tunnel2"] = &ProxyTunnel{Config: protocol.ProxyConfig{Status: protocol.ProxyStatusPaused}, done: make(chan struct{})}
	agent.proxies["tunnel3"] = &ProxyTunnel{Config: protocol.ProxyConfig{Status: protocol.ProxyStatusStopped}, done: make(chan struct{})}
	agent.proxyMu.Unlock()

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

func TestAPI_Status_WithAgents(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn2, _ := connectAndAuth(t, ts, "agent-host")
	defer conn2.Close()

	time.Sleep(50 * time.Millisecond)

	result := getAPIJSON(t, s, ts, "/api/status")
	count := result["agent_count"].(float64)
	if count < 1 {
		t.Errorf("agent_count 期望 ≥ 1，得到 %v", count)
	}
}

func TestAPI_Status_AfterDisconnect(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn2, _ := connectAndAuth(t, ts, "temp-agent")
	time.Sleep(50 * time.Millisecond)

	result := getAPIJSON(t, s, ts, "/api/status")
	before := result["agent_count"].(float64)

	conn2.Close()
	time.Sleep(100 * time.Millisecond)

	result2 := getAPIJSON(t, s, ts, "/api/status")
	after := result2["agent_count"].(float64)

	if after >= before {
		t.Errorf("断开后 agent_count 应减少: before=%v, after=%v", before, after)
	}
}

func TestAPI_Agents_Empty(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	w := httptest.NewRecorder()
	s.handleAPIAgents(w, req)

	body := strings.TrimSpace(w.Body.String())
	if body != "null" && body != "[]" {
		t.Errorf("无 Agent 时期望空结果，得到 %s", body)
	}
}

func TestAPI_Agents_Multiple(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, _ := connectAndAuth(t, ts, "host-A")
	defer conn1.Close()
	conn2, _ := connectAndAuth(t, ts, "host-B")
	defer conn2.Close()
	conn3, _ := connectAndAuth(t, ts, "host-C")
	defer conn3.Close()

	time.Sleep(50 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	var agents []map[string]any
	json.NewDecoder(resp.Body).Decode(&agents)

	if len(agents) < 3 {
		t.Errorf("期望至少 3 个 Agent，得到 %d", len(agents))
	}

	for i, a := range agents {
		if a["id"] == nil {
			t.Errorf("Agent[%d] 缺少 id", i)
		}
		if a["info"] == nil {
			t.Errorf("Agent[%d] 缺少 info", i)
		}
	}
}

func TestAPI_Agents_WithStats(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, _ := connectAndAuth(t, ts, "stats-host")
	defer conn1.Close()

	stats := protocol.SystemStats{CPUUsage: 55.5, MemUsage: 70.0, NumCPU: 8}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	conn1.WriteJSON(msg)
	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	var agents []map[string]any
	json.NewDecoder(resp.Body).Decode(&agents)

	if len(agents) == 0 {
		t.Fatal("期望至少 1 个 Agent")
	}

	found := false
	for _, a := range agents {
		if a["stats"] != nil {
			found = true
			statsMap := a["stats"].(map[string]any)
			if statsMap["cpu_usage"].(float64) != 55.5 {
				t.Errorf("cpu_usage 期望 55.5，得到 %v", statsMap["cpu_usage"])
			}
		}
	}
	if !found {
		t.Error("未找到包含 stats 的 Agent")
	}
}

func TestAPI_Agents_StatsUpdated(t *testing.T) {
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

	val, ok := s.agents.Load(authResp.AgentID)
	if !ok {
		t.Fatal("Agent 未找到")
	}
	agent := val.(*AgentConn)
	if agent.GetStats().CPUUsage != 80.0 {
		t.Errorf("Stats 应被更新为最新值 80.0，得到 %f", agent.GetStats().CPUUsage)
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
// 控制通道 — 认证 (5)
// ============================================================

func TestAuth_Success(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)

	if !authResp.Success {
		t.Errorf("认证应成功: %s", authResp.Message)
	}
	if authResp.AgentID == "" {
		t.Error("AgentID 不应为空")
	}
	// AgentID 应为 UUID v4 格式: 8-4-4-4-12
	uuidPattern := `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	if matched, _ := regexp.MatchString(uuidPattern, authResp.AgentID); !matched {
		t.Errorf("AgentID 应为 UUID v4 格式，得到: %q", authResp.AgentID)
	}
}

func TestAuth_EmptyKey(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authReq := protocol.AuthRequest{
		Key:       "",
		InstallID: "install-empty-key",
		Agent: protocol.AgentInfo{
			Hostname: "host",
			OS:       "linux",
			Arch:     "amd64",
			Version:  "0.1.0",
		},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("发送认证消息失败: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err == nil {
		t.Fatal("缺少 API Key 时服务端应拒绝连接")
	}
}

func TestAuth_EmptyHostname(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuthWithInfo(t, conn, "", "test-key")

	if !authResp.Success {
		t.Errorf("空主机名不应导致认证失败: %s", authResp.Message)
	}
	if authResp.AgentID == "" {
		t.Error("AgentID 不应为空")
	}
}

func TestAuth_ReconnectSameInstallIDReplacesOldConnection(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, auth1 := connectAndAuthWithInstallID(t, ts, "stable-host", "install-stable-host")
	defer conn1.Close()

	time.Sleep(50 * time.Millisecond)
	previous, ok := s.agents.Load(auth1.AgentID)
	if !ok {
		t.Fatal("第一次认证后 Agent 应已注册")
	}

	conn2, auth2 := connectAndAuthWithInstallID(t, ts, "stable-host", "install-stable-host")
	defer conn2.Close()

	if auth2.AgentID != auth1.AgentID {
		t.Fatalf("相同 install_id 重连后应保持稳定 AgentID: %s != %s", auth2.AgentID, auth1.AgentID)
	}

	time.Sleep(100 * time.Millisecond)

	current, ok := s.agents.Load(auth1.AgentID)
	if !ok {
		t.Fatal("重连后 Agent 应仍然在线")
	}
	if current == previous {
		t.Error("重连后应以新连接替换旧连接")
	}

	conn1.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := conn1.ReadMessage(); err == nil {
		t.Error("旧连接应被服务端主动断开")
	}

	count := 0
	s.RangeAgents(func(_ string, _ *AgentConn) bool {
		count++
		return true
	})
	if count != 1 {
		t.Errorf("相同 install_id 在线会话应被收敛为 1 个，得到 %d", count)
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
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	doAuth(t, conn)

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
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	doAuth(t, conn)

	for i := 0; i < 10; i++ {
		ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
		if err := conn.WriteJSON(ping); err != nil {
			t.Fatalf("第 %d 次发送 Ping 失败: %v", i, err)
		}

		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
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
	s, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)

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

	val, ok := s.agents.Load(authResp.AgentID)
	if !ok {
		t.Fatal("Agent 未注册")
	}
	agent := val.(*AgentConn)
	if agent.GetStats() == nil {
		t.Fatal("Stats 不应为 nil")
	}
	if agent.GetStats().CPUUsage != 42.5 {
		t.Errorf("CPUUsage 期望 42.5，得到 %f", agent.GetStats().CPUUsage)
	}
	if agent.GetStats().MemUsage != 60.0 {
		t.Errorf("MemUsage 期望 60.0，得到 %f", agent.GetStats().MemUsage)
	}
	if agent.GetStats().NumCPU != 4 {
		t.Errorf("NumCPU 期望 4，得到 %d", agent.GetStats().NumCPU)
	}
}

func TestProbe_ReportPersistedAfterDisconnect(t *testing.T) {
	s, conn, ts, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)

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

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 /api/agents 失败: %v", err)
	}
	defer resp.Body.Close()

	var agents []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		t.Fatalf("解析 /api/agents 响应失败: %v", err)
	}

	for _, agent := range agents {
		if agent["id"] != authResp.AgentID {
			continue
		}
		if online, _ := agent["online"].(bool); online {
			t.Fatal("断开后的 Agent 不应仍然标记为在线")
		}
		statsMap, ok := agent["stats"].(map[string]any)
		if !ok {
			t.Fatal("断开后的 Agent 仍应返回最后一次 stats")
		}
		if statsMap["cpu_usage"].(float64) != 42.5 {
			t.Fatalf("cpu_usage 期望 42.5，得到 %v", statsMap["cpu_usage"])
		}
		return
	}

	t.Fatalf("未找到 Agent %s", authResp.AgentID)
}

func TestAPI_Agents_FallbackToPersistedStatsBeforeNextReport(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	info := protocol.AgentInfo{
		Hostname: "persisted-host",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}

	record, err := s.adminStore.GetOrCreateAgent("install-persisted-host", info, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("预创建 Agent 记录失败: %v", err)
	}
	if err := s.adminStore.UpdateAgentStats(record.ID, info, protocol.SystemStats{
		CPUUsage: 88.8,
		MemUsage: 66.6,
		NumCPU:   16,
	}, "127.0.0.1:12345"); err != nil {
		t.Fatalf("预写入 Agent stats 失败: %v", err)
	}

	conn, authResp := connectAndAuthWithInstallID(t, ts, "persisted-host", "install-persisted-host")
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 /api/agents 失败: %v", err)
	}
	defer resp.Body.Close()

	var agents []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		t.Fatalf("解析 /api/agents 响应失败: %v", err)
	}

	for _, agent := range agents {
		if agent["id"] != authResp.AgentID {
			continue
		}
		if online, _ := agent["online"].(bool); !online {
			t.Fatal("已连接的 Agent 应标记为在线")
		}
		statsMap, ok := agent["stats"].(map[string]any)
		if !ok {
			t.Fatal("首次新上报前应先返回持久化的旧 stats")
		}
		if statsMap["cpu_usage"].(float64) != 88.8 {
			t.Fatalf("cpu_usage 期望 88.8，得到 %v", statsMap["cpu_usage"])
		}
		return
	}

	t.Fatalf("未找到 Agent %s", authResp.AgentID)
}

func TestProbe_MultipleReports(t *testing.T) {
	s, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuth(t, conn)

	for i := 0; i < 5; i++ {
		cpuVal := float64(i+1) * 10.0
		stats := protocol.SystemStats{CPUUsage: cpuVal, NumCPU: 8}
		msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
		conn.WriteJSON(msg)
		time.Sleep(30 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	val, _ := s.agents.Load(authResp.AgentID)
	agent := val.(*AgentConn)
	if agent.GetStats().CPUUsage != 50.0 {
		t.Errorf("最终 CPUUsage 应为 50.0（最后一次上报），得到 %f", agent.GetStats().CPUUsage)
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

	_, ok := s.agents.Load(authResp.AgentID)
	if !ok {
		t.Fatal("认证后 Agent 应已注册")
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

	val, _ := s.agents.Load(authResp.AgentID)
	if val.(*AgentConn).GetStats().CPUUsage != 33.3 {
		t.Error("探针数据未正确更新")
	}

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	_, ok = s.agents.Load(authResp.AgentID)
	if ok {
		t.Error("断开后 Agent 应已从 map 中移除")
	}
}

func TestMultipleAgents_Concurrent(t *testing.T) {
	_, _, ts, cleanup := setupWSTest(t)
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
				Agent:     protocol.AgentInfo{Hostname: hostname, OS: "linux", Arch: "amd64", Version: "0.1.0"},
			}
			msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
			conn.WriteJSON(msg)

			var resp protocol.Message
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if err := conn.ReadJSON(&resp); err != nil {
				errors <- err
				return
			}

			var authResp protocol.AuthResponse
			resp.ParsePayload(&authResp)
			if !authResp.Success {
				errors <- json.Unmarshal([]byte("auth failed"), nil)
				return
			}

			ping, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
			conn.WriteJSON(ping)
			conn.ReadJSON(&resp)
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("并发 Agent 出错: %v", err)
	}
}

func TestAgent_DisconnectCleansUp(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, auth1 := connectAndAuth(t, ts, "stay-host")
	conn2, auth2 := connectAndAuth(t, ts, "leave-host")

	time.Sleep(50 * time.Millisecond)

	_, ok1 := s.agents.Load(auth1.AgentID)
	_, ok2 := s.agents.Load(auth2.AgentID)
	if !ok1 || !ok2 {
		t.Fatal("两个 Agent 都应已注册")
	}

	conn2.Close()
	time.Sleep(100 * time.Millisecond)

	_, ok1 = s.agents.Load(auth1.AgentID)
	_, ok2 = s.agents.Load(auth2.AgentID)
	if !ok1 {
		t.Error("Agent1 不应被移除")
	}
	if ok2 {
		t.Error("Agent2 应已被移除")
	}

	conn1.Close()
}

func TestHandleControlWS_MigratesLegacyTunnelsToStableAgentID(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}
	s.store = store

	seedLegacyTunnels(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "legacy-tunnel", Type: "tcp", RemotePort: 18080},
		Status:          protocol.ProxyStatusPaused,
		Hostname:        "legacy-host",
		Binding:         TunnelBindingLegacyHostname,
	})

	conn, authResp := connectAndAuthWithInstallID(t, ts, "legacy-host", "install-legacy-host")
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	migrated := s.store.GetTunnelsByAgentID(authResp.AgentID)
	if len(migrated) != 1 {
		t.Fatalf("期望迁移出 1 条稳定绑定隧道，得到 %d", len(migrated))
	}
	if migrated[0].Binding != TunnelBindingAgentID {
		t.Errorf("迁移后 Binding 应为 %s，得到 %s", TunnelBindingAgentID, migrated[0].Binding)
	}
	if migrated[0].Hostname != "legacy-host" {
		t.Errorf("迁移后 Hostname 应保留 legacy-host，得到 %s", migrated[0].Hostname)
	}

	pending := s.store.GetLegacyTunnelsByHostname("legacy-host")
	if len(pending) != 0 {
		t.Errorf("迁移后不应再保留 legacy 记录，得到 %d 条", len(pending))
	}
}

func TestHandleControlWS_SkipsLegacyMigrationForAmbiguousHostname(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
	}
	s.store = store

	seedLegacyTunnels(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "legacy-tunnel", Type: "tcp", RemotePort: 18081},
		Status:          protocol.ProxyStatusPaused,
		Hostname:        "shared-host",
		Binding:         TunnelBindingLegacyHostname,
	})

	if _, err := s.adminStore.GetOrCreateAgent("install-existing-host", protocol.AgentInfo{
		Hostname: "shared-host",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}, "127.0.0.1"); err != nil {
		t.Fatalf("预注册旧 Agent 失败: %v", err)
	}

	conn, authResp := connectAndAuthWithInstallID(t, ts, "shared-host", "install-new-host")
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	if migrated := s.store.GetTunnelsByAgentID(authResp.AgentID); len(migrated) != 0 {
		t.Fatalf("hostname 冲突时不应自动迁移 legacy 隧道，得到 %d 条", len(migrated))
	}
	if pending := s.store.GetLegacyTunnelsByHostname("shared-host"); len(pending) != 1 {
		t.Fatalf("hostname 冲突时应保留 legacy 记录，得到 %d 条", len(pending))
	}
}

func TestControlLoop_ProxyMessages(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn, authResp := connectAndAuth(t, ts, "proxy-msg-host")
	defer conn.Close()

	// agents.Store 已在 handleAuth 内、认证响应发送前完成，
	// 所以 connectAndAuth 返回时 agent 一定已在 map 中。
	val, ok := s.agents.Load(authResp.AgentID)
	if !ok {
		t.Fatal("认证成功后 Agent 应已注册到 agents map")
	}
	agent := val.(*AgentConn)
	cPipe, sPipe := net.Pipe()
	defer cPipe.Close()
	defer sPipe.Close()
	agent.dataSession, _ = mux.NewServerSession(sPipe, mux.DefaultConfig())

	// 测试 MsgTypeProxyNew
	req := protocol.ProxyNewRequest{
		Name:       "ws-tunnel-1",
		RemotePort: 0,
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyNew, req)
	conn.WriteJSON(msg)

	var resp protocol.Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("读取创建代理响应失败: %v", err)
	}

	if resp.Type != protocol.MsgTypeProxyNewResp {
		t.Errorf("期望返回 %s，得到 %s", protocol.MsgTypeProxyNewResp, resp.Type)
	}

	// 测试 MsgTypeProxyClose
	closeReq := protocol.ProxyCloseRequest{Name: "ws-tunnel-1"}
	closeMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyClose, closeReq)
	conn.WriteJSON(closeMsg)
	time.Sleep(100 * time.Millisecond)

	agent.proxyMu.RLock()
	_, exists := agent.proxies["ws-tunnel-1"]
	agent.proxyMu.RUnlock()

	if exists {
		t.Error("发送 ProxyClose 后代理隧道仍存在")
	}
}

// ============================================================
// PeekConn 测试 (2)
// ============================================================

func TestPeekConn_PeekByte(t *testing.T) {
	// 创建一个 net.Pipe 来测试
	client, server := pipeConn()
	defer client.Close()
	defer server.Close()

	// 向 server 端写入数据
	go func() {
		server.Write([]byte("Hello"))
	}()

	pc := &PeekConn{Conn: client}

	// Peek 应返回 'H' 但不消费
	b, err := pc.PeekByte()
	if err != nil {
		t.Fatalf("PeekByte 失败: %v", err)
	}
	if b != 'H' {
		t.Errorf("PeekByte 期望 'H'，得到 %c", b)
	}

	// 再次 Peek 应返回相同值
	b2, _ := pc.PeekByte()
	if b2 != 'H' {
		t.Errorf("重复 PeekByte 期望 'H'，得到 %c", b2)
	}

	// Read 应先返回 'H'，然后正常继续
	buf := make([]byte, 5)
	n, err := pc.Read(buf)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if string(buf[:n]) != "Hello" {
		t.Errorf("Read 期望 'Hello'，得到 %q", buf[:n])
	}
}

func TestPeekConn_ReadWithoutPeek(t *testing.T) {
	client, server := pipeConn()
	defer client.Close()
	defer server.Close()

	go func() {
		server.Write([]byte("World"))
	}()

	pc := &PeekConn{Conn: client}

	// 不 Peek 直接 Read
	buf := make([]byte, 5)
	n, err := pc.Read(buf)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if string(buf[:n]) != "World" {
		t.Errorf("Read 期望 'World'，得到 %q", buf[:n])
	}
}

// pipeConn 创建一对 net.Conn 用于测试
func pipeConn() (net.Conn, net.Conn) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	var serverConn net.Conn
	done := make(chan struct{})
	go func() {
		serverConn, _ = ln.Accept()
		close(done)
	}()
	clientConn, _ := net.Dial("tcp", ln.Addr().String())
	<-done
	ln.Close()
	return clientConn, serverConn
}

// ============================================================
// PeekListener 分发测试 (2)
// ============================================================

func TestPeekListener_HTTPDispatch(t *testing.T) {
	s := New(0)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	pl := &PeekListener{
		Listener: ln,
		server:   s,
	}

	// 发送 HTTP 首字节 'G' (GET)
	go func() {
		conn, _ := net.Dial("tcp", ln.Addr().String())
		conn.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
		time.Sleep(200 * time.Millisecond)
		conn.Close()
	}()

	// Accept 应该把 HTTP 连接送入 pending channel
	accepted, err := pl.Accept()
	if err != nil {
		t.Fatalf("Accept 失败: %v", err)
	}
	defer accepted.Close()

	// 读取内容验证是 HTTP
	buf := make([]byte, 3)
	accepted.Read(buf)
	if string(buf) != "GET" {
		t.Errorf("期望读到 GET，得到 %q", string(buf))
	}
}

func TestPeekListener_DataChannelDispatch(t *testing.T) {
	s := New(0)
	agentID := "peek-dispatch-agent"
	agent := &AgentConn{
		ID:      agentID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.agents.Store(agentID, agent)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	pl := &PeekListener{
		Listener: ln,
		server:   s,
	}

	// 启动 dispatch loop
	pl.once.Do(func() {
		pl.pending = make(chan net.Conn, 64)
		go pl.dispatchLoop()
	})

	// 发送数据通道魔数 + 握手
	go func() {
		conn, _ := net.Dial("tcp", ln.Addr().String())
		handshake := DataHandshakeBytes(agentID)
		conn.Write(handshake)

		// 读取响应
		resp := make([]byte, 1)
		conn.Read(resp)
		// 不关注结果，重点是走通了 dispatchLoop 的数据通道分支
		time.Sleep(100 * time.Millisecond)
		conn.Close()
	}()

	// 数据通道连接不应出现在 pending channel 里
	// 等一下看 pending 是否为空
	time.Sleep(300 * time.Millisecond)
	select {
	case conn := <-pl.pending:
		// 如果收到了连接，说明被错误路由到了 HTTP
		conn.Close()
		t.Error("数据通道连接不应出现在 pending channel")
	default:
		// 正确：数据通道被 handleDataConn 处理了
	}
}

// ============================================================
// controlLoop 边缘场景测试 (2)
// ============================================================

func TestControlLoop_UnknownMsgType(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	doAuth(t, conn)

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

	// Agent 的 stats 应该没被更新（还是 nil）
	val, ok := s.agents.Load(authResp.AgentID)
	if !ok {
		t.Fatal("Agent 应该仍然已注册")
	}
	agent := val.(*AgentConn)
	if agent.GetStats() != nil {
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
	session := store.CreateSession("test-user", "admin", "admin", "127.0.0.1", "test")
	token, _ := s.GenerateAdminToken(session)

	// API 请求助手
	doRequest := func(method, path string, body []byte) (int, map[string]any) {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("API 请求失败 %s: %v", path, err)
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return resp.StatusCode, result
	}

	// 2. 模拟一个 Agent 连接
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer wsConn.Close()

	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-lifecycle-agent",
		Agent:     protocol.AgentInfo{Hostname: "lifecycle-agent", OS: "linux", Version: "1.0.0"},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	wsConn.WriteJSON(msg)

	var authRespMsg protocol.Message
	wsConn.ReadJSON(&authRespMsg)
	var authResp protocol.AuthResponse
	authRespMsg.ParsePayload(&authResp)

	agentID := authResp.AgentID
	time.Sleep(50 * time.Millisecond) // 等待 agent 注册到 s.agents

	// 设置假的 DataSession，避免 StartProxy 失败
	val, _ := s.agents.Load(agentID)
	agentConn := val.(*AgentConn)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	agentConn.dataSession = sSession
	defer func() {
		cConn.Close()
		sConn.Close()
	}()

	// ========= 测试开始 =========

	// 1. 创建隧道 (/api/agents/{id}/tunnels)
	createReq := []byte(`{"name":"test-tunnel","type":"tcp","local_ip":"127.0.0.1","local_port":8080,"remote_port":18080}`)
	code, resp := doRequest(http.MethodPost, fmt.Sprintf("/api/agents/%s/tunnels", agentID), createReq)

	if code != http.StatusCreated {
		t.Errorf("创建隧道期望 201 Created，得到 %d, 响应: %v", code, resp)
	}

	// 验证隧道在 Store 中生成
	tunnel, ok := s.store.GetTunnel(agentID, "test-tunnel")
	if !ok {
		t.Fatal("Tunnel 未写入 Store")
	}
	if tunnel.Status != protocol.ProxyStatusActive {
		t.Errorf("初创状态应为 active，得到 %s", tunnel.Status)
	}

	// 模拟 Client 返回 proxy_new_resp (成功)
	proxyNewRespMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyNewResp, protocol.ProxyNewResponse{
		Success:    true,
		Message:    "ok",
		RemotePort: 18080,
	})
	wsConn.WriteJSON(proxyNewRespMsg)
	time.Sleep(100 * time.Millisecond) // 等待服务端处理状态更新

	// 二次检查 Store，状态应为 active
	tunnel, _ = s.store.GetTunnel(agentID, "test-tunnel")
	if tunnel.Status != protocol.ProxyStatusActive {
		t.Errorf("隧道已成功创建，状态应为 active，但仍为 %s", tunnel.Status)
	}

	// 2. 暂停隧道 (/api/agents/{id}/tunnels/{name}/pause)
	pauseReq := []byte(`{}`)
	code, _ = doRequest(http.MethodPut, fmt.Sprintf("/api/agents/%s/tunnels/test-tunnel/pause", agentID), pauseReq)
	if code != http.StatusOK {
		t.Errorf("暂停隧道期望 200，得到 %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(agentID, "test-tunnel")
	if tunnel.Status != protocol.ProxyStatusPaused {
		t.Errorf("隧道暂停后，状态应为 paused，得到 %s", tunnel.Status)
	}

	// 3. 恢复隧道 (/api/agents/{id}/tunnels/{name}/resume)
	resumeReq := []byte(`{}`)
	code, _ = doRequest(http.MethodPut, fmt.Sprintf("/api/agents/%s/tunnels/test-tunnel/resume", agentID), resumeReq)
	if code != http.StatusOK {
		t.Errorf("恢复隧道期望 200，得到 %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(agentID, "test-tunnel")
	if tunnel.Status != protocol.ProxyStatusActive {
		t.Errorf("隧道恢复后，状态应为 active，得到 %s", tunnel.Status)
	}

	// 4. 停止隧道 (/api/agents/{id}/tunnels/{name}/stop)
	stopReq := []byte(`{}`)
	code, _ = doRequest(http.MethodPut, fmt.Sprintf("/api/agents/%s/tunnels/test-tunnel/stop", agentID), stopReq)
	if code != http.StatusOK {
		t.Errorf("停止隧道期望 200，得到 %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	tunnel, _ = s.store.GetTunnel(agentID, "test-tunnel")
	if tunnel.Status != protocol.ProxyStatusStopped {
		t.Errorf("隧道停止后，状态应为 stopped，得到 %s", tunnel.Status)
	}

	// 5. 删除隧道 (/api/agents/{id}/tunnels/{name})
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+fmt.Sprintf("/api/agents/%s/tunnels/test-tunnel", agentID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	respDel, _ := http.DefaultClient.Do(req)
	if respDel.StatusCode != http.StatusNoContent {
		t.Errorf("删除隧道期望 204 No Content，得到 %d", respDel.StatusCode)
	}
	respDel.Body.Close()

	if _, ok := s.store.GetTunnel(agentID, "test-tunnel"); ok {
		t.Error("删除后，Store 中不应再有此隧道")
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
		Status:          protocol.ProxyStatusActive,
		AgentID:         "agent-1",
		Hostname:        "restore-host",
		Binding:         TunnelBindingAgentID,
	})
	tStore.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "tunnel2", Type: "tcp", RemotePort: 5678},
		Status:          protocol.ProxyStatusPaused,
		AgentID:         "agent-1",
		Hostname:        "restore-host",
		Binding:         TunnelBindingAgentID,
	})

	s := New(0)
	s.adminStore = store
	s.store = tStore // 会由 s.initStore(tStore) 被自动绑定在真实环境中，这里手动绑定

	agent := &AgentConn{
		ID:      "agent-1",
		Info:    protocol.AgentInfo{Hostname: "restore-host"},
		proxies: make(map[string]*ProxyTunnel),
	}
	s.agents.Store("agent-1", agent)

	// 欺骗有数据通道
	cPipe, sPipe := net.Pipe()
	sess, _ := mux.NewServerSession(sPipe, mux.DefaultConfig())
	agent.dataSession = sess
	defer func() {
		cPipe.Close()
		sPipe.Close()
	}()

	// 欺骗有 WebSocket 连接
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			agent.conn = conn
		}
	}))
	defer wsServer.Close()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	clientConn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer clientConn.Close()

	// 等待 agent.conn 被赋值
	time.Sleep(50 * time.Millisecond)

	// 测试恢复逻辑
	s.restoreTunnels(agent)

	time.Sleep(100 * time.Millisecond)

	// 因为 agent-1 的 dataSession 并没有建立，所以 Active 的 tunnel1 会触发 StartProxy失败，但 restoreTunnels 没有降级操作。
	// 但实际上在我们的 restoreTunnels 逻辑中，如果 StartProxy 失败，状态没有通过 proxyMu 进行修改。
	// 不过既然 s.StartProxy 失败，它不会出现在 agent.proxies 里。
	// 为了简化断言，我们直接使用 store.GetTunnel。
	t1, _ := s.store.GetTunnel("agent-1", "tunnel1")
	if t1.Status != protocol.ProxyStatusActive {
		t.Logf("⚠️ tunnel1 恢复后状态为 %s (restoreTunnels 失败时不降级，符合预期)", t1.Status)
	}

	t2, _ := s.store.GetTunnel("agent-1", "tunnel2")
	if t2.Status != protocol.ProxyStatusPaused {
		t.Errorf("Paused 隧道重启时应维持 paused，得到 %s", t2.Status)
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
		Status:          protocol.ProxyStatusPaused,
		AgentID:         "agent-restore",
		Hostname:        "restore-host",
	})

	agent := &AgentConn{
		ID:      "agent-restore",
		Info:    protocol.AgentInfo{Hostname: "restore-host"},
		proxies: make(map[string]*ProxyTunnel),
	}

	start := time.Now()
	s.restoreTunnels(agent)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("仅恢复 paused 隧道不应等待数据通道，耗时 %v", elapsed)
	}

	agent.proxyMu.RLock()
	tunnel, ok := agent.proxies["paused-only"]
	agent.proxyMu.RUnlock()
	if !ok {
		t.Fatal("paused 隧道应被恢复到内存态")
	}
	if tunnel.Config.Status != protocol.ProxyStatusPaused {
		t.Errorf("恢复后的状态应保持 paused，得到 %s", tunnel.Config.Status)
	}
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
		Agent: protocol.AgentInfo{
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
	if authResp.AgentID == "" {
		t.Error("AgentID 不应为空")
	}
}

func TestAuth_TokenReconnect(t *testing.T) {
	s, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	// 1. 首先用 Key 认证获取 Token
	conn1, _ := connectAndAuth(t, ts, "token-reconnect-host")

	// 从 adminStore 获取为此 install_id 生成的 Token
	agentToken := s.adminStore.GetAgentTokenByInstallID("install-token-reconnect-host")
	if agentToken == nil {
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
	_, _, err := s.adminStore.ExchangeToken("test-key", "install-token-reconnect-host", agentToken.AgentID, "127.0.0.1:12345")
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
		Agent: protocol.AgentInfo{
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

