package client

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestClientControlLoopLegacyProxyProvisionFixturesStillUseLegacyProxyStore(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		want     protocol.ProxyNewRequest
		revision uint64
	}{
		{
			name:    "tcp",
			fixture: "testdata/legacy_v0.1.8_proxy_provision_tcp.json",
			want: protocol.ProxyNewRequest{
				ID:              "legacy-flat-tcp-id",
				Name:            "legacy-flat-tcp",
				Type:            protocol.ProxyTypeTCP,
				LocalIP:         "127.0.0.1",
				LocalPort:       8080,
				RemotePort:      19090,
				TransportPolicy: protocol.TransportPolicyServerRelayOnly,
				ActualTransport: protocol.ActualTransportUnknown,
			},
			revision: 8,
		},
		{
			name:    "udp",
			fixture: "testdata/legacy_v0.1.8_proxy_provision_udp.json",
			want: protocol.ProxyNewRequest{
				ID:              "legacy-flat-udp-id",
				Name:            "legacy-flat-udp",
				Type:            protocol.ProxyTypeUDP,
				LocalIP:         "127.0.0.1",
				LocalPort:       5353,
				RemotePort:      19091,
				TransportPolicy: protocol.TransportPolicyServerRelayOnly,
				ActualTransport: protocol.ActualTransportUnknown,
			},
			revision: 9,
		},
		{
			name:    "tcp bound server relay",
			fixture: "testdata/legacy_v0.1.8_proxy_provision_tcp_bound.json",
			want: protocol.ProxyNewRequest{
				ID:              "legacy-flat-tcp-bound-id",
				Name:            "legacy-flat-tcp-bound",
				Type:            protocol.ProxyTypeTCP,
				LocalIP:         "127.0.0.1",
				LocalPort:       8083,
				RemotePort:      19092,
				BindIP:          "127.0.0.1",
				TransportPolicy: protocol.TransportPolicyServerRelayOnly,
				ActualTransport: protocol.ActualTransportServerRelay,
				BandwidthSettings: protocol.BandwidthSettings{
					IngressBPS: 4096,
					EgressBPS:  8192,
				},
			},
			revision: 12,
		},
		{
			name:    "udp server relay",
			fixture: "testdata/legacy_v0.1.8_proxy_provision_udp_relay.json",
			want: protocol.ProxyNewRequest{
				ID:              "legacy-flat-udp-relay-id",
				Name:            "legacy-flat-udp-relay",
				Type:            protocol.ProxyTypeUDP,
				LocalIP:         "127.0.0.1",
				LocalPort:       5354,
				RemotePort:      19093,
				TransportPolicy: protocol.TransportPolicyServerRelayOnly,
				ActualTransport: protocol.ActualTransportServerRelay,
			},
			revision: 13,
		},
		{
			name:    "http",
			fixture: "testdata/legacy_v0.1.8_proxy_provision_http.json",
			want: protocol.ProxyNewRequest{
				ID:              "legacy-flat-http-id",
				Name:            "legacy-flat-http",
				Type:            protocol.ProxyTypeHTTP,
				LocalIP:         "127.0.0.1",
				LocalPort:       8081,
				Domain:          "legacy.example.test",
				TransportPolicy: protocol.TransportPolicyServerRelayOnly,
				ActualTransport: protocol.ActualTransportUnknown,
			},
			revision: 10,
		},
		{
			name:    "http full fields",
			fixture: "testdata/legacy_v0.1.8_proxy_provision_http_full.json",
			want: protocol.ProxyNewRequest{
				ID:              "legacy-flat-http-full-id",
				Name:            "legacy-flat-http-full",
				Type:            protocol.ProxyTypeHTTP,
				LocalIP:         "127.0.0.1",
				LocalPort:       8082,
				BindIP:          "127.0.0.1",
				Domain:          "legacy-full.example.test",
				RemotePort:      0,
				TransportPolicy: protocol.TransportPolicyServerRelayOnly,
				ActualTransport: protocol.ActualTransportServerRelay,
				BandwidthSettings: protocol.BandwidthSettings{
					IngressBPS: 1024,
					EgressBPS:  2048,
				},
			},
			revision: 11,
		},
		{
			name:    "tcp unknown field ignored",
			fixture: "testdata/legacy_v0.1.8_proxy_provision_tcp_unknown_field.json",
			want: protocol.ProxyNewRequest{
				ID:              "legacy-flat-tcp-unknown-id",
				Name:            "legacy-flat-tcp-unknown",
				Type:            protocol.ProxyTypeTCP,
				LocalIP:         "127.0.0.1",
				LocalPort:       8084,
				RemotePort:      19094,
				TransportPolicy: protocol.TransportPolicyServerRelayOnly,
				ActualTransport: protocol.ActualTransportUnknown,
			},
			revision: 14,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provisionAck := make(chan protocol.ProxyProvisionAck, 1)
			ackErr := make(chan error, 1)
			ms := newMockServer(true)
			ms.onMessage = func(msg protocol.Message) *protocol.Message {
				if msg.Type != protocol.MsgTypeProxyProvisionAck {
					return nil
				}
				var ack protocol.ProxyProvisionAck
				if err := msg.ParsePayload(&ack); err != nil {
					ackErr <- err
					return nil
				}
				provisionAck <- ack
				return nil
			}
			ts := newMockHTTPServer(ms)
			defer ts.Close()

			c := newIsolatedTestClient(t, "ws"+ts.URL[len("http"):], "test-key")
			c.DisableReconnect = true

			go func() { _ = c.Start() }()
			conn := ms.waitForConn(t, 2*time.Second)

			// These fixtures are hand-crafted from the v0.1.8 ProxyNewRequest
			// schema and dual-dispatch code, not captured from a live server.
			payload, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatalf("read legacy fixture: %v", err)
			}
			var fixture map[string]json.RawMessage
			if err := json.Unmarshal(payload, &fixture); err != nil {
				t.Fatalf("decode legacy fixture: %v", err)
			}
			if _, exists := fixture["tunnel_id"]; exists {
				t.Fatal("legacy flat fixture must not include tunnel_id; that would exercise unified dispatch")
			}
			msg := protocol.Message{
				Type:    protocol.MsgTypeProxyProvision,
				Payload: json.RawMessage(payload),
			}
			if err := ms.writeControlJSON(conn, msg); err != nil {
				t.Fatalf("server failed to send legacy proxy_provision: %v", err)
			}

			select {
			case err := <-ackErr:
				t.Fatalf("failed to parse legacy proxy_provision_ack: %v", err)
			case ack := <-provisionAck:
				if !ack.Accepted {
					t.Fatalf("legacy proxy provision should be accepted: %+v", ack)
				}
				if ack.Name != tc.want.Name {
					t.Fatalf("legacy ack name: got %q", ack.Name)
				}
				if ack.ProvisionRevision != tc.revision {
					t.Fatalf("legacy ack revision: got %d", ack.ProvisionRevision)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("did not receive legacy proxy_provision_ack")
			}

			value, ok := c.proxies.Load(tc.want.Name)
			if !ok {
				t.Fatal("legacy flat provision should be stored in c.proxies by name")
			}
			cfg, ok := value.(protocol.ProxyNewRequest)
			if !ok {
				t.Fatalf("legacy proxy cache entry has unexpected type %T", value)
			}
			if cfg.ID != tc.want.ID ||
				cfg.Type != tc.want.Type ||
				cfg.LocalIP != tc.want.LocalIP ||
				cfg.LocalPort != tc.want.LocalPort ||
				cfg.BindIP != tc.want.BindIP ||
				cfg.RemotePort != tc.want.RemotePort ||
				cfg.Domain != tc.want.Domain ||
				cfg.TransportPolicy != tc.want.TransportPolicy ||
				cfg.ActualTransport != tc.want.ActualTransport ||
				cfg.IngressBPS != tc.want.IngressBPS ||
				cfg.EgressBPS != tc.want.EgressBPS {
				t.Fatalf("legacy proxy cache entry mismatch: %+v", cfg)
			}
			if _, ok := c.proxies.Load(tc.want.ID); ok {
				t.Fatal("legacy flat provision must not be re-keyed into unified tunnel id storage")
			}
		})
	}
}

