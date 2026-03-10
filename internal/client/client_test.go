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
	mu            sync.Mutex
	receivedMsgs  []protocol.Message
	authSuccess   bool
	conns         []*websocket.Conn
	onMessage     func(msg protocol.Message) *protocol.Message // 收到消息后的回调
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
				Success: ms.authSuccess,
				Message: "mock response",
				AgentID: "mock_agent_1",
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
// Client 集成测试 (4)
// ============================================================

func TestClient_ConnectAndAuth(t *testing.T) {
	ms := newMockServer(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-token")

	// 在后台启动 Client（Start 会阻塞在 controlLoop 里）
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Start()
	}()

	// 等 Client 完成认证
	time.Sleep(500 * time.Millisecond)

	// 验证 AgentID 被设置
	if c.AgentID != "mock_agent_1" {
		t.Errorf("AgentID 期望 'mock_agent_1'，得到 %q", c.AgentID)
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
	c := New(wsURL, "test-token")

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
	c := New(wsURL, "test-token")

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

func TestClient_ServerDisconnect(t *testing.T) {
	ms := newMockServer(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(mux)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-token")

	// 后台启动 Client
	started := make(chan struct{})
	go func() {
		close(started)
		c.Start() // 不关心返回值，重点是不 panic
	}()
	<-started

	// 等 Client 完成认证和至少一次探针采集
	time.Sleep(3 * time.Second)

	// 验证连接正常
	if c.AgentID == "" {
		t.Fatal("Client 应已完成认证")
	}

	// 模拟 Server 断开 — 必须主动关闭 WS 连接
	// httptest.Server.Close() 仅关闭 listener，不会立即关闭已有的 WS 连接
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
	c := New(wsURL, "wrong-token")

	err := c.Start()
	if err == nil || !strings.Contains(err.Error(), "认证被拒绝") {
		t.Errorf("期望因认证失败而 Start 报错，实际得到: %v", err)
	}
}

func TestClient_DataChannelConnectErrorHandling(t *testing.T) {
	// 创建一个没有提供 HTTP Server 而是直接关闭了监听的 mock
	c := New("ws://127.0.0.1:11111", "token")
	err := c.connectDataChannel()
	if err == nil {
		t.Error("期望连不上目标服务器时报错")
	}
}

// ============================================================
// acceptStreamLoop 测试 (2)
// ============================================================

func TestClient_AcceptStreamLoop_NilSession(t *testing.T) {
	c := New("ws://localhost:8080", "token")
	// dataSession = nil, 应该直接 return 不 panic
	c.acceptStreamLoop()
}

func TestClient_AcceptStreamLoop_SessionClosed(t *testing.T) {
	c := New("ws://localhost:8080", "token")

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
// requestProxy 测试 (1)
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
	c := New(wsURL, "test-token")

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
// controlLoop — ProxyNewResp 处理测试 (2)
// ============================================================

func TestClient_ControlLoop_ProxyNewResp_Success(t *testing.T) {
	ms := newMockServer(true)
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/control", ms.handler)
	ts := httptest.NewServer(httpMux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-token")

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
	c := New(wsURL, "test-token")

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
// connectDataChannel 完整握手测试 (2)
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

		// 读取握手: [1B 魔数] [2B len] [NB agentID]
		magic := make([]byte, 1)
		conn.Read(magic)

		lenBuf := make([]byte, 2)
		conn.Read(lenBuf)

		idLen := int(lenBuf[0])<<8 | int(lenBuf[1])
		idBuf := make([]byte, idLen)
		conn.Read(idBuf)

		// 回复握手成功
		conn.Write([]byte{protocol.DataHandshakeOK})

		// 保持连接以便建立 yamux session
		time.Sleep(1 * time.Second)
	}()

	c := New("ws://"+ln.Addr().String(), "token")
	c.AgentID = "test-agent-dc"

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

		// 读取握手
		buf := make([]byte, 256)
		conn.Read(buf)

		// 回复拒绝
		conn.Write([]byte{protocol.DataHandshakeFail})
	}()

	c := New("ws://"+ln.Addr().String(), "token")
	c.AgentID = "rejected-agent"

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
	c := New("ws://some-host-without-port-1234567.invalid", "token")
	c.AgentID = "no-port-agent"
	err := c.connectDataChannel()
	if err == nil {
		t.Error("无法连接时应返回错误")
	}
}
