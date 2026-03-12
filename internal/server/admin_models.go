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

// TunnelPolicy 隧道策略控制（旧版，保留向后兼容）
type TunnelPolicy struct {
	MinPort        int      `json:"min_port"`
	MaxPort        int      `json:"max_port"`
	BlockedPorts   []int    `json:"blocked_ports"`
	AgentWhitelist []string `json:"agent_whitelist"` // 允许的 agent hostname 列表
}

// ServerConfig 服务端配置（初始化时设置）
type ServerConfig struct {
	ServerAddr   string      `json:"server_addr"`   // 对外服务地址 (如 https://tunnel.example.com)
	AllowedPorts []PortRange `json:"allowed_ports"` // 允许穿透的端口白名单
}

// PortRange 端口范围 (Start==End 表示单端口)
type PortRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// AdminSession 服务端 session 记录（实现 JWT + Session Binding）
type AdminSession struct {
	ID        string    `json:"id"`         // sessionID (UUID)
	UserID    string    `json:"user_id"`    // 关联的管理员 ID
	Username  string    `json:"username"`   // 冗余，方便日志
	Role      string    `json:"role"`       // 用户角色
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"` // 服务端控制的过期时间
	IP        string    `json:"ip"`         // 登录 IP
	UserAgent string    `json:"user_agent"` // 浏览器信息
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
