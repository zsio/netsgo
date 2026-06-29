package client

import (
	"bytes"
	"encoding/json"
	"io"
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

	ingressConfig, err := json.Marshal(map[string]any{
		"bind_ip":              "127.0.0.1",
		"port":                 port,
		"allowed_source_cidrs": []string{"0.0.0.0/0", "::/0"},
	})
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

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
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

type testRemoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c testRemoteAddrConn) RemoteAddr() net.Addr {
	return c.remote
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
	sourcePolicy, err := parseIngressAccessPolicy([]string{"0.0.0.0/0", "::/0"}, false)
	if err != nil {
		t.Fatalf("parse source policy: %v", err)
	}
	runtime := &clientTunnelRuntime{done: make(chan struct{}), sourceCIDRs: sourcePolicy.sourceCIDRs}

	done := make(chan struct{})
	go func() {
		c.handleIngressTCPConn(rt, req, runtime, testRemoteAddrConn{
			Conn:   tunnelConn,
			remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40000},
		})
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

func TestClientIngressTCPRejectsDisallowedSourceBeforeDataSession(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	c.ClientID = "ingress-client"
	clientWS, serverWS := newClientTestWebSocketPair(t)
	defer mustClose(t, clientWS)
	defer mustClose(t, serverWS)

	rt := &sessionRuntime{done: make(chan struct{}), conn: clientWS}
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleIngress, reserveClientTCPPort(t))
	sourcePolicy, err := parseIngressAccessPolicy([]string{"203.0.113.0/24"}, false)
	if err != nil {
		t.Fatalf("parse source policy: %v", err)
	}
	runtime := &clientTunnelRuntime{done: make(chan struct{}), sourceCIDRs: sourcePolicy.sourceCIDRs}
	for _, tc := range []struct {
		name string
		ip   string
	}{
		{name: "external", ip: "198.51.100.10"},
		{name: "loopback", ip: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			externalConn, tunnelConn := net.Pipe()
			defer mustClose(t, externalConn)

			done := make(chan struct{})
			go func() {
				c.handleIngressTCPConn(rt, req, runtime, testRemoteAddrConn{
					Conn:   tunnelConn,
					remote: &net.TCPAddr{IP: net.ParseIP(tc.ip), Port: 40000},
				})
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("ingress connection handler did not return for disallowed source %s", tc.ip)
			}
			if err := serverWS.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				t.Fatalf("set websocket read deadline: %v", err)
			}
			var msg protocol.Message
			if err := serverWS.ReadJSON(&msg); err == nil {
				t.Fatalf("disallowed TCP source %s should not report data-session error or open stream, got message %s", tc.ip, msg.Type)
			}
		})
	}
}

func TestClientReportsIngressRuntimeErrorWhenTCPListenerFails(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	c.ClientID = "ingress-client"
	clientWS, serverWS := newClientTestWebSocketPair(t)
	defer mustClose(t, clientWS)
	defer mustClose(t, serverWS)

	rt := &sessionRuntime{done: make(chan struct{}), conn: clientWS}
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleIngress, reserveClientTCPPort(t))
	ack := c.handleTunnelProvision(rt, req)
	if !ack.Accepted {
		t.Fatalf("ingress provision rejected: %s", ack.Message)
	}

	key := tunnelRuntimeKey(req.TunnelID, protocol.DataStreamRoleIngress)
	value, ok := c.tunnels.Load(key)
	if !ok {
		t.Fatal("ingress runtime was not stored")
	}
	runtime, ok := value.(*clientTunnelRuntime)
	if !ok {
		t.Fatalf("ingress runtime has unexpected type %T", value)
	}
	if runtime.listener == nil {
		t.Fatal("ingress runtime missing TCP listener")
	}
	if err := runtime.listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

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
	if !strings.Contains(report.Message, "tunnel ingress accept failed") {
		t.Fatalf("runtime report message should explain listener failure, got %q", report.Message)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := c.tunnels.Load(key); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("failed ingress runtime remained registered after listener failure")
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

func TestClientTunnelProvisionFixedTCPTargetDoesNotRegisterLegacyProxy(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("target provision rejected: %s", ack.Message)
	}
	if _, ok := c.proxies.Load(req.TunnelID); ok {
		t.Fatal("unified fixed TCP target provision must not register legacy ProxyNewRequest under tunnel id")
	}
	if _, ok := c.proxies.Load(req.Spec.Name); ok {
		t.Fatal("unified fixed TCP target provision must not register legacy ProxyNewRequest under tunnel name")
	}
}

