package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"netsgo/pkg/protocol"
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

// setupTestServerWithDB 创建用于 API 测试的 Server
func setupTestServerWithDB(t *testing.T, initialized bool) (*Server, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "api_test_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "admin.db")
	store, err := NewAdminStore(dbPath)
	if err != nil {
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}

	if initialized {
		err = store.Initialize("admin", "password123", "http://localhost", nil)
		if err != nil {
			t.Fatalf("初始化 AdminStore 失败: %v", err)
		}
	}

	s := New(0)
	s.auth.adminStore = store

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return s, cleanup
}

func loginAdminToken(t *testing.T, ts *httptest.Server, username, password string) string {
	t.Helper()

	body := []byte(`{"username":"` + username + `","password":"` + password + `"}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("创建登录请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录期望 200，得到 %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析登录响应失败: %v", err)
	}

	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatal("登录响应未返回 token")
	}
	return token
}

func doAuthorizedRequest(t *testing.T, client *http.Client, method, url, token string, body []byte) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	return resp
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
		t.Fatalf("创建 TunnelStore 失败: %v", err)
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
		t.Fatal("测试前置错误：s.store 不能为空")
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
	case protocol.ProxyStatusPaused:
		desiredState = protocol.ProxyDesiredStatePaused
		runtimeState = protocol.ProxyRuntimeStateIdle
	case protocol.ProxyStatusStopped:
		desiredState = protocol.ProxyDesiredStateStopped
		runtimeState = protocol.ProxyRuntimeStateIdle
	case protocol.ProxyStatusError:
		runtimeState = protocol.ProxyRuntimeStateError
	default:
		t.Fatalf("未知测试状态: %s", status)
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
		t.Fatalf("写入测试隧道失败: %v", err)
	}
}

func loginAdminTokenLocal(t *testing.T, handler http.Handler, username, password string) string {
	t.Helper()

	body := []byte(`{"username":"` + username + `","password":"` + password + `"}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/auth/login", "", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("登录期望 200，得到 %d", resp.Code)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析登录响应失败: %v", err)
	}

	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatal("登录响应未返回 token")
	}
	return token
}

func TestAPI_SetupStatus_NotInitialized(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()

	s.handleSetupStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，得到 %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["initialized"] != false {
		t.Errorf("期望 initialized 为 false，得到 %v", resp["initialized"])
	}
}

