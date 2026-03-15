package client

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// ============================================================
// 测试辅助：模拟一个 Server 端 WebSocket 处理器
// ============================================================

// mockServer 模拟 Server 端行为，用于测试 Client
type mockServer struct {
	mu           sync.Mutex
	receivedMsgs []protocol.Message
	authSuccess  bool
	conns        []*websocket.Conn
	onMessage    func(msg protocol.Message) *protocol.Message // 收到消息后的回调
}

func newMockServer(authSuccess bool) *mockServer {
	return &mockServer{
		authSuccess: authSuccess,
	}
}

func (ms *mockServer) handler(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ms.mu.Lock()
	ms.conns = append(ms.conns, conn)
	ms.mu.Unlock()

	for {
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}

		ms.mu.Lock()
		ms.receivedMsgs = append(ms.receivedMsgs, msg)
		ms.mu.Unlock()

		// 处理消息
		switch msg.Type {
		case protocol.MsgTypeAuth:
			resp, _ := protocol.NewMessage(protocol.MsgTypeAuthResp, protocol.AuthResponse{
				Success:   ms.authSuccess,
				Message:   "mock response",
				ClientID:  "mock_client_1",
				DataToken: "mock-data-token",
			})
			conn.WriteJSON(resp)

		case protocol.MsgTypePing:
			pong, _ := protocol.NewMessage(protocol.MsgTypePong, nil)
			conn.WriteJSON(pong)

		case protocol.MsgTypeProbeReport:
			// 服务端不回复探针上报

		default:
			if ms.onMessage != nil {
				if reply := ms.onMessage(msg); reply != nil {
					conn.WriteJSON(reply)
				}
			}
		}
	}
}

// closeConns 主动关闭所有 WebSocket 连接
func (ms *mockServer) closeConns() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	for _, conn := range ms.conns {
		conn.Close()
	}
	ms.conns = nil
}

func (ms *mockServer) getReceivedMsgs() []protocol.Message {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	result := make([]protocol.Message, len(ms.receivedMsgs))
	copy(result, ms.receivedMsgs)
	return result
}

// ============================================================
// Client 集成测试
// ============================================================

func TestClient_ConnectAndAuth(t *testing.T) {
	ms := newMockServer(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	// 在后台启动 Client（Start 会阻塞在 controlLoop 里）
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Start()
	}()

	// 等 Client 完成认证
	time.Sleep(500 * time.Millisecond)

	// 验证 ClientID 被设置
	if c.ClientID != "mock_client_1" {
		t.Errorf("ClientID 期望 'mock_client_1'，得到 %q", c.ClientID)
	}

	// 验证 Server 收到了认证消息
	msgs := ms.getReceivedMsgs()
	if len(msgs) == 0 {
		t.Fatal("Server 未收到任何消息")
	}
	if msgs[0].Type != protocol.MsgTypeAuth {
		t.Errorf("第一条消息应为 auth，得到 %s", msgs[0].Type)
	}
}

func TestClient_HeartbeatSent(t *testing.T) {
	ms := newMockServer(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()

	// 数据通道连接会快速失败（~1s），然后心跳间隔 5s，等 8s 应收到至少 1 次
	time.Sleep(8 * time.Second)

	msgs := ms.getReceivedMsgs()
	pingCount := 0
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypePing {
			pingCount++
		}
	}

	if pingCount == 0 {
		t.Errorf("等待 7 秒后应至少收到 1 次心跳，实际收到 %d 次", pingCount)
	}
}

func TestClient_ProbeReportSent(t *testing.T) {
	ms := newMockServer(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()

	// 探针在数据通道失败（~2s）后上报，CPU 采样约 1s，等 5s 足够
	time.Sleep(5 * time.Second)

	msgs := ms.getReceivedMsgs()
	probeCount := 0
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypeProbeReport {
			probeCount++
		}
	}

	if probeCount == 0 {
		t.Error("应收到至少 1 次探针上报（启动时立即上报）")
	}

	// 验证探针数据内容
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypeProbeReport {
			var stats protocol.SystemStats
			if err := msg.ParsePayload(&stats); err != nil {
				t.Fatalf("解析探针数据失败: %v", err)
			}
			if stats.NumCPU == 0 {
				t.Error("探针数据 NumCPU 不应为 0")
			}
			if stats.MemTotal == 0 {
				t.Error("探针数据 MemTotal 不应为 0")
			}
			break
		}
	}
}

