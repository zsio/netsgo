package server

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

func reserveUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve UDP port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	if err := conn.Close(); err != nil {
		t.Fatalf("failed to close reserved UDP port: %v", err)
	}
	return port
}

func TestStartProxy_UDP(t *testing.T) {
	s := New(0)
	clientID := "udp-test-client"
	client := &ClientConn{ID: clientID, proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(clientID, client)

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

	testConn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", tunnel.Config.RemotePort), 100*time.Millisecond)
	if err != nil {
		t.Errorf("Unable to connect to the UDP port: %v", err)
	} else {
		if _, err := testConn.Write([]byte("probe")); err != nil {
			t.Fatalf("write probe failed: %v", err)
		}
		_ = testConn.Close()
	}

	s.StopAllProxies(client)
	_ = cConn.Close()
	_ = sConn.Close()
}

func TestStopProxy_UDP(t *testing.T) {
	s := New(0)
	clientID := "udp-stop-client"
	client := &ClientConn{ID: clientID, proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(clientID, client)

	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{Name: "udp-to-stop", Type: protocol.ProxyTypeUDP, RemotePort: reserveUDPPort(t)}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy UDP failed: %v", err)
	}

	client.proxyMu.RLock()
	port := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	if err := s.StopProxy(client, req.Name); err != nil {
		t.Fatalf("StopProxy UDP failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	probe, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Errorf("UDP port %d was not released: %v", port, err)
	} else {
		_ = probe.Close()
	}

	_ = cConn.Close()
	_ = sConn.Close()
}

func TestCloseAndReopenProxyRuntime_UDP(t *testing.T) {
	s := New(0)
	clientID := "udp-stop-client"
	client := &ClientConn{ID: clientID, proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(clientID, client)

	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{Name: "udp-stop-test", Type: protocol.ProxyTypeUDP, RemotePort: reserveUDPPort(t)}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy UDP failed: %v", err)
	}

	client.proxyMu.RLock()
	port := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	if err := s.CloseProxyRuntime(client, req.Name); err != nil {
		t.Fatalf("CloseProxyRuntime UDP failed: %v", err)
	}

	client.proxyMu.RLock()
	desiredState := client.proxies[req.Name].Config.DesiredState
	runtimeState := client.proxies[req.Name].Config.RuntimeState
	client.proxyMu.RUnlock()
	if desiredState != protocol.ProxyDesiredStateRunning || runtimeState != protocol.ProxyRuntimeStateExposed {
		t.Errorf("CloseProxyRuntime only closes runtime resources; got %s/%s", desiredState, runtimeState)
	}

	time.Sleep(50 * time.Millisecond)
	if err := s.ReopenProxyRuntime(client, req.Name); err != nil {
		t.Fatalf("ReopenProxyRuntime UDP failed: %v", err)
	}

	client.proxyMu.RLock()
	newPort := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()
	if newPort != port {
		t.Errorf("After resuming, expected port %d, got %d", port, newPort)
	}

	s.StopAllProxies(client)
	_ = cConn.Close()
	_ = sConn.Close()
}

func TestUDPProxy_E2E_ForwardAndReply(t *testing.T) {
	s := New(0)
	clientID := "udp-e2e-client"
	client := &ClientConn{ID: clientID, proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(clientID, client)

	echoConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start echo service: %v", err)
	}
	defer func() { _ = echoConn.Close() }()
	echoPort := echoConn.LocalAddr().(*net.UDPAddr).Port

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echoConn.WriteTo(buf[:n], addr)
		}
	}()

	pipeC, pipeS := net.Pipe()
	defer mustClose(t, pipeC)
	defer mustClose(t, pipeS)

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
	defer mustClose(t, serverSession)
	defer mustClose(t, clientSession)

	go func() {
		for {
			stream, err := clientSession.AcceptStream()
			if err != nil {
				return
			}
			go func(s *yamux.Stream) {
				defer func() { _ = s.Close() }()
				var lenBuf [2]byte
				_, _ = io.ReadFull(s, lenBuf[:])
				nameLen := int(lenBuf[0])<<8 | int(lenBuf[1])
				nameBuf := make([]byte, nameLen)
				_, _ = io.ReadFull(s, nameBuf)
				localConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", echoPort))
				if err != nil {
					return
				}
				defer func() { _ = localConn.Close() }()
				mux.UDPRelay(s, localConn)
			}(stream)
		}
	}()

	tunnelName := "udp-e2e-tunnel"
	req := protocol.ProxyNewRequest{Name: tunnelName, Type: protocol.ProxyTypeUDP, LocalIP: "127.0.0.1", LocalPort: echoPort, RemotePort: reserveUDPPort(t)}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("Failed to start UDP proxy: %v", err)
	}
	defer func() { _ = s.StopProxy(client, tunnelName) }()

	client.proxyMu.RLock()
	remotePort := client.proxies[tunnelName].Config.RemotePort
	client.proxyMu.RUnlock()

	extConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("External client connection failed: %v", err)
	}
	defer mustClose(t, extConn)

	testMsg := []byte("hello from external client")
	if _, err := extConn.Write(testMsg); err != nil {
		t.Fatalf("Failed to send UDP packet: %v", err)
	}
	mustSetReadDeadline(t, extConn, time.Now().Add(5*time.Second))
	buf := make([]byte, 1024)
	n, err := extConn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read UDP reply: %v", err)
	}
	if !bytes.Equal(buf[:n], testMsg) {
		t.Fatalf("unexpected UDP reply: %q", buf[:n])
	}
}

