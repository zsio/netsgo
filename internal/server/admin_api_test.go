package server

import (
	"bytes"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"netsgo/pkg/protocol"

	"golang.org/x/crypto/bcrypt"
)

func defaultTestRequestHost() string {
	if env := os.Getenv("NETSGO_SERVER_ADDR"); env != "" {
		if normalized, err := validateServerAddr(env); err == nil {
			if host := canonicalHost(normalized); host != "" {
				return host
			}
		}
	}
	return "localhost"
}

// setupTestServerWithDB creates a server for API tests
func setupTestServerWithDB(t *testing.T, initialized bool) (*Server, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "api_test_*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "admin.db")
	store, err := NewAdminStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create AdminStore: %v", err)
	}
	store.bcryptCost = bcrypt.MinCost // Use the minimum cost in tests to avoid slowing down the suite

	if initialized {
		err = store.Initialize("admin", "password123", "http://localhost", nil)
		if err != nil {
			t.Fatalf("failed to initialize AdminStore: %v", err)
		}
	}

	s := New(0)
	s.auth.adminStore = store

	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	return s, cleanup
}

func doMuxRequest(t *testing.T, handler http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Host = defaultTestRequestHost()
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func setupTestServerWithStores(t *testing.T, initialized bool) (*Server, http.Handler, string, func()) {
	t.Helper()

	s, cleanup := setupTestServerWithDB(t, initialized)

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("failed to create TunnelStore: %v", err)
	}
	s.store = store

	handler := s.StartHTTPOnly()
	token := ""
	if initialized {
		token = loginAdminTokenLocal(t, handler, "admin", "password123")
	}

	return s, handler, token, cleanup
}

func seedStoredTunnel(t *testing.T, s *Server, clientID string, req protocol.ProxyNewRequest, status string) {
	t.Helper()

	if s.store == nil {
		t.Fatal("test setup error: s.store must not be nil")
	}
	if req.LocalIP == "" {
		req.LocalIP = "127.0.0.1"
	}
	if req.LocalPort == 0 {
		req.LocalPort = 8080
	}

	desiredState := protocol.ProxyDesiredStateRunning
	runtimeState := protocol.ProxyRuntimeStateExposed
	switch status {
	case protocol.ProxyStatusPending:
		runtimeState = protocol.ProxyRuntimeStatePending
	case protocol.ProxyStatusActive:
		runtimeState = protocol.ProxyRuntimeStateExposed
	case protocol.ProxyStatusStopped:
		desiredState = protocol.ProxyDesiredStateStopped
		runtimeState = protocol.ProxyRuntimeStateIdle
	case protocol.ProxyStatusError:
		runtimeState = protocol.ProxyRuntimeStateError
	default:
		t.Fatalf("unknown test status: %s", status)
	}

	err := s.store.AddTunnel(StoredTunnel{
		ProxyNewRequest: req,
		DesiredState:    desiredState,
		RuntimeState:    runtimeState,
		ClientID:        clientID,
		Hostname:        clientID + ".local",
		Binding:         TunnelBindingClientID,
	})
	if err != nil {
		t.Fatalf("failed to write test tunnel: %v", err)
	}
}

