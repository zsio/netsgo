package server

import (
	"errors"
	"net"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestHasStoredTunnelForEvent(t *testing.T) {
	s := New(0)
	storedAt := time.Now().UTC()
	stored := testStoredServerExposeTCPTunnel("stored-event-id", "stored-event", "client-a", 8080, 18080, storedAt)

	if s.hasStoredTunnelForEvent("client-a", storedTunnelToProxyConfig(stored)) {
		t.Fatal("server without a tunnel store should suppress tunnel events")
	}

	s.store = newTestTunnelStore(t)
	mustAddStableTunnel(t, s.store, stored)

	tests := []struct {
		name     string
		clientID string
		config   protocol.ProxyConfig
		want     bool
	}{
		{
			name:     "owner id match",
			clientID: "ignored-client",
			config: protocol.ProxyConfig{
				ID:            stored.ID,
				Name:          stored.Name,
				OwnerClientID: stored.OwnerClientID,
			},
			want: true,
		},
		{
			name:     "client id fallback",
			clientID: "ignored-client",
			config: protocol.ProxyConfig{
				ID:       stored.ID,
				Name:     stored.Name,
				ClientID: stored.ClientID,
			},
			want: true,
		},
		{
			name:     "event client id fallback",
			clientID: stored.ClientID,
			config: protocol.ProxyConfig{
				ID:   stored.ID,
				Name: stored.Name,
			},
			want: true,
		},
		{
			name:     "name fallback after missing id",
			clientID: stored.ClientID,
			config: protocol.ProxyConfig{
				ID:            "missing-id",
				Name:          stored.Name,
				OwnerClientID: stored.OwnerClientID,
			},
			want: true,
		},
		{
			name:     "runtime only id and name",
			clientID: stored.ClientID,
			config: protocol.ProxyConfig{
				ID:            "runtime-id",
				Name:          "runtime-only",
				OwnerClientID: stored.OwnerClientID,
			},
			want: false,
		},
		{
			name:     "wrong owner suppresses",
			clientID: stored.ClientID,
			config: protocol.ProxyConfig{
				ID:            stored.ID,
				Name:          stored.Name,
				OwnerClientID: "other-client",
			},
			want: false,
		},
		{
			name:     "empty identity suppresses",
			clientID: "",
			config:   protocol.ProxyConfig{Name: stored.Name},
			want:     false,
		},
		{
			name:     "empty id and name suppresses",
			clientID: stored.ClientID,
			config:   protocol.ProxyConfig{OwnerClientID: stored.OwnerClientID},
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.hasStoredTunnelForEvent(tc.clientID, tc.config); got != tc.want {
				t.Fatalf("hasStoredTunnelForEvent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEmitTunnelChangedIfStoredSuppressesRuntimeOnlyTunnels(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	now := time.Now().UTC()
	runtimeOnly := testRuntimeOnlyProxyTunnel("runtime-event-id", "runtime-only-event", "client-a", 8081, 18081, now)
	s.emitTunnelChangedIfStored("client-a", runtimeOnly.Config, "error")
	assertNoTunnelChangedEvent(t, ch, 150*time.Millisecond, runtimeOnly.Config.Name)

	stored := testStoredServerExposeTCPTunnel("stored-event-id", "stored-event", "client-a", 8080, 18080, now)
	mustAddStableTunnel(t, s.store, stored)
	s.emitTunnelChangedIfStored("client-a", storedTunnelToProxyConfig(stored), "error")
	payload := waitForTunnelChangedEvent(t, ch, "error", stored.Name)
	if payload["id"] != stored.ID {
		t.Fatalf("stored tunnel_changed id: want %q, got %#v", stored.ID, payload["id"])
	}

	next := stored
	next.Revision++
	next.UpdatedAt = time.Now().UTC()
	if err := s.store.ReplaceTunnelByID(stored.OwnerClientID, stored.ID, stored.Revision, next); err != nil {
		t.Fatalf("advance stored event revision: %v", err)
	}
	s.emitTunnelChangedIfStored("client-a", storedTunnelToProxyConfig(stored), "error")
	assertNoTunnelChangedEvent(t, ch, 150*time.Millisecond, stored.Name)

	staleState := storedTunnelToProxyConfig(next)
	setProxyConfigStates(&staleState, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "late runtime error")
	s.emitTunnelChangedIfStored("client-a", staleState, "error")
	assertNoTunnelChangedEvent(t, ch, 150*time.Millisecond, stored.Name)
}

func TestMarkTunnelsPortNotAllowedTransitionsExactTunnel(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testStoredServerExposeTCPTunnel(
		"port-policy-id",
		"port-policy",
		"port-policy-client",
		8080,
		18080,
		time.Now().UTC(),
	)
	mustAddStableTunnel(t, s.store, stored)

	clientWS, serverWS := newTestWebSocketPair(t)
	t.Cleanup(func() {
		_ = clientWS.Close()
		_ = serverWS.Close()
	})
	listener := newScriptedListener(t)
	runtimeTunnel := &ProxyTunnel{
		Config:   storedTunnelToProxyConfig(stored),
		Listener: listener,
		done:     make(chan struct{}),
	}
	initializeTunnelRuntimeFromState(runtimeTunnel, stored.OwnerClientID, time.Now())
	client := &ClientConn{
		ID:         stored.OwnerClientID,
		generation: 1,
		state:      clientStateLive,
		conn:       serverWS,
		proxies: map[string]*ProxyTunnel{
			stored.Name: runtimeTunnel,
		},
	}
	s.clients.Store(client.ID, client)

	affected, err := s.findTunnelsAffectedByPortChange([]PortRange{{Start: 20000, End: 20010}})
	if err != nil {
		t.Fatalf("find affected tunnels: %v", err)
	}
	if len(affected) != 1 {
		t.Fatalf("affected tunnels: want 1, got %+v", affected)
	}
	if affected[0].TunnelID != stored.ID ||
		affected[0].Revision != stored.Revision ||
		affected[0].OwnerClientID != stored.OwnerClientID ||
		affected[0].Config.ID != stored.ID {
		t.Fatalf("affected tunnel lost stable identity/config: %+v", affected[0])
	}

	eventsCh := s.events.Subscribe()
	defer s.events.Unsubscribe(eventsCh)
	s.markTunnelsPortNotAllowed(affected)

	unprovision := readTunnelUnprovision(t, clientWS)
	if unprovision.TunnelID != stored.ID || unprovision.Revision != stored.Revision {
		t.Fatalf("unprovision must target exact revision: %+v", unprovision)
	}
	if unprovision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("unprovision role: want target, got %q", unprovision.Role)
	}
	if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("forbidden listener should be closed, got %v", err)
	}

	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load persisted tunnel: %v", err)
	}
	wantError := "port 18080 is not allowed"
	if got.DesiredState != protocol.ProxyDesiredStateRunning ||
		got.RuntimeState != protocol.ProxyRuntimeStateError ||
		got.Error != wantError ||
		got.ActualTransport != protocol.ActualTransportUnknown {
		t.Fatalf("persisted tunnel state mismatch: %+v", got)
	}
	client.proxyMu.RLock()
	current := client.proxies[stored.Name]
	client.proxyMu.RUnlock()
	if current != runtimeTunnel ||
		current.Config.RuntimeState != protocol.ProxyRuntimeStateError ||
		current.Config.Error != wantError ||
		current.Config.ActualTransport != protocol.ActualTransportUnknown ||
		current.Listener != nil {
		t.Fatalf("runtime tunnel state mismatch: %+v", current)
	}
	payload := waitForTunnelChangedEvent(t, eventsCh, "port_not_allowed", stored.Name)
	if payload["id"] != stored.ID || payload["revision"] != float64(stored.Revision) {
		t.Fatalf("event must carry exact tunnel identity: %+v", payload)
	}
	if payload["runtime_state"] != protocol.ProxyRuntimeStateError || payload["error"] != wantError {
		t.Fatalf("event state mismatch: %+v", payload)
	}
	if payload["actual_transport"] != protocol.ActualTransportUnknown {
		t.Fatalf("event transport should match persisted error state: %+v", payload)
	}
}

func TestMarkTunnelsPortNotAllowedDoesNotMutateNewRevisionAfterCleanupBarrier(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testStoredServerExposeTCPTunnel(
		"port-policy-barrier-id",
		"port-policy-barrier",
		"port-policy-barrier-client",
		8080,
		18081,
		time.Now().UTC(),
	)
	mustAddStableTunnel(t, s.store, stored)

	clientWS, serverWS := newTestWebSocketPair(t)
	t.Cleanup(func() {
		_ = clientWS.Close()
		_ = serverWS.Close()
	})
	oldListener := newScriptedListener(t)
	oldRuntime := &ProxyTunnel{
		Config:   storedTunnelToProxyConfig(stored),
		Listener: oldListener,
		done:     make(chan struct{}),
	}
	initializeTunnelRuntimeFromState(oldRuntime, stored.OwnerClientID, time.Now())
	client := &ClientConn{
		ID:         stored.OwnerClientID,
		generation: 1,
		state:      clientStateLive,
		conn:       serverWS,
		proxies: map[string]*ProxyTunnel{
			stored.Name: oldRuntime,
		},
	}
	s.clients.Store(client.ID, client)

	affected, err := s.findTunnelsAffectedByPortChange([]PortRange{{Start: 25000, End: 25010}})
	if err != nil {
		t.Fatalf("find affected tunnels: %v", err)
	}
	if len(affected) != 1 {
		t.Fatalf("affected tunnels: want 1, got %+v", affected)
	}

	newListener := newScriptedListener(t)
	var newRuntime *ProxyTunnel
	hookCalled := false
	s.portPolicyAfterRuntimeCleanupHook = func(got affectedTunnel) {
		hookCalled = true
		if got.TunnelID != stored.ID || got.Revision != stored.Revision {
			t.Fatalf("cleanup hook identity mismatch: %+v", got)
		}
		if _, err := oldListener.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("old listener must be closed before barrier, got %v", err)
		}

		next := stored
		next.Revision = stored.Revision + 1
		next.RemotePort = 25001
		next.Ingress.Config = mustRawJSON(tcpListenConfigAPI{
			BindIP:             "0.0.0.0",
			Port:               next.RemotePort,
			AllowedSourceCIDRs: allowAllSourceCIDRs(),
		})
		next.UpdatedAt = time.Now().UTC()
		if err := s.store.ReplaceTunnelByID(stored.OwnerClientID, stored.ID, stored.Revision, next); err != nil {
			t.Fatalf("advance stored tunnel revision: %v", err)
		}
		reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
		if err != nil {
			t.Fatalf("reload advanced tunnel: %v", err)
		}
		newRuntime = &ProxyTunnel{
			Config:   storedTunnelToProxyConfig(reloaded),
			Listener: newListener,
			done:     make(chan struct{}),
		}
		initializeTunnelRuntimeFromState(newRuntime, stored.OwnerClientID, time.Now())
		client.proxyMu.Lock()
		client.proxies[stored.Name] = newRuntime
		client.proxyMu.Unlock()
	}

	eventsCh := s.events.Subscribe()
	defer s.events.Unsubscribe(eventsCh)
	s.markTunnelsPortNotAllowed(affected)
	if !hookCalled {
		t.Fatal("port policy cleanup barrier was not reached")
	}

	unprovision := readTunnelUnprovision(t, clientWS)
	if unprovision.TunnelID != stored.ID || unprovision.Revision != stored.Revision {
		t.Fatalf("cleanup must only target the old revision: %+v", unprovision)
	}
	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load new stored revision: %v", err)
	}
	if got.Revision != stored.Revision+1 ||
		got.RemotePort != 25001 ||
		got.RuntimeState != protocol.ProxyRuntimeStateExposed ||
		got.Error != "" {
		t.Fatalf("new stored revision was polluted: %+v", got)
	}
	client.proxyMu.RLock()
	current := client.proxies[stored.Name]
	client.proxyMu.RUnlock()
	if current != newRuntime || current.Listener != newListener || current.Config.Revision != stored.Revision+1 {
		t.Fatalf("new runtime was replaced or closed: %+v", current)
	}
	select {
	case _, ok := <-newListener.acceptCh:
		if !ok {
			t.Fatal("new revision listener was closed by stale cleanup")
		}
	default:
	}
	assertNoTunnelChangedEvent(t, eventsCh, 150*time.Millisecond, stored.Name)
}

