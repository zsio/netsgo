package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode"

	"netsgo/pkg/protocol"

	"golang.org/x/crypto/bcrypt"
)

// AdminData 包含所有持久化的管理数据
type AdminData struct {
	APIKeys      []APIKey          `json:"api_keys"`
	AdminUsers   []AdminUser       `json:"admin_users"`
	Agents       []RegisteredAgent `json:"agents"`
	AgentTokens  []AgentToken      `json:"agent_tokens"`  // 客户端连接密钥
	TunnelPolicy TunnelPolicy      `json:"tunnel_policy"` // 旧版策略，保留向后兼容
	Events       []EventRecord     `json:"events"`
	ServerConfig ServerConfig      `json:"server_config"` // 服务配置（初始化时设置）
	Initialized  bool              `json:"initialized"`   // 是否已完成初始化
	JWTSecret    string            `json:"jwt_secret"`    // 随机生成的 JWT 签名密钥
	Sessions     []AdminSession    `json:"sessions"`      // 服务端 session 列表
}

// AdminStore 负责管理员账号、API Key、策略和 Session 的持久化
type AdminStore struct {
	path string
	mu   sync.RWMutex
	data AdminData

	// 日志环形缓冲区 (内存)
	logMu   sync.RWMutex
	logs    []SystemLogEntry
	logHead int
	logTail int
	logCnt  int
}

const maxLogs = 1000
const maxEvents = 500
const tokenExpiryDuration = 7 * 24 * time.Hour // Token 不活跃过期时间
const sessionDefaultTTL = 24 * time.Hour

// NewAdminStore 创建一个新的管理存储
func NewAdminStore(path string) (*AdminStore, error) {
	store := &AdminStore{
		path: path,
		data: AdminData{
			APIKeys:     []APIKey{},
			AdminUsers:  []AdminUser{},
			Agents:      []RegisteredAgent{},
			AgentTokens: []AgentToken{},
			Events:      []EventRecord{},
			Sessions:    []AdminSession{},
		},
		logs: make([]SystemLogEntry, maxLogs),
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建 admin 存储目录失败: %w", err)
	}

	// 尝试加载已有数据
	if _, err := os.Stat(path); err == nil {
		if err := store.load(); err != nil {
			log.Printf("⚠️ 加载 admin 配置失败，将使用空配置: %v", err)
			store.data = AdminData{
				APIKeys: []APIKey{}, AdminUsers: []AdminUser{}, Agents: []RegisteredAgent{}, AgentTokens: []AgentToken{}, Events: []EventRecord{}, Sessions: []AdminSession{},
			}
		}
	}

	// 启动后清理过期 session
	store.CleanExpiredSessions()

	if !store.data.Initialized {
		log.Printf("⚠️ 服务尚未初始化，请通过 Web 面板完成初始化设置")
	}

	return store, nil
}

func (s *AdminStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.data)
}

func (s *AdminStore) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

// ========== 初始化 ==========

// IsInitialized 检查服务是否已初始化
func (s *AdminStore) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Initialized
}

// Initialize 执行一次性初始化
func (s *AdminStore) Initialize(username, password, serverAddr string, allowedPorts []PortRange) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.Initialized {
		return fmt.Errorf("服务已初始化，不可重复操作")
	}

	// 密码强度校验
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("密码不符合要求: %w", err)
	}

	// 创建管理员账号
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("密码加密失败: %w", err)
	}
	s.data.AdminUsers = []AdminUser{{
		ID:           generateUUID(),
		Username:     username,
		PasswordHash: string(hash),
		Role:         "admin",
		CreatedAt:    time.Now(),
	}}

	// 生成随机 JWT Secret (32 字节 = 256 bit)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return fmt.Errorf("生成 JWT Secret 失败: %w", err)
	}
	s.data.JWTSecret = hex.EncodeToString(secretBytes)

	// 设置服务配置
	s.data.ServerConfig = ServerConfig{
		ServerAddr:   serverAddr,
		AllowedPorts: allowedPorts,
	}

	s.data.Initialized = true

	log.Printf("✅ 服务初始化完成，管理员: %s", username)
	return s.save()
}

