package client

import (
	"io"
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
// Test helper: simulate a server-side WebSocket handler
// ============================================================

// mockServer simulates server-side behavior for client tests
func (ms *mockServer) writeControlJSON(conn *websocket.Conn, v any) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return conn.WriteJSON(v)
}

type mockServer struct {
	mu                   sync.Mutex
	receivedMsgs         []protocol.Message
	authResp             protocol.AuthResponse
	dataStatus           byte
	closeDataOnHandshake bool
	controlProtocols     [][]string
	dataProtocols        [][]string
	conns                []*websocket.Conn
	dataConns            []*websocket.Conn
	dataSessions         []io.Closer
	onMessage            func(msg protocol.Message) *protocol.Message // Callback invoked after receiving a message
}

func newMockServer(authSuccess bool) *mockServer {
	authResp := protocol.AuthResponse{
		Success:   authSuccess,
		Message:   "mock response",
		ClientID:  "mock_client_1",
		DataToken: "mock-data-token",
	}
	if authSuccess {
		authResp.Code = protocol.AuthCodeOK
	} else {
		authResp.Code = protocol.AuthCodeInvalidKey
	}
	return &mockServer{
		authResp:   authResp,
		dataStatus: protocol.DataHandshakeOK,
	}
}

func (ms *mockServer) controlHandler(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	ms.mu.Lock()
	ms.controlProtocols = append(ms.controlProtocols, websocket.Subprotocols(r))
	ms.mu.Unlock()

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

		// Handle messages
		switch msg.Type {
		case protocol.MsgTypeAuth:
			resp, _ := protocol.NewMessage(protocol.MsgTypeAuthResp, ms.authResp)
			ms.writeControlJSON(conn, resp)

		case protocol.MsgTypePing:
			pong, _ := protocol.NewMessage(protocol.MsgTypePong, nil)
			ms.writeControlJSON(conn, pong)

		case protocol.MsgTypeProbeReport:
			// The server does not reply to probe reports

		default:
			if ms.onMessage != nil {
				if reply := ms.onMessage(msg); reply != nil {
					ms.writeControlJSON(conn, reply)
				}
			}
		}
	}
}

func (ms *mockServer) dataHandler(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	ms.mu.Lock()
	ms.dataProtocols = append(ms.dataProtocols, websocket.Subprotocols(r))
	ms.mu.Unlock()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ms.mu.Lock()
	ms.dataConns = append(ms.dataConns, conn)
	ms.mu.Unlock()

	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return
	}
	if ms.closeDataOnHandshake {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "closing"),
			time.Now().Add(time.Second),
		)
		return
	}
	if messageType != websocket.BinaryMessage {
		return
	}

	clientID, dataToken, err := protocol.DecodeDataHandshake(payload)
	if err != nil {
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{protocol.DataHandshakeFail})
		return
	}
	if clientID != ms.authResp.ClientID || dataToken != ms.authResp.DataToken {
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{protocol.DataHandshakeAuthFail})
		return
	}
	if ms.dataStatus != protocol.DataHandshakeOK {
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{ms.dataStatus})
		return
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{protocol.DataHandshakeOK}); err != nil {
		return
	}

	session, err := mux.NewServerSession(mux.NewWSConn(conn), mux.DefaultConfig())
	if err != nil {
		return
	}

	ms.mu.Lock()
	ms.dataSessions = append(ms.dataSessions, session)
	ms.mu.Unlock()

	<-session.CloseChan()
}

// closeConns proactively closes all WebSocket connections
func (ms *mockServer) closeConns() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	for _, conn := range ms.conns {
		conn.Close()
	}
	for _, conn := range ms.dataConns {
		conn.Close()
	}
	for _, session := range ms.dataSessions {
		session.Close()
	}
	ms.conns = nil
	ms.dataConns = nil
	ms.dataSessions = nil
}

func (ms *mockServer) getReceivedMsgs() []protocol.Message {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	result := make([]protocol.Message, len(ms.receivedMsgs))
	copy(result, ms.receivedMsgs)
	return result
}

func (ms *mockServer) getControlProtocols() [][]string {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	result := make([][]string, len(ms.controlProtocols))
	for i := range ms.controlProtocols {
		result[i] = append([]string(nil), ms.controlProtocols[i]...)
	}
	return result
}