func TestClientControlLoopLegacyProxyCloseFixtureDeletesLegacyProxyStore(t *testing.T) {
	ms := newMockServer(true)
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	c := newIsolatedTestClient(t, "ws"+ts.URL[len("http"):], "test-key")
	c.DisableReconnect = true
	c.proxies.Store("legacy-flat-tcp", protocol.ProxyNewRequest{
		ID:   "legacy-flat-tcp-id",
		Name: "legacy-flat-tcp",
		Type: protocol.ProxyTypeTCP,
	})
	c.proxies.Store("unified-shadow-id", protocol.ProxyNewRequest{
		ID:   "unified-shadow-id",
		Name: "unified-shadow",
		Type: protocol.ProxyTypeTCP,
	})

	errCh := make(chan error, 1)
	go func() { errCh <- c.Start() }()
	t.Cleanup(func() {
		c.Shutdown()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Error("client did not stop within 2s")
		}
	})
	conn := ms.waitForConn(t, 2*time.Second)
	ms.waitForMessage(t, 2*time.Second, protocol.MsgTypeAuth)
	waitForClientCondition(t, 2*time.Second, func() bool {
		return c.CurrentClientID() == "mock_client_1"
	})

	payload, err := os.ReadFile("testdata/legacy_v0.1.8_proxy_close.json")
	if err != nil {
		t.Fatalf("read legacy close fixture: %v", err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatalf("decode legacy close fixture: %v", err)
	}
	if _, exists := fixture["tunnel_id"]; exists {
		t.Fatal("legacy close fixture must not include tunnel_id; that would exercise unified unprovision")
	}
	msg := protocol.Message{
		Type:    protocol.MsgTypeProxyClose,
		Payload: json.RawMessage(payload),
	}
	if err := ms.writeControlJSON(conn, msg); err != nil {
		t.Fatalf("server failed to send legacy proxy_close: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := c.proxies.Load("legacy-flat-tcp"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("legacy proxy_close did not delete c.proxies entry by name")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := c.proxies.Load("unified-shadow-id"); !ok {
		t.Fatal("legacy proxy_close must not delete unrelated tunnel-id keyed entry")
	}
}

func TestClientControlLoopUnifiedPayloadIgnoresLegacyFlatFields(t *testing.T) {
	cases := []struct {
		name   string
		spec   protocol.TunnelSpec
		assert func(t *testing.T, c *Client, spec protocol.TunnelSpec)
	}{
		{
			name: "tcp target",
			spec: mixedPayloadTunnelSpec(t, "split-tcp", protocol.IngressTypeTCPListen, protocol.TargetTypeTCPService, map[string]any{
				"bind_ip": "0.0.0.0",
				"port":    19091,
			}, map[string]any{
				"host": "127.0.0.1",
				"port": 8080,
			}),
			assert: func(t *testing.T, c *Client, spec protocol.TunnelSpec) {
				t.Helper()
				if _, ok := c.fixedTargetRuntimes.Load(spec.ID); !ok {
					t.Fatal("mixed TCP payload with tunnel_id must create unified fixed target runtime")
				}
			},
		},
		{
			name: "udp target",
			spec: mixedPayloadTunnelSpec(t, "split-udp", protocol.IngressTypeUDPListen, protocol.TargetTypeUDPService, map[string]any{
				"bind_ip": "0.0.0.0",
				"port":    19093,
			}, map[string]any{
				"host": "127.0.0.1",
				"port": 8081,
			}),
			assert: func(t *testing.T, c *Client, spec protocol.TunnelSpec) {
				t.Helper()
				if _, ok := c.fixedTargetRuntimes.Load(spec.ID); !ok {
					t.Fatal("mixed UDP payload with tunnel_id must create unified fixed target runtime")
				}
			},
		},
		{
			name: "socks5 target",
			spec: mixedPayloadTunnelSpec(t, "split-socks5", protocol.IngressTypeSOCKS5Listen, protocol.TargetTypeSOCKS5ConnectHandler, map[string]any{
				"bind_ip":              "0.0.0.0",
				"port":                 19094,
				"auth":                 map[string]any{"type": "none"},
				"allowed_source_cidrs": []string{"0.0.0.0/0", "::/0"},
			}, protocol.SOCKS5ConnectHandlerConfig{
				AllowedTargetCIDRs: []string{"127.0.0.0/8"},
				AllowedTargetHosts: []string{"127.0.0.1"},
				AllowedTargetPorts: []int{8080},
				DialTimeoutSeconds: 2,
			}),
			assert: func(t *testing.T, c *Client, spec protocol.TunnelSpec) {
				t.Helper()
				if _, ok := c.socks5Targets.Load(spec.ID); !ok {
					t.Fatal("mixed SOCKS5 payload with tunnel_id must create unified SOCKS5 target runtime")
				}
				if _, ok := c.fixedTargetRuntimes.Load(spec.ID); ok {
					t.Fatal("mixed SOCKS5 payload with tunnel_id must not create fixed target runtime")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provisionAck := make(chan protocol.TunnelProvisionAck, 1)
			ackErr := make(chan error, 1)
			ms := newMockServer(true)
			ms.onMessage = func(msg protocol.Message) *protocol.Message {
				if msg.Type != protocol.MsgTypeTunnelProvisionAck {
					return nil
				}
				var ack protocol.TunnelProvisionAck
				if err := msg.ParsePayload(&ack); err != nil {
					ackErr <- err
					return nil
				}
				provisionAck <- ack
				return nil
			}
			ts := newMockHTTPServer(ms)
			defer ts.Close()

			c := newIsolatedTestClient(t, "ws"+ts.URL[len("http"):], "test-key")
			c.DisableReconnect = true

			go func() { _ = c.Start() }()
			conn := ms.waitForConn(t, 2*time.Second)
			spec := tc.spec
			const legacyShadowName = "legacy-shadow"
			const legacyShadowID = "legacy-shadow-id"
			payload := mustJSON(t, map[string]any{
				// Legacy flat fields are deliberately contradictory. Presence of
				// tunnel_id must make the client use the unified payload shape.
				"id":          legacyShadowID,
				"name":        legacyShadowName,
				"type":        protocol.ProxyTypeTCP,
				"local_ip":    "192.0.2.200",
				"local_port":  6553,
				"remote_port": 19092,
				"tunnel_id":   spec.ID,
				"revision":    spec.Revision,
				"role":        protocol.DataStreamRoleTarget,
				"spec":        spec,
			})
			msg := protocol.Message{
				Type:    protocol.MsgTypeProxyProvision,
				Payload: json.RawMessage(payload),
			}
			if err := ms.writeControlJSON(conn, msg); err != nil {
				t.Fatalf("server failed to send mixed unified proxy_provision: %v", err)
			}

			select {
			case err := <-ackErr:
				t.Fatalf("failed to parse tunnel_provision_ack: %v", err)
			case ack := <-provisionAck:
				if !ack.Accepted {
					t.Fatalf("unified tunnel provision should be accepted: %+v", ack)
				}
				if ack.TunnelID != spec.ID || ack.Revision != spec.Revision || ack.Role != protocol.DataStreamRoleTarget {
					t.Fatalf("unified ack identity mismatch: %+v", ack)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("did not receive tunnel_provision_ack")
			}

			if _, ok := c.proxies.Load(legacyShadowName); ok {
				t.Fatal("mixed payload with tunnel_id must not fall back to legacy flat proxy store")
			}
			if _, ok := c.proxies.Load(spec.ID); ok {
				t.Fatal("unified target provision must not write ProxyNewRequest into legacy c.proxies")
			}
			tc.assert(t, c, spec)
		})
	}
}

func mixedPayloadTunnelSpec(t *testing.T, idSuffix, ingressType, targetType string, ingressConfig, targetConfig any) protocol.TunnelSpec {
	t.Helper()
	return protocol.TunnelSpec{
		ID:              "split-tunnel-" + idSuffix,
		Name:            "split-tunnel-" + idSuffix,
		Revision:        11,
		Topology:        protocol.TunnelTopologyServerExpose,
		OwnerClientID:   "target-client",
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		Ingress: protocol.EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     ingressType,
			Config:   mustJSON(t, ingressConfig),
		},
		Target: protocol.EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     targetType,
			Config:   mustJSON(t, targetConfig),
		},
	}
}

func TestClientControlLoopRejectedUnifiedPayloadDoesNotFallBackToLegacyProxyStore(t *testing.T) {
	provisionAck := make(chan protocol.TunnelProvisionAck, 1)
	ackErr := make(chan error, 1)
	ms := newMockServer(true)
	ms.onMessage = func(msg protocol.Message) *protocol.Message {
		if msg.Type != protocol.MsgTypeTunnelProvisionAck {
			return nil
		}
		var ack protocol.TunnelProvisionAck
		if err := msg.ParsePayload(&ack); err != nil {
			ackErr <- err
			return nil
		}
		provisionAck <- ack
		return nil
	}
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	c := newIsolatedTestClient(t, "ws"+ts.URL[len("http"):], "test-key")
	c.DisableReconnect = true

	go func() { _ = c.Start() }()
	conn := ms.waitForConn(t, 2*time.Second)
	spec := mixedPayloadTunnelSpec(t, "split-unsupported", protocol.IngressTypeTCPListen, "future_target", map[string]any{
		"bind_ip": "0.0.0.0",
		"port":    19091,
	}, map[string]any{
		"host": "127.0.0.1",
		"port": 8080,
	})
	payload := mustJSON(t, map[string]any{
		"id":          "legacy-reject-shadow-id",
		"name":        "legacy-reject-shadow",
		"type":        protocol.ProxyTypeTCP,
		"local_ip":    "192.0.2.201",
		"local_port":  6554,
		"remote_port": 19093,
		"tunnel_id":   spec.ID,
		"revision":    spec.Revision,
		"role":        protocol.DataStreamRoleTarget,
		"spec":        spec,
	})
	msg := protocol.Message{
		Type:    protocol.MsgTypeProxyProvision,
		Payload: json.RawMessage(payload),
	}
	if err := ms.writeControlJSON(conn, msg); err != nil {
		t.Fatalf("server failed to send rejected mixed unified proxy_provision: %v", err)
	}

	select {
	case err := <-ackErr:
		t.Fatalf("failed to parse tunnel_provision_ack: %v", err)
	case ack := <-provisionAck:
		if ack.Accepted {
			t.Fatalf("unsupported unified target should be rejected: %+v", ack)
		}
		if ack.TunnelID != spec.ID || ack.Revision != spec.Revision || ack.Role != protocol.DataStreamRoleTarget {
			t.Fatalf("unified reject ack identity mismatch: %+v", ack)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive tunnel_provision_ack")
	}
	if _, ok := c.proxies.Load("legacy-reject-shadow"); ok {
		t.Fatal("rejected mixed payload with tunnel_id must not fall back to legacy flat proxy store")
	}
	if _, ok := c.proxies.Load(spec.ID); ok {
		t.Fatal("rejected unified target provision must not write ProxyNewRequest into legacy c.proxies")
	}
	if _, ok := c.fixedTargetRuntimes.Load(spec.ID); ok {
		t.Fatal("rejected unified target provision must not create fixed target runtime")
	}
	if _, ok := c.socks5Targets.Load(spec.ID); ok {
		t.Fatal("rejected unified target provision must not create SOCKS5 target runtime")
	}
}

func TestClientControlLoopMalformedUnifiedPayloadDoesNotFallBackToLegacyProxyStore(t *testing.T) {
	provisionAck := make(chan protocol.TunnelProvisionAck, 1)
	ms := newMockServer(true)
	ms.onMessage = func(msg protocol.Message) *protocol.Message {
		if msg.Type == protocol.MsgTypeTunnelProvisionAck {
			var ack protocol.TunnelProvisionAck
			if err := msg.ParsePayload(&ack); err == nil {
				provisionAck <- ack
			}
		}
		return nil
	}
	ts := newMockHTTPServer(ms)
	defer ts.Close()

	c := newIsolatedTestClient(t, "ws"+ts.URL[len("http"):], "test-key")
	c.DisableReconnect = true

	go func() { _ = c.Start() }()
	conn := ms.waitForConn(t, 2*time.Second)
	payload := mustJSON(t, map[string]any{
		"id":          "legacy-malformed-shadow-id",
		"name":        "legacy-malformed-shadow",
		"type":        protocol.ProxyTypeTCP,
		"local_ip":    "192.0.2.202",
		"local_port":  6555,
		"remote_port": 19094,
		"tunnel_id":   "malformed-unified-id",
		"revision":    "not-a-number",
		"role":        protocol.DataStreamRoleTarget,
		"spec":        map[string]any{"id": "malformed-unified-id"},
	})
	msg := protocol.Message{
		Type:    protocol.MsgTypeProxyProvision,
		Payload: json.RawMessage(payload),
	}
	if err := ms.writeControlJSON(conn, msg); err != nil {
		t.Fatalf("server failed to send malformed mixed unified proxy_provision: %v", err)
	}

	select {
	case ack := <-provisionAck:
		t.Fatalf("malformed unified payload should not produce ACK or fall back: %+v", ack)
	case <-time.After(200 * time.Millisecond):
	}
	if _, ok := c.proxies.Load("legacy-malformed-shadow"); ok {
		t.Fatal("malformed mixed payload with tunnel_id must not fall back to legacy flat proxy store")
	}
	if _, ok := c.proxies.Load("malformed-unified-id"); ok {
		t.Fatal("malformed unified provision must not write ProxyNewRequest into legacy c.proxies")
	}
	if _, ok := c.fixedTargetRuntimes.Load("malformed-unified-id"); ok {
		t.Fatal("malformed unified provision must not create fixed target runtime")
	}
	if _, ok := c.socks5Targets.Load("malformed-unified-id"); ok {
		t.Fatal("malformed unified provision must not create SOCKS5 target runtime")
	}
}
