package server

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

type scriptedPacketConn struct {
	readFrom  func([]byte) (int, net.Addr, error)
	closeFunc func()
	closeOnce sync.Once
}

func (c *scriptedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if c.readFrom == nil {
		return 0, nil, net.ErrClosed
	}
	return c.readFrom(p)
}

func (c *scriptedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	return len(p), nil
}

func (c *scriptedPacketConn) Close() error {
	c.closeOnce.Do(func() {
		if c.closeFunc != nil {
			c.closeFunc()
		}
	})
	return nil
}

func (c *scriptedPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
}

func (c *scriptedPacketConn) SetDeadline(time.Time) error {
	return nil
}

func (c *scriptedPacketConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *scriptedPacketConn) SetWriteDeadline(time.Time) error {
	return nil
}

func setupManagedUDPErrorTestTunnel(t *testing.T, tunnelName string) (*Server, *ClientConn, *ProxyTunnel, *TunnelStore) {
	t.Helper()

	s := New(0)
	store, err := NewTunnelStore(fmt.Sprintf("%s/%s.json", t.TempDir(), tunnelName))
	if err != nil {
		t.Fatalf("Failed to create TunnelStore: %v", err)
	}
	s.store = store

	client := &ClientConn{
		ID:      "udp-managed-client",
		Info:    protocol.ClientInfo{Hostname: "udp-host"},
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:       tunnelName,
			Type:       protocol.ProxyTypeUDP,
			LocalIP:    "127.0.0.1",
			LocalPort:  5353,
			RemotePort: reserveUDPPort(t),
		},
		ClientID: client.ID,
		Hostname: client.Info.Hostname,
		Binding:  TunnelBindingClientID,
	}
	setStoredTunnelStates(&stored, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
	if err := store.AddTunnel(stored); err != nil {
		t.Fatalf("Failed to add persistent tunnel: %v", err)
	}

	tunnel := &ProxyTunnel{
		Config: storedTunnelToProxyConfig(stored),
		done:   make(chan struct{}),
	}
	client.proxies[tunnelName] = tunnel

	return s, client, tunnel, store
}

func attachUDPTestDataSessionSink(t *testing.T, client *ClientConn) func() {
	t.Helper()

	pipeC, pipeS := net.Pipe()

	var serverSession *yamux.Session
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverSession, _ = mux.NewServerSession(pipeS, mux.DefaultConfig())
		wg.Done()
	}()
	clientSession, err := mux.NewClientSession(pipeC, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create client yamux session: %v", err)
	}
	wg.Wait()

	client.dataSession = serverSession

	stop := make(chan struct{})
	go func() {
		for {
			stream, err := clientSession.AcceptStream()
			if err != nil {
				return
			}
			go func(s *yamux.Stream) {
				var lenBuf [2]byte
				if _, err := io.ReadFull(s, lenBuf[:]); err != nil {
					s.Close()
					return
				}
				nameLen := int(lenBuf[0])<<8 | int(lenBuf[1])
				nameBuf := make([]byte, nameLen)
				if _, err := io.ReadFull(s, nameBuf); err != nil {
					s.Close()
					return
				}
				if _, err := mux.ReadUDPFrame(s); err != nil {
					s.Close()
					return
				}
				<-stop
				s.Close()
			}(stream)
		}
	}()

	return func() {
		close(stop)
		clientSession.Close()
		serverSession.Close()
		pipeC.Close()
		pipeS.Close()
	}
}

func reserveUDPPort(t *testing.T) int {
	t.Helper()

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to reserve UDP port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	if err := conn.Close(); err != nil {
		t.Fatalf("Failed to close reserved UDP port: %v", err)
	}
	return port
}

// ============================================================
// Server-side UDP proxy tests
// ============================================================

func TestStartProxy_UDP_Success(t *testing.T) {
	s := New(0)
	clientID := "udp-proxy-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	// Build the yamux session
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{
		Name:       "udp-test-tunnel",
		Type:       protocol.ProxyTypeUDP,
		LocalIP:    "127.0.0.1",
		LocalPort:  5353,
		RemotePort: reserveUDPPort(t),
	}

	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy UDP failed: %v", err)
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
	if tunnel.Config.Type != protocol.ProxyTypeUDP {
		t.Errorf("Expected type udp, got %s", tunnel.Config.Type)
	}
	if tunnel.UDPState == nil {
		t.Fatal("The UDPState of a UDP tunnel should not be nil")
	}
	if tunnel.Listener != nil {
		t.Error("A UDP tunnel should not have a TCP listener")
	}

	// Verify the UDP port is actually listening: sending a UDP packet should not error
	testConn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", tunnel.Config.RemotePort), 100*time.Millisecond)
	if err != nil {
		t.Errorf("Unable to connect to the UDP port: %v", err)
	} else {
		testConn.Write([]byte("probe"))
		testConn.Close()
	}

	// Cleanup
	s.StopAllProxies(client)
	cConn.Close()
	sConn.Close()
}