func TestClient_ServerDisconnect_WithReconnect(t *testing.T) {
	ms := newMockServer(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(mux)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true // 测试中禁用重连避免阻塞

	// 后台启动 Client
	started := make(chan struct{})
	go func() {
		close(started)
		c.Start()
	}()
	<-started

	// 等 Client 完成认证和至少一次探针采集
	time.Sleep(3 * time.Second)

	// 验证连接正常
	if c.ClientID == "" {
		t.Fatal("Client 应已完成认证")
	}

	// 模拟 Server 断开
	ms.closeConns()
	ts.Close()

	// 验证 done channel 被关闭（controlLoop 检测到断连后关闭）
	select {
	case <-c.done:
		// 成功：Client 检测到了断连
	case <-time.After(5 * time.Second):
		t.Error("Server 断开后 Client 的 done channel 应在合理时间内关闭")
	}
}

func TestClient_AuthFailed(t *testing.T) {
	ms := newMockServer(false) // 模拟认证失败
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "wrong-key")

	err := c.Start()
	if err == nil || !strings.Contains(err.Error(), "认证被拒绝") {
		t.Errorf("期望因认证失败而 Start 报错，实际得到: %v", err)
	}
}

func TestClient_DataChannelConnectErrorHandling(t *testing.T) {
	// 创建一个没有提供 HTTP Server 而是直接关闭了监听的 mock
	c := New("ws://127.0.0.1:11111", "key")
	err := c.connectDataChannel()
	if err == nil {
		t.Error("期望连不上目标服务器时报错")
	}
}

// ============================================================
// Reconnect 测试
// ============================================================

func TestClient_Reconnect_AfterDisconnect(t *testing.T) {
	ms := newMockServer(true)

	// 统计认证次数
	var authCount int
	var authMu sync.Mutex

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/control", func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		ms.mu.Lock()
		ms.conns = append(ms.conns, conn)
		ms.mu.Unlock()

		for {
			var msg protocol.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}

			switch msg.Type {
			case protocol.MsgTypeAuth:
				authMu.Lock()
				authCount++
				authMu.Unlock()
				resp, _ := protocol.NewMessage(protocol.MsgTypeAuthResp, protocol.AuthResponse{
					Success:   true,
					Message:   "ok",
					ClientID:  "reconnect-client",
					DataToken: "reconnect-data-token",
				})
				conn.WriteJSON(resp)
			case protocol.MsgTypePing:
				pong, _ := protocol.NewMessage(protocol.MsgTypePong, nil)
				conn.WriteJSON(pong)
			}
		}
	})
	ts := httptest.NewServer(httpMux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	// 不设 DisableReconnect，让 reconnect 生效

	// 后台启动 Client
	go c.Start()
	time.Sleep(1 * time.Second)

	// 验证首次认证完成
	authMu.Lock()
	firstAuth := authCount
	authMu.Unlock()
	if firstAuth == 0 {
		t.Fatal("首次认证应已完成")
	}

	// 断开连接
	ms.closeConns()

	// 等待重连（3s 间隔 + 连接时间）
	time.Sleep(5 * time.Second)

	// 验证重连成功（认证次数增加）
	authMu.Lock()
	finalAuth := authCount
	authMu.Unlock()
	if finalAuth <= firstAuth {
		t.Errorf("重连后认证次数应增加，首次: %d, 当前: %d", firstAuth, finalAuth)
	}
}

func TestClient_RetryInterval(t *testing.T) {
	// 测试前 5 分钟的间隔
	recent := time.Now().Add(-1 * time.Minute) // 1 分钟前
	interval := retryInterval(recent)
	if interval != 3*time.Second {
		t.Errorf("断连 1 分钟内应返回 3s，得到 %v", interval)
	}

	// 测试 5 分钟后的间隔
	old := time.Now().Add(-6 * time.Minute) // 6 分钟前
	interval = retryInterval(old)
	if interval != 10*time.Second {
		t.Errorf("断连超过 5 分钟应返回 10s，得到 %v", interval)
	}
}

