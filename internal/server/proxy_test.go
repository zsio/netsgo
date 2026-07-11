package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// ============================================================
// Proxy management and listener tests
// ============================================================

type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteAddrConn) RemoteAddr() net.Addr {
	return c.remote
}

func TestStartProxy_Success(t *testing.T) {
	s := New(0)
	clientID := "proxy-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	// Pretend it has an active DataSession (use net.Pipe as a placeholder)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	// Try to start a public-facing proxy (assign a random port)
	req := protocol.ProxyNewRequest{
		Name:       "random-port-tunnel",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  8080,
		RemotePort: reserveTCPPort(t),
	}

	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}

	// Check internal state
	client.proxyMu.RLock()
	tunnel, exists := client.proxies[req.Name]
	client.proxyMu.RUnlock()

	if !exists {
		t.Fatal("StartProxy succeeded but did not add the tunnel to the map")
	}

	if tunnel.Config.RemotePort <= 0 {
		t.Errorf("Allocated port is invalid: %d", tunnel.Config.RemotePort)
	}

	// Confirm the listener is actually open
	testConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", tunnel.Config.RemotePort))
	if err != nil {
		t.Errorf("Unable to dial the bound public port, which indicates the listener is broken: %v", err)
	} else {
		_ = testConn.Close()
	}

	// Cleanup
	s.StopAllProxies(client)
	_ = cConn.Close()
	_ = sConn.Close()
}

func TestServerListenAddressPreservesWildcardHost(t *testing.T) {
	tests := []struct {
		name   string
		bindIP string
		want   string
	}{
		{name: "empty", bindIP: "", want: ":1234"},
		{name: "wildcard", bindIP: "0.0.0.0", want: ":1234"},
		{name: "trimmed wildcard", bindIP: " 0.0.0.0 ", want: ":1234"},
		{name: "loopback", bindIP: "127.0.0.1", want: "127.0.0.1:1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serverListenAddress(tt.bindIP, 1234); got != tt.want {
				t.Fatalf("serverListenAddress(%q, 1234) = %q, want %q", tt.bindIP, got, tt.want)
			}
		})
	}
}

func TestStartProxy_DefaultBindIPListensWildcard(t *testing.T) {
	s := New(0)
	client := &ClientConn{ID: "proxy-bind-default", proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(client.ID, client)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession
	t.Cleanup(func() {
		s.StopAllProxies(client)
		_ = cConn.Close()
		_ = sConn.Close()
	})

	req := protocol.ProxyNewRequest{Name: "wildcard", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: reserveTCPPort(t)}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}
	client.proxyMu.RLock()
	addr := client.proxies[req.Name].Listener.Addr().(*net.TCPAddr)
	client.proxyMu.RUnlock()
	if !addr.IP.IsUnspecified() {
		t.Fatalf("omitted bind_ip should listen wildcard, got %s", addr.IP)
	}
}

func TestStartProxy_ExplicitLoopbackBindIPUsesLoopback(t *testing.T) {
	s := New(0)
	client := &ClientConn{ID: "proxy-bind-loopback", proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(client.ID, client)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession
	t.Cleanup(func() {
		s.StopAllProxies(client)
		_ = cConn.Close()
		_ = sConn.Close()
	})

	req := protocol.ProxyNewRequest{Name: "loopback", Type: protocol.ProxyTypeTCP, BindIP: "127.0.0.1", LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: reserveTCPPort(t)}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}
	client.proxyMu.RLock()
	tunnel := client.proxies[req.Name]
	addr := tunnel.Listener.Addr().(*net.TCPAddr)
	client.proxyMu.RUnlock()
	if !addr.IP.IsLoopback() {
		t.Fatalf("explicit 127.0.0.1 bind should listen loopback, got %s", addr.IP)
	}
	if tunnel.Config.BindIP != "127.0.0.1" {
		t.Fatalf("runtime config should preserve bind_ip, got %q", tunnel.Config.BindIP)
	}
}

func TestStartProxy_NoDataChannel(t *testing.T) {
	s := New(0)
	clientID := "proxy-no-data"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	req := protocol.ProxyNewRequest{
		Name: "fail-tunnel",
	}

	if err := s.StartProxy(client, req); err == nil {
		t.Error("Startup should fail when the Data channel is missing")
	}
}