func TestStopProxy_UDP(t *testing.T) {
	s := New(0)
	clientID := "udp-stop-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{
		Name:       "udp-to-stop",
		Type:       protocol.ProxyTypeUDP,
		RemotePort: reserveUDPPort(t),
	}
	s.StartProxy(client, req)

	client.proxyMu.RLock()
	tunnel := client.proxies[req.Name]
	port := tunnel.Config.RemotePort
	client.proxyMu.RUnlock()

	// Stop
	if err := s.StopProxy(client, req.Name); err != nil {
		t.Fatalf("StopProxy UDP failed: %v", err)
	}

	// Wait for the port to be released
	time.Sleep(50 * time.Millisecond)

	// UDP port is closed: re-listening with ListenPacket should succeed (meaning the old one was released)
	probe, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Errorf("UDP port %d was not released: %v", port, err)
	} else {
		probe.Close()
	}

	cConn.Close()
	sConn.Close()
}

func TestCloseAndReopenProxyRuntime_UDP(t *testing.T) {
	s := New(0)
	clientID := "udp-stop-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{
		Name:       "udp-stop-test",
		Type:       protocol.ProxyTypeUDP,
		RemotePort: reserveUDPPort(t),
	}
	s.StartProxy(client, req)

	client.proxyMu.RLock()
	port := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	// Close runtime resources without changing business state.
	if err := s.CloseProxyRuntime(client, req.Name); err != nil {
		t.Fatalf("CloseProxyRuntime UDP failed: %v", err)
	}

	client.proxyMu.RLock()
	desiredState := client.proxies[req.Name].Config.DesiredState
	runtimeState := client.proxies[req.Name].Config.RuntimeState
	client.proxyMu.RUnlock()
	if desiredState != protocol.ProxyDesiredStateRunning || runtimeState != protocol.ProxyRuntimeStateExposed {
		t.Errorf("CloseProxyRuntime only closes runtime resources; the state should remain running/exposed, got %s/%s", desiredState, runtimeState)
	}

	// Wait for the port to be released
	time.Sleep(50 * time.Millisecond)

	// Reopen runtime resources.
	if err := s.ReopenProxyRuntime(client, req.Name); err != nil {
		t.Fatalf("ReopenProxyRuntime UDP failed: %v", err)
	}

	client.proxyMu.RLock()
	desiredState = client.proxies[req.Name].Config.DesiredState
	runtimeState = client.proxies[req.Name].Config.RuntimeState
	newPort := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	if desiredState != protocol.ProxyDesiredStateRunning || runtimeState != protocol.ProxyRuntimeStateExposed {
		t.Errorf("After resuming, expected running/exposed, got %s/%s", desiredState, runtimeState)
	}
	if newPort != port {
		t.Errorf("After resuming, expected port %d, got %d", port, newPort)
	}

	s.StopAllProxies(client)
	cConn.Close()
	sConn.Close()
}

// ============================================================
// UDP proxy end-to-end forwarding tests (simulate a full yamux channel)
// ============================================================

