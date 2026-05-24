package server

import (
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestTunnelRegistryRejectsStaleProvisionRevisionAck(t *testing.T) {
	tr := newTunnelRegistry()
	client := &ClientConn{ID: "client-stale", generation: 7}

	ch, err := tr.registerProvisionAckWaiter(client, "rev-tunnel", 2, "")
	if err != nil {
		t.Fatalf("register waiter failed: %v", err)
	}

	if tr.resolveProvisionAckWaiter(client.ID, client.generation, provisionAckResult{
		name:     "rev-tunnel",
		revision: 1,
		accepted: true,
	}) {
		t.Fatal("stale revision ack should not resolve the current waiter")
	}

	select {
	case <-ch:
		t.Fatal("stale revision ack must not deliver on the waiter channel")
	default:
	}

	if !tr.resolveProvisionAckWaiter(client.ID, client.generation, provisionAckResult{
		name:     "rev-tunnel",
		revision: 2,
		accepted: true,
	}) {
		t.Fatal("current revision ack should resolve the waiter")
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			t.Fatal("current ack should deliver a result before closing")
		}
		if resp.revision != 2 || !resp.accepted {
			t.Fatalf("unexpected ack result: %+v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for current revision ack")
	}
}

func TestTunnelRegistryRejectsWrongProvisionRoleAck(t *testing.T) {
	tr := newTunnelRegistry()
	client := &ClientConn{ID: "client-role", generation: 8}

	ch, err := tr.registerProvisionAckWaiter(client, "role-tunnel", 3, protocol.DataStreamRoleTarget)
	if err != nil {
		t.Fatalf("register waiter failed: %v", err)
	}

	if tr.resolveProvisionAckWaiter(client.ID, client.generation, provisionAckResult{
		name:     "role-tunnel",
		revision: 3,
		role:     protocol.DataStreamRoleIngress,
		accepted: true,
	}) {
		t.Fatal("wrong role ack should not resolve the current waiter")
	}

	select {
	case <-ch:
		t.Fatal("wrong role ack must not deliver on the waiter channel")
	default:
	}

	if !tr.resolveProvisionAckWaiter(client.ID, client.generation, provisionAckResult{
		name:     "role-tunnel",
		revision: 3,
		role:     protocol.DataStreamRoleTarget,
		accepted: true,
	}) {
		t.Fatal("matching role ack should resolve the waiter")
	}
}

func TestTunnelRegistryCancelsPreflightWaitersForClientGeneration(t *testing.T) {
	tr := newTunnelRegistry()
	client := &ClientConn{ID: "client-preflight-cancel", generation: 11}

	ch, err := tr.registerPreflightWaiter(client, "req-cancel")
	if err != nil {
		t.Fatalf("register preflight waiter failed: %v", err)
	}
	tr.cancelPreflightWaiters(client.ID, client.generation)

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("cancelled preflight waiter channel should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for preflight waiter cancellation")
	}
}

func TestTunnelPreflightResponseRequiresTunnelRevisionAndRoleMatch(t *testing.T) {
	req := protocol.TunnelPreflightRequest{
		RequestID: "req-1",
		TunnelID:  "tun-1",
		Revision:  3,
		Role:      protocol.DataStreamRoleIngress,
	}
	valid := protocol.TunnelPreflightResponse{
		RequestID: req.RequestID,
		TunnelID:  req.TunnelID,
		Revision:  req.Revision,
		Role:      req.Role,
		Accepted:  true,
	}
	if err := validateTunnelPreflightResponse(req, valid); err != nil {
		t.Fatalf("valid preflight response rejected: %v", err)
	}

	for name, mutate := range map[string]func(*protocol.TunnelPreflightResponse){
		"tunnel":   func(resp *protocol.TunnelPreflightResponse) { resp.TunnelID = "other" },
		"revision": func(resp *protocol.TunnelPreflightResponse) { resp.Revision++ },
		"role":     func(resp *protocol.TunnelPreflightResponse) { resp.Role = protocol.DataStreamRoleTarget },
	} {
		t.Run(name, func(t *testing.T) {
			resp := valid
			mutate(&resp)
			if err := validateTunnelPreflightResponse(req, resp); err == nil {
				t.Fatal("mismatched preflight response should be rejected")
			}
		})
	}
}

func TestPrepareTunnelProvisionRequestAssignsRevisionAndPendingRuntime(t *testing.T) {
	s := New(0)
	client := &ClientConn{
		ID:      "client-runtime",
		proxies: make(map[string]*ProxyTunnel),
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "runtime-tunnel",
			Type:         protocol.ProxyTypeTCP,
			RemotePort:   18080,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStatePending,
		},
		done: make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	req := s.prepareTunnelProvisionRequest(client, tunnel)
	if req.Name != tunnel.Config.Name {
		t.Fatalf("provision request name = %q, want %q", req.Name, tunnel.Config.Name)
	}
	if req.ProvisionRevision == 0 {
		t.Fatal("provision request should carry a non-zero revision")
	}
	if tunnel.runtime.Revision != req.ProvisionRevision {
		t.Fatalf("runtime revision = %d, request revision = %d", tunnel.runtime.Revision, req.ProvisionRevision)
	}
	if tunnel.runtime.Target.ClientID != client.ID {
		t.Fatalf("target runtime client_id = %q, want %q", tunnel.runtime.Target.ClientID, client.ID)
	}
	if got := aggregateTunnelRuntimeState(tunnel.runtime); got != protocol.ProxyRuntimeStatePending {
		t.Fatalf("aggregate runtime state = %q, want pending", got)
	}
}

func TestTunnelRuntimeActiveDoesNotRequireTargetHealth(t *testing.T) {
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "active-tunnel",
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStatePending,
		},
	}

	markTunnelServerRelayActive(tunnel, "target-client", time.Now())

	if got := aggregateTunnelRuntimeState(tunnel.runtime); got != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("aggregate runtime state = %q, want exposed", got)
	}
	if tunnel.runtime.Target.State != tunnelParticipantStateTargetReady {
		t.Fatalf("target participant state = %q, want %q", tunnel.runtime.Target.State, tunnelParticipantStateTargetReady)
	}
	if tunnel.runtime.Transport.State != tunnelTransportStateServerRelay {
		t.Fatalf("transport state = %q, want %q", tunnel.runtime.Transport.State, tunnelTransportStateServerRelay)
	}
}
