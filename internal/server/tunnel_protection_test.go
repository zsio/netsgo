package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	clientpkg "netsgo/internal/client"
	"netsgo/pkg/protocol"
)

func protectedTCPRequest(name, clientID, host string, ingressPort, targetPort int) []byte {
	return []byte(fmt.Sprintf(`{"name":%q,"topology":"server_expose","ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d,"allowed_source_cidrs":["127.0.0.0/8"]}},"target":{"location":"client","client_id":%q,"type":"tcp_service","config":{"host":%q,"port":%d}},"transport_policy":"server_relay_only"}`, name, ingressPort, clientID, host, targetPort))
}

func TestTunnelProtectionRejectsCreateAndUpdateLoop(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	s.networkNamespaceID = "test-network"
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	c := startUnifiedE2EClient(t, s, ts.URL, "protection-target")
	id := waitForUnifiedE2EClientReady(t, s, c)
	live, _ := s.loadLiveClient(id)
	info := live.GetInfo()
	info.NetworkNamespaceID = "test-network"
	live.SetInfo(info)
	port := reserveTCPPort(t)
	bad := protectedTCPRequest("self-loop", id, "127.0.0.1", port, port)
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, bad)
	if resp.Code != http.StatusBadRequest || !responseHasTunnelErrorCode(t, resp, protocol.TunnelMutationErrorCodeTunnelLoop) {
		t.Fatalf("create=%d %s", resp.Code, resp.Body.String())
	}
	_, backendPort := startTestTCPEchoService(t)
	good := protectedTCPRequest("safe", id, "127.0.0.1", port, backendPort)
	resp = doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, good)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create safe=%d %s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	update := []byte(fmt.Sprintf(`{"expected_revision":%d,"spec":%s}`, created.Revision, bad))
	resp = doMuxRequest(t, s.StartHTTPOnly(), http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	if resp.Code != http.StatusBadRequest || !responseHasTunnelErrorCode(t, resp, protocol.TunnelMutationErrorCodeTunnelLoop) {
		t.Fatalf("update=%d %s", resp.Code, resp.Body.String())
	}
	assertUnifiedTCPEcho(t, port, []byte("existing tunnel survives rejected edit"))
	info.NetworkNamespaceID = "other-network"
	live.SetInfo(info)
	otherPort := reserveTCPPort(t)
	resp = doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, protectedTCPRequest("remote-same-port", id, "127.0.0.1", otherPort, otherPort))
	if resp.Code != http.StatusCreated {
		t.Fatalf("different network rejected: %d %s", resp.Code, resp.Body.String())
	}
}

func TestConnectionLimitPreservesExistingTrafficAndRecovers(t *testing.T) {
	for _, side := range []string{"server", "client"} {
		t.Run(side, func(t *testing.T) { testConnectionLimitRecovery(t, side) })
	}
}

func TestUDPConnectionLimitRejectsNewSourceWithoutStoppingTunnel(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	s.MaxConnections = 1
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	c := startUnifiedE2EClient(t, s, ts.URL, "udp-capacity-target")
	id := waitForUnifiedE2EClientReady(t, s, c)
	backendHost, backendPort := startTestUDPEchoService(t)
	port := reserveUDPPort(t)
	body := []byte(fmt.Sprintf(`{"name":"udp-capacity","topology":"server_expose","ingress":{"location":"server","type":"udp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["127.0.0.0/8"]}},"target":{"location":"client","client_id":%q,"type":"udp_service","config":{"host":%q,"port":%d}},"transport_policy":"server_relay_only"}`, port, id, backendHost, backendPort))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create UDP=%d %s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	dial := func() net.Conn {
		t.Helper()
		conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	echo := func(conn net.Conn) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		payload := []byte("existing UDP association")
		if _, err := conn.Write(payload); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(payload)+1)
		if n, err := conn.Read(buf); err != nil || !bytes.Equal(buf[:n], payload) {
			t.Fatalf("UDP echo=%q err=%v", buf[:n], err)
		}
	}
	first, excess := dial(), dial()
	echo(first)
	_ = excess.SetDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := excess.Write([]byte("new source")); err != nil {
		t.Fatal(err)
	}
	if _, err := excess.Read(make([]byte, 32)); err == nil {
		t.Fatal("excess UDP association was admitted")
	}
	echo(first)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	if active := s.dataConnections.Active(); active != 1 {
		t.Fatalf("active UDP associations=%d, want 1", active)
	}
}

