package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"netsgo/pkg/protocol"
)

const (
	TunnelBindingClientID       = "client_id"
	TunnelBindingLegacyHostname = "legacy_hostname"
)

// StoredTunnel 持久化存储的隧道配置
type StoredTunnel struct {
	protocol.ProxyNewRequest
	Status   string `json:"status"`              // active, paused, stopped
	ClientID string `json:"client_id,omitempty"` // 所属稳定 Client ID
	Hostname string `json:"hostname,omitempty"`  // 当前主机名（展示用）
	Binding  string `json:"binding,omitempty"`   // client_id | legacy_hostname
}

func (t *StoredTunnel) normalize() {
	switch t.Binding {
	case TunnelBindingClientID:
		if t.ClientID == "" {
			t.Binding = TunnelBindingLegacyHostname
		}
	case TunnelBindingLegacyHostname:
	default:
		// 旧版数据默认都按 hostname 绑定处理，避免误信任历史上的临时 client_id
		t.Binding = TunnelBindingLegacyHostname
		t.ClientID = ""
	}
}

func (t StoredTunnel) matchesClient(clientID, name string) bool {
	return t.Binding == TunnelBindingClientID && t.ClientID == clientID && t.Name == name
}

func (t StoredTunnel) matchesIdentifier(identifier, name string) bool {
	if t.Name != name {
		return false
	}
	if t.Binding == TunnelBindingClientID {
		return t.ClientID == identifier
	}
	return t.Hostname == identifier
}

func (t StoredTunnel) matchesLegacyHostname(hostname string) bool {
	return t.Binding == TunnelBindingLegacyHostname && t.Hostname == hostname
}

// TunnelStore 基于 JSON 文件的隧道配置持久化存储
type TunnelStore struct {
	path    string
	mu      sync.RWMutex
	tunnels []StoredTunnel
}

// NewTunnelStore 创建或加载一个隧道存储
// 如果文件不存在则创建空存储
func NewTunnelStore(path string) (*TunnelStore, error) {
	store := &TunnelStore{
		path:    path,
		tunnels: []StoredTunnel{},
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		if err := store.load(); err != nil {
			log.Printf("⚠️ 加载隧道配置失败（使用空配置）: %v", err)
			store.tunnels = []StoredTunnel{}
		}
	}

	return store, nil
}

func (s *TunnelStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.tunnels); err != nil {
		return err
	}
	for i := range s.tunnels {
		s.tunnels[i].normalize()
	}
	return nil
}

func (s *TunnelStore) save() error {
	data, err := json.MarshalIndent(s.tunnels, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// AddTunnel 添加一条隧道配置并持久化
func (s *TunnelStore) AddTunnel(tunnel StoredTunnel) error {
	tunnel.normalize()
	if tunnel.ClientID == "" || tunnel.Binding != TunnelBindingClientID {
		return fmt.Errorf("新隧道必须使用稳定 client_id 绑定")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.tunnels {
		if existing.matchesClient(tunnel.ClientID, tunnel.Name) {
			return fmt.Errorf("隧道 %q 已存在 (client_id: %s)", tunnel.Name, tunnel.ClientID)
		}
	}

	s.tunnels = append(s.tunnels, tunnel)
	return s.save()
}

// RemoveTunnel 删除一条隧道配置并持久化
func (s *TunnelStore) RemoveTunnel(clientID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, tunnel := range s.tunnels {
		if tunnel.matchesIdentifier(clientID, name) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("隧道 %q 不存在 (client_id: %s)", name, clientID)
	}

	s.tunnels = append(s.tunnels[:idx], s.tunnels[idx+1:]...)
	return s.save()
}

// UpdateStatus 更新隧道状态并持久化
func (s *TunnelStore) UpdateStatus(clientID, name, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, tunnel := range s.tunnels {
		if tunnel.matchesIdentifier(clientID, name) {
			s.tunnels[i].Status = status
			return s.save()
		}
	}
	return fmt.Errorf("隧道 %q 不存在 (client_id: %s)", name, clientID)
}

// UpdateHostname 更新某个 Client 的展示主机名
func (s *TunnelStore) UpdateHostname(clientID, hostname string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for i, tunnel := range s.tunnels {
		if tunnel.Binding == TunnelBindingClientID && tunnel.ClientID == clientID && tunnel.Hostname != hostname {
			s.tunnels[i].Hostname = hostname
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}

// MigrateLegacyTunnels 以 hostname 为条件，将旧版记录迁移到稳定 client_id
func (s *TunnelStore) MigrateLegacyTunnels(hostname, clientID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := 0
	for i, tunnel := range s.tunnels {
		if tunnel.matchesLegacyHostname(hostname) {
			s.tunnels[i].ClientID = clientID
			s.tunnels[i].Binding = TunnelBindingClientID
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	return changed, s.save()
}

// UpdateClientID 向后兼容旧接口：将 hostname 绑定的旧隧道迁移到稳定 client_id
func (s *TunnelStore) UpdateClientID(hostname, oldID, newID string) {
	_, _ = s.MigrateLegacyTunnels(hostname, newID)
}

// GetTunnelsByClientID 按稳定 client_id 查找所有隧道配置
func (s *TunnelStore) GetTunnelsByClientID(clientID string) []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredTunnel, 0)
	for _, tunnel := range s.tunnels {
		if tunnel.Binding == TunnelBindingClientID && tunnel.ClientID == clientID {
			result = append(result, tunnel)
		}
	}
	return result
}

// GetLegacyTunnelsByHostname 获取尚未迁移的 hostname 绑定隧道
func (s *TunnelStore) GetLegacyTunnelsByHostname(hostname string) []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredTunnel, 0)
	for _, tunnel := range s.tunnels {
		if tunnel.matchesLegacyHostname(hostname) {
			result = append(result, tunnel)
		}
	}
	return result
}

// GetTunnelsByHostname 向后兼容旧接口：返回匹配 hostname 的全部隧道
func (s *TunnelStore) GetTunnelsByHostname(hostname string) []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredTunnel, 0)
	for _, tunnel := range s.tunnels {
		if tunnel.Hostname == hostname {
			result = append(result, tunnel)
		}
	}
	return result
}

// GetTunnel 按稳定 client_id 和 name 查找单条隧道
func (s *TunnelStore) GetTunnel(clientID, name string) (StoredTunnel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, tunnel := range s.tunnels {
		if tunnel.matchesIdentifier(clientID, name) {
			return tunnel, true
		}
	}
	return StoredTunnel{}, false
}

// GetAllTunnels 获取所有隧道配置
func (s *TunnelStore) GetAllTunnels() []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredTunnel, len(s.tunnels))
	copy(result, s.tunnels)
	return result
}
