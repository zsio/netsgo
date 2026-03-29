package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"netsgo/pkg/protocol"
)

type httpTunnelRoute struct {
	config protocol.ProxyConfig
	client *ClientConn
}

func (r httpTunnelRoute) serviceable() bool {
	return r.client != nil &&
		r.client.isLive() &&
		isTunnelExposed(r.config)
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

		if route, ok := s.findHTTPRouteByHost(r.Host); ok {
			s.serveHTTPRoute(w, r, route)
			return
		}

		if s.allowSetupRequest(r) || s.isManagementHost(r.Host) {
			management.ServeHTTP(w, r)
			return
		}

		http.NotFound(w, r)
	})
}

func (s *Server) allowSetupRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if s.auth.adminStore != nil && s.auth.adminStore.IsInitialized() {
		return false
	}

	path := r.URL.Path
	switch {
	case path == "/":
		return true
	case path == "/favicon.ico":
		return true
	case strings.HasPrefix(path, "/assets/"):
		return true
	case strings.HasPrefix(path, "/api/setup/"):
		return true
	default:
		return false
	}
}

func (s *Server) isManagementHost(host string) bool {
	var cfg *ServerConfig
	if s.auth.adminStore != nil {
		current := s.auth.adminStore.GetServerConfig()
		cfg = &current
	}
	managementHost := effectiveManagementHost(cfg, serverListenAddr(s))
	if managementHost == "" {
		return false
	}

	reqCanonical := canonicalHost(host)
	if reqCanonical == managementHost {
		return true
	}

	if !s.AllowLoopbackManagementHost {
		return false
	}

	return isLoopbackHost(reqCanonical)
}

func isLoopbackHost(host string) bool {
	canonical := canonicalHost(host)
	if canonical == "" {
		return false
	}

	if hostPart, _, err := net.SplitHostPort(canonical); err == nil {
		canonical = hostPart
	}

	switch strings.Trim(canonical, "[]") {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func (s *Server) findHTTPRouteByHost(host string) (httpTunnelRoute, bool) {
	canonical := canonicalHost(host)
	if canonical == "" {
		return httpTunnelRoute{}, false
	}

	serverRoute, ok := s.findRuntimeHTTPRoute(canonical)
	if ok {
		return serverRoute, true
	}
	if s.store == nil {
		return httpTunnelRoute{}, false
	}

	for _, stored := range s.store.GetAllTunnels() {
		if stored.Type != protocol.ProxyTypeHTTP {
			continue
		}
		if canonicalHost(stored.Domain) != canonical {
			continue
		}
		return httpTunnelRoute{
			config: storedTunnelToProxyConfig(stored),
		}, true
	}

	return httpTunnelRoute{}, false
}

func (s *Server) findRuntimeHTTPRoute(host string) (httpTunnelRoute, bool) {
	var route httpTunnelRoute
	var found bool

	s.RangeClients(func(_ string, client *ClientConn) bool {
		client.RangeProxies(func(_ string, tunnel *ProxyTunnel) bool {
			if tunnel.Config.Type != protocol.ProxyTypeHTTP {
				return true
			}
			if canonicalHost(tunnel.Config.Domain) != host {
				return true
			}
			route = httpTunnelRoute{
				config: tunnel.Config,
				client: client,
			}
			found = true
			return false
		})
		return !found
	})

	return route, found
}

func (s *Server) serveHTTPRoute(w http.ResponseWriter, r *http.Request, route httpTunnelRoute) {
	if !route.serviceable() {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}

	s.proxyHTTPRequest(w, r, route)
}

func (s *Server) proxyHTTPRequest(w http.ResponseWriter, r *http.Request, route httpTunnelRoute) {
	target := &url.URL{
		Scheme: "http",
		Host:   "netsgo-http-tunnel",
	}

	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		DisableKeepAlives:     true,
		DisableCompression:    false,
		ResponseHeaderTimeout: 0,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return s.openStreamToClient(route.client, route.config.Name)
		},
	}

	proxy := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			host, headers := computeForwardedHeaders(s, pr.In, route.config.Domain)
			pr.Out.Host = host
			pr.Out.Header = headers
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			status := http.StatusBadGateway
			if isHTTPRouteUnavailable(err) {
				status = http.StatusServiceUnavailable
			}
			http.Error(w, http.StatusText(status), status)
		},
	}

	proxy.ServeHTTP(w, r)
}

func isHTTPRouteUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return errors.Is(err, context.Canceled) ||
		strings.Contains(msg, "当前不在线") ||
		strings.Contains(msg, "数据通道未建立")
}
