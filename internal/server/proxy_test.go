package server

import (
	"errors"
	"fmt"
	"io"
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
				// Discard the 2-byte length and Name header sent by the proxy (mock client parsing)
				var ln [2]byte
				if _, err := io.ReadFull(stream, ln[:]); err != nil {
					return
				}
				nameLen := int(ln[0])<<8 | int(ln[1])
				nameBuf := make([]byte, nameLen)
				if _, err := io.ReadFull(stream, nameBuf); err != nil {
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
			Name:         "accept-error-tunnel",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   listener.addr.(*net.TCPAddr).Port,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntime(client, tunnel))

	listener.acceptCh <- errors.New("boom")
	s.proxyAcceptLoop(client, tunnel, listener, tunnel.done)

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
			Name:         "accept-shutdown-tunnel",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   listener.addr.(*net.TCPAddr).Port,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntime(client, tunnel))

	close(tunnel.done)
	listener.acceptCh <- net.ErrClosed
	s.proxyAcceptLoop(client, tunnel, listener, tunnel.done)

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
			Name:         "stale-listener-tunnel",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   currentListener.addr.(*net.TCPAddr).Port,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: currentListener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntime(client, tunnel))

	s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel, oldListener, "stale accept failure")

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
			Name:         "open-stream-error-tunnel",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   listener.addr.(*net.TCPAddr).Port,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntime(client, tunnel))

	peerConn, extConn := net.Pipe()
	defer mustClose(t, peerConn)

	done := make(chan struct{})
	go func() {
		s.handleProxyConn(client, tunnel, listener, extConn)
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