func TestClient_Cleanup(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	c.ClientID = "cleanup-test"
	c.proxies.Store("proxy1", protocol.ProxyNewRequest{Name: "proxy1"})

	// 模拟创建一个 dataSession
	clientConn, serverConn := net.Pipe()
	session, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = session

	// 执行清理
	c.cleanup()

	// 验证清理结果
	if c.ClientID != "" {
		t.Error("cleanup 后 ClientID 应为空")
	}

	_, ok := c.proxies.Load("proxy1")
	if ok {
		t.Error("cleanup 后 proxies 应被清空")
	}

	c.dataMu.RLock()
	if c.dataSession != nil {
		t.Error("cleanup 后 dataSession 应为 nil")
	}
	c.dataMu.RUnlock()

	serverConn.Close()
	clientConn.Close()
}

// ============================================================
// acceptStreamLoop 测试
// ============================================================

func TestClient_AcceptStreamLoop_NilSession(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	// dataSession = nil, 应该直接 return 不 panic
	c.acceptStreamLoop()
}

func TestClient_AcceptStreamLoop_SessionClosed(t *testing.T) {
	c := New("ws://localhost:8080", "key")

	clientConn, serverConn := net.Pipe()
	session, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = session

	// 立即关闭 session，模拟断连
	session.Close()
	serverConn.Close()
	clientConn.Close()

	// 应当安全退出
	c.acceptStreamLoop()
}

// ============================================================
// requestProxy 测试
// ============================================================

func TestClient_RequestProxy(t *testing.T) {
	ms := newMockServer(true)
	ms.onMessage = func(msg protocol.Message) *protocol.Message {
		if msg.Type == protocol.MsgTypeProxyNew {
			resp, _ := protocol.NewMessage(protocol.MsgTypeProxyNewResp, protocol.ProxyNewResponse{
				Success:    true,
				Message:    "ok",
				RemotePort: 18080,
			})
			return resp
		}
		return nil
	}

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(httpMux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	// 启动 Client（后台阻塞在 controlLoop）
	go c.Start()
	time.Sleep(500 * time.Millisecond) // 等认证和数据通道尝试完成

	// 手动调用 requestProxy
	cfg := protocol.ProxyNewRequest{
		Name:       "test-proxy",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  8080,
		RemotePort: 18080,
	}
	c.requestProxy(cfg)

	// 验证 Server 收到了 proxy_new 消息
	time.Sleep(200 * time.Millisecond)
	msgs := ms.getReceivedMsgs()
	found := false
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypeProxyNew {
			found = true
			break
		}
	}
	if !found {
		t.Error("Server 应收到 proxy_new 消息")
	}

	// 验证 proxies sync.Map 已注册
	_, ok := c.proxies.Load("test-proxy")
	if !ok {
		t.Error("requestProxy 应在 proxies 中注册配置")
	}
}

// ============================================================
// controlLoop — ProxyNewResp 处理测试
// ============================================================

