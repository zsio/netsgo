package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/bcrypt"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

func newDispatchTestServer(t *testing.T, initialized bool, serverAddr string) (*Server, func()) {
	t.Helper()

	adminStore, err := NewAdminStore(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatalf("Failed to create AdminStore: %v", err)
	}
	t.Cleanup(func() { _ = adminStore.Close() })
	adminStore.bcryptCost = bcrypt.MinCost // 测试用最低强度，避免 bcrypt 拖慢测试套件
	if initialized {
		if serverAddr == "" {
			serverAddr = "https://panel.example.com"
		}
		if err := adminStore.Initialize("admin", "password123", serverAddr, nil); err != nil {
			t.Fatalf("Failed to initialize AdminStore: %v", err)
		}
	}

	tunnelStore, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("Failed to create TunnelStore: %v", err)
	}

	s := New(0)
	s.auth.adminStore = adminStore
	s.store = tunnelStore

	return s, func() {}
}

func newManagementRequest(method, path, host string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Host = host
	req.RemoteAddr = "192.0.2.1:12345"
	return req
}

func newAuthenticatedManagementRequest(t *testing.T, s *Server, method, path, host string, body io.Reader) *http.Request {
	t.Helper()

	req := newManagementRequest(method, path, host, body)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	req.Header.Set("Authorization", "Bearer "+issueAdminToken(t, s))
	return req
}

func addLiveHTTPDispatchTunnel(t *testing.T, s *Server, clientID, tunnelName, domain string, backendAddr net.Addr) func() {
	t.Helper()

	client := &ClientConn{
		ID:         clientID,
		Info:       protocol.ClientInfo{Hostname: clientID + ".local"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(clientID, client)

	pipeClient, pipeServer := net.Pipe()
	var serverSession *yamux.Session
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverSession, _ = mux.NewServerSession(pipeServer, mux.DefaultConfig())
		wg.Done()
	}()
	clientSession, err := mux.NewClientSession(pipeClient, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create client yamux session: %v", err)
	}
	wg.Wait()

	client.dataMu.Lock()
	client.dataSession = serverSession
	client.dataMu.Unlock()

	client.proxyMu.Lock()
	client.proxies[tunnelName] = &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         tunnelName,
			Type:         protocol.ProxyTypeHTTP,
			LocalIP:      "127.0.0.1",
			LocalPort:    3000,
			Domain:       domain,
			ClientID:     clientID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		done: make(chan struct{}),
	}
	client.proxyMu.Unlock()

	seedStoredTunnel(t, s, clientID, protocol.ProxyNewRequest{
		Name:      tunnelName,
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    domain,
	}, protocol.ProxyStatusActive)

	stopRelay := make(chan struct{})
	go func() {
		for {
			stream, err := clientSession.AcceptStream()
			if err != nil {
				select {
				case <-stopRelay:
				default:
				}
				return
			}
			go relayDispatchStreamToBackend(stream, tunnelName, backendAddr.String())
		}
	}()

	return func() {
		close(stopRelay)
		_ = clientSession.Close()
		_ = serverSession.Close()
		_ = pipeClient.Close()
		_ = pipeServer.Close()
		s.clients.Delete(clientID)
	}
}

func relayDispatchStreamToBackend(stream *yamux.Stream, expectedTunnelName, backendAddr string) {
	defer func() { _ = stream.Close() }()

	var lenBuf [2]byte
	if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
		return
	}
	nameLen := int(lenBuf[0])<<8 | int(lenBuf[1])
	if nameLen <= 0 || nameLen > 1024 {
		return
	}

	nameBuf := make([]byte, nameLen)
	if _, err := io.ReadFull(stream, nameBuf); err != nil {
		return
	}
	if string(nameBuf) != expectedTunnelName {
		return
	}

	backendConn, err := net.Dial("tcp", backendAddr)
	if err != nil {
		return
	}
	defer func() { _ = backendConn.Close() }()

	mux.Relay(stream, backendConn)
}