func TestUDPProxy_E2E_ForwardAndReply(t *testing.T) {
	s := New(0)
	clientID := "udp-e2e-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	// 1. Start a local UDP echo service (simulate an internal service)
	echoConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start echo service: %v", err)
	}
	defer echoConn.Close()
	echoPort := echoConn.LocalAddr().(*net.UDPAddr).Port

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			echoConn.WriteTo(buf[:n], addr)
		}
	}()

	// 2. Build the yamux session (simulate the Server ↔ Client data channel)
	pipeC, pipeS := net.Pipe()
	defer pipeC.Close()
	defer pipeS.Close()

	var serverSession *yamux.Session
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverSession, _ = mux.NewServerSession(pipeS, mux.DefaultConfig())
		wg.Done()
	}()
	clientSession, _ := mux.NewClientSession(pipeC, mux.DefaultConfig())
	wg.Wait()

	client.dataSession = serverSession
	defer serverSession.Close()
	defer clientSession.Close()

	// 3. Start the client-side stream accept loop (simulate the Client acceptStreamLoop)
	go func() {
		for {
			stream, err := clientSession.AcceptStream()
			if err != nil {
				return
			}
			go func(s *yamux.Stream) {
				defer s.Close()

				// Read the StreamHeader
				var lenBuf [2]byte
				s.Read(lenBuf[:])
				nameLen := int(lenBuf[0])<<8 | int(lenBuf[1])
				nameBuf := make([]byte, nameLen)
				s.Read(nameBuf)

				// Connect to the local UDP service (echo)
				localConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", echoPort))
				if err != nil {
					return
				}
				defer localConn.Close()

				mux.UDPRelay(s, localConn)
			}(stream)
		}
	}()

	// 4. Start the UDP proxy
	tunnelName := "udp-e2e-tunnel"
	req := protocol.ProxyNewRequest{
		Name:       tunnelName,
		Type:       protocol.ProxyTypeUDP,
		LocalIP:    "127.0.0.1",
		LocalPort:  echoPort,
		RemotePort: reserveUDPPort(t),
	}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("Failed to start UDP proxy: %v", err)
	}
	defer s.StopProxy(client, tunnelName)

	client.proxyMu.RLock()
	remotePort := client.proxies[tunnelName].Config.RemotePort
	client.proxyMu.RUnlock()

	// 5. Simulate an external UDP client: send a message and wait for the echo reply
	extConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("External client connection failed: %v", err)
	}
	defer extConn.Close()

	testMsg := []byte("hello from external client")
	if _, err := extConn.Write(testMsg); err != nil {
		t.Fatalf("Failed to send UDP packet: %v", err)
	}

	// Read the echo reply
	buf := make([]byte, 65535)
	extConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := extConn.Read(buf)
	if err != nil {
		t.Fatalf("Timed out while reading the reply: %v", err)
	}

	if string(buf[:n]) != string(testMsg) {
		t.Errorf("Reply data mismatch: expected %q, got %q", testMsg, buf[:n])
	}
}

func TestUDPProxy_MultipleSessions(t *testing.T) {
	s := New(0)
	clientID := "udp-multi-sess"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	// Start the UDP echo service
	echoConn, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer echoConn.Close()
	echoPort := echoConn.LocalAddr().(*net.UDPAddr).Port

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			echoConn.WriteTo(buf[:n], addr)
		}
	}()

	// Build yamux
	pipeC, pipeS := net.Pipe()
	defer pipeC.Close()
	defer pipeS.Close()

	var serverSession *yamux.Session
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverSession, _ = mux.NewServerSession(pipeS, mux.DefaultConfig())
		wg.Done()
	}()
	clientSession, _ := mux.NewClientSession(pipeC, mux.DefaultConfig())
	wg.Wait()

	client.dataSession = serverSession
	defer serverSession.Close()
	defer clientSession.Close()

	// Client-side stream accept loop
	go func() {
		for {
			stream, err := clientSession.AcceptStream()
			if err != nil {
				return
			}
			go func(s *yamux.Stream) {
				defer s.Close()
				var lenBuf [2]byte
				s.Read(lenBuf[:])
				nameLen := int(lenBuf[0])<<8 | int(lenBuf[1])
				nameBuf := make([]byte, nameLen)
				s.Read(nameBuf)
				localConn, _ := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", echoPort))
				defer localConn.Close()
				mux.UDPRelay(s, localConn)
			}(stream)
		}
	}()

	// Start the UDP proxy
	req := protocol.ProxyNewRequest{
		Name:       "udp-multi-tunnel",
		Type:       protocol.ProxyTypeUDP,
		RemotePort: reserveUDPPort(t),
	}
	s.StartProxy(client, req)
	defer s.StopProxy(client, req.Name)

	client.proxyMu.RLock()
	remotePort := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	// Use multiple local ports to simulate multiple external clients (different srcAddr)
	const numClients = 3
	var clientWg sync.WaitGroup
	errors := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		clientWg.Add(1)
		go func(idx int) {
			defer clientWg.Done()

			conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", remotePort))
			if err != nil {
				errors <- fmt.Errorf("client #%d dial: %v", idx, err)
				return
			}
			defer conn.Close()

			msg := fmt.Sprintf("client-%d-msg", idx)
			conn.Write([]byte(msg))

			buf := make([]byte, 1024)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				errors <- fmt.Errorf("client #%d read: %v", idx, err)
				return
			}
			if string(buf[:n]) != msg {
				errors <- fmt.Errorf("client #%d: expected %q, got %q", idx, msg, buf[:n])
			}
		}(i)
	}

	clientWg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

