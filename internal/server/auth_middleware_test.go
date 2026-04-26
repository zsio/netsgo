package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// setupMockAdminStore 创建一个用于测试的临时 AdminStore
func setupMockAdminStore(t *testing.T) (*AdminStore, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "admin_store_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	dbPath := filepath.Join(tmpDir, "admin.db")
	store, err := NewAdminStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create AdminStore: %v", err)
	}
	store.bcryptCost = bcrypt.MinCost // 测试用最低强度，避免 bcrypt 拖慢测试套件

	// 初始化一个默认的 admin
	err = store.Initialize("admin", "password123", "localhost", nil)
	if err != nil {
		t.Fatalf("Failed to initialize AdminStore: %v", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	return store, cleanup
}

func clearJWTSecretForTest(t *testing.T, store *AdminStore) {
	t.Helper()

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, err := store.db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatalf("enable ignore_check_constraints: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE server_config SET initialized = 1, jwt_secret = '' WHERE id = 1`); err != nil {
		t.Fatalf("clear jwt_secret: %v", err)
	}
	if _, err := store.db.Exec(`PRAGMA ignore_check_constraints = OFF`); err != nil {
		t.Fatalf("disable ignore_check_constraints: %v", err)
	}
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
		t.Errorf("Missing Authorization header should return 401, got %d", w.Code)
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
		t.Errorf("Invalid Authorization format should return 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_InvalidTokenSignature(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store

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
		t.Errorf("Token with invalid signature should return 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_FallbackSecretTokenRejected(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store

	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "test-client")
	claims := AdminClaims{
		SessionID: session.ID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte("netsgo-dev-fallback-secret"))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Token signed with old fallback secret should return 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store

	// 创建一个已过期的 token
	claims := AdminClaims{
		SessionID: "fake-session",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	secret, err := store.GetJWTSecret()
	if err != nil {
		t.Fatalf("Failed to get JWT Secret: %v", err)
	}
	tokenString, _ := token.SignedString(secret)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expired token should return 401, got %d", w.Code)
	}
}

func TestGenerateAdminToken_MissingJWTSecret(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store
	clearJWTSecretForTest(t, store)

	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "test-client")
	_, err := s.GenerateAdminToken(session)
	if !errors.Is(err, errJWTSecretMissing) {
		t.Fatalf("GenerateAdminToken should return errJWTSecretMissing when JWT Secret is missing, got %v", err)
	}
}

func TestAuthMiddleware_MissingJWTSecret(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store
	clearJWTSecretForTest(t, store)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Should return 500 when JWT Secret is missing, got %d", w.Code)
	}
}

func TestAuthMiddleware_ValidTokenButSessionRevoked(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store

	// 创建一个合法的 session 并生成 token
	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "test-client")
	tokenString, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	// 模拟 session 被注销/踢出
	mustDeleteSession(t, store, session.ID)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Token for revoked Session should return 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ValidTokenSuccess(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store

	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "test-client")
	tokenString, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	req.Header.Set("User-Agent", "test-client")
	w := httptest.NewRecorder()

	// 验证请求是否成功到达了 handler
	handlerCalled := false
	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)

		// 验证上下文中是否成功注入了 session 信息
		info := GetSessionFromContext(r.Context())
		if info == nil {
			t.Errorf("SessionInfo not found in context")
		} else if info.SessionID != session.ID {
			t.Errorf("Expected SessionID %s in context, got %s", session.ID, info.SessionID)
		}

		// 验证兼容接口
		adminInfo := GetAdminFromContext(r.Context())
		if adminInfo == nil || adminInfo.SessionID != session.ID {
			t.Errorf("GetAdminFromContext failed to get info")
		}
	})

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Valid token should return 200, got %d", w.Code)
	}
	if !handlerCalled {
		t.Errorf("Handler was not called")
	}
}

func TestGetSessionFromContext_Nil(t *testing.T) {
	ctx := context.Background()
	info := GetSessionFromContext(ctx)
	if info != nil {
		t.Errorf("Empty Context should return nil")
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
		t.Errorf("Uninitialized Store should return 500 (or 401 depending on secret validation result), got %d", w.Code)
	}
}

// ========== P5: Cookie 认证测试 ==========

func TestAuthMiddleware_CookieAuth_Success(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store

	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "test-client")
	tokenString, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokenString})
	req.Header.Set("User-Agent", "test-client")
	w := httptest.NewRecorder()

	handlerCalled := false
	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		info := GetSessionFromContext(r.Context())
		if info == nil {
			t.Errorf("SessionInfo not found in context")
		} else if info.SessionID != session.ID {
			t.Errorf("Expected SessionID %s in context, got %s", session.ID, info.SessionID)
		}
		w.WriteHeader(http.StatusOK)
	})

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Valid token in Cookie should return 200, got %d", w.Code)
	}
	if !handlerCalled {
		t.Errorf("Handler was not called")
	}
}

func TestAuthMiddleware_CookieAuth_InvalidToken(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "invalid-token"})
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Invalid token in Cookie should return 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_HeaderPriority(t *testing.T) {
	store, cleanup := setupMockAdminStore(t)
	defer cleanup()

	s := New(0)
	s.auth.adminStore = store

	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "test-client")
	validToken, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	// Header 中放合法 token，Cookie 中放非法 token
	// 应使用 Header 中的 token 认证成功
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	req.Header.Set("User-Agent", "test-client")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "invalid-cookie-token"})
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Header takes precedence over Cookie, should return 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_NoCredentials(t *testing.T) {
	s := New(0)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Missing both header and cookie should return 401, got %d", w.Code)
	}
}
