package server

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"netsgo/internal/credential"
	"netsgo/pkg/protocol"
)

const (
	defaultSOCKS5DialTimeoutSeconds = 10
	maxSOCKS5DialTimeoutSeconds     = 120
)

var allowAllCIDRs = []string{"0.0.0.0/0", "::/0"}

func normalizeSOCKS5ListenConfig(raw json.RawMessage, requireNoAuthConfirmation bool) (protocol.SOCKS5ListenConfig, error) {
	var cfg protocol.SOCKS5ListenConfig
	if err := decodeStrictEndpointConfig(raw, &cfg); err != nil {
		return protocol.SOCKS5ListenConfig{}, err
	}
	cfg.BindIP = strings.TrimSpace(cfg.BindIP)
	if cfg.BindIP == "" {
		cfg.BindIP = "0.0.0.0"
	}
	if ip := net.ParseIP(cfg.BindIP); ip == nil {
		return protocol.SOCKS5ListenConfig{}, fmt.Errorf("bind_ip must be a valid IP address")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return protocol.SOCKS5ListenConfig{}, fmt.Errorf("port must be in range 1-65535")
	}
	cidrs, err := normalizeCIDRList(cfg.AllowedSourceCIDRs, "allowed_source_cidrs")
	if err != nil {
		return protocol.SOCKS5ListenConfig{}, err
	}
	cfg.AllowedSourceCIDRs = cidrs

	auth, err := normalizeSOCKS5AuthConfig(cfg.Auth)
	if err != nil {
		return protocol.SOCKS5ListenConfig{}, err
	}
	if auth.Type == protocol.SOCKS5AuthTypeNone && requireNoAuthConfirmation {
		return protocol.SOCKS5ListenConfig{}, fmt.Errorf("confirm_no_auth_risk is required when SOCKS5 auth is disabled")
	}
	cfg.Auth = auth
	return cfg, nil
}

func normalizeSOCKS5AuthConfig(auth protocol.SOCKS5AuthConfig) (protocol.SOCKS5AuthConfig, error) {
	auth.Type = strings.TrimSpace(auth.Type)
	if auth.Type == "" {
		auth.Type = protocol.SOCKS5AuthTypeNone
	}
	switch auth.Type {
	case protocol.SOCKS5AuthTypeNone:
		return protocol.SOCKS5AuthConfig{Type: protocol.SOCKS5AuthTypeNone}, nil
	case protocol.SOCKS5AuthTypeUsernamePassword:
		auth.Username = strings.TrimSpace(auth.Username)
		if auth.Username == "" {
			return protocol.SOCKS5AuthConfig{}, fmt.Errorf("auth.username is required")
		}
		if len(auth.Username) > 255 {
			return protocol.SOCKS5AuthConfig{}, fmt.Errorf("auth.username cannot exceed 255 bytes")
		}
		if auth.Password != "" {
			hash, err := hashEndpointPassword(auth.Password)
			if err != nil {
				return protocol.SOCKS5AuthConfig{}, err
			}
			auth.PasswordHash = hash
		}
		if auth.PasswordHash == "" {
			return protocol.SOCKS5AuthConfig{}, fmt.Errorf("auth.password is required")
		}
		auth.Password = ""
		return auth, nil
	default:
		return protocol.SOCKS5AuthConfig{}, fmt.Errorf("unsupported auth.type %q", auth.Type)
	}
}

func redactSOCKS5ListenConfig(raw json.RawMessage) json.RawMessage {
	var cfg protocol.SOCKS5ListenConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return raw
	}
	cfg.Auth.Password = ""
	redacted := map[string]any{
		"bind_ip":              cfg.BindIP,
		"port":                 cfg.Port,
		"allowed_source_cidrs": cfg.AllowedSourceCIDRs,
		"auth": map[string]any{
			"type":     cfg.Auth.Type,
			"username": cfg.Auth.Username,
		},
	}
	if cfg.Auth.Username == "" {
		delete(redacted["auth"].(map[string]any), "username")
	}
	return mustRawJSON(redacted)
}

