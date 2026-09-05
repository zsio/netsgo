package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"

	"netsgo/pkg/protocol"
)

type countingConn struct {
	net.Conn
	ingressSlots []*budgetSlot
	egressSlots  []*budgetSlot
	read         atomic.Int64
	written      atomic.Int64
}

func (c *countingConn) ingressEgressBytes() (uint64, uint64) {
	return uint64(c.written.Load()), uint64(c.read.Load())
}

func (c *countingConn) Read(b []byte) (int, error) {
	for {
		allowed := waitForBandwidthAllowance(len(b), c.egressSlots...)
		n, err := c.Conn.Read(b[:allowed])
		if unused := allowed - n; unused > 0 {
			refundBandwidthAllowance(unused, c.egressSlots...)
		}
		if n > 0 {
			c.read.Add(int64(n))
		}
		if n > 0 || err != nil {
			return n, err
		}
	}
}

func (c *countingConn) Write(b []byte) (int, error) {
	written := 0
	for written < len(b) {
		allowed := waitForBandwidthAllowance(len(b)-written, c.ingressSlots...)
		n, err := c.Conn.Write(b[written : written+allowed])
		if unused := allowed - n; unused > 0 {
			refundBandwidthAllowance(unused, c.ingressSlots...)
		}
		if n > 0 {
			written += n
			c.written.Add(int64(n))
		}
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

type httpTunnelRoute struct {
	config      protocol.ProxyConfig
	client      *ClientConn
	tunnel      *ProxyTunnel
	activation  proxyActivationSnapshot
	sourceCIDRs []*net.IPNet
}

func (r httpTunnelRoute) serviceable() bool {
	return r.client != nil &&
		r.client.isLive() &&
		clientHasDataSession(r.client) &&
		isTunnelExposed(r.config) &&
		proxyActivationDoneOpen(r.activation.done)
}

func (s *Server) hostDispatchHandler(management http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isNetsgoControlRequest(r):
			s.handleControlWS(w, r)
			return
		case isNetsgoDataRequest(r):
			s.handleDataWS(w, r)
			return
		}

		// The configured management address is reserved even when an HTTP
		// wildcard covers it. Generic loopback/dev fallbacks stay below routes.
		reserved, err := s.managementHostMatch(r.Host, false)
		if err != nil {
			http.Error(w, `{"error":"temporary storage failure"}`, http.StatusServiceUnavailable)
			return
		}
		if reserved {
			management.ServeHTTP(w, r)
			return
		}

		route, ok, err := s.findHTTPRouteByHost(r.Host)
		if err != nil {
			http.Error(w, `{"error":"temporary storage failure"}`, http.StatusServiceUnavailable)
			return
		}
		if ok {
			s.serveHTTPRoute(w, r, route)
			return
		}

		if s.devMode {
			management.ServeHTTP(w, r)
			return
		}

		isManagement, err := s.isManagementHost(r.Host)
		if err != nil {
			http.Error(w, `{"error":"temporary storage failure"}`, http.StatusServiceUnavailable)
			return
		}
		if isManagement {
			management.ServeHTTP(w, r)
			return
		}

		http.NotFound(w, r)
	})
}

func (s *Server) isManagementHost(host string) (bool, error) {
	return s.managementHostMatch(host, true)
}

