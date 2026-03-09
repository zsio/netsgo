package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

// ============================================================
// 测试辅助函数
// ============================================================

// setupWSTest 创建测试 Server + WebSocket 连接
func setupWSTest(t *testing.T) (*Server, *websocket.Conn, *httptest.Server, func()) {
	t.Helper()
	s := New(0)
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
	ts := httptest.NewServer(s.newHTTPMux())
	return s, ts, ts.Close
}

// doAuth 完成认证，返回响应
func doAuth(t *testing.T, conn *websocket.Conn) protocol.AuthResponse {
	return doAuthWithInfo(t, conn, "test-host", "test-token")
}

// doAuthWithInfo 用指定信息完成认证
func doAuthWithInfo(t *testing.T, conn *websocket.Conn, hostname, token string) protocol.AuthResponse {
	t.Helper()
	authReq := protocol.AuthRequest{
		Token: token,
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
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	authResp := doAuthWithInfo(t, conn, hostname, "token")
	return conn, authResp
}

// getAPIJSON 发起 HTTP GET 请求并解析 JSON
func getAPIJSON(t *testing.T, ts *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
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

func TestAPI_Status_WithAgents(t *testing.T) {
	_, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn2, _ := connectAndAuth(t, ts, "agent-host")
	defer conn2.Close()

	time.Sleep(50 * time.Millisecond)

	result := getAPIJSON(t, ts, "/api/status")
	count := result["agent_count"].(float64)
	if count < 1 {
		t.Errorf("agent_count 期望 ≥ 1，得到 %v", count)
	}
}

func TestAPI_Status_AfterDisconnect(t *testing.T) {
	_, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn2, _ := connectAndAuth(t, ts, "temp-agent")
	time.Sleep(50 * time.Millisecond)

	result := getAPIJSON(t, ts, "/api/status")
	before := result["agent_count"].(float64)

	conn2.Close()
	time.Sleep(100 * time.Millisecond)

	result2 := getAPIJSON(t, ts, "/api/status")
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
	_, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, _ := connectAndAuth(t, ts, "host-A")
	defer conn1.Close()
	conn2, _ := connectAndAuth(t, ts, "host-B")
	defer conn2.Close()
	conn3, _ := connectAndAuth(t, ts, "host-C")
	defer conn3.Close()

	time.Sleep(50 * time.Millisecond)

	resp, _ := http.Get(ts.URL + "/api/agents")
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
	_, _, ts, cleanup := setupWSTest(t)
	defer cleanup()

	conn1, _ := connectAndAuth(t, ts, "stats-host")
	defer conn1.Close()

	stats := protocol.SystemStats{CPUUsage: 55.5, MemUsage: 70.0, NumCPU: 8}
	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	conn1.WriteJSON(msg)
	time.Sleep(100 * time.Millisecond)

	resp, _ := http.Get(ts.URL + "/api/agents")
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
	if agent.Stats.CPUUsage != 80.0 {
		t.Errorf("Stats 应被更新为最新值 80.0，得到 %f", agent.Stats.CPUUsage)
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

func TestWeb_NotFound(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/nonexist", nil)
	w := httptest.NewRecorder()
	s.handleWeb(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("状态码期望 404，得到 %d", w.Code)
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
	if !strings.HasPrefix(authResp.AgentID, "agent_test-host_") {
		t.Errorf("AgentID 格式错误: %q", authResp.AgentID)
	}
}

func TestAuth_EmptyToken(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuthWithInfo(t, conn, "host", "")

	if !authResp.Success {
		t.Errorf("Phase1 空 token 应允许连接: %s", authResp.Message)
	}
}

func TestAuth_EmptyHostname(t *testing.T) {
	_, conn, _, cleanup := setupWSTest(t)
	defer cleanup()

	authResp := doAuthWithInfo(t, conn, "", "token")

	if !authResp.Success {
		t.Errorf("空主机名不应导致认证失败: %s", authResp.Message)
	}
	if authResp.AgentID == "" {
		t.Error("AgentID 不应为空")
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
	if agent.Stats == nil {
		t.Fatal("Stats 不应为 nil")
	}
	if agent.Stats.CPUUsage != 42.5 {
		t.Errorf("CPUUsage 期望 42.5，得到 %f", agent.Stats.CPUUsage)
	}
	if agent.Stats.MemUsage != 60.0 {
		t.Errorf("MemUsage 期望 60.0，得到 %f", agent.Stats.MemUsage)
	}
	if agent.Stats.NumCPU != 4 {
		t.Errorf("NumCPU 期望 4，得到 %d", agent.Stats.NumCPU)
	}
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
	if agent.Stats.CPUUsage != 50.0 {
		t.Errorf("最终 CPUUsage 应为 50.0（最后一次上报），得到 %f", agent.Stats.CPUUsage)
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
	if val.(*AgentConn).Stats.CPUUsage != 33.3 {
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
				Token: "token",
				Agent: protocol.AgentInfo{Hostname: hostname, OS: "linux", Arch: "amd64", Version: "0.1.0"},
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