func TestPrepareProxyTunnel_PreservesHTTPDomain(t *testing.T) {
	s := New(0)
	client := &ClientConn{
		ID:      "proxy-http-domain",
		proxies: make(map[string]*ProxyTunnel),
	}

	clientConn, serverConn := net.Pipe()
	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	client.dataSession = serverSession
	t.Cleanup(func() {
		s.StopAllProxies(client)
		_ = serverSession.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	req := protocol.ProxyNewRequest{
		Name:      "http-domain-preserve",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "app.example.com",
	}

	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		t.Fatalf("prepareProxyTunnel failed: %v", err)
	}
	if tunnel.Config.Domain != req.Domain {
		t.Fatalf("Domain should remain %q, got %q", req.Domain, tunnel.Config.Domain)
	}
}

func TestFindTunnelBySelectorPrefersNameOverID(t *testing.T) {
	client := &ClientConn{proxies: map[string]*ProxyTunnel{
		"id-of-other": {Config: protocol.ProxyConfig{Name: "id-of-other", ID: "name-tunnel-id"}},
		"other":       {Config: protocol.ProxyConfig{Name: "other", ID: "id-of-other"}},
	}}

	name, tunnel, ok := findTunnelBySelector(client, "id-of-other")
	if !ok {
		t.Fatal("expected selector to match a tunnel")
	}
	if name != "id-of-other" || tunnel.Config.ID != "name-tunnel-id" {
		t.Fatalf("selector should prefer exact name matches over ID matches, got name=%q id=%q", name, tunnel.Config.ID)
	}
}

func TestActivatePreparedTunnel_HTTPDoesNotBindListener(t *testing.T) {
	s := New(0)
	client := &ClientConn{
		ID:      "proxy-http-activate",
		proxies: make(map[string]*ProxyTunnel),
	}

	clientConn, serverConn := net.Pipe()
	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	client.dataSession = serverSession
	t.Cleanup(func() {
		s.StopAllProxies(client)
		_ = serverSession.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	req := protocol.ProxyNewRequest{
		Name:       "http-no-listen",
		Type:       protocol.ProxyTypeHTTP,
		LocalIP:    "127.0.0.1",
		LocalPort:  3000,
		RemotePort: 18080,
		Domain:     "svc.example.com",
	}

	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		t.Fatalf("prepareProxyTunnel failed: %v", err)
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		t.Fatalf("activatePreparedTunnel failed: %v", err)
	}
	if tunnel.Listener != nil {
		t.Fatal("HTTP tunnels should not create a TCP listener")
	}
	if tunnel.Config.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.Config.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("After HTTP tunnel activation the state should be running/exposed, got %s/%s", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
	}
}

func TestActivatePreparedTunnel_HTTPDoesNotConflictWithSelf(t *testing.T) {
	s := New(0)
	client := &ClientConn{
		ID:      "proxy-http-self-conflict",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	clientConn, serverConn := net.Pipe()
	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	client.dataSession = serverSession
	t.Cleanup(func() {
		s.StopAllProxies(client)
		_ = serverSession.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	req := protocol.ProxyNewRequest{
		Name:      "http-self-ok",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "self.example.com",
	}

	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		t.Fatalf("prepareProxyTunnel failed: %v", err)
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		t.Fatalf("activatePreparedTunnel should not conflict with its own domain: %v", err)
	}
}

func TestHandleProxyConnRejectsDisallowedSourceBeforeOpeningStream(t *testing.T) {
	s := New(0)
	clientConn, serverConn := net.Pipe()
	serverSession, err := mux.NewServerSession(serverConn, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("server session: %v", err)
	}
	clientSession, err := mux.NewClientSession(clientConn, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("client session: %v", err)
	}
	defer mustClose(t, clientSession)
	defer mustClose(t, serverSession)
	defer mustClose(t, clientConn)
	defer mustClose(t, serverConn)

	client := &ClientConn{
		ID:          "tcp-source-policy-client",
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:           "tcp-source-policy-id",
			Name:         "tcp-source-policy",
			Type:         protocol.ProxyTypeTCP,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
			Ingress: &protocol.EndpointSpec{
				Location: protocol.EndpointLocationServer,
				Type:     protocol.IngressTypeTCPListen,
				Config: mustRawJSON(tcpListenConfigAPI{
					BindIP:             "127.0.0.1",
					Port:               18080,
					AllowedSourceCIDRs: []string{"203.0.113.0/24"},
				}),
			},
		},
	}
	activation := proxyActivationSnapshot{
		config:      tunnel.Config,
		sourceCIDRs: mustParseRuntimeCIDRs(t, []string{"203.0.113.0/24"}),
		limits:      tunnel.limits,
	}

	accepted := make(chan struct{}, 1)
	go func() {
		stream, err := clientSession.AcceptStream()
		if err == nil {
			_ = stream.Close()
			accepted <- struct{}{}
		}
	}()

	for _, tc := range []struct {
		name string
		ip   string
	}{
		{name: "external", ip: "198.51.100.10"},
		{name: "loopback", ip: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extConn, peer := net.Pipe()
			defer mustClose(t, peer)
			s.handleProxyConn(client, tunnel, nil, remoteAddrConn{
				Conn:   extConn,
				remote: &net.TCPAddr{IP: net.ParseIP(tc.ip), Port: 40000},
			}, activation)

			select {
			case <-accepted:
				t.Fatalf("disallowed TCP source %s should not open a client stream", tc.ip)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func TestHandleSOCKS5ProxyConnRejectsDisallowedSourceBeforeHandshake(t *testing.T) {
	s := New(0)
	clientConn, serverConn := net.Pipe()
	serverSession, err := mux.NewServerSession(serverConn, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("server session: %v", err)
	}
	clientSession, err := mux.NewClientSession(clientConn, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("client session: %v", err)
	}
	defer mustClose(t, clientSession)
	defer mustClose(t, serverSession)
	defer mustClose(t, clientConn)
	defer mustClose(t, serverConn)

	client := &ClientConn{
		ID:          "socks5-source-policy-client",
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:           "socks5-source-policy-id",
			Name:         "socks5-source-policy",
			Type:         protocol.ProxyTypeTCP,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
	}
	listenCfg := socks5ServerListenRuntimeConfig{
		config: protocol.SOCKS5ListenConfig{
			BindIP:             "127.0.0.1",
			Port:               1080,
			AllowedSourceCIDRs: []string{"203.0.113.0/24"},
			Auth:               protocol.SOCKS5AuthConfig{Type: protocol.SOCKS5AuthTypeNone},
		},
		sourceCIDRs:        mustParseRuntimeCIDRs(t, []string{"203.0.113.0/24"}),
		dialTimeoutSeconds: 1,
	}
	activation := proxyActivationSnapshot{
		config:      tunnel.Config,
		sourceCIDRs: append([]*net.IPNet(nil), listenCfg.sourceCIDRs...),
		limits:      tunnel.limits,
	}

	accepted := make(chan struct{}, 1)
	go func() {
		stream, err := clientSession.AcceptStream()
		if err == nil {
			_ = stream.Close()
			accepted <- struct{}{}
		}
	}()

	for _, tc := range []struct {
		name string
		ip   string
	}{
		{name: "external", ip: "198.51.100.10"},
		{name: "loopback", ip: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extConn, peer := net.Pipe()
			defer mustClose(t, peer)
			done := make(chan struct{})
			go func() {
				s.handleSOCKS5ProxyConn(client, tunnel, nil, remoteAddrConn{
					Conn:   extConn,
					remote: &net.TCPAddr{IP: net.ParseIP(tc.ip), Port: 40000},
				}, listenCfg, activation)
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("SOCKS5 handler did not return for disallowed source %s", tc.ip)
			}
			select {
			case <-accepted:
				t.Fatalf("disallowed SOCKS5 source %s should not open a client stream", tc.ip)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func TestStartProxy_DuplicateName(t *testing.T) {
	s := New(0)
	clientID := "proxy-dup"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{
		Name:       "dup-tunnel",
		RemotePort: reserveTCPPort(t),
	}

	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("The first startup should succeed: %v", err)
	}

	if err := s.StartProxy(client, req); err == nil {
		t.Error("Starting a tunnel with the same name a second time should fail with a conflict")
	}

	s.StopAllProxies(client)
	_ = cConn.Close()
	_ = sConn.Close()
}

func TestStopProxy(t *testing.T) {
	s := New(0)
	clientID := "proxy-stop"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{Name: "to-be-stopped", RemotePort: reserveTCPPort(t)}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}

	client.proxyMu.RLock()
	port := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	// Execute Stop
	if err := s.StopProxy(client, "to-be-stopped"); err != nil {
		t.Fatalf("StopProxy failed: %v", err)
	}

	// Wait a moment to ensure net.Listener close takes effect
	time.Sleep(50 * time.Millisecond)

	// Test that dialing the original port should now be refused
	_, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
	if err == nil {
		t.Errorf("The proxy has stopped, but port %d can still be connected to", port)
	}

	_ = cConn.Close()
	_ = sConn.Close()
}

func TestStopAllProxies(t *testing.T) {
	s := New(0)
	clientID := "proxy-stop-all"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	if err := s.StartProxy(client, protocol.ProxyNewRequest{Name: "t1", RemotePort: reserveTCPPort(t)}); err != nil {
		t.Fatalf("Failed to start t1: %v", err)
	}
	if err := s.StartProxy(client, protocol.ProxyNewRequest{Name: "t2", RemotePort: reserveTCPPort(t)}); err != nil {
		t.Fatalf("Failed to start t2: %v", err)
	}

	client.proxyMu.RLock()
	count := len(client.proxies)
	client.proxyMu.RUnlock()

	if count != 2 {
		t.Fatalf("Expected 2 tunnels, got %d", count)
	}

	s.StopAllProxies(client)

	client.proxyMu.RLock()
	countAf := len(client.proxies)
	client.proxyMu.RUnlock()

	if countAf != 0 {
		t.Errorf("The proxy map should be empty after StopAllProxies, got length %d", countAf)
	}
	_ = cConn.Close()
	_ = sConn.Close()
}

func TestCloseTunnelRuntimeResourcesAllowsUnboundPlaceholder(t *testing.T) {
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "placeholder",
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateOffline,
		},
	}

	closeTunnelRuntimeResources(tunnel)
	closeTunnelRuntimeResources(tunnel)
}

// ============================================================
// Complete Proxy accept loop and forwarding behavior tests
// ============================================================

func TestProxyAcceptLoop_And_HandleProxyConn(t *testing.T) {
	s := New(0)
	clientID := "forward-client"
	cc := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, cc)

	// 1. Simulate a network channel (for Yamux multiplexing)
	pipeC, pipeS := net.Pipe()
	defer mustClose(t, pipeC)
	defer mustClose(t, pipeS)

	// Initialize the Yamux server/client session
	var serverSession *yamux.Session
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverSession, _ = mux.NewServerSession(pipeS, mux.DefaultConfig())
		wg.Done()
	}()
	clientSession, _ := mux.NewClientSession(pipeC, mux.DefaultConfig())
	wg.Wait()

	cc.dataSession = serverSession
	defer mustClose(t, serverSession)
	defer mustClose(t, clientSession)

	// 2. Start proxy listening
	tunnelName := "echo-http-tunnel"
	req := protocol.ProxyNewRequest{
		Name:       tunnelName,
		Type:       protocol.ProxyTypeTCP,
		RemotePort: reserveTCPPort(t),
	}

	err := s.StartProxy(cc, req)
	if err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer func() { _ = s.StopProxy(cc, tunnelName) }()

	cc.proxyMu.RLock()
	remotePort := cc.proxies[tunnelName].Config.RemotePort
	cc.proxyMu.RUnlock()

	// 3. Start a goroutine on the client side to handle the Yamux connection
	// The received traffic is expected to be forwarded to the local HTTP test server
	localBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proxy-Target", "hit")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("hello from backend")); err != nil {
			t.Fatalf("write backend response failed: %v", err)
		}
	}))
	defer localBackend.Close()

	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			go func(stream net.Conn) {
				defer func() { _ = stream.Close() }()
				// Discard the versioned DataStreamHeader sent by the proxy (mock client parsing).
				if _, err := protocol.DecodeDataStreamHeader(stream); err != nil {
					return
				}

				// Dial the real local backend
				backendConn, err := net.Dial("tcp", localBackend.Listener.Addr().String())
				if err != nil {
					return
				}
				defer func() { _ = backendConn.Close() }()
				mux.Relay(stream, backendConn)
			}(stream)
		}
	}()

	// 4. Send a request from a real network client (connect to the Server-assigned RemotePort)
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("Failed to request the proxy address: %v", err)
	}
	defer mustClose(t, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Proxy-Target") != "hit" {
		t.Errorf("Did not correctly reach the backend HTTP server")
	}
}

