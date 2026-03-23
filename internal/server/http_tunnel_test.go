package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"netsgo/pkg/protocol"
)

func newHTTPRuleTestServer(t *testing.T) (*Server, func()) {
	t.Helper()

	s, _, _, cleanup := setupTestServerWithStores(t, true)
	return s, cleanup
}

func TestCanonicalHost(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain host", input: "example.com", want: "example.com"},
		{name: "strip standard http port", input: "example.com:80", want: "example.com"},
		{name: "keep non standard port", input: "example.com:8080", want: "example.com:8080"},
		{name: "lower case", input: "EXAMPLE.COM", want: "example.com"},
		{name: "strip http scheme", input: "http://example.com", want: "example.com"},
		{name: "strip https scheme path and standard port", input: "https://example.com:443/path", want: "example.com"},
		{name: "empty", input: "", want: ""},
		{name: "ipv6 keep non standard port", input: "https://[2001:db8::1]:8443/path", want: "[2001:db8::1]:8443"},
		{name: "ipv6 strip standard https port", input: "https://[2001:db8::1]:443/path", want: "[2001:db8::1]"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalHost(tc.input); got != tc.want {
				t.Fatalf("canonicalHost(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateDomain(t *testing.T) {
	testCases := []struct {
		name   string
		domain string
		valid  bool
	}{
		{name: "valid root domain", domain: "example.com", valid: true},
		{name: "valid subdomain", domain: "sub.example.com", valid: true},
		{name: "valid deep subdomain", domain: "a.b.c.example.com", valid: true},
		{name: "invalid empty", domain: "", valid: false},
		{name: "invalid wildcard", domain: "*.example.com", valid: false},
		{name: "invalid localhost", domain: "localhost", valid: false},
		{name: "invalid space", domain: "app example.com", valid: false},
		{name: "invalid scheme", domain: "https://example.com", valid: false},
		{name: "invalid path", domain: "example.com/path", valid: false},
		{name: "invalid ipv4", domain: "192.168.1.10", valid: false},
		{name: "invalid ipv6", domain: "[2001:db8::1]", valid: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDomain(tc.domain)
			if tc.valid && err != nil {
				t.Fatalf("validateDomain(%q) unexpected error: %v", tc.domain, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("validateDomain(%q) expected error, got nil", tc.domain)
			}
		})
	}
}

func TestEffectiveManagementHost(t *testing.T) {
	testCases := []struct {
		name       string
		env        string
		configAddr string
		listenAddr string
		want       string
	}{
		{
			name:       "env has highest priority",
			env:        "https://Env.EXAMPLE.com:443",
			configAddr: "https://stored.example.com",
			listenAddr: "listen.example.com:9443",
			want:       "env.example.com",
		},
		{
			name:       "persistent server addr used when env absent",
			configAddr: "https://Stored.EXAMPLE.com:8443",
			listenAddr: "listen.example.com:9443",
			want:       "stored.example.com:8443",
		},
		{
			name:       "derive from listen addr when env and config missing",
			listenAddr: "listen.example.com:9443",
			want:       "listen.example.com:9443",
		},
		{
			name:       "strip standard port from listen addr",
			listenAddr: "listen.example.com:80",
			want:       "listen.example.com",
		},
		{
			name:       "invalid env should fall back to config",
			env:        "ws://invalid.example.com",
			configAddr: "https://stored.example.com:8443",
			listenAddr: "listen.example.com:9443",
			want:       "stored.example.com:8443",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NETSGO_SERVER_ADDR", tc.env)

			cfg := &ServerConfig{ServerAddr: tc.configAddr}
			if got := effectiveManagementHost(cfg, tc.listenAddr); got != tc.want {
				t.Fatalf("effectiveManagementHost(%q, %q) = %q, want %q", tc.configAddr, tc.listenAddr, got, tc.want)
			}
		})
	}
}

func TestIsServerAddrLocked(t *testing.T) {
	t.Run("valid env locks server addr", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "https://locked.example.com")
		if !isServerAddrLocked() {
			t.Fatal("合法环境变量应锁定 server_addr")
		}
	})

	t.Run("invalid env should not lock server addr", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "ws://locked.example.com")
		if isServerAddrLocked() {
			t.Fatal("非法环境变量不应锁定 server_addr")
		}
	})
}