func loginAdminTokenLocal(t *testing.T, handler http.Handler, username, password string) string {
	t.Helper()

	body := []byte(`{"username":"` + username + `","password":"` + password + `"}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/auth/login", "", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d", resp.Code)
	}

	var payload map[string]any
	if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
		t.Fatalf("failed to parse login response: %v", err)
	}

	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatal("login response did not return a token")
	}
	return token
}

func TestAPI_Login_Success(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected successful login with 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := mustDecodeJSON(t, w.Body, &resp); err != nil {
		t.Fatalf("decode login response failed: %v", err)
	}
	if resp["token"] == nil || resp["token"] == "" {
		t.Errorf("successful login did not return a token")
	}
}

func TestAPI_Login_WrongPassword(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"username":"admin","password":"wrongpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPILogin(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password should return 401, got %d", w.Code)
	}
}

func TestAPI_Login_PersistSessionFailure(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	s.auth.adminStore.failSaveErr = errors.New("save failed")
	s.auth.adminStore.failSaveCount = 1

	body := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPILogin(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("session persistence failure should return 500, got %d", w.Code)
	}
}

func TestAPI_ProtectedRoutes_LoginLogoutAndSingleSession(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	mux := s.newHTTPMux()

	protected := []string{
		"/api/status",
		"/api/clients",
		"/api/events",
		"/api/admin/keys",
	}

	for _, path := range protected {
		resp := doMuxRequest(t, mux, http.MethodGet, path, "", nil)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous access to %s should return 401, got %d", path, resp.Code)
		}
	}

	token1 := loginAdminTokenLocal(t, mux, "admin", "password123")

	statusResp := doMuxRequest(t, mux, http.MethodGet, "/api/status", token1, nil)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("accessing /api/status after login should succeed, got %d", statusResp.Code)
	}

	token2 := loginAdminTokenLocal(t, mux, "admin", "password123")

	oldSessionResp := doMuxRequest(t, mux, http.MethodGet, "/api/status", token1, nil)
	if oldSessionResp.Code != http.StatusUnauthorized {
		t.Fatalf("after single-session login, the old token should be invalidated, got %d", oldSessionResp.Code)
	}

	currentSessionResp := doMuxRequest(t, mux, http.MethodGet, "/api/clients", token2, nil)
	if currentSessionResp.Code != http.StatusOK {
		t.Fatalf("the new token should be able to access protected routes, got %d", currentSessionResp.Code)
	}

	logoutResp := doMuxRequest(t, mux, http.MethodPost, "/api/auth/logout", token2, nil)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("logout should return 200, got %d", logoutResp.Code)
	}

	revokedResp := doMuxRequest(t, mux, http.MethodGet, "/api/status", token2, nil)
	if revokedResp.Code != http.StatusUnauthorized {
		t.Fatalf("the token should be invalid immediately after logout, got %d", revokedResp.Code)
	}
}

func TestAPI_AdminKeys_CreateAndList(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// 1. Create API key (POST)
	body := []byte(`{"name":"test-key","permissions":["connect"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/keys", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleAPIAdminKeys(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected key creation success with 201, got %d", w.Code)
	}

	var resp map[string]any
	if err := mustDecodeJSON(t, w.Body, &resp); err != nil {
		t.Fatalf("decode key creation response failed: %v", err)
	}
	if resp["raw_key"] == nil || resp["raw_key"] == "" {
		t.Errorf("creating a key should return raw_key and other frontend-facing fields")
	}
	if keyPayload, ok := resp["key"].(map[string]any); ok {
		if _, exists := keyPayload["key_hash"]; exists {
			t.Error("API response should not leak key_hash")
		}
	}

	// 2. Get API keys (GET)
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/keys", nil)
	w2 := httptest.NewRecorder()
	s.handleAPIAdminKeys(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("getting keys: want 200, got %d", w2.Code)
	}

	var keys []map[string]any
	if err := mustDecodeJSON(t, w2.Body, &keys); err != nil {
		t.Fatalf("decode keys response failed: %v", err)
	}

	// test-key = 1
	if len(keys) != 1 {
		t.Errorf("expected 1 API key (the newly created one), got %d", len(keys))
	}
	if len(keys) == 1 {
		if _, exists := keys[0]["key_hash"]; exists {
			t.Error("key list should not return key_hash")
		}
	}
}

func TestAPI_AdminKeys_CreateFailsWhenPersistFails(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	s.auth.adminStore.failSaveErr = errors.New("save failed")
	s.auth.adminStore.failSaveCount = 1

	body := []byte(`{"name":"test-key","permissions":["connect"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/keys", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleAPIAdminKeys(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("API key persistence failure should return 400, got %d", w.Code)
	}
}

func TestAPI_AdminConfig_GetAndUpdate(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// GET: should return the configuration set at initialization
	req := httptest.NewRequest(http.MethodGet, "/api/admin/config", nil)
	w := httptest.NewRecorder()
	s.handleAPIAdminConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("getting config: want 200, got %d", w.Code)
	}

	var config map[string]any
	if err := mustDecodeJSON(t, w.Body, &config); err != nil {
		t.Fatalf("decode config response failed: %v", err)
	}
	if config["server_addr"] != "http://localhost" {
		t.Errorf("initial server_addr should be http://localhost, got %v", config["server_addr"])
	}

	// PUT: update configuration
	updateBody := []byte(`{"server_addr":"https://tunnel.example.com","allowed_ports":[{"start":10000,"end":20000},{"start":30000,"end":30000}]}`)
	req2 := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(updateBody))
	w2 := httptest.NewRecorder()
	s.handleAPIAdminConfig(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("updating config: want 200, got %d", w2.Code)
	}

	// GET: verify the updated values
	req3 := httptest.NewRequest(http.MethodGet, "/api/admin/config", nil)
	w3 := httptest.NewRecorder()
	s.handleAPIAdminConfig(w3, req3)

	var updated map[string]any
	if err := mustDecodeJSON(t, w3.Body, &updated); err != nil {
		t.Fatalf("decode updated config response failed: %v", err)
	}
	if updated["server_addr"] != "https://tunnel.example.com" {
		t.Errorf("updated server_addr should be https://tunnel.example.com, got %v", updated["server_addr"])
	}
	ports, ok := updated["allowed_ports"].([]any)
	if !ok || len(ports) != 2 {
		t.Errorf("updated allowed_ports should have 2 ranges, got %v", updated["allowed_ports"])
	}

	// PUT: invalid port range should return 400
	invalidBody := []byte(`{"server_addr":"test","allowed_ports":[{"start":70000,"end":80000}]}`)
	req4 := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(invalidBody))
	w4 := httptest.NewRecorder()
	s.handleAPIAdminConfig(w4, req4)

	if w4.Code != http.StatusBadRequest {
		t.Fatalf("invalid port range should return 400, got %d", w4.Code)
	}
}

func TestAPI_AdminConfig_ServerAddrValidation(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"server_addr":"ws://example.com","allowed_ports":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPIAdminConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid server_addr should return 400, got %d", w.Code)
	}
}

func TestAPI_AdminConfig_ServerAddrNormalization(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"server_addr":"https://example.com/","allowed_ports":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPIAdminConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("server_addr with a root path should return 200, got %d, body: %s", w.Code, w.Body.String())
	}

	if got := s.auth.adminStore.GetServerConfig().ServerAddr; got != "https://example.com" {
		t.Fatalf("server_addr should be normalized without a trailing slash, got %q", got)
	}
}

func TestAPI_AdminConfig_AllowsUpdatingPortsWithLegacyServerAddr(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	if err := s.auth.adminStore.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("failed to initialize legacy server_addr: %v", err)
	}

	body := []byte(`{"server_addr":"localhost","allowed_ports":[{"start":20000,"end":20010}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPIAdminConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("when legacy server_addr is unchanged, updating only the port should be allowed, got %d, body: %s", w.Code, w.Body.String())
	}

	if got := s.auth.adminStore.GetServerConfig().ServerAddr; got != "localhost" {
		t.Fatalf("legacy server_addr should keep its original value, got %q", got)
	}
}

func TestAdminConfigResponse(t *testing.T) {
	t.Run("returns effective address and unlocked state when no environment variable is set", func(t *testing.T) {
		_, handler, token, cleanup := setupTestServerWithStores(t, true)
		defer cleanup()

		resp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/config", token, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /api/admin/config: want 200, got %d", resp.Code)
		}

		var payload map[string]any
		if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if payload["server_addr"] != "http://localhost" {
			t.Fatalf("server_addr: want http://localhost, got %v", payload["server_addr"])
		}
		if payload["effective_server_addr"] != "localhost" {
			t.Fatalf("effective_server_addr: want localhost, got %v", payload["effective_server_addr"])
		}
		if locked, ok := payload["server_addr_locked"].(bool); !ok || locked {
			t.Fatalf("server_addr_locked: want false, got %v", payload["server_addr_locked"])
		}
	})

	t.Run("returns locked state and env effective address when the environment variable exists", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "https://Locked.EXAMPLE.com:443")

		_, handler, token, cleanup := setupTestServerWithStores(t, true)
		defer cleanup()

		resp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/config", token, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /api/admin/config: want 200, got %d", resp.Code)
		}

		var payload map[string]any
		if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if payload["server_addr"] != "http://localhost" {
			t.Fatalf("server_addr should keep the persisted value http://localhost, got %v", payload["server_addr"])
		}
		if payload["effective_server_addr"] != "locked.example.com" {
			t.Fatalf("effective_server_addr: want locked.example.com, got %v", payload["effective_server_addr"])
		}
		if locked, ok := payload["server_addr_locked"].(bool); !ok || !locked {
			t.Fatalf("server_addr_locked: want true, got %v", payload["server_addr_locked"])
		}
	})
}