// ============================================================
// Concurrent port contention tests
// ============================================================

func TestStartProxy_ConcurrentPortConflict(t *testing.T) {
	s := New(0)

	// Reserve a fixed port for contention first
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to preallocate port: %v", err)
	}
	contestedPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // Release the port so two clients can race for it

	// Create two clients, each with its own data session
	makeClient := func(id string) *ClientConn {
		client := &ClientConn{
			ID:      id,
			proxies: make(map[string]*ProxyTunnel),
		}
		s.clients.Store(id, client)
		cConn, sConn := net.Pipe()
		session, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
		client.dataSession = session
		t.Cleanup(func() {
			_ = cConn.Close()
			_ = sConn.Close()
			_ = session.Close()
		})
		return client
	}

	client1 := makeClient("race-client-1")
	client2 := makeClient("race-client-2")

	// Start proxies concurrently to race for the same port
	var wg sync.WaitGroup
	results := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		results <- s.StartProxy(client1, protocol.ProxyNewRequest{
			Name:       "race-tunnel",
			RemotePort: contestedPort,
		})
	}()
	go func() {
		defer wg.Done()
		results <- s.StartProxy(client2, protocol.ProxyNewRequest{
			Name:       "race-tunnel",
			RemotePort: contestedPort,
		})
	}()

	wg.Wait()
	close(results)

	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}

	if successes != 1 {
		t.Errorf("Only 1 success is expected when racing for the same port, got %d", successes)
	}
	if failures != 1 {
		t.Errorf("Only 1 failure is expected when racing for the same port, got %d", failures)
	}

	// Cleanup
	s.StopAllProxies(client1)
	s.StopAllProxies(client2)
}

