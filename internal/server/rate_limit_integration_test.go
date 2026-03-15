package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

// setupRateLimitedServer 创建一个带有自定义速率限制器的测试 Server
func setupRateLimitedServer(t *testing.T, loginCfg, setupCfg RateLimiterConfig) (*Server, func()) {
	t.Helper()
	s, cleanup := setupTestServerWithDB(t, true)

	s.loginLimiter = NewRateLimiter(loginCfg)
	s.setupLimiter = NewRateLimiter(setupCfg)

	origCleanup := cleanup
	cleanup = func() {
		s.loginLimiter.Stop()
		s.setupLimiter.Stop()
		origCleanup()
	}

	return s, cleanup
}

// ============================================================
// Login 速率限制集成测试
// ============================================================

func TestLogin_RateLimitBlocksAfterMaxRequests(t *testing.T) {
	s, cleanup := setupRateLimitedServer(t, RateLimiterConfig{
		WindowSize:  time.Minute,
		MaxRequests: 3, // 窗口内最多 3 次
		MaxFailures: 100,
		LockoutPeriod: time.Hour,
	}, RateLimiterConfig{
		WindowSize:  time.Minute,
		MaxRequests: 100,
		MaxFailures: 100,
		LockoutPeriod: time.Hour,
	})
	defer cleanup()

	loginBody := []byte(`{"username":"admin","password":"password123"}`)

	// 前 3 次应该成功
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次登录期望 200，得到 %d", i+1, w.Code)
		}
	}

	// 第 4 次应被限速
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超过窗口限制后期望 429，得到 %d", w.Code)
	}
	if retryAfter := w.Header().Get("Retry-After"); retryAfter == "" {
		t.Error("429 响应应包含 Retry-After 头")
	}
}

func TestLogin_RateLimitLockoutAfterFailures(t *testing.T) {
	s, cleanup := setupRateLimitedServer(t, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,           // 不受请求总数限制
		MaxFailures:   3,             // 3 次失败触发锁定
		LockoutPeriod: 200 * time.Millisecond, // 短锁定方便测试
	}, RateLimiterConfig{
		WindowSize:  time.Minute,
		MaxRequests: 100,
		MaxFailures: 100,
		LockoutPeriod: time.Hour,
	})
	defer cleanup()

	wrongBody := []byte(`{"username":"admin","password":"wrong"}`)

	// 连续 3 次错误密码
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(wrongBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.2:12345"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码期望 401，得到 %d", i+1, w.Code)
		}
	}

	// 第 4 次应被锁定（即使用了正确密码）
	correctBody := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.2:12345"
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("连续失败后期望 429 锁定，得到 %d", w.Code)
	}

	// 不同 IP 不受影响
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "10.0.0.99:12345"
	w2 := httptest.NewRecorder()
	s.handleAPILogin(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("不同 IP 不应受锁定影响，得到 %d", w2.Code)
	}

	// 等待锁定过期后恢复
	time.Sleep(250 * time.Millisecond)

	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req3.Header.Set("Content-Type", "application/json")
	req3.RemoteAddr = "10.0.0.2:12345"
	w3 := httptest.NewRecorder()
	s.handleAPILogin(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("锁定过期后应恢复，得到 %d", w3.Code)
	}
}

func TestLogin_RateLimitResetOnSuccess(t *testing.T) {
	s, cleanup := setupRateLimitedServer(t, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   3,
		LockoutPeriod: time.Hour,
	}, RateLimiterConfig{
		WindowSize:  time.Minute,
		MaxRequests: 100,
		MaxFailures: 100,
		LockoutPeriod: time.Hour,
	})
	defer cleanup()

	wrongBody := []byte(`{"username":"admin","password":"wrong"}`)
	correctBody := []byte(`{"username":"admin","password":"password123"}`)

	// 2 次失败（未达阈值 3）
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(wrongBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.3:12345"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)
	}

	// 成功登录，重置计数
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.3:12345"
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("成功登录期望 200，得到 %d", w.Code)
	}

	// 再连续 2 次失败（从 0 开始，不应触发锁定）
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(wrongBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.3:12345"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)
	}

	// 再次成功登录（不应被锁定，因为之前重置了计数）
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "10.0.0.3:12345"
	w2 := httptest.NewRecorder()
	s.handleAPILogin(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("重置后再次失败未达阈值，不应被锁定，得到 %d", w2.Code)
	}
}

// ============================================================
// Setup 速率限制集成测试
// ============================================================