func TestAdminConfigDryRun(t *testing.T) {
	t.Run("returns affected_tunnels and an empty conflict array when there is no domain conflict", func(t *testing.T) {
		s, handler, token, cleanup := setupTestServerWithStores(t, true)
		defer cleanup()

		seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
			Name:       "tcp-affected",
			Type:       protocol.ProxyTypeTCP,
			RemotePort: 40000,
		}, protocol.ProxyStatusActive)

		body := []byte(`{"server_addr":"https://mgmt.example.com","allowed_ports":[{"start":30000,"end":30010}]}`)
		resp := doMuxRequest(t, handler, http.MethodPut, "/api/admin/config?dry_run=true", token, body)
		if resp.Code != http.StatusOK {
			t.Fatalf("PUT /api/admin/config?dry_run=true: want 200, got %d", resp.Code)
		}

		var payload map[string]any
		if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		affected, ok := payload["affected_tunnels"].([]any)
		if !ok || len(affected) != 1 {
			t.Fatalf("affected_tunnels: want 1 entry, got %v", payload["affected_tunnels"])
		}

		conflicts, ok := payload["conflicting_http_tunnels"].([]any)
		if !ok || len(conflicts) != 0 {
			t.Fatalf("conflicting_http_tunnels: want an empty array, got %v", payload["conflicting_http_tunnels"])
		}
	})

	t.Run("returns conflicting tunnels when the management address conflicts with an HTTP domain", func(t *testing.T) {
		s, handler, token, cleanup := setupTestServerWithStores(t, true)
		defer cleanup()

		seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
			Name:      "http-app",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "App.Example.com",
			LocalIP:   "127.0.0.1",
			LocalPort: 8080,
		}, protocol.ProxyStatusStopped)

		body := []byte(`{"server_addr":"https://app.example.com","allowed_ports":[]}`)
		resp := doMuxRequest(t, handler, http.MethodPut, "/api/admin/config?dry_run=true", token, body)
		if resp.Code != http.StatusOK {
			t.Fatalf("PUT /api/admin/config?dry_run=true: want 200, got %d", resp.Code)
		}

		var payload map[string]any
		if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		conflicts, ok := payload["conflicting_http_tunnels"].([]any)
		if !ok || len(conflicts) != 1 {
			t.Fatalf("conflicting_http_tunnels: expected 1 conflict, got %v", payload["conflicting_http_tunnels"])
		}
		if conflicts[0] != "client-1:http-app" {
			t.Fatalf("conflicting tunnel: want client-1:http-app, got %v", conflicts[0])
		}
	})
}

