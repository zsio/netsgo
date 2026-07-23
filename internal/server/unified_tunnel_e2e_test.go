package server

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	clientpkg "netsgo/internal/client"
	"netsgo/internal/socks5wire"
	"netsgo/pkg/protocol"
)

func newUnifiedE2ETestServer(t *testing.T) *Server {
	t.Helper()

	s := New(0)
	initTestAdminStore(t, s)

	var err error
	s.store, err = newTunnelStoreWithDB(s.auth.adminStore.path, s.auth.adminStore.db, false)
	if err != nil {
		t.Fatalf("failed to create shared TunnelStore: %v", err)
	}
	s.trafficStore = newTrafficStoreWithDB(s.auth.adminStore.path, s.auth.adminStore.db, false)
	s.store.attachTrafficStore(s.trafficStore, s.trafficAccumulator)
	return s
}

func TestUnifiedServerExposeTCPEndToEndWithRealClient(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-server-tcp-target")
	newTargetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-server-tcp-target-new")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	newTargetID := waitForUnifiedE2EClientReady(t, s, newTargetClient)
	ingressPort := reserveTCPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"e2e-server-tcp",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["127.0.0.0/8"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"%s","port":%d}},
		"transport_policy":"server_relay_only"
	}`, ingressPort, targetID, targetAddr, targetPort))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server_expose TCP create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)

	assertUnifiedTCPEcho(t, ingressPort, []byte("server-expose tcp payload"))
	migrated := migrateUnifiedE2ETunnel(t, s, token, created.ID, created.Revision, newTargetID)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	assertUnifiedE2EMigrationIdentity(t, created, migrated, newTargetID)
	assertUnifiedTCPEcho(t, ingressPort, []byte("server-expose migrated tcp payload"))
}

func TestUnifiedServerExposeUDPEndToEndWithRealClient(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetAddr, targetPort := startTestUDPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-server-udp-target")
	newTargetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-server-udp-target-new")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	newTargetID := waitForUnifiedE2EClientReady(t, s, newTargetClient)
	ingressPort := reserveUDPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"e2e-server-udp",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"udp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["127.0.0.0/8"]}},
		"target":{"location":"client","client_id":"%s","type":"udp_service","config":{"ip":"%s","port":%d}},
		"transport_policy":"server_relay_only"
	}`, ingressPort, targetID, targetAddr, targetPort))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server_expose UDP create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)

	assertUnifiedUDPEcho(t, ingressPort, []byte("server-expose udp payload"))
	migrated := migrateUnifiedE2ETunnel(t, s, token, created.ID, created.Revision, newTargetID)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	assertUnifiedE2EMigrationIdentity(t, created, migrated, newTargetID)
	assertUnifiedUDPEcho(t, ingressPort, []byte("server-expose migrated udp payload"))
}

func TestUnifiedServerExposeSOCKS5EndToEndWithRealClient(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-server-socks5-target")
	newTargetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-server-socks5-target-new")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	newTargetID := waitForUnifiedE2EClientReady(t, s, newTargetClient)
	ingressPort := reserveTCPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"e2e-server-socks5",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"127.0.0.1",
			"port":%d,
			"allowed_source_cidrs":["127.0.0.0/8"],
			"auth":{"type":"none"}
		}},
		"target":{"location":"client","client_id":"%s","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["127.0.0.0/8"],
			"allowed_target_hosts":["%s"],
			"allowed_target_ports":[%d],
			"dial_timeout_seconds":5
		}},
		"transport_policy":"server_relay_only",
		"confirm_no_auth_risk":true
	}`, ingressPort, targetID, targetAddr, targetPort))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server_expose SOCKS5 create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)

	conn := dialSOCKS5ConnectNoAuth(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), targetAddr, targetPort)
	defer func() { _ = conn.Close() }()
	assertSOCKS5Echo(t, conn, []byte("server-expose socks5 payload"))
	_ = conn.Close()

	migrated := migrateUnifiedE2ETunnel(t, s, token, created.ID, created.Revision, newTargetID)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	assertUnifiedE2EMigrationIdentity(t, created, migrated, newTargetID)
	migratedConn := dialSOCKS5ConnectNoAuth(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), targetAddr, targetPort)
	defer func() { _ = migratedConn.Close() }()
	assertSOCKS5Echo(t, migratedConn, []byte("server-expose socks5 migrated payload"))
}

func TestUnifiedClientToClientTCPEndToEndWithRealClients(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-target")
	newTargetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-target-new")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	newTargetID := waitForUnifiedE2EClientReady(t, s, newTargetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveTCPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"e2e-c2c-tcp",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"%s","port":%d}},
		"transport_policy":"server_relay_only"
	}`, ingressID, ingressPort, targetID, targetAddr, targetPort))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial client ingress listener: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set client ingress deadline: %v", err)
	}

	payload := []byte("client-to-client tcp payload")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write ingress payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read echoed payload through c2c tunnel: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echoed payload mismatch: got %q want %q", got, payload)
	}
	_ = conn.Close()

	migrated := migrateUnifiedE2ETunnel(t, s, token, created.ID, created.Revision, newTargetID)
	assertUnifiedE2EMigrationIdentity(t, created, migrated, newTargetID)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	migratedConn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial migrated client ingress listener: %v", err)
	}
	defer func() { _ = migratedConn.Close() }()
	if err := migratedConn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set migrated client ingress deadline: %v", err)
	}
	migratedPayload := []byte("client-to-client migrated tcp payload")
	if _, err := migratedConn.Write(migratedPayload); err != nil {
		t.Fatalf("write migrated ingress payload: %v", err)
	}
	migratedGot := make([]byte, len(migratedPayload))
	if _, err := io.ReadFull(migratedConn, migratedGot); err != nil {
		t.Fatalf("read migrated echoed payload: %v", err)
	}
	if string(migratedGot) != string(migratedPayload) {
		t.Fatalf("migrated echoed payload mismatch: got %q want %q", migratedGot, migratedPayload)
	}
}