func TestUnprovisionServerExposeTunnelDoesNotCloseNewRevision(t *testing.T) {
	s := New(0)
	stored := testStoredServerExposeTCPTunnel(
		"exact-unprovision-id",
		"exact-unprovision",
		"exact-unprovision-client",
		8080,
		18082,
		time.Now().UTC(),
	)
	next := stored
	next.Revision = stored.Revision + 1
	next.UpdatedAt = time.Now().UTC()

	clientWS, serverWS := newTestWebSocketPair(t)
	t.Cleanup(func() {
		_ = clientWS.Close()
		_ = serverWS.Close()
	})
	newListener := newScriptedListener(t)
	newRuntime := &ProxyTunnel{
		Config:   storedTunnelToProxyConfig(next),
		Listener: newListener,
		done:     make(chan struct{}),
	}
	initializeTunnelRuntimeFromState(newRuntime, stored.OwnerClientID, time.Now())
	client := &ClientConn{
		ID:         stored.OwnerClientID,
		generation: 1,
		state:      clientStateLive,
		conn:       serverWS,
		proxies: map[string]*ProxyTunnel{
			stored.Name: newRuntime,
		},
	}
	s.clients.Store(client.ID, client)

	if err := s.unprovisionServerExposeTunnel(stored, "stale_cleanup", true); err != nil {
		t.Fatalf("unprovision old revision: %v", err)
	}
	unprovision := readTunnelUnprovision(t, clientWS)
	if unprovision.TunnelID != stored.ID || unprovision.Revision != stored.Revision {
		t.Fatalf("unprovision message must retain old identity: %+v", unprovision)
	}
	client.proxyMu.RLock()
	current := client.proxies[stored.Name]
	client.proxyMu.RUnlock()
	if current != newRuntime || current.Listener != newListener {
		t.Fatalf("old cleanup removed or closed new runtime: %+v", current)
	}
	select {
	case _, ok := <-newListener.acceptCh:
		if !ok {
			t.Fatal("old cleanup closed the new revision listener")
		}
	default:
	}
}

