package client

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestRuntimeSnapshotReflectsUnifiedTunnelsAndEmitEvent(t *testing.T) {
	var buf bytes.Buffer
	c := New("https://netsgo.example.com:9527", "")
	c.Logger = NewEventLogger(LogFormatJSON, &buf)
	c.ClientID = "cli_123"
	c.publicIPv4 = "203.0.113.10"

	ingRuntime := &clientTunnelRuntime{
		tunnelID:        "tun_ing",
		revision:        3,
		role:            protocol.DataStreamRoleIngress,
		name:            "ssh-dev",
		topology:        protocol.TunnelTopologyClientToClient,
		ingressType:     protocol.IngressTypeTCPListen,
		targetType:      protocol.TargetTypeTCPService,
		listenAddr:      "127.0.0.1:2222",
		transportPolicy: protocol.TransportPolicyDirectPreferred,
		done:            make(chan struct{}),
	}
	ingRuntime.tcpConnCount.Store(2)
	c.tunnels.Store(tunnelRuntimeKey("tun_ing", protocol.DataStreamRoleIngress), ingRuntime)

	c.fixedTargetRuntimes.Store("tun_tgt", &fixedServiceTargetRuntime{
		tunnelID:        "tun_tgt",
		revision:        5,
		name:            "web-local",
		topology:        protocol.TunnelTopologyServerExpose,
		ingressType:     protocol.IngressTypeHTTPHost,
		targetType:      protocol.TargetTypeTCPService,
		host:            "127.0.0.1",
		port:            8080,
		transportPolicy: protocol.TransportPolicyServerRelayOnly,
	})

	snap := c.RuntimeSnapshot()
	if snap.ClientID != "cli_123" {
		t.Fatalf("ClientID = %q, want cli_123", snap.ClientID)
	}
	if snap.PublicIPv4 != "203.0.113.10" {
		t.Fatalf("PublicIPv4 = %q, want 203.0.113.10", snap.PublicIPv4)
	}
	if len(snap.Tunnels) != 2 {
		t.Fatalf("len(Tunnels) = %d, want 2", len(snap.Tunnels))
	}
	if snap.Tunnels[0].Name != "ssh-dev" || snap.Tunnels[0].ActiveConnections != 2 || snap.Tunnels[0].LocalEndpoint != "127.0.0.1:2222" {
		t.Fatalf("unexpected ingress tunnel snapshot: %+v", snap.Tunnels[0])
	}
	if snap.Tunnels[1].Name != "web-local" || snap.Tunnels[1].LocalEndpoint != "127.0.0.1:8080" {
		t.Fatalf("unexpected target tunnel snapshot: %+v", snap.Tunnels[1])
	}

	c.emitSidecarSnapshot("unit_test")
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected emitted JSON line")
	}
	var ev struct {
		Event  string `json:"event"`
		Fields struct {
			Reason   string          `json:"reason"`
			Snapshot SidecarSnapshot `json:"snapshot"`
		} `json:"fields"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal snapshot event: %v\n%s", err, line)
	}
	if ev.Event != "client.runtime_snapshot" || ev.Fields.Reason != "unit_test" {
		t.Fatalf("unexpected event wrapper: %+v", ev)
	}
	if len(ev.Fields.Snapshot.Tunnels) != 2 {
		t.Fatalf("snapshot tunnels count = %d, want 2", len(ev.Fields.Snapshot.Tunnels))
	}
}

func TestRuntimeSnapshotWithoutTunnelsEmitsEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	c := New("https://netsgo.example.com:9527", "")
	c.Logger = NewEventLogger(LogFormatJSON, &buf)
	c.startTime = time.Now().Add(-65 * time.Second)
	c.emitSidecarSnapshot("connected")

	var event struct {
		Fields struct {
			Snapshot struct {
				UptimeSeconds int64           `json:"uptime_seconds"`
				Tunnels       json.RawMessage `json:"tunnels"`
			} `json:"snapshot"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(buf.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	// Rust's Vec decoder requires an array; null would discard the entire snapshot.
	if string(event.Fields.Snapshot.Tunnels) != "[]" {
		t.Fatalf("tunnels = %s, want [] for desktop snapshot decoding", event.Fields.Snapshot.Tunnels)
	}
	if event.Fields.Snapshot.UptimeSeconds < 65 {
		t.Fatalf("uptime_seconds = %d, want at least 65", event.Fields.Snapshot.UptimeSeconds)
	}
}