func TestUnifiedClientToClientDirectPreferredTCPEndToEnd(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-p2p-target")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-p2p-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveTCPPort(t)
	create := []byte(fmt.Sprintf(`{
		"name":"e2e-c2c-direct","topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["127.0.0.0/8"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"%s","port":%d}},
		"transport_policy":"direct_preferred","bandwidth_settings":{"total_bps":1048576}
	}`, ingressID, ingressPort, targetID, targetAddr, targetPort))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("direct create: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatal(err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	waitForUnifiedP2PConnected(t, s, created.ID)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("peer-direct payload")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo=%q", got)
	}
	_ = conn.Close()
	stored, _, _ := s.findStoredTunnelByID(created.ID)
	if stored.ActualTransport != protocol.ActualTransportPeerDirect {
		t.Fatalf("actual transport=%q", stored.ActualTransport)
	}
	statsDeadline := time.Now().Add(2 * time.Second)
	for {
		ingress, egress, ok := s.p2p.statsForTunnel(stored.P2P.SessionID, stored.ID)
		if ok && ingress >= uint64(len(payload)) && egress >= uint64(len(payload)) {
			break
		}
		if time.Now().After(statsDeadline) {
			t.Fatalf("direct owner traffic not reported: ingress=%d egress=%d", ingress, egress)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForUnifiedP2PConnected(t *testing.T, s *Server, tunnelID string) {
	t.Helper()
	// WebRTC setup is CPU-heavy under the race detector and when the full
	// server suite runs concurrently. Keep this bounded without making the
	// integration test depend on normal-build timing.
	deadline := time.Now().Add(30 * time.Second)
	for {
		stored, ok, err := s.findStoredTunnelByID(tunnelID)
		if err == nil && ok && stored.P2P.State == protocol.P2PStateConnected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("P2P did not connect: state=%q err=%v", stored.P2P.State, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUnifiedClientToClientP2PFailurePolicyBehavior(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	s.p2pSignalDropHook = func(string, string, protocol.P2PSignal) bool { return true }
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-p2p-fail-target")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-p2p-fail-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	create := func(name, policy string, port int) tunnelSpecAPI {
		body := []byte(fmt.Sprintf(`{"name":"%s","topology":"client_to_client","ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["127.0.0.0/8"]}},"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"%s","port":%d}},"transport_policy":"%s"}`, name, ingressID, port, targetID, targetAddr, targetPort, policy))
		resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, body)
		if resp.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", policy, resp.Code, resp.Body.String())
		}
		var spec tunnelSpecAPI
		if err := mustDecodeJSON(t, resp.Body, &spec); err != nil {
			t.Fatal(err)
		}
		waitForUnifiedTunnelRuntimeState(t, s, token, spec.ID, tunnelRuntimeStateActive)
		return spec
	}
	preferredPort := reserveTCPPort(t)
	create("p2p-fallback", protocol.TransportPolicyDirectPreferred, preferredPort)
	assertUnifiedTCPEcho(t, preferredPort, []byte("relay fallback payload"))
	onlyPort := reserveTCPPort(t)
	create("p2p-only", protocol.TransportPolicyDirectOnly, onlyPort)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(onlyPort)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("must not relay")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("direct_only unexpectedly relayed payload while P2P was unavailable")
	}
}

func TestUnifiedClientToClientRelayToDirectSwitchKeepsStreamsPinnedAndBytesExact(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	var dropSignals atomic.Bool
	dropSignals.Store(true)
	s.p2pSignalDropHook = func(string, string, protocol.P2PSignal) bool { return dropSignals.Load() }
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-switch-target")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-switch-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveTCPPort(t)
	created := createUnifiedP2PTCPTunnel(t, s, token, "p2p-switch", protocol.TransportPolicyDirectPreferred, ingressID, targetID, ingressPort, targetAddr, targetPort)

	relayConn := dialUnifiedTCP(t, ingressPort)
	relayBefore := []byte("relay-before-direct")
	assertConnEchoExact(t, relayConn, relayBefore)
	stored, ok, err := s.findStoredTunnelByID(created.ID)
	if err != nil || !ok || stored.ActualTransport != protocol.ActualTransportServerRelay || stored.P2P.SessionID == "" {
		t.Fatalf("expected gathering relay state, stored=%+v ok=%v err=%v", stored, ok, err)
	}

	dropSignals.Store(false)
	s.sendP2PLifecycleResult(s.p2p.closeSession(stored.P2P.SessionID, "test restart with signaling enabled"))
	ingressLive, ingressOK := s.loadLiveClient(ingressID)
	targetLive, targetOK := s.loadLiveClient(targetID)
	if !ingressOK || !targetOK {
		t.Fatal("test clients went offline before P2P restart")
	}
	if err := s.ensureP2PForTunnel(stored, ingressLive, targetLive); err != nil {
		t.Fatalf("restart P2P negotiation: %v", err)
	}
	waitForUnifiedP2PConnected(t, s, created.ID)

	relayAfter := []byte("same-relay-stream-after-direct")
	assertConnEchoExact(t, relayConn, relayAfter)
	directConn := dialUnifiedTCP(t, ingressPort)
	directPayload := []byte("new-direct-stream")
	assertConnEchoExact(t, directConn, directPayload)
	assertNoExtraTCPPayload(t, directConn)
	_ = directConn.Close()
	assertNoExtraTCPPayload(t, relayConn)
	_ = relayConn.Close()

	waitForP2PTrafficCounters(t, s, created.ID, uint64(len(directPayload)), uint64(len(directPayload)))
	waitForTrafficAccumulatorEntries(t, s, 2)
	wantRelay := uint64(len(relayBefore) + len(relayAfter))
	assertTransportTrafficExact(t, s.trafficAccumulator.Drain(), created.ID, map[string][2]uint64{
		protocol.ActualTransportServerRelay: {wantRelay, wantRelay},
		protocol.ActualTransportPeerDirect:  {uint64(len(directPayload)), uint64(len(directPayload))},
	})
}

func TestUnifiedClientToClientDirectFailureClosesOldStreamAndFallsBackExactlyOnce(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-failover-target")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-failover-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveTCPPort(t)
	created := createUnifiedP2PTCPTunnel(t, s, token, "p2p-failover", protocol.TransportPolicyDirectPreferred, ingressID, targetID, ingressPort, targetAddr, targetPort)
	waitForUnifiedP2PConnected(t, s, created.ID)

	directConn := dialUnifiedTCP(t, ingressPort)
	directPayload := []byte("direct-before-failure")
	assertConnEchoExact(t, directConn, directPayload)
	stored, _, _ := s.findStoredTunnelByID(created.ID)
	ingressLive, ok := s.loadLiveClient(ingressID)
	if !ok {
		t.Fatal("ingress client went offline")
	}
	failed, err := protocol.NewMessage(protocol.MsgTypeP2PSessionReady, protocol.P2PSessionStatus{SessionID: stored.P2P.SessionID, Sequence: 2, State: protocol.P2PStateFailed, Error: "injected peer failure"})
	if err != nil {
		t.Fatal(err)
	}
	s.handleP2PStatusMessage(ingressLive, *failed)
	waitForUnifiedP2PState(t, s, created.ID, protocol.P2PStateFallback, protocol.ActualTransportServerRelay)
	_ = directConn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := directConn.Write([]byte("must-not-survive")); err == nil {
		buf := make([]byte, 16)
		if _, readErr := directConn.Read(buf); readErr == nil {
			t.Fatal("failed direct stream remained usable")
		}
	}
	_ = directConn.Close()

	relayConn := dialUnifiedTCP(t, ingressPort)
	relayPayload := []byte("single-relay-after-failure")
	assertConnEchoExact(t, relayConn, relayPayload)
	assertNoExtraTCPPayload(t, relayConn)
	_ = relayConn.Close()
	waitForTrafficAccumulatorEntries(t, s, 2)
	assertTransportTrafficExact(t, s.trafficAccumulator.Drain(), created.ID, map[string][2]uint64{
		protocol.ActualTransportPeerDirect:  {uint64(len(directPayload)), uint64(len(directPayload))},
		protocol.ActualTransportServerRelay: {uint64(len(relayPayload)), uint64(len(relayPayload))},
	})

	// The production retry schedule performs the first retry after ten seconds.
	// Do not call ensureP2PForTunnel directly here: this verifies the timer,
	// reconcile path, fresh signaling session, and selector recovery together.
	waitForUnifiedP2PConnected(t, s, created.ID)
	recoveredConn := dialUnifiedTCP(t, ingressPort)
	recoveredPayload := []byte("direct-after-automatic-retry")
	assertConnEchoExact(t, recoveredConn, recoveredPayload)
	assertNoExtraTCPPayload(t, recoveredConn)
	_ = recoveredConn.Close()
	waitForP2PTrafficCounters(t, s, created.ID, uint64(len(recoveredPayload)), uint64(len(recoveredPayload)))
	waitForTransportTrafficExact(t, s, created.ID, protocol.ActualTransportPeerDirect, uint64(len(recoveredPayload)), uint64(len(recoveredPayload)))
}

func TestUnifiedClientToClientDisconnectReconnectRebuildsDirectSessionWithoutDuplicateBytes(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	targetAddr, targetPort := startTestTCPEchoService(t)
	targetDir := t.TempDir()
	targetClient, targetErr := launchUnifiedE2EClient(t, s, ts.URL, "install-e2e-reconnect-target", targetDir)
	var currentTarget = targetClient
	var currentTargetErr = targetErr
	t.Cleanup(func() {
		if currentTarget != nil {
			stopUnifiedE2EClient(t, currentTarget, currentTargetErr)
		}
	})
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-reconnect-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveTCPPort(t)
	created := createUnifiedP2PTCPTunnel(t, s, token, "p2p-reconnect", protocol.TransportPolicyDirectPreferred, ingressID, targetID, ingressPort, targetAddr, targetPort)
	waitForUnifiedP2PConnected(t, s, created.ID)

	oldConn := dialUnifiedTCP(t, ingressPort)
	before := []byte("before-target-disconnect")
	assertConnEchoExact(t, oldConn, before)
	stopUnifiedE2EClient(t, currentTarget, currentTargetErr)
	currentTarget, currentTargetErr = nil, nil
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, protocol.ProxyRuntimeStateOffline)
	_ = oldConn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := oldConn.Write([]byte("must-not-cross-disconnect")); err == nil {
		var one [1]byte
		if _, readErr := oldConn.Read(one[:]); readErr == nil {
			t.Fatal("old direct stream survived target disconnect")
		}
	}
	_ = oldConn.Close()

	restarted, restartedErr := launchUnifiedE2EClient(t, s, ts.URL, "install-e2e-reconnect-target", targetDir)
	currentTarget, currentTargetErr = restarted, restartedErr
	if restartedID := waitForUnifiedE2EClientReady(t, s, restarted); restartedID != targetID {
		t.Fatalf("persistent target identity changed: before=%s after=%s", targetID, restartedID)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	waitForUnifiedP2PConnected(t, s, created.ID)
	newConn := dialUnifiedTCP(t, ingressPort)
	after := []byte("after-target-reconnect")
	assertConnEchoExact(t, newConn, after)
	assertNoExtraTCPPayload(t, newConn)
	_ = newConn.Close()

	want := uint64(len(before) + len(after))
	waitForTransportTrafficExact(t, s, created.ID, protocol.ActualTransportPeerDirect, want, want)
}

func TestUnifiedClientToClientSharedLimitServesReverseTrafficWhileForwardBacklogged(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	const totalBPS = 64 * 1024
	const forwardThreshold = 64 * 1024
	marker := []byte("reverse-direction-must-not-wait-behind-forward-backlog")
	targetAddr, targetPort, thresholdReached, backendResult := startBidirectionalLimitProbeService(t, forwardThreshold, marker)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-shared-limit-target")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-shared-limit-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveTCPPort(t)

	body := []byte(fmt.Sprintf(`{
		"name":"e2e-c2c-shared-limit","topology":"client_to_client",
		"ingress":{"location":"client","client_id":%q,"type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["127.0.0.0/8"]}},
		"target":{"location":"client","client_id":%q,"type":"tcp_service","config":{"ip":%q,"port":%d}},
		"transport_policy":"direct_preferred","bandwidth_settings":{"total_bps":%d}
	}`, ingressID, ingressPort, targetID, targetAddr, targetPort, totalBPS))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create shared-limit tunnel: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatal(err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	waitForUnifiedP2PConnected(t, s, created.ID)

	conn := dialUnifiedTCP(t, ingressPort)
	defer func() { _ = conn.Close() }()
	forward := bytes.Repeat([]byte{0x6d}, 8*1024*1024)
	writeDone := make(chan error, 1)
	go func() {
		n, err := conn.Write(forward)
		if err == nil && n != len(forward) {
			err = io.ErrShortWrite
		}
		writeDone <- err
	}()

	select {
	case <-thresholdReached:
	case err := <-backendResult:
		t.Fatalf("backend failed before reverse probe: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("forward direction did not reach the rate-limited backend")
	}
	select {
	case err := <-writeDone:
		t.Fatalf("forward write was not backlogged at reverse activation: %v", err)
	default:
	}

	reverseStarted := time.Now()
	if err := conn.SetReadDeadline(reverseStarted.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(marker))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reverse payload was blocked behind forward backlog: %v", err)
	}
	if !bytes.Equal(got, marker) {
		t.Fatalf("reverse marker mismatch: got=%q want=%q", got, marker)
	}
	if elapsed := time.Since(reverseStarted); elapsed > 2*time.Second {
		t.Fatalf("reverse payload waited too long behind forward backlog: %v", elapsed)
	}

	_ = conn.Close()
	select {
	case <-writeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the connection did not unblock the backlogged writer")
	}
}

func TestUnifiedClientToClientSOCKS5EndToEndWithRealClients(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-socks5-target")
	newTargetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-socks5-target-new")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-socks5-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	newTargetID := waitForUnifiedE2EClientReady(t, s, newTargetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveTCPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"e2e-c2c-socks5",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"socks5_listen","config":{
			"bind_ip":"127.0.0.1",
			"port":%d,
			"allowed_source_cidrs":["127.0.0.0/8"],
			"auth":{"type":"none"}
		}},
		"target":{"location":"client","client_id":"%s","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["127.0.0.0/8"],
			"allowed_target_hosts":["%s"],
			"allowed_target_ports":[%d],
			"dial_timeout_seconds":5
		}},
		"transport_policy":"direct_preferred"
	}`, ingressID, ingressPort, targetID, targetAddr, targetPort))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client SOCKS5 create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	waitForUnifiedP2PConnected(t, s, created.ID)

	conn := dialSOCKS5ConnectNoAuth(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), targetAddr, targetPort)
	defer func() { _ = conn.Close() }()
	payload := []byte("client-to-client socks5 payload")
	assertSOCKS5Echo(t, conn, payload)
	assertNoExtraTCPPayload(t, conn)
	_ = conn.Close()
	waitForP2PTrafficCounters(t, s, created.ID, uint64(len(payload)), uint64(len(payload)))
	waitForTransportTrafficExact(t, s, created.ID, protocol.ActualTransportPeerDirect, uint64(len(payload)), uint64(len(payload)))
	assertTransportTrafficExact(t, s.trafficAccumulator.Drain(), created.ID, map[string][2]uint64{
		protocol.ActualTransportPeerDirect: {uint64(len(payload)), uint64(len(payload))},
	})

	migrated := migrateUnifiedE2ETunnel(t, s, token, created.ID, created.Revision, newTargetID)
	assertUnifiedE2EMigrationIdentity(t, created, migrated, newTargetID)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	waitForUnifiedP2PConnected(t, s, created.ID)
	migratedConn := dialSOCKS5ConnectNoAuth(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), targetAddr, targetPort)
	defer func() { _ = migratedConn.Close() }()
	migratedPayload := []byte("client-to-client socks5 migrated payload")
	assertSOCKS5Echo(t, migratedConn, migratedPayload)
	assertNoExtraTCPPayload(t, migratedConn)
	_ = migratedConn.Close()
	waitForP2PTrafficCounters(t, s, created.ID, uint64(len(migratedPayload)), uint64(len(migratedPayload)))
	waitForTransportTrafficExact(t, s, created.ID, protocol.ActualTransportPeerDirect, uint64(len(migratedPayload)), uint64(len(migratedPayload)))
	assertTransportTrafficExact(t, s.trafficAccumulator.Drain(), created.ID, map[string][2]uint64{
		protocol.ActualTransportPeerDirect: {uint64(len(migratedPayload)), uint64(len(migratedPayload))},
	})
}

func TestUnifiedClientToClientUDPEndToEndWithRealClients(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetAddr, targetPort := startTestUDPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-udp-target")
	newTargetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-udp-target-new")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-udp-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	newTargetID := waitForUnifiedE2EClientReady(t, s, newTargetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveUDPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"e2e-c2c-udp",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"udp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"udp_service","config":{"ip":"%s","port":%d}},
		"transport_policy":"direct_preferred"
	}`, ingressID, ingressPort, targetID, targetAddr, targetPort))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	waitForUnifiedP2PConnected(t, s, created.ID)

	conn, err := net.DialTimeout("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial client UDP ingress listener: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set client UDP ingress deadline: %v", err)
	}

	payloads := [][]byte{
		[]byte("client-to-client udp payload one"),
		bytes.Repeat([]byte{0x5a}, 137),
		[]byte("client-to-client udp payload three"),
	}
	assertUDPDatagramsEchoExactlyOnce(t, conn, payloads)
	_ = conn.Close()
	wantBytes := totalPayloadBytes(payloads)
	waitForP2PTrafficCounters(t, s, created.ID, wantBytes, wantBytes)
	waitForTransportTrafficExact(t, s, created.ID, protocol.ActualTransportPeerDirect, wantBytes, wantBytes)
	assertTransportTrafficExact(t, s.trafficAccumulator.Drain(), created.ID, map[string][2]uint64{
		protocol.ActualTransportPeerDirect: {wantBytes, wantBytes},
	})

	migrated := migrateUnifiedE2ETunnel(t, s, token, created.ID, created.Revision, newTargetID)
	assertUnifiedE2EMigrationIdentity(t, created, migrated, newTargetID)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	waitForUnifiedP2PConnected(t, s, created.ID)
	migratedConn, err := net.DialTimeout("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial migrated client UDP ingress listener: %v", err)
	}
	defer func() { _ = migratedConn.Close() }()
	if err := migratedConn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set migrated UDP ingress deadline: %v", err)
	}
	migratedPayloads := [][]byte{
		[]byte("client-to-client migrated udp payload one"),
		bytes.Repeat([]byte{0xa5}, 211),
	}
	assertUDPDatagramsEchoExactlyOnce(t, migratedConn, migratedPayloads)
	_ = migratedConn.Close()
	migratedWantBytes := totalPayloadBytes(migratedPayloads)
	waitForP2PTrafficCounters(t, s, created.ID, migratedWantBytes, migratedWantBytes)
	waitForTransportTrafficExact(t, s, created.ID, protocol.ActualTransportPeerDirect, migratedWantBytes, migratedWantBytes)
	assertTransportTrafficExact(t, s.trafficAccumulator.Drain(), created.ID, map[string][2]uint64{
		protocol.ActualTransportPeerDirect: {migratedWantBytes, migratedWantBytes},
	})
}

