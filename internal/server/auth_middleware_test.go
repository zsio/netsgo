package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// setupMockAdminStore 创建一个用于测试的临时 AdminStore
func setupMockAdminStore(t *testing.T) (*AdminStore, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "admin_store_test_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	dbPath := filepath.Join(tmpDir, "admin.db")
	store, err := NewAdminStore(dbPath)
	if err != nil {
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}

	// 初始化一个默认的 admin
	err = store.Initialize("admin", "password123", "localhost", nil)
	if err != nil {
		t.Fatalf("初始化 AdminStore 失败: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return store, cleanup
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	s := New(0)
	
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("缺少 Authorization 头应返回 401，得到 %d", w.Code)
	}
}

func TestAuthMiddleware_InvalidFormat(t *testing.T) {
	s := New(0)
	
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "InvalidFormatToken")
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("错误的 Authorization 格式应返回 401，得到 %d", w.Code)
	}
}

func TestAuthMiddleware_InvalidTokenSignature(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.adminStore = store

	// 创建一个被其他密钥签名的 token
	claims := AdminClaims{
		SessionID: "fake-session",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte("wrong-secret"))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("签名错误的 token 应返回 401，得到 %d", w.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.adminStore = store

	// 创建一个已过期的 token
	claims := AdminClaims{
		SessionID: "fake-session",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString(store.GetJWTSecret())

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("过期的 token 应返回 401，得到 %d", w.Code)
	}
}

func TestAuthMiddleware_ValidTokenButSessionRevoked(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.adminStore = store

	// 创建一个合法的 session 并生成 token
	session := store.CreateSession("user-1", "admin", "admin", "127.0.0.1", "test-agent")
	tokenString, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}

	// 模拟 session 被注销/踢出
	store.DeleteSession(session.ID)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Session 被注销的 token 应返回 401，得到 %d", w.Code)
	}
}

func TestAuthMiddleware_ValidTokenSuccess(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.adminStore = store

	session := store.CreateSession("user-1", "admin", "admin", "127.0.0.1", "test-agent")
	tokenString, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	// 验证请求是否成功到达了 handler
	handlerCalled := false
	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)

		// 验证上下文中是否成功注入了 session 信息
		info := GetSessionFromContext(r.Context())
		if info == nil {
			t.Errorf("上下文中未找到 SessionInfo")
		} else if info.SessionID != session.ID {
			t.Errorf("上下文中 SessionID 期望 %s，得到 %s", session.ID, info.SessionID)
		}

		// 验证兼容接口
		adminInfo := GetAdminFromContext(r.Context())
		if adminInfo == nil || adminInfo.SessionID != session.ID {
			t.Errorf("GetAdminFromContext 获取信息失败")
		}
	})

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("合法的 token 应返回 200，得到 %d", w.Code)
	}
	if !handlerCalled {
		t.Errorf("Handler 未被调用")
	}
}

func TestGetSessionFromContext_Nil(t *testing.T) {
	ctx := context.Background()
	info := GetSessionFromContext(ctx)
	if info != nil {
		t.Errorf("空 Context 应该返回 nil")
	}
}

func TestAuthMiddleware_StoreNotInitialized(t *testing.T) {
	s := New(0)
	// adminStore 为 nil
	
	// 造一个假但格式合法的 token，它的签名如果用 nil secret 会使用默认 []byte{} (或直接由于我们代码里使用 secret 而 panic/报错)
	// 为了确保走到 store 未初始化判断，我们需要给它一个 store，但为了避免麻烦，其实 requireAuth 里有检测 store nil 的逻辑
	
	// Create a token signed with empty secret so it passes signature verification
	claims := AdminClaims{
		SessionID: "fake",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte{})
	
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Store 未初始化应返回 500 (或 401 视秘钥验证结果)，得到 %d", w.Code)
	}
}
