package server

import (
	"net"
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

func TestRestoreTunnelsReconcilesRunningErrorTunnel(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	stored := testStoredC2CTunnelForReconcile(
		"error-c2c",
		"error-c2c",
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStateError,
		22025,
	)
	stored.Error = "old persisted failure"
	mustAddStableTunnel(t, s.store, stored)

	caps := protocol.DefaultClientCapabilities()
	_, ingressSession := newTestClientRelayDataSession(t)
	_, targetSession := newTestClientRelayDataSession(t)
	targetClient := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		dataSession: targetSession,
		generation:  1,
		state:       clientStateLive,
		proxies:     make(map[string]*ProxyTunnel),
	}
	ingressClient := &ClientConn{
		ID:          stored.Ingress.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		dataSession: ingressSession,
		generation:  1,
		state:       clientStateLive,
		proxies:     make(map[string]*ProxyTunnel),
	}
	s.clients.Store(targetClient.ID, targetClient)
	s.clients.Store(ingressClient.ID, ingressClient)

	s.restoreTunnels(targetClient)

	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load restored tunnel: %v", err)
	}
	spec := specFromStoredTunnel(got, s)
	if len(spec.Issues) == 0 || spec.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckRejected {
		t.Fatalf("running/error restore should attempt fresh reconcile and record current issue, state=%q issues=%+v", got.RuntimeState, spec.Issues)
	}
	if got.Error == "old persisted failure" {
		t.Fatal("running/error restore reused stale persisted runtime error")
	}
}

func TestUnifiedServerExposeProvisionAndDataHeaderUseStoredRevision(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	reservedListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("reserve remote port: %v", err)
	}
	remotePort := reservedListener.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() {
		if reservedListener != nil {
			_ = reservedListener.Close()
		}
	})

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "server-expose-unified-id",
			Name:       "server-expose-unified",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  22,
			RemotePort: remotePort,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        9,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "0.0.0.0", Port: remotePort}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}
	mustAddStableTunnel(t, s.store, stored)

	targetWS, targetServerWS := newTestWebSocketPair(t)
	defer mustClose(t, targetWS)
	defer mustClose(t, targetServerWS)
	clientSession, serverSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	target := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Hostname: "target-client", Capabilities: &caps},
		conn:        targetServerWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(target.ID, target)
	go s.controlLoop(target)

	eventsCh := s.events.Subscribe()
	defer s.events.Unsubscribe(eventsCh)

	restoreDone := make(chan error, 1)
	go func() {
		restoreDone <- s.restoreUnifiedServerExposeTunnel(target, stored)
	}()
	pendingPayload := waitForTunnelChangedEvent(t, eventsCh, "pending", stored.Name)
	if got, _ := pendingPayload["runtime_state"].(string); got != protocol.ProxyRuntimeStatePending {
		t.Fatalf("pending event runtime_state: want %s, got %s", protocol.ProxyRuntimeStatePending, got)
	}
	msg := readControlMessageOfType(t, targetWS, protocol.MsgTypeTunnelProvision)
	var provision protocol.TunnelProvisionRequest
	if err := msg.ParsePayload(&provision); err != nil {
		t.Fatalf("parse provision payload: %v", err)
	}
	if provision.TunnelID == "" {
		t.Fatalf("expected unified tunnel provision payload, got empty tunnel_id: %+v", provision)
	}
	if provision.TunnelID != stored.ID || provision.Revision != stored.Revision || provision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("provision identity mismatch: %+v", provision)
	}
	if provision.Spec.Topology != TunnelTopologyServerExpose || provision.Spec.Target.ClientID != stored.Target.ClientID {
		t.Fatalf("provision spec mismatch: %+v", provision.Spec)
	}
	if err := reservedListener.Close(); err != nil {
		t.Fatalf("release remote port: %v", err)
	}
	reservedListener = nil
	ack, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: provision.TunnelID,
		Revision: provision.Revision,
		Role:     provision.Role,
		Accepted: true,
		Message:  "ok",
	})
	if err != nil {
		t.Fatalf("build provision ack: %v", err)
	}
	if err := targetWS.WriteJSON(ack); err != nil {
		t.Fatalf("write provision ack: %v", err)
	}
	select {
	case err := <-restoreDone:
		if err != nil {
			t.Fatalf("restore unified server-expose: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for restore")
	}
	restoredPayload := waitForTunnelChangedEvent(t, eventsCh, "restored", stored.Name)
	if got, _ := restoredPayload["runtime_state"].(string); got != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("restored event runtime_state: want %s, got %s", protocol.ProxyRuntimeStateExposed, got)
	}
	snapshot := s.collectSnapshot()
	if len(snapshot.Clients) != 1 || len(snapshot.Clients[0].Proxies) != 1 {
		t.Fatalf("snapshot should include one restored tunnel, got %+v", snapshot.Clients)
	}
	if got := snapshot.Clients[0].Proxies[0].RuntimeState; got != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("snapshot runtime_state after restore: want %s, got %s", protocol.ProxyRuntimeStateExposed, got)
	}
	t.Cleanup(func() {
		_ = s.CloseProxyRuntime(target, stored.Name)
	})

	type openResult struct {
		stream net.Conn
		err    error
	}
	openCh := make(chan openResult, 1)
	go func() {
		stream, err := s.openStreamToClient(target, stored.Name)
		openCh <- openResult{stream: stream, err: err}
	}()

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("accept client stream: %v", err)
	}
	defer mustClose(t, clientStream)
	header, err := protocol.DecodeDataStreamHeader(clientStream)
	if err != nil {
		t.Fatalf("decode data stream header: %v", err)
	}
	if header.TunnelID != stored.ID || header.Revision != stored.Revision {
		t.Fatalf("data stream header should use stored identity, got %+v", header)
	}
	if header.SourceRole != protocol.DataStreamRoleServer || header.TargetRole != protocol.DataStreamRoleTarget || header.Transport != protocol.ActualTransportServerRelay {
		t.Fatalf("data stream route mismatch: %+v", header)
	}
	select {
	case result := <-openCh:
		if result.err != nil {
			t.Fatalf("open stream: %v", result.err)
		}
		mustClose(t, result.stream)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for open stream")
	}
}