func assertUDPDatagramsEchoExactlyOnce(t *testing.T, conn net.Conn, payloads [][]byte) {
	t.Helper()
	maxPayload := 0
	for _, payload := range payloads {
		if len(payload) > maxPayload {
			maxPayload = len(payload)
		}
		if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if n, err := conn.Write(payload); err != nil || n != len(payload) {
			t.Fatalf("write UDP datagram: n=%d want=%d err=%v", n, len(payload), err)
		}
		got := make([]byte, len(payload)+1)
		n, err := conn.Read(got)
		if err != nil {
			t.Fatalf("read echoed UDP datagram: %v", err)
		}
		if !bytes.Equal(got[:n], payload) {
			t.Fatalf("UDP datagram mismatch: got=%x want=%x", got[:n], payload)
		}
	}
	if err := conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, maxPayload+1)
	if n, err := conn.Read(got); err == nil || n != 0 {
		t.Fatalf("unexpected duplicate UDP datagram: n=%d payload=%x err=%v", n, got[:n], err)
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("UDP association ended instead of remaining idle: %v", err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func totalPayloadBytes(payloads [][]byte) uint64 {
	var total uint64
	for _, payload := range payloads {
		total += uint64(len(payload))
	}
	return total
}

func startBidirectionalLimitProbeService(t *testing.T, threshold int, marker []byte) (string, int, <-chan struct{}, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for bidirectional limit probe: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	reached := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			result <- err
			return
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
			result <- err
			return
		}
		if _, err := io.CopyN(io.Discard, conn, int64(threshold)); err != nil {
			result <- err
			return
		}
		close(reached)
		if _, err := conn.Write(marker); err != nil {
			result <- err
			return
		}
		_, err = io.Copy(io.Discard, conn)
		result <- err
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, reached, result
}

func migrateUnifiedE2ETunnel(t *testing.T, s *Server, token, tunnelID string, revision int64, targetClientID string) tunnelSpecAPI {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"expected_revision":%d,"target_client_id":"%s"}`, revision, targetClientID))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels/"+tunnelID+"/migrate", token, body)
	if resp.Code != http.StatusOK {
		t.Fatalf("migrate tunnel: want 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Tunnel tunnelSpecAPI `json:"tunnel"`
	}
	if err := mustDecodeJSON(t, resp.Body, &payload); err != nil {
		t.Fatalf("decode migrated tunnel: %v", err)
	}
	return payload.Tunnel
}

func createUnifiedP2PTCPTunnel(t *testing.T, s *Server, token, name, policy, ingressID, targetID string, ingressPort int, targetHost string, targetPort int) tunnelSpecAPI {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"name":%q,"topology":"client_to_client","ingress":{"location":"client","client_id":%q,"type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["127.0.0.0/8"]}},"target":{"location":"client","client_id":%q,"type":"tcp_service","config":{"ip":%q,"port":%d}},"transport_policy":%q}`, name, ingressID, ingressPort, targetID, targetHost, targetPort, policy))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create P2P TCP tunnel: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatal(err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	return created
}