func TestActivatePreparedHTTPRuntimeReplacesClosedActivationToken(t *testing.T) {
	s := New(0)
	_, serverSession := newTestClientRelayDataSession(t)
	client := &ClientConn{ID: "http-reopen-client", proxies: make(map[string]*ProxyTunnel), dataSession: serverSession}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:              "http-reopen-id",
			Name:            "http-reopen",
			Revision:        2,
			Type:            protocol.ProxyTypeHTTP,
			Domain:          "http-reopen.example.com",
			ClientID:        client.ID,
			DesiredState:    protocol.ProxyDesiredStateRunning,
			RuntimeState:    protocol.ProxyRuntimeStateExposed,
			ActualTransport: protocol.ActualTransportServerRelay,
		},
		done: make(chan struct{}),
	}
	tunnel.runtime.Revision = 2
	client.proxies[tunnel.Config.Name] = tunnel
	closedDone := tunnel.done
	closeTunnelRuntimeResources(tunnel)

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		t.Fatalf("reactivate HTTP runtime: %v", err)
	}
	client.proxyMu.RLock()
	currentDone := tunnel.done
	config := tunnel.Config
	client.proxyMu.RUnlock()
	if currentDone == closedDone {
		t.Fatal("HTTP reactivation must replace the closed activation token")
	}
	select {
	case <-currentDone:
		t.Fatal("HTTP reactivation token should be open")
	default:
	}
	if !isTunnelExposed(config) {
		t.Fatalf("HTTP reactivation should be exposed: %+v", config)
	}
}

