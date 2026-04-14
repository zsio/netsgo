package server

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"netsgo/pkg/protocol"
)

// httpTunnelRuleError represents a structured conflict returned by the HTTP domain rule layer.
type httpTunnelRuleError struct {
	code               string
	message            string
	conflictingTunnels []string
}

func (e *httpTunnelRuleError) Error() string {
	return e.message
}

func (e *httpTunnelRuleError) ErrorCode() string {
	return e.code
}

func (e *httpTunnelRuleError) Field() string {
	return protocol.TunnelMutationFieldDomain
}

func (e *httpTunnelRuleError) ConflictingTunnels() []string {
	if len(e.conflictingTunnels) == 0 {
		return []string{}
	}
	return append([]string(nil), e.conflictingTunnels...)
}

// canonicalHost normalizes an address / HTTP domain name for comparison.
// It strips the scheme and path, lowercases the result, and removes standard ports 80/443.
func canonicalHost(addr string) string {
	raw := strings.TrimSpace(addr)
	if raw == "" {
		return ""
	}

	hostPart := raw
	switch {
	case strings.Contains(raw, "://"):
		if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
			hostPart = parsed.Host
		}
	case strings.ContainsAny(raw, "/?#"):
		if parsed, err := url.Parse("//" + raw); err == nil && parsed.Host != "" {
			hostPart = parsed.Host
		} else {
			parts := strings.FieldsFunc(raw, func(r rune) bool {
				return r == '/' || r == '?' || r == '#'
			})
			if len(parts) == 0 {
				return ""
			}
			hostPart = parts[0]
		}
	}

	hostPart = strings.ToLower(strings.TrimSpace(hostPart))
	if hostPart == "" {
		return ""
	}

	host, port, err := net.SplitHostPort(hostPart)
	if err == nil {
		host = normalizeHostLiteral(host)
		if host == "" {
			return ""
		}
		if port == "80" || port == "443" {
			return host
		}
		return net.JoinHostPort(strings.Trim(host, "[]"), port)
	}

	return normalizeHostLiteral(hostPart)
}

func normalizeHostLiteral(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	host = strings.Trim(host, "[]")
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

// validateDomain only accepts explicit FQDNs; rejects scheme/path/IP/wildcard.
func validateDomain(domain string) error {
	raw := strings.TrimSpace(domain)
	if raw == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	if raw != domain || strings.ContainsAny(domain, " \t\r\n") {
		return fmt.Errorf("domain cannot contain whitespace")
	}
	if strings.Contains(domain, "*") {
		return fmt.Errorf("domain does not support wildcards")
	}
	if strings.Contains(domain, "://") {
		return fmt.Errorf("domain cannot contain a scheme")
	}
	if strings.ContainsAny(domain, "/?#") {
		return fmt.Errorf("domain cannot contain a path or query string")
	}
	if strings.Contains(domain, ":") {
		return fmt.Errorf("domain cannot contain a port or IPv6 literal")
	}

	normalized := strings.ToLower(raw)
	normalized = strings.TrimSuffix(normalized, ".")

	if len(normalized) > 253 {
		return fmt.Errorf("domain length cannot exceed 253 characters")
	}

	if ip := net.ParseIP(normalized); ip != nil {
		return fmt.Errorf("domain cannot be an IP address")
	}

	labels := strings.Split(normalized, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain must contain at least two labels")
	}

	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("domain label cannot be empty")
		}
		if len(label) > 63 {
			return fmt.Errorf("domain label length cannot exceed 63 characters")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("domain label cannot start or end with a hyphen")
		}
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			if ch > 127 {
				return fmt.Errorf("domain contains non-ASCII characters; please use Punycode format (xn-- prefix)")
			}
			return fmt.Errorf("domain contains invalid characters")
		}
	}

	return nil
}

// validateServerAddr validates the management address in setup / system settings.
// Only allows http(s):// + (FQDN | localhost | IPv4 | IPv6 literal) + optional port.
func validateServerAddr(addr string) (string, error) {
	raw := strings.TrimSpace(addr)
	if raw == "" {
		return "", fmt.Errorf("server_addr cannot be empty")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("server_addr must be a complete http:// or https:// URL")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("server_addr only supports http:// or https://")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("server_addr must include a hostname")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("server_addr cannot contain user info")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("server_addr cannot contain query parameters or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("server_addr cannot contain a path")
	}

	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("server_addr must include a hostname")
	}

	switch {
	case hostname == "localhost":
	case net.ParseIP(hostname) != nil:
	default:
		if err := validateDomain(hostname); err != nil {
			return "", fmt.Errorf("server_addr hostname is invalid: %w", err)
		}
	}

	port := parsed.Port()
	if port != "" {
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return "", fmt.Errorf("server_addr port is invalid")
		}
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	normalizedHost := hostname
	if strings.Contains(hostname, ":") {
		normalizedHost = "[" + hostname + "]"
	}
	if port != "" {
		normalizedHost = net.JoinHostPort(hostname, port)
	}

	return scheme + "://" + normalizedHost, nil
}

func normalizeServerAddrForConfigUpdate(candidate, current string) (string, error) {
	trimmedCandidate := strings.TrimSpace(candidate)
	trimmedCurrent := strings.TrimSpace(current)

	if trimmedCandidate == trimmedCurrent {
		if normalizedCurrent, err := validateServerAddr(current); err == nil {
			return normalizedCurrent, nil
		}
		return trimmedCandidate, nil
	}

	return validateServerAddr(candidate)
}