// validatePassword 验证密码强度（至少 8 位，包含字母和数字）
func validatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("密码至少需要 8 个字符")
	}
	hasLetter := false
	hasDigit := false
	for _, c := range password {
		if unicode.IsLetter(c) {
			hasLetter = true
		}
		if unicode.IsDigit(c) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("密码必须同时包含字母和数字")
	}
	return nil
}

// ========== JWT Secret ==========

// GetJWTSecret 获取 JWT 签名密钥
func (s *AdminStore) GetJWTSecret() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data.JWTSecret == "" {
		// 未初始化时返回 fallback（仅用于不需要安全性的开发场景）
		return []byte("netsgo-dev-fallback-secret")
	}
	return []byte(s.data.JWTSecret)
}

// ========== Server Config ==========

// GetServerConfig 获取服务端配置
func (s *AdminStore) GetServerConfig() ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.ServerConfig
}

// UpdateServerConfig 更新服务端配置（初始化后可修改）
func (s *AdminStore) UpdateServerConfig(config ServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.ServerConfig = config
	return s.save()
}

// ========== Port Whitelist ==========

// IsPortAllowed 检查端口是否在白名单范围内
// 如果白名单为空（未初始化），返回 true（向后兼容）
func (s *AdminStore) IsPortAllowed(port int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.ServerConfig.AllowedPorts) == 0 {
		return true // 白名单为空 → 不限制
	}

	for _, pr := range s.data.ServerConfig.AllowedPorts {
		if port >= pr.Start && port <= pr.End {
			return true
		}
	}
	return false
}

// ========== AdminUsers ==========

func (s *AdminStore) ValidateAdminPassword(username, password string) (*AdminUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i, u := range s.data.AdminUsers {
		if u.Username == username {
			if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
				return nil, fmt.Errorf("密码错误")
			}
			userCopy := s.data.AdminUsers[i]
			return &userCopy, nil
		}
	}
	return nil, fmt.Errorf("用户不存在")
}

func (s *AdminStore) UpdateAdminLoginTime(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.data.AdminUsers {
		if u.ID == id {
			now := time.Now()
			s.data.AdminUsers[i].LastLogin = &now
			s.save()
			break
		}
	}
}

// ========== Agents ==========

func (s *AdminStore) GetOrCreateAgent(installID string, info protocol.AgentInfo, remoteAddr string) (*RegisteredAgent, error) {
	if installID == "" {
		return nil, fmt.Errorf("install_id 不能为空")
	}

	lastIP := remoteIP(remoteAddr)

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, agent := range s.data.Agents {
		if agent.InstallID == installID {
			s.data.Agents[i].Info = info
			s.data.Agents[i].LastSeen = time.Now()
			s.data.Agents[i].LastIP = lastIP
			if err := s.save(); err != nil {
				return nil, err
			}
			copy := s.data.Agents[i]
			return &copy, nil
		}
	}

	agent := RegisteredAgent{
		ID:        generateUUID(),
		InstallID: installID,
		Info:      info,
		CreatedAt: time.Now(),
		LastSeen:  time.Now(),
		LastIP:    lastIP,
	}
	s.data.Agents = append(s.data.Agents, agent)
	if err := s.save(); err != nil {
		return nil, err
	}
	return &agent, nil
}

func (s *AdminStore) TouchAgent(agentID string, info protocol.AgentInfo, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, agent := range s.data.Agents {
		if agent.ID == agentID {
			s.data.Agents[i].Info = info
			s.data.Agents[i].LastSeen = time.Now()
			if ip := remoteIP(remoteAddr); ip != "" {
				s.data.Agents[i].LastIP = ip
			}
			return s.save()
		}
	}

	return fmt.Errorf("agent %q 不存在", agentID)
}

func cloneSystemStats(stats *protocol.SystemStats) *protocol.SystemStats {
	if stats == nil {
		return nil
	}

	copy := *stats
	if len(stats.DiskPartitions) > 0 {
		copy.DiskPartitions = append([]protocol.DiskPartition(nil), stats.DiskPartitions...)
	}

	return &copy
}

