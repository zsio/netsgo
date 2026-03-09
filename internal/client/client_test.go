package client

import (
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

