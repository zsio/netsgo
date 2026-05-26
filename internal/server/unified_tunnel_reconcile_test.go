package server

import (
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func testStoredC2CTunnelForReconcile(id, name, desired, runtime string, ingressPort int) StoredTunnel {
	now := time.Now().UTC()
	return StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         id,
			Name:       name,
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  22,
			RemotePort: ingressPort,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        1,
		Topology:        TunnelTopologyClientToClient,
		DesiredState:    desired,
		RuntimeState:    runtime,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "ingress-client",
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: ingressPort}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestReconcileRunningUnifiedTunnelsSkipsStoppedAndProjectsOffline(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	running := testStoredC2CTunnelForReconcile("running-c2c", "running-c2c", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, 22022)
	stopped := testStoredC2CTunnelForReconcile("stopped-c2c", "stopped-c2c", protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, 22023)
	mustAddStableTunnel(t, s.store, running)
	mustAddStableTunnel(t, s.store, stopped)

	s.reconcileRunningUnifiedTunnels("test")

	gotRunning, err := s.store.GetTunnelByIDE(running.OwnerClientID, running.ID)
	if err != nil {
		t.Fatalf("load running tunnel: %v", err)
	}
	if gotRunning.DesiredState != protocol.ProxyDesiredStateRunning || gotRunning.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("running tunnel should be reconciled to running/offline without live clients, got %s/%s", gotRunning.DesiredState, gotRunning.RuntimeState)
	}

	gotStopped, err := s.store.GetTunnelByIDE(stopped.OwnerClientID, stopped.ID)
	if err != nil {
		t.Fatalf("load stopped tunnel: %v", err)
	}
	if gotStopped.DesiredState != protocol.ProxyDesiredStateStopped || gotStopped.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("stopped tunnel should be skipped by retry reconcile, got %s/%s", gotStopped.DesiredState, gotStopped.RuntimeState)
	}
}

func TestRestoreTunnelsReconcilesNonOwnerClientRelayParticipant(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	stored := testStoredC2CTunnelForReconcile(
		"related-c2c",
		"related-c2c",
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStateOffline,
		22024,
	)
	mustAddStableTunnel(t, s.store, stored)

	caps := protocol.DefaultClientCapabilities()
	_, ingressSession := newTestClientRelayDataSession(t)
	_, targetSession := newTestClientRelayDataSession(t)
	ingressClient := &ClientConn{
		ID:          stored.Ingress.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		dataSession: ingressSession,
		generation:  1,
		state:       clientStateLive,
		proxies:     make(map[string]*ProxyTunnel),
	}
	targetClient := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		dataSession: targetSession,
		generation:  1,
		state:       clientStateLive,
		proxies:     make(map[string]*ProxyTunnel),
	}
	s.clients.Store(ingressClient.ID, ingressClient)
	s.clients.Store(targetClient.ID, targetClient)

	s.restoreTunnels(ingressClient)

	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load related tunnel: %v", err)
	}
	if got.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("non-owner participant restore should reconcile related tunnel, got runtime_state=%q", got.RuntimeState)
	}
	spec := specFromStoredTunnel(got, s)
	if len(spec.Issues) == 0 || spec.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckRejected {
		t.Fatalf("related reconcile should record provisioning issue after control write failure, got %+v", spec.Issues)
	}
}