func TestAPI_SetupStatus_Initialized(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()

	s.handleSetupStatus(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["initialized"] != true {
		t.Errorf("期望 initialized 为 true，得到 %v", resp["initialized"])
	}
}

func TestAPI_SetupInit_Success(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	body := []byte(`{"admin":{"username":"admin2","password":"password123"},"server_addr":"https://test-server.com","allowed_ports":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSetupInit(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("期望状态码 201 Created，得到 %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["success"] != true {
		t.Errorf("期望 success 为 true，得到 %v", resp["success"])
	}
	if resp["message"] == nil || resp["message"] == "" {
		t.Errorf("期望返回 message")
	}
	// 初始化后不再自动创建 session，用户需要单独登录
	if resp["token"] != nil {
		t.Errorf("初始化后不应返回 token，用户应通过登录页登录")
	}

	secret, err := s.auth.adminStore.GetJWTSecret()
	if err != nil {
		t.Fatalf("setup 完成后应已生成 JWT Secret: %v", err)
	}
	if len(secret) == 0 {
		t.Fatal("setup 完成后 JWT Secret 不应为空")
	}

	// 验证确实已经初始化
	if !s.auth.adminStore.IsInitialized() {
		t.Errorf("API 成功后，Store 状态应为已初始化")
	}
}

func TestAPI_SetupInit_AlreadyInitialized(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"admin":{"username":"attacker","password":"password123"},"server_addr":"https://evil.com","allowed_ports":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleSetupInit(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("重复初始化应返回 403 Forbidden，得到 %d", w.Code)
	}
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
		t.Fatalf("期望登录成功 200，得到 %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["token"] == nil || resp["token"] == "" {
		t.Errorf("登录成功未返回 token")
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
		t.Fatalf("密码错误应返回 401，得到 %d", w.Code)
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
		t.Fatalf("session 持久化失败应返回 500，得到 %d", w.Code)
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
			t.Fatalf("%s 匿名访问应返回 401，得到 %d", path, resp.Code)
		}
	}

	setupResp := doMuxRequest(t, mux, http.MethodGet, "/api/setup/status", "", nil)
	if setupResp.Code != http.StatusOK {
		t.Fatalf("/api/setup/status 应保持公开，得到 %d", setupResp.Code)
	}

	token1 := loginAdminTokenLocal(t, mux, "admin", "password123")

	statusResp := doMuxRequest(t, mux, http.MethodGet, "/api/status", token1, nil)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("登录后访问 /api/status 应成功，得到 %d", statusResp.Code)
	}

	token2 := loginAdminTokenLocal(t, mux, "admin", "password123")

	oldSessionResp := doMuxRequest(t, mux, http.MethodGet, "/api/status", token1, nil)
	if oldSessionResp.Code != http.StatusUnauthorized {
		t.Fatalf("单端登录后旧 token 应失效，得到 %d", oldSessionResp.Code)
	}

	currentSessionResp := doMuxRequest(t, mux, http.MethodGet, "/api/clients", token2, nil)
	if currentSessionResp.Code != http.StatusOK {
		t.Fatalf("新 token 应可访问受保护路由，得到 %d", currentSessionResp.Code)
	}

	logoutResp := doMuxRequest(t, mux, http.MethodPost, "/api/auth/logout", token2, nil)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("logout 应返回 200，得到 %d", logoutResp.Code)
	}

	revokedResp := doMuxRequest(t, mux, http.MethodGet, "/api/status", token2, nil)
	if revokedResp.Code != http.StatusUnauthorized {
		t.Fatalf("logout 后 token 应立即失效，得到 %d", revokedResp.Code)
	}
}

func TestAPI_AdminKeys_CreateAndList(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// 1. 创建 API Key (POST)
	body := []byte(`{"name":"test-key","permissions":["connect"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/keys", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleAPIAdminKeys(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("期望创建 Key 成功 201，得到 %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["raw_key"] == nil || resp["raw_key"] == "" {
		t.Errorf("创建 Key 应返回 raw_key 等前端展示")
	}
	if keyPayload, ok := resp["key"].(map[string]any); ok {
		if _, exists := keyPayload["key_hash"]; exists {
			t.Error("API 响应不应泄露 key_hash")
		}
	}

	// 2. 获取 API Keys (GET)
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/keys", nil)
	w2 := httptest.NewRecorder()
	s.handleAPIAdminKeys(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("获取 Keys 期望 200，得到 %d", w2.Code)
	}

	var keys []map[string]any
	json.NewDecoder(w2.Body).Decode(&keys)

	// test-key = 1
	if len(keys) != 1 {
		t.Errorf("期望有 1 个 API Key（新创建），得到 %d", len(keys))
	}
	if len(keys) == 1 {
		if _, exists := keys[0]["key_hash"]; exists {
			t.Error("Key 列表不应返回 key_hash")
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
		t.Fatalf("Key 持久化失败应返回 400，得到 %d", w.Code)
	}
}

func TestAPI_AdminConfig_GetAndUpdate(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// GET: 应返回初始化时设置的配置
	req := httptest.NewRequest(http.MethodGet, "/api/admin/config", nil)
	w := httptest.NewRecorder()
	s.handleAPIAdminConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("获取配置期望 200，得到 %d", w.Code)
	}

	var config map[string]any
	json.NewDecoder(w.Body).Decode(&config)
	if config["server_addr"] != "http://localhost" {
		t.Errorf("初始 server_addr 应为 http://localhost，得到 %v", config["server_addr"])
	}

	// PUT: 更新配置
	updateBody := []byte(`{"server_addr":"https://tunnel.example.com","allowed_ports":[{"start":10000,"end":20000},{"start":30000,"end":30000}]}`)
	req2 := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(updateBody))
	w2 := httptest.NewRecorder()
	s.handleAPIAdminConfig(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("更新配置期望 200，得到 %d", w2.Code)
	}

	// GET: 验证更新后的值
	req3 := httptest.NewRequest(http.MethodGet, "/api/admin/config", nil)
	w3 := httptest.NewRecorder()
	s.handleAPIAdminConfig(w3, req3)

	var updated map[string]any
	json.NewDecoder(w3.Body).Decode(&updated)
	if updated["server_addr"] != "https://tunnel.example.com" {
		t.Errorf("更新后 server_addr 应为 https://tunnel.example.com，得到 %v", updated["server_addr"])
	}
	ports, ok := updated["allowed_ports"].([]any)
	if !ok || len(ports) != 2 {
		t.Errorf("更新后 allowed_ports 应有 2 个范围，得到 %v", updated["allowed_ports"])
	}

	// PUT: 无效端口范围应返回 400
	invalidBody := []byte(`{"server_addr":"test","allowed_ports":[{"start":70000,"end":80000}]}`)
	req4 := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(invalidBody))
	w4 := httptest.NewRecorder()
	s.handleAPIAdminConfig(w4, req4)

	if w4.Code != http.StatusBadRequest {
		t.Fatalf("无效端口范围应返回 400，得到 %d", w4.Code)
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
		t.Fatalf("非法 server_addr 应返回 400，得到 %d", w.Code)
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
		t.Fatalf("带根路径的 server_addr 应返回 200，得到 %d, body: %s", w.Code, w.Body.String())
	}

	if got := s.auth.adminStore.GetServerConfig().ServerAddr; got != "https://example.com" {
		t.Fatalf("server_addr 应规范化为无尾斜杠，得到 %q", got)
	}
}

func TestAPI_AdminConfig_AllowsUpdatingPortsWithLegacyServerAddr(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	if err := s.auth.adminStore.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("初始化 legacy server_addr 失败: %v", err)
	}

	body := []byte(`{"server_addr":"localhost","allowed_ports":[{"start":20000,"end":20010}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPIAdminConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("legacy server_addr 未修改时应允许仅更新端口，得到 %d, body: %s", w.Code, w.Body.String())
	}

	if got := s.auth.adminStore.GetServerConfig().ServerAddr; got != "localhost" {
		t.Fatalf("legacy server_addr 应保持原值，得到 %q", got)
	}
}

func TestAPI_SetupInit_ServerAddrValidation(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	body := []byte(`{"admin":{"username":"admin2","password":"password123"},"server_addr":"example.com","allowed_ports":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSetupInit(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 server_addr 应返回 400，得到 %d", w.Code)
	}
}

func TestAdminConfigResponse(t *testing.T) {
	t.Run("无环境变量时返回生效地址与未锁定状态", func(t *testing.T) {
		_, handler, token, cleanup := setupTestServerWithStores(t, true)
		defer cleanup()

		resp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/config", token, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /api/admin/config 期望 200，得到 %d", resp.Code)
		}

		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}

		if payload["server_addr"] != "http://localhost" {
			t.Fatalf("server_addr 期望 http://localhost，得到 %v", payload["server_addr"])
		}
		if payload["effective_server_addr"] != "localhost" {
			t.Fatalf("effective_server_addr 期望 localhost，得到 %v", payload["effective_server_addr"])
		}
		if locked, ok := payload["server_addr_locked"].(bool); !ok || locked {
			t.Fatalf("server_addr_locked 期望 false，得到 %v", payload["server_addr_locked"])
		}
	})

	t.Run("环境变量存在时返回锁定状态与环境生效地址", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "https://Locked.EXAMPLE.com:443")

		_, handler, token, cleanup := setupTestServerWithStores(t, true)
		defer cleanup()

		resp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/config", token, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /api/admin/config 期望 200，得到 %d", resp.Code)
		}

		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}

		if payload["server_addr"] != "http://localhost" {
			t.Fatalf("server_addr 应保持持久化值 http://localhost，得到 %v", payload["server_addr"])
		}
		if payload["effective_server_addr"] != "locked.example.com" {
			t.Fatalf("effective_server_addr 期望 locked.example.com，得到 %v", payload["effective_server_addr"])
		}
		if locked, ok := payload["server_addr_locked"].(bool); !ok || !locked {
			t.Fatalf("server_addr_locked 期望 true，得到 %v", payload["server_addr_locked"])
		}
	})
}

