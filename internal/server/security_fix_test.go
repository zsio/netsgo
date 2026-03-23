package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ==================== P6: Session Binding UA 校验 ====================

func TestP6_SessionBinding_SameUA_Passes(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	// 用特定 UA 登录
	loginBody := []byte(`{"username":"admin","password":"password123"}`)
	loginReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("User-Agent", "TestBrowser/1.0")

	resp, err := http.DefaultClient.Do(loginReq)
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()

	var loginResp map[string]any
	json.NewDecoder(resp.Body).Decode(&loginResp)
	token := loginResp["token"].(string)

	// 使用同一 UA 访问受保护接口 → 应该成功
	statusReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/status", nil)
	statusReq.Header.Set("Authorization", "Bearer "+token)
	statusReq.Header.Set("User-Agent", "TestBrowser/1.0")

	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer statusResp.Body.Close()

	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("同一 UA 应放行，得到 %d", statusResp.StatusCode)
	}
}

func TestP6_SessionBinding_DifferentUA_Rejected(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	// 用特定 UA 登录
	loginBody := []byte(`{"username":"admin","password":"password123"}`)
	loginReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("User-Agent", "TestBrowser/1.0")

	resp, err := http.DefaultClient.Do(loginReq)
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()

	var loginResp map[string]any
	json.NewDecoder(resp.Body).Decode(&loginResp)
	token := loginResp["token"].(string)

	// 使用不同 UA 访问受保护接口 → 应该被拒绝
	statusReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/status", nil)
	statusReq.Header.Set("Authorization", "Bearer "+token)
	statusReq.Header.Set("User-Agent", "EvilBrowser/2.0")

	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer statusResp.Body.Close()

	if statusResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("不同 UA 应返回 401，得到 %d", statusResp.StatusCode)
	}

	// 验证返回的错误信息
	var errResp map[string]any
	json.NewDecoder(statusResp.Body).Decode(&errResp)
	if errResp["error"] != "session environment mismatch" {
		t.Errorf("错误信息应为 session environment mismatch，得到 %v", errResp["error"])
	}
}

func TestP6_SessionBinding_EmptyUA_Matches(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()

	// 不设置 UA 登录（Go HTTP Client 默认 UA 为 "Go-http-client/1.1"）
	// 但 httptest 的 DefaultTransport 也有默认 UA
	// 直接用同一 client 登录和请求就行
	token := loginAdminToken(t, ts, "admin", "password123")

	// 使用同一默认 client 访问 → 应该成功（UA 自然一致）
	statusResp := doAuthorizedRequest(t, http.DefaultClient, http.MethodGet, ts.URL+"/api/status", token, nil)
	defer statusResp.Body.Close()

	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("同一 client 的默认 UA 应匹配，得到 %d", statusResp.StatusCode)
	}
}

// ==================== P8: Setup Token 防抢初始化 ====================

func TestP8_SetupToken_Required(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	// 模拟服务启动时生成的 Setup Token
	s.setupToken = "test-setup-token-12345"

	// 不携带 setup_token 的初始化请求 → 应该被拒绝
	body := []byte(`{"admin":{"username":"admin","password":"password123"},"server_addr":"http://localhost","allowed_ports":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSetupInit(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("缺少 setup_token 应返回 403，得到 %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] == nil || resp["error"] == "" {
		t.Error("应返回错误信息")
	}
}

func TestP8_SetupToken_Wrong(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	s.setupToken = "correct-token"

	// 携带错误的 setup_token → 应该被拒绝
	body := []byte(`{"admin":{"username":"admin","password":"password123"},"server_addr":"http://localhost","allowed_ports":[],"setup_token":"wrong-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSetupInit(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("错误 setup_token 应返回 403，得到 %d", w.Code)
	}
}

func TestP8_SetupToken_Correct(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	s.setupToken = "correct-token-abc123"

	// 携带正确的 setup_token → 应该成功初始化
	body := []byte(`{"admin":{"username":"admin","password":"password123"},"server_addr":"http://localhost","allowed_ports":[],"setup_token":"correct-token-abc123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSetupInit(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("正确 setup_token 应返回 201 Created，得到 %d, body: %s", w.Code, w.Body.String())
	}

	// 验证 setupToken 被清空
	if s.setupToken != "" {
		t.Error("初始化成功后 setupToken 应被清空")
	}

	// 验证确实已经初始化
	if !s.adminStore.IsInitialized() {
		t.Error("初始化应成功完成")
	}
}

func TestP8_SetupToken_NoTokenRequired_Passes(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	// setupToken 为空（模拟没有设置 token 的场景，例如测试中直接调用）
	// 这种情况下初始化应该正常通过

	body := []byte(`{"admin":{"username":"admin","password":"password123"},"server_addr":"http://localhost","allowed_ports":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSetupInit(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("无 setupToken 时应直接放行，得到 %d", w.Code)
	}
}

func TestP8_SetupStatus_ReportsTokenRequired(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, false)
	defer cleanup()

	// 设置 setupToken → status 应返回 setup_token_required: true
	s.setupToken = "some-token"

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	s.handleSetupStatus(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["initialized"] != false {
		t.Errorf("应未初始化")
	}
	if resp["setup_token_required"] != true {
		t.Errorf("setup_token_required 应为 true，得到 %v", resp["setup_token_required"])
	}
}

func TestP8_SetupStatus_NoTokenAfterInit(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// 已初始化，setupToken 为空 → setup_token_required 应为 false
	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	s.handleSetupStatus(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["initialized"] != true {
		t.Errorf("应已初始化")
	}
	if resp["setup_token_required"] != false {
		t.Errorf("已初始化后 setup_token_required 应为 false，得到 %v", resp["setup_token_required"])
	}
}
