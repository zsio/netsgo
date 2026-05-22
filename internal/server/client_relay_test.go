package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

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

func TestClientRelayTCPTransfersBytes(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

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
