package server

import (
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

const testDataToken = "test-data-token-abc123"

type unixDataTestServer struct {
	httpServer *httptest.Server
	httpClient *http.Client
	wsURL      string
}

func newUnixDataTestServer(t *testing.T, handler http.Handler) *unixDataTestServer {
	t.Helper()

	httpServer := httptest.NewServer(handler)

	wsURL, err := testWebSocketURL(httpServer.URL + "/ws/data")
	if err != nil {
		httpServer.Close()
		t.Fatalf("Failed to construct data test WebSocket URL: %v", err)
	}

	return &unixDataTestServer{
		httpServer: httpServer,
		httpClient: httpServer.Client(),
		wsURL:      wsURL,
	}
}

func (ts *unixDataTestServer) Close() {
	ts.httpServer.Close()
}

func setupDataWSTest(t *testing.T) (*Server, *unixDataTestServer, func()) {
	t.Helper()
	s := New(0)
	ts := newUnixDataTestServer(t, s.newHTTPMux())
	return s, ts, ts.Close
}

func dialDataWS(t *testing.T, ts *unixDataTestServer) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(ts.wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to /ws/data: %v", err)
	}
	return conn
}

func testWebSocketURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	}
	return parsed.String(), nil
}

func readHandshakeStatus(t *testing.T, conn *websocket.Conn) byte {
	t.Helper()
	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read handshake response: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("Handshake response type error: %d", messageType)
	}
	if len(payload) != 1 {
		t.Fatalf("Handshake response length error: %d", len(payload))
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
	defer mustClose(t, conn)

	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, testDataToken)); err != nil {
		t.Fatalf("Failed to send handshake: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeOK {
		t.Fatalf("Expected OK, got 0x%02x", status)
	}

	time.Sleep(50 * time.Millisecond)
	cc.dataMu.RLock()
	hasSession := cc.dataSession != nil && !cc.dataSession.IsClosed()
	cc.dataMu.RUnlock()
	if !hasSession {
		t.Fatal("dataSession should be established after successful handshake")
	}
	if cc.getState() != clientStateLive {
		t.Fatalf("State should be promoted to live after successful handshake, got %s", cc.getState())
	}
}

func TestDataChannel_Handshake_InvalidLength(t *testing.T) {
	_, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	conn := dialDataWS(t, ts)
	defer mustClose(t, conn)

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x00, 0x00}); err != nil {
		t.Fatalf("Failed to send illegal handshake: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeFail {
		t.Fatalf("Expected Fail, got 0x%02x", status)
	}
}

func TestDataChannel_Handshake_UnregisteredClient(t *testing.T) {
	_, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	conn := dialDataWS(t, ts)
	defer mustClose(t, conn)

	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake("ghost-client", "some-token")); err != nil {
		t.Fatalf("Failed to send handshake: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeFail {
		t.Fatalf("Expected Fail, got 0x%02x", status)
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
	defer mustClose(t, conn1)
	if err := conn1.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, testDataToken)); err != nil {
		t.Fatalf("Failed to send first handshake: %v", err)
	}
	if status := readHandshakeStatus(t, conn1); status != protocol.DataHandshakeOK {
		t.Fatalf("First handshake failed: 0x%02x", status)
	}

	time.Sleep(50 * time.Millisecond)
	cc.dataMu.RLock()
	session1 := cc.dataSession
	cc.dataMu.RUnlock()
	if session1 == nil {
		t.Fatal("session1 should not be empty after first handshake")
	}

	conn2 := dialDataWS(t, ts)
	defer mustClose(t, conn2)
	if err := conn2.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, testDataToken)); err != nil {
		t.Fatalf("Failed to send second handshake: %v", err)
	}
	if status := readHandshakeStatus(t, conn2); status != protocol.DataHandshakeOK {
		t.Fatalf("Second handshake failed: 0x%02x", status)
	}

	time.Sleep(50 * time.Millisecond)
	cc.dataMu.RLock()
	session2 := cc.dataSession
	cc.dataMu.RUnlock()
	if session2 == nil {
		t.Fatal("session2 should not be empty after second handshake")
	}
	if session1 == session2 {
		t.Fatal("Second connection should replace dataSession")
	}
	if !session1.IsClosed() {
		t.Fatal("Old dataSession should be closed")
	}
}

