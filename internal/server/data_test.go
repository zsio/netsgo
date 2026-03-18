package server

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

const testDataToken = "test-data-token-abc123"

type unixDataTestServer struct {
	socketPath string
	httpServer *http.Server
	listener   net.Listener
	httpClient *http.Client
}

func newUnixDataTestServer(t *testing.T, handler http.Handler) *unixDataTestServer {
	t.Helper()

	socketPath := filepath.Join("/tmp", fmt.Sprintf("netsgo-data-%d.sock", time.Now().UnixNano()))
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("创建 unix socket 测试监听器失败: %v", err)
	}

	srv := &http.Server{Handler: handler}
	go func() {
		_ = srv.Serve(ln)
	}()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}

	return &unixDataTestServer{
		socketPath: socketPath,
		httpServer: srv,
		listener:   ln,
		httpClient: &http.Client{Transport: transport},
	}
}

func (ts *unixDataTestServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = ts.httpServer.Shutdown(ctx)
	_ = ts.listener.Close()
	_ = os.Remove(ts.socketPath)
}

func setupDataWSTest(t *testing.T) (*Server, *unixDataTestServer, func()) {
	t.Helper()
	s := New(0)
	ts := newUnixDataTestServer(t, s.newHTTPMux())
	return s, ts, ts.Close
}

func dialDataWS(t *testing.T, ts *unixDataTestServer) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", ts.socketPath)
		},
	}
	conn, _, err := dialer.Dial("ws://unix/ws/data", nil)
	if err != nil {
		t.Fatalf("连接 /ws/data 失败: %v", err)
	}
	return conn
}

func readHandshakeStatus(t *testing.T, conn *websocket.Conn) byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取握手响应失败: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("握手响应类型错误: %d", messageType)
	}
	if len(payload) != 1 {
		t.Fatalf("握手响应长度错误: %d", len(payload))
	}
	return payload[0]
}

func newPendingTestClient(clientID, token string) *ClientConn {
	return &ClientConn{
		ID:         clientID,
		proxies:    make(map[string]*ProxyTunnel),
		dataToken:  token,
		generation: 1,
		state:      clientStatePendingData,
	}
}

func TestDataChannel_HandshakeSuccess(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "test-client-123"
	cc := newPendingTestClient(clientID, testDataToken)
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer conn.Close()

	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, testDataToken)); err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeOK {
		t.Fatalf("期望 OK，得到 0x%02x", status)
	}

	time.Sleep(50 * time.Millisecond)
	cc.dataMu.RLock()
	hasSession := cc.dataSession != nil && !cc.dataSession.IsClosed()
	cc.dataMu.RUnlock()
	if !hasSession {
		t.Fatal("握手成功后应建立 dataSession")
	}
	if cc.getState() != clientStateLive {
		t.Fatalf("握手成功后状态应提升为 live，得到 %s", cc.getState())
	}
}

func TestDataChannel_Handshake_InvalidLength(t *testing.T) {
	_, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	conn := dialDataWS(t, ts)
	defer conn.Close()

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x00, 0x00}); err != nil {
		t.Fatalf("发送非法握手失败: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeFail {
		t.Fatalf("期望 Fail，得到 0x%02x", status)
	}
}

func TestDataChannel_Handshake_UnregisteredClient(t *testing.T) {
	_, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	conn := dialDataWS(t, ts)
	defer conn.Close()

	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake("ghost-client", "some-token")); err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeFail {
		t.Fatalf("期望 Fail，得到 0x%02x", status)
	}
}

func TestDataChannel_Handshake_ReconnectClosesOldSession(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "reconnect-client"
	cc := newPendingTestClient(clientID, testDataToken)
	cc.state = clientStateLive
	s.clients.Store(clientID, cc)

	conn1 := dialDataWS(t, ts)
	defer conn1.Close()
	if err := conn1.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, testDataToken)); err != nil {
		t.Fatalf("发送第一次握手失败: %v", err)
	}
	if status := readHandshakeStatus(t, conn1); status != protocol.DataHandshakeOK {
		t.Fatalf("第一次握手失败: 0x%02x", status)
	}

	time.Sleep(50 * time.Millisecond)
	cc.dataMu.RLock()
	session1 := cc.dataSession
	cc.dataMu.RUnlock()
	if session1 == nil {
		t.Fatal("第一次握手后 session1 不应为空")
	}

	conn2 := dialDataWS(t, ts)
	defer conn2.Close()
	if err := conn2.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, testDataToken)); err != nil {
		t.Fatalf("发送第二次握手失败: %v", err)
	}
	if status := readHandshakeStatus(t, conn2); status != protocol.DataHandshakeOK {
		t.Fatalf("第二次握手失败: 0x%02x", status)
	}

	time.Sleep(50 * time.Millisecond)
	cc.dataMu.RLock()
	session2 := cc.dataSession
	cc.dataMu.RUnlock()
	if session2 == nil {
		t.Fatal("第二次握手后 session2 不应为空")
	}
	if session1 == session2 {
		t.Fatal("第二次接入应替换 dataSession")
	}
	if !session1.IsClosed() {
		t.Fatal("旧 dataSession 应被关闭")
	}
}