func TestUDPProxyTrafficAccounting_RecordsPayloadBytesOnly(t *testing.T) {
	s := New(0)
	clientID := "udp-traffic-client"
	client := &ClientConn{ID: clientID, proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(clientID, client)

	trafficStore, trafficCleanup := newTestTrafficStore(t)
	defer trafficCleanup()
	s.trafficStore = trafficStore

	echoConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start echo service: %v", err)
	}
	defer func() { _ = echoConn.Close() }()
	echoPort := echoConn.LocalAddr().(*net.UDPAddr).Port

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echoConn.WriteTo(buf[:n], addr)
		}
	}()

	pipeC, pipeS := net.Pipe()
	defer mustClose(t, pipeC)
	defer mustClose(t, pipeS)

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
	defer mustClose(t, serverSession)
	defer mustClose(t, clientSession)

	go func() {
		for {
			stream, err := clientSession.AcceptStream()
			if err != nil {
				return
			}
			go func(s *yamux.Stream) {
				defer func() { _ = s.Close() }()
				var lenBuf [2]byte
				_, _ = io.ReadFull(s, lenBuf[:])
				nameLen := int(lenBuf[0])<<8 | int(lenBuf[1])
				nameBuf := make([]byte, nameLen)
				_, _ = io.ReadFull(s, nameBuf)
				localConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", echoPort))
				if err != nil {
					return
				}
				defer func() { _ = localConn.Close() }()
				mux.UDPRelay(s, localConn)
			}(stream)
		}
	}()

	tunnelName := "udp-traffic-tunnel"
	req := protocol.ProxyNewRequest{Name: tunnelName, Type: protocol.ProxyTypeUDP, LocalIP: "127.0.0.1", LocalPort: echoPort, RemotePort: reserveUDPPort(t)}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("Failed to start UDP proxy: %v", err)
	}
	defer func() { _ = s.StopProxy(client, tunnelName) }()

	client.proxyMu.RLock()
	remotePort := client.proxies[tunnelName].Config.RemotePort
	client.proxyMu.RUnlock()

	extConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("External client connection failed: %v", err)
	}
	defer mustClose(t, extConn)

	payload := []byte("udp-payload-only")
	if _, err := extConn.Write(payload); err != nil {
		t.Fatalf("Failed to send UDP packet: %v", err)
	}
	mustSetReadDeadline(t, extConn, time.Now().Add(5*time.Second))
	reply := make([]byte, 1024)
	n, err := extConn.Read(reply)
	if err != nil {
		t.Fatalf("Failed to read UDP reply: %v", err)
	}
	if !bytes.Equal(reply[:n], payload) {
		t.Fatalf("unexpected UDP echo reply: want %q, got %q", payload, reply[:n])
	}

	result := mustQueryTraffic(t, trafficStore, clientID, tunnelName, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if len(result.Items) != 1 {
		t.Fatalf("traffic items: want 1, got %d", len(result.Items))
	}
	points := result.Items[0].Points
	if len(points) != 1 {
		t.Fatalf("traffic points: want 1, got %d", len(points))
	}
	if points[0].IngressBytes != uint64(len(payload)) {
		t.Fatalf("ingress bytes should count only UDP payload bytes: want %d, got %d", len(payload), points[0].IngressBytes)
	}
	if points[0].EgressBytes != uint64(len(payload)) {
		t.Fatalf("egress bytes should count only UDP payload bytes: want %d, got %d", len(payload), points[0].EgressBytes)
	}
}
