package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
		err = store.Initialize("admin", "password123", "localhost", nil)
		if err != nil {
			t.Fatalf("初始化 AdminStore 失败: %v", err)
		}
	}

	s := New(0)
	s.adminStore = store

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return s, cleanup
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

	body := []byte(`{"admin":{"username":"admin2","password":"password123"},"server_addr":"test-server.com","allowed_ports":[]}`)
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
	if resp["token"] == nil || resp["token"] == "" {
		t.Errorf("期望返回合法的 token")
	}

	// 验证确实已经初始化
	if !s.adminStore.IsInitialized() {
		t.Errorf("API 成功后，Store 状态应为已初始化")
	}
}

func TestAPI_SetupInit_AlreadyInitialized(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	body := []byte(`{"admin":{"username":"attacker","password":"password123"},"server_addr":"evil.com","allowed_ports":[]}`)
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

func TestAPI_AdminKeys_CreateAndList(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// 1. 创建 API Key (POST)
	body := []byte(`{"name":"test-key","permissions":["all"]}`)
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
}

func TestAPI_TunnelPolicies_GetAndUpdate(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// 获取默认策略
	req := httptest.NewRequest(http.MethodGet, "/api/admin/policies", nil)
	w := httptest.NewRecorder()
	s.handleAPIAdminPolicies(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("获取策略期望 200，得到 %d", w.Code)
	}

	// 更新策略
	newPolicyBody := []byte(`{"min_port":10000,"max_port":20000}`)
	req2 := httptest.NewRequest(http.MethodPut, "/api/admin/policies", bytes.NewReader(newPolicyBody))
	w2 := httptest.NewRecorder()
	s.handleAPIAdminPolicies(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("更新策略期望 200，得到 %d", w2.Code)
	}

	// 重新获取验证
	req3 := httptest.NewRequest(http.MethodGet, "/api/admin/policies", nil)
	w3 := httptest.NewRecorder()
	s.handleAPIAdminPolicies(w3, req3)

	var policy map[string]any
	json.NewDecoder(w3.Body).Decode(&policy)
	
	if val, ok := policy["min_port"].(float64); !ok || val != 10000 {
		t.Errorf("策略更新失败，min_port 期望 10000，得到 %v", policy["min_port"])
	}
}

func TestAPI_AdminLogs_And_Events(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	// 产生一些日志和事件
	s.adminStore.AddSystemLog("INFO", "system started", "test")
	s.adminStore.AddSystemLog("ERROR", "test error", "test")
	s.adminStore.AddEvent("user-test", "agent connected")

	// 测试获取日志
	req := httptest.NewRequest(http.MethodGet, "/api/admin/logs?limit=10", nil)
	w := httptest.NewRecorder()
	s.handleAPIAdminLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("获取日志期望 200，得到 %d", w.Code)
	}

	var logs []map[string]any
	json.NewDecoder(w.Body).Decode(&logs)
	if len(logs) < 2 {
		t.Errorf("期望至少 2 条日志，得到 %d", len(logs))
	}

	// 测试获取事件
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/events?limit=10", nil)
	w2 := httptest.NewRecorder()
	s.handleAPIAdminEvents(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("获取事件期望 200，得到 %d", w2.Code)
	}

	var events []map[string]any
	json.NewDecoder(w2.Body).Decode(&events)
	if len(events) != 1 {
		t.Errorf("期望 1 条事件，得到 %d", len(events))
	}
}