func TestValidateServerAddr(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
		valid bool
	}{
		{name: "fqdn https", input: "https://example.com", want: "https://example.com", valid: true},
		{name: "https default port normalized", input: "https://example.com:443", want: "https://example.com", valid: true},
		{name: "http default port normalized", input: "http://example.com:80", want: "http://example.com", valid: true},
		{name: "fqdn with port", input: "https://example.com:8443", want: "https://example.com:8443", valid: true},
		{name: "localhost", input: "http://localhost", want: "http://localhost", valid: true},
		{name: "ipv4", input: "https://127.0.0.1", want: "https://127.0.0.1", valid: true},
		{name: "ipv4 with port", input: "http://192.168.1.10:8080", want: "http://192.168.1.10:8080", valid: true},
		{name: "ipv6 without port", input: "https://[::1]", want: "https://[::1]", valid: true},
		{name: "ipv6", input: "https://[::1]:8443", want: "https://[::1]:8443", valid: true},
		{name: "root path normalized", input: "https://example.com/", want: "https://example.com", valid: true},
		{name: "missing scheme", input: "example.com", valid: false},
		{name: "missing scheme ipv4", input: "127.0.0.1:8080", valid: false},
		{name: "bare localhost", input: "localhost", valid: false},
		{name: "unsupported scheme ftp", input: "ftp://example.com", valid: false},
		{name: "unsupported scheme ws", input: "ws://example.com", valid: false},
		{name: "path not allowed", input: "https://example.com/path", valid: false},
		{name: "query not allowed", input: "https://example.com?x=1", valid: false},
		{name: "userinfo not allowed", input: "https://user:pass@example.com", valid: false},
		{name: "empty userinfo not allowed", input: "https://@example.com", valid: false},
		{name: "non fqdn domain", input: "http://test", valid: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateServerAddr(tc.input)
			if tc.valid {
				if err != nil {
					t.Fatalf("validateServerAddr(%q) unexpected error: %v", tc.input, err)
				}
				if got != tc.want {
					t.Fatalf("validateServerAddr(%q) = %q, want %q", tc.input, got, tc.want)
				}
				return
			}

			if err == nil {
				t.Fatalf("validateServerAddr(%q) expected error, got nil", tc.input)
			}
		})
	}
}

func TestNormalizeServerAddrForConfigUpdate(t *testing.T) {
	t.Run("unchanged legacy value is accepted", func(t *testing.T) {
		got, err := normalizeServerAddrForConfigUpdate("localhost", "localhost")
		if err != nil {
			t.Fatalf("unchanged legacy value should be accepted, got error: %v", err)
		}
		if got != "localhost" {
			t.Fatalf("expected unchanged legacy value, got %q", got)
		}
	})

	t.Run("default port is normalized for current valid value", func(t *testing.T) {
		got, err := normalizeServerAddrForConfigUpdate("https://example.com", "https://example.com:443")
		if err != nil {
			t.Fatalf("expected normalized valid current value, got error: %v", err)
		}
		if got != "https://example.com" {
			t.Fatalf("expected https://example.com, got %q", got)
		}
	})
}

func TestDomainConflictWithManagementHost(t *testing.T) {
	t.Run("same host conflicts", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "https://Panel.EXAMPLE.com")

		s, cleanup := newHTTPRuleTestServer(t)
		defer cleanup()

		if err := checkDomainConflict("panel.example.com", "", "", s); err == nil {
			t.Fatal("domain 与 management host 相同时应返回冲突")
		}
	})

	t.Run("different host does not conflict", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "https://panel.example.com")

		s, cleanup := newHTTPRuleTestServer(t)
		defer cleanup()

		if err := checkDomainConflict("app.example.com", "", "", s); err != nil {
			t.Fatalf("不同 host 不应冲突，得到 %v", err)
		}
	})

	t.Run("comparison is case insensitive", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "https://panel.example.com")

		s, cleanup := newHTTPRuleTestServer(t)
		defer cleanup()

		if err := checkDomainConflict("PANEL.EXAMPLE.COM", "", "", s); err == nil {
			t.Fatal("management host 冲突应大小写不敏感")
		}
	})
}

