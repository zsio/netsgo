package server

import "time"

// APIKey 表示一个 Agent 用于认证的密钥
type APIKey struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	KeyHash     string    `json:"-"` // hash 后的 key，不直接返回
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IsActive    bool      `json:"is_active"`
}

// AdminUser 表示一个 Web 管理员账号
type AdminUser struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"` // bcrypt hash
	Role         string    `json:"role"` // admin, viewer
	CreatedAt    time.Time `json:"created_at"`
	LastLogin    *time.Time `json:"last_login,omitempty"`
}

// TunnelPolicy 隧道策略控制
type TunnelPolicy struct {
	MinPort        int      `json:"min_port"`
	MaxPort        int      `json:"max_port"`
	BlockedPorts   []int    `json:"blocked_ports"`
	AgentWhitelist []string `json:"agent_whitelist"` // 允许的 agent hostname 列表
}

// EventRecord 审计/系统事件记录（持久化）
type EventRecord struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Data      string    `json:"data"` // JSON 字符串
}

// SystemLogEntry 系统日志记录（内存 Ring Buffer）
type SystemLogEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // INFO, WARN, ERROR
	Message   string    `json:"message"`
	Source    string    `json:"source"`
}