func normalizeSOCKS5ConnectHandlerConfig(raw json.RawMessage) (protocol.SOCKS5ConnectHandlerConfig, error) {
	var cfg protocol.SOCKS5ConnectHandlerConfig
	if err := decodeStrictEndpointConfig(raw, &cfg); err != nil {
		return protocol.SOCKS5ConnectHandlerConfig{}, err
	}
	cidrs, err := normalizeCIDRList(cfg.AllowedTargetCIDRs, "allowed_target_cidrs")
	if err != nil {
		return protocol.SOCKS5ConnectHandlerConfig{}, err
	}
	cfg.AllowedTargetCIDRs = cidrs
	hosts := make([]string, 0, len(cfg.AllowedTargetHosts))
	for _, rawHost := range cfg.AllowedTargetHosts {
		host, err := normalizeTargetHost(rawHost)
		if err != nil {
			return protocol.SOCKS5ConnectHandlerConfig{}, fmt.Errorf("allowed_target_hosts: %w", err)
		}
		hosts = append(hosts, host)
	}
	cfg.AllowedTargetHosts = dedupeStrings(hosts)
	ports := make([]int, 0, len(cfg.AllowedTargetPorts))
	seenPorts := make(map[int]struct{})
	for _, port := range cfg.AllowedTargetPorts {
		if port < 1 || port > 65535 {
			return protocol.SOCKS5ConnectHandlerConfig{}, fmt.Errorf("allowed_target_ports values must be in range 1-65535")
		}
		if _, ok := seenPorts[port]; ok {
			continue
		}
		seenPorts[port] = struct{}{}
		ports = append(ports, port)
	}
	cfg.AllowedTargetPorts = ports
	if cfg.DialTimeoutSeconds == 0 {
		cfg.DialTimeoutSeconds = defaultSOCKS5DialTimeoutSeconds
	}
	if cfg.DialTimeoutSeconds < 1 || cfg.DialTimeoutSeconds > maxSOCKS5DialTimeoutSeconds {
		return protocol.SOCKS5ConnectHandlerConfig{}, fmt.Errorf("dial_timeout_seconds must be in range 1-%d", maxSOCKS5DialTimeoutSeconds)
	}
	return cfg, nil
}

func normalizeCIDRList(values []string, field string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s is required and must explicitly include allowed CIDRs", field)
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s contains an empty CIDR", field)
		}
		_, ipNet, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("%s contains invalid CIDR %q", field, value)
		}
		canonical := ipNet.String()
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out, nil
}

func normalizeTargetHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	host = strings.TrimSuffix(host, ".")
	host = strings.Trim(host, "[]")
	if host == "" {
		return "", fmt.Errorf("host cannot be empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		return strings.ToLower(ip.String()), nil
	}
	ascii := strings.ToLower(host)
	if err := validateSOCKS5Domain(ascii); err != nil {
		return "", err
	}
	return ascii, nil
}

func validateSOCKS5Domain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	if strings.ContainsAny(domain, " \t\r\n/?:#") || strings.Contains(domain, "*") {
		return fmt.Errorf("domain contains invalid characters")
	}
	if len(domain) > 253 {
		return fmt.Errorf("domain length cannot exceed 253 characters")
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("domain label cannot be empty")
		}
		if len(label) > 63 {
			return fmt.Errorf("domain label length cannot exceed 63 characters")
		}
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			return fmt.Errorf("domain contains invalid characters; use punycode for non-ASCII names")
		}
	}
	return nil
}

func dedupeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func hashEndpointPassword(password string) (string, error) {
	return credential.HashPassword(password)
}

func verifyEndpointPassword(encoded, password string) bool {
	return credential.VerifyPassword(encoded, password)
}
