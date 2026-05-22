package client

import (
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

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
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("listener was not reachable: %v", err)
	}
	mustClose(t, conn)

	c.handleTunnelUnprovision(protocol.TunnelUnprovisionRequest{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     protocol.DataStreamRoleIngress,
	})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn, err = net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("listener still accepts connections after unprovision")
}
