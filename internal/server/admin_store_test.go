package server

import (
	"path/filepath"
	"testing"
	"time"
)

// ============================================================
// AdminStore 单元测试
// ============================================================

// helper: 创建一个临时的 AdminStore
func newTestAdminStore(t *testing.T) *AdminStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewAdminStore(filepath.Join(dir, "admin.json"))
	if err != nil {
		t.Fatalf("NewAdminStore 失败: %v", err)
	}
	return store
}

// helper: 创建并初始化一个 AdminStore
func newInitializedAdminStore(t *testing.T) *AdminStore {
	t.Helper()
	store := newTestAdminStore(t)
	if err := store.Initialize("admin", "Admin1234", "https://example.com", []PortRange{{Start: 8000, End: 9000}}); err != nil {
		t.Fatalf("Initialize 失败: %v", err)
	}
	return store
}

// --- 初始化 ---

func TestAdminStore_NewEmpty(t *testing.T) {
	store := newTestAdminStore(t)
	if store.IsInitialized() {
		t.Error("新建的 store 不应已初始化")
	}
}

func TestAdminStore_Initialize_Success(t *testing.T) {
	store := newTestAdminStore(t)

	err := store.Initialize("admin", "Admin1234", "https://tunnel.example.com", []PortRange{
		{Start: 8000, End: 9000},
		{Start: 3000, End: 3000},
	})
	if err != nil {
		t.Fatalf("Initialize 失败: %v", err)
	}
	if !store.IsInitialized() {
		t.Error("初始化后应返回 true")
	}
}

func TestAdminStore_Initialize_Duplicate(t *testing.T) {
	store := newInitializedAdminStore(t)

	err := store.Initialize("admin2", "Pass1234", "", nil)
	if err == nil {
		t.Error("重复初始化应报错")
	}
}

// --- validatePassword ---