func TestAdminConfigDryRun(t *testing.T) {
	t.Run("无域名冲突时仍返回 affected_tunnels 与空冲突数组", func(t *testing.T) {
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
			t.Fatalf("PUT /api/admin/config?dry_run=true 期望 200，得到 %d", resp.Code)
		}

		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}

		affected, ok := payload["affected_tunnels"].([]any)
		if !ok || len(affected) != 1 {
			t.Fatalf("affected_tunnels 期望 1 条，得到 %v", payload["affected_tunnels"])
		}

		conflicts, ok := payload["conflicting_http_tunnels"].([]any)
		if !ok || len(conflicts) != 0 {
			t.Fatalf("conflicting_http_tunnels 期望空数组，得到 %v", payload["conflicting_http_tunnels"])
		}
	})

	t.Run("管理地址与 HTTP 域名冲突时返回冲突隧道", func(t *testing.T) {
		s, handler, token, cleanup := setupTestServerWithStores(t, true)
		defer cleanup()

		seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
			Name:      "http-app",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "App.Example.com",
			LocalIP:   "127.0.0.1",
			LocalPort: 8080,
		}, protocol.ProxyStatusPaused)

		body := []byte(`{"server_addr":"https://app.example.com","allowed_ports":[]}`)
		resp := doMuxRequest(t, handler, http.MethodPut, "/api/admin/config?dry_run=true", token, body)
		if resp.Code != http.StatusOK {
			t.Fatalf("PUT /api/admin/config?dry_run=true 期望 200，得到 %d", resp.Code)
		}

		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}

		conflicts, ok := payload["conflicting_http_tunnels"].([]any)
		if !ok || len(conflicts) != 1 {
			t.Fatalf("conflicting_http_tunnels 期望返回 1 条冲突，得到 %v", payload["conflicting_http_tunnels"])
		}
		if conflicts[0] != "client-1:http-app" {
			t.Fatalf("冲突隧道期望 client-1:http-app，得到 %v", conflicts[0])
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
		t.Fatalf("server_addr 被锁定时期望 409，得到 %d", resp.Code)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if _, ok := payload["error"].(string); !ok {
		t.Fatalf("锁定冲突应返回结构化 error，得到 %v", payload)
	}
	if locked, ok := payload["server_addr_locked"].(bool); !ok || !locked {
		t.Fatalf("锁定冲突应返回 server_addr_locked=true，得到 %v", payload["server_addr_locked"])
	}
}

