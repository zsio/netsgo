package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ============================================================
// AdminStore unit tests
// ============================================================

// helper: create a temporary AdminStore
func newTestAdminStore(t *testing.T) *AdminStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewAdminStore(filepath.Join(dir, "admin.json"))
	if err != nil {
		t.Fatalf("NewAdminStore failed: %v", err)
	}
	store.bcryptCost = bcrypt.MinCost // use minimum cost for testing to avoid bcrypt slowing down test suite
	return store
}

// helper: 创建并初始化一个 AdminStore
func newInitializedAdminStore(t *testing.T) *AdminStore {
	t.Helper()
	store := newTestAdminStore(t)
	if err := store.Initialize("admin", "Admin1234", "https://example.com", []PortRange{{Start: 8000, End: 9000}}); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return store
}

// --- 初始化 ---

func TestAdminStore_NewEmpty(t *testing.T) {
	store := newTestAdminStore(t)
	if store.IsInitialized() {
		t.Error("newly created store should not be initialized")
	}
}

func TestAdminStore_Initialize_Success(t *testing.T) {
	store := newTestAdminStore(t)

	err := store.Initialize("admin", "Admin1234", "https://tunnel.example.com", []PortRange{
		{Start: 8000, End: 9000},
		{Start: 3000, End: 3000},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if !store.IsInitialized() {
		t.Error("should return true after initialization")
	}
}

func TestAdminStore_Initialize_Duplicate(t *testing.T) {
	store := newInitializedAdminStore(t)

	err := store.Initialize("admin2", "Pass1234", "", nil)
	if err == nil {
		t.Error("duplicate initialization should return error")
	}
}

// --- validatePassword ---

func TestValidatePassword_Good(t *testing.T) {
	cases := []string{"Admin123", "pass1234", "12345678a", "Aa1!@#$%"}
	for _, pw := range cases {
		if err := validatePassword(pw); err != nil {
			t.Errorf("password %q should pass but got error: %v", pw, err)
		}
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	if err := validatePassword("Ab1234"); err == nil {
		t.Error("6-character password should be rejected")
	}
}

func TestValidatePassword_LettersOnly(t *testing.T) {
	if err := validatePassword("abcdefgh"); err == nil {
		t.Error("alphabetic-only password should be rejected")
	}
}

func TestValidatePassword_DigitsOnly(t *testing.T) {
	if err := validatePassword("12345678"); err == nil {
		t.Error("numeric-only password should be rejected")
	}
}

func TestValidatePassword_Empty(t *testing.T) {
	if err := validatePassword(""); err == nil {
		t.Error("empty password should be rejected")
	}
}

func TestAdminStore_Initialize_WeakPassword(t *testing.T) {
	store := newTestAdminStore(t)
	err := store.Initialize("admin", "short1", "", nil)
	if err == nil {
		t.Error("weak password should cause initialization to fail")
	}
	if store.IsInitialized() {
		t.Error("weak password should not cause Initialized flag to become true")
	}
}

// --- 管理员认证 ---

func TestAdminStore_ValidateAdminPassword_Success(t *testing.T) {
	store := newInitializedAdminStore(t)

	user, err := store.ValidateAdminPassword("admin", "Admin1234")
	if err != nil {
		t.Fatalf("correct password should pass: %v", err)
	}
	if user == nil {
		t.Fatal("user should not be nil")
	}
	if user.Username != "admin" {
		t.Errorf("expected username admin, got %s", user.Username)
	}
	if user.Role != "admin" {
		t.Errorf("expected role admin, got %s", user.Role)
	}
}

func TestAdminStore_ValidateAdminPassword_Wrong(t *testing.T) {
	store := newInitializedAdminStore(t)

	_, err := store.ValidateAdminPassword("admin", "WrongPass1")
	if err == nil {
		t.Error("incorrect password should be rejected")
	}
}

func TestAdminStore_ValidateAdminPassword_NoUser(t *testing.T) {
	store := newInitializedAdminStore(t)

	_, err := store.ValidateAdminPassword("nonexistent", "Admin1234")
	if err == nil {
		t.Error("non-existent user should be rejected")
	}
}

// --- JWT Secret ---

func TestAdminStore_GetJWTSecret_BeforeInit(t *testing.T) {
	store := newTestAdminStore(t)
	secret, err := store.GetJWTSecret()
	if !errors.Is(err, errJWTSecretUnavailable) {
		t.Fatalf("should return errJWTSecretUnavailable when not initialized, got %v", err)
	}
	if len(secret) != 0 {
		t.Errorf("should not return secret when not initialized, got %q", string(secret))
	}
}

func TestAdminStore_GetJWTSecret_AfterInit(t *testing.T) {
	store := newInitializedAdminStore(t)
	secret, err := store.GetJWTSecret()
	if err != nil {
		t.Fatalf("getting secret after initialization should not error: %v", err)
	}
	if len(secret) == 0 {
		t.Error("should return non-empty secret after initialization")
	}
	if string(secret) == "netsgo-dev-fallback-secret" {
		t.Error("should not return fallback secret after initialization")
	}
}

func TestAdminStore_NewInitializedWithoutJWTSecret_Fails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.json")
	data := []byte(`{
  "initialized": true,
  "jwt_secret": "",
  "api_keys": [],
  "admin_users": [],
  "clients": [],
  "client_tokens": [],
  "events": [],
  "sessions": []
}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("failed to write test admin.json: %v", err)
	}

	_, err := NewAdminStore(path)
	if !errors.Is(err, errJWTSecretMissing) {
		t.Fatalf("initialized instance missing jwt_secret should return errJWTSecretMissing, got %v", err)
	}
}

func TestAdminStore_NewCorruptedFileFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.json")
	if err := os.WriteFile(path, []byte(`{{{invalid json`), 0o600); err != nil {
		t.Fatalf("failed to write corrupted admin.json: %v", err)
	}

	if _, err := NewAdminStore(path); err == nil {
		t.Fatal("corrupted admin.json should cause NewAdminStore to return error")
	}
}

// --- 端口白名单 ---

func TestAdminStore_IsPortAllowed_EmptyWhitelist(t *testing.T) {
	store := newTestAdminStore(t)
	// 未初始化 → 白名单为空 → 全放行
	if !store.IsPortAllowed(9999) {
		t.Error("should allow all when whitelist is empty")
	}
}

func TestAdminStore_IsPortAllowed_InRange(t *testing.T) {
	store := newInitializedAdminStore(t) // AllowedPorts: [{8000, 9000}]

	if !store.IsPortAllowed(8000) {
		t.Error("8000 should be allowed in range [8000,9000]")
	}
	if !store.IsPortAllowed(8500) {
		t.Error("8500 should be allowed in range")
	}
	if !store.IsPortAllowed(9000) {
		t.Error("9000 should be allowed at range boundary")
	}
}

func TestAdminStore_IsPortAllowed_OutOfRange(t *testing.T) {
	store := newInitializedAdminStore(t) // AllowedPorts: [{8000, 9000}]

	if store.IsPortAllowed(7999) {
		t.Error("7999 should be rejected when not in range")
	}
	if store.IsPortAllowed(9001) {
		t.Error("9001 should be rejected when not in range")
	}
	if store.IsPortAllowed(80) {
		t.Error("80 should be rejected when not in range")
	}
}

// --- Client Key ---

func TestAdminStore_ValidateClientKey_NoKeysBeforeInit(t *testing.T) {
	store := newTestAdminStore(t)
	valid, err := store.ValidateClientKey("")
	if valid {
		t.Error("should not accept client connections when not initialized")
	}
	if err == nil {
		t.Error("should return error when not initialized")
	}
}

func TestAdminStore_ValidateClientKey_NoKeysAfterInit(t *testing.T) {
	store := newInitializedAdminStore(t)

	valid, err := store.ValidateClientKey("")
	if valid {
		t.Error("should not allow connections when key not configured after initialization")
	}
	if err == nil {
		t.Error("should return error when key not configured after initialization")
	}
}

func TestAdminStore_ValidateClientKey_Valid(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-test-key-123"
	if _, err := store.AddAPIKey("test", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	valid, err := store.ValidateClientKey(rawKey)
	if !valid || err != nil {
		t.Errorf("valid key should pass: valid=%v, err=%v", valid, err)
	}
}

func TestAdminStore_ValidateClientKey_Invalid(t *testing.T) {
	store := newTestAdminStore(t)
	if _, err := store.AddAPIKey("test", "sk-real-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	valid, err := store.ValidateClientKey("sk-wrong-key")
	if valid {
		t.Error("invalid key should be rejected")
	}
	if err == nil {
		t.Error("invalid key should return error")
	}
}

func TestAdminStore_ValidateClientKey_EmptyWhenKeysExist(t *testing.T) {
	store := newTestAdminStore(t)
	if _, err := store.AddAPIKey("test", "sk-real-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	valid, err := store.ValidateClientKey("")
	if valid {
		t.Error("empty key should be rejected when key already exists")
	}
	if err == nil {
		t.Error("should return error")
	}
}

func TestAdminStore_ValidateClientKey_Expired(t *testing.T) {
	store := newTestAdminStore(t)
	past := time.Now().Add(-1 * time.Hour)
	if _, err := store.AddAPIKey("expired", "sk-expired-key", []string{"connect"}, &past); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	valid, err := store.ValidateClientKey("sk-expired-key")
	if valid {
		t.Error("expired key should be rejected")
	}
	if err == nil {
		t.Error("expired key should return error")
	}
}

// --- API Key CRUD ---

func TestAdminStore_AddAndGetAPIKeys(t *testing.T) {
	store := newTestAdminStore(t)

	keys := store.GetAPIKeys()
	if len(keys) != 0 {
		t.Errorf("should be empty initially, got %d", len(keys))
	}

	if _, err := store.AddAPIKey("key1", "sk-key1", []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}
	if _, err := store.AddAPIKey("key2", "sk-key2", []string{"manage"}, nil); err == nil {
		t.Fatal("unsupported permission should return error")
	}

	keys = store.GetAPIKeys()
	if len(keys) != 1 {
		t.Errorf("expected 1 available key, got %d", len(keys))
	}
}

func TestAdminStore_APIKey_DisableEnableDeleteLifecycle(t *testing.T) {
	store := newInitializedAdminStore(t)

	rawKey := "sk-lifecycle-key"
	key, err := store.AddAPIKey("lifecycle", rawKey, []string{"connect"}, nil)
	if err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	if valid, err := store.ValidateClientKey(rawKey); !valid || err != nil {
		t.Fatalf("new key should be available: valid=%v err=%v", valid, err)
	}

	if err := store.SetAPIKeyActive(key.ID, false); err != nil {
		t.Fatalf("disabling key failed: %v", err)
	}
	if valid, err := store.ValidateClientKey(rawKey); valid || err == nil {
		t.Fatalf("key should be rejected after disabling: valid=%v err=%v", valid, err)
	}

	if err := store.SetAPIKeyActive(key.ID, true); err != nil {
		t.Fatalf("enabling key failed: %v", err)
	}
	if valid, err := store.ValidateClientKey(rawKey); !valid || err != nil {
		t.Fatalf("key should be allowed after re-enabling: valid=%v err=%v", valid, err)
	}

	if err := store.DeleteAPIKey(key.ID); err != nil {
		t.Fatalf("deleting key failed: %v", err)
	}
	if valid, err := store.ValidateClientKey(rawKey); valid || err == nil {
		t.Fatalf("key should be rejected after deletion: valid=%v err=%v", valid, err)
	}
}

func TestAdminStore_PersistedSecretsSurviveReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.json")

	store, err := NewAdminStore(path)
	if err != nil {
		t.Fatalf("NewAdminStore failed: %v", err)
	}
	if err := store.Initialize("admin", "Admin1234", "https://example.com", nil); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if _, err := store.AddAPIKey("persisted", "sk-persisted", []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	reloaded, err := NewAdminStore(path)
	if err != nil {
		t.Fatalf("reloading AdminStore failed: %v", err)
	}

	if _, err := reloaded.ValidateAdminPassword("admin", "Admin1234"); err != nil {
		t.Fatalf("admin password should still be verifiable after reload: %v", err)
	}
	if valid, err := reloaded.ValidateClientKey("sk-persisted"); !valid || err != nil {
		t.Fatalf("API key should still be valid after reload: valid=%v err=%v", valid, err)
	}
}

// --- Session CRUD ---

func TestAdminStore_Session_CreateAndGet(t *testing.T) {
	store := newInitializedAdminStore(t)

	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "test-client")
	if session == nil {
		t.Fatal("CreateSession should not return nil")
	}
	if session.ID == "" {
		t.Error("session ID should not be empty")
	}

	got := store.GetSession(session.ID)
	if got == nil {
		t.Fatal("GetSession should return the created session")
	}
	if got.Username != "admin" {
		t.Errorf("expected username admin, got %s", got.Username)
	}
}

func TestAdminStore_Session_GetExpired(t *testing.T) {
	store := newInitializedAdminStore(t)

	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "ua")

	// 手动设置为过期
	store.mu.Lock()
	for i := range store.data.Sessions {
		if store.data.Sessions[i].ID == session.ID {
			store.data.Sessions[i].ExpiresAt = time.Now().Add(-1 * time.Hour)
		}
	}
	store.mu.Unlock()

	got := store.GetSession(session.ID)
	if got != nil {
		t.Error("expired session should return nil")
	}
}

func TestAdminStore_Session_GetNotFound(t *testing.T) {
	store := newInitializedAdminStore(t)

	got := store.GetSession("not-exist-id")
	if got != nil {
		t.Error("non-existent session should return nil")
	}
}

func TestAdminStore_Session_Delete(t *testing.T) {
	store := newInitializedAdminStore(t)

	session := mustCreateSession(t, store, "user-1", "admin", "admin", "127.0.0.1", "ua")
	mustDeleteSession(t, store, session.ID)

	got := store.GetSession(session.ID)
	if got != nil {
		t.Error("session should return nil after deletion")
	}
}

func TestAdminStore_Session_SingleLogin(t *testing.T) {
	store := newInitializedAdminStore(t)

	// 同一 userID 创建两次 session → 旧的被踢出
	s1 := mustCreateSession(t, store, "user-1", "admin", "admin", "1.1.1.1", "ua1")
	s2 := mustCreateSession(t, store, "user-1", "admin", "admin", "2.2.2.2", "ua2")

	got1 := store.GetSession(s1.ID)
	if got1 != nil {
		t.Error("old session should be kicked out (single-session login)")
	}

	got2 := store.GetSession(s2.ID)
	if got2 == nil {
		t.Error("new session should be valid")
	}
}

func TestAdminStore_Session_DeleteByUserID(t *testing.T) {
	store := newInitializedAdminStore(t)

	s1 := mustCreateSession(t, store, "user-1", "admin", "admin", "1.1.1.1", "ua")
	if err := store.DeleteSessionsByUserID("user-1"); err != nil {
		t.Fatalf("DeleteSessionsByUserID failed: %v", err)
	}

	if store.GetSession(s1.ID) != nil {
		t.Error("session should not exist after DeleteSessionsByUserID")
	}
}

func TestAdminStore_Session_CleanExpired(t *testing.T) {
	store := newInitializedAdminStore(t)

	mustCreateSession(t, store, "user-1", "admin", "admin", "1.1.1.1", "ua")

	// 手动将所有 session 设为过期
	store.mu.Lock()
	for i := range store.data.Sessions {
		store.data.Sessions[i].ExpiresAt = time.Now().Add(-1 * time.Hour)
	}
	store.mu.Unlock()

	if err := store.CleanExpiredSessions(); err != nil {
		t.Fatalf("CleanExpiredSessions failed: %v", err)
	}

	store.mu.RLock()
	count := len(store.data.Sessions)
	store.mu.RUnlock()

	if count != 0 {
		t.Errorf("should have no sessions after cleanup, got %d", count)
	}
}

func TestAdminStore_CreateSession_SaveFailureRollsBack(t *testing.T) {
	store := newInitializedAdminStore(t)
	originalSessions := len(store.data.Sessions)
	originalLastLogin := store.data.AdminUsers[0].LastLogin
	saveErr := errors.New("save failed")
	store.failSaveErr = saveErr
	store.failSaveCount = 1

	session, err := store.CreateSession("user-1", "admin", "admin", "127.0.0.1", "ua")
	if !errors.Is(err, saveErr) {
		t.Fatalf("CreateSession should return save error, got %v", err)
	}
	if session != nil {
		t.Fatal("should not return session when save fails")
	}
	if got := len(store.data.Sessions); got != originalSessions {
		t.Fatalf("session count should rollback to %d after save failure, got %d", originalSessions, got)
	}
	if store.data.AdminUsers[0].LastLogin != originalLastLogin {
		t.Fatal("LastLogin should rollback after save failure")
	}
}

// --- Login Time ---

func TestAdminStore_UpdateAdminLoginTime(t *testing.T) {
	store := newInitializedAdminStore(t)

	user, _ := store.ValidateAdminPassword("admin", "Admin1234")
	if err := store.UpdateAdminLoginTime(user.ID); err != nil {
		t.Fatalf("UpdateAdminLoginTime failed: %v", err)
	}

	// 再次获取用户信息
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, u := range store.data.AdminUsers {
		if u.ID == user.ID {
			if u.LastLogin == nil {
				t.Error("LastLogin should be set")
			}
			return
		}
	}
	t.Error("user not found")
}

func TestAdminStore_AddAPIKey_SaveFailureRollsBack(t *testing.T) {
	store := newInitializedAdminStore(t)
	saveErr := errors.New("save failed")
	store.failSaveErr = saveErr
	store.failSaveCount = 1

	key, err := store.AddAPIKey("test", "sk-real-key", []string{"connect"}, nil)
	if !errors.Is(err, saveErr) {
		t.Fatalf("AddAPIKey should return save error, got %v", err)
	}
	if key != nil {
		t.Fatal("should not return API key when save fails")
	}
	if got := len(store.GetAPIKeys()); got != 0 {
		t.Fatalf("should not have residual API key after save failure, got %d", got)
	}
}

// --- Server Config ---

func TestAdminStore_GetServerConfig(t *testing.T) {
	store := newInitializedAdminStore(t)

	cfg := store.GetServerConfig()
	if cfg.ServerAddr != "https://example.com" {
		t.Errorf("expected ServerAddr https://example.com, got %s", cfg.ServerAddr)
	}
	if len(cfg.AllowedPorts) != 1 {
		t.Errorf("expected 1 AllowedPorts range, got %d", len(cfg.AllowedPorts))
	}
}

// ============================================================
// Client Token 测试
// ============================================================

func TestAdminStore_Token_ExchangeAndValidate(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-test-key"
	if _, err := store.AddAPIKey("test", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	// 兑换 Token
	tokenStr, clientToken, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken failed: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("token should not be empty")
	}
	if clientToken == nil {
		t.Fatal("ClientToken should not be nil")
	}
	if clientToken.InstallID != "install-1" {
		t.Errorf("expected InstallID install-1, got %s", clientToken.InstallID)
	}

	// 验证 Token
	result, err := store.ValidateClientToken(tokenStr, "install-1")
	if err != nil {
		t.Fatalf("ValidateClientToken failed: %v", err)
	}
	if result.ID != clientToken.ID {
		t.Errorf("token ID mismatch: %s != %s", result.ID, clientToken.ID)
	}
}

func TestAdminStore_Token_ExchangeSaveFailureRollsBack(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-test-key"
	if _, err := store.AddAPIKey("test", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}
	saveErr := errors.New("save failed")
	store.failSaveErr = saveErr
	store.failSaveCount = 1

	tokenStr, clientToken, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678")
	if !errors.Is(err, saveErr) {
		t.Fatalf("ExchangeToken should return save error, got %v", err)
	}
	if tokenStr != "" || clientToken != nil {
		t.Fatal("should not return valid token when save fails")
	}
	if got := len(store.data.ClientTokens); got != 0 {
		t.Fatalf("should not have residual ClientToken after save failure, got %d", got)
	}
	keys := store.GetAPIKeys()
	if len(keys) != 1 || keys[0].UseCount != 0 {
		t.Fatalf("API key UseCount should rollback to 0 after save failure, got %+v", keys)
	}
}

func TestAdminStore_Token_ExchangeConsumesKeyUseCount(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-counted-key"
	if _, err := store.AddAPIKey("counted", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	// 兑换 Token — 应消耗 Key use_count
	_, _, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken failed: %v", err)
	}

	keys := store.GetAPIKeys()
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0].UseCount != 1 {
		t.Errorf("expected key UseCount 1 after exchange, got %d", keys[0].UseCount)
	}

	// 再次验证 Key（不应增加 use_count）
	valid, _ := store.ValidateClientKey(rawKey)
	if !valid {
		t.Fatal("key should still be valid")
	}

	keys = store.GetAPIKeys()
	if keys[0].UseCount != 1 {
		t.Errorf("ValidateClientKey should not increase UseCount, expected 1, got %d", keys[0].UseCount)
	}
}

func TestAdminStore_Token_ValidateExpired(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-expiry-key"
	if _, err := store.AddAPIKey("test", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	tokenStr, _, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken failed: %v", err)
	}

	// 手动设置 Token 为过期（超过 7 天不活跃）
	store.mu.Lock()
	for i := range store.data.ClientTokens {
		if store.data.ClientTokens[i].InstallID == "install-1" {
			store.data.ClientTokens[i].LastActiveAt = time.Now().Add(-8 * 24 * time.Hour)
		}
	}
	store.mu.Unlock()

	_, err = store.ValidateClientToken(tokenStr, "install-1")
	if err == nil {
		t.Error("expired token should fail validation")
	}
	if !errors.Is(err, ErrClientTokenExpired) {
		t.Fatalf("expired token should return ErrClientTokenExpired, got %v", err)
	}
}

func TestAdminStore_Token_ValidateSaveFailureReturnsError(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-validate-key"
	if _, err := store.AddAPIKey("test", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	tokenStr, clientToken, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken failed: %v", err)
	}

	store.mu.RLock()
	originalLastActiveAt := store.data.ClientTokens[0].LastActiveAt
	store.mu.RUnlock()

	saveErr := errors.New("save failed")
	store.failSaveErr = saveErr
	store.failSaveCount = 1

	result, err := store.ValidateClientToken(tokenStr, "install-1")
	if !errors.Is(err, saveErr) {
		t.Fatalf("ValidateClientToken should return save error, got %v", err)
	}
	if result != nil {
		t.Fatal("should not return valid token when save fails")
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if len(store.data.ClientTokens) != 1 {
		t.Fatalf("token record count should remain 1 after save failure, got %d", len(store.data.ClientTokens))
	}
	if store.data.ClientTokens[0].ID != clientToken.ID {
		t.Fatalf("token ID should not change, expected %s, got %s", clientToken.ID, store.data.ClientTokens[0].ID)
	}
	if !store.data.ClientTokens[0].LastActiveAt.Equal(originalLastActiveAt) {
		t.Fatal("LastActiveAt should rollback after save failure")
	}
}

func TestAdminStore_Token_ValidateRevoked(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-revoke-key"
	if _, err := store.AddAPIKey("test", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	tokenStr, clientToken, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken failed: %v", err)
	}

	// 吊销 Token
	if err := store.RevokeToken(clientToken.ID); err != nil {
		t.Fatalf("RevokeToken failed: %v", err)
	}

	_, err = store.ValidateClientToken(tokenStr, "install-1")
	if err == nil {
		t.Error("revoked token should fail validation")
	}
	if !errors.Is(err, ErrClientTokenRevoked) {
		t.Fatalf("revoked token should return ErrClientTokenRevoked, got %v", err)
	}
}

func TestAdminStore_Token_ReuseExistingToken(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-reuse-key"
	if _, err := store.AddAPIKey("reuse", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	// 首次兑换
	_, _, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("first ExchangeToken failed: %v", err)
	}

	keys := store.GetAPIKeys()
	useCountAfterFirst := keys[0].UseCount

	// 再次调用 ExchangeToken（同一 install_id，已有有效 Token）
	newTokenStr, _, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("second ExchangeToken failed: %v", err)
	}
	if newTokenStr == "" {
		t.Fatal("should return new token string")
	}

	// Key use_count 不应增加
	keys = store.GetAPIKeys()
	if keys[0].UseCount != useCountAfterFirst {
		t.Errorf("should not consume key when token already exists: expected %d, got %d", useCountAfterFirst, keys[0].UseCount)
	}

	// 新 Token 应能验证通过
	_, err = store.ValidateClientToken(newTokenStr, "install-1")
	if err != nil {
		t.Fatalf("new token should be valid: %v", err)
	}
}

func TestAdminStore_Token_CleanExpired(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-clean-key"
	if _, err := store.AddAPIKey("test", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}

	if _, _, err := store.ExchangeToken(rawKey, "install-1", "client-1", "1.2.3.4:5678"); err != nil {
		t.Fatalf("ExchangeToken failed: %v", err)
	}

	// 手动设置为过期
	store.mu.Lock()
	for i := range store.data.ClientTokens {
		store.data.ClientTokens[i].LastActiveAt = time.Now().Add(-8 * 24 * time.Hour)
	}
	store.mu.Unlock()

	if err := store.CleanExpiredTokens(); err != nil {
		t.Fatalf("CleanExpiredTokens failed: %v", err)
	}

	store.mu.RLock()
	count := len(store.data.ClientTokens)
	store.mu.RUnlock()

	if count != 0 {
		t.Errorf("should have no tokens after cleanup, got %d", count)
	}
}