func TestValidatePassword_Good(t *testing.T) {
	cases := []string{"Admin123", "pass1234", "12345678a", "Aa1!@#$%"}
	for _, pw := range cases {
		if err := validatePassword(pw); err != nil {
			t.Errorf("密码 %q 应通过，但报错: %v", pw, err)
		}
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	if err := validatePassword("Ab1234"); err == nil {
		t.Error("6位密码应被拒绝")
	}
}

func TestValidatePassword_LettersOnly(t *testing.T) {
	if err := validatePassword("abcdefgh"); err == nil {
		t.Error("纯字母密码应被拒绝")
	}
}

func TestValidatePassword_DigitsOnly(t *testing.T) {
	if err := validatePassword("12345678"); err == nil {
		t.Error("纯数字密码应被拒绝")
	}
}

func TestValidatePassword_Empty(t *testing.T) {
	if err := validatePassword(""); err == nil {
		t.Error("空密码应被拒绝")
	}
}

func TestAdminStore_Initialize_WeakPassword(t *testing.T) {
	store := newTestAdminStore(t)
	err := store.Initialize("admin", "short1", "", nil)
	if err == nil {
		t.Error("弱密码应导致初始化失败")
	}
	if store.IsInitialized() {
		t.Error("弱密码不应导致 Initialized 标志变为 true")
	}
}

// --- 管理员认证 ---

func TestAdminStore_ValidateAdminPassword_Success(t *testing.T) {
	store := newInitializedAdminStore(t)

	user, err := store.ValidateAdminPassword("admin", "Admin1234")
	if err != nil {
		t.Fatalf("正确密码应通过: %v", err)
	}
	if user == nil {
		t.Fatal("用户不应为 nil")
	}
	if user.Username != "admin" {
		t.Errorf("用户名期望 admin，得到 %s", user.Username)
	}
	if user.Role != "admin" {
		t.Errorf("角色期望 admin，得到 %s", user.Role)
	}
}

func TestAdminStore_ValidateAdminPassword_Wrong(t *testing.T) {
	store := newInitializedAdminStore(t)

	_, err := store.ValidateAdminPassword("admin", "WrongPass1")
	if err == nil {
		t.Error("错误密码应被拒绝")
	}
}

func TestAdminStore_ValidateAdminPassword_NoUser(t *testing.T) {
	store := newInitializedAdminStore(t)

	_, err := store.ValidateAdminPassword("nonexistent", "Admin1234")
	if err == nil {
		t.Error("不存在的用户应被拒绝")
	}
}

// --- JWT Secret ---

func TestAdminStore_GetJWTSecret_BeforeInit(t *testing.T) {
	store := newTestAdminStore(t)
	secret := store.GetJWTSecret()
	if len(secret) == 0 {
		t.Error("未初始化时也应返回 fallback secret")
	}
}

func TestAdminStore_GetJWTSecret_AfterInit(t *testing.T) {
	store := newInitializedAdminStore(t)
	secret := store.GetJWTSecret()
	if len(secret) == 0 {
		t.Error("初始化后应返回非空 secret")
	}
	// 应该不是 fallback
	if string(secret) == "netsgo-dev-fallback-secret" {
		t.Error("初始化后不应返回 fallback secret")
	}
}

// --- 端口白名单 ---

func TestAdminStore_IsPortAllowed_EmptyWhitelist(t *testing.T) {
	store := newTestAdminStore(t)
	// 未初始化 → 白名单为空 → 全放行
	if !store.IsPortAllowed(9999) {
		t.Error("白名单为空时应全放行")
	}
}

func TestAdminStore_IsPortAllowed_InRange(t *testing.T) {
	store := newInitializedAdminStore(t) // AllowedPorts: [{8000, 9000}]

	if !store.IsPortAllowed(8000) {
		t.Error("8000 在 [8000,9000] 范围内应允许")
	}
	if !store.IsPortAllowed(8500) {
		t.Error("8500 在范围内应允许")
	}
	if !store.IsPortAllowed(9000) {
		t.Error("9000 在范围边界应允许")
	}
}

func TestAdminStore_IsPortAllowed_OutOfRange(t *testing.T) {
	store := newInitializedAdminStore(t) // AllowedPorts: [{8000, 9000}]

	if store.IsPortAllowed(7999) {
		t.Error("7999 不在范围内应拒绝")
	}
	if store.IsPortAllowed(9001) {
		t.Error("9001 不在范围内应拒绝")
	}
	if store.IsPortAllowed(80) {
		t.Error("80 不在范围内应拒绝")
	}
}

// --- Agent Key ---

func TestAdminStore_ValidateAgentKey_NoKeys(t *testing.T) {
	store := newTestAdminStore(t)
	// 无 key 时开放
	valid, err := store.ValidateAgentKey("")
	if !valid || err != nil {
		t.Error("无 Key 配置时应开放所有连接")
	}
}

func TestAdminStore_ValidateAgentKey_NoKeysAfterInit(t *testing.T) {
	store := newInitializedAdminStore(t)

	valid, err := store.ValidateAgentKey("")
	if valid {
		t.Error("初始化后未配置 Key 不应开放连接")
	}
	if err == nil {
		t.Error("初始化后未配置 Key 应返回错误")
	}
}

func TestAdminStore_ValidateAgentKey_Valid(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-test-key-123"
	store.AddAPIKey("test", rawKey, []string{"connect"}, nil)

	valid, err := store.ValidateAgentKey(rawKey)
	if !valid || err != nil {
		t.Errorf("有效 Key 应通过: valid=%v, err=%v", valid, err)
	}
}

func TestAdminStore_ValidateAgentKey_Invalid(t *testing.T) {
	store := newTestAdminStore(t)
	store.AddAPIKey("test", "sk-real-key", []string{"connect"}, nil)

	valid, err := store.ValidateAgentKey("sk-wrong-key")
	if valid {
		t.Error("无效 Key 应被拒绝")
	}
	if err == nil {
		t.Error("无效 Key 应返回错误")
	}
}

func TestAdminStore_ValidateAgentKey_EmptyWhenKeysExist(t *testing.T) {
	store := newTestAdminStore(t)
	store.AddAPIKey("test", "sk-real-key", []string{"connect"}, nil)

	valid, err := store.ValidateAgentKey("")
	if valid {
		t.Error("已有 Key 时空 Key 应被拒绝")
	}
	if err == nil {
		t.Error("应返回错误")
	}
}

func TestAdminStore_ValidateAgentKey_Expired(t *testing.T) {
	store := newTestAdminStore(t)
	past := time.Now().Add(-1 * time.Hour)
	store.AddAPIKey("expired", "sk-expired-key", []string{"connect"}, &past)

	valid, err := store.ValidateAgentKey("sk-expired-key")
	if valid {
		t.Error("过期 Key 应被拒绝")
	}
	if err == nil {
		t.Error("过期 Key 应返回错误")
	}
}

// --- API Key CRUD ---

func TestAdminStore_AddAndGetAPIKeys(t *testing.T) {
	store := newTestAdminStore(t)

	keys := store.GetAPIKeys()
	if len(keys) != 0 {
		t.Errorf("初始应为空，得到 %d", len(keys))
	}

	store.AddAPIKey("key1", "sk-key1", []string{"connect"}, nil)
	if _, err := store.AddAPIKey("key2", "sk-key2", []string{"manage"}, nil); err == nil {
		t.Fatal("不支持的权限应返回错误")
	}

	keys = store.GetAPIKeys()
	if len(keys) != 1 {
		t.Errorf("期望 1 个可用 Key，得到 %d", len(keys))
	}
}

func TestAdminStore_APIKey_DisableEnableDeleteLifecycle(t *testing.T) {
	store := newInitializedAdminStore(t)

	rawKey := "sk-lifecycle-key"
	key, err := store.AddAPIKey("lifecycle", rawKey, []string{"connect"}, nil)
	if err != nil {
		t.Fatalf("AddAPIKey 失败: %v", err)
	}

	if valid, err := store.ValidateAgentKey(rawKey); !valid || err != nil {
		t.Fatalf("新建 Key 应可用: valid=%v err=%v", valid, err)
	}

	if err := store.SetAPIKeyActive(key.ID, false); err != nil {
		t.Fatalf("禁用 Key 失败: %v", err)
	}
	if valid, err := store.ValidateAgentKey(rawKey); valid || err == nil {
		t.Fatalf("禁用后应拒绝 Key: valid=%v err=%v", valid, err)
	}

	if err := store.SetAPIKeyActive(key.ID, true); err != nil {
		t.Fatalf("启用 Key 失败: %v", err)
	}
	if valid, err := store.ValidateAgentKey(rawKey); !valid || err != nil {
		t.Fatalf("重新启用后应允许 Key: valid=%v err=%v", valid, err)
	}

	if err := store.DeleteAPIKey(key.ID); err != nil {
		t.Fatalf("删除 Key 失败: %v", err)
	}
	if valid, err := store.ValidateAgentKey(rawKey); valid || err == nil {
		t.Fatalf("删除后应拒绝 Key: valid=%v err=%v", valid, err)
	}
}

func TestAdminStore_PersistedSecretsSurviveReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.json")

	store, err := NewAdminStore(path)
	if err != nil {
		t.Fatalf("NewAdminStore 失败: %v", err)
	}
	if err := store.Initialize("admin", "Admin1234", "https://example.com", nil); err != nil {
		t.Fatalf("Initialize 失败: %v", err)
	}
	if _, err := store.AddAPIKey("persisted", "sk-persisted", []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey 失败: %v", err)
	}

	reloaded, err := NewAdminStore(path)
	if err != nil {
		t.Fatalf("重新加载 AdminStore 失败: %v", err)
	}

	if _, err := reloaded.ValidateAdminPassword("admin", "Admin1234"); err != nil {
		t.Fatalf("重载后管理员密码应仍可验证: %v", err)
	}
	if valid, err := reloaded.ValidateAgentKey("sk-persisted"); !valid || err != nil {
		t.Fatalf("重载后 API Key 应仍可验证: valid=%v err=%v", valid, err)
	}
}