func TestDataChannel_Handshake_WrongToken(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "token-test-client"
	cc := newPendingTestClient(clientID, "correct-token")
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer mustClose(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, "wrong-token")); err != nil {
		t.Fatalf("Failed to send handshake: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeAuthFail {
		t.Fatalf("Expected AuthFail, got 0x%02x", status)
	}
}

func TestDataChannel_Handshake_EmptyToken(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "empty-token-client"
	cc := newPendingTestClient(clientID, "some-valid-token")
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer mustClose(t, conn)
	payload := protocol.EncodeDataHandshake(clientID, "")
	if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("Failed to send handshake: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeFail {
		t.Fatalf("Expected Fail, got 0x%02x", status)
	}
}

func TestDataChannel_Handshake_ClientHasNoToken(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "no-token-client"
	cc := newPendingTestClient(clientID, "")
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer mustClose(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeDataHandshake(clientID, "any-token")); err != nil {
		t.Fatalf("Failed to send handshake: %v", err)
	}

	if status := readHandshakeStatus(t, conn); status != protocol.DataHandshakeAuthFail {
		t.Fatalf("Expected AuthFail, got 0x%02x", status)
	}
}

func TestDataChannel_Handshake_NonBinaryFrame(t *testing.T) {
	s, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	clientID := "text-frame-client"
	cc := newPendingTestClient(clientID, testDataToken)
	s.clients.Store(clientID, cc)

	conn := dialDataWS(t, ts)
	defer mustClose(t, conn)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not-binary")); err != nil {
		t.Fatalf("Failed to send text frame: %v", err)
	}

	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("Should be closed when first frame is not binary")
	}
}

func TestDataChannel_NonUpgradeRequestReturns426(t *testing.T) {
	_, ts, cleanup := setupDataWSTest(t)
	defer cleanup()

	resp, err := ts.httpClient.Get(ts.httpServer.URL + "/ws/data")
	if err != nil {
		t.Fatalf("HTTP GET /ws/data failed: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("Status code should be 426, got %d", resp.StatusCode)
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
	defer mustClose(t, clientPipe)
	defer mustClose(t, serverPipe)

	serverReady := make(chan error, 1)
	go func() {
		cc.dataMu.Lock()
		cc.dataSession, _ = mux.NewServerSession(serverPipe, mux.DefaultConfig())
		cc.dataMu.Unlock()
		serverReady <- nil
	}()

	clientSession, err := mux.NewClientSession(clientPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create client Yamux Session: %v", err)
	}
	defer mustClose(t, clientSession)

	select {
	case err := <-serverReady:
		if err != nil {
			t.Fatalf("Failed to create server Yamux Session: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for server Yamux Session to be ready")
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
		t.Fatalf("Client accepting stream failed: %v", err)
	}
	defer mustClose(t, clientStream)

	var lenBuf [2]byte
	if _, err := clientStream.Read(lenBuf[:]); err != nil {
		t.Fatalf("Failed to read proxyName length: %v", err)
	}
	nameLen := binary.BigEndian.Uint16(lenBuf[:])
	nameBuf := make([]byte, nameLen)
	if _, err := clientStream.Read(nameBuf); err != nil {
		t.Fatalf("Failed to read proxyName content: %v", err)
	}
	if string(nameBuf) != "test-tunnel" {
		t.Fatalf("proxyName error: %q", string(nameBuf))
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("openStreamToClient error: %v", result.err)
		}
		if result.stream == nil {
			t.Fatal("openStreamToClient should return valid conn")
		}
		_ = result.stream.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for openStreamToClient to return")
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
		t.Fatal("Should return error when no dataSession exists")
	}
}
