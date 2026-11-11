package client

import (
	"net"
	"testing"
	"time"

	"netsgo/internal/tunnelloop"
	"netsgo/pkg/protocol"
)

func TestTargetLoopRejectsProvisionOnlyWithSharedNamespace(t *testing.T) {
	for _, tc := range []struct {
		name, ingressID string
		accepted        bool
	}{
		{"same namespace", "client-network", false},
		{"different namespace", "remote-network", true},
		{"old server has no evidence", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New("http://localhost:9527", "key")
			c.NetworkNamespaceID = "client-network"
			req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, 13200)
			req.IngressNetworkID = tc.ingressID
			req.Spec.Ingress.Type = protocol.IngressTypeTCPListen
			req.Spec.Ingress.Config = mustJSON(t, map[string]any{"bind_ip": "0.0.0.0", "port": 13200})
			req.Spec.Target.Config = mustJSON(t, map[string]any{"host": "127.0.0.1", "port": 13200})
			ack := c.handleTunnelProvision(&sessionRuntime{}, req)
			if ack.Accepted != tc.accepted {
				t.Fatalf("ack=%+v", ack)
			}
			if _, ok := c.fixedTargetRuntimes.Load(req.TunnelID); ok != tc.accepted {
				t.Fatal("rejected runtime installed")
			}
		})
	}
}

func TestResolvedLoopIsRejectedBeforeTCPConnect(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	c := New("http://localhost:9527", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, port)
	req.Spec.Ingress.Config = mustJSON(t, map[string]any{"bind_ip": "0.0.0.0", "port": port})
	target, err := newFixedServiceTargetRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	target.host = "localhost"
	target.port = port
	target.loopGuard = tunnelloop.New(req.Spec, "same", "same")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c.handleFixedTargetStream(a, testDataStreamHeader(req.TunnelID), target, nil)
	_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(20 * time.Millisecond))
	if conn, err := ln.Accept(); err == nil {
		_ = conn.Close()
		t.Fatal("loop dial reached the TCP listener")
	}
}