func testConnectionLimitRecovery(t *testing.T, side string) {
	s := newUnifiedE2ETestServer(t)
	if side == "server" {
		s.MaxConnections = 2
	}
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	clientLimit := 10000
	if side == "client" {
		clientLimit = 2
	}
	c := clientpkg.New(ts.URL, "test-key")
	c.MaxConnections = clientLimit
	c.InstallID = "capacity-target"
	c.DataDir = t.TempDir()
	c.DisableReconnect = true
	c.Logger = clientpkg.NewEventLogger(clientpkg.LogFormatJSON, io.Discard)
	errCh := make(chan error, 1)
	go func() { errCh <- c.Start() }()
	t.Cleanup(func() { stopUnifiedE2EClient(t, c, errCh) })
	id := waitForUnifiedE2EClientReady(t, s, c)
	_, backendPort := startTestTCPEchoService(t)
	port := reserveTCPPort(t)
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, protectedTCPRequest("capacity", id, "127.0.0.1", port, backendPort))
	if resp.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	dial := func() net.Conn {
		t.Helper()
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	echo := func(conn net.Conn) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		p := []byte("download still active")
		if _, err := conn.Write(p); err != nil {
			t.Fatal(err)
		}
		out := make([]byte, len(p))
		if _, err := io.ReadFull(conn, out); err != nil || !bytes.Equal(p, out) {
			t.Fatalf("echo=%q err=%v", out, err)
		}
	}
	a, b := dial(), dial()
	echo(a)
	echo(b)
	if got := s.dataConnections.Active(); got != 2 {
		t.Fatalf("active=%d", got)
	}
	excess := dial()
	_ = excess.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := excess.Read(make([]byte, 1)); err == nil {
		t.Fatal("excess connection remained open")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("excess connection queued instead of rejected")
	}
	echo(a)
	echo(b)
	_ = a.Close()
	deadline := time.Now().Add(time.Second)
	for s.dataConnections.Active() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	echo(dial())
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
}

func TestLinuxNetworkNamespaceLoopRejectedAfterAuth(t *testing.T) {
	s := newUnifiedE2ETestServer(t)
	if s.networkNamespaceID == "" {
		t.Skip("OS does not provide network namespace identity")
	}
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	c := startUnifiedE2EClient(t, s, ts.URL, "linux-network-loop")
	id := waitForUnifiedE2EClientReady(t, s, c)
	live, _ := s.loadLiveClient(id)
	if live.GetInfo().NetworkNamespaceID != s.networkNamespaceID {
		t.Fatal("shared namespace identity lost during auth")
	}
	port := reserveTCPPort(t)
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, protectedTCPRequest("linux-loop", id, "127.0.0.1", port, port))
	if resp.Code != http.StatusBadRequest || !responseHasTunnelErrorCode(t, resp, protocol.TunnelMutationErrorCodeTunnelLoop) {
		t.Fatalf("loop accepted: %d %s", resp.Code, resp.Body.String())
	}
	// Simulate an older snapshot whose identity was unavailable to API validation.
	// Provision must still reject it before a listener is exposed.
	info := live.GetInfo()
	info.NetworkNamespaceID = ""
	live.SetInfo(info)
	resp = doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, protectedTCPRequest("restored-loop", id, "127.0.0.1", port, port))
	if resp.Code != http.StatusCreated {
		t.Fatalf("fixture create: %d %s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, protocol.TunnelRuntimeStateError)
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("rejected provision exposed a listener")
	}

	// Hostnames are not proactively probed. A real connection must be rejected
	// after resolution, with an error report rather than recursive target dials.
	port = reserveTCPPort(t)
	resp = doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, protectedTCPRequest("dns-loop", id, "localhost", port, port))
	if resp.Code != http.StatusCreated {
		t.Fatalf("DNS fixture: %d %s", resp.Code, resp.Body.String())
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &created)
	waitForUnifiedTunnelRuntimeState(t, s, token, created.ID, tunnelRuntimeStateActive)
	events := s.events.Subscribe()
	defer s.events.Unsubscribe(events)
	seed, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	_ = seed.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := seed.Read(make([]byte, 1)); err == nil {
		t.Fatal("loop not closed")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("loop stalled instead of rejected")
	}
	// Runtime reports schedule reconciliation, which may reprovision the
	// hostname before an API poll observes its transient issue. Verify the
	// accepted report event instead of requiring that issue to stay visible.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	reported := false
	for !reported {
		select {
		case event := <-events:
			if event.Type != "tunnel_changed" {
				continue
			}
			var payload struct {
				Action   string `json:"action"`
				ClientID string `json:"client_id"`
				Tunnel   struct {
					ID string `json:"id"`
				} `json:"tunnel"`
			}
			if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
				t.Fatal(err)
			}
			reported = payload.Action == "runtime_report" && payload.ClientID == id && payload.Tunnel.ID == created.ID
		case <-timer.C:
			t.Fatal("server did not accept the DNS loop runtime report")
		}
	}
	deadline := time.Now().Add(time.Second)
	for s.dataConnections.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.dataConnections.Active() != 0 {
		t.Fatal("loop leaked relay capacity")
	}
}