func dialUnifiedTCP(t *testing.T, port int) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial unified TCP ingress: %v", err)
	}
	return conn
}

func assertConnEchoExact(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write exact payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read exact payload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got=%q want=%q", got, payload)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func assertNoExtraTCPPayload(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if n, err := conn.Read(one[:]); err == nil || n != 0 {
		t.Fatalf("unexpected duplicate TCP payload: n=%d byte=%q err=%v", n, one[:n], err)
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("connection ended instead of remaining idle: %v", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func waitForUnifiedP2PState(t *testing.T, s *Server, tunnelID, state, actual string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stored, ok, err := s.findStoredTunnelByID(tunnelID)
		if err == nil && ok && stored.P2P.State == state && stored.ActualTransport == actual {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	stored, _, _ := s.findStoredTunnelByID(tunnelID)
	t.Fatalf("P2P state did not converge: got state=%q actual=%q want state=%q actual=%q", stored.P2P.State, stored.ActualTransport, state, actual)
}

func waitForP2PTrafficCounters(t *testing.T, s *Server, tunnelID string, ingressWant, egressWant uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stored, ok, _ := s.findStoredTunnelByID(tunnelID)
		if ok {
			ingress, egress, found := s.p2p.statsForTunnel(stored.P2P.SessionID, tunnelID)
			if found && ingress == ingressWant && egress == egressWant {
				return
			}
			if found && (ingress > ingressWant || egress > egressWant) {
				t.Fatalf("P2P traffic exceeded payload: got=(%d,%d) want=(%d,%d)", ingress, egress, ingressWant, egressWant)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("P2P traffic counters did not reach exact payload (%d,%d)", ingressWant, egressWant)
}

func waitForTrafficAccumulatorEntries(t *testing.T, s *Server, minimum int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.trafficAccumulator.Len() >= minimum {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("traffic accumulator entries=%d, want at least %d", s.trafficAccumulator.Len(), minimum)
}

func waitForTransportTrafficExact(t *testing.T, s *Server, tunnelID, transport string, ingressWant, egressWant uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var ingress, egress uint64
		for _, delta := range snapshotTrafficAccumulator(s.trafficAccumulator) {
			if delta.TunnelID == tunnelID && delta.Transport == transport {
				ingress += delta.IngressBytes
				egress += delta.EgressBytes
			}
		}
		if ingress == ingressWant && egress == egressWant {
			return
		}
		if ingress > ingressWant || egress > egressWant {
			t.Fatalf("transport %s traffic exceeded expected bytes: got=(%d,%d) want=(%d,%d)", transport, ingress, egress, ingressWant, egressWant)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("transport %s traffic did not reach exact bytes (%d,%d): %+v", transport, ingressWant, egressWant, snapshotTrafficAccumulator(s.trafficAccumulator))
}

func snapshotTrafficAccumulator(acc *trafficAccumulator) []TrafficDelta {
	if acc == nil {
		return nil
	}
	var deltas []TrafficDelta
	for i := range acc.shards {
		shard := &acc.shards[i]
		shard.mu.Lock()
		for _, delta := range shard.pending {
			deltas = append(deltas, delta)
		}
		shard.mu.Unlock()
	}
	return deltas
}

func assertTransportTrafficExact(t *testing.T, deltas []TrafficDelta, tunnelID string, wants map[string][2]uint64) {
	t.Helper()
	got := make(map[string][2]uint64)
	for _, delta := range deltas {
		if delta.TunnelID != tunnelID {
			continue
		}
		current := got[delta.Transport]
		current[0] += delta.IngressBytes
		current[1] += delta.EgressBytes
		got[delta.Transport] = current
	}
	if len(got) != len(wants) {
		t.Fatalf("transport bucket count mismatch: got=%+v want=%+v all=%+v", got, wants, deltas)
	}
	for transport, want := range wants {
		if got[transport] != want {
			t.Fatalf("transport %s bytes=%v want=%v all=%+v", transport, got[transport], want, deltas)
		}
	}
}

func assertUnifiedE2EMigrationIdentity(t *testing.T, before, after tunnelSpecAPI, targetClientID string) {
	t.Helper()
	if after.ID != before.ID || after.Revision != before.Revision+1 {
		t.Fatalf("migrated tunnel identity mismatch: before=%+v after=%+v", before, after)
	}
	if after.OwnerClientID != targetClientID || after.Target.ClientID != targetClientID {
		t.Fatalf("migrated tunnel owner/target mismatch: %+v", after)
	}
	if after.Ingress.Location != before.Ingress.Location ||
		after.Ingress.ClientID != before.Ingress.ClientID ||
		after.Ingress.Type != before.Ingress.Type ||
		!bytes.Equal(after.Ingress.Config, before.Ingress.Config) {
		t.Fatalf("migration changed ingress: before=%+v after=%+v", before.Ingress, after.Ingress)
	}
}

func assertUnifiedTCPEcho(t *testing.T, ingressPort int, payload []byte) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial TCP ingress: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set TCP ingress deadline: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write TCP ingress payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read TCP ingress payload: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("TCP echoed payload mismatch: got %q want %q", got, payload)
	}
}

func assertUnifiedUDPEcho(t *testing.T, ingressPort int, payload []byte) {
	t.Helper()
	conn, err := net.DialTimeout("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial UDP ingress: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set UDP ingress deadline: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write UDP ingress payload: %v", err)
	}
	got := make([]byte, len(payload))
	n, err := conn.Read(got)
	if err != nil {
		t.Fatalf("read UDP ingress payload: %v", err)
	}
	if string(got[:n]) != string(payload) {
		t.Fatalf("UDP echoed payload mismatch: got %q want %q", got[:n], payload)
	}
}

func startUnifiedE2EClient(t *testing.T, s *Server, serverURL, installID string) *clientpkg.Client {
	t.Helper()
	c, errCh := launchUnifiedE2EClient(t, s, serverURL, installID, t.TempDir())
	t.Cleanup(func() { stopUnifiedE2EClient(t, c, errCh) })
	return c
}

func launchUnifiedE2EClient(t *testing.T, s *Server, serverURL, installID, dataDir string) (*clientpkg.Client, chan error) {
	t.Helper()
	c := clientpkg.New(serverURL, "test-key")
	c.InstallID = installID
	c.DataDir = dataDir
	c.DisableReconnect = true
	c.Logger = clientpkg.NewEventLogger(clientpkg.LogFormatJSON, io.Discard)

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Start()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("client %s exited before ready: %v", installID, err)
		default:
		}
		if id := c.CurrentClientID(); id != "" {
			if live, ok := s.loadLiveClient(id); ok && clientHasDataSession(live) {
				return c, errCh
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("client %s did not become ready", installID)
	return c, errCh
}

func stopUnifiedE2EClient(t *testing.T, c *clientpkg.Client, errCh chan error) {
	t.Helper()
	if c == nil {
		return
	}
	c.Shutdown()
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("client %s did not shut down", c.InstallID)
	}
}

func waitForUnifiedE2EClientReady(t *testing.T, s *Server, c *clientpkg.Client) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		id := c.CurrentClientID()
		if id != "" {
			if live, ok := s.loadLiveClient(id); ok && clientHasDataSession(live) {
				return id
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("client did not keep a ready live session")
	return ""
}

func dialSOCKS5ConnectNoAuth(t *testing.T, proxyAddr, targetHost string, targetPort int) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5 proxy %s: %v", proxyAddr, err)
	}
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		_ = conn.Close()
		t.Fatalf("set SOCKS5 client deadline: %v", err)
	}
	if _, err := conn.Write([]byte{socks5wire.Version, 0x01, socks5wire.MethodNoAuth}); err != nil {
		_ = conn.Close()
		t.Fatalf("write SOCKS5 method negotiation: %v", err)
	}
	var methodResp [2]byte
	if _, err := io.ReadFull(conn, methodResp[:]); err != nil {
		_ = conn.Close()
		t.Fatalf("read SOCKS5 method response: %v", err)
	}
	if methodResp != [2]byte{socks5wire.Version, socks5wire.MethodNoAuth} {
		_ = conn.Close()
		t.Fatalf("SOCKS5 method response: got %#v", methodResp)
	}

	req := buildSOCKS5ConnectRequest(t, targetHost, targetPort)
	if _, err := conn.Write(req); err != nil {
		_ = conn.Close()
		t.Fatalf("write SOCKS5 CONNECT request: %v", err)
	}
	if rep := readSOCKS5Reply(t, conn); rep != socks5wire.RepSuccess {
		_ = conn.Close()
		t.Fatalf("SOCKS5 CONNECT reply: want success, got %#x", rep)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		t.Fatalf("clear SOCKS5 client deadline: %v", err)
	}
	return conn
}

func buildSOCKS5ConnectRequest(t *testing.T, targetHost string, targetPort int) []byte {
	t.Helper()
	if targetPort < 1 || targetPort > 65535 {
		t.Fatalf("invalid target port %d", targetPort)
	}
	if ip := net.ParseIP(targetHost); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req := []byte{socks5wire.Version, socks5wire.CommandConnect, 0x00, socks5wire.AddrIPv4, ip4[0], ip4[1], ip4[2], ip4[3], 0, 0}
			binary.BigEndian.PutUint16(req[8:10], uint16(targetPort))
			return req
		}
		ip16 := ip.To16()
		if ip16 == nil {
			t.Fatalf("invalid IP target %q", targetHost)
		}
		req := make([]byte, 4+16+2)
		req[0], req[1], req[2], req[3] = socks5wire.Version, socks5wire.CommandConnect, 0x00, socks5wire.AddrIPv6
		copy(req[4:20], ip16)
		binary.BigEndian.PutUint16(req[20:22], uint16(targetPort))
		return req
	}
	if len(targetHost) == 0 || len(targetHost) > 255 {
		t.Fatalf("invalid domain target %q", targetHost)
	}
	req := []byte{socks5wire.Version, socks5wire.CommandConnect, 0x00, socks5wire.AddrDomain, byte(len(targetHost))}
	req = append(req, []byte(targetHost)...)
	req = append(req, 0, 0)
	binary.BigEndian.PutUint16(req[len(req)-2:], uint16(targetPort))
	return req
}