func (s *AdminStore) UpdateAgentStats(agentID string, info protocol.AgentInfo, stats protocol.SystemStats, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, agent := range s.data.Agents {
		if agent.ID == agentID {
			s.data.Agents[i].Info = info
			s.data.Agents[i].LastSeen = time.Now()
			s.data.Agents[i].Stats = cloneSystemStats(&stats)
			if ip := remoteIP(remoteAddr); ip != "" {
				s.data.Agents[i].LastIP = ip
			}
			return s.save()
		}
	}

	return fmt.Errorf("agent %q 不存在", agentID)
}

func (s *AdminStore) GetRegisteredAgents() []RegisteredAgent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	agents := make([]RegisteredAgent, len(s.data.Agents))
	copy(agents, s.data.Agents)
	for i := range agents {
		agents[i].Stats = cloneSystemStats(agents[i].Stats)
	}
	return agents
}

func (s *AdminStore) GetRegisteredAgent(agentID string) (RegisteredAgent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, agent := range s.data.Agents {
		if agent.ID == agentID {
			agent.Stats = cloneSystemStats(agent.Stats)
			return agent, true
		}
	}
	return RegisteredAgent{}, false
}

func (s *AdminStore) CountAgentsByHostname(hostname string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, agent := range s.data.Agents {
		if agent.Info.Hostname == hostname {
			count++
		}
	}
	return count
}

// ========== Sessions ==========

// CreateSession 创建新 session（会先删除同用户旧 session → 单端登录）
func (s *AdminStore) CreateSession(userID, username, role, ip, ua string) *AdminSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 删除该用户的所有旧 session（实现单端登录）
	filtered := make([]AdminSession, 0, len(s.data.Sessions))
	for _, sess := range s.data.Sessions {
		if sess.UserID != userID {
			filtered = append(filtered, sess)
		}
	}

	session := AdminSession{
		ID:        generateUUID(),
		UserID:    userID,
		Username:  username,
		Role:      role,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(sessionDefaultTTL),
		IP:        ip,
		UserAgent: ua,
	}

	s.data.Sessions = append(filtered, session)
	s.save()
	return &session
}

// GetSession 获取指定 session（不存在或已过期返回 nil）
func (s *AdminStore) GetSession(sessionID string) *AdminSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i, sess := range s.data.Sessions {
		if sess.ID == sessionID {
			if time.Now().After(sess.ExpiresAt) {
				return nil // 已过期
			}
			copy := s.data.Sessions[i]
			return &copy
		}
	}
	return nil
}

// DeleteSession 删除指定 session
func (s *AdminStore) DeleteSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := make([]AdminSession, 0, len(s.data.Sessions))
	for _, sess := range s.data.Sessions {
		if sess.ID != sessionID {
			filtered = append(filtered, sess)
		}
	}
	s.data.Sessions = filtered
	s.save()
}

// DeleteSessionsByUserID 删除该用户的所有 session
func (s *AdminStore) DeleteSessionsByUserID(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := make([]AdminSession, 0, len(s.data.Sessions))
	for _, sess := range s.data.Sessions {
		if sess.UserID != userID {
			filtered = append(filtered, sess)
		}
	}
	s.data.Sessions = filtered
	s.save()
}

// CleanExpiredSessions 清理过期 session
func (s *AdminStore) CleanExpiredSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	filtered := make([]AdminSession, 0, len(s.data.Sessions))
	cleaned := 0
	for _, sess := range s.data.Sessions {
		if now.Before(sess.ExpiresAt) {
			filtered = append(filtered, sess)
		} else {
			cleaned++
		}
	}
	if cleaned > 0 {
		s.data.Sessions = filtered
		s.save()
		log.Printf("🧹 清理了 %d 个过期 session", cleaned)
	}
}

// ========== API Keys ==========

// ValidateAgentKey 检查 Key 是否存在且处于启用状态并且没有过期
// 仅做验证，不消耗使用次数（计数在 ExchangeToken 中消耗）
// 如果没有任何 Key 存在，则开放所有连接
func (s *AdminStore) ValidateAgentKey(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateAgentKeyLocked(key)
}