func TestActivatePreparedTunnelDoesNotPublishForExpiredGeneration(t *testing.T) {
	s := New(0)
	_, serverSession := newTestClientRelayDataSession(t)
	remotePort := reserveTCPPort(t)
	client := &ClientConn{
		ID:          "expired-generation-client",
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  9,
		state:       clientStateLive,
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:           "expired-generation-id",
			Name:         "expired-generation",
			Revision:     3,
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   remotePort,
			BindIP:       "127.0.0.1",
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStatePending,
		},
		done: make(chan struct{}),
	}
	tunnel.runtime.Revision = 3
	client.proxies[tunnel.Config.Name] = tunnel

	if err := s.activatePreparedTunnel(client, tunnel); err == nil {
		t.Fatal("expired client generation should prevent runtime publication")
	}
	client.proxyMu.RLock()
	listener := tunnel.Listener
	runtimeState := tunnel.Config.RuntimeState
	client.proxyMu.RUnlock()
	if listener != nil || runtimeState != protocol.ProxyRuntimeStatePending {
		t.Fatalf("expired generation published runtime: listener=%v state=%s", listener, runtimeState)
	}
	probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("expired generation leaked listener on port %d: %v", remotePort, err)
	}
	_ = probe.Close()
}

func TestActivatePreparedUDPTunnelDoesNotPublishForExpiredGeneration(t *testing.T) {
	s := New(0)
	_, serverSession := newTestClientRelayDataSession(t)
	remotePort := reserveUDPPort(t)
	client := &ClientConn{
		ID:          "expired-udp-client",
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  10,
		state:       clientStateLive,
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:           "expired-udp-id",
			Name:         "expired-udp",
			Revision:     3,
			Type:         protocol.ProxyTypeUDP,
			LocalIP:      "127.0.0.1",
			LocalPort:    5353,
			RemotePort:   remotePort,
			BindIP:       "127.0.0.1",
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStatePending,
		},
		done: make(chan struct{}),
	}
	tunnel.runtime.Revision = 3
	client.proxies[tunnel.Config.Name] = tunnel

	if err := s.activatePreparedTunnel(client, tunnel); err == nil {
		t.Fatal("expired client generation should prevent UDP runtime publication")
	}
	client.proxyMu.RLock()
	state := tunnel.UDPState
	runtimeState := tunnel.Config.RuntimeState
	client.proxyMu.RUnlock()
	if state != nil || runtimeState != protocol.ProxyRuntimeStatePending {
		t.Fatalf("expired generation published UDP runtime: state=%v runtime=%s", state, runtimeState)
	}
	probe, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("expired generation leaked UDP listener on port %d: %v", remotePort, err)
	}
	_ = probe.Close()
}

func TestActivatePreparedSOCKS5TunnelDoesNotPublishForExpiredGeneration(t *testing.T) {
	s := New(0)
	_, serverSession := newTestClientRelayDataSession(t)
	remotePort := reserveTCPPort(t)
	client := &ClientConn{
		ID:          "expired-socks5-client",
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  11,
		state:       clientStateLive,
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:           "expired-socks5-id",
			Name:         "expired-socks5",
			Revision:     4,
			Type:         protocol.ProxyTypeTCP,
			RemotePort:   remotePort,
			BindIP:       "127.0.0.1",
			ClientID:     client.ID,
			Topology:     protocol.TunnelTopologyServerExpose,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStatePending,
			Ingress: &protocol.EndpointSpec{
				Location: protocol.EndpointLocationServer,
				Type:     protocol.IngressTypeSOCKS5Listen,
				Config: mustRawJSON(protocol.SOCKS5ListenConfig{
					BindIP:             "127.0.0.1",
					Port:               remotePort,
					AllowedSourceCIDRs: allowAllSourceCIDRs(),
					Auth:               protocol.SOCKS5AuthConfig{Type: protocol.SOCKS5AuthTypeNone},
				}),
			},
			Target: &protocol.EndpointSpec{
				Location: protocol.EndpointLocationClient,
				ClientID: client.ID,
				Type:     protocol.TargetTypeSOCKS5ConnectHandler,
				Config: mustRawJSON(protocol.SOCKS5ConnectHandlerConfig{
					AllowedTargetCIDRs: []string{"0.0.0.0/0", "::/0"},
					DialTimeoutSeconds: 5,
				}),
			},
		},
		done: make(chan struct{}),
	}
	tunnel.runtime.Revision = 4
	client.proxies[tunnel.Config.Name] = tunnel

	if err := s.activatePreparedTunnel(client, tunnel); err == nil {
		t.Fatal("expired client generation should prevent SOCKS5 runtime publication")
	}
	client.proxyMu.RLock()
	listener := tunnel.Listener
	runtimeState := tunnel.Config.RuntimeState
	client.proxyMu.RUnlock()
	if listener != nil || runtimeState != protocol.ProxyRuntimeStatePending {
		t.Fatalf("expired generation published SOCKS5 runtime: listener=%v state=%s", listener, runtimeState)
	}
	probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("expired generation leaked SOCKS5 listener on port %d: %v", remotePort, err)
	}
	_ = probe.Close()
}