func TestUDPReadLoop_UnexpectedReadError_MarksTunnelErrorAndPersistsState(t *testing.T) {
	s, client, tunnel, store := setupManagedUDPErrorTestTunnel(t, "udp-runtime-error")

	state := &UDPProxyState{
		done: make(chan struct{}),
		packetConn: &scriptedPacketConn{
			readFrom: func([]byte) (int, net.Addr, error) {
				return 0, nil, errors.New("boom")
			},
		},
	}
	tunnel.UDPState = state

	s.udpReadLoop(client, tunnel, state)

	if tunnel.Config.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.Config.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("After an unexpected exit, expected running/error state, got %s/%s", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
	}
	if !strings.Contains(tunnel.Config.Error, "boom") {
		t.Fatalf("After an unexpected exit, expected the error to contain boom, got %q", tunnel.Config.Error)
	}

	stored, exists := store.GetTunnel(client.ID, tunnel.Config.Name)
	if !exists {
		t.Fatal("The UDP tunnel should be retained in the store")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("Expected store state running/error, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if !strings.Contains(stored.Error, "boom") {
		t.Fatalf("Expected the store error to contain boom, got %q", stored.Error)
	}
}

func TestUDPReadLoop_StateClose_DoesNotMarkTunnelError(t *testing.T) {
	s, client, tunnel, store := setupManagedUDPErrorTestTunnel(t, "udp-runtime-close")

	readReleased := make(chan struct{})
	state := &UDPProxyState{
		done: make(chan struct{}),
		packetConn: &scriptedPacketConn{
			readFrom: func([]byte) (int, net.Addr, error) {
				<-readReleased
				return 0, nil, net.ErrClosed
			},
			closeFunc: func() {
				close(readReleased)
			},
		},
	}
	tunnel.UDPState = state

	loopDone := make(chan struct{})
	go func() {
		s.udpReadLoop(client, tunnel, state)
		close(loopDone)
	}()

	time.Sleep(20 * time.Millisecond)
	state.Close()

	select {
	case <-loopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("udpReadLoop did not exit after state.Close()")
	}

	if tunnel.Config.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.Config.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("After a normal shutdown, the state should remain running/exposed, got %s/%s", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
	}
	if tunnel.Config.Error != "" {
		t.Fatalf("After a normal shutdown, the error should be empty, got %q", tunnel.Config.Error)
	}

	stored, exists := store.GetTunnel(client.ID, tunnel.Config.Name)
	if !exists {
		t.Fatal("The UDP tunnel should be retained in the store")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("After a normal shutdown, the store state should remain running/exposed, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Error != "" {
		t.Fatalf("After a normal shutdown, the store error should be empty, got %q", stored.Error)
	}
}

func TestUDPReadLoop_UnexpectedReadError_DoesNotPoisonReplacedRuntime(t *testing.T) {
	s, client, tunnel, store := setupManagedUDPErrorTestTunnel(t, "udp-stale-runtime")

	currentState := &UDPProxyState{done: make(chan struct{})}
	tunnel.UDPState = currentState

	staleState := &UDPProxyState{
		done: make(chan struct{}),
		packetConn: &scriptedPacketConn{
			readFrom: func([]byte) (int, net.Addr, error) {
				return 0, nil, errors.New("stale boom")
			},
		},
	}

	s.udpReadLoop(client, tunnel, staleState)

	if tunnel.Config.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.Config.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("A stale runtime error should not contaminate the current state, got %s/%s", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
	}
	if tunnel.Config.Error != "" {
		t.Fatalf("A stale runtime error should not be written to error, got %q", tunnel.Config.Error)
	}

	stored, exists := store.GetTunnel(client.ID, tunnel.Config.Name)
	if !exists {
		t.Fatal("The UDP tunnel should be retained in the store")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("A stale runtime error should not contaminate the store state, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Error != "" {
		t.Fatalf("A stale runtime error should not be written to the store error, got %q", stored.Error)
	}
}

func TestUDPReadLoop_OpenStreamFailureMarksTunnelErrorAndPersistsState(t *testing.T) {
	s, client, tunnel, store := setupManagedUDPErrorTestTunnel(t, "udp-open-stream-error")

	firstRead := true
	state := &UDPProxyState{
		done: make(chan struct{}),
		packetConn: &scriptedPacketConn{
			readFrom: func(buf []byte) (int, net.Addr, error) {
				if !firstRead {
					return 0, nil, net.ErrClosed
				}
				firstRead = false
				payload := []byte("ping")
				copy(buf, payload)
				return len(payload), &net.UDPAddr{IP: net.ParseIP("203.0.113.40"), Port: 2053}, nil
			},
		},
	}
	tunnel.UDPState = state

	s.udpReadLoop(client, tunnel, state)

	if tunnel.Config.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.Config.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("Expected running/error state after OpenStream failure, got %s/%s", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
	}
	if !strings.Contains(tunnel.Config.Error, "data channel not established") {
		t.Fatalf("Expected the error to contain the data channel reason after OpenStream failure, got %q", tunnel.Config.Error)
	}

	stored, exists := store.GetTunnel(client.ID, tunnel.Config.Name)
	if !exists {
		t.Fatal("The UDP tunnel should be retained in the store")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("Expected store state running/error, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if !strings.Contains(stored.Error, "data channel not established") {
		t.Fatalf("Expected the store error to contain the data channel reason, got %q", stored.Error)
	}
}

func TestUDPProxyState_RemoveSession_DecrementsPerIPCount(t *testing.T) {
	state := &UDPProxyState{done: make(chan struct{})}
	key := "203.0.113.10:10001"
	sess := &UDPSession{
		srcAddr: &net.UDPAddr{IP: net.ParseIP("203.0.113.10"), Port: 10001},
		ipKey:   "203.0.113.10",
		done:    make(chan struct{}),
	}

	if _, added := state.storeSession(key, sess); !added {
		t.Fatal("The first storeSession call should succeed")
	}
	if got := state.sessionCountForIP("203.0.113.10"); got != 1 {
		t.Fatalf("Expected 1 session for the single IP, got %d", got)
	}

	if removed := state.removeSession(key); !removed {
		t.Fatal("removeSession should return true")
	}
	if got := state.sessionCountForIP("203.0.113.10"); got != 0 {
		t.Fatalf("After removeSession, the single-IP session count should be 0, got %d", got)
	}
}

func TestUDPReadLoop_PerIPSessionLimit_DropsNewSessionFromSameIP(t *testing.T) {
	s, client, tunnel, _ := setupManagedUDPErrorTestTunnel(t, "udp-per-ip-limit")
	cleanupData := attachUDPTestDataSessionSink(t, client)
	defer cleanupData()

	state := &UDPProxyState{done: make(chan struct{})}
	tunnel.UDPState = state

	sameIP := "203.0.113.20"
	for i := 0; i < MaxUDPSessionsPerIP; i++ {
		key := fmt.Sprintf("%s:%d", sameIP, 20000+i)
		if _, added := state.storeSession(key, &UDPSession{
			srcAddr: &net.UDPAddr{IP: net.ParseIP(sameIP), Port: 20000 + i},
			ipKey:   sameIP,
			done:    make(chan struct{}),
		}); !added {
			t.Fatalf("Failed to prefill session %s", key)
		}
	}

	releaseRead := make(chan struct{})
	var firstPacket sync.Once
	newKey := fmt.Sprintf("%s:%d", sameIP, 29999)
	state.packetConn = &scriptedPacketConn{
		readFrom: func(buf []byte) (int, net.Addr, error) {
			fired := false
			firstPacket.Do(func() { fired = true })
			if fired {
				payload := []byte("one-packet")
				copy(buf, payload)
				return len(payload), &net.UDPAddr{IP: net.ParseIP(sameIP), Port: 29999}, nil
			}
			<-releaseRead
			return 0, nil, net.ErrClosed
		},
		closeFunc: func() {
			close(releaseRead)
		},
	}

	loopDone := make(chan struct{})
	go func() {
		s.udpReadLoop(client, tunnel, state)
		close(loopDone)
	}()

	if _, exists := state.sessions.Load(newKey); exists {
		state.Close()
		<-loopDone
		t.Fatal("No new session should be created when the same IP exceeds the limit")
	}
	if got := state.sessionCountForIP(sameIP); got != MaxUDPSessionsPerIP {
		state.Close()
		<-loopDone
		t.Fatalf("The session count for the same IP should remain %d, got %d", MaxUDPSessionsPerIP, got)
	}

	state.Close()
	<-loopDone
}

func TestUDPProxyState_CanCreateSessionForIP_BlocksOnlySaturatedIP(t *testing.T) {
	state := &UDPProxyState{done: make(chan struct{})}
	fullIP := "203.0.113.30"
	otherIP := "203.0.113.31"

	for i := 0; i < MaxUDPSessionsPerIP; i++ {
		key := fmt.Sprintf("%s:%d", fullIP, 30000+i)
		if _, added := state.storeSession(key, &UDPSession{
			srcAddr: &net.UDPAddr{IP: net.ParseIP(fullIP), Port: 30000 + i},
			ipKey:   fullIP,
			done:    make(chan struct{}),
		}); !added {
			t.Fatalf("Failed to prefill session %s", key)
		}
	}

	if state.canCreateSessionForIP(fullIP) {
		t.Fatal("An IP that has reached its limit should not create new sessions")
	}
	if !state.canCreateSessionForIP(otherIP) {
		t.Fatal("Other IPs should not be affected by the saturated single-IP limit")
	}
}

// ============================================================
// sessionCount correctness tests
// ============================================================

// TestRemoveSession_Idempotent verifies that repeated removeSession calls decrement the count only once.
func TestRemoveSession_Idempotent(t *testing.T) {
	state := &UDPProxyState{
		done: make(chan struct{}),
	}

	key := "127.0.0.1:12345"
	sess := &UDPSession{done: make(chan struct{})}
	state.sessions.Store(key, sess)
	state.sessionCount.Store(1)

	// First call: should remove successfully and decrement
	if removed := state.removeSession(key); !removed {
		t.Error("The first removeSession call should return true")
	}
	if got := state.sessionCount.Load(); got != 0 {
		t.Errorf("After the first call, sessionCount should be 0, got %d", got)
	}

	// Second call: the key no longer exists, so it should be a no-op
	if removed := state.removeSession(key); removed {
		t.Error("The second removeSession call should return false (the key no longer exists)")
	}
	if got := state.sessionCount.Load(); got != 0 {
		t.Errorf("After the second call, sessionCount should still be 0, got %d (double decrement occurred)", got)
	}
}

// TestUDPProxy_SessionCount_AfterCleanup verifies that sessionCount does not become negative after Close().
func TestUDPProxy_SessionCount_AfterCleanup(t *testing.T) {
	pipeC, pipeS := net.Pipe()
	defer pipeC.Close()
	defer pipeS.Close()

	// Build a minimal usable UDPProxyState (no real packetConn needed)
	state := &UDPProxyState{
		done: make(chan struct{}),
	}

	// Manually inject multiple closed stream sessions (simulate active sessions)
	const numSessions = 3
	for i := 0; i < numSessions; i++ {
		c1, c2 := net.Pipe()
		key := fmt.Sprintf("127.0.0.1:%d", 10000+i)
		sess := &UDPSession{
			srcAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 10000 + i},
			stream:  c1,
			done:    make(chan struct{}),
		}
		sess.Touch()
		state.sessions.Store(key, sess)
		state.sessionCount.Add(1)
		// Start the reverse goroutine; it holds c1 and exits when sess.Close() runs
		go func(s *UDPSession) {
			buf := make([]byte, 1024)
			s.stream.Read(buf) //nolint
		}(sess)
		_ = c2
	}

	if got := state.sessionCount.Load(); got != numSessions {
		t.Fatalf("The initial sessionCount should be %d, got %d", numSessions, got)
	}

	// Trigger Close()
	state.Close()

	// Poll until sessionCount reaches zero (up to 2s), without relying on goroutine exit signals
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if state.sessionCount.Load() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := state.sessionCount.Load(); got < 0 {
		t.Errorf("After Close(), sessionCount should not be negative, got %d", got)
	}
}

// TestUDPReaper_NoDoubleDecrement verifies that sessionCount is not decremented twice when udpReaper and removeSession run concurrently.
func TestUDPReaper_NoDoubleDecrement(t *testing.T) {
	state := &UDPProxyState{
		done: make(chan struct{}),
	}

	// Create a pipe pair as the stream
	c1, c2 := net.Pipe()
	defer c2.Close()

	key := "127.0.0.1:9999"
	sess := &UDPSession{
		srcAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999},
		stream:  c1,
		done:    make(chan struct{}),
	}
	sess.Touch()
	state.sessions.Store(key, sess)
	state.sessionCount.Store(1)

	// Simulate udpReaper timeout cleanup
	sess.Close()
	state.removeSession(key)

	// Simultaneously simulate udpSessionReverse defer also triggering removeSession (race scenario)
	state.removeSession(key)

	if got := state.sessionCount.Load(); got != 0 {
		t.Errorf("After concurrent double removeSession calls, sessionCount should be 0, got %d (double decrement occurred)", got)
	}
}