func dialWSWithHost(t *testing.T, ts *httptest.Server, host, path string, subprotocols []string) (*websocket.Conn, *http.Response) {
	t.Helper()

	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil // disable proxy to prevent CONNECT tunnel when local http_proxy is set
	dialer.Subprotocols = subprotocols
	dialer.NetDialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var nd net.Dialer
		return nd.DialContext(ctx, network, strings.TrimPrefix(ts.URL, "http://"))
	}

	conn, resp, err := dialer.Dial("ws://"+host+path, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	return conn, resp
}

func TestDispatch_InternalControl_ValidSubprotocol_OnNonManagementHost(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	ts := httptest.NewServer(s.StartHTTPOnly())
	defer ts.Close()

	conn, _ := dialWSWithHost(t, ts, "app.example.com", "/ws/control", []string{protocol.WSSubProtocolControl})
	defer mustClose(t, conn)

	if got := conn.Subprotocol(); got != protocol.WSSubProtocolControl {
		t.Fatalf("Control channel negotiated subprotocol should be %q, got %q", protocol.WSSubProtocolControl, got)
	}
}

func TestDispatch_InternalData_ValidSubprotocol_OnNonManagementHost(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	ts := httptest.NewServer(s.StartHTTPOnly())
	defer ts.Close()

	conn, _ := dialWSWithHost(t, ts, "app.example.com", "/ws/data", []string{protocol.WSSubProtocolData})
	defer mustClose(t, conn)

	if got := conn.Subprotocol(); got != protocol.WSSubProtocolData {
		t.Fatalf("Data channel negotiated subprotocol should be %q, got %q", protocol.WSSubProtocolData, got)
	}
}

func TestDispatch_InternalControl_MissingSubprotocol_RoutesToBusinessTunnel(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Path", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	cleanupTunnel := addLiveHTTPDispatchTunnel(t, s, "client-http", "app-http", "app.example.com", backend.Listener.Addr())
	defer cleanupTunnel()

	ts := httptest.NewServer(s.StartHTTPOnly())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ws/control", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Host = "app.example.com"

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("Should continue to business proxy when subprotocol is missing, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Upstream-Path"); got != "/ws/control" {
		t.Fatalf("Business service should receive original path, got %q", got)
	}
}

func TestDispatch_HTTPTunnel_ManagementAPI_Blocked(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "hit")
		w.WriteHeader(http.StatusCreated)
	}))
	defer backend.Close()

	cleanupTunnel := addLiveHTTPDispatchTunnel(t, s, "client-http", "app-http", "app.example.com", backend.Listener.Addr())
	defer cleanupTunnel()

	ts := httptest.NewServer(s.StartHTTPOnly())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/admin/config", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Host = "app.example.com"

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Should not enter admin API when business domain matches, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Backend"); got != "hit" {
		t.Fatalf("Should enter business backend when business domain matches, got %q", got)
	}
}

func TestDispatch_HTTPTunnel_UnavailableStatuses_Return503(t *testing.T) {
	testCases := []struct {
		name   string
		status string
	}{
		{name: "pending", status: protocol.ProxyStatusPending},
		{name: "stopped", status: protocol.ProxyStatusStopped},
		{name: "error", status: protocol.ProxyStatusError},
		{name: "active but client offline", status: protocol.ProxyStatusActive},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newDispatchTestServer(t, true, "https://panel.example.com")
			seedStoredTunnel(t, s, "offline-client", protocol.ProxyNewRequest{
				Name:      "offline-http",
				Type:      protocol.ProxyTypeHTTP,
				LocalIP:   "127.0.0.1",
				LocalPort: 3000,
				Domain:    "app.example.com",
			}, tc.status)

			req := newManagementRequest(http.MethodGet, "http://app.example.com/", "app.example.com", nil)
			w := httptest.NewRecorder()

			s.StartHTTPOnly().ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("HTTP tunnel declared but not servicable should return 503, got %d", w.Code)
			}
		})
	}
}

func TestDispatch_HTTPTunnel_ProxyFail_Returns502(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	port := reserveTCPPort(t)
	unreachableAddr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	cleanupTunnel := addLiveHTTPDispatchTunnel(t, s, "client-http", "app-http", "app.example.com", unreachableAddr)
	defer cleanupTunnel()

	req := newManagementRequest(http.MethodGet, "http://app.example.com/", "app.example.com", nil)
	w := httptest.NewRecorder()

	s.StartHTTPOnly().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("Proxy dial failure should return 502, got %d", w.Code)
	}
}

