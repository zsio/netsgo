package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	clientpkg "netsgo/internal/client"
)

func TestUnifiedClientToClientTCPEndToEndWithRealClients(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := newIPv4HTTPTestServer(t, s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetAddr, targetPort := startTestTCPEchoService(t)
	targetClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-target")
	ingressClient := startUnifiedE2EClient(t, s, ts.URL, "install-e2e-c2c-ingress")
	targetID := waitForUnifiedE2EClientReady(t, s, targetClient)
	ingressID := waitForUnifiedE2EClientReady(t, s, ingressClient)
	ingressPort := reserveTCPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"e2e-c2c-tcp",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d}},
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
}

func startUnifiedE2EClient(t *testing.T, s *Server, serverURL, installID string) *clientpkg.Client {
	t.Helper()
	c := clientpkg.New(serverURL, "test-key")
	c.InstallID = installID
	c.DataDir = t.TempDir()
	c.DisableReconnect = true
	c.Logger = clientpkg.NewEventLogger(clientpkg.LogFormatJSON, io.Discard)

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Start()
	}()
	t.Cleanup(func() {
		c.Shutdown()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("client %s did not shut down", installID)
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("client %s exited before ready: %v", installID, err)
		default:
		}
		if id := c.CurrentClientID(); id != "" {
			if live, ok := s.loadLiveClient(id); ok && clientHasDataSession(live) {
				return c
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("client %s did not become ready", installID)
	return c
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
