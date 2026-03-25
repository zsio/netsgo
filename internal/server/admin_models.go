package server

import (
	"time"

	"netsgo/pkg/protocol"
)

// APIKey 表示一个 Client 用于认证的密钥
type APIKey struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	KeyHash     string     `json:"key_hash"` // 仅供持久化存储，不应直接返回给前端
	Permissions []string   `json:"permissions"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IsActive    bool       `json:"is_active"`
	MaxUses     int        `json:"max_uses"`  // 最大使用次数，0 表示无限制
	UseCount    int        `json:"use_count"` // 已使用次数
}

// AdminUser 表示一个 Web 管理员账号
type AdminUser struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"password_hash"` // 仅供持久化存储，不应直接返回给前端
	Role         string     `json:"role"`          // admin, viewer
	CreatedAt    time.Time  `json:"created_at"`
	LastLogin    *time.Time `json:"last_login,omitempty"`
}

// RegisteredClient 表示一个具有稳定身份的 Client 记录
type RegisteredClient struct {
	ID          string                `json:"id"`
	InstallID   string                `json:"install_id"`
	DisplayName string                `json:"display_name,omitempty"` // 自定义展示名（空则使用 hostname）
	Info        protocol.ClientInfo   `json:"info"`
	Stats       *protocol.SystemStats `json:"stats,omitempty"`
	CreatedAt   time.Time             `json:"created_at"`
	LastSeen    time.Time             `json:"last_seen"`
	LastIP      string                `json:"last_ip"`
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
	ID        string    `json:"id"`       // sessionID (UUID)
	UserID    string    `json:"user_id"`  // 关联的管理员 ID
	Username  string    `json:"username"` // 冗余，方便日志
	Role      string    `json:"role"`     // 用户角色
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"` // 服务端控制的过期时间
	IP        string    `json:"ip"`         // 登录 IP
	UserAgent string    `json:"user_agent"` // 浏览器信息
}

// ClientToken 表示一个由 Key 兑换而来的客户端长期连接密钥
type ClientToken struct {
	ID           string    `json:"id"`             // UUID
	TokenHash    string    `json:"token_hash"`     // SHA-256 hex hash
	InstallID    string    `json:"install_id"`     // 关联的客户端 install_id
	KeyID        string    `json:"key_id"`         // 由哪个 Key 兑换而来
	ClientID     string    `json:"client_id"`      // 关联的 Client 稳定 ID
	CreatedAt    time.Time `json:"created_at"`     // 创建时间
	LastActiveAt time.Time `json:"last_active_at"` // 最后活跃时间（用于过期判断）
	LastIP       string    `json:"last_ip"`        // 最后连接 IP
	IsRevoked    bool      `json:"is_revoked"`     // 是否已被吊销
}

// adminConfigResponse 是 `/api/admin/config` 的读取响应。
type adminConfigResponse struct {
	ServerAddr          string      `json:"server_addr"`
	AllowedPorts        []PortRange `json:"allowed_ports"`
	EffectiveServerAddr string      `json:"effective_server_addr"`
	ServerAddrLocked    bool        `json:"server_addr_locked"`
}

// adminConfigUpdateResponse 统一承载 dry-run、保存成功和冲突响应。
type adminConfigUpdateResponse struct {
	Success                bool             `json:"success,omitempty"`
	Error                  string           `json:"error,omitempty"`
	ServerAddrLocked       bool             `json:"server_addr_locked,omitempty"`
	AffectedTunnels        []affectedTunnel `json:"affected_tunnels"`
	ConflictingHTTPTunnels []string         `json:"conflicting_http_tunnels"`
}

// tunnelMutationErrorResponse is returned by tunnel create/update HTTP APIs.
type tunnelMutationErrorResponse struct {
	Success            bool     `json:"success"`
	Error              string   `json:"error"`
	ErrorCode          string   `json:"error_code,omitempty"`
	Field              string   `json:"field,omitempty"`
	ConflictingTunnels []string `json:"conflicting_tunnels,omitempty"`
}
