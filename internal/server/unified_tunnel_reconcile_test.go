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