func containsProtocol(h http.Header, expected string) bool {
	for _, value := range h.Values("Sec-WebSocket-Protocol") {
		for _, token := range strings.Split(value, ",") {
			if strings.TrimSpace(token) == expected {
				return true
			}
		}
	}
	return false
}

func isNetsgoControlRequest(r *http.Request) bool {
	return r != nil &&
		r.URL != nil &&
		r.URL.Path == "/ws/control" &&
		containsProtocol(r.Header, protocol.WSSubProtocolControl)
}

func isNetsgoDataRequest(r *http.Request) bool {
	return r != nil &&
		r.URL != nil &&
		r.URL.Path == "/ws/data" &&
		containsProtocol(r.Header, protocol.WSSubProtocolData)
}

func effectiveManagementHost(cfg *ServerConfig, listenAddr string) string {
	if env := strings.TrimSpace(os.Getenv("NETSGO_SERVER_ADDR")); env != "" {
		if normalized, err := validateServerAddr(env); err == nil {
			return canonicalHost(normalized)
		}
	}
	if cfg != nil && strings.TrimSpace(cfg.ServerAddr) != "" {
		return canonicalHost(cfg.ServerAddr)
	}
	return canonicalHost(listenAddr)
}

func isServerAddrLocked() bool {
	env := strings.TrimSpace(os.Getenv("NETSGO_SERVER_ADDR"))
	if env == "" {
		return false
	}
	_, err := validateServerAddr(env)
	return err == nil
}

func checkDomainConflict(domain, excludeName, excludeClientID string, server *Server) error {
	canonicalDomain := canonicalHost(domain)
	if canonicalDomain == "" || server == nil {
		return nil
	}

	var cfg *ServerConfig
	if server.auth.adminStore != nil {
		current := server.auth.adminStore.GetServerConfig()
		cfg = &current
	}

	if managementHost := effectiveManagementHost(cfg, serverListenAddr(server)); managementHost != "" && canonicalDomain == managementHost {
		return &httpTunnelRuleError{
			code:    protocol.TunnelMutationErrorCodeServerAddrConflict,
			message: fmt.Sprintf("domain %q conflicts with the current management address", domain),
		}
	}

	conflicts := findHTTPDomainConflictNames(canonicalDomain, excludeName, excludeClientID, server)
	if len(conflicts) == 0 {
		return nil
	}

	return &httpTunnelRuleError{
		code:               protocol.TunnelMutationErrorCodeHTTPTunnelConflict,
		message:            fmt.Sprintf("domain %q is already claimed by an HTTP tunnel", domain),
		conflictingTunnels: conflicts,
	}
}

func findHTTPDomainConflictNames(domain, excludeName, excludeClientID string, server *Server) []string {
	canonicalDomain := canonicalHost(domain)
	if canonicalDomain == "" || server == nil {
		return []string{}
	}

	conflicts := []string{}
	seenTunnels := map[string]struct{}{}
	matchAndAppend := func(clientID, name, tunnelType, tunnelDomain string) {
		if tunnelType != protocol.ProxyTypeHTTP {
			return
		}
		if excludeName != "" && excludeClientID != "" && name == excludeName && clientID == excludeClientID {
			return
		}
		if canonicalHost(tunnelDomain) != canonicalDomain {
			return
		}
		key := clientID + ":" + name
		if _, exists := seenTunnels[key]; exists {
			return
		}
		seenTunnels[key] = struct{}{}
		conflicts = append(conflicts, key)
	}

	server.RangeClients(func(clientID string, client *ClientConn) bool {
		client.RangeProxies(func(name string, tunnel *ProxyTunnel) bool {
			matchAndAppend(clientID, name, tunnel.Config.Type, tunnel.Config.Domain)
			return true
		})
		return true
	})

	if server.store != nil {
		for _, tunnel := range server.store.GetAllTunnels() {
			matchAndAppend(tunnel.ClientID, tunnel.Name, tunnel.Type, tunnel.Domain)
		}
	}

	sort.Strings(conflicts)
	return conflicts
}

func computeForwardedHeaders(s *Server, r *http.Request, originalDomain string) (string, http.Header) {
	host := originalDomain
	if strings.TrimSpace(host) == "" {
		host = r.Host
	}

	headers := r.Header.Clone()
	headers.Set("X-Forwarded-Host", host)
	if s != nil && s.isHTTPSRequest(r) {
		headers.Set("X-Forwarded-Proto", "https")
	} else {
		headers.Set("X-Forwarded-Proto", "http")
	}

	clientIP := remoteIPFromAddr(r.RemoteAddr)
	switch {
	case clientIP == "":
		headers.Del("X-Forwarded-For")
	case s != nil && s.trustProxyHeaders(r):
		if existing := strings.TrimSpace(headers.Get("X-Forwarded-For")); existing != "" {
			headers.Set("X-Forwarded-For", existing+", "+clientIP)
		} else {
			headers.Set("X-Forwarded-For", clientIP)
		}
	default:
		headers.Set("X-Forwarded-For", clientIP)
	}

	return host, headers
}

func conflictingHTTPDomainsForServerAddr(serverAddr string, server *Server) []string {
	return findHTTPDomainConflictNames(canonicalHost(serverAddr), "", "", server)
}

func serverListenAddr(server *Server) string {
	if server == nil {
		return ""
	}
	if server.listener != nil {
		if tcp, ok := server.listener.Addr().(*net.TCPAddr); ok {
			if tcp.IP == nil || tcp.IP.IsUnspecified() {
				return fmt.Sprintf("localhost:%d", tcp.Port)
			}
		}
		return server.listener.Addr().String()
	}
	if server.Port > 0 {
		return fmt.Sprintf("localhost:%d", server.Port)
	}
	return ""
}
