package server

import (
	"fmt"
	"net"
	"sync"
	"testing"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

func newLiveConsoleRaceClient(t *testing.T, s *Server, clientID string) (*ClientConn, func()) {
	t.Helper()

	client := &ClientConn{
		ID:         clientID,
		RemoteAddr: "127.0.0.1:12345",
		state:      clientStateLive,
		generation: 1,
		proxies:    make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	clientConn, serverConn := net.Pipe()
	serverSession, err := mux.NewServerSession(serverConn, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create server session: %v", err)
	}
	client.dataSession = serverSession

	cleanup := func() {
		s.StopAllProxies(client)
		_ = serverSession.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		s.clients.Delete(clientID)
	}
	return client, cleanup
}

func TestCollectClientViews_ProxyConfigsSnapshotAvoidsDirectMapRace(t *testing.T) {
	s := New(0)
	client, cleanup := newLiveConsoleRaceClient(t, s, "console-map-race-client")
	defer cleanup()

	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 4000; i++ {
			name := fmt.Sprintf("map-race-%d", i%8)
			client.proxyMu.Lock()
			if i%3 == 0 {
				delete(client.proxies, name)
			} else {
				client.proxies[name] = &ProxyTunnel{
					Config: protocol.ProxyConfig{
						Name:         name,
						Type:         protocol.ProxyTypeTCP,
						DesiredState: protocol.ProxyDesiredStateRunning,
						RuntimeState: protocol.ProxyRuntimeStateExposed,
					},
					done: make(chan struct{}),
				}
			}
			client.proxyMu.Unlock()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 4000; i++ {
			_ = s.collectClientViews()
		}
	}()

	close(start)
	wg.Wait()
}

func TestCollectClientViews_HTTPActivationSnapshotRace(t *testing.T) {
	s := New(0)
	client, cleanup := newLiveConsoleRaceClient(t, s, "console-http-race-client")
	defer cleanup()

	tunnel, err := s.prepareProxyTunnel(client, protocol.ProxyNewRequest{
		Name:      "http-race",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "http-race.example.com",
	}, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		t.Fatalf("prepareProxyTunnel failed: %v", err)
	}

	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 1000; i++ {
			if _, err := s.stageTunnelPending(client, tunnel.Config.Name); err != nil {
				errCh <- fmt.Errorf("stageTunnelPending failed: %w", err)
				return
			}
			if err := s.activatePreparedTunnel(client, tunnel); err != nil {
				errCh <- fmt.Errorf("activatePreparedTunnel failed: %w", err)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 1000; i++ {
			_ = s.collectClientViews()
		}
	}()

	close(start)
	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}

func TestCollectClientViews_UDPActivationSnapshotRace(t *testing.T) {
	s := New(0)
	client, cleanup := newLiveConsoleRaceClient(t, s, "console-udp-race-client")
	defer cleanup()

	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 200; i++ {
			name := fmt.Sprintf("udp-race-%d", i)
			tunnel := &ProxyTunnel{
				Config: protocol.ProxyConfig{
					Name:         name,
					Type:         protocol.ProxyTypeUDP,
					LocalIP:      "127.0.0.1",
					LocalPort:    5353,
					ClientID:     client.ID,
					DesiredState: protocol.ProxyDesiredStateRunning,
					RuntimeState: protocol.ProxyRuntimeStatePending,
				},
				done: make(chan struct{}),
			}
			client.proxyMu.Lock()
			client.proxies[name] = tunnel
			client.proxyMu.Unlock()

			runtime, err := s.bindUDPProxyRuntime(tunnel)
			if err != nil {
				errCh <- fmt.Errorf("bindUDPProxyRuntime failed: %w", err)
				return
			}
			if _, _, err := s.publishUDPProxyRuntime(client, tunnel, runtime); err != nil {
				errCh <- fmt.Errorf("publishUDPProxyRuntime failed: %w", err)
				return
			}
			s.removeTunnelRuntime(client, name)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			_ = s.collectClientViews()
		}
	}()

	close(start)
	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}

func TestPublishUDPProxyRuntime_StaleActivationClosesFreshState(t *testing.T) {
	s := New(0)
	client, cleanup := newLiveConsoleRaceClient(t, s, "udp-stale-activation-client")
	defer cleanup()

	tunnel, err := s.prepareProxyTunnel(client, protocol.ProxyNewRequest{
		Name:       "udp-stale",
		Type:       protocol.ProxyTypeUDP,
		LocalIP:    "127.0.0.1",
		LocalPort:  5353,
		RemotePort: reserveUDPPort(t),
	}, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		t.Fatalf("prepareProxyTunnel failed: %v", err)
	}

	runtime, err := s.bindUDPProxyRuntime(tunnel)
	if err != nil {
		t.Fatalf("bindUDPProxyRuntime failed: %v", err)
	}

	replacement := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         tunnel.Config.Name,
			Type:         protocol.ProxyTypeUDP,
			LocalIP:      tunnel.Config.LocalIP,
			LocalPort:    tunnel.Config.LocalPort,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStatePending,
		},
		done: make(chan struct{}),
	}

	client.proxyMu.Lock()
	client.proxies[tunnel.Config.Name] = replacement
	client.proxyMu.Unlock()

	if _, _, err := s.publishUDPProxyRuntime(client, tunnel, runtime); err == nil {
		t.Fatal("publishUDPProxyRuntime should reject stale activation")
	}

	select {
	case <-runtime.state.done:
	default:
		t.Fatal("stale activation should close fresh UDP state")
	}

	if _, err := runtime.state.packetConn.WriteTo([]byte("probe"), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: runtime.actualPort}); err == nil {
		t.Fatal("stale activation should close the fresh packet conn")
	}

	client.proxyMu.RLock()
	current := client.proxies[tunnel.Config.Name]
	client.proxyMu.RUnlock()

	if current != replacement {
		t.Fatal("stale activation should not replace the current tunnel entry")
	}
	if current.UDPState != nil {
		t.Fatal("stale activation should not publish UDP state")
	}
	if current.Config.RemotePort != 0 {
		t.Fatalf("stale activation should not publish a remote port, got %d", current.Config.RemotePort)
	}
	if current.Config.DesiredState != protocol.ProxyDesiredStateRunning || current.Config.RuntimeState != protocol.ProxyRuntimeStatePending {
		t.Fatalf("stale activation should preserve running/pending state, got %s/%s", current.Config.DesiredState, current.Config.RuntimeState)
	}
}
