package server

import (
	"bytes"
	"encoding/json"
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

func TestClient_RateLimitBlocksAfterWindowExceeded(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.auth.replaceClientRateLimiter(ClientAuthRateLimitSettings{Enabled: true, RequestsPerMinute: 3})
	defer s.auth.stopRateLimiters()

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"

	for i := range 3 {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket connection #%d failed: %v", i+1, err)
		}
		authReq := protocol.AuthRequest{
			Key:       "wrong-key",
			InstallID: "install-rate-test",
			Client:    protocol.ClientInfo{Hostname: "rate-test-host", OS: "linux", Arch: "amd64", Version: "0.1.0"},
		}
		msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
		if err := conn.WriteJSON(msg); err != nil {
			t.Fatalf("WriteJSON failed: %v", err)
		}
		mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
		var resp protocol.Message
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("failed to read auth response for attempt #%d: %v", i+1, err)
		}
		var authResp protocol.AuthResponse
		if err := resp.ParsePayload(&authResp); err != nil {
			t.Fatalf("failed to parse auth response for attempt #%d: %v", i+1, err)
		}
		if authResp.Code != protocol.AuthCodeInvalidKey {
			t.Fatalf("attempt #%d code = %q, want %q", i+1, authResp.Code, protocol.AuthCodeInvalidKey)
		}
		_ = conn.Close()
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer mustClose(t, conn)
	authReq := protocol.AuthRequest{
		Key:       "test-key",
		InstallID: "install-rate-test",
		Client:    protocol.ClientInfo{Hostname: "rate-test-host", OS: "linux", Arch: "amd64", Version: "0.1.0"},
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}
	mustSetReadDeadline(t, conn, time.Now().Add(2*time.Second))
	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read rate-limited auth response: %v", err)
	}
	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		t.Fatalf("failed to parse rate-limited auth response: %v", err)
	}
	if authResp.Success || authResp.Code != protocol.AuthCodeRateLimited || !authResp.Retryable || authResp.ClearToken {
		t.Fatalf("unexpected rate-limited auth response: %+v", authResp)
	}
}

func TestClient_RateLimitDisabledByDefault(t *testing.T) {
	s := New(0)
	for i := range defaultClientAuthRateLimitPerMinute + 1 {
		if allowed, _ := s.auth.allowClientAuthentication("203.0.113.50"); !allowed {
			t.Fatalf("disabled client auth limiter rejected request #%d", i+1)
		}
	}
	settings, entries := s.auth.clientRateLimitSnapshot(time.Now())
	if settings.Enabled || settings.RequestsPerMinute != defaultClientAuthRateLimitPerMinute {
		t.Fatalf("default settings = %+v", settings)
	}
	if len(entries) != 0 {
		t.Fatalf("disabled limiter recorded %d entries", len(entries))
	}
}

func TestAdminClientAuthRateLimits_UpdateListAndDelete(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	handler := s.StartHTTPOnly()
	token := loginAdminTokenLocal(t, handler, "admin", "password123")

	initialResp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/rate-limits/client-auth", token, nil)
	if initialResp.Code != http.StatusOK {
		t.Fatalf("initial GET status = %d: %s", initialResp.Code, initialResp.Body.String())
	}
	var initial clientAuthRateLimitResponse
	if err := json.Unmarshal(initialResp.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode initial response: %v", err)
	}
	if initial.Enabled || initial.RequestsPerMinute != defaultClientAuthRateLimitPerMinute || len(initial.Entries) != 0 {
		t.Fatalf("unexpected initial response: %+v", initial)
	}

	updateResp := doMuxRequest(t, handler, http.MethodPut, "/api/admin/rate-limits/client-auth", token, []byte(`{"enabled":true,"requests_per_minute":2}`))
	if updateResp.Code != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", updateResp.Code, updateResp.Body.String())
	}
	stored, err := s.auth.adminStore.GetClientAuthRateLimitSettings()
	if err != nil {
		t.Fatalf("load stored settings: %v", err)
	}
	if !stored.Enabled || stored.RequestsPerMinute != 2 {
		t.Fatalf("stored settings = %+v", stored)
	}

	ip := "203.0.113.44"
	if allowed, _ := s.auth.allowClientAuthentication(ip); !allowed {
		t.Fatal("first request should be allowed")
	}
	if allowed, _ := s.auth.allowClientAuthentication(ip); !allowed {
		t.Fatal("second request should be allowed")
	}
	if allowed, _ := s.auth.allowClientAuthentication(ip); allowed {
		t.Fatal("third request should be limited")
	}

	listResp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/rate-limits/client-auth", token, nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", listResp.Code, listResp.Body.String())
	}
	var list clientAuthRateLimitResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode rate-limit list response: %v", err)
	}
	if !list.Enabled || list.RequestsPerMinute != 2 || len(list.Entries) != 1 || !list.Entries[0].Limited || list.Entries[0].Reason != "window" {
		t.Fatalf("unexpected rate-limit response: %+v", list)
	}

	deleteResp := doMuxRequest(t, handler, http.MethodDelete, "/api/admin/rate-limits/client-auth", token, []byte(`{"ip":"203.0.113.44"}`))
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d: %s", deleteResp.Code, deleteResp.Body.String())
	}
	if allowed, _ := s.auth.allowClientAuthentication(ip); !allowed {
		t.Fatal("deleted IP should be allowed with a fresh counter")
	}

	disableResp := doMuxRequest(t, handler, http.MethodPut, "/api/admin/rate-limits/client-auth", token, []byte(`{"enabled":false,"requests_per_minute":2}`))
	if disableResp.Code != http.StatusOK {
		t.Fatalf("disable PUT status = %d: %s", disableResp.Code, disableResp.Body.String())
	}
	for i := range 3 {
		if allowed, _ := s.auth.allowClientAuthentication(ip); !allowed {
			t.Fatalf("disabled limiter rejected request #%d", i+1)
		}
	}
}

func TestAdminClientAuthRateLimits_RejectsInvalidSettingsWithoutReplacingLimiter(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()
	handler := s.StartHTTPOnly()
	token := loginAdminTokenLocal(t, handler, "admin", "password123")

	for _, body := range []string{
		`{"enabled":true,"requests_per_minute":0}`,
		`{"enabled":true,"requests_per_minute":1001}`,
	} {
		resp := doMuxRequest(t, handler, http.MethodPut, "/api/admin/rate-limits/client-auth", token, []byte(body))
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s: status = %d, want 400: %s", body, resp.Code, resp.Body.String())
		}
	}

	settings, entries := s.auth.clientRateLimitSnapshot(time.Now())
	if settings.Enabled || settings.RequestsPerMinute != defaultClientAuthRateLimitPerMinute || len(entries) != 0 {
		t.Fatalf("runtime limiter changed after invalid update: settings=%+v entries=%+v", settings, entries)
	}
	stored, err := s.auth.adminStore.GetClientAuthRateLimitSettings()
	if err != nil {
		t.Fatalf("load stored settings: %v", err)
	}
	if stored != settings {
		t.Fatalf("stored settings = %+v, runtime settings = %+v", stored, settings)
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