func (ms *mockServer) getDataProtocols() [][]string {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	result := make([][]string, len(ms.dataProtocols))
	for i := range ms.dataProtocols {
		result[i] = append([]string(nil), ms.dataProtocols[i]...)
	}
	return result
}

func newMockHTTPServer(ms *mockServer) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/control", ms.controlHandler)
	mux.HandleFunc("/ws/data", ms.dataHandler)
	return httptest.NewServer(mux)
}

// ============================================================
// Client integration tests
// ============================================================

func TestClient_ConnectAndAuth(t *testing.T) {
	ms := newMockServer(true)
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	// Start the client in the background (Start blocks in controlLoop)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Start()
	}()

	// Wait for the client to finish authentication
	time.Sleep(500 * time.Millisecond)

	// Verify that ClientID was set
	if c.CurrentClientID() != "mock_client_1" {
		t.Errorf("ClientID: want 'mock_client_1', got %q", c.CurrentClientID())
	}

	// Verify that the server received the authentication message
	msgs := ms.getReceivedMsgs()
	if len(msgs) == 0 {
		t.Fatal("server did not receive any messages")
	}
	if msgs[0].Type != protocol.MsgTypeAuth {
		t.Errorf("the first message should be auth, got %s", msgs[0].Type)
	}
}

func TestClientControlDial_SendsSubprotocol(t *testing.T) {
	ms := newMockServer(true)
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		protocols := ms.getControlProtocols()
		if len(protocols) > 0 {
			if len(protocols[0]) != 1 || protocols[0][0] != protocol.WSSubProtocolControl {
				t.Fatalf("control channel should send subprotocol %q, got %v", protocol.WSSubProtocolControl, protocols[0])
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatal("did not observe the control channel handshake")
}

func TestClient_HeartbeatSent(t *testing.T) {
	ms := newMockServer(true)
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()

	// The data channel connection fails quickly (~1s), then the heartbeat interval is 5s, so waiting 8s should observe at least one heartbeat
	time.Sleep(8 * time.Second)

	msgs := ms.getReceivedMsgs()
	pingCount := 0
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypePing {
			pingCount++
		}
	}

	if pingCount == 0 {
		t.Errorf("after waiting 7 seconds, expected at least 1 heartbeat, got %d", pingCount)
	}
}

func TestClient_ProbeReportSent(t *testing.T) {
	ms := newMockServer(true)
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()

	// Probe reporting happens after the data channel fails (~2s), and CPU sampling takes about 1s, so 5s is enough
	time.Sleep(5 * time.Second)

	msgs := ms.getReceivedMsgs()
	probeCount := 0
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypeProbeReport {
			probeCount++
		}
	}

	if probeCount == 0 {
		t.Error("expected at least 1 probe report (it reports immediately on startup)")
	}

	// Verify probe data contents
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypeProbeReport {
			var stats protocol.SystemStats
			if err := msg.ParsePayload(&stats); err != nil {
				t.Fatalf("failed to parse probe data: %v", err)
			}
			if stats.NumCPU == 0 {
				t.Error("probe data NumCPU should not be 0")
			}
			if stats.MemTotal == 0 {
				t.Error("probe data MemTotal should not be 0")
			}
			break
		}
	}
}

func TestClient_ServerDisconnect_WithReconnect(t *testing.T) {
	ms := newMockServer(true)
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true // Disable reconnect in this test to avoid blocking

	// Start the client in the background
	started := make(chan struct{})
	go func() {
		close(started)
		c.Start()
	}()
	<-started

	// Wait for the client to finish authentication and complete at least one probe collection
	time.Sleep(3 * time.Second)

	// Verify that the connection is healthy
	if c.CurrentClientID() == "" {
		t.Fatal("client should have completed authentication")
	}

	// Simulate a server disconnect
	ms.closeConns()
	ts.Close()

	// Verify that the done channel is closed (controlLoop closes it when it detects disconnect)
	select {
	case <-c.done:
		// Success: the client detected the disconnect
	case <-time.After(5 * time.Second):
		t.Error("the client's done channel should close within a reasonable time after the server disconnects")
	}
}

