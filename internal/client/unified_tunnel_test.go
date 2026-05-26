package client

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

func testTunnelProvisionRequest(t *testing.T, role string, port int) protocol.TunnelProvisionRequest {
	t.Helper()

	ingressConfig, err := json.Marshal(map[string]any{"bind_ip": "127.0.0.1", "port": port})
	if err != nil {
		t.Fatalf("marshal ingress config: %v", err)
	}
	targetConfig, err := json.Marshal(map[string]any{"host": "127.0.0.1", "port": 8080})
	if err != nil {
		t.Fatalf("marshal target config: %v", err)
	}
	return protocol.TunnelProvisionRequest{
		TunnelID: "tunnel-id",
		Revision: 3,
		Role:     role,
		Spec: protocol.TunnelSpec{
			ID:              "tunnel-id",
			Name:            "tunnel-name",
			Revision:        3,
			Topology:        protocol.TunnelTopologyClientToClient,
			OwnerClientID:   "target-client",
			TransportPolicy: protocol.TransportPolicyServerRelayOnly,
			Ingress: protocol.EndpointSpec{
				Location: protocol.EndpointLocationClient,
				ClientID: "ingress-client",
				Type:     protocol.IngressTypeTCPListen,
				Config:   ingressConfig,
			},
			Target: protocol.EndpointSpec{
				Location: protocol.EndpointLocationClient,
				ClientID: "target-client",
				Type:     protocol.TargetTypeTCPService,
				Config:   targetConfig,
			},
		},
	}
}

func reserveClientTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp port: %v", err)
	}
	defer mustClose(t, ln)
	return ln.Addr().(*net.TCPAddr).Port
}

func newClientTestWebSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	serverConnCh := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConnCh <- conn
	}))
	t.Cleanup(ts.Close)

	clientURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(clientURL, nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case serverConn := <-serverConnCh:
		return clientConn, serverConn
	case <-time.After(time.Second):
		_ = clientConn.Close()
		t.Fatal("timed out waiting for test websocket")
		return nil, nil
	}
}

func assertTCPPortAccepts(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("listener was not reachable: %v", err)
	}
	mustClose(t, conn)
}

func TestClientReportsIngressRuntimeErrorWhenDataSessionUnavailable(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	c.ClientID = "ingress-client"
	clientWS, serverWS := newClientTestWebSocketPair(t)
	defer mustClose(t, clientWS)
	defer mustClose(t, serverWS)

	rt := &sessionRuntime{done: make(chan struct{}), conn: clientWS}
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleIngress, reserveClientTCPPort(t))
	externalConn, tunnelConn := net.Pipe()
	defer mustClose(t, externalConn)

	done := make(chan struct{})
	go func() {
		c.handleIngressTCPConn(rt, req, tunnelConn)
		close(done)
	}()

	if err := serverWS.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	var msg protocol.Message
	if err := serverWS.ReadJSON(&msg); err != nil {
		t.Fatalf("read runtime report: %v", err)
	}
	if msg.Type != protocol.MsgTypeTunnelRuntimeReport {
		t.Fatalf("message type: want %s, got %s", protocol.MsgTypeTunnelRuntimeReport, msg.Type)
	}
	var report protocol.TunnelRuntimeReport
	if err := msg.ParsePayload(&report); err != nil {
		t.Fatalf("parse runtime report: %v", err)
	}
	if report.TunnelID != req.TunnelID || report.Revision != req.Revision || report.Role != protocol.DataStreamRoleIngress {
		t.Fatalf("runtime report identity mismatch: %+v", report)
	}
	if report.Participant.ClientID != c.CurrentClientID() || report.Participant.State != protocol.ProxyRuntimeStateError {
		t.Fatalf("runtime report participant mismatch: %+v", report.Participant)
	}
	if !strings.Contains(report.Message, "data session unavailable") {
		t.Fatalf("runtime report message should explain data session failure, got %q", report.Message)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ingress connection handler did not return")
	}
}

func assertTCPPortClosed(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("listener still accepts connections after unprovision")
}

func TestClientTunnelProvisionTargetRegistersProxyByTunnelID(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("target provision rejected: %s", ack.Message)
	}
	value, ok := c.proxies.Load(req.TunnelID)
	if !ok {
		t.Fatal("target provision did not register proxy under tunnel id")
	}
	proxy := value.(protocol.ProxyNewRequest)
	if proxy.Name != req.Spec.Name || proxy.LocalIP != "127.0.0.1" || proxy.LocalPort != 8080 {
		t.Fatalf("proxy mismatch: %+v", proxy)
	}
	if proxy.ProvisionRevision != uint64(req.Revision) {
		t.Fatalf("provision revision mismatch: got %d want %d", proxy.ProvisionRevision, req.Revision)
	}
}