// --- Session CRUD ---

func TestAdminStore_Session_CreateAndGet(t *testing.T) {
	store := newInitializedAdminStore(t)

	session := store.CreateSession("user-1", "admin", "admin", "127.0.0.1", "test-agent")
	if session == nil {
		t.Fatal("CreateSession 不应返回 nil")
	}
	if session.ID == "" {
		t.Error("Session ID 不应为空")
	}

	got := store.GetSession(session.ID)
	if got == nil {
		t.Fatal("GetSession 应返回已创建的 session")
	}
	if got.Username != "admin" {
		t.Errorf("Username 期望 admin，得到 %s", got.Username)
	}
}

func TestAdminStore_Session_GetExpired(t *testing.T) {
	store := newInitializedAdminStore(t)

	session := store.CreateSession("user-1", "admin", "admin", "127.0.0.1", "ua")

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
		t.Error("过期 session 应返回 nil")
	}
}

func TestAdminStore_Session_GetNotFound(t *testing.T) {
	store := newInitializedAdminStore(t)

	got := store.GetSession("not-exist-id")
	if got != nil {
		t.Error("不存在的 session 应返回 nil")
	}
}

func TestAdminStore_Session_Delete(t *testing.T) {
	store := newInitializedAdminStore(t)

	session := store.CreateSession("user-1", "admin", "admin", "127.0.0.1", "ua")
	store.DeleteSession(session.ID)

	got := store.GetSession(session.ID)
	if got != nil {
		t.Error("删除后 session 应返回 nil")
	}
}

