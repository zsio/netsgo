package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

func testClientRelayStoredTunnel(t *testing.T) StoredTunnel {
	t.Helper()

	ingressConfig, err := json.Marshal(map[string]any{"bind_ip": "127.0.0.1", "port": 18080})
	if err != nil {
		t.Fatalf("marshal ingress config: %v", err)
	}
	targetConfig, err := json.Marshal(map[string]any{"host": "127.0.0.1", "port": 8080})
	if err != nil {
		t.Fatalf("marshal target config: %v", err)
	}
	return StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:   "relay-tunnel-id",
			Name: "relay-tunnel",
			Type: protocol.ProxyTypeTCP,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Revision:        7,
		Topology:        TunnelTopologyClientToClient,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportServerRelay,
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "ingress-client",
			Type:     protocol.IngressTypeTCPListen,
			Config:   ingressConfig,
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   targetConfig,
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func testClientRelayHeader(stored StoredTunnel) protocol.DataStreamHeader {
	return protocol.DataStreamHeader{
		Kind:         protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:     stored.ID,
		Revision:     stored.Revision,
		StreamID:     "ingress-stream",
		OpenClientID: stored.Ingress.ClientID,
		SourceRole:   protocol.DataStreamRoleIngress,
		TargetRole:   protocol.DataStreamRoleTarget,
		Direction:    protocol.DataStreamDirectionIngressToTarget,
		Transport:    protocol.ActualTransportServerRelay,
	}
}

func newTestClientRelayDataSession(t *testing.T) (*yamux.Session, *yamux.Session) {
	t.Helper()
	clientPipe, serverPipe := net.Pipe()
	t.Cleanup(func() {
		_ = clientPipe.Close()
		_ = serverPipe.Close()
	})
	clientSession, err := mux.NewClientSession(clientPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("client yamux session: %v", err)
	}
	serverSession, err := mux.NewServerSession(serverPipe, mux.DefaultConfig())
	if err != nil {
		_ = clientSession.Close()
		t.Fatalf("server yamux session: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession, serverSession
}

func assertClientRelayTrafficBucket(t *testing.T, s *Server, store *TrafficStore, stored StoredTunnel, ingressBytes, egressBytes uint64) {
	t.Helper()

	s.flushTrafficObservations()
	if err := store.Flush(); err != nil {
		t.Fatalf("flush traffic store: %v", err)
	}

	rows, err := store.db.Query(`SELECT client_id, owner_client_id, ingress_client_id, target_client_id, topology, transport, tunnel_name, tunnel_type, SUM(ingress_bytes), SUM(egress_bytes)
FROM traffic_buckets
WHERE tunnel_id = ? AND resolution = ?
GROUP BY client_id, owner_client_id, ingress_client_id, target_client_id, topology, transport, tunnel_name, tunnel_type`,
		stored.ID, string(TrafficResolutionMinute))
	if err != nil {
		t.Fatalf("query traffic bucket: %v", err)
	}
	defer func() { _ = rows.Close() }()

	type trafficGroup struct {
		clientID        string
		ownerClientID   string
		ingressClientID string
		targetClientID  string
		topology        string
		transport       string
		tunnelName      string
		tunnelType      string
		ingressBytes    int64
		egressBytes     int64
	}
	groups := []trafficGroup{}
	for rows.Next() {
		var group trafficGroup
		if err := rows.Scan(
			&group.clientID,
			&group.ownerClientID,
			&group.ingressClientID,
			&group.targetClientID,
			&group.topology,
			&group.transport,
			&group.tunnelName,
			&group.tunnelType,
			&group.ingressBytes,
			&group.egressBytes,
		); err != nil {
			t.Fatalf("scan traffic bucket: %v", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate traffic buckets: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected one client relay traffic metadata group, got %+v", groups)
	}

	group := groups[0]
	if group.clientID != stored.OwnerClientID ||
		group.ownerClientID != stored.OwnerClientID ||
		group.ingressClientID != stored.Ingress.ClientID ||
		group.targetClientID != stored.Target.ClientID ||
		group.topology != TunnelTopologyClientToClient ||
		group.transport != protocol.ActualTransportServerRelay ||
		group.tunnelName != stored.Name ||
		group.tunnelType != stored.Type {
		t.Fatalf("client relay traffic identity mismatch: %+v", group)
	}
	if uint64(group.ingressBytes) != ingressBytes || uint64(group.egressBytes) != egressBytes {
		t.Fatalf("client relay traffic bytes mismatch: got ingress=%d egress=%d want ingress=%d egress=%d", group.ingressBytes, group.egressBytes, ingressBytes, egressBytes)
	}
}

func TestClientRelayRegistryStoresTunnelBandwidthRuntime(t *testing.T) {
	stored := testClientRelayStoredTunnel(t)
	stored.IngressBPS = 123
	stored.EgressBPS = 456

	registry := newClientRelayRegistry()
	registry.set(stored)

	limits := registry.limits(stored.ID)
	if limits == nil {
		t.Fatal("expected client relay registry to create tunnel bandwidth runtime")
	}
	if got := limits.Budget(payloadDirectionIngress).Preview(4096); got != 123 {
		t.Fatalf("ingress tunnel budget: want 123, got %d", got)
	}
	if got := limits.Budget(payloadDirectionEgress).Preview(4096); got != 456 {
		t.Fatalf("egress tunnel budget: want 456, got %d", got)
	}

	registry.delete(stored.ID)
	if limits := registry.limits(stored.ID); limits != nil {
		t.Fatal("delete should remove client relay bandwidth runtime")
	}
}

func TestClientRelayTCPTransfersBytes(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	trafficStore, cleanupTraffic := newTestTrafficStore(t)
	defer cleanupTraffic()
	s.trafficStore = trafficStore

	stored := testClientRelayStoredTunnel(t)
	mustAddStableTunnel(t, s.store, stored)
	s.c2c.set(stored)

	targetPipe, serverPipe := net.Pipe()
	defer mustClose(t, targetPipe)
	defer mustClose(t, serverPipe)
	targetClientSession, err := mux.NewClientSession(targetPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("target client session: %v", err)
	}
	defer mustClose(t, targetClientSession)
	serverTargetSession, err := mux.NewServerSession(serverPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("server target session: %v", err)
	}
	defer mustClose(t, serverTargetSession)

	targetClient := &ClientConn{
		ID:          stored.Target.ClientID,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverTargetSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(targetClient.ID, targetClient)

	ingressClient := &ClientConn{
		ID:         stored.Ingress.ClientID,
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}

	targetStreamCh := make(chan net.Conn, 1)
	go func() {
		stream, err := targetClientSession.AcceptStream()
		if err != nil {
			targetStreamCh <- nil
			return
		}
		targetStreamCh <- stream
	}()

	ingressStream, relayStream := net.Pipe()
	defer mustClose(t, ingressStream)
	defer mustClose(t, relayStream)

	done := make(chan struct{})
	go func() {
		s.handleClientOpenedDataStream(ingressClient, relayStream, testClientRelayHeader(stored))
		close(done)
	}()

	var targetStream net.Conn
	select {
	case targetStream = <-targetStreamCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for target stream")
	}
	if targetStream == nil {
		t.Fatal("target stream failed to open")
	}
	defer mustClose(t, targetStream)

	header, err := protocol.DecodeDataStreamHeader(targetStream)
	if err != nil {
		t.Fatalf("decode target stream header: %v", err)
	}
	if header.TunnelID != stored.ID || header.Revision != stored.Revision {
		t.Fatalf("target header identity mismatch: %+v", header)
	}
	if header.SourceRole != protocol.DataStreamRoleServer || header.TargetRole != protocol.DataStreamRoleTarget {
		t.Fatalf("target header roles mismatch: %+v", header)
	}

	payload := []byte("hello target")
	if _, err := ingressStream.Write(payload); err != nil {
		t.Fatalf("write ingress payload: %v", err)
	}
	readBuf := make([]byte, len(payload))
	mustSetReadDeadline(t, targetStream, time.Now().Add(2*time.Second))
	if _, err := io.ReadFull(targetStream, readBuf); err != nil {
		t.Fatalf("read target payload: %v", err)
	}
	if !bytes.Equal(readBuf, payload) {
		t.Fatalf("target payload mismatch: got %q want %q", readBuf, payload)
	}

	response := []byte("hello ingress")
	if _, err := targetStream.Write(response); err != nil {
		t.Fatalf("write target response: %v", err)
	}
	responseBuf := make([]byte, len(response))
	mustSetReadDeadline(t, ingressStream, time.Now().Add(2*time.Second))
	if _, err := io.ReadFull(ingressStream, responseBuf); err != nil {
		t.Fatalf("read ingress response: %v", err)
	}
	if !bytes.Equal(responseBuf, response) {
		t.Fatalf("ingress response mismatch: got %q want %q", responseBuf, response)
	}

	_ = ingressStream.Close()
	_ = targetStream.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not stop after streams closed")
	}

	assertClientRelayTrafficBucket(t, s, trafficStore, stored, uint64(len(payload)), uint64(len(response)))
}

func TestClientRelayRejectsStaleRevision(t *testing.T) {
	s := New(0)
	stored := testClientRelayStoredTunnel(t)
	s.c2c.set(stored)

	header := testClientRelayHeader(stored)
	header.Revision--
	ingressStream, relayStream := net.Pipe()
	defer mustClose(t, ingressStream)

	done := make(chan struct{})
	go func() {
		s.handleClientOpenedDataStream(&ClientConn{ID: stored.Ingress.ClientID}, relayStream, header)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stale relay stream was not rejected")
	}
}

func TestClientRelayRejectsWrongDirection(t *testing.T) {
	stored := testClientRelayStoredTunnel(t)
	header := testClientRelayHeader(stored)
	header.Direction = "target_to_ingress"

	if err := validateClientRelayHeader(stored, stored.Ingress.ClientID, header); err == nil {
		t.Fatal("wrong relay direction should be rejected")
	}
}

func TestClientRelayUDPTransfersFrames(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	trafficStore, cleanupTraffic := newTestTrafficStore(t)
	defer cleanupTraffic()
	s.trafficStore = trafficStore

	stored := testClientRelayStoredTunnel(t)
	stored.Type = protocol.ProxyTypeUDP
	stored.Ingress.Type = protocol.IngressTypeUDPListen
	stored.Target.Type = protocol.TargetTypeUDPService
	mustAddStableTunnel(t, s.store, stored)
	s.c2c.set(stored)

	targetPipe, serverPipe := net.Pipe()
	defer mustClose(t, targetPipe)
	defer mustClose(t, serverPipe)
	targetClientSession, err := mux.NewClientSession(targetPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("target client session: %v", err)
	}
	defer mustClose(t, targetClientSession)
	serverTargetSession, err := mux.NewServerSession(serverPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("server target session: %v", err)
	}
	defer mustClose(t, serverTargetSession)

	targetClient := &ClientConn{
		ID:          stored.Target.ClientID,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverTargetSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(targetClient.ID, targetClient)

	ingressClient := &ClientConn{
		ID:         stored.Ingress.ClientID,
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}

	targetStreamCh := make(chan net.Conn, 1)
	go func() {
		stream, err := targetClientSession.AcceptStream()
		if err != nil {
			targetStreamCh <- nil
			return
		}
		targetStreamCh <- stream
	}()

	ingressStream, relayStream := net.Pipe()
	defer mustClose(t, ingressStream)
	defer mustClose(t, relayStream)

	done := make(chan struct{})
	go func() {
		s.handleClientOpenedDataStream(ingressClient, relayStream, testClientRelayHeader(stored))
		close(done)
	}()

	var targetStream net.Conn
	select {
	case targetStream = <-targetStreamCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for target stream")
	}
	if targetStream == nil {
		t.Fatal("target stream failed to open")
	}
	defer mustClose(t, targetStream)

	header, err := protocol.DecodeDataStreamHeader(targetStream)
	if err != nil {
		t.Fatalf("decode target stream header: %v", err)
	}
	if header.TunnelID != stored.ID || header.Revision != stored.Revision {
		t.Fatalf("target header identity mismatch: %+v", header)
	}
	if header.SourceRole != protocol.DataStreamRoleServer || header.TargetRole != protocol.DataStreamRoleTarget {
		t.Fatalf("target header roles mismatch: %+v", header)
	}

	payload := []byte("udp packet to target")
	if err := mux.WriteUDPFrame(ingressStream, payload); err != nil {
		t.Fatalf("write ingress udp frame: %v", err)
	}
	mustSetReadDeadline(t, targetStream, time.Now().Add(2*time.Second))
	got, err := mux.ReadUDPFrame(targetStream)
	if err != nil {
		t.Fatalf("read target udp frame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("target udp payload mismatch: got %q want %q", got, payload)
	}

	response := []byte("udp reply to ingress")
	if err := mux.WriteUDPFrame(targetStream, response); err != nil {
		t.Fatalf("write target udp frame: %v", err)
	}
	mustSetReadDeadline(t, ingressStream, time.Now().Add(2*time.Second))
	reply, err := mux.ReadUDPFrame(ingressStream)
	if err != nil {
		t.Fatalf("read ingress udp frame: %v", err)
	}
	if !bytes.Equal(reply, response) {
		t.Fatalf("ingress udp payload mismatch: got %q want %q", reply, response)
	}

	_ = ingressStream.Close()
	_ = targetStream.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("udp relay did not stop after streams closed")
	}

	assertClientRelayTrafficBucket(t, s, trafficStore, stored, uint64(len(payload)), uint64(len(response)))
}

func TestClientRelayProvisionTimeoutProjectsIssue(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	s.tunnels.tunnelReadyTimeout = 20 * time.Millisecond

	stored := testClientRelayStoredTunnel(t)
	stored.RuntimeState = protocol.ProxyRuntimeStateOffline
	mustAddStableTunnel(t, s.store, stored)

	targetWS, targetServerWS := newTestWebSocketPair(t)
	defer mustClose(t, targetWS)
	defer mustClose(t, targetServerWS)
	caps := protocol.DefaultClientCapabilities()
	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)

	targetClient := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		conn:        targetServerWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: targetSession,
		generation:  1,
		state:       clientStateLive,
	}
	ingressClient := &ClientConn{
		ID:          stored.Ingress.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: ingressSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(targetClient.ID, targetClient)
	s.clients.Store(ingressClient.ID, ingressClient)

	if err := s.reconcileClientRelayTunnel(stored); err == nil {
		t.Fatal("provision timeout should return an error")
	}
	if _, ok := s.c2c.get(stored.ID); ok {
		t.Fatal("failed provisioning should remove client relay runtime")
	}
	reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("reload tunnel: %v", err)
	}
	spec := specFromStoredTunnel(reloaded, s)
	if spec.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("provision failure should project error, got %q", spec.RuntimeState)
	}
	if len(spec.Issues) != 1 || spec.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckTimeout || spec.Issues[0].ClientID != stored.Target.ClientID {
		t.Fatalf("provision timeout issue mismatch: %+v", spec.Issues)
	}
}

func TestClientRelayActiveReconcileIsIdempotent(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	s.tunnels.tunnelReadyTimeout = 20 * time.Millisecond

	stored := testClientRelayStoredTunnel(t)
	mustAddStableTunnel(t, s.store, stored)
	s.c2c.set(stored)

	caps := protocol.DefaultClientCapabilities()
	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	s.clients.Store(stored.Target.ClientID, &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: targetSession,
		generation:  1,
		state:       clientStateLive,
	})
	s.clients.Store(stored.Ingress.ClientID, &ClientConn{
		ID:          stored.Ingress.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: ingressSession,
		generation:  1,
		state:       clientStateLive,
	})

	started := time.Now()
	if err := s.reconcileClientRelayTunnel(stored); err != nil {
		t.Fatalf("active reconcile should be a no-op: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= s.tunnels.tunnelReadyTimeout {
		t.Fatalf("active reconcile should not wait for provisioning ACKs, elapsed=%s", elapsed)
	}
}

func TestClientRelayCapabilityLossProjectsErrorWithoutProvision(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testClientRelayStoredTunnel(t)
	mustAddStableTunnel(t, s.store, stored)
	s.c2c.set(stored)

	targetCaps := protocol.DefaultClientCapabilities()
	ingressCaps := protocol.ClientCapabilities{}
	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	s.clients.Store(stored.Target.ClientID, &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &targetCaps},
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: targetSession,
		generation:  1,
		state:       clientStateLive,
	})
	s.clients.Store(stored.Ingress.ClientID, &ClientConn{
		ID:          stored.Ingress.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &ingressCaps},
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: ingressSession,
		generation:  1,
		state:       clientStateLive,
	})

	if err := s.reconcileClientRelayTunnel(stored); err != nil {
		t.Fatalf("capability loss should project error without provisioning failure: %v", err)
	}
	if _, ok := s.c2c.get(stored.ID); ok {
		t.Fatal("capability loss should release client relay runtime")
	}
	reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("reload tunnel: %v", err)
	}
	spec := specFromStoredTunnel(reloaded, s)
	if spec.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("capability loss should project error, got %q", spec.RuntimeState)
	}
	if len(spec.Issues) != 1 || spec.Issues[0].Code != protocol.TunnelIssueCodeCapabilityNotSupported || spec.Issues[0].ClientID != stored.Ingress.ClientID {
		t.Fatalf("capability issue mismatch: %+v", spec.Issues)
	}
}

func TestClientRelayTargetDataOfflineProjectsOffline(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	stored := testClientRelayStoredTunnel(t)
	mustAddStableTunnel(t, s.store, stored)
	s.c2c.set(stored)
	s.clients.Store(stored.Target.ClientID, &ClientConn{
		ID:         stored.Target.ClientID,
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	})
	s.clients.Store(stored.Ingress.ClientID, &ClientConn{
		ID:         stored.Ingress.ClientID,
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	})

	ingressStream, relayStream := net.Pipe()
	defer mustClose(t, ingressStream)
	done := make(chan struct{})
	go func() {
		s.handleClientOpenedDataStream(&ClientConn{ID: stored.Ingress.ClientID}, relayStream, testClientRelayHeader(stored))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("target stream failure did not return")
	}
	reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("reload tunnel: %v", err)
	}
	spec := specFromStoredTunnel(reloaded, s)
	if spec.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("target data channel loss should project offline, got %q", spec.RuntimeState)
	}
	if len(spec.Issues) != 0 {
		t.Fatalf("target data channel loss should suppress transport issues, got %+v", spec.Issues)
	}
}