func TestActivatePreparedHTTPTunnelDoesNotPublishForExpiredGeneration(t *testing.T) {
	s := New(0)
	_, serverSession := newTestClientRelayDataSession(t)
	client := &ClientConn{
		ID:          "expired-http-client",
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  12,
		state:       clientStateLive,
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:           "expired-http-id",
			Name:         "expired-http",
			Revision:     5,
			Type:         protocol.ProxyTypeHTTP,
			Domain:       "expired-http.example.com",
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStatePending,
		},
		done: make(chan struct{}),
	}
	tunnel.runtime.Revision = 5
	client.proxies[tunnel.Config.Name] = tunnel
	originalDone := tunnel.done

	if err := s.activatePreparedTunnel(client, tunnel); err == nil {
		t.Fatal("expired client generation should prevent HTTP runtime publication")
	}
	client.proxyMu.RLock()
	currentDone := tunnel.done
	runtimeState := tunnel.Config.RuntimeState
	client.proxyMu.RUnlock()
	if currentDone != originalDone || runtimeState != protocol.ProxyRuntimeStatePending {
		t.Fatalf("expired generation published HTTP runtime: token_changed=%v state=%s", currentDone != originalDone, runtimeState)
	}
}

type scriptedListener struct {
	addr      net.Addr
	acceptCh  chan error
	closeOnce sync.Once
}

func newScriptedListener(t *testing.T) *scriptedListener {
	t.Helper()
	return &scriptedListener{
		addr:     &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: reserveTCPPort(t)},
		acceptCh: make(chan error, 1),
	}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	err, ok := <-l.acceptCh
	if !ok {
		return nil, net.ErrClosed
	}
	return nil, err
}

func (l *scriptedListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.acceptCh)
	})
	return nil
}

func (l *scriptedListener) Addr() net.Addr { return l.addr }

func storedTunnelFromRuntimeForTest(client *ClientConn, tunnel *ProxyTunnel) StoredTunnel {
	stored := StoredTunnel{
		ProxyNewRequest: tunnel.Config.ToProxyNewRequest(),
		ClientID:        client.ID,
		Hostname:        client.GetInfo().Hostname,
		Binding:         TunnelBindingClientID,
		Revision:        tunnel.Config.Revision,
		CreatedAt:       tunnel.Config.CreatedAt,
	}
	stored.DesiredState = tunnel.Config.DesiredState
	stored.RuntimeState = tunnel.Config.RuntimeState
	stored.Error = tunnel.Config.Error
	_ = stored.normalize()
	return stored
}

func TestProxyAcceptLoop_UnexpectedAcceptFailureMarksTunnelError(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:      "accept-error-client",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:            "accept-error-tunnel-id",
			Name:          "accept-error-tunnel",
			Revision:      1,
			Type:          protocol.ProxyTypeTCP,
			LocalIP:       "127.0.0.1",
			LocalPort:     8080,
			RemotePort:    listener.addr.(*net.TCPAddr).Port,
			ClientID:      client.ID,
			OwnerClientID: client.ID,
			DesiredState:  protocol.ProxyDesiredStateRunning,
			RuntimeState:  protocol.ProxyRuntimeStateExposed,
			Ingress: &protocol.EndpointSpec{
				Type: protocol.IngressTypeTCPListen,
				Config: mustRawJSON(tcpListenConfigAPI{
					BindIP:             "0.0.0.0",
					Port:               listener.addr.(*net.TCPAddr).Port,
					AllowedSourceCIDRs: allowAllSourceCIDRs(),
				}),
			},
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntimeForTest(client, tunnel))

	listener.acceptCh <- errors.New("boom")
	s.proxyAcceptLoop(client, tunnel, listener, tunnel.done, proxyActivationSnapshot{config: tunnel.Config})

	client.proxyMu.RLock()
	got := client.proxies[tunnel.Config.Name].Config
	currentListener := client.proxies[tunnel.Config.Name].Listener
	client.proxyMu.RUnlock()

	if got.DesiredState != protocol.ProxyDesiredStateRunning || got.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("After an unexpected Accept failure, the state should be running/error, got %s/%s", got.DesiredState, got.RuntimeState)
	}
	if got.Error == "" {
		t.Fatal("The error should not be empty after an unexpected Accept failure")
	}
	if currentListener != nil {
		t.Fatal("The listener should have been cleaned up after an unexpected Accept failure")
	}

	stored, ok := s.store.GetTunnel(client.ID, tunnel.Config.Name)
	if !ok {
		t.Fatal("The tunnel should still exist in the store")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("The store state should be running/error, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Error == "" {
		t.Fatal("The store error should not be empty")
	}
}