// validateAgentKeyLocked 内部方法，调用时需要已持有 mu 锁
func (s *AdminStore) validateAgentKeyLocked(key string) (bool, error) {
	if len(s.data.APIKeys) == 0 {
		if s.data.Initialized {
			return false, fmt.Errorf("未配置可用 API Key")
		}
		return true, nil // 向后兼容，未初始化且没有 Key 时开放连接
	}

	if key == "" {
		return false, fmt.Errorf("无有效 API Key 提供且已开启权限验证")
	}

	for _, k := range s.data.APIKeys {
		if err := bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(key)); err == nil {
			if !k.IsActive {
				return false, fmt.Errorf("API Key 已被禁用")
			}
			if k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()) {
				return false, fmt.Errorf("API Key 已过期")
			}
			if k.MaxUses > 0 && k.UseCount >= k.MaxUses {
				return false, fmt.Errorf("API Key 已达到最大使用次数")
			}
			return true, nil
		}
	}

	return false, fmt.Errorf("API Key 无效")
}

// findKeyIndexByRaw 查找匹配的 Key 索引，调用时需已持有 mu 锁
func (s *AdminStore) findKeyIndexByRaw(key string) int {
	for i, k := range s.data.APIKeys {
		if err := bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(key)); err == nil {
			return i
		}
	}
	return -1
}

// ========== Agent Tokens ==========

// hashToken 对 Token 做 SHA-256 hash
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// generateToken 生成一个随机 Token (256-bit)
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 Token 失败: %w", err)
	}
	return "tk-" + hex.EncodeToString(buf), nil
}

// ExchangeToken 用 Key 兑换一个客户端 Token
// 如果该 install_id 已有有效 Token，则直接返回既有 Token 的原始值（不重复消耗 Key）
// 否则验证 Key → 消耗 use_count → 生成新 Token
func (s *AdminStore) ExchangeToken(key, installID, agentID, remoteAddr string) (string, *AgentToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ip := remoteIP(remoteAddr)

	// 查找该 install_id 是否已有未过期、未吊销的 Token
	for i, t := range s.data.AgentTokens {
		if t.InstallID == installID && !t.IsRevoked && time.Since(t.LastActiveAt) < tokenExpiryDuration {
			// 已有有效 Token，无需消耗 Key，但无法返回原始 Token
			// 需要生成新 Token 并替换旧 hash
			newToken, err := generateToken()
			if err != nil {
				return "", nil, err
			}
			s.data.AgentTokens[i].TokenHash = hashToken(newToken)
			s.data.AgentTokens[i].LastActiveAt = time.Now()
			s.data.AgentTokens[i].LastIP = ip
			s.data.AgentTokens[i].AgentID = agentID
			s.save()
			copy := s.data.AgentTokens[i]
			log.Printf("🔑 Token 已刷新 [install_id=%s]: 已有有效 Token，未消耗 Key", installID)
			return newToken, &copy, nil
		}
	}

	// 没有有效 Token，需要验证 Key
	valid, err := s.validateAgentKeyLocked(key)
	if !valid {
		return "", nil, fmt.Errorf("Key 验证失败: %w", err)
	}

	// 消耗 Key 的使用次数
	idx := s.findKeyIndexByRaw(key)
	if idx >= 0 {
		s.data.APIKeys[idx].UseCount++
	}

	// 生成新 Token
	newToken, err := generateToken()
	if err != nil {
		return "", nil, err
	}

	keyID := ""
	if idx >= 0 {
		keyID = s.data.APIKeys[idx].ID
	}

	agentToken := AgentToken{
		ID:           generateUUID(),
		TokenHash:    hashToken(newToken),
		InstallID:    installID,
		KeyID:        keyID,
		AgentID:      agentID,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
		LastIP:       ip,
	}

	// 吊销该 install_id 的旧 Token（同一客户端只保留一个有效 Token）
	for i, t := range s.data.AgentTokens {
		if t.InstallID == installID && !t.IsRevoked {
			s.data.AgentTokens[i].IsRevoked = true
		}
	}

	s.data.AgentTokens = append(s.data.AgentTokens, agentToken)
	s.save()

	log.Printf("🔑 Token 已兑换 [install_id=%s, key_id=%s]", installID, keyID)
	return newToken, &agentToken, nil
}