func TestClientTunnelProvisionFixedUDPTargetDoesNotRegisterLegacyProxy(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testUDPTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("udp target provision rejected: %s", ack.Message)
	}
	if _, ok := c.proxies.Load(req.TunnelID); ok {
		t.Fatal("unified fixed UDP target provision must not register legacy ProxyNewRequest under tunnel id")
	}
	if _, ok := c.proxies.Load(req.Spec.Name); ok {
		t.Fatal("unified fixed UDP target provision must not register legacy ProxyNewRequest under tunnel name")
	}
}

func TestClientTunnelProvisionUnsupportedTargetRejectsWithoutRuntime(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	req.Spec.Target.Type = "future_target"
	req.Spec.Target.Config = mustJSON(t, map[string]any{
		"host": "127.0.0.1",
		"port": 8080,
	})

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if ack.Accepted {
		t.Fatalf("unsupported target type must be rejected, got %+v", ack)
	}
	if ack.TunnelID != req.TunnelID || ack.Revision != req.Revision || ack.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("reject ack identity mismatch: %+v", ack)
	}
	if !strings.Contains(ack.Message, "unsupported target type future_target") {
		t.Fatalf("reject ack should explain unsupported target type, got %q", ack.Message)
	}
	if _, ok := c.proxies.Load(req.TunnelID); ok {
		t.Fatal("unsupported target reject must not write legacy proxy by tunnel id")
	}
	if _, ok := c.proxies.Load(req.Spec.Name); ok {
		t.Fatal("unsupported target reject must not write legacy proxy by tunnel name")
	}
	if _, ok := c.socks5Targets.Load(req.TunnelID); ok {
		t.Fatal("unsupported target reject must not write SOCKS5 target runtime")
	}
	if _, ok := c.tunnels.Load(tunnelRuntimeKey(req.TunnelID, protocol.DataStreamRoleIngress)); ok {
		t.Fatal("unsupported target reject must not create ingress runtime")
	}
}

func TestClientTunnelProvisionUnsupportedIngressRejectsWithoutRuntime(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	port := reserveClientTCPPort(t)
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleIngress, port)
	req.Spec.Ingress.Type = "future_ingress"
	req.Spec.Ingress.Config = mustJSON(t, map[string]any{
		"bind_ip":              "127.0.0.1",
		"port":                 port,
		"allowed_source_cidrs": []string{"0.0.0.0/0", "::/0"},
	})

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if ack.Accepted {
		t.Fatalf("unsupported ingress type must be rejected, got %+v", ack)
	}
	if ack.TunnelID != req.TunnelID || ack.Revision != req.Revision || ack.Role != protocol.DataStreamRoleIngress {
		t.Fatalf("reject ack identity mismatch: %+v", ack)
	}
	if !strings.Contains(ack.Message, "unsupported ingress type future_ingress") {
		t.Fatalf("reject ack should explain unsupported ingress type, got %q", ack.Message)
	}
	if _, ok := c.tunnels.Load(tunnelRuntimeKey(req.TunnelID, protocol.DataStreamRoleIngress)); ok {
		t.Fatal("unsupported ingress reject must not create ingress runtime")
	}
	if _, ok := c.proxies.Load(req.TunnelID); ok {
		t.Fatal("unsupported ingress reject must not write legacy proxy by tunnel id")
	}
	if _, ok := c.socks5Targets.Load(req.TunnelID); ok {
		t.Fatal("unsupported ingress reject must not write SOCKS5 target runtime")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("unsupported ingress reject must leave port reusable: %v", err)
	}
	mustClose(t, ln)
}