func TestClientTunnelProvisionIngressStartsAndStopsListener(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleIngress, reserveClientTCPPort(t))

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("ingress provision rejected: %s", ack.Message)
	}

	var cfg struct {
		BindIP string `json:"bind_ip"`
		Port   int    `json:"port"`
	}
	if err := json.Unmarshal(req.Spec.Ingress.Config, &cfg); err != nil {
		t.Fatalf("decode ingress config: %v", err)
	}
	addr := net.JoinHostPort(cfg.BindIP, strconv.Itoa(cfg.Port))
	assertTCPPortAccepts(t, addr)

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     protocol.DataStreamRoleIngress,
	})

	assertTCPPortClosed(t, addr)
}

func TestClientTunnelUnprovisionIgnoresStaleIngressRevision(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleIngress, reserveClientTCPPort(t))

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("ingress provision rejected: %s", ack.Message)
	}

	var cfg struct {
		BindIP string `json:"bind_ip"`
		Port   int    `json:"port"`
	}
	if err := json.Unmarshal(req.Spec.Ingress.Config, &cfg); err != nil {
		t.Fatalf("decode ingress config: %v", err)
	}
	addr := net.JoinHostPort(cfg.BindIP, strconv.Itoa(cfg.Port))
	assertTCPPortAccepts(t, addr)

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision - 1,
		Role:     protocol.DataStreamRoleIngress,
	})
	assertTCPPortAccepts(t, addr)

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     protocol.DataStreamRoleIngress,
	})
	assertTCPPortClosed(t, addr)
}

func TestClientTunnelUnprovisionIgnoresStaleTargetRevision(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("target provision rejected: %s", ack.Message)
	}

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision - 1,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.proxies.Load(req.TunnelID); !ok {
		t.Fatal("stale target unprovision deleted current proxy")
	}

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.proxies.Load(req.TunnelID); ok {
		t.Fatal("current target unprovision did not delete proxy")
	}
}

func TestClientTunnelPreflightTCPBindSuccessAndFailure(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	port := reserveClientTCPPort(t)
	config, err := json.Marshal(map[string]any{"bind_ip": "127.0.0.1", "port": port})
	if err != nil {
		t.Fatalf("marshal preflight config: %v", err)
	}

	resp := c.handleTunnelPreflight(protocol.TunnelPreflightRequest{
		RequestID: "req-ok",
		Role:      protocol.DataStreamRoleIngress,
		Ingress: protocol.EndpointSpec{
			Location: protocol.EndpointLocationClient,
			Type:     protocol.IngressTypeTCPListen,
			Config:   config,
		},
	})
	if !resp.Accepted || resp.Code != "" {
		t.Fatalf("free tcp port preflight should pass: %+v", resp)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("occupy tcp port: %v", err)
	}
	defer mustClose(t, ln)

	resp = c.handleTunnelPreflight(protocol.TunnelPreflightRequest{
		RequestID: "req-busy",
		Role:      protocol.DataStreamRoleIngress,
		Ingress: protocol.EndpointSpec{
			Location: protocol.EndpointLocationClient,
			Type:     protocol.IngressTypeTCPListen,
			Config:   config,
		},
	})
	if resp.Accepted || resp.Code != protocol.TunnelMutationErrorCodeIngressPortInUse {
		t.Fatalf("occupied tcp port preflight should fail with ingress_port_in_use: %+v", resp)
	}
}

func TestClientTunnelPreflightUDPBindSuccessAndFailure(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve udp port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	mustClose(t, conn)

	config, err := json.Marshal(map[string]any{"bind_ip": "127.0.0.1", "port": port})
	if err != nil {
		t.Fatalf("marshal preflight config: %v", err)
	}
	resp := c.handleTunnelPreflight(protocol.TunnelPreflightRequest{
		RequestID: "req-udp-ok",
		Role:      protocol.DataStreamRoleIngress,
		Ingress: protocol.EndpointSpec{
			Location: protocol.EndpointLocationClient,
			Type:     protocol.IngressTypeUDPListen,
			Config:   config,
		},
	})
	if !resp.Accepted || resp.Code != "" {
		t.Fatalf("free udp port preflight should pass: %+v", resp)
	}

	busy, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("occupy udp port: %v", err)
	}
	defer mustClose(t, busy)

	resp = c.handleTunnelPreflight(protocol.TunnelPreflightRequest{
		RequestID: "req-udp-busy",
		Role:      protocol.DataStreamRoleIngress,
		Ingress: protocol.EndpointSpec{
			Location: protocol.EndpointLocationClient,
			Type:     protocol.IngressTypeUDPListen,
			Config:   config,
		},
	})
	if resp.Accepted || resp.Code != protocol.TunnelMutationErrorCodeIngressPortInUse {
		t.Fatalf("occupied udp port preflight should fail with ingress_port_in_use: %+v", resp)
	}
}

func reserveClientUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve udp port: %v", err)
	}
	defer mustClose(t, conn)
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func testUDPTunnelProvisionRequest(t *testing.T, role string, port int) protocol.TunnelProvisionRequest {
	t.Helper()
	req := testTunnelProvisionRequest(t, role, port)
	targetConfig, err := json.Marshal(map[string]any{"host": "127.0.0.1", "port": reserveClientUDPPort(t)})
	if err != nil {
		t.Fatalf("marshal udp target config: %v", err)
	}
	req.Spec.Ingress.Type = protocol.IngressTypeUDPListen
	req.Spec.Target.Type = protocol.TargetTypeUDPService
	req.Spec.Target.Config = targetConfig
	return req
}