func TestDispatch_UninitializedServer_DoesNotExposeManagementHostOnRandomHost(t *testing.T) {
	s, _ := newDispatchTestServer(t, false, "")

	req := newManagementRequest(http.MethodGet, "http://random.example.com/", "random.example.com", nil)
	w := httptest.NewRecorder()

	s.StartHTTPOnly().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Random Host should not fall back to admin frontend when uninitialized and no admin Host, got %d", w.Code)
	}
}

func TestDispatch_UnknownAPIPath_DoesNotFallbackToWebIndex(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	req := newManagementRequest(http.MethodGet, "http://panel.example.com/api/setup/status", "panel.example.com", nil)
	w := httptest.NewRecorder()

	s.StartHTTPOnly().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Deleted API should not fallback to frontend page, got %d", w.Code)
	}
}

func TestDispatch_ManagementHost_AdminAPI_WithSecurityHeaders(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", "panel.example.com", nil)
	w := httptest.NewRecorder()

	s.StartHTTPOnly().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Admin Host accessing admin API should succeed, got %d", w.Code)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("Admin plane response should have security headers, got %q", got)
	}
}

func TestDispatch_ExplicitLoopbackManagementHosts_AllowManagementAPI(t *testing.T) {
	testCases := []struct {
		name       string
		serverAddr string
		host       string
	}{
		{name: "localhost", serverAddr: "http://localhost", host: "localhost"},
		{name: "ipv4 loopback", serverAddr: "http://127.0.0.1", host: "127.0.0.1"},
		{name: "ipv6 loopback", serverAddr: "http://[::1]", host: "[::1]"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newDispatchTestServer(t, true, tc.serverAddr)

			req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", tc.host, nil)
			w := httptest.NewRecorder()

			s.StartHTTPOnly().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("Should allow access when loopback admin address is explicitly configured, got %d", w.Code)
			}
		})
	}
}

func TestDispatch_ExplicitLoopbackManagementHostWithoutPort_AllowsLoopbackEquivalenceOnListenPort(t *testing.T) {
	testCases := []struct {
		name      string
		reqHost   string
		wantAllow bool
	}{
		{name: "ipv4 same listen port", reqHost: "127.0.0.1:8080", wantAllow: true},
		{name: "ipv6 same listen port", reqHost: "[::1]:8080", wantAllow: true},
		{name: "localhost no port", reqHost: "localhost", wantAllow: true},
		{name: "ipv4 different port", reqHost: "127.0.0.1:9090", wantAllow: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newDispatchTestServer(t, true, "http://localhost")
			s.Port = 8080

			req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", tc.reqHost, nil)
			w := httptest.NewRecorder()

			s.StartHTTPOnly().ServeHTTP(w, req)

			if tc.wantAllow && w.Code != http.StatusOK {
				t.Fatalf("Explicit no-port loopback admin address should allow %s access, got %d", tc.reqHost, w.Code)
			}
			if !tc.wantAllow && w.Code != http.StatusNotFound {
				t.Fatalf("Different port %s should not match admin plane, got %d", tc.reqHost, w.Code)
			}
		})
	}
}

func TestDispatch_LoopbackHostsDoNotBypassManagementHostByDefault(t *testing.T) {
	testCases := []string{"localhost", "127.0.0.1", "[::1]"}

	for _, host := range testCases {
		t.Run(host, func(t *testing.T) {
			s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

			req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", host, nil)
			w := httptest.NewRecorder()

			s.StartHTTPOnly().ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("Non-loopback admin address should not treat %s as implicit entry, got %d", host, w.Code)
			}
		})
	}
}

func TestDispatch_AllowLoopbackManagementHostFallbackFlag(t *testing.T) {
	testCases := []string{"localhost", "127.0.0.1", "[::1]"}

	for _, host := range testCases {
		t.Run(host, func(t *testing.T) {
			s, _ := newDispatchTestServer(t, true, "https://panel.example.com")
			s.AllowLoopbackManagementHost = true

			req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", host, nil)
			w := httptest.NewRecorder()

			s.StartHTTPOnly().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("Should allow access after explicitly enabling loopback Host fallback, got %d", w.Code)
			}
		})
	}
}