func TestProxyAcceptLoop_ClosedDoneDoesNotMarkTunnelError(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:      "accept-shutdown-client",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:            "accept-shutdown-tunnel-id",
			Name:          "accept-shutdown-tunnel",
			Revision:      1,
			Type:          protocol.ProxyTypeTCP,
			LocalIP:       "127.0.0.1",
			LocalPort:     8080,
			RemotePort:    listener.addr.(*net.TCPAddr).Port,
			ClientID:      client.ID,
			OwnerClientID: client.ID,
			DesiredState:  protocol.ProxyDesiredStateRunning,
			RuntimeState:  protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntimeForTest(client, tunnel))

	close(tunnel.done)
	listener.acceptCh <- net.ErrClosed
	s.proxyAcceptLoop(client, tunnel, listener, tunnel.done, proxyActivationSnapshot{config: tunnel.Config})

	client.proxyMu.RLock()
	got := client.proxies[tunnel.Config.Name].Config
	client.proxyMu.RUnlock()

	if got.DesiredState != protocol.ProxyDesiredStateRunning || got.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("A normal shutdown should not downgrade the state to error, got %s/%s", got.DesiredState, got.RuntimeState)
	}

	stored, ok := s.store.GetTunnel(client.ID, tunnel.Config.Name)
	if !ok {
		t.Fatal("The tunnel should still exist in the store")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("After a normal shutdown, the store state should remain running/exposed, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
}

func TestMarkTCPProxyRuntimeErrorIfCurrent_StaleListenerDoesNotDemote(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:      "stale-listener-client",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	oldListener := newScriptedListener(t)
	currentListener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:            "stale-listener-tunnel-id",
			Name:          "stale-listener-tunnel",
			Revision:      1,
			Type:          protocol.ProxyTypeTCP,
			LocalIP:       "127.0.0.1",
			LocalPort:     8080,
			RemotePort:    currentListener.addr.(*net.TCPAddr).Port,
			ClientID:      client.ID,
			OwnerClientID: client.ID,
			DesiredState:  protocol.ProxyDesiredStateRunning,
			RuntimeState:  protocol.ProxyRuntimeStateExposed,
		},
		Listener: currentListener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntimeForTest(client, tunnel))

	s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel.Config.Name, tunnel, oldListener, "stale accept failure")

	client.proxyMu.RLock()
	got := client.proxies[tunnel.Config.Name].Config
	gotListener := client.proxies[tunnel.Config.Name].Listener
	client.proxyMu.RUnlock()

	if got.DesiredState != protocol.ProxyDesiredStateRunning || got.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("A stale listener should not downgrade the state to error, got %s/%s", got.DesiredState, got.RuntimeState)
	}
	if gotListener != currentListener {
		t.Fatal("A stale listener should not clean up the current listener")
	}

	stored, ok := s.store.GetTunnel(client.ID, tunnel.Config.Name)
	if !ok {
		t.Fatal("The tunnel should still exist in the store")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("After a stale listener event, the store state should remain running/exposed, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
}

func TestMarkTCPProxyRuntimeErrorIfCurrent_StaleRevisionDoesNotDemoteStore(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	client := &ClientConn{ID: "stale-tcp-revision-client", proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(client.ID, client)

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:            "stale-tcp-revision-id",
			Name:          "stale-tcp-revision",
			Revision:      1,
			Type:          protocol.ProxyTypeTCP,
			LocalIP:       "127.0.0.1",
			LocalPort:     8080,
			RemotePort:    listener.addr.(*net.TCPAddr).Port,
			ClientID:      client.ID,
			OwnerClientID: client.ID,
			Topology:      protocol.TunnelTopologyServerExpose,
			DesiredState:  protocol.ProxyDesiredStateRunning,
			RuntimeState:  protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel
	stored := storedTunnelFromRuntimeForTest(client, tunnel)
	mustAddStableTunnel(t, s.store, stored)

	next := stored
	next.Revision++
	next.RuntimeState = protocol.ProxyRuntimeStateExposed
	next.UpdatedAt = time.Now().UTC()
	if err := s.store.ReplaceTunnelByID(client.ID, stored.ID, stored.Revision, next); err != nil {
		t.Fatalf("advance stored tunnel revision: %v", err)
	}
	s.unifiedRuntime.recordServerIssue(next.ID, next.Revision, protocol.TunnelIssue{
		Code:    "current-revision-issue",
		Scope:   "server",
		Message: "keep current issue",
	})

	s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel.Config.Name, tunnel, listener, "late old listener failure")

	reloaded, err := s.store.GetTunnelByIDE(client.ID, stored.ID)
	if err != nil {
		t.Fatalf("reload tunnel: %v", err)
	}
	if reloaded.Revision != next.Revision || reloaded.RuntimeState != protocol.ProxyRuntimeStateExposed || reloaded.Error != "" {
		t.Fatalf("old TCP runtime changed new revision state: %+v", reloaded)
	}
	issues := s.unifiedRuntime.issuesForStoredTunnel(reloaded, true)
	if len(issues) != 1 || issues[0].Code != "current-revision-issue" {
		t.Fatalf("old TCP runtime changed new revision issues: %+v", issues)
	}
}

func TestMarkTCPProxyRuntimeErrorIfCurrent_LegacyCleanupDoesNotDependOnStoreRow(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	clientWS, serverWS := newTestWebSocketPair(t)
	defer mustClose(t, clientWS)
	defer mustClose(t, serverWS)
	client := &ClientConn{ID: "legacy-runtime-error-client", conn: serverWS, proxies: make(map[string]*ProxyTunnel)}

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:           "legacy-runtime-error-id",
			Name:         "legacy-runtime-error",
			Revision:     1,
			Type:         protocol.ProxyTypeTCP,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel.Config.Name, tunnel, listener, "legacy listener failed")

	msg := readControlMessageOfType(t, clientWS, protocol.MsgTypeProxyClose)
	var closeReq protocol.ProxyCloseRequest
	if err := msg.ParsePayload(&closeReq); err != nil {
		t.Fatalf("parse legacy proxy close: %v", err)
	}
	if closeReq.Name != tunnel.Config.Name || closeReq.Reason != "runtime_error" {
		t.Fatalf("legacy runtime cleanup mismatch: %+v", closeReq)
	}
}