func TestDataChannel_Handshake_WrongToken(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "token-test-client"
	cc := newPendingTestClient(clientID, "correct-token")
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, "wrong-token")); err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeAuthFail {
		t.Fatalf("期望 AuthFail，得到 0x%02x", status)
	}
}

func TestDataChannel_Handshake_EmptyToken(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "empty-token-client"
	cc := newPendingTestClient(clientID, "some-valid-token")
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer conn.Close()
	payload := protocol.EncodeDataHandshake(clientID, "")
	if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeFail {
		t.Fatalf("期望 Fail，得到 0x%02x", status)
	}
}

func TestDataChannel_Handshake_ClientHasNoToken(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "no-token-client"
	cc := newPendingTestClient(clientID, "")
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, "any-token")); err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeAuthFail {
		t.Fatalf("期望 AuthFail，得到 0x%02x", status)
	}
}

func TestDataChannel_Handshake_NonBinaryFrame(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "text-frame-client"
	cc := newPendingTestClient(clientID, testDataToken)
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not-binary")); err != nil {
		t.Fatalf("发送 text frame 失败: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("首帧非 binary 时应被关闭")
	}
}

func TestDataChannel_NonUpgradeRequestReturns426(t *testing.T) {
	_, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	resp, err := ts.httpClient.Get("http://unix/ws/data")
	if err != nil {
		t.Fatalf("HTTP GET /ws/data 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("状态码应为 426，得到 %d", resp.StatusCode)
	}
}

func TestOpenStreamToClient_Success(t *testing.T) {
	s := New(0)
	clientID := "stream-client"
	cc := &ClientConn{
		ID:         clientID,
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(clientID, cc)

	clientPipe, serverPipe := net.Pipe()
	defer clientPipe.Close()
	defer serverPipe.Close()

	serverReady := make(chan error, 1)
	go func() {
		cc.dataMu.Lock()
		cc.dataSession, _ = mux.NewServerSession(serverPipe, mux.DefaultConfig())
		cc.dataMu.Unlock()
		serverReady <- nil
	}()

	clientSession, err := mux.NewClientSession(clientPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("创建客户端 Yamux Session 失败: %v", err)
	}
	defer clientSession.Close()

	select {
	case err := <-serverReady:
		if err != nil {
			t.Fatalf("创建服务端 Yamux Session 失败: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待服务端 Yamux Session 就绪超时")
	}

	type openResult struct {
		stream net.Conn
		err    error
	}
	resultCh := make(chan openResult, 1)
	go func() {
		stream, err := s.openStreamToClient(cc, "test-tunnel")
		resultCh <- openResult{stream: stream, err: err}
	}()

	clientStream, err := clientSession.Accept()
	if err != nil {
		t.Fatalf("客户端接受 Stream 失败: %v", err)
	}
	defer clientStream.Close()

	var lenBuf [2]byte
	if _, err := clientStream.Read(lenBuf[:]); err != nil {
		t.Fatalf("读取 proxyName 长度失败: %v", err)
	}
	nameLen := binary.BigEndian.Uint16(lenBuf[:])
	nameBuf := make([]byte, nameLen)
	if _, err := clientStream.Read(nameBuf); err != nil {
		t.Fatalf("读取 proxyName 内容失败: %v", err)
	}
	if string(nameBuf) != "test-tunnel" {
		t.Fatalf("proxyName 错误: %q", string(nameBuf))
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("openStreamToClient 报错: %v", result.err)
		}
		if result.stream == nil {
			t.Fatal("openStreamToClient 应返回有效 conn")
		}
		_ = result.stream.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("等待 openStreamToClient 返回超时")
	}
}

func TestOpenStreamToClient_NoDataSession(t *testing.T) {
	s := New(0)
	clientID := "no-data-client"
	cc := &ClientConn{
		ID:         clientID,
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(clientID, cc)

	if _, err := s.openStreamToClient(cc, "test-proxy"); err == nil {
		t.Fatal("没有 dataSession 时应报错")
	}
}