func TestMarkTunnelsPortNotAllowedLeavesStoppedTunnelStopped(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testStoredServerExposeTCPTunnel(
		"stopped-port-policy-id",
		"stopped-port-policy",
		"stopped-port-policy-client",
		8080,
		18083,
		time.Now().UTC(),
	)
	stored.DesiredState = protocol.ProxyDesiredStateStopped
	stored.RuntimeState = protocol.ProxyRuntimeStateIdle
	stored.ActualTransport = protocol.ActualTransportUnknown
	mustAddStableTunnel(t, s.store, stored)

	runtimeTunnel := &ProxyTunnel{
		Config: storedTunnelToProxyConfig(stored),
		done:   make(chan struct{}),
	}
	initializeTunnelRuntimeFromState(runtimeTunnel, stored.OwnerClientID, time.Now())
	client := &ClientConn{
		ID:         stored.OwnerClientID,
		generation: 1,
		state:      clientStateLive,
		proxies: map[string]*ProxyTunnel{
			stored.Name: runtimeTunnel,
		},
	}
	s.clients.Store(client.ID, client)

	affected, err := s.findTunnelsAffectedByPortChange([]PortRange{{Start: 20000, End: 20010}})
	if err != nil {
		t.Fatalf("find affected tunnels: %v", err)
	}
	if len(affected) != 0 {
		t.Fatalf("stopped tunnel should not be included in the policy mutation set: %+v", affected)
	}
	eventsCh := s.events.Subscribe()
	defer s.events.Unsubscribe(eventsCh)
	s.markTunnelsPortNotAllowed(affected)

	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load stopped tunnel: %v", err)
	}
	if got.DesiredState != protocol.ProxyDesiredStateStopped || got.RuntimeState != protocol.ProxyRuntimeStateIdle || got.Error != "" {
		t.Fatalf("port policy update changed stopped stored tunnel: %+v", got)
	}
	client.proxyMu.RLock()
	current := client.proxies[stored.Name]
	client.proxyMu.RUnlock()
	if current != runtimeTunnel || current.Config.DesiredState != protocol.ProxyDesiredStateStopped || current.Config.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("port policy update changed stopped runtime tunnel: %+v", current)
	}
	assertNoTunnelChangedEvent(t, eventsCh, 100*time.Millisecond, "")
}