func TestAdminStore_Session_SingleLogin(t *testing.T) {
	store := newInitializedAdminStore(t)

	// 同一 userID 创建两次 session → 旧的被踢出
	s1 := store.CreateSession("user-1", "admin", "admin", "1.1.1.1", "ua1")
	s2 := store.CreateSession("user-1", "admin", "admin", "2.2.2.2", "ua2")

	got1 := store.GetSession(s1.ID)
	if got1 != nil {
		t.Error("旧 session 应被踢出（单端登录）")
	}

	got2 := store.GetSession(s2.ID)
	if got2 == nil {
		t.Error("新 session 应有效")
	}
}

func TestAdminStore_Session_DeleteByUserID(t *testing.T) {
	store := newInitializedAdminStore(t)

	s1 := store.CreateSession("user-1", "admin", "admin", "1.1.1.1", "ua")
	store.DeleteSessionsByUserID("user-1")

	if store.GetSession(s1.ID) != nil {
		t.Error("DeleteSessionsByUserID 后 session 应不存在")
	}
}

func TestAdminStore_Session_CleanExpired(t *testing.T) {
	store := newInitializedAdminStore(t)

	store.CreateSession("user-1", "admin", "admin", "1.1.1.1", "ua")

	// 手动将所有 session 设为过期
	store.mu.Lock()
	for i := range store.data.Sessions {
		store.data.Sessions[i].ExpiresAt = time.Now().Add(-1 * time.Hour)
	}
	store.mu.Unlock()

	store.CleanExpiredSessions()

	store.mu.RLock()
	count := len(store.data.Sessions)
	store.mu.RUnlock()

	if count != 0 {
		t.Errorf("清理后应无 session，实际 %d", count)
	}
}

// --- System Logs ---

func TestAdminStore_SystemLogs(t *testing.T) {
	store := newTestAdminStore(t)

	store.AddSystemLog("INFO", "test message 1", "test")
	store.AddSystemLog("WARN", "test message 2", "test")

	logs := store.GetSystemLogs(10)
	if len(logs) != 2 {
		t.Errorf("期望 2 条日志，得到 %d", len(logs))
	}
	// 最新的在前
	if logs[0].Message != "test message 2" {
		t.Errorf("最新日志应在首位，得到 %s", logs[0].Message)
	}
}

func TestAdminStore_SystemLogs_Limit(t *testing.T) {
	store := newTestAdminStore(t)

	for i := 0; i < 10; i++ {
		store.AddSystemLog("INFO", "log", "test")
	}

	logs := store.GetSystemLogs(3)
	if len(logs) != 3 {
		t.Errorf("limit=3 应返回 3 条，得到 %d", len(logs))
	}
}

func TestAdminStore_SystemLogs_RingBuffer(t *testing.T) {
	store := newTestAdminStore(t)

	// 写入超过 maxLogs (1000) 条
	for i := 0; i < 1010; i++ {
		store.AddSystemLog("INFO", "log", "test")
	}

	logs := store.GetSystemLogs(0) // 0 = 获取全部
	if len(logs) > maxLogs {
		t.Errorf("环形缓冲区最多 %d 条，得到 %d", maxLogs, len(logs))
	}
}

// --- Events ---

