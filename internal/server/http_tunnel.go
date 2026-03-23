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

const (
	httpTunnelErrCodeServerAddrConflict = "server_addr_conflict"
	httpTunnelErrCodeDomainConflict     = "http_tunnel_conflict"
)

// httpTunnelRuleError 表示 HTTP 域名规则层返回的结构化冲突。
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

func (e *httpTunnelRuleError) ConflictingTunnels() []string {
	if len(e.conflictingTunnels) == 0 {
		return []string{}
	}
	return append([]string(nil), e.conflictingTunnels...)
}

// canonicalHost 统一管理地址 / HTTP 域名的比较口径。
// 它会去掉 scheme、path，转小写，并去掉标准端口 80/443。
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
	host = strings.Trim(host, "[]")
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

// validateDomain 只接受明确的 FQDN，不接受 scheme/path/IP/wildcard。
func validateDomain(domain string) error {
	raw := strings.TrimSpace(domain)
	if raw == "" {
		return fmt.Errorf("domain 不能为空")
	}
	if raw != domain || strings.ContainsAny(domain, " \t\r\n") {
		return fmt.Errorf("domain 不能包含空白字符")
	}
	if strings.Contains(domain, "*") {
		return fmt.Errorf("domain 不支持通配符")
	}
	if strings.Contains(domain, "://") {
		return fmt.Errorf("domain 不能包含 scheme")
	}
	if strings.ContainsAny(domain, "/?#") {
		return fmt.Errorf("domain 不能包含路径或查询")
	}
	if strings.Contains(domain, ":") {
		return fmt.Errorf("domain 不能包含端口或 IPv6 字面量")
	}

	normalized := strings.ToLower(raw)
	if ip := net.ParseIP(normalized); ip != nil {
		return fmt.Errorf("domain 不能是 IP 地址")
	}

	labels := strings.Split(normalized, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain 必须包含至少两个标签")
	}

	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("domain 标签不能为空")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("domain 标签不能以连字符开头或结尾")
		}
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			return fmt.Errorf("domain 包含非法字符")
		}
	}

	return nil
}

// validateServerAddr 校验 setup / 系统设置里的管理地址。
// 仅允许 http(s):// + (FQDN | localhost | IPv4 | IPv6字面量) + 可选端口。
func validateServerAddr(addr string) (string, error) {
	raw := strings.TrimSpace(addr)
	if raw == "" {
		return "", fmt.Errorf("server_addr 不能为空")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("server_addr 必须是完整的 http:// 或 https:// URL")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("server_addr 仅支持 http:// 或 https://")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("server_addr 必须包含主机名")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("server_addr 不能包含用户信息")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("server_addr 不能包含查询参数或锚点")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("server_addr 不能包含路径")
	}

	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("server_addr 必须包含主机名")
	}

	switch {
	case hostname == "localhost":
	case net.ParseIP(hostname) != nil:
	default:
		if err := validateDomain(hostname); err != nil {
			return "", fmt.Errorf("server_addr 主机名无效: %w", err)
		}
	}

	port := parsed.Port()
	if port != "" {
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return "", fmt.Errorf("server_addr 端口无效")
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

func collectDeclaredHTTPDomains(server *Server) map[string]string {
	owners := collectDeclaredHTTPDomainOwners(server)
	result := make(map[string]string, len(owners))
	for domain, names := range owners {
		if len(names) > 0 {
			result[domain] = names[0]
		}
	}
	return result
}

// collectDeclaredHTTPDomainOwners 会同时扫描运行时和 store，
// 让冲突检测覆盖在线与离线 HTTP 隧道。
func collectDeclaredHTTPDomainOwners(server *Server) map[string][]string {
	owners := map[string][]string{}
	if server == nil {
		return owners
	}

	seenTunnels := map[string]struct{}{}
	appendOwner := func(clientID, name, tunnelType, domain string) {
		if tunnelType != protocol.ProxyTypeHTTP {
			return
		}
		canonical := canonicalHost(domain)
		if canonical == "" {
			return
		}

		key := clientID + ":" + name
		if _, exists := seenTunnels[key]; exists {
			return
		}
		seenTunnels[key] = struct{}{}

		for _, existing := range owners[canonical] {
			if existing == name {
				return
			}
		}
		owners[canonical] = append(owners[canonical], name)
	}

	server.RangeClients(func(clientID string, client *ClientConn) bool {
		client.RangeProxies(func(name string, tunnel *ProxyTunnel) bool {
			appendOwner(clientID, name, tunnel.Config.Type, tunnel.Config.Domain)
			return true
		})
		return true
	})

	if server.store != nil {
		for _, tunnel := range server.store.GetAllTunnels() {
			appendOwner(tunnel.ClientID, tunnel.Name, tunnel.Type, tunnel.Domain)
		}
	}

	for domain := range owners {
		sort.Strings(owners[domain])
	}

	return owners
}

func checkDomainConflict(domain, excludeName, excludeClientID string, server *Server) error {
	canonicalDomain := canonicalHost(domain)
	if canonicalDomain == "" || server == nil {
		return nil
	}

	var cfg *ServerConfig
	if server.adminStore != nil {
		current := server.adminStore.GetServerConfig()
		cfg = &current
	}

	if managementHost := effectiveManagementHost(cfg, serverListenAddr(server)); managementHost != "" && canonicalDomain == managementHost {
		return &httpTunnelRuleError{
			code:    httpTunnelErrCodeServerAddrConflict,
			message: fmt.Sprintf("域名 %q 与当前管理地址冲突", domain),
		}
	}

	conflicts := findHTTPDomainConflictNames(canonicalDomain, excludeName, excludeClientID, server)
	if len(conflicts) == 0 {
		return nil
	}

	return &httpTunnelRuleError{
		code:               httpTunnelErrCodeDomainConflict,
		message:            fmt.Sprintf("域名 %q 已被 HTTP 隧道占用", domain),
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
		conflicts = append(conflicts, name)
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
		return server.listener.Addr().String()
	}
	if server.Port > 0 {
		return fmt.Sprintf("localhost:%d", server.Port)
	}
	return ""
}