func readSOCKS5Reply(t *testing.T, conn net.Conn) byte {
	t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatalf("read SOCKS5 reply header: %v", err)
	}
	if header[0] != socks5wire.Version || header[2] != 0x00 {
		t.Fatalf("invalid SOCKS5 reply header: %#v", header)
	}
	switch header[3] {
	case socks5wire.AddrIPv4:
		var rest [6]byte
		if _, err := io.ReadFull(conn, rest[:]); err != nil {
			t.Fatalf("read SOCKS5 IPv4 reply body: %v", err)
		}
	case socks5wire.AddrIPv6:
		var rest [18]byte
		if _, err := io.ReadFull(conn, rest[:]); err != nil {
			t.Fatalf("read SOCKS5 IPv6 reply body: %v", err)
		}
	case socks5wire.AddrDomain:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			t.Fatalf("read SOCKS5 domain reply length: %v", err)
		}
		rest := make([]byte, int(length[0])+2)
		if _, err := io.ReadFull(conn, rest); err != nil {
			t.Fatalf("read SOCKS5 domain reply body: %v", err)
		}
	default:
		t.Fatalf("unsupported SOCKS5 reply address type %#x", header[3])
	}
	return header[1]
}

func assertSOCKS5Echo(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set SOCKS5 echo deadline: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write SOCKS5 payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read SOCKS5 echoed payload: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("SOCKS5 echoed payload mismatch: got %q want %q", got, payload)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		t.Fatalf("clear SOCKS5 echo deadline: %v", err)
	}
}

func waitForUnifiedTunnelRuntimeState(t *testing.T, s *Server, token, tunnelID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last tunnelSpecAPI
	for time.Now().Before(deadline) {
		resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodGet, "/api/tunnels/"+tunnelID, token, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET tunnel: want 200, got %d body=%s", resp.Code, resp.Body.String())
		}
		if err := mustDecodeJSON(t, resp.Body, &last); err != nil {
			t.Fatalf("decode tunnel: %v", err)
		}
		if last.RuntimeState == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tunnel %s runtime_state: want %q, last=%q issues=%+v", tunnelID, want, last.RuntimeState, last.Issues)
}

func startTestTCPEchoService(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo service: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func startTestUDPEchoService(t *testing.T) (string, int) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP echo service: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteTo(buf[:n], addr)
		}
	}()

	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String(), addr.Port
}

func newIPv4HTTPTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test HTTP server: %v", err)
	}
	ts := httptest.NewUnstartedServer(handler)
	ts.Listener = ln
	ts.Start()
	return ts
}
