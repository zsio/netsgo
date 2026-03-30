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
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}
	adminStore.bcryptCost = bcrypt.MinCost // 测试用最低强度，避免 bcrypt 拖慢测试套件
	if initialized {
		if serverAddr == "" {
			serverAddr = "https://panel.example.com"
		}
		if err := adminStore.Initialize("admin", "password123", serverAddr, nil); err != nil {
			t.Fatalf("初始化 AdminStore 失败: %v", err)
		}
	}

	tunnelStore, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("创建 TunnelStore 失败: %v", err)
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
		t.Fatalf("创建 client yamux session 失败: %v", err)
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
	defer stream.Close()

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
	defer backendConn.Close()

	mux.Relay(stream, backendConn)
}

func dialWSWithHost(t *testing.T, ts *httptest.Server, host, path string, subprotocols []string) (*websocket.Conn, *http.Response) {
	t.Helper()

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = subprotocols
	dialer.NetDialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var nd net.Dialer
		return nd.DialContext(ctx, network, strings.TrimPrefix(ts.URL, "http://"))
	}

	conn, resp, err := dialer.Dial("ws://"+host+path, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	return conn, resp
}

func TestDispatch_InternalControl_ValidSubprotocol_OnNonManagementHost(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	ts := httptest.NewServer(s.StartHTTPOnly())
	defer ts.Close()

	conn, _ := dialWSWithHost(t, ts, "app.example.com", "/ws/control", []string{protocol.WSSubProtocolControl})
	defer conn.Close()

	if got := conn.Subprotocol(); got != protocol.WSSubProtocolControl {
		t.Fatalf("控制通道协商子协议应为 %q，得到 %q", protocol.WSSubProtocolControl, got)
	}
}

func TestDispatch_InternalData_ValidSubprotocol_OnNonManagementHost(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	ts := httptest.NewServer(s.StartHTTPOnly())
	defer ts.Close()

	conn, _ := dialWSWithHost(t, ts, "app.example.com", "/ws/data", []string{protocol.WSSubProtocolData})
	defer conn.Close()

	if got := conn.Subprotocol(); got != protocol.WSSubProtocolData {
		t.Fatalf("数据通道协商子协议应为 %q，得到 %q", protocol.WSSubProtocolData, got)
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
		t.Fatalf("创建请求失败: %v", err)
	}
	req.Host = "app.example.com"

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("缺失子协议时应继续走业务代理，得到 %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Upstream-Path"); got != "/ws/control" {
		t.Fatalf("业务服务应收到原始 path，得到 %q", got)
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
		t.Fatalf("创建请求失败: %v", err)
	}
	req.Host = "app.example.com"

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("业务域名命中时不应进入管理 API，得到 %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Backend"); got != "hit" {
		t.Fatalf("业务域名命中时应进入业务后端，得到 %q", got)
	}
}

func TestDispatch_HTTPTunnel_UnavailableStatuses_Return503(t *testing.T) {
	testCases := []struct {
		name   string
		status string
	}{
		{name: "pending", status: protocol.ProxyStatusPending},
		{name: "paused", status: protocol.ProxyStatusPaused},
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
				t.Fatalf("命中已声明但不可服务的 HTTP 隧道应返回 503，得到 %d", w.Code)
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
		t.Fatalf("代理拨号失败应返回 502，得到 %d", w.Code)
	}
}

func TestDispatch_SetupPhase_AllowsFrontendAndBlocksOtherAPIs(t *testing.T) {
	s, _ := newDispatchTestServer(t, false, "")

	rootReq := newManagementRequest(http.MethodGet, "http://random.example.com/", "random.example.com", nil)
	rootResp := httptest.NewRecorder()
	s.StartHTTPOnly().ServeHTTP(rootResp, rootReq)
	if rootResp.Code != http.StatusOK {
		t.Fatalf("setup 阶段任意 Host 访问管理前端应放行，得到 %d", rootResp.Code)
	}

	assetReq := newManagementRequest(http.MethodGet, "http://random.example.com/assets/app.js", "random.example.com", nil)
	assetResp := httptest.NewRecorder()
	s.StartHTTPOnly().ServeHTTP(assetResp, assetReq)
	if assetResp.Code != http.StatusOK {
		t.Fatalf("setup 阶段静态资源应放行，得到 %d", assetResp.Code)
	}

	adminReq := newManagementRequest(http.MethodGet, "http://random.example.com/api/admin/config", "random.example.com", nil)
	adminResp := httptest.NewRecorder()
	s.StartHTTPOnly().ServeHTTP(adminResp, adminReq)
	if adminResp.Code == http.StatusOK {
		t.Fatalf("setup 阶段非 setup API 不应被直接放行")
	}
}

func TestDispatch_ManagementHost_AdminAPI_WithSecurityHeaders(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")

	req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", "panel.example.com", nil)
	w := httptest.NewRecorder()

	s.StartHTTPOnly().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("管理 Host 访问管理 API 应成功，得到 %d", w.Code)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("管理面响应应带安全头，得到 %q", got)
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
				t.Fatalf("显式配置 loopback 管理地址时应允许访问，得到 %d", w.Code)
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
				t.Fatalf("非 loopback 管理地址下，%s 不应再作为隐含入口，得到 %d", host, w.Code)
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
				t.Fatalf("显式开启 loopback Host 兜底后应允许访问，得到 %d", w.Code)
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
		t.Fatalf("未知 Host 不应回落到管理面，得到 %d", w.Code)
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
		t.Fatalf("业务域名命中代理应返回 200，得到 %d", w.Code)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "" {
		t.Fatalf("业务代理响应不应注入管理面安全头，得到 %q", got)
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
		defer conn.Close()
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
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("发送业务 WebSocket 消息失败: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取业务 WebSocket echo 失败: %v", err)
	}
	if string(payload) != "ping" {
		t.Fatalf("业务 WebSocket echo 期望 ping，得到 %q", payload)
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
		t.Fatalf("创建请求失败: %v", err)
	}
	req.Host = "app.example.com"

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 代理状态码期望 200，得到 %d", resp.StatusCode)
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
			t.Fatalf("SSE 首行期望立即收到 data: hello，得到 %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSE 首条事件未能及时 flush 到客户端")
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
				t.Fatalf("创建 AdminStore 失败: %v", err)
			}
			if err := adminStore.Initialize("admin", "password123", "", nil); err != nil {
				t.Fatalf("初始化失败: %v", err)
			}
			tunnelStore, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
			if err != nil {
				t.Fatalf("创建 TunnelStore 失败: %v", err)
			}
			s := New(8080)
			s.auth.adminStore = adminStore
			s.store = tunnelStore

			req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", tc.reqHost, nil)
			w := httptest.NewRecorder()

			s.StartHTTPOnly().ServeHTTP(w, req)

			if tc.wantAllow && w.Code == http.StatusNotFound {
				t.Fatalf("loopback 等价地址 %s 应能访问管理面，得到 404", tc.reqHost)
			}
			if !tc.wantAllow && w.Code != http.StatusNotFound {
				t.Fatalf("不同端口 %s 不应匹配管理面，得到 %d", tc.reqHost, w.Code)
			}
		})
	}
}