func TestMarkTCPProxyRuntimeErrorIfCurrent_UnprovisionsWhenStoreWriteFails(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	clientWS, serverWS := newTestWebSocketPair(t)
	defer mustClose(t, clientWS)
	defer mustClose(t, serverWS)
	client := &ClientConn{ID: "unified-runtime-error-client", conn: serverWS, proxies: make(map[string]*ProxyTunnel)}

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:            "unified-runtime-error-id",
			Name:          "unified-runtime-error",
			Revision:      3,
			Type:          protocol.ProxyTypeTCP,
			ClientID:      client.ID,
			OwnerClientID: client.ID,
			Topology:      protocol.TunnelTopologyServerExpose,
			DesiredState:  protocol.ProxyDesiredStateRunning,
			RuntimeState:  protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel
	mustAddStableTunnel(t, s.store, storedTunnelFromRuntimeForTest(client, tunnel))
	s.store.failSaveErr = errors.New("injected runtime error save failure")
	s.store.failSaveCount = 1

	s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel.Config.Name, tunnel, listener, "listener failed")

	msg := readControlMessageOfType(t, clientWS, protocol.MsgTypeTunnelUnprovision)
	var unprovision protocol.TunnelUnprovisionRequest
	if err := msg.ParsePayload(&unprovision); err != nil {
		t.Fatalf("parse unified tunnel unprovision: %v", err)
	}
	if unprovision.TunnelID != tunnel.Config.ID || unprovision.Revision != tunnel.Config.Revision || unprovision.Reason != "runtime_error" {
		t.Fatalf("unified runtime cleanup mismatch: %+v", unprovision)
	}
}

func TestHandleProxyConn_OpenStreamFailureMarksTunnelError(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:      "open-stream-error-client",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			ID:            "open-stream-error-tunnel-id",
			Name:          "open-stream-error-tunnel",
			Revision:      1,
			Type:          protocol.ProxyTypeTCP,
			LocalIP:       "127.0.0.1",
			LocalPort:     8080,
			RemotePort:    listener.addr.(*net.TCPAddr).Port,
			ClientID:      client.ID,
			OwnerClientID: client.ID,
			DesiredState:  protocol.ProxyDesiredStateRunning,
			RuntimeState:  protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntimeForTest(client, tunnel))

	peerConn, extConn := net.Pipe()
	defer mustClose(t, peerConn)

	done := make(chan struct{})
	activation := proxyActivationSnapshot{
		config:      tunnel.Config,
		sourceCIDRs: mustParseRuntimeCIDRs(t, allowAllSourceCIDRs()),
		limits:      tunnel.limits,
	}
	go func() {
		s.handleProxyConn(client, tunnel, listener, remoteAddrConn{
			Conn:   extConn,
			remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321},
		}, activation)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleProxyConn did not exit promptly after OpenStream failure")
	}

	client.proxyMu.RLock()
	got := client.proxies[tunnel.Config.Name].Config
	currentListener := client.proxies[tunnel.Config.Name].Listener
	client.proxyMu.RUnlock()

	if got.DesiredState != protocol.ProxyDesiredStateRunning || got.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("Expected running/error state after OpenStream failure, got %s/%s", got.DesiredState, got.RuntimeState)
	}
	if !strings.Contains(got.Error, "data channel not established") {
		t.Fatalf("Expected the error to contain the data channel reason after OpenStream failure, got %q", got.Error)
	}
	if currentListener != nil {
		t.Fatal("Expected the listener to be cleaned up after OpenStream failure")
	}

	stored, ok := s.store.GetTunnel(client.ID, tunnel.Config.Name)
	if !ok {
		t.Fatal("The tunnel should still exist in the store")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("Expected store state running/error, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if !strings.Contains(stored.Error, "data channel not established") {
		t.Fatalf("Expected the store error to contain the data channel reason, got %q", stored.Error)
	}
}