func TestClient_AuthFailed(t *testing.T) {
	ms := newMockServer(false) // Simulate authentication failure
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "wrong-key")

	err := c.Start()
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("expected Start to fail due to authentication failure, got: %v", err)
	}
}

func TestClient_DataChannelConnectErrorHandling(t *testing.T) {
	// Create a mock with no HTTP server and a closed listener
	c := New("ws://127.0.0.1:11111", "key")
	err := c.connectDataChannel()
	if err == nil {
		t.Error("expected an error when the target server cannot be reached")
	}
}

// ============================================================
// Reconnect tests
// ============================================================

func TestClient_Reconnect_AfterDisconnect(t *testing.T) {
	ms := newMockServer(true)
	ms.authResp = protocol.AuthResponse{
		Success:   true,
		Message:   "ok",
		ClientID:  "reconnect-client",
		DataToken: "reconnect-data-token",
		Code:      protocol.AuthCodeOK,
	}

	// Count authentication attempts
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
				resp, _ := protocol.NewMessage(protocol.MsgTypeAuthResp, ms.authResp)
				ms.writeControlJSON(conn, resp)
			case protocol.MsgTypePing:
				pong, _ := protocol.NewMessage(protocol.MsgTypePong, nil)
				ms.writeControlJSON(conn, pong)
			}
		}
	})
	httpMux.HandleFunc("/ws/data", ms.dataHandler)
	ts := httptest.NewServer(httpMux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	// Do not set DisableReconnect so reconnect can take effect

	// Start the client in the background
	go c.Start()
	time.Sleep(1 * time.Second)

	// Verify that the first authentication completed
	authMu.Lock()
	firstAuth := authCount
	authMu.Unlock()
	if firstAuth == 0 {
		t.Fatal("the initial authentication should have completed")
	}

	// Disconnect
	ms.closeConns()

	// Poll until reconnect succeeds to avoid flaky timing from fixed sleeps.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		authMu.Lock()
		finalAuth := authCount
		authMu.Unlock()
		if finalAuth > firstAuth {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	authMu.Lock()
	finalAuth := authCount
	authMu.Unlock()
	t.Errorf("authentication count should increase after reconnect, initial: %d, current: %d", firstAuth, finalAuth)
}

func TestClient_RetryInterval(t *testing.T) {
	recent := time.Now().Add(-1 * time.Minute)
	old := time.Now().Add(-6 * time.Minute)

	if interval := retryIntervalWithJitter(recent, 0); interval != retryShortInterval {
		t.Errorf("minimum retry interval within 1 minute after disconnect should be %v, got %v", retryShortInterval, interval)
	}
	if interval := retryIntervalWithJitter(old, 0); interval != retryLongInterval {
		t.Errorf("minimum retry interval after more than 5 minutes disconnected should be %v, got %v", retryLongInterval, interval)
	}

	if interval := retryIntervalWithJitter(recent, 1); interval != 4500*time.Millisecond {
		t.Errorf("maximum retry interval within 1 minute after disconnect should be 4.5s, got %v", interval)
	}
	if interval := retryIntervalWithJitter(old, 1); interval != 15*time.Second {
		t.Errorf("maximum retry interval after more than 5 minutes disconnected should be 15s, got %v", interval)
	}
}

func TestClient_Cleanup(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	c.ClientID = "cleanup-test"
	c.proxies.Store("proxy1", protocol.ProxyNewRequest{Name: "proxy1"})

	// Simulate creating a dataSession
	clientConn, serverConn := net.Pipe()
	session, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = session

	// Run cleanup
	c.cleanup()

	// Verify the cleanup results
	if c.CurrentClientID() != "" {
		t.Error("ClientID should be empty after cleanup")
	}

	_, ok := c.proxies.Load("proxy1")
	if ok {
		t.Error("proxies should be cleared after cleanup")
	}

	c.dataMu.RLock()
	if c.dataSession != nil {
		t.Error("dataSession should be nil after cleanup")
	}
	c.dataMu.RUnlock()

	serverConn.Close()
	clientConn.Close()
}

// ============================================================
// acceptStreamLoop tests
// ============================================================

func TestClient_AcceptStreamLoop_NilSession(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	// dataSession = nil should return immediately without panicking
	c.acceptStreamLoop()
}

func TestClient_AcceptStreamLoop_SessionClosed(t *testing.T) {
	c := New("ws://localhost:8080", "key")

	clientConn, serverConn := net.Pipe()
	session, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = session

	// Close the session immediately to simulate a disconnect
	session.Close()
	serverConn.Close()
	clientConn.Close()

	// It should exit safely
	c.acceptStreamLoop()
}

// ============================================================
// requestProxy tests
// ============================================================

func TestClient_RequestProxy(t *testing.T) {
	ms := newMockServer(true)
	ms.onMessage = func(msg protocol.Message) *protocol.Message {
		if msg.Type == protocol.MsgTypeProxyCreate {
			resp, _ := protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
				Success:    true,
				Message:    "ok",
				RemotePort: 18080,
			})
			return resp
		}
		return nil
	}

	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	// Start the client (it blocks in controlLoop in the background)
	go c.Start()
	time.Sleep(500 * time.Millisecond) // Wait for authentication and the data channel attempt to complete

	// Call requestProxy manually
	cfg := protocol.ProxyNewRequest{
		Name:       "test-proxy",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  8080,
		RemotePort: 18080,
	}
	c.requestProxy(cfg)

	// Verify that the server received the proxy_create message
	time.Sleep(200 * time.Millisecond)
	msgs := ms.getReceivedMsgs()
	found := false
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypeProxyCreate {
			found = true
			break
		}
	}
	if !found {
		t.Error("server should receive the proxy_create message")
	}

	// Verify that the config was registered in the proxies sync.Map
	_, ok := c.proxies.Load("test-proxy")
	if !ok {
		t.Error("requestProxy should register the config in proxies")
	}
}