func TestClient_ControlLoop_ProxyNewResp_Success(t *testing.T) {
	ms := newMockServer(true)
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(httpMux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()
	time.Sleep(500 * time.Millisecond)

	// Server 主动发送 proxy_new_resp (成功)
	ms.mu.Lock()
	if len(ms.conns) > 0 {
		resp, _ := protocol.NewMessage(protocol.MsgTypeProxyNewResp, protocol.ProxyNewResponse{
			Success:    true,
			Message:    "tunnel created",
			RemotePort: 19090,
		})
		ms.conns[len(ms.conns)-1].WriteJSON(resp)
	}
	ms.mu.Unlock()

	// 等待 Client 处理，不崩溃即通过
	time.Sleep(200 * time.Millisecond)
}

func TestClient_ControlLoop_ProxyNewResp_Failure(t *testing.T) {
	ms := newMockServer(true)
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(httpMux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()
	time.Sleep(500 * time.Millisecond)

	// Server 主动发送 proxy_new_resp (失败)
	ms.mu.Lock()
	if len(ms.conns) > 0 {
		resp, _ := protocol.NewMessage(protocol.MsgTypeProxyNewResp, protocol.ProxyNewResponse{
			Success: false,
			Message: "port conflict",
		})
		ms.conns[len(ms.conns)-1].WriteJSON(resp)
	}
	ms.mu.Unlock()

	time.Sleep(200 * time.Millisecond)
}

// ============================================================
// connectDataChannel 完整握手测试
// ============================================================

func TestClient_ConnectDataChannel_Success(t *testing.T) {
	// 启动一个 TCP Server 模拟完整数据通道握手
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// 读取握手: [1B 魔数] [2B len] [NB clientID] [2B tokenLen] [NB dataToken]
		magic := make([]byte, 1)
		conn.Read(magic)

		lenBuf := make([]byte, 2)
		conn.Read(lenBuf)
		idLen := int(lenBuf[0])<<8 | int(lenBuf[1])
		idBuf := make([]byte, idLen)
		conn.Read(idBuf)

		tokenLenBuf := make([]byte, 2)
		conn.Read(tokenLenBuf)
		tokenLen := int(tokenLenBuf[0])<<8 | int(tokenLenBuf[1])
		tokenBuf := make([]byte, tokenLen)
		conn.Read(tokenBuf)

		// 回复握手成功
		conn.Write([]byte{protocol.DataHandshakeOK})

		// 保持连接以便建立 yamux session
		time.Sleep(1 * time.Second)
	}()

	c := New("ws://"+ln.Addr().String(), "key")
	c.ClientID = "test-client-dc"
	c.dataToken = "test-dc-token"

	err = c.connectDataChannel()
	if err != nil {
		t.Fatalf("connectDataChannel 应成功: %v", err)
	}

	c.dataMu.RLock()
	hasSession := c.dataSession != nil
	c.dataMu.RUnlock()

	if !hasSession {
		t.Error("成功握手后 dataSession 不应为 nil")
	}
}

func TestClient_ConnectDataChannel_Rejected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// 读取完整握手包内容
		buf := make([]byte, 512)
		conn.Read(buf)

		// 回复拒绝
		conn.Write([]byte{protocol.DataHandshakeFail})
	}()

	c := New("ws://"+ln.Addr().String(), "key")
	c.ClientID = "rejected-client"
	c.dataToken = "some-token"

	err = c.connectDataChannel()
	if err == nil {
		t.Error("Server 拒绝握手时应返回错误")
	}
	if !strings.Contains(err.Error(), "握手被拒绝") {
		t.Errorf("错误信息应包含'握手被拒绝'，实际得到: %v", err)
	}
}

func TestClient_ConnectDataChannel_NoPort(t *testing.T) {
	// ServerAddr 没有端口的情况
	c := New("ws://some-host-without-port-1234567.invalid", "key")
	c.ClientID = "no-port-client"
	c.dataToken = "some-token"
	err := c.connectDataChannel()
	if err == nil {
		t.Error("无法连接时应返回错误")
	}
}

// ============================================================
// ============================================================

func TestNormalizeServerAddr(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		useTLS   bool
	}{
		{"ws://localhost:8080", "http://localhost:8080", false},
		{"wss://localhost:8080", "https://localhost:8080", true},
		{"http://localhost:8080", "http://localhost:8080", false},
		{"https://localhost:8080", "https://localhost:8080", true},
		{"ws://1.2.3.4:9090", "http://1.2.3.4:9090", false},
		{"wss://example.com:443", "https://example.com:443", true},
		{"localhost:8080", "http://localhost:8080", false},
		{"ws://localhost:8080/", "http://localhost:8080", false},
		{"https://tunnel.example.com", "https://tunnel.example.com", true},
	}

	for _, tt := range tests {
		c := New(tt.input, "key")
		c.normalizeServerAddr()
		if c.ServerAddr != tt.expected {
			t.Errorf("normalizeServerAddr(%q) = %q, 期望 %q", tt.input, c.ServerAddr, tt.expected)
		}
		if c.useTLS != tt.useTLS {
			t.Errorf("normalizeServerAddr(%q): useTLS = %v, 期望 %v", tt.input, c.useTLS, tt.useTLS)
		}
	}
}

func TestDeriveControlURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ws://localhost:8080", "ws://localhost:8080/ws/control"},
		{"wss://localhost:8080", "wss://localhost:8080/ws/control"},
		{"http://localhost:8080", "ws://localhost:8080/ws/control"},
		{"https://tunnel.example.com", "wss://tunnel.example.com/ws/control"},
	}

	for _, tt := range tests {
		c := New(tt.input, "key")
		c.normalizeServerAddr()
		url := c.deriveControlURL()
		if url != tt.expected {
			t.Errorf("deriveControlURL() for %q = %q, 期望 %q", tt.input, url, tt.expected)
		}
	}
}