func TestClientTunnelProvisionSOCKS5TargetUsesEndpointRuntime(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	req.Spec.Target.Type = protocol.TargetTypeSOCKS5ConnectHandler
	req.Spec.Target.Config = mustJSON(t, protocol.SOCKS5ConnectHandlerConfig{
		AllowedTargetCIDRs: []string{"127.0.0.0/8"},
		AllowedTargetHosts: []string{"127.0.0.1"},
		AllowedTargetPorts: []int{8080},
		DialTimeoutSeconds: 2,
	})

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("SOCKS5 target provision rejected: %s", ack.Message)
	}
	if _, ok := c.proxies.Load(req.TunnelID); ok {
		t.Fatal("SOCKS5 target provision must not register legacy ProxyNewRequest")
	}
	value, ok := c.socks5Targets.Load(req.TunnelID)
	if !ok {
		t.Fatal("SOCKS5 target provision did not store endpoint-specific runtime")
	}
	target, ok := value.(*clientSOCKS5TargetRuntime)
	if !ok || target == nil {
		t.Fatalf("SOCKS5 target runtime has unexpected type %T", value)
	}
	if target.tunnelID != req.TunnelID || target.revision != req.Revision {
		t.Fatalf("SOCKS5 target runtime identity mismatch: %+v", target)
	}
	if target.config.DialTimeoutSeconds != 2 {
		t.Fatalf("SOCKS5 target config not preserved: %+v", target.config)
	}
}

func TestClientSOCKS5TargetStreamHeaderTransportPolicy(t *testing.T) {
	baseReq := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	baseReq.Spec.Target.Type = protocol.TargetTypeSOCKS5ConnectHandler
	baseReq.Spec.Target.Config = mustJSON(t, protocol.SOCKS5ConnectHandlerConfig{
		AllowedTargetCIDRs: []string{"127.0.0.0/8"},
		AllowedTargetHosts: []string{"127.0.0.1"},
		AllowedTargetPorts: []int{8080},
		DialTimeoutSeconds: 2,
	})
	baseHeader := protocol.DataStreamHeader{
		TunnelID:   baseReq.TunnelID,
		Revision:   baseReq.Revision,
		SourceRole: protocol.DataStreamRoleServer,
		TargetRole: protocol.DataStreamRoleTarget,
		Direction:  protocol.DataStreamDirectionIngressToTarget,
		Transport:  protocol.ActualTransportServerRelay,
		TargetHost: "127.0.0.1",
		TargetPort: 8080,
	}

	t.Run("server relay policy accepts server relay header", func(t *testing.T) {
		req := baseReq
		req.Spec.TransportPolicy = protocol.TransportPolicyServerRelayOnly
		target, err := newClientSOCKS5TargetRuntime(req)
		if err != nil {
			t.Fatalf("new SOCKS5 target runtime: %v", err)
		}
		if !dataStreamHeaderMatchesSOCKS5Target(baseHeader, target) {
			t.Fatal("server_relay_only SOCKS5 target should accept server-relay data stream headers")
		}
	})

	t.Run("direct only policy rejects server relay header", func(t *testing.T) {
		req := baseReq
		req.Spec.TransportPolicy = protocol.TransportPolicyDirectOnly
		target, err := newClientSOCKS5TargetRuntime(req)
		if err != nil {
			t.Fatalf("new SOCKS5 target runtime: %v", err)
		}
		if dataStreamHeaderMatchesSOCKS5Target(baseHeader, target) {
			t.Fatal("direct_only SOCKS5 target must reject server-relay data stream headers")
		}
	})

	t.Run("peer direct header remains rejected", func(t *testing.T) {
		req := baseReq
		req.Spec.TransportPolicy = protocol.TransportPolicyServerRelayOnly
		target, err := newClientSOCKS5TargetRuntime(req)
		if err != nil {
			t.Fatalf("new SOCKS5 target runtime: %v", err)
		}
		header := baseHeader
		header.Transport = protocol.ActualTransportPeerDirect
		if dataStreamHeaderMatchesSOCKS5Target(header, target) {
			t.Fatal("SOCKS5 target must reject peer-direct data stream headers in server-relay handler")
		}
	})
}

func TestClientTunnelUnprovisionDeletesSOCKS5TargetWithoutComparingUncomparableValue(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	req.Spec.Target.Type = protocol.TargetTypeSOCKS5ConnectHandler
	req.Spec.Target.Config = mustJSON(t, protocol.SOCKS5ConnectHandlerConfig{
		AllowedTargetCIDRs: []string{"127.0.0.0/8"},
		AllowedTargetHosts: []string{"localhost"},
		AllowedTargetPorts: []int{8080},
		DialTimeoutSeconds: 2,
	})

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("SOCKS5 target provision rejected: %s", ack.Message)
	}

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision - 1,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.socks5Targets.Load(req.TunnelID); !ok {
		t.Fatal("stale SOCKS5 target unprovision should not delete current runtime")
	}

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.socks5Targets.Load(req.TunnelID); ok {
		t.Fatal("matching SOCKS5 target unprovision should delete current runtime")
	}
}