func TestAdminStore_Events(t *testing.T) {
	store := newTestAdminStore(t)

	store.AddEvent("agent_online", `{"agent_id":"a1"}`)
	store.AddEvent("tunnel_changed", `{"tunnel":"t1"}`)
	// AddEvent 内部有异步 goroutine save，等一下
	time.Sleep(50 * time.Millisecond)

	events := store.GetEvents(10)
	if len(events) != 2 {
		t.Errorf("期望 2 个事件，得到 %d", len(events))
	}
	// 最新在前
	if events[0].Type != "tunnel_changed" {
		t.Errorf("最新事件应在首位，得到 %s", events[0].Type)
	}
}

func TestAdminStore_Events_Limit(t *testing.T) {
	store := newTestAdminStore(t)

	for i := 0; i < 10; i++ {
		store.AddEvent("test", "{}")
	}
	time.Sleep(50 * time.Millisecond)

	events := store.GetEvents(3)
	if len(events) != 3 {
		t.Errorf("limit=3 应返回 3 个，得到 %d", len(events))
	}
}

// --- Tunnel Policy ---

func TestAdminStore_TunnelPolicy(t *testing.T) {
	store := newTestAdminStore(t)

	policy := TunnelPolicy{
		MinPort:      1000,
		MaxPort:      60000,
		BlockedPorts: []int{22, 80, 443},
	}
	if err := store.UpdateTunnelPolicy(policy); err != nil {
		t.Fatalf("UpdateTunnelPolicy 失败: %v", err)
	}

	got := store.GetTunnelPolicy()
	if got.MinPort != 1000 {
		t.Errorf("MinPort 期望 1000，得到 %d", got.MinPort)
	}
	if len(got.BlockedPorts) != 3 {
		t.Errorf("BlockedPorts 期望 3 个，得到 %d", len(got.BlockedPorts))
	}
}

// --- Login Time ---

func TestAdminStore_UpdateAdminLoginTime(t *testing.T) {
	store := newInitializedAdminStore(t)

	user, _ := store.ValidateAdminPassword("admin", "Admin1234")
	store.UpdateAdminLoginTime(user.ID)

	// 再次获取用户信息
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, u := range store.data.AdminUsers {
		if u.ID == user.ID {
			if u.LastLogin == nil {
				t.Error("LastLogin 应已被设置")
			}
			return
		}
	}
	t.Error("未找到用户")
}

// --- Server Config ---

func TestAdminStore_GetServerConfig(t *testing.T) {
	store := newInitializedAdminStore(t)

	cfg := store.GetServerConfig()
	if cfg.ServerAddr != "https://example.com" {
		t.Errorf("ServerAddr 期望 https://example.com，得到 %s", cfg.ServerAddr)
	}
	if len(cfg.AllowedPorts) != 1 {
		t.Errorf("AllowedPorts 期望 1 个范围，得到 %d", len(cfg.AllowedPorts))
	}
}

// ============================================================
// Agent Token 测试
// ============================================================

func TestAdminStore_Token_ExchangeAndValidate(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-test-key"
	store.AddAPIKey("test", rawKey, []string{"connect"}, nil)

	// 兑换 Token
	tokenStr, agentToken, err := store.ExchangeToken(rawKey, "install-1", "agent-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken 失败: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("Token 不应为空")
	}
	if agentToken == nil {
		t.Fatal("AgentToken 不应为 nil")
	}
	if agentToken.InstallID != "install-1" {
		t.Errorf("InstallID 期望 install-1, 得到 %s", agentToken.InstallID)
	}

	// 验证 Token
	result, err := store.ValidateAgentToken(tokenStr, "install-1")
	if err != nil {
		t.Fatalf("ValidateAgentToken 失败: %v", err)
	}
	if result.ID != agentToken.ID {
		t.Errorf("Token ID 不匹配: %s != %s", result.ID, agentToken.ID)
	}
}

