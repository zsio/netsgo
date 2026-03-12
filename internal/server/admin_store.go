package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// AdminData 包含所有持久化的管理数据
type AdminData struct {
	APIKeys      []APIKey       `json:"api_keys"`
	AdminUsers   []AdminUser    `json:"admin_users"`
	TunnelPolicy TunnelPolicy   `json:"tunnel_policy"`
	Events       []EventRecord  `json:"events"`
}

// AdminStore 负责管理员账号、API Key 和策略的持久化
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

// NewAdminStore 创建一个新的管理存储，并在必要时初始化默认数据
func NewAdminStore(path string) (*AdminStore, error) {
	store := &AdminStore{
		path: path,
		data: AdminData{
			APIKeys:      []APIKey{},
			AdminUsers:   []AdminUser{},
			TunnelPolicy: TunnelPolicy{},
			Events:       []EventRecord{},
		},
		logs: make([]SystemLogEntry, maxLogs),
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建 admin 存储目录失败: %w", err)
	}

	// 尝试加载
	if _, err := os.Stat(path); err == nil {
		if err := store.load(); err != nil {
			log.Printf("⚠️ 加载 admin 配置失败，将使用空配置: %v", err)
			store.data = AdminData{
				APIKeys: []APIKey{}, AdminUsers: []AdminUser{}, Events: []EventRecord{},
			}
		}
	}

	// 初始化默认账号 admin/admin（如果没有任何账号）
	if len(store.data.AdminUsers) == 0 {
		hash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		store.data.AdminUsers = append(store.data.AdminUsers, AdminUser{
			ID:           generateUUID(),
			Username:     "admin",
			PasswordHash: string(hash),
			Role:         "admin",
			CreatedAt:    time.Now(),
		})
		log.Printf("⚠️ 自动创建默认管理员账号 (admin / admin)，请尽快修改密码！")
		store.save()
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
	return os.WriteFile(s.path, data, 0644)
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
			// 我们需要返回一个副本并更新 last_login
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

// ========== API Keys ==========

// ValidateAgentKey 检查 Key 是否存在且处于启用状态并且没有过期
// 如果没有任何 Key 存在，则开放所有连接
func (s *AdminStore) ValidateAgentKey(key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.APIKeys) == 0 {
		return true, nil // 向后兼容，没有 Key 时开放连接
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
			return true, nil
		}
	}

	return false, fmt.Errorf("API Key 无效")
}

func (s *AdminStore) AddAPIKey(name, keyString string, permissions []string, expiresAt *time.Time) (*APIKey, error) {
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

	// 注意：返回的结构体中包含了 KeyHash，但我们会通过 "-" 阻止返回 JSON（根据 model 的配置）
	return &k, nil
}

func (s *AdminStore) GetAPIKeys() []APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]APIKey, len(s.data.APIKeys))
	copy(keys, s.data.APIKeys)
	return keys
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

	// 异步保存，避免卡主流程
	go func(store *AdminStore) {
		store.mu.Lock()
		defer store.mu.Unlock()
		store.save()
	}(s)
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