func TestDispatch_NonManagementHost_NoTunnel_Returns404(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	req := newManagementRequest(http.MethodGet, "http://unknown.example.com/", "unknown.example.com", nil)
	w := httptest.NewRecorder()

	s.StartHTTPOnly().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Unknown Host should not fall back to admin plane, got %d", w.Code)
	}
}

func TestSecurityHeaders_NotOnHTTPTunnel(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cleanupTunnel := addLiveHTTPDispatchTunnel(t, s, "client-http", "app-http", "app.example.com", backend.Listener.Addr())
	defer cleanupTunnel()

	req := newManagementRequest(http.MethodGet, "http://app.example.com/", "app.example.com", nil)
	w := httptest.NewRecorder()

	s.StartHTTPOnly().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Business domain hitting proxy should return 200, got %d", w.Code)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "" {
		t.Fatalf("Business proxy response should not inject admin plane security headers, got %q", got)
	}
}

func TestDispatch_BusinessWebSocket_CanUpgrade(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer mustClose(t, conn)
		mt, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(mt, message)
	}))
	defer backend.Close()

	cleanupTunnel := addLiveHTTPDispatchTunnel(t, s, "client-http", "app-http", "app.example.com", backend.Listener.Addr())
	defer cleanupTunnel()

	ts := httptest.NewServer(s.StartHTTPOnly())
	defer ts.Close()

	conn, _ := dialWSWithHost(t, ts, "app.example.com", "/ws/chat", nil)
	defer mustClose(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("Failed to send business WebSocket message: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read business WebSocket echo: %v", err)
	}
	if string(payload) != "ping" {
		t.Fatalf("Business WebSocket echo expected ping, got %q", payload)
	}
}

func TestDispatch_SSE_ImmediateFlush(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "flusher required", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "data: hello\n\n")
		flusher.Flush()
	}))
	defer backend.Close()

	cleanupTunnel := addLiveHTTPDispatchTunnel(t, s, "client-http", "app-http", "app.example.com", backend.Listener.Addr())
	defer cleanupTunnel()

	ts := httptest.NewServer(s.StartHTTPOnly())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Host = "app.example.com"

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE proxy status code expected 200, got %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	done := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		done <- line
	}()

	select {
	case line := <-done:
		if strings.TrimSpace(line) != "data: hello" {
			t.Fatalf("SSE first line expected immediate data: hello, got %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSE first event failed to flush to client in time")
	}
}

// TestDispatch_LoopbackEquivalence 验证 serverListenAddr 返回 localhost:PORT 时，
// 用 127.0.0.1:PORT 或 [::1]:PORT 访问同样能进入管理面（Vite changeOrigin 场景）。
func TestDispatch_LoopbackEquivalence(t *testing.T) {
	testCases := []struct {
		name      string
		reqHost   string
		wantAllow bool
	}{
		{"127.0.0.1 same port", "127.0.0.1:8080", true},
		{"[::1] same port", "[::1]:8080", true},
		{"127.0.0.1 different port", "127.0.0.1:9090", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			adminStore, err := NewAdminStore(filepath.Join(t.TempDir(), "admin.db"))
			if err != nil {
				t.Fatalf("Failed to create AdminStore: %v", err)
			}
			t.Cleanup(func() { _ = adminStore.Close() })
			if err := adminStore.Initialize("admin", "password123", "", nil); err != nil {
				t.Fatalf("Initialization failed: %v", err)
			}
			tunnelStore, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
			if err != nil {
				t.Fatalf("Failed to create TunnelStore: %v", err)
			}
			s := New(8080)
			s.auth.adminStore = adminStore
			s.store = tunnelStore

			req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", tc.reqHost, nil)
			w := httptest.NewRecorder()

			s.StartHTTPOnly().ServeHTTP(w, req)

			if tc.wantAllow && w.Code == http.StatusNotFound {
				t.Fatalf("Loopback equivalent address %s should be able to access admin plane, got 404", tc.reqHost)
			}
			if !tc.wantAllow && w.Code != http.StatusNotFound {
				t.Fatalf("Different port %s should not match admin plane, got %d", tc.reqHost, w.Code)
			}
		})
	}
}