func TestDomainConflictBetweenTunnels(t *testing.T) {
	t.Run("same client duplicate domain conflicts", func(t *testing.T) {
		s, cleanup := newHTTPRuleTestServer(t)
		defer cleanup()

		seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
			Name:      "http-a",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "app.example.com",
			LocalIP:   "127.0.0.1",
			LocalPort: 8080,
		}, protocol.ProxyStatusActive)

		if err := checkDomainConflict("app.example.com", "", "", s); err == nil {
			t.Fatal("同一 client 重复 domain 应冲突")
		}
	})

	t.Run("different client duplicate domain conflicts", func(t *testing.T) {
		s, cleanup := newHTTPRuleTestServer(t)
		defer cleanup()

		seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
			Name:      "http-a",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "app.example.com",
			LocalIP:   "127.0.0.1",
			LocalPort: 8080,
		}, protocol.ProxyStatusActive)
		seedStoredTunnel(t, s, "client-2", protocol.ProxyNewRequest{
			Name:      "http-b",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "other.example.com",
			LocalIP:   "127.0.0.1",
			LocalPort: 9090,
		}, protocol.ProxyStatusActive)

		if err := checkDomainConflict("APP.EXAMPLE.COM", "", "", s); err == nil {
			t.Fatal("不同 client 重复 domain 也应冲突")
		}
	})

	t.Run("pending paused stopped and error still conflict", func(t *testing.T) {
		statuses := []string{
			protocol.ProxyStatusPending,
			protocol.ProxyStatusPaused,
			protocol.ProxyStatusStopped,
			protocol.ProxyStatusError,
		}

		for _, status := range statuses {
			t.Run(status, func(t *testing.T) {
				s, cleanup := newHTTPRuleTestServer(t)
				defer cleanup()

				seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
					Name:      "http-" + status,
					Type:      protocol.ProxyTypeHTTP,
					Domain:    "state.example.com",
					LocalIP:   "127.0.0.1",
					LocalPort: 8080,
				}, status)

				if err := checkDomainConflict("state.example.com", "", "", s); err == nil {
					t.Fatalf("%s 状态仍应参与冲突检测", status)
				}
			})
		}
	})

	t.Run("removed tunnel no longer conflicts", func(t *testing.T) {
		s, cleanup := newHTTPRuleTestServer(t)
		defer cleanup()

		seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
			Name:      "http-a",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "deleted.example.com",
			LocalIP:   "127.0.0.1",
			LocalPort: 8080,
		}, protocol.ProxyStatusStopped)

		if err := s.store.RemoveTunnel("client-1", "http-a"); err != nil {
			t.Fatalf("删除测试隧道失败: %v", err)
		}

		if err := checkDomainConflict("deleted.example.com", "", "", s); err != nil {
			t.Fatalf("已删除隧道不应再冲突，得到 %v", err)
		}
	})

	t.Run("same name on different clients should both be reported", func(t *testing.T) {
		s, cleanup := newHTTPRuleTestServer(t)
		defer cleanup()

		seedStoredTunnel(t, s, "client-1", protocol.ProxyNewRequest{
			Name:      "shared-name",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "dup.example.com",
			LocalIP:   "127.0.0.1",
			LocalPort: 8080,
		}, protocol.ProxyStatusPaused)
		seedStoredTunnel(t, s, "client-2", protocol.ProxyNewRequest{
			Name:      "shared-name",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "dup.example.com",
			LocalIP:   "127.0.0.1",
			LocalPort: 9090,
		}, protocol.ProxyStatusStopped)

		conflicts := findHTTPDomainConflictNames("dup.example.com", "", "", s)
		if len(conflicts) != 2 {
			t.Fatalf("不同 client 同名 tunnel 不应被去重，得到 %v", conflicts)
		}
	})
}

func TestIsNetsgoControlRequest(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		protocol string
		want     bool
	}{
		{name: "control path and correct subprotocol", path: "/ws/control", protocol: "netsgo-control.v1", want: true},
		{name: "control path without subprotocol", path: "/ws/control", protocol: "", want: false},
		{name: "control path with wrong subprotocol", path: "/ws/control", protocol: "netsgo-data.v1", want: false},
		{name: "wrong path with control subprotocol", path: "/ws/data", protocol: "netsgo-control.v1", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.protocol != "" {
				req.Header.Set("Sec-WebSocket-Protocol", tc.protocol)
			}

			if got := isNetsgoControlRequest(req); got != tc.want {
				t.Fatalf("isNetsgoControlRequest(%q, %q) = %v, want %v", tc.path, tc.protocol, got, tc.want)
			}
		})
	}
}

