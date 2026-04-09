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

// setupRateLimitedServer creates a test server with a custom rate limiter
func setupRateLimitedServer(t *testing.T, loginCfg, setupCfg RateLimiterConfig) (*Server, func()) {
	t.Helper()
	s, cleanup := setupTestServerWithDB(t, true)

	s.auth.loginLimiter = NewRateLimiter(loginCfg)

	origCleanup := cleanup
	cleanup = func() {
		s.auth.loginLimiter.Stop()
		origCleanup()
	}

	return s, cleanup
}

// ============================================================
// Login rate limiting integration tests
// ============================================================

func TestLogin_RateLimitBlocksAfterMaxRequests(t *testing.T) {
	s, cleanup := setupRateLimitedServer(t, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   3, // At most 3 requests in the window
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	}, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	})
	defer cleanup()

	loginBody := []byte(`{"username":"admin","password":"password123"}`)

	// The first 3 attempts should succeed
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("login attempt #%d: want 200, got %d", i+1, w.Code)
		}
	}

	// The 4th attempt should be rate limited
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after exceeding the window limit, want 429, got %d", w.Code)
	}
	if retryAfter := w.Header().Get("Retry-After"); retryAfter == "" {
		t.Error("429 response should include a Retry-After header")
	}
}

func TestLogin_RateLimitLockoutAfterFailures(t *testing.T) {
	s, cleanup := setupRateLimitedServer(t, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,                    // Not limited by total request count
		MaxFailures:   3,                      // 3 failures trigger lockout
		LockoutPeriod: 200 * time.Millisecond, // Short lockout period for easier testing
	}, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	})
	defer cleanup()

	wrongBody := []byte(`{"username":"admin","password":"wrong"}`)

	// Three consecutive wrong passwords
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(wrongBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.2:12345"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong-password attempt #%d: want 401, got %d", i+1, w.Code)
		}
	}

	// The 4th attempt should be locked out (even with the correct password)
	correctBody := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.2:12345"
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after consecutive failures, expected 429 lockout, got %d", w.Code)
	}

	// A different IP should be unaffected
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "10.0.0.99:12345"
	w2 := httptest.NewRecorder()
	s.handleAPILogin(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("a different IP should not be affected by the lockout, got %d", w2.Code)
	}

	// Wait for the lockout to expire and recover
	time.Sleep(250 * time.Millisecond)

	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req3.Header.Set("Content-Type", "application/json")
	req3.RemoteAddr = "10.0.0.2:12345"
	w3 := httptest.NewRecorder()
	s.handleAPILogin(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("after lockout expiry, it should recover, got %d", w3.Code)
	}
}

func TestLogin_RateLimitResetOnSuccess(t *testing.T) {
	s, cleanup := setupRateLimitedServer(t, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   3,
		LockoutPeriod: time.Hour,
	}, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	})
	defer cleanup()

	wrongBody := []byte(`{"username":"admin","password":"wrong"}`)
	correctBody := []byte(`{"username":"admin","password":"password123"}`)

	// 2 failures (below the threshold of 3)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(wrongBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.3:12345"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)
	}

	// Successful login resets the counters
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.3:12345"
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("successful login: want 200, got %d", w.Code)
	}

	// Then 2 more consecutive failures (starting from 0, should not trigger lockout)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(wrongBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.3:12345"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)
	}

	// Successful login again (should not be locked because the counter was reset earlier)
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(correctBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "10.0.0.3:12345"
	w2 := httptest.NewRecorder()
	s.handleAPILogin(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("after reset, failing again below the threshold should not trigger lockout, got %d", w2.Code)
	}
}

// ============================================================
// Client authentication rate limiting integration tests
// ============================================================

