package protocol

const (
	SOCKS5AuthTypeNone             = "none"
	SOCKS5AuthTypeUsernamePassword = "username_password"

	SOCKS5AddrTypeIPv4   = "ipv4"
	SOCKS5AddrTypeDomain = "domain"
	SOCKS5AddrTypeIPv6   = "ipv6"

	SOCKS5DialStatusSuccess            = "success"
	SOCKS5DialStatusTargetDenied       = "target_denied"
	SOCKS5DialStatusNetworkUnreachable = "network_unreachable"
	SOCKS5DialStatusHostUnreachable    = "host_unreachable"
	SOCKS5DialStatusConnectionRefused  = "connection_refused"
	SOCKS5DialStatusDialTimeout        = "dial_timeout"
	SOCKS5DialStatusGeneralFailure     = "general_failure"
)

// SOCKS5AuthConfig is stored inside socks5_listen endpoint config. Password is
// accepted only on mutation input; persisted specs and provisioned specs carry
// PasswordHash only.
type SOCKS5AuthConfig struct {
	Type         string `json:"type"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
}

// SOCKS5ListenConfig describes ingress behavior for socks5_listen endpoints.
type SOCKS5ListenConfig struct {
	BindIP             string           `json:"bind_ip"`
	Port               int              `json:"port"`
	AllowedSourceCIDRs []string         `json:"allowed_source_cidrs"`
	Auth               SOCKS5AuthConfig `json:"auth"`
}

// SOCKS5ConnectHandlerConfig describes target-side authorization and dial
// behavior for socks5_connect_handler endpoints.
type SOCKS5ConnectHandlerConfig struct {
	AllowedTargetCIDRs []string `json:"allowed_target_cidrs"`
	AllowedTargetHosts []string `json:"allowed_target_hosts"`
	AllowedTargetPorts []int    `json:"allowed_target_ports"`
	DialTimeoutSeconds int      `json:"dial_timeout_seconds"`
}

// SOCKS5DialResult is exchanged on SOCKS5 target data streams before payload
// relay starts. It is not a control-channel provisioning schema.
type SOCKS5DialResult struct {
	Status    string `json:"status"`
	BoundAddr string `json:"bound_addr,omitempty"`
	BoundPort int    `json:"bound_port,omitempty"`
	Message   string `json:"message,omitempty"`
}