func TestSetup_RateLimitBlocksAfterMaxRequests(t *testing.T) {
	// 使用未初始化的 Server
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	s.setupLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   2, // 窗口内最多 2 次
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	})
	defer s.setupLimiter.Stop()

	body := []byte(`{"admin":{"username":"admin","password":"password123"},"server_addr":"localhost","allowed_ports":[]}`)

	// 第 1 次初始化成功
	req := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.1.1:12345"
	w := httptest.NewRecorder()
	s.handleSetupInit(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("首次初始化期望 201，得到 %d (body: %s)", w.Code, w.Body.String())
	}

	// 第 2 次请求（已初始化会返回 403，但仍消耗 rate limit 配额）
	req2 := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "10.0.1.1:12345"
	w2 := httptest.NewRecorder()
	s.handleSetupInit(w2, req2)

	// 第 3 次应被限速
	req3 := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req3.Header.Set("Content-Type", "application/json")
	req3.RemoteAddr = "10.0.1.1:12345"
	w3 := httptest.NewRecorder()
	s.handleSetupInit(w3, req3)

	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("超过限制后期望 429，得到 %d", w3.Code)
	}
}

// ============================================================
// Client 认证速率限制集成测试
// ============================================================

func TestClient_RateLimitBlocksAfterFailures(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)

	s.clientLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   3,             // 3 次失败触发锁定
		LockoutPeriod: 200 * time.Millisecond,
	})
	defer s.clientLimiter.Stop()

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"

	// 连续 3 次使用错误 Key 认证
	for i := 0; i < 3; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("第 %d 次 WebSocket 连接失败: %v", i+1, err)
		}

		authReq := protocol.AuthRequest{
			Key:       "wrong-key",
			InstallID: "install-rate-test",
			Client: protocol.ClientInfo{
				Hostname: "rate-test-host",
				OS:       "linux",
				Arch:     "amd64",
				Version:  "0.1.0",
			},
		}
		msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
		conn.WriteJSON(msg)

		// 等待服务端关闭连接（认证失败）
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, err = conn.ReadMessage()
		if err == nil {
			t.Fatalf("第 %d 次错误 Key 应导致连接关闭", i+1)
		}
		conn.Close()
	}

	// 第 4 次连接：即使用了正确 Key，也应被限速关闭
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-rate-test",
		Client: protocol.ClientInfo{
			Hostname: "rate-test-host",
			OS:       "linux",
			Arch:     "amd64",
			Version:  "0.1.0",
		},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	conn.WriteJSON(msg)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("被锁定的 IP 应无法通过认证")
	}

	// 等待锁定过期后恢复
	time.Sleep(250 * time.Millisecond)

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("锁定过期后 WebSocket 连接失败: %v", err)
	}
	defer conn2.Close()

	authReq2 := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-rate-test-recovery",
		Client: protocol.ClientInfo{
			Hostname: "rate-test-host-2",
			OS:       "linux",
			Arch:     "amd64",
			Version:  "0.1.0",
		},
	}
	msg2, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq2)
	conn2.WriteJSON(msg2)

	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Message
	if err := conn2.ReadJSON(&resp); err != nil {
		t.Fatalf("锁定过期后认证应成功: %v", err)
	}
	if resp.Type != protocol.MsgTypeAuthResp {
		t.Fatalf("期望 auth_resp，得到 %s", resp.Type)
	}
}

func TestLogin_RateLimitXForwardedFor(t *testing.T) {
	s, cleanup := setupRateLimitedServer(t, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   2,
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	}, RateLimiterConfig{
		WindowSize:  time.Minute,
		MaxRequests: 100,
		MaxFailures: 100,
		LockoutPeriod: time.Hour,
	})
	defer cleanup()

	// 配置反代模式：tls.mode=off + 代理 IP 10.0.0.0/8 受信任
	s.TLS = &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"10.0.0.0/8"},
	}

	loginBody := []byte(`{"username":"admin","password":"password123"}`)

	// 通过 XFF 头识别真实 IP（来源 10.0.0.1 在受信任列表中）
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "203.0.113.50")
		req.RemoteAddr = "10.0.0.1:80" // 受信任代理 IP
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次请求期望 200，得到 %d", i+1, w.Code)
		}
	}

	// 同一真实 IP 的第 3 次请求应被限速
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	req.RemoteAddr = "10.0.0.1:80"
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("XFF 相同 IP 超限后期望 429，得到 %d", w.Code)
	}

	// 不同真实 IP 不受影响
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Forwarded-For", "198.51.100.1")
	req2.RemoteAddr = "10.0.0.1:80"
	w2 := httptest.NewRecorder()
	s.handleAPILogin(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("不同真实 IP 不应受限速影响，得到 %d", w2.Code)
	}

	// 来自非受信任代理的请求，即使带了 XFF 也应使用 RemoteAddr
	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Forwarded-For", "203.0.113.50") // 尝试伪造
	req3.RemoteAddr = "1.2.3.4:80"                     // 非受信任 IP
	w3 := httptest.NewRecorder()
	s.handleAPILogin(w3, req3)

	// 应该使用 RemoteAddr (1.2.3.4) 作为限速 key，而不是 XFF 中的地址
	if w3.Code != http.StatusOK {
		t.Fatalf("非受信来源应使用 RemoteAddr 限速，得到 %d", w3.Code)
	}
}