// ValidateAgentToken 验证客户端 Token 是否有效
// 返回匹配的 AgentToken 记录（如果有效），否则返回 error
func (s *AdminStore) ValidateAgentToken(token, installID string) (*AgentToken, error) {
	if token == "" {
		return nil, fmt.Errorf("Token 为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tokenHash := hashToken(token)

	for i, t := range s.data.AgentTokens {
		if t.TokenHash == tokenHash {
			// Token hash 匹配
			if t.IsRevoked {
				return nil, fmt.Errorf("Token 已被吊销")
			}
			if t.InstallID != installID {
				log.Printf("⚠️ Token install_id 不匹配: token_install=%s, req_install=%s", t.InstallID, installID)
				return nil, fmt.Errorf("Token 无效")
			}
			if time.Since(t.LastActiveAt) >= tokenExpiryDuration {
				return nil, fmt.Errorf("Token 已过期（超过 %d 天未活跃）", int(tokenExpiryDuration.Hours()/24))
			}
			// 验证通过，更新最后活跃时间
			s.data.AgentTokens[i].LastActiveAt = time.Now()
			s.save()
			copy := s.data.AgentTokens[i]
			return &copy, nil
		}
	}

	return nil, fmt.Errorf("Token 无效")
}

// TouchToken 更新 Token 的最后活跃时间和 IP
func (s *AdminStore) TouchToken(tokenID, remoteAddr string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.data.AgentTokens {
		if t.ID == tokenID {
			s.data.AgentTokens[i].LastActiveAt = time.Now()
			if ip := remoteIP(remoteAddr); ip != "" {
				s.data.AgentTokens[i].LastIP = ip
			}
			s.save()
			return
		}
	}
}

// RevokeToken 吊销指定 Token
func (s *AdminStore) RevokeToken(tokenID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.data.AgentTokens {
		if t.ID == tokenID {
			s.data.AgentTokens[i].IsRevoked = true
			return s.save()
		}
	}
	return fmt.Errorf("Token %q 不存在", tokenID)
}

// RevokeTokensByKeyID 吊销某 Key 下所有 Token，返回吊销数量
func (s *AdminStore) RevokeTokensByKeyID(keyID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for i, t := range s.data.AgentTokens {
		if t.KeyID == keyID && !t.IsRevoked {
			s.data.AgentTokens[i].IsRevoked = true
			count++
		}
	}
	if count > 0 {
		s.save()
	}
	return count
}

// CleanExpiredTokens 清理不活跃超过 7 天的 Token 和已吊销的 Token
func (s *AdminStore) CleanExpiredTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	filtered := make([]AgentToken, 0, len(s.data.AgentTokens))
	cleaned := 0
	for _, t := range s.data.AgentTokens {
		if t.IsRevoked || now.Sub(t.LastActiveAt) >= tokenExpiryDuration {
			cleaned++
			continue
		}
		filtered = append(filtered, t)
	}
	if cleaned > 0 {
		s.data.AgentTokens = filtered
		s.save()
		log.Printf("🧹 清理了 %d 个过期/已吊销 Token", cleaned)
	}
}

// GetTokensByKeyID 查询某 Key 兑换的所有 Token
func (s *AdminStore) GetTokensByKeyID(keyID string) []AgentToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]AgentToken, 0)
	for _, t := range s.data.AgentTokens {
		if t.KeyID == keyID {
			result = append(result, t)
		}
	}
	return result
}

// GetAgentTokenByInstallID 查找某 install_id 对应的有效 Token
func (s *AdminStore) GetAgentTokenByInstallID(installID string) *AgentToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, t := range s.data.AgentTokens {
		if t.InstallID == installID && !t.IsRevoked && time.Since(t.LastActiveAt) < tokenExpiryDuration {
			copy := t
			return &copy
		}
	}
	return nil
}