func (s *Server) managementHostMatch(host string, allowLoopbackFallback bool) (bool, error) {
	var cfg *ServerConfig
	if s.auth.adminStore != nil {
		current, err := s.auth.adminStore.GetServerConfigE()
		if err != nil {
			return false, fmt.Errorf("load server config for management host detection: %w", err)
		}
		cfg = &current
	}
	managementHost := effectiveManagementHost(cfg, serverListenAddr(s))
	if managementHost == "" {
		return false, nil
	}

	reqCanonical := canonicalHost(host)
	if reqCanonical == managementHost {
		return true, nil
	}

	// localhost / 127.0.0.1 / [::1] are treated as equivalent on the same port.
	// In dev environments, tools like Vite may rewrite the Host header to 127.0.0.1:PORT,
	// while serverListenAddr falls back to localhost:PORT, so we need to align here.
	if isLoopbackHost(managementHost) && isLoopbackHost(reqCanonical) {
		_, mPort := splitCanonicalHostPort(managementHost)
		_, rPort := splitCanonicalHostPort(reqCanonical)
		if mPort != "" && mPort == rPort {
			return true, nil
		}

		// When explicitly configured as a portless loopback (e.g. http://localhost),
		// also allow same-machine proxies that rewrite to 127.0.0.1:<server-port> / [::1]:<server-port>.
		if mPort == "" {
			_, listenPort := splitCanonicalHostPort(serverListenAddr(s))
			switch {
			case rPort == "":
				return true, nil
			case listenPort != "" && rPort == listenPort:
				return true, nil
			}
		}

		if mPort == "" && rPort == "" {
			return true, nil
		}
	}

	if !allowLoopbackFallback || !s.AllowLoopbackManagementHost {
		return false, nil
	}

	return isLoopbackHost(reqCanonical), nil
}

func splitCanonicalHostPort(host string) (string, string) {
	canonical := canonicalHost(host)
	if canonical == "" {
		return "", ""
	}
	if hostPart, port, err := net.SplitHostPort(canonical); err == nil {
		return strings.Trim(hostPart, "[]"), port
	}
	return strings.Trim(canonical, "[]"), ""
}

