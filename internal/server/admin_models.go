package server

import (
	"time"

	"netsgo/pkg/protocol"
)

// APIKey represents an authentication key used by a Client
type APIKey struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	KeyHash      string     `json:"key_hash"`                // for persistence only; must not be returned to the frontend
	LookupDigest string     `json:"lookup_digest,omitempty"` // for candidate lookup only; must not be returned to the frontend
	Permissions  []string   `json:"permissions"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	IsActive     bool       `json:"is_active"`
	MaxUses      int        `json:"max_uses"`  // maximum number of uses; 0 means unlimited
	UseCount     int        `json:"use_count"` // number of times already used
}

// AdminUser represents a web admin account
type AdminUser struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"password_hash"` // for persistence only; must not be returned to the frontend
	Role         string     `json:"role"`          // admin, viewer
	CreatedAt    time.Time  `json:"created_at"`
	LastLogin    *time.Time `json:"last_login,omitempty"`
	TOTPEnabled  bool       `json:"totp_enabled"`
	TOTPSecret   string     `json:"totp_secret,omitempty"` // for persistence only; must not be returned to the frontend
}

// AdminPasskey is a stored WebAuthn/passkey credential for the admin user.
type AdminPasskey struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	Name           string     `json:"name"`
	CredentialID   string     `json:"credential_id"`
	CredentialJSON string     `json:"credential_json"` // for persistence only; must not be returned to the frontend
	RPID           string     `json:"rp_id"`
	Origin         string     `json:"origin"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
}

// AdminAuthChallenge stores short-lived MFA and WebAuthn ceremony data.
type AdminAuthChallenge struct {
	ID           string
	UserID       string
	Kind         string
	SessionJSON  string
	MetadataJSON string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// adminSecurityResponse is returned by `/api/admin/security`.
type adminSecurityResponse struct {
	User                   adminSecurityUserResponse     `json:"user"`
	TOTPEnabled            bool                          `json:"totp_enabled"`
	RecoveryCodesRemaining int                           `json:"recovery_codes_remaining"`
	Passkeys               []adminPasskeyResponse        `json:"passkeys"`
	WebAuthn               adminSecurityWebAuthnResponse `json:"webauthn"`
}

type adminSecurityUserResponse struct {
	ID        string     `json:"id"`
	Username  string     `json:"username"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	LastLogin *time.Time `json:"last_login,omitempty"`
}

type adminSecurityWebAuthnResponse struct {
	RPID   string `json:"rp_id"`
	Origin string `json:"origin"`
}

type adminPasskeyResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	RPID       string     `json:"rp_id"`
	Origin     string     `json:"origin"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// RegisteredClient represents a Client record with a stable identity
type RegisteredClient struct {
	ID          string                `json:"id"`
	InstallID   string                `json:"install_id"`
	DisplayName string                `json:"display_name,omitempty"` // custom display name (falls back to hostname if empty)
	Info        protocol.ClientInfo   `json:"info"`
	Stats       *protocol.SystemStats `json:"stats,omitempty"`
	IngressBPS  int64                 `json:"ingress_bps"`
	EgressBPS   int64                 `json:"egress_bps"`
	CreatedAt   time.Time             `json:"created_at"`
	LastSeen    time.Time             `json:"last_seen"`
	LastIP      string                `json:"last_ip"`
}

// ServerConfig holds server configuration (set during initialization)
type ServerConfig struct {
	ServerAddr   string      `json:"server_addr"`   // public-facing server address (e.g. https://tunnel.example.com)
	AllowedPorts []PortRange `json:"allowed_ports"` // allowlist of ports available for tunneling
}

const (
	defaultClientAuthRateLimitPerMinute = 20
	maxClientAuthRateLimitPerMinute     = 1000
)

// ClientAuthRateLimitSettings controls optional per-IP client authentication throttling.
type ClientAuthRateLimitSettings struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute"`
}

// PortRange represents a port range (Start==End means a single port)
type PortRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// AdminSession holds a server-side session record (implements JWT + Session Binding)
type AdminSession struct {
	ID        string    `json:"id"`       // sessionID (UUID)
	UserID    string    `json:"user_id"`  // associated admin user ID
	Username  string    `json:"username"` // redundant, used for logging
	Role      string    `json:"role"`     // user role
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"` // server-controlled expiry time
	IP        string    `json:"ip"`         // login IP address
	UserAgent string    `json:"user_agent"` // browser user agent
}

// ClientToken represents a long-lived client connection key exchanged from an API Key
type ClientToken struct {
	ID           string    `json:"id"`             // UUID
	TokenHash    string    `json:"token_hash"`     // SHA-256 hex hash
	InstallID    string    `json:"install_id"`     // associated client install_id
	KeyID        string    `json:"key_id"`         // which API Key this was exchanged from
	ClientID     string    `json:"client_id"`      // associated stable client ID
	CreatedAt    time.Time `json:"created_at"`     // creation time
	LastActiveAt time.Time `json:"last_active_at"` // last active time (used for expiry checks)
	LastIP       string    `json:"last_ip"`        // last connection IP
	IsRevoked    bool      `json:"is_revoked"`     // whether this token has been revoked
}

// adminConfigResponse is the read response for `/api/admin/config`.
type adminConfigResponse struct {
	ServerAddr          string      `json:"server_addr"`
	AllowedPorts        []PortRange `json:"allowed_ports"`
	EffectiveServerAddr string      `json:"effective_server_addr"`
	ServerAddrLocked    bool        `json:"server_addr_locked"`
}

// adminConfigUpdateResponse carries unified responses for dry-run, successful save, and conflict scenarios.
type adminConfigUpdateResponse struct {
	Success                bool             `json:"success,omitempty"`
	Error                  string           `json:"error,omitempty"`
	Message                string           `json:"message,omitempty"`
	Code                   string           `json:"code,omitempty"`
	ServerAddrLocked       bool             `json:"server_addr_locked,omitempty"`
	AffectedTunnels        []affectedTunnel `json:"affected_tunnels"`
	ConflictingHTTPTunnels []string         `json:"conflicting_http_tunnels"`
}

type apiErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Field   string `json:"field,omitempty"`
}

// tunnelMutationErrorResponse is returned by tunnel create/update HTTP APIs.
type tunnelMutationErrorResponse struct {
	Success            bool     `json:"success"`
	Error              string   `json:"error"`
	Message            string   `json:"message,omitempty"`
	ErrorCode          string   `json:"error_code,omitempty"`
	Code               string   `json:"code,omitempty"`
	Field              string   `json:"field,omitempty"`
	ConflictingTunnels []string `json:"conflicting_tunnels,omitempty"`
}