func (s *AdminStore) AddAPIKey(name, keyString string, permissions []string, expiresAt *time.Time) (*APIKey, error) {
	permissions, err := normalizeKeyPermissions(permissions)
	if err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(keyString), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	k := APIKey{
		ID:          generateUUID(),
		Name:        name,
		KeyHash:     string(hash),
		Permissions: permissions,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		IsActive:    true,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.APIKeys = append(s.data.APIKeys, k)
	s.save()

	return &k, nil
}

func (s *AdminStore) GetAPIKeys() []APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]APIKey, len(s.data.APIKeys))
	copy(keys, s.data.APIKeys)
	return keys
}

func (s *AdminStore) SetAPIKeyActive(id string, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, key := range s.data.APIKeys {
		if key.ID == id {
			s.data.APIKeys[i].IsActive = active
			return s.save()
		}
	}
	return fmt.Errorf("API Key %q 不存在", id)
}

func (s *AdminStore) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := make([]APIKey, 0, len(s.data.APIKeys))
	found := false
	for _, key := range s.data.APIKeys {
		if key.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, key)
	}
	if !found {
		return fmt.Errorf("API Key %q 不存在", id)
	}

	s.data.APIKeys = filtered
	return s.save()
}

// SetAPIKeyMaxUses 设置 API Key 的最大使用次数
func (s *AdminStore) SetAPIKeyMaxUses(id string, maxUses int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, key := range s.data.APIKeys {
		if key.ID == id {
			s.data.APIKeys[i].MaxUses = maxUses
			return s.save()
		}
	}
	return fmt.Errorf("API Key %q 不存在", id)
}

// ========== Tunnel Policy ==========

func (s *AdminStore) GetTunnelPolicy() TunnelPolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.TunnelPolicy
}

func (s *AdminStore) UpdateTunnelPolicy(policy TunnelPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.TunnelPolicy = policy
	return s.save()
}

// ========== System Logs ==========

func (s *AdminStore) AddSystemLog(level, message, source string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()

	entry := SystemLogEntry{
		ID:        generateUUID(),
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Source:    source,
	}

	s.logs[s.logTail] = entry
	s.logTail = (s.logTail + 1) % maxLogs
	if s.logCnt < maxLogs {
		s.logCnt++
	} else {
		s.logHead = (s.logHead + 1) % maxLogs
	}
}

func (s *AdminStore) GetSystemLogs(limit int) []SystemLogEntry {
	s.logMu.RLock()
	defer s.logMu.RUnlock()

	count := s.logCnt
	if limit > 0 && limit < count {
		count = limit
	}

	result := make([]SystemLogEntry, count)
	// 从最新的往前取
	for i := 0; i < count; i++ {
		idx := (s.logTail - 1 - i + maxLogs) % maxLogs
		result[i] = s.logs[idx]
	}
	return result
}

// ========== Events ==========

func (s *AdminStore) AddEvent(eventType, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	event := EventRecord{
		ID:        generateUUID(),
		Timestamp: time.Now(),
		Type:      eventType,
		Data:      data,
	}

	s.data.Events = append([]EventRecord{event}, s.data.Events...) // 插入头部
	if len(s.data.Events) > maxEvents {
		s.data.Events = s.data.Events[:maxEvents]
	}

	s.save()
}

func (s *AdminStore) GetEvents(limit int) []EventRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := len(s.data.Events)
	if limit > 0 && limit < count {
		count = limit
	}
	result := make([]EventRecord, count)
	copy(result, s.data.Events[:count])
	return result
}

func normalizeKeyPermissions(permissions []string) ([]string, error) {
	if len(permissions) == 0 {
		return []string{"connect"}, nil
	}

	normalized := make([]string, 0, len(permissions))
	seen := map[string]struct{}{}
	for _, permission := range permissions {
		if permission != "connect" {
			return nil, fmt.Errorf("不支持的 API Key 权限: %s", permission)
		}
		if _, ok := seen[permission]; ok {
			continue
		}
		seen[permission] = struct{}{}
		normalized = append(normalized, permission)
	}
	return normalized, nil
}

func remoteIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}