// ============================================================
// controlLoop — create response handling tests
// ============================================================

func TestClient_ControlLoop_ProxyCreateResp_Success(t *testing.T) {
	ms := newMockServer(true)
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()
	time.Sleep(500 * time.Millisecond)

	// The server proactively sends proxy_create_resp (success)
	ms.mu.Lock()
	var conn *websocket.Conn
	if len(ms.conns) > 0 {
		conn = ms.conns[len(ms.conns)-1]
	}
	ms.mu.Unlock()
	if conn != nil {
		resp, _ := protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
			Success:    true,
			Message:    "tunnel created",
			RemotePort: 19090,
		})
		if err := ms.writeControlJSON(conn, resp); err != nil {
			t.Fatalf("server failed to send proxy_create_resp: %v", err)
		}
	}

	// Wait for the client to handle it; not crashing is enough
	time.Sleep(200 * time.Millisecond)
}

func TestClient_ControlLoop_ProxyCreateResp_Failure(t *testing.T) {
	ms := newMockServer(true)
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()
	time.Sleep(500 * time.Millisecond)

	// The server proactively sends proxy_create_resp (failure)
	ms.mu.Lock()
	var conn *websocket.Conn
	if len(ms.conns) > 0 {
		conn = ms.conns[len(ms.conns)-1]
	}
	ms.mu.Unlock()
	if conn != nil {
		resp, _ := protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
			Success: false,
			Message: "port conflict",
		})
		if err := ms.writeControlJSON(conn, resp); err != nil {
			t.Fatalf("server failed to send proxy_create_resp: %v", err)
		}
	}

	time.Sleep(200 * time.Millisecond)
}