func TestClientSOCKS5TargetPolicyDialResults(t *testing.T) {
	addr, port := startClientTestTCPEchoService(t)
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	req.Spec.Target.Type = protocol.TargetTypeSOCKS5ConnectHandler
	req.Spec.Target.Config = mustJSON(t, protocol.SOCKS5ConnectHandlerConfig{
		AllowedTargetCIDRs: []string{"127.0.0.0/8"},
		AllowedTargetHosts: []string{addr},
		AllowedTargetPorts: []int{port},
		DialTimeoutSeconds: 2,
	})
	target, err := newClientSOCKS5TargetRuntime(req)
	if err != nil {
		t.Fatalf("build SOCKS5 target runtime: %v", err)
	}

	conn, result := dialSOCKS5Target(protocol.DataStreamHeader{TargetHost: addr, TargetPort: port}, target)
	if result.Status != protocol.SOCKS5DialStatusSuccess {
		t.Fatalf("allowed target should dial successfully: %+v", result)
	}
	if conn == nil {
		t.Fatal("allowed target returned nil connection")
	}
	_ = conn.Close()

	_, result = dialSOCKS5Target(protocol.DataStreamHeader{TargetHost: addr, TargetPort: port + 1}, target)
	if result.Status != protocol.SOCKS5DialStatusTargetDenied {
		t.Fatalf("denied port should return target_denied, got %+v", result)
	}

	req.Spec.Target.Config = mustJSON(t, protocol.SOCKS5ConnectHandlerConfig{
		AllowedTargetCIDRs: []string{"192.0.2.0/24"},
		AllowedTargetHosts: []string{addr},
		AllowedTargetPorts: []int{port},
		DialTimeoutSeconds: 2,
	})
	target, err = newClientSOCKS5TargetRuntime(req)
	if err != nil {
		t.Fatalf("build restrictive SOCKS5 target runtime: %v", err)
	}
	_, result = dialSOCKS5Target(protocol.DataStreamHeader{TargetHost: addr, TargetPort: port}, target)
	if result.Status != protocol.SOCKS5DialStatusTargetDenied {
		t.Fatalf("denied resolved IP should return target_denied, got %+v", result)
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

func TestClientTunnelUnprovisionNewerRevisionClosesOlderIngressRuntime(t *testing.T) {
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
		Revision: req.Revision + 1,
		Role:     protocol.DataStreamRoleIngress,
	})
	assertTCPPortClosed(t, addr)
}

func TestClientTunnelUnprovisionIgnoresStaleTargetRevision(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	proxy := protocol.ProxyNewRequest{
		ID:                req.TunnelID,
		Name:              req.Spec.Name,
		Type:              protocol.ProxyTypeTCP,
		LocalIP:           "127.0.0.1",
		LocalPort:         8080,
		TransportPolicy:   req.Spec.TransportPolicy,
		ActualTransport:   protocol.ActualTransportServerRelay,
		ProvisionRevision: uint64(req.Revision),
	}
	c.proxies.Store(req.TunnelID, proxy)

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision - 1,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.proxies.Load(req.TunnelID); !ok {
		t.Fatal("stale target unprovision deleted current legacy proxy")
	}

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.proxies.Load(req.TunnelID); ok {
		t.Fatal("current target unprovision did not delete legacy proxy")
	}
}

func TestClientTunnelUnprovisionNewerRevisionDeletesOlderTargetProxy(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	proxy := protocol.ProxyNewRequest{
		ID:                req.TunnelID,
		Name:              req.Spec.Name,
		Type:              protocol.ProxyTypeTCP,
		LocalIP:           "127.0.0.1",
		LocalPort:         8080,
		TransportPolicy:   req.Spec.TransportPolicy,
		ActualTransport:   protocol.ActualTransportServerRelay,
		ProvisionRevision: uint64(req.Revision),
	}
	c.proxies.Store(req.TunnelID, proxy)

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision + 1,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.proxies.Load(req.TunnelID); ok {
		t.Fatal("newer target unprovision did not delete older legacy proxy")
	}
}

