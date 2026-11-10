package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	clientpkg "netsgo/internal/client"
	"netsgo/pkg/protocol"
)

func tlsSNIAPIRequest(name, clientID, domain string, port int) tunnelCreateRequestAPI {
	req := httpDomainAPIRequest(name, clientID, domain)
	req.Ingress.Type = protocol.IngressTypeTLSSNI
	req.Ingress.Config = mustRawJSON(protocol.TLSSNIConfig{Domain: domain, AllowedSourceCIDRs: allowAllSourceCIDRs()})
	req.Target.Config = mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: port})
	return req
}

func TestTLSSNIAPIValidationAndSharedDomainClaims(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()
	target := createUnifiedAPITestClient(t, s, "sni-api", "sni-api")
	req := tlsSNIAPIRequest("tls", target.ID, "APP.EXAMPLE.TEST.", 443)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, mustRawJSON(req))
	if resp.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	stored, err := s.store.GetTunnelByID(created.ID)
	if err != nil || stored.Ingress.Type != protocol.IngressTypeTLSSNI || tunnelIngressDomain(stored) != "app.example.test" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	assertHTTPDomainClaim(t, s.store, "app.example.test", created.ID)
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, mustRawJSON(httpDomainAPIRequest("http-conflict", target.ID, "app.example.test")))
	if resp.Code != http.StatusConflict {
		t.Fatalf("HTTP/TLS duplicate: %d %s", resp.Code, resp.Body.String())
	}
	for _, bad := range []tunnelCreateRequestAPI{
		tlsSNIAPIRequest("bad-domain", target.ID, "*.*.*.*.example.test", 443),
		tlsSNIAPIRequest("bad-auth", target.ID, "auth.example.test", 443),
	} {
		if bad.Name == "bad-auth" {
			bad.Ingress.Config = mustRawJSON(httpHostConfigAPI{Domain: "auth.example.test", Auth: protocol.HTTPAuthConfig{Type: "basic", Username: "user", Password: "Password123"}})
		}
		resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, mustRawJSON(bad))
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s: %d %s", bad.Name, resp.Code, resp.Body.String())
		}
	}
	req.Target.Type = protocol.TargetTypeUDPService
	if err := validateUnifiedEndpointCombination(req.Topology, req.Ingress, req.Target); err == nil {
		t.Fatal("TLS accepted UDP target")
	}
	req.Target.Type = protocol.TargetTypeTCPService
	req.Topology = protocol.TunnelTopologyClientToClient
	req.Ingress.Location, req.Ingress.ClientID = protocol.EndpointLocationClient, "other-client"
	if err := validateUnifiedEndpointCombination(req.Topology, req.Ingress, req.Target); err == nil {
		t.Fatal("TLS accepted client ingress")
	}
	if _, err := s.auth.adminStore.db.Exec(`UPDATE server_config SET server_addr = 'https://panel.example.test:9443' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, mustRawJSON(tlsSNIAPIRequest("reserved", target.ID, "panel.example.test", 443)))
	if resp.Code != http.StatusConflict || !responseHasTunnelErrorCode(t, resp, protocol.TunnelMutationErrorCodeServerAddrConflict) {
		t.Fatalf("reserved management SNI: %d %s", resp.Code, resp.Body.String())
	}
}

func TestTLSSNIPassthroughEndToEndWithRealClient(t *testing.T) {
	testTLSSNIPassthrough(t, sniE2EOptions{})
}

func TestTLSSNIPassthroughWithPlainManagement(t *testing.T) {
	testTLSSNIPassthrough(t, sniE2EOptions{plainManagement: true})
}

func TestTLSSNIPassthroughThroughNginx(t *testing.T) {
	if os.Getenv("NETSGO_TEST_SNI_NGINX") != "1" {
		t.Skip("set NETSGO_TEST_SNI_NGINX=1 to run nginx stream integration")
	}
	testTLSSNIPassthrough(t, sniE2EOptions{proxy: "nginx"})
}

func TestTLSSNIPassthroughThroughCaddy(t *testing.T) {
	if os.Getenv("NETSGO_TEST_SNI_CADDY_BINARY") == "" {
		t.Skip("set NETSGO_TEST_SNI_CADDY_BINARY to a caddy-l4 binary")
	}
	testTLSSNIPassthrough(t, sniE2EOptions{proxy: "caddy"})
}

func TestTLSSNIPassthroughWithOldClient(t *testing.T) {
	binary := os.Getenv("NETSGO_TEST_SNI_CLIENT_BINARY")
	if binary == "" {
		t.Skip("set NETSGO_TEST_SNI_CLIENT_BINARY to the compatibility baseline client")
	}
	testTLSSNIPassthrough(t, sniE2EOptions{clientBinary: binary})
}

type sniE2EOptions struct {
	plainManagement bool
	proxy           string
	clientBinary    string
}

func testTLSSNIPassthrough(t *testing.T, options sniE2EOptions) {
	t.Helper()
	s := newUnifiedE2ETestServer(t)
	if _, err := s.auth.adminStore.db.Exec(`UPDATE server_config SET server_addr = 'https://panel.example.test:9443' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewUnstartedServer(s.newHTTPHandler())
	if options.proxy == "nginx" {
		_ = front.Listener.Close()
		var err error
		front.Listener, err = net.Listen("tcp4", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
	}
	front.Listener = newSNIListener(front.Listener, s.routeSNIConnection)
	if options.plainManagement {
		front.Start()
	} else {
		front.StartTLS()
	}
	t.Cleanup(front.Close)
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")
	serverURL, dialAddr := front.URL, front.Listener.Addr().String()
	if options.proxy != "" {
		dialAddr = startSNIProxy(t, options.proxy, front.Listener.Addr().(*net.TCPAddr).Port)
		serverURL = "https://" + dialAddr
	}
	var clientID string
	if options.clientBinary != "" {
		clientID = startOldSNIClient(t, s, options.clientBinary, serverURL)
	} else {
		client := clientpkg.New(serverURL, "test-key")
		client.TLSSkipVerify, client.DisableReconnect = true, true
		client.InstallID, client.DataDir = "sni-real-client", t.TempDir()
		client.Logger = clientpkg.NewEventLogger(clientpkg.LogFormatJSON, io.Discard)
		errCh := make(chan error, 1)
		go func() { errCh <- client.Start() }()
		t.Cleanup(func() { stopUnifiedE2EClient(t, client, errCh) })
		clientID = waitForUnifiedE2EClientReady(t, s, client)
	}
	var backendHits atomic.Int32
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHits.Add(1)
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Tunnel-Host", r.Host)
		_, _ = w.Write(payload)
	}))
	backendTLS := &TLSConfig{Mode: TLSModeAuto, AutoDir: t.TempDir()}
	var tlsErr error
	backend.TLS, _, tlsErr = backendTLS.loadOrBuildTLSConfig(t.TempDir())
	if tlsErr != nil {
		t.Fatal(tlsErr)
	}
	backend.StartTLS()
	t.Cleanup(backend.Close)
	backendPort := backend.Listener.Addr().(*net.TCPAddr).Port
	create := func(name, domain string) tunnelSpecAPI {
		t.Helper()
		resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, mustRawJSON(tlsSNIAPIRequest(name, clientID, domain, backendPort)))
		if resp.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", domain, resp.Code, resp.Body.String())
		}
		var spec tunnelSpecAPI
		if err := json.Unmarshal(resp.Body.Bytes(), &spec); err != nil {
			t.Fatal(err)
		}
		waitForUnifiedTunnelRuntimeState(t, s, token, spec.ID, protocol.TunnelRuntimeStateActive)
		return spec
	}
	create("broad-sni", "*.example.test")
	exact := create("exact-sni", "app.example.test")
	claim, found, err := s.store.findHTTPDomainClaim("app.example.test")
	if err != nil || !found || claim.ID != exact.ID || claim.Ingress.Type != protocol.IngressTypeTLSSNI {
		t.Fatalf("SNI claim: %+v found=%v err=%v", claim, found, err)
	}
	route, found := s.findRuntimeDomainRouteForClaim("app.example.test", &claim, protocol.IngressTypeTLSSNI)
	if !found || !route.serviceable() {
		t.Fatalf("SNI runtime missing: found=%v config=%+v", found, route.config)
	}
	dial := func(name string, version uint16) (*tls.Conn, error) {
		conn, err := net.DialTimeout("tcp", dialAddr, 3*time.Second)
		if err != nil {
			return nil, err
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		tlsConn := tls.Client(conn, &tls.Config{ServerName: name, InsecureSkipVerify: true, MinVersion: version, MaxVersion: version})
		if err := tlsConn.HandshakeContext(context.Background()); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return tlsConn, nil
	}
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(fmt.Sprintf("TLS-%x", version), func(t *testing.T) {
			conn, err := dial("app.example.test", version)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if !bytes.Equal(conn.ConnectionState().PeerCertificates[0].Raw, backend.Certificate().Raw) {
				t.Fatal("business TLS was terminated at NetsGo")
			}
			payload := strings.Repeat("opaque-tls-payload-", 8192)
			req, _ := http.NewRequest(http.MethodPost, "https://app.example.test/echo", strings.NewReader(payload))
			if err := req.Write(conn); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || string(body) != payload || resp.Header.Get("X-Tunnel-Host") != "app.example.test" {
				t.Fatalf("TLS payload/Host changed: status=%d header=%v bytes=%d body=%q err=%v", resp.StatusCode, resp.Header, len(body), body[:min(len(body), 128)], err)
			}
		})
	}
	// The management name must win even when a business wildcard covers it.
	if !options.plainManagement {
		management, err := dial("panel.example.test", tls.VersionTLS13)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(management.ConnectionState().PeerCertificates[0].Raw, front.Certificate().Raw) {
			t.Fatal("management SNI was hijacked by wildcard")
		}
		_ = management.Close()
		// An exact HTTP claim also wins over the broad TLS claim at the TLS layer.
		httpReq := httpDomainAPIRequest("exact-http", clientID, "http.example.test")
		httpResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, mustRawJSON(httpReq))
		if httpResp.Code != http.StatusCreated {
			t.Fatalf("HTTP claim: %d %s", httpResp.Code, httpResp.Body.String())
		}
		httpConn, err := dial("http.example.test", tls.VersionTLS13)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(httpConn.ConnectionState().PeerCertificates[0].Raw, front.Certificate().Raw) {
			t.Fatal("exact HTTP claim was intercepted by TLS wildcard")
		}
		_ = httpConn.Close()
	}
	// A TLS claim must never be served by a broader HTTP route.
	broadHTTP := httpDomainAPIRequest("broad-http", clientID, "*.other.test")
	broadResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, mustRawJSON(broadHTTP))
	if broadResp.Code != http.StatusCreated {
		t.Fatalf("HTTP wildcard: %d %s", broadResp.Code, broadResp.Body.String())
	}
	create("tls-over-http", "tls.other.test")
	if _, found, err := s.findHTTPRouteByHost("tls.other.test"); err != nil || found {
		t.Fatalf("TLS claim fell back to HTTP: found=%v err=%v", found, err)
	}
	// Deny loopback peers before opening a stream to the backend.
	deniedReq := tlsSNIAPIRequest("source-denied", clientID, "denied.example.test", backendPort)
	deniedReq.Ingress.Config = mustRawJSON(protocol.TLSSNIConfig{Domain: "denied.example.test", AllowedSourceCIDRs: []string{"192.0.2.0/24"}})
	deniedResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, mustRawJSON(deniedReq))
	if deniedResp.Code != http.StatusCreated {
		t.Fatalf("denied source tunnel: %d %s", deniedResp.Code, deniedResp.Body.String())
	}
	var denied tunnelSpecAPI
	if err := json.Unmarshal(deniedResp.Body.Bytes(), &denied); err != nil {
		t.Fatal(err)
	}
	waitForUnifiedTunnelRuntimeState(t, s, token, denied.ID, protocol.TunnelRuntimeStateActive)
	if conn, err := dial("denied.example.test", tls.VersionTLS13); err == nil {
		_ = conn.Close()
		t.Fatal("TLS source CIDR policy was bypassed")
	}
	// A committed stop must reject new connections even before reconciliation
	// has removed the previously active runtime.
	if _, err := s.store.db.Exec(`UPDATE tunnels SET desired_state = 'stopped' WHERE id = ?`, exact.ID); err != nil {
		t.Fatal(err)
	}
	if conn, err := dial("app.example.test", tls.VersionTLS13); err == nil {
		_ = conn.Close()
		t.Fatal("stopped persisted claim reused its active runtime")
	}
	if _, err := s.store.db.Exec(`UPDATE tunnels SET desired_state = 'running' WHERE id = ?`, exact.ID); err != nil {
		t.Fatal(err)
	}
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPut, "/api/tunnels/"+exact.ID+"/stop", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", resp.Code, resp.Body.String())
	}
	hits := backendHits.Load()
	if conn, err := dial("app.example.test", tls.VersionTLS13); err == nil {
		_ = conn.Close()
		t.Fatal("stopped exact SNI fell back to wildcard")
	}
	if backendHits.Load() != hits {
		t.Fatal("unavailable exact route reached backend")
	}
}
