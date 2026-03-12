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

// StoredTunnel 持久化存储的隧道配置
type StoredTunnel struct {
	protocol.ProxyNewRequest
	Status   string `json:"status"`    // active, paused, stopped
	AgentID  string `json:"agent_id"`  // 所属 Agent ID
	Hostname string `json:"hostname"`  // Agent 主机名（用于重连匹配）
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

	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}

	// 尝试加载现有文件
	if _, err := os.Stat(path); err == nil {
		if err := store.load(); err != nil {
			log.Printf("⚠️ 加载隧道配置失败（使用空配置）: %v", err)
			store.tunnels = []StoredTunnel{}
		}
	}

	return store, nil
}

// load 从文件加载隧道配置
func (s *TunnelStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.tunnels)
}

// save 将隧道配置写入文件
func (s *TunnelStore) save() error {
	data, err := json.MarshalIndent(s.tunnels, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// AddTunnel 添加一条隧道配置并持久化
func (s *TunnelStore) AddTunnel(tunnel StoredTunnel) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查是否已存在同名（同 hostname）的隧道
	for _, t := range s.tunnels {
		if t.Hostname == tunnel.Hostname && t.Name == tunnel.Name {
			return fmt.Errorf("隧道 %q 已存在 (hostname: %s)", tunnel.Name, tunnel.Hostname)
		}
	}

	s.tunnels = append(s.tunnels, tunnel)
	return s.save()
}

// RemoveTunnel 删除一条隧道配置并持久化
func (s *TunnelStore) RemoveTunnel(hostname, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, t := range s.tunnels {
		if t.Hostname == hostname && t.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("隧道 %q 不存在 (hostname: %s)", name, hostname)
	}

	s.tunnels = append(s.tunnels[:idx], s.tunnels[idx+1:]...)
	return s.save()
}

// UpdateStatus 更新隧道状态并持久化
func (s *TunnelStore) UpdateStatus(hostname, name, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.tunnels {
		if t.Hostname == hostname && t.Name == name {
			s.tunnels[i].Status = status
			return s.save()
		}
	}
	return fmt.Errorf("隧道 %q 不存在 (hostname: %s)", name, hostname)
}

// UpdateAgentID 更新隧道的 AgentID（Agent 重连时迁移到新 ID）
func (s *TunnelStore) UpdateAgentID(hostname, oldID, newID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for i, t := range s.tunnels {
		if t.Hostname == hostname {
			s.tunnels[i].AgentID = newID
			changed = true
		}
	}
	if changed {
		s.save()
	}
}

// GetTunnelsByHostname 按 hostname 查找所有隧道配置
func (s *TunnelStore) GetTunnelsByHostname(hostname string) []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []StoredTunnel
	for _, t := range s.tunnels {
		if t.Hostname == hostname {
			result = append(result, t)
		}
	}
	return result
}

// GetTunnel 按 hostname 和 name 查找单条隧道
func (s *TunnelStore) GetTunnel(hostname, name string) (StoredTunnel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, t := range s.tunnels {
		if t.Hostname == hostname && t.Name == name {
			return t, true
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