func TestAdminStore_Token_ExchangeConsumesKeyUseCount(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-counted-key"
	store.AddAPIKey("counted", rawKey, []string{"connect"}, nil)

	// 兑换 Token — 应消耗 Key use_count
	_, _, err := store.ExchangeToken(rawKey, "install-1", "agent-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken 失败: %v", err)
	}

	keys := store.GetAPIKeys()
	if len(keys) != 1 {
		t.Fatalf("期望 1 个 Key, 得到 %d", len(keys))
	}
	if keys[0].UseCount != 1 {
		t.Errorf("兑换后 Key UseCount 期望 1, 得到 %d", keys[0].UseCount)
	}

	// 再次验证 Key（不应增加 use_count）
	valid, _ := store.ValidateAgentKey(rawKey)
	if !valid {
		t.Fatal("Key 仍应有效")
	}

	keys = store.GetAPIKeys()
	if keys[0].UseCount != 1 {
		t.Errorf("ValidateAgentKey 不应增加 UseCount, 期望 1, 得到 %d", keys[0].UseCount)
	}
}

func TestAdminStore_Token_ValidateExpired(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-expiry-key"
	store.AddAPIKey("test", rawKey, []string{"connect"}, nil)

	tokenStr, _, err := store.ExchangeToken(rawKey, "install-1", "agent-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken 失败: %v", err)
	}

	// 手动设置 Token 为过期（超过 7 天不活跃）
	store.mu.Lock()
	for i := range store.data.AgentTokens {
		if store.data.AgentTokens[i].InstallID == "install-1" {
			store.data.AgentTokens[i].LastActiveAt = time.Now().Add(-8 * 24 * time.Hour)
		}
	}
	store.mu.Unlock()

	_, err = store.ValidateAgentToken(tokenStr, "install-1")
	if err == nil {
		t.Error("过期 Token 应验证失败")
	}
}

func TestAdminStore_Token_ValidateRevoked(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-revoke-key"
	store.AddAPIKey("test", rawKey, []string{"connect"}, nil)

	tokenStr, agentToken, err := store.ExchangeToken(rawKey, "install-1", "agent-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("ExchangeToken 失败: %v", err)
	}

	// 吊销 Token
	if err := store.RevokeToken(agentToken.ID); err != nil {
		t.Fatalf("RevokeToken 失败: %v", err)
	}

	_, err = store.ValidateAgentToken(tokenStr, "install-1")
	if err == nil {
		t.Error("已吊销 Token 应验证失败")
	}
}

func TestAdminStore_Token_ReuseExistingToken(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-reuse-key"
	store.AddAPIKey("reuse", rawKey, []string{"connect"}, nil)

	// 首次兑换
	_, _, err := store.ExchangeToken(rawKey, "install-1", "agent-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("首次 ExchangeToken 失败: %v", err)
	}

	keys := store.GetAPIKeys()
	useCountAfterFirst := keys[0].UseCount

	// 再次调用 ExchangeToken（同一 install_id，已有有效 Token）
	newTokenStr, _, err := store.ExchangeToken(rawKey, "install-1", "agent-1", "1.2.3.4:5678")
	if err != nil {
		t.Fatalf("二次 ExchangeToken 失败: %v", err)
	}
	if newTokenStr == "" {
		t.Fatal("应返回新的 Token 字符串")
	}

	// Key use_count 不应增加
	keys = store.GetAPIKeys()
	if keys[0].UseCount != useCountAfterFirst {
		t.Errorf("已有 Token 时不应消耗 Key: 期望 %d, 得到 %d", useCountAfterFirst, keys[0].UseCount)
	}

	// 新 Token 应能验证通过
	_, err = store.ValidateAgentToken(newTokenStr, "install-1")
	if err != nil {
		t.Fatalf("新 Token 应有效: %v", err)
	}
}

func TestAdminStore_Token_CleanExpired(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-clean-key"
	store.AddAPIKey("test", rawKey, []string{"connect"}, nil)

	store.ExchangeToken(rawKey, "install-1", "agent-1", "1.2.3.4:5678")

	// 手动设置为过期
	store.mu.Lock()
	for i := range store.data.AgentTokens {
		store.data.AgentTokens[i].LastActiveAt = time.Now().Add(-8 * 24 * time.Hour)
	}
	store.mu.Unlock()

	store.CleanExpiredTokens()

	store.mu.RLock()
	count := len(store.data.AgentTokens)
	store.mu.RUnlock()

	if count != 0 {
		t.Errorf("清理后应无 Token，实际 %d", count)
	}
}