func TestAdminConfigUpdateAllowsDefaultPortNormalizationWhenLocked(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	if err := s.auth.adminStore.Initialize("admin", "password123", "https://example.com:443", nil); err != nil {
		t.Fatalf("初始化带默认端口的 server_addr 失败: %v", err)
	}
	t.Setenv("NETSGO_SERVER_ADDR", "https://locked.example.com")

	body := []byte(`{"server_addr":"https://example.com","allowed_ports":[{"start":30000,"end":30000}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPIAdminConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("默认端口规范化后应允许在锁定下保存非 server_addr 变更，得到 %d, body: %s", w.Code, w.Body.String())
	}
}

func TestAdminConfigResponse_InvalidEnvDoesNotOverrideOrLock(t *testing.T) {
	t.Setenv("NETSGO_SERVER_ADDR", "ws://locked.example.com")

	_, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	resp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/config", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/config 期望 200，得到 %d", resp.Code)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if payload["effective_server_addr"] != "localhost" {
		t.Fatalf("非法环境变量不应覆盖 effective_server_addr，得到 %v", payload["effective_server_addr"])
	}
	if locked, ok := payload["server_addr_locked"].(bool); !ok || locked {
		t.Fatalf("非法环境变量不应锁定 server_addr，得到 %v", payload["server_addr_locked"])
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
		t.Fatalf("server_addr 与 HTTP 域名冲突时期望 409，得到 %d", resp.Code)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	conflicts, ok := payload["conflicting_http_tunnels"].([]any)
	if !ok || len(conflicts) != 1 {
		t.Fatalf("conflicting_http_tunnels 期望返回 1 条冲突，得到 %v", payload["conflicting_http_tunnels"])
	}
	if conflicts[0] != "client-1:http-app" {
		t.Fatalf("冲突隧道期望 client-1:http-app，得到 %v", conflicts[0])
	}
}

// ========== P5: Cookie 设置/清除测试 ==========

func TestAPI_Login_SetsCookie(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望登录成功 200，得到 %d", w.Code)
	}

	// 检查 Set-Cookie 头
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("登录响应中缺少 netsgo_session cookie")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie 应设置 HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie SameSite 应为 Strict，得到 %v", sessionCookie.SameSite)
	}
	if sessionCookie.Path != "/api" {
		t.Errorf("session cookie Path 应为 /api，得到 %s", sessionCookie.Path)
	}
	if sessionCookie.Value == "" {
		t.Error("session cookie 值不应为空")
	}
	if sessionCookie.Secure {
		t.Error("普通 HTTP 登录默认不应设置 Secure")
	}
}

func TestAPI_Logout_ClearsCookie(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// 先登录获取 session
	session := mustCreateSession(t, s.auth.adminStore, "user-1", "admin", "admin", "127.0.0.1", "")
	tokenString, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	s.RequireAuth(s.handleAPILogout).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望登出成功 200，得到 %d", w.Code)
	}

	// 检查 Set-Cookie 头是否清除了 cookie
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("登出响应中缺少清除 netsgo_session cookie 的 Set-Cookie 头")
	}
	if sessionCookie.MaxAge != -1 {
		t.Errorf("清除 cookie 的 MaxAge 应为 -1，得到 %d", sessionCookie.MaxAge)
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
		t.Fatalf("期望登录成功 200，得到 %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("登录响应中缺少 netsgo_session cookie")
	}
	if !sessionCookie.Secure {
		t.Error("TLS 请求下应设置 Secure cookie")
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
		t.Fatalf("期望登录成功 200，得到 %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("登录响应中缺少 netsgo_session cookie")
	}
	if !sessionCookie.Secure {
		t.Error("受信反代声明 HTTPS 时应设置 Secure cookie")
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
		t.Fatalf("期望登录成功 200，得到 %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("登录响应中缺少 netsgo_session cookie")
	}
	if sessionCookie.Secure {
		t.Error("不应信任非受信代理伪造的 HTTPS 头")
	}
}