func TestClient_ControlLoop_ServerProvisionSendsProvisionAck(t *testing.T) {
	provisionAck := make(chan protocol.ProxyProvisionAck, 1)
	ackErr := make(chan error, 1)
	ms := newMockServer(true)
	ms.onMessage = func(msg protocol.Message) *protocol.Message {
		if msg.Type != protocol.MsgTypeProxyProvisionAck {
			return nil
		}
		var resp protocol.ProxyProvisionAck
		if err := msg.ParsePayload(&resp); err != nil {
			ackErr <- err
			return nil
		}
		provisionAck <- resp
		return nil
	}
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()
	time.Sleep(500 * time.Millisecond)

	ms.mu.Lock()
	if len(ms.conns) == 0 {
		ms.mu.Unlock()
		t.Fatal("client control connection was not established")
	}
	conn := ms.conns[len(ms.conns)-1]
	ms.mu.Unlock()
	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyProvision, protocol.ProxyProvisionRequest{
		Name:       "server-pushed-proxy",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  8080,
		RemotePort: 19090,
	})
	err := ms.writeControlJSON(conn, msg)
	if err != nil {
		t.Fatalf("server failed to send proxy_provision: %v", err)
	}

	select {
	case err := <-ackErr:
		t.Fatalf("failed to parse proxy_provision_ack: %v", err)
	case resp := <-provisionAck:
		if resp.Name != "server-pushed-proxy" {
			t.Fatalf("wrong provision ack name: %s", resp.Name)
		}
		if !resp.Accepted {
			t.Fatal("provision ack should be marked accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive proxy_provision_ack returned by the client")
	}
}

func TestClient_ControlLoop_ServerProvisionDoesNotGateOnBackendHealth(t *testing.T) {
	ackPayload := make(chan map[string]any, 1)
	ms := newMockServer(true)
	ms.onMessage = func(msg protocol.Message) *protocol.Message {
		if msg.Type != protocol.MsgTypeProxyProvisionAck {
			return nil
		}
		var payload map[string]any
		if err := msg.ParsePayload(&payload); err != nil {
			t.Fatalf("failed to parse proxy_provision_ack: %v", err)
		}
		ackPayload <- payload
		return nil
	}
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true

	go c.Start()
	time.Sleep(500 * time.Millisecond)

	ms.mu.Lock()
	if len(ms.conns) == 0 {
		ms.mu.Unlock()
		t.Fatal("client control connection was not established")
	}
	conn := ms.conns[len(ms.conns)-1]
	ms.mu.Unlock()
	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyProvision, protocol.ProxyProvisionRequest{
		Name:       "unreachable-backend",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  1,
		RemotePort: 19091,
	})
	err := ms.writeControlJSON(conn, msg)
	if err != nil {
		t.Fatalf("server failed to send proxy_provision: %v", err)
	}

	select {
	case payload := <-ackPayload:
		if payload["name"] != "unreachable-backend" {
			t.Fatalf("wrong ack name: %v", payload["name"])
		}
		accepted, ok := payload["accepted"].(bool)
		if !ok || !accepted {
			t.Fatalf("ack accepted should be true, got %#v", payload["accepted"])
		}
		if _, exists := payload["remote_port"]; exists {
			t.Fatalf("proxy_provision_ack should not contain remote_port: %v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive proxy_provision_ack returned by the client")
	}

	if _, ok := c.proxies.Load("unreachable-backend"); !ok {
		t.Fatal("tunnel config should be cached after successful provision")
	}
}

func TestClient_FailRuntime_DoesNotCloseNewRuntime(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	oldRT := c.beginRuntime()
	newRT := c.beginRuntime()

	oldClosed := make(chan struct{})
	go func() {
		<-oldRT.done
		close(oldClosed)
	}()

	c.failRuntime(oldRT, "old_runtime_failed")

	select {
	case <-oldClosed:
	case <-time.After(time.Second):
		t.Fatal("the old runtime should be closed after failRuntime")
	}

	select {
	case <-newRT.done:
		t.Fatal("closing the old runtime should not affect the new runtime")
	default:
	}

	if got := c.getCurrentRuntime(); got != newRT {
		t.Fatal("the current runtime should remain the new runtime")
	}
}

func TestClient_Cleanup_WaitsForRuntimeGoroutines(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	rt := c.beginRuntime()

	exited := make(chan struct{})
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		<-rt.done
		time.Sleep(50 * time.Millisecond)
		close(exited)
	}()

	start := time.Now()
	c.cleanup()

	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("cleanup should wait for the runtime goroutine to exit")
	}

	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("cleanup should wait on the WaitGroup instead of returning immediately")
	}
}

// ============================================================
// connectDataChannel full handshake tests
// ============================================================