func isLoopbackHost(host string) bool {
	canonical, _ := splitCanonicalHostPort(host)
	if canonical == "" {
		return false
	}

	switch canonical {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func (s *Server) findHTTPRouteByHost(host string) (httpTunnelRoute, bool, error) {
	canonical := canonicalHTTPRouteHost(host)
	if canonical == "" {
		return httpTunnelRoute{}, false, nil
	}
	if s.store != nil {
		claim, ok, err := s.store.findHTTPDomainClaim(canonical)
		if err != nil || !ok {
			return httpTunnelRoute{}, false, err
		}
		if claim.DesiredState == protocol.ProxyDesiredStateRunning {
			if route, found := s.findRuntimeHTTPRouteForClaim(canonical, &claim); found {
				return route, true, nil
			}
		}
		return httpTunnelRoute{config: storedTunnelToProxyConfig(claim)}, true, nil
	}
	route, ok := s.findRuntimeHTTPRoute(canonical)
	return route, ok, nil
}

func (s *Server) findRuntimeHTTPRoute(host string) (httpTunnelRoute, bool) {
	return s.findRuntimeHTTPRouteForClaim(host, nil)
}

func (s *Server) findRuntimeHTTPRouteForClaim(host string, claim *StoredTunnel) (httpTunnelRoute, bool) {
	patterns := httpDomainCandidates(host)
	bestRank := len(patterns)
	var route httpTunnelRoute

	s.RangeClients(func(_ string, client *ClientConn) bool {
		if claim != nil && (client.ID != claim.OwnerClientID || client.OwnerUserID != claim.OwnerUserID) {
			return true
		}
		client.proxyMu.Lock()
		defer client.proxyMu.Unlock()
		for _, tunnel := range client.proxies {
			if tunnel.Config.Type != protocol.ProxyTypeHTTP {
				continue
			}
			config := tunnel.Config
			domain := canonicalHTTPDomain(httpTunnelDomain(config))
			if claim != nil {
				// Legacy runtime entries can lack ID/revision, but must still
				// match this owner's exact persisted name and endpoint domain.
				if (config.ID != "" && config.ID != claim.ID) || config.Name != claim.Name ||
					(config.Revision > 0 && config.Revision != claim.Revision) ||
					domain != canonicalHTTPDomain(tunnelIngressDomain(*claim)) {
					continue
				}
			}
			for rank, pattern := range patterns {
				if pattern != domain || rank >= bestRank {
					continue
				}
				activation := proxyActivationSnapshotLocked(tunnel)
				config = activation.config
				config.Domain = domain
				route = httpTunnelRoute{
					config: config, client: client, tunnel: tunnel,
					activation: activation, sourceCIDRs: activation.sourceCIDRs,
				}
				bestRank = rank
			}
		}
		return true
	})
	return route, bestRank < len(patterns)
}

func httpTunnelDomain(config protocol.ProxyConfig) string {
	if config.Ingress != nil && config.Ingress.Type == protocol.IngressTypeHTTPHost {
		var cfg httpHostConfigAPI
		if err := json.Unmarshal(config.Ingress.Config, &cfg); err == nil && strings.TrimSpace(cfg.Domain) != "" {
			return cfg.Domain
		}
	}
	return config.Domain
}

func (s *Server) serveHTTPRoute(w http.ResponseWriter, r *http.Request, route httpTunnelRoute) {
	if !route.serviceable() {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	if !sourceAddressAllowed(stringAddr(httpTunnelSourceIP(s, r)), route.sourceCIDRs) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if !httpTunnelAuthAllowed(w, r, route.config) {
		return
	}

	s.proxyHTTPRequest(w, r, route)
}

func httpTunnelAuthAllowed(w http.ResponseWriter, r *http.Request, config protocol.ProxyConfig) bool {
	auth := httpTunnelAuthConfig(config)
	if auth.Type != protocol.HTTPAuthTypeBasic {
		return true
	}
	username, password, ok := parseBasicAuthHeader(r.Header.Get("Authorization"))
	if !ok ||
		subtle.ConstantTimeCompare([]byte(username), []byte(auth.Username)) != 1 ||
		!verifyEndpointPassword(auth.PasswordHash, password) {
		w.Header().Set("WWW-Authenticate", `Basic realm="NetsGo tunnel", charset="UTF-8"`)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return false
	}
	return true
}

func httpTunnelAuthConfig(config protocol.ProxyConfig) protocol.HTTPAuthConfig {
	if config.Ingress == nil {
		return protocol.HTTPAuthConfig{Type: protocol.HTTPAuthTypeNone}
	}
	var cfg httpHostConfigAPI
	if err := json.Unmarshal(config.Ingress.Config, &cfg); err != nil {
		return protocol.HTTPAuthConfig{Type: protocol.HTTPAuthTypeNone}
	}
	if cfg.Auth.Type == "" {
		cfg.Auth.Type = protocol.HTTPAuthTypeNone
	}
	return cfg.Auth
}

func parseBasicAuthHeader(value string) (string, string, bool) {
	scheme, payload, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") || strings.TrimSpace(payload) == "" {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return "", "", false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	return username, password, ok
}

func (s *Server) proxyHTTPRequest(w http.ResponseWriter, r *http.Request, route httpTunnelRoute) {
	target := &url.URL{
		Scheme: "http",
		Host:   "netsgo-http-tunnel",
	}

	var cc *countingConn

	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		DisableKeepAlives:     true,
		DisableCompression:    false,
		ResponseHeaderTimeout: 0,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := s.openStreamToClientForActivation(route.client, route.tunnel, route.activation)
			if err != nil {
				return nil, err
			}
			cc = &countingConn{
				Conn:         conn,
				ingressSlots: payloadBudgetSlots(payloadDirectionIngress, route.client.BandwidthRuntime(), route.activation.limits),
				egressSlots:  payloadBudgetSlots(payloadDirectionEgress, route.client.BandwidthRuntime(), route.activation.limits),
			}
			return cc, nil
		},
	}

	proxy := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			host, headers := computeForwardedHeaders(s, pr.In)
			pr.Out.Host = host
			pr.Out.Header = headers
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)

	if cc != nil {
		ingressBytes, egressBytes := cc.ingressEgressBytes()
		s.recordTunnelTraffic(route.client.ID, route.config, ingressBytes, egressBytes)
	}
}