func TestClientTunnelUnprovisionDeletesLegacyProxyByTunnelID(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	proxy := protocol.ProxyNewRequest{
		ID:                req.TunnelID,
		Name:              req.Spec.Name,
		Type:              protocol.ProxyTypeTCP,
		LocalIP:           "127.0.0.1",
		LocalPort:         8080,
		TransportPolicy:   req.Spec.TransportPolicy,
		ActualTransport:   protocol.ActualTransportServerRelay,
		ProvisionRevision: uint64(req.Revision),
	}
	c.proxies.Store(proxy.Name, proxy)

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision - 1,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.proxies.Load(proxy.Name); !ok {
		t.Fatal("stale tunnel-id unprovision deleted legacy keyed proxy")
	}

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     protocol.DataStreamRoleTarget,
	})
	if _, ok := c.proxies.Load(proxy.Name); ok {
		t.Fatal("current tunnel-id unprovision did not delete legacy keyed proxy")
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

func TestClientTunnelPreflightSOCKS5UsesTCPBindResource(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	port := reserveClientTCPPort(t)
	config, err := json.Marshal(protocol.SOCKS5ListenConfig{
		BindIP:             "127.0.0.1",
		Port:               port,
		AllowedSourceCIDRs: []string{"127.0.0.0/8"},
		Auth:               protocol.SOCKS5AuthConfig{Type: protocol.SOCKS5AuthTypeNone},
	})
	if err != nil {
		t.Fatalf("marshal socks5 preflight config: %v", err)
	}

	resp := c.handleTunnelPreflight(protocol.TunnelPreflightRequest{
		RequestID: "req-socks5-ok",
		Role:      protocol.DataStreamRoleIngress,
		Ingress: protocol.EndpointSpec{
			Location: protocol.EndpointLocationClient,
			Type:     protocol.IngressTypeSOCKS5Listen,
			Config:   config,
		},
	})
	if !resp.Accepted || resp.Code != "" {
		t.Fatalf("free SOCKS5 tcp port preflight should pass: %+v", resp)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("occupy tcp port: %v", err)
	}
	defer mustClose(t, ln)

	resp = c.handleTunnelPreflight(protocol.TunnelPreflightRequest{
		RequestID: "req-socks5-busy",
		Role:      protocol.DataStreamRoleIngress,
		Ingress: protocol.EndpointSpec{
			Location: protocol.EndpointLocationClient,
			Type:     protocol.IngressTypeSOCKS5Listen,
			Config:   config,
		},
	})
	if resp.Accepted || resp.Code != protocol.TunnelMutationErrorCodeIngressPortInUse {
		t.Fatalf("SOCKS5 occupied tcp port preflight should fail with ingress_port_in_use: %+v", resp)
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

func TestClientIngressUDPRejectsDisallowedSourceBeforeAssociation(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	rt := &sessionRuntime{done: make(chan struct{})}
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleIngress, reserveClientTCPPort(t))
	sourcePolicy, err := parseIngressAccessPolicy([]string{"203.0.113.0/24"}, false)
	if err != nil {
		t.Fatalf("parse source policy: %v", err)
	}
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer mustClose(t, packetConn)
	runtime := &clientTunnelRuntime{
		packetConn:  packetConn,
		sourceCIDRs: sourcePolicy.sourceCIDRs,
		done:        make(chan struct{}),
	}

	for _, tc := range []struct {
		name string
		ip   string
	}{
		{name: "external", ip: "198.51.100.10"},
		{name: "loopback", ip: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srcAddr := &net.UDPAddr{IP: net.ParseIP(tc.ip), Port: 40000}
			c.handleIngressUDPDatagram(rt, req, runtime, srcAddr, []byte("blocked"))

			if got := runtime.udpAssociationCount.Load(); got != 0 {
				t.Fatalf("disallowed UDP source %s should not create association, got %d", tc.ip, got)
			}
			if _, ok := runtime.udpAssociations.Load(srcAddr.String()); ok {
				t.Fatalf("disallowed UDP source %s association should not be stored", tc.ip)
			}
		})
	}
}

func startClientTestTCPEchoService(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo service: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}