func TestClient_ConnectDataChannel_Success(t *testing.T) {
	ms := newMockServer(true)
	ms.authResp.ClientID = "test-client-dc"
	ms.authResp.DataToken = "test-dc-token"
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	c := New("ws"+strings.TrimPrefix(ts.URL, "http"), "key")
	c.ClientID = ms.authResp.ClientID
	c.dataToken = ms.authResp.DataToken

	err := c.connectDataChannel()
	if err != nil {
		t.Fatalf("connectDataChannel should succeed: %v", err)
	}

	c.dataMu.RLock()
	hasSession := c.dataSession != nil
	c.dataMu.RUnlock()

	if !hasSession {
		t.Error("dataSession should not be nil after a successful handshake")
	}
}

func TestClient_ConnectDataChannel_Rejected(t *testing.T) {
	ms := newMockServer(true)
	ms.authResp.ClientID = "rejected-client"
	ms.authResp.DataToken = "some-token"
	ms.dataStatus = protocol.DataHandshakeFail
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	c := New("ws"+strings.TrimPrefix(ts.URL, "http"), "key")
	c.ClientID = ms.authResp.ClientID
	c.dataToken = ms.authResp.DataToken

	err := c.connectDataChannel()
	if err == nil {
		t.Error("should return an error when the server rejects the handshake")
	}
	if !strings.Contains(err.Error(), "handshake rejected") {
		t.Errorf("error should contain 'handshake rejected', got: %v", err)
	}
}

func TestClientDataDial_SendsSubprotocol(t *testing.T) {
	ms := newMockServer(true)
	ms.authResp.ClientID = "subprotocol-client"
	ms.authResp.DataToken = "subprotocol-token"
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	c := New("ws"+strings.TrimPrefix(ts.URL, "http"), "key")
	c.ClientID = ms.authResp.ClientID
	c.dataToken = ms.authResp.DataToken

	if err := c.connectDataChannel(); err != nil {
		t.Fatalf("connectDataChannel should succeed: %v", err)
	}

	protocols := ms.getDataProtocols()
	if len(protocols) == 0 {
		t.Fatal("did not observe the data channel handshake")
	}
	if len(protocols[0]) != 1 || protocols[0][0] != protocol.WSSubProtocolData {
		t.Fatalf("data channel should send subprotocol %q, got %v", protocol.WSSubProtocolData, protocols[0])
	}
}

func TestClient_ConnectDataChannel_HandlesCloseWithoutStatusByte(t *testing.T) {
	ms := newMockServer(true)
	ms.authResp.ClientID = "close-without-status"
	ms.authResp.DataToken = "close-token"
	ms.closeDataOnHandshake = true
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	c := New("ws"+strings.TrimPrefix(ts.URL, "http"), "key")
	c.ClientID = ms.authResp.ClientID
	c.dataToken = ms.authResp.DataToken

	if err := c.connectDataChannel(); err == nil {
		t.Fatal("should return an error when the connection closes directly during the handshake")
	}
}

func TestClient_ConnectDataChannel_NoPort(t *testing.T) {
	// Case where ServerAddr has no port
	c := New("ws://some-host-without-port-1234567.invalid", "key")
	c.ClientID = "no-port-client"
	c.dataToken = "some-token"
	err := c.connectDataChannel()
	if err == nil {
		t.Error("should return an error when connection is not possible")
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
			t.Errorf("normalizeServerAddr(%q) = %q, want %q", tt.input, c.ServerAddr, tt.expected)
		}
		if c.useTLS != tt.useTLS {
			t.Errorf("normalizeServerAddr(%q): useTLS = %v, want %v", tt.input, c.useTLS, tt.useTLS)
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
			t.Errorf("deriveControlURL() for %q = %q, want %q", tt.input, url, tt.expected)
		}
	}
}

func TestDeriveDataURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ws://localhost:8080", "ws://localhost:8080/ws/data"},
		{"wss://localhost:8080", "wss://localhost:8080/ws/data"},
		{"http://localhost:8080", "ws://localhost:8080/ws/data"},
		{"https://tunnel.example.com", "wss://tunnel.example.com/ws/data"},
	}

	for _, tt := range tests {
		c := New(tt.input, "key")
		c.normalizeServerAddr()
		url := c.deriveDataURL()
		if url != tt.expected {
			t.Errorf("deriveDataURL() for %q = %q, want %q", tt.input, url, tt.expected)
		}
	}
}