func TestClient_RateLimitBlocksAfterFailures(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)

	s.auth.clientLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   3, // 3 failures trigger lockout
		LockoutPeriod: 200 * time.Millisecond,
	})
	defer s.auth.clientLimiter.Stop()

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"

	// Authenticate three times in a row with the wrong key
	for i := 0; i < 3; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket connection #%d failed: %v", i+1, err)
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

		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var resp protocol.Message
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("failed to read auth response for wrong-key attempt #%d: %v", i+1, err)
		}
		if resp.Type != protocol.MsgTypeAuthResp {
			t.Fatalf("wrong-key attempt #%d: want auth_resp, got %s", i+1, resp.Type)
		}
		var authResp protocol.AuthResponse
		if err := resp.ParsePayload(&authResp); err != nil {
			t.Fatalf("failed to parse auth_resp for wrong-key attempt #%d: %v", i+1, err)
		}
		if authResp.Success {
			t.Fatalf("wrong-key attempt #%d should not authenticate successfully", i+1)
		}
		if authResp.Code != protocol.AuthCodeInvalidKey {
			t.Fatalf("wrong-key attempt #%d: error code should be invalid_key, got %s", i+1, authResp.Code)
		}
		if authResp.Retryable {
			t.Fatalf("wrong-key attempt #%d should not be marked retryable", i+1)
		}
		if authResp.ClearToken {
			t.Fatalf("wrong-key attempt #%d should not request token clearing", i+1)
		}
		conn.Close()
	}

	// 4th connection: even with the correct key, it should be rejected due to rate limiting
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
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
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read rate-limited auth response: %v", err)
	}
	if resp.Type != protocol.MsgTypeAuthResp {
		t.Fatalf("when rate limited, want auth_resp, got %s", resp.Type)
	}
	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		t.Fatalf("failed to parse rate-limited auth response: %v", err)
	}
	if authResp.Success {
		t.Fatal("a locked IP should not authenticate successfully")
	}
	if authResp.Code != protocol.AuthCodeRateLimited {
		t.Fatalf("when rate limited, error code should be rate_limited, got %s", authResp.Code)
	}
	if !authResp.Retryable {
		t.Fatal("rate_limited should be marked retryable")
	}
	if authResp.ClearToken {
		t.Fatal("rate_limited should not require token clearing")
	}

	// Wait for the lockout to expire and recover
	time.Sleep(250 * time.Millisecond)

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed after lockout expiry: %v", err)
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
	var recoveryResp protocol.Message
	if err := conn2.ReadJSON(&recoveryResp); err != nil {
		t.Fatalf("authentication should succeed after lockout expiry: %v", err)
	}
	if recoveryResp.Type != protocol.MsgTypeAuthResp {
		t.Fatalf("want auth_resp, got %s", recoveryResp.Type)
	}
	var recoveredAuth protocol.AuthResponse
	if err := recoveryResp.ParsePayload(&recoveredAuth); err != nil {
		t.Fatalf("failed to parse recovered auth_resp: %v", err)
	}
	if !recoveredAuth.Success {
		t.Fatalf("authentication should succeed after lockout expiry, got code=%s message=%s", recoveredAuth.Code, recoveredAuth.Message)
	}
}

func TestLogin_RateLimitXForwardedFor(t *testing.T) {
	s, cleanup := setupRateLimitedServer(t, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   2,
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	}, RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	})
	defer cleanup()

	// Configure reverse proxy mode: tls.mode=off + proxy IP 10.0.0.0/8 is trusted
	s.TLS = &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"10.0.0.0/8"},
	}

	loginBody := []byte(`{"username":"admin","password":"password123"}`)

	// Identify the real IP through the XFF header (source 10.0.0.1 is in the trusted list)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "203.0.113.50")
		req.RemoteAddr = "10.0.0.1:80" // Trusted proxy IP
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request #%d: want 200, got %d", i+1, w.Code)
		}
	}

	// The 3rd request from the same real IP should be rate limited
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	req.RemoteAddr = "10.0.0.1:80"
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after the same XFF IP exceeds the limit, want 429, got %d", w.Code)
	}

	// A different real IP should be unaffected
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Forwarded-For", "198.51.100.1")
	req2.RemoteAddr = "10.0.0.1:80"
	w2 := httptest.NewRecorder()
	s.handleAPILogin(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("a different real IP should not be affected by rate limiting, got %d", w2.Code)
	}

	// Requests from an untrusted proxy should use RemoteAddr even if they include XFF
	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Forwarded-For", "203.0.113.50") // Attempted spoofing
	req3.RemoteAddr = "1.2.3.4:80"                     // Untrusted IP
	w3 := httptest.NewRecorder()
	s.handleAPILogin(w3, req3)

	// RemoteAddr (1.2.3.4) should be used as the rate-limit key instead of the address in XFF
	if w3.Code != http.StatusOK {
		t.Fatalf("untrusted sources should be rate limited by RemoteAddr, got %d", w3.Code)
	}
}