func TestClientTunnelProvisionUDPIngressRelaysFramesAndUnprovisions(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer mustClose(t, clientSide)
	defer mustClose(t, serverSide)

	clientSession, err := mux.NewClientSession(clientSide, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("client yamux session: %v", err)
	}
	defer mustClose(t, clientSession)
	serverSession, err := mux.NewServerSession(serverSide, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("server yamux session: %v", err)
	}
	defer mustClose(t, serverSession)

	c := New("ws://localhost:8080", "key")
	c.ClientID = "ingress-client"
	rt := &sessionRuntime{done: make(chan struct{})}
	rt.dataSession = clientSession

	port := reserveClientUDPPort(t)
	req := testUDPTunnelProvisionRequest(t, protocol.DataStreamRoleIngress, port)
	ack := c.handleTunnelProvision(rt, req)
	if !ack.Accepted {
		t.Fatalf("udp ingress provision rejected: %s", ack.Message)
	}

	serverStreamCh := make(chan net.Conn, 1)
	go func() {
		stream, err := serverSession.AcceptStream()
		if err != nil {
			serverStreamCh <- nil
			return
		}
		serverStreamCh <- stream
	}()

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp external source: %v", err)
	}
	defer mustClose(t, udpConn)
	payload := []byte("udp ingress payload")
	if _, err := udpConn.WriteTo(payload, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("send udp payload: %v", err)
	}

	var stream net.Conn
	select {
	case stream = <-serverStreamCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ingress data stream")
	}
	if stream == nil {
		t.Fatal("ingress data stream failed to open")
	}
	defer mustClose(t, stream)

	header, err := protocol.DecodeDataStreamHeader(stream)
	if err != nil {
		t.Fatalf("decode stream header: %v", err)
	}
	if header.TunnelID != req.TunnelID || header.Revision != req.Revision || header.OpenClientID != c.CurrentClientID() {
		t.Fatalf("header identity mismatch: %+v", header)
	}
	if header.SourceRole != protocol.DataStreamRoleIngress || header.TargetRole != protocol.DataStreamRoleTarget || header.Transport != protocol.ActualTransportServerRelay {
		t.Fatalf("header route mismatch: %+v", header)
	}

	got, err := mux.ReadUDPFrame(stream)
	if err != nil {
		t.Fatalf("read udp frame from stream: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("udp frame mismatch: got %q want %q", got, payload)
	}

	reply := []byte("udp ingress reply")
	if err := mux.WriteUDPFrame(stream, reply); err != nil {
		t.Fatalf("write udp reply frame: %v", err)
	}
	buf := make([]byte, 1024)
	if err := udpConn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set udp deadline: %v", err)
	}
	n, _, err := udpConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read udp reply: %v", err)
	}
	if !bytes.Equal(buf[:n], reply) {
		t.Fatalf("udp reply mismatch: got %q want %q", buf[:n], reply)
	}

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     protocol.DataStreamRoleIngress,
	})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		probe, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			_ = probe.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("udp listener still bound after unprovision")
}

func TestClientUDPAssociationBoundsAndOldestEviction(t *testing.T) {
	if clientMaxUDPAssociations != 4096 {
		t.Fatalf("client UDP association cap: want 4096, got %d", clientMaxUDPAssociations)
	}
	if clientUDPAssociationTimeout != 2*time.Minute {
		t.Fatalf("client UDP association timeout: want 2m, got %s", clientUDPAssociationTimeout)
	}

	runtime := &clientTunnelRuntime{done: make(chan struct{})}
	oldStream, oldPeer := net.Pipe()
	newStream, newPeer := net.Pipe()
	t.Cleanup(func() {
		_ = oldStream.Close()
		_ = oldPeer.Close()
		_ = newStream.Close()
		_ = newPeer.Close()
	})

	oldAssoc := newClientUDPAssociation("old", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 10001}, oldStream)
	newAssoc := newClientUDPAssociation("new", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 10002}, newStream)
	oldAssoc.lastActive.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	newAssoc.lastActive.Store(time.Now().Add(-time.Second).UnixNano())
	runtime.udpAssociations.Store(oldAssoc.key, oldAssoc)
	runtime.udpAssociations.Store(newAssoc.key, newAssoc)
	runtime.udpAssociationCount.Store(2)

	if !runtime.removeOldestUDPAssociation() {
		t.Fatal("expected oldest UDP association to be evicted")
	}
	if _, ok := runtime.udpAssociations.Load(oldAssoc.key); ok {
		t.Fatal("oldest UDP association was not removed")
	}
	if _, ok := runtime.udpAssociations.Load(newAssoc.key); !ok {
		t.Fatal("newer UDP association should remain")
	}
	if got := runtime.udpAssociationCount.Load(); got != 1 {
		t.Fatalf("association count: want 1, got %d", got)
	}
}