func TestIsNetsgoDataRequest(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		protocol string
		want     bool
	}{
		{name: "data path and correct subprotocol", path: "/ws/data", protocol: "netsgo-data.v1", want: true},
		{name: "data path without subprotocol", path: "/ws/data", protocol: "", want: false},
		{name: "data path with wrong subprotocol", path: "/ws/data", protocol: "netsgo-control.v1", want: false},
		{name: "wrong path with data subprotocol", path: "/ws/control", protocol: "netsgo-data.v1", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.protocol != "" {
				req.Header.Set("Sec-WebSocket-Protocol", tc.protocol)
			}

			if got := isNetsgoDataRequest(req); got != tc.want {
				t.Fatalf("isNetsgoDataRequest(%q, %q) = %v, want %v", tc.path, tc.protocol, got, tc.want)
			}
		})
	}
}

func TestTrustedProxyHeaders(t *testing.T) {
	testCases := []struct {
		name       string
		serverTLS  *TLSConfig
		remoteAddr string
		forwarded  http.Header
		requestTLS *tls.ConnectionState
		domain     string
		wantHost   string
		wantXFF    string
		wantXFH    string
		wantXFP    string
	}{
		{
			name:       "direct request sets fresh forwarded for",
			serverTLS:  &TLSConfig{Mode: TLSModeOff},
			remoteAddr: "198.51.100.10:4321",
			forwarded:  http.Header{},
			domain:     "app.example.com",
			wantHost:   "app.example.com",
			wantXFF:    "198.51.100.10",
			wantXFH:    "app.example.com",
			wantXFP:    "http",
		},
		{
			name:       "trusted proxy appends remote peer and keeps forwarded proto",
			serverTLS:  &TLSConfig{Mode: TLSModeOff, TrustedProxies: []string{"10.0.0.0/8"}},
			remoteAddr: "10.1.2.3:4321",
			forwarded: http.Header{
				"X-Forwarded-For":   []string{"198.51.100.10"},
				"X-Forwarded-Proto": []string{"https"},
			},
			domain:   "app.example.com",
			wantHost: "app.example.com",
			wantXFF:  "198.51.100.10, 10.1.2.3",
			wantXFH:  "app.example.com",
			wantXFP:  "https",
		},
		{
			name:       "untrusted proxy headers are ignored",
			serverTLS:  &TLSConfig{Mode: TLSModeOff, TrustedProxies: []string{"10.0.0.0/8"}},
			remoteAddr: "203.0.113.5:4321",
			forwarded: http.Header{
				"X-Forwarded-For":   []string{"198.51.100.10"},
				"X-Forwarded-Proto": []string{"https"},
			},
			domain:   "app.example.com",
			wantHost: "app.example.com",
			wantXFF:  "203.0.113.5",
			wantXFH:  "app.example.com",
			wantXFP:  "http",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(0)
			s.TLS = tc.serverTLS

			req := httptest.NewRequest(http.MethodGet, "http://"+tc.domain+"/", nil)
			req.Host = tc.domain
			req.RemoteAddr = tc.remoteAddr
			req.TLS = tc.requestTLS
			req.Header = tc.forwarded.Clone()

			gotHost, gotHeaders := computeForwardedHeaders(s, req, tc.domain)
			if gotHost != tc.wantHost {
				t.Fatalf("Host = %q, want %q", gotHost, tc.wantHost)
			}
			if gotHeaders.Get("X-Forwarded-For") != tc.wantXFF {
				t.Fatalf("X-Forwarded-For = %q, want %q", gotHeaders.Get("X-Forwarded-For"), tc.wantXFF)
			}
			if gotHeaders.Get("X-Forwarded-Host") != tc.wantXFH {
				t.Fatalf("X-Forwarded-Host = %q, want %q", gotHeaders.Get("X-Forwarded-Host"), tc.wantXFH)
			}
			if gotHeaders.Get("X-Forwarded-Proto") != tc.wantXFP {
				t.Fatalf("X-Forwarded-Proto = %q, want %q", gotHeaders.Get("X-Forwarded-Proto"), tc.wantXFP)
			}
		})
	}
}