func TestAdminConfigUpdateRejectsWhenLocked(t *testing.T) {
	t.Setenv("NETSGO_SERVER_ADDR", "https://locked.example.com")

	_, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	body := []byte(`{"server_addr":"https://new.example.com","allowed_ports":[]}`)
	resp := doMuxRequest(t, handler, http.MethodPut, "/api/admin/config", token, body)
	if resp.Code != http.StatusConflict {
		t.Fatalf("when server_addr is locked, want 409, got %d", resp.Code)
	}

	var payload map[string]any
	if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if _, ok := payload["error"].(string); !ok {
		t.Fatalf("locked conflict should return structured error, got %v", payload)
	}
	if locked, ok := payload["server_addr_locked"].(bool); !ok || !locked {
		t.Fatalf("locked conflict should return server_addr_locked=true, got %v", payload["server_addr_locked"])
	}
}

func TestAdminConfigUpdateAllowsDefaultPortNormalizationWhenLocked(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	if err := s.auth.adminStore.Initialize("admin", "password123", "https://example.com:443", nil); err != nil {
		t.Fatalf("failed to initialize server_addr with default port: %v", err)
	}
	t.Setenv("NETSGO_SERVER_ADDR", "https://locked.example.com")

	body := []byte(`{"server_addr":"https://example.com","allowed_ports":[{"start":30000,"end":30000}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPIAdminConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("after default-port normalization, non-server_addr changes should still be savable while locked, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestAdminConfigResponse_InvalidEnvDoesNotOverrideOrLock(t *testing.T) {
	t.Setenv("NETSGO_SERVER_ADDR", "ws://locked.example.com")

	_, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	resp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/config", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/config: want 200, got %d", resp.Code)
	}

	var payload map[string]any
	if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if payload["effective_server_addr"] != "localhost" {
		t.Fatalf("invalid environment variable should not override effective_server_addr, got %v", payload["effective_server_addr"])
	}
	if locked, ok := payload["server_addr_locked"].(bool); !ok || locked {
		t.Fatalf("invalid environment variable should not lock server_addr, got %v", payload["server_addr_locked"])
	}
}

func TestAdminConfigUpdateRejectsWhenHTTPDomainConflicts(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
		Name:      "http-app",
		Type:      protocol.ProxyTypeHTTP,
		Domain:    "app.example.com",
		LocalIP:   "127.0.0.1",
		LocalPort: 8080,
	}, protocol.ProxyStatusStopped)

	body := []byte(`{"server_addr":"https://app.example.com","allowed_ports":[]}`)
	resp := doMuxRequest(t, handler, http.MethodPut, "/api/admin/config", token, body)
	if resp.Code != http.StatusConflict {
		t.Fatalf("server_addr conflict with HTTP domain: want 409, got %d", resp.Code)
	}

	var payload map[string]any
	if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	conflicts, ok := payload["conflicting_http_tunnels"].([]any)
	if !ok || len(conflicts) != 1 {
		t.Fatalf("conflicting_http_tunnels: expected 1 conflict, got %v", payload["conflicting_http_tunnels"])
	}
	if conflicts[0] != "client-1:http-app" {
		t.Fatalf("conflicting tunnel: want client-1:http-app, got %v", conflicts[0])
	}
}

// ========== P5: Cookie set/clear tests ==========

func TestAPI_Login_SetsCookie(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected successful login with 200, got %d", w.Code)
	}

	// Check the Set-Cookie header
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("login response is missing the netsgo_session cookie")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie should set HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie SameSite should be Strict, got %v", sessionCookie.SameSite)
	}
	if sessionCookie.Path != "/api" {
		t.Errorf("session cookie Path should be /api, got %s", sessionCookie.Path)
	}
	if sessionCookie.Value == "" {
		t.Error("session cookie value should not be empty")
	}
	if sessionCookie.Secure {
		t.Error("plain HTTP login should not set Secure by default")
	}
}

func TestAPI_Logout_ClearsCookie(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// First log in to obtain a session
	session := mustCreateSession(t, s.auth.adminStore, "user-1", "admin", "admin", "127.0.0.1", "")
	tokenString, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	s.RequireAuth(s.handleAPILogout).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected logout success with 200, got %d", w.Code)
	}

	// Check whether the Set-Cookie header cleared the cookie
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("logout response is missing the Set-Cookie header that clears the netsgo_session cookie")
	}
	if sessionCookie.MaxAge != -1 {
		t.Errorf("clearing cookie MaxAge should be -1, got %d", sessionCookie.MaxAge)
	}
}

func TestAPI_Login_SetsSecureCookie_WhenRequestIsTLS(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()

	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected successful login with 200, got %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("login response is missing the netsgo_session cookie")
	}
	if !sessionCookie.Secure {
		t.Error("TLS requests should set a Secure cookie")
	}
}

func TestAPI_Login_SetsSecureCookie_WhenTrustedProxyReportsHTTPS(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()
	s.TLS = &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"10.0.0.0/8"},
	}

	body := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.1.2.3:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected successful login with 200, got %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("login response is missing the netsgo_session cookie")
	}
	if !sessionCookie.Secure {
		t.Error("should set a Secure cookie when a trusted reverse proxy declares HTTPS")
	}
}

func TestAPI_Login_IgnoresUntrustedProxyHTTPSForSecureCookie(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()
	s.TLS = &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"10.0.0.0/8"},
	}

	body := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected successful login with 200, got %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("login response is missing the netsgo_session cookie")
	}
	if sessionCookie.Secure {
		t.Error("should not trust forged HTTPS headers from an untrusted proxy")
	}
}
