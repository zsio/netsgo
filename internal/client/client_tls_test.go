package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

// ============================================================
// Test helper functions
// ============================================================

// generateTestCert creates a self-signed ECDSA P-256 certificate for tests
func generateTestCert(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: "test-server"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	x509Cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}, x509Cert
}

// computeTestFingerprint computes a certificate fingerprint in the same format used by client checkTLSFingerprint
func computeTestFingerprint(certDER []byte) string {
	hash := sha256.Sum256(certDER)
	hexStr := strings.ToUpper(hex.EncodeToString(hash[:]))
	parts := make([]string, 0, len(hexStr)/2)
	for i := 0; i < len(hexStr); i += 2 {
		end := i + 2
		if end > len(hexStr) {
			end = len(hexStr)
		}
		parts = append(parts, hexStr[i:end])
	}
	return strings.Join(parts, ":")
}

func saveTestClientState(t *testing.T, path string, state persistedState) {
	t.Helper()
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func loadTestClientState(t *testing.T, path string) persistedState {
	t.Helper()
	store, err := newClientStateStore(path)
	if err != nil {
		t.Fatalf("newClientStateStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	state, ok, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok {
		t.Fatal("expected saved client identity")
	}
	return state
}

// startTLSWSServer starts a TLS WebSocket server and returns the server and wss:// URL
func startTLSWSServer(t *testing.T, cert tls.Certificate) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	ts := httptest.NewUnstartedServer(mux)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	wsURL := "wss" + strings.TrimPrefix(ts.URL, "https") + "/ws"
	return ts, wsURL
}

// dialTLSWS connects to a TLS WebSocket (InsecureSkipVerify skips CA verification)
func dialTLSWS(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("TLS WebSocket connection failed: %v", err)
	}
	return conn
}

// ============================================================
// Part 1: buildTLSConfig unit tests
// ============================================================

func TestBuildTLSConfig_Defaults(t *testing.T) {
	c := New("wss://example.com", "key")
	cfg := c.buildTLSConfig("example.com")

	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion should be TLS 1.2, got 0x%04x", cfg.MinVersion)
	}
	if cfg.InsecureSkipVerify {
		t.Error("default InsecureSkipVerify should be false")
	}
	if cfg.ServerName != "example.com" {
		t.Errorf("ServerName should be 'example.com', got %q", cfg.ServerName)
	}
}

func TestBuildTLSConfig_SkipVerify(t *testing.T) {
	c := New("wss://example.com", "key")
	c.TLSSkipVerify = true
	cfg := c.buildTLSConfig("example.com")

	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true when TLSSkipVerify=true")
	}
}

func TestBuildTLSConfig_ServerName_Variants(t *testing.T) {
	tests := []struct {
		host       string
		expectName string
	}{
		{"example.com", "example.com"},
		{"127.0.0.1", "127.0.0.1"},
		{"my-tunnel.internal", "my-tunnel.internal"},
		{"localhost", "localhost"},
	}
	for _, tt := range tests {
		c := New("wss://"+tt.host, "key")
		cfg := c.buildTLSConfig(tt.host)
		if cfg.ServerName != tt.expectName {
			t.Errorf("buildTLSConfig(%q): ServerName = %q, want %q", tt.host, cfg.ServerName, tt.expectName)
		}
	}
}

// ============================================================
// Part 2: checkTLSFingerprint unit tests
// ============================================================

func TestCheckTLSFingerprint_NonTLSConn_Skipped(t *testing.T) {
	// Plain HTTP WS server (non-TLS)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connection failed: %v", err)
	}
	defer func() { _ = conn.Close() }()

	c := New("ws://localhost", "key")
	// Calling checkTLSFingerprint on a non-TLS connection should return nil directly
	if err := c.checkTLSFingerprint(conn); err != nil {
		t.Errorf("non-TLS connections should not return an error, got: %v", err)
	}
}

func TestCheckTLSFingerprint_TOFU_FirstConnect_RecordsFingerprint(t *testing.T) {
	cert, x509Cert := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	conn := dialTLSWS(t, wsURL)
	defer func() { _ = conn.Close() }()

	c := New("wss://localhost", "key")
	// Empty TLSFingerprint -> first TOFU connection
	if err := c.checkTLSFingerprint(conn); err != nil {
		t.Fatalf("first TOFU connection should not error: %v", err)
	}

	expectedFP := computeTestFingerprint(x509Cert.Raw)
	if c.CurrentTLSFingerprint() != expectedFP {
		t.Errorf("TOFU should record the fingerprint:\nwant: %s\ngot: %s", expectedFP, c.TLSFingerprint)
	}
}

func TestCheckTLSFingerprint_TOFU_SameFingerprint_Passes(t *testing.T) {
	cert, x509Cert := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	expectedFP := computeTestFingerprint(x509Cert.Raw)

	conn := dialTLSWS(t, wsURL)
	defer func() { _ = conn.Close() }()

	c := New("wss://localhost", "key")
	c.TLSFingerprint = expectedFP // Simulate an existing fingerprint

	if err := c.checkTLSFingerprint(conn); err != nil {
		t.Errorf("a matching fingerprint should not error: %v", err)
	}
}

func TestCheckTLSFingerprint_TOFU_DifferentFingerprint_Rejects(t *testing.T) {
	cert, _ := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	conn := dialTLSWS(t, wsURL)
	defer func() { _ = conn.Close() }()

	c := New("wss://localhost", "key")
	c.TLSFingerprint = "AA:BB:CC:DD:FAKE:FINGERPRINT" // Forged old fingerprint

	err := c.checkTLSFingerprint(conn)
	if err == nil {
		t.Fatal("fingerprint mismatch should return an error")
	}
	if !strings.Contains(err.Error(), "TLS certificate fingerprint mismatch") {
		t.Errorf("error should contain 'TLS certificate fingerprint mismatch', got: %v", err)
	}
	if !strings.Contains(err.Error(), "man-in-the-middle attack") {
		t.Errorf("error should mention a man-in-the-middle attack: %v", err)
	}
}

func TestCheckTLSFingerprint_TOFU_PersistsToStateFile(t *testing.T) {
	cert, x509Cert := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	conn := dialTLSWS(t, wsURL)
	defer func() { _ = conn.Close() }()

	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client", clientDBFileName)

	c := New("wss://localhost", "key")
	c.InstallID = "test-install-id"
	c.DataDir = tmpDir

	if err := c.checkTLSFingerprint(conn); err != nil {
		t.Fatalf("first TOFU connection should not error: %v", err)
	}

	state := loadTestClientState(t, statePath)
	expectedFP := computeTestFingerprint(x509Cert.Raw)
	if state.TLSFingerprint != expectedFP {
		t.Errorf("fingerprint in state file is incorrect:\nwant: %s\ngot: %s", expectedFP, state.TLSFingerprint)
	}
}

// ============================================================
// Part 3: TLS fingerprint persistence tests
// ============================================================

func TestSaveTLSFingerprint_WritesCorrectly(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client", clientDBFileName)

	c := New("wss://localhost", "key")
	c.InstallID = "install-abc"
	c.Token = "token-xyz"
	c.DataDir = tmpDir

	fp := "AA:BB:CC:DD:EE:FF"
	if err := c.saveTLSFingerprint(fp); err != nil {
		t.Fatalf("failed to save fingerprint: %v", err)
	}

	state := loadTestClientState(t, statePath)
	if state.TLSFingerprint != fp {
		t.Errorf("wrong fingerprint: want %q, got %q", fp, state.TLSFingerprint)
	}
	if state.InstallID != "install-abc" {
		t.Errorf("InstallID should be preserved: want %q, got %q", "install-abc", state.InstallID)
	}
	if state.Token != "token-xyz" {
		t.Errorf("Token should be preserved: want %q, got %q", "token-xyz", state.Token)
	}
}

func TestSaveTLSFingerprint_WithInstallID(t *testing.T) {
	c := New("wss://localhost", "key")
	c.DataDir = t.TempDir()
	c.InstallID = "install-with-fingerprint"

	if err := c.saveTLSFingerprint("AA:BB"); err != nil {
		t.Errorf("saveTLSFingerprint() error = %v", err)
	}
}

func TestEnsureInstallID_LoadsTLSFingerprint(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client", clientDBFileName)

	saveTestClientState(t, statePath, persistedState{
		InstallID:      "saved-install-id",
		Token:          "saved-token",
		TLSFingerprint: "11:22:33:44:55:66",
	})

	// Have a new client load the state
	c := New("wss://localhost", "key")
	c.DataDir = tmpDir

	if err := c.ensureInstallID(); err != nil {
		t.Fatalf("ensureInstallID failed: %v", err)
	}

	if c.InstallID != "saved-install-id" {
		t.Errorf("InstallID should be loaded: want %q, got %q", "saved-install-id", c.InstallID)
	}
	if c.CurrentToken() != "saved-token" {
		t.Errorf("Token should be loaded: want %q, got %q", "saved-token", c.CurrentToken())
	}
	if c.CurrentTLSFingerprint() != "11:22:33:44:55:66" {
		t.Errorf("TLSFingerprint should be loaded: want %q, got %q", "11:22:33:44:55:66", c.CurrentTLSFingerprint())
	}
}

func TestEnsureInstallID_DoesNotOverwriteExistingFingerprint(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client", clientDBFileName)

	saveTestClientState(t, statePath, persistedState{
		InstallID:      "install-old",
		TLSFingerprint: "OLD:FP",
	})

	// The client already has a new fingerprint
	c := New("wss://localhost", "key")
	c.DataDir = tmpDir
	c.TLSFingerprint = "NEW:FP" // Existing value

	if err := c.ensureInstallID(); err != nil {
		t.Fatalf("ensureInstallID failed: %v", err)
	}

	// It should not be overwritten by the old value in the file
	if c.CurrentTLSFingerprint() != "NEW:FP" {
		t.Errorf("existing TLSFingerprint should not be overwritten: want %q, got %q", "NEW:FP", c.CurrentTLSFingerprint())
	}
}

func TestState_AllFieldsRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	// Save the full state
	c1 := New("wss://localhost", "key")
	c1.InstallID = "rt-install"
	c1.Token = "rt-token"
	c1.DataDir = tmpDir

	if err := c1.saveTLSFingerprint("RT:FP:AA:BB"); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Load with a new client
	c2 := New("wss://localhost", "key")
	c2.DataDir = tmpDir

	if err := c2.ensureInstallID(); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if c2.InstallID != "rt-install" {
		t.Errorf("InstallID lost: %q", c2.InstallID)
	}
	if c2.Token != "rt-token" {
		t.Errorf("Token lost: %q", c2.Token)
	}
	if c2.TLSFingerprint != "RT:FP:AA:BB" {
		t.Errorf("TLSFingerprint lost: %q", c2.TLSFingerprint)
	}
}

// ============================================================
// Part 4: end-to-end TLS scenario tests
// ============================================================

// Scenario: connect with wss:// and complete authentication
func TestScenario_TLS_ConnectAndAuth(t *testing.T) {
	cert, _ := generateTestCert(t)
	ms := newMockServer(true)

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/control", ms.controlHandler)
	httpMux.HandleFunc("/ws/data", ms.dataHandler)

	ts := httptest.NewUnstartedServer(httpMux)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	defer ts.Close()

	wssURL := "wss" + strings.TrimPrefix(ts.URL, "https")
	c := New(wssURL, "test-key")
	c.TLSSkipVerify = true
	c.DisableReconnect = true
	c.DataDir = t.TempDir()

	go func() { _ = c.Start() }()
	time.Sleep(3 * time.Second)

	if c.CurrentClientID() != "mock_client_1" {
		t.Errorf("after TLS authentication, ClientID: want 'mock_client_1', got %q", c.CurrentClientID())
	}
	if !c.UsesTLS() {
		t.Error("wss:// should set useTLS = true")
	}

	// The server should receive the authentication message
	msgs := ms.getReceivedMsgs()
	authFound := false
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypeAuth {
			authFound = true
			break
		}
	}
	if !authFound {
		t.Error("server should receive the auth message")
	}
}

// Scenario: ws:// connection does not use TLS
func TestScenario_PlainWS_NoTLSUsed(t *testing.T) {
	ms := newMockServer(true)
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/control", ms.controlHandler)
	httpMux.HandleFunc("/ws/data", ms.dataHandler)
	ts := httptest.NewServer(httpMux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	c := New(wsURL, "test-key")
	c.DisableReconnect = true
	c.DataDir = t.TempDir()

	go func() { _ = c.Start() }()
	time.Sleep(2 * time.Second)

	if c.UsesTLS() {
		t.Error("ws:// should not set useTLS")
	}
	if c.CurrentClientID() != "mock_client_1" {
		t.Errorf("after plain authentication, ClientID: want 'mock_client_1', got %q", c.CurrentClientID())
	}
}

// Scenario: skip TOFU fingerprint checks when TLSSkipVerify=true
func TestScenario_TLS_SkipVerify_SkipsFingerprintCheck(t *testing.T) {
	cert, _ := generateTestCert(t)
	ms := newMockServer(true)
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/control", ms.controlHandler)
	httpMux.HandleFunc("/ws/data", ms.dataHandler)

	ts := httptest.NewUnstartedServer(httpMux)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	defer ts.Close()

	wssURL := "wss" + strings.TrimPrefix(ts.URL, "https")
	c := New(wssURL, "test-key")
	c.TLSSkipVerify = true
	c.DisableReconnect = true
	c.DataDir = t.TempDir()

	go func() { _ = c.Start() }()
	time.Sleep(3 * time.Second)

	// TLSSkipVerify=true -> checkTLSFingerprint is not called -> the fingerprint is not recorded
	if c.CurrentTLSFingerprint() != "" {
		t.Errorf("should not record fingerprint when TLSSkipVerify=true, got: %q", c.CurrentTLSFingerprint())
	}
	if c.CurrentClientID() != "mock_client_1" {
		t.Errorf("authentication should succeed, ClientID: want 'mock_client_1', got %q", c.CurrentClientID())
	}
}

// Scenario: the data channel uses TLS when useTLS=true
func TestScenario_TLS_DataChannelUsesTLS(t *testing.T) {
	cert, _ := generateTestCert(t)
	ms := newMockServer(true)
	ms.authResp.ClientID = "tls-data-test"
	ms.authResp.DataToken = "tls-data-token"

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/data", ms.dataHandler)

	ts := httptest.NewUnstartedServer(httpMux)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	defer ts.Close()

	c := New("wss"+strings.TrimPrefix(ts.URL, "https"), "key")
	c.ClientID = "tls-data-test"
	c.dataToken = "tls-data-token"
	c.TLSSkipVerify = true

	if err := c.connectDataChannel(); err != nil {
		t.Fatalf("TLS data channel connection failed: %v", err)
	}

	c.dataMu.RLock()
	hasSession := c.dataSession != nil
	c.dataMu.RUnlock()

	if !hasSession {
		t.Error("dataSession should not be nil after a successful TLS data channel handshake")
	}
}

// Scenario: the data channel rejects TLS connections when useTLS=false
func TestScenario_PlainWS_DataChannelUsesPlainWS(t *testing.T) {
	// TLS-only server, client connects in plain text -> should fail
	cert, _ := generateTestCert(t)
	ms := newMockServer(true)
	ms.authResp.ClientID = "plain-data-test"
	ms.authResp.DataToken = "plain-data-token"

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ws/data", ms.dataHandler)

	ts := httptest.NewUnstartedServer(httpMux)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	defer ts.Close()

	c := New("ws"+strings.TrimPrefix(ts.URL, "http"), "key")
	c.normalizeServerAddr() // useTLS = false
	c.ClientID = "plain-data-test"
	c.dataToken = "plain-data-token"

	// Plain WS connection to a TLS server -> handshake fails
	err := c.connectDataChannel()
	if err == nil {
		t.Error("plain WS connection to a TLS server should fail")
	}
}

// Scenario: after the fingerprint is persisted, a new client instance can load it and pass verification
func TestScenario_TLS_FingerprintPersistedAndLoadedOnRestart(t *testing.T) {
	cert, x509Cert := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	tmpDir := t.TempDir()
	expectedFP := computeTestFingerprint(x509Cert.Raw)

	// ---- First connection: record the fingerprint ----
	conn1 := dialTLSWS(t, wsURL)
	c1 := New("wss://localhost", "key")
	c1.InstallID = "persist-test"
	c1.DataDir = tmpDir

	if err := c1.checkTLSFingerprint(conn1); err != nil {
		t.Fatalf("first connection should not error: %v", err)
	}
	_ = conn1.Close()

	if c1.TLSFingerprint != expectedFP {
		t.Fatalf("the fingerprint should be recorded after the first connection")
	}

	// ---- Simulate restart: a new client instance loads the state ----
	c2 := New("wss://localhost", "key")
	c2.DataDir = tmpDir
	if err := c2.ensureInstallID(); err != nil {
		t.Fatalf("failed to load state: %v", err)
	}

	if c2.TLSFingerprint != expectedFP {
		t.Errorf("fingerprint should be loaded after restart:\nwant: %s\ngot: %s", expectedFP, c2.TLSFingerprint)
	}

	// ---- Reconnect to the same server: the fingerprint should match ----
	conn2 := dialTLSWS(t, wsURL)
	defer func() { _ = conn2.Close() }()

	if err := c2.checkTLSFingerprint(conn2); err != nil {
		t.Errorf("reconnecting to the same server after restart should pass fingerprint verification: %v", err)
	}
}

// Scenario: TOFU reconnects to the same certificate -> passes
func TestScenario_TLS_TOFU_ReconnectSameCert_Passes(t *testing.T) {
	cert, _ := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	c := New("wss://localhost", "key")

	// First connection -> record the fingerprint
	conn1 := dialTLSWS(t, wsURL)
	if err := c.checkTLSFingerprint(conn1); err != nil {
		t.Fatalf("first connection failed: %v", err)
	}
	_ = conn1.Close()

	fp := c.CurrentTLSFingerprint()
	if fp == "" {
		t.Fatal("the fingerprint should have been recorded after the first connection")
	}

	// Second connection to the same server -> should pass
	conn2 := dialTLSWS(t, wsURL)
	defer func() { _ = conn2.Close() }()

	if err := c.checkTLSFingerprint(conn2); err != nil {
		t.Errorf("reconnecting to the same certificate should pass: %v", err)
	}

	if c.CurrentTLSFingerprint() != fp {
		t.Error("the fingerprint should not change")
	}
}

// Scenario: server certificate changes (simulated MITM or legitimate rotation) -> reject connection
func TestScenario_TLS_TOFU_DetectsCertChange(t *testing.T) {
	cert1, _ := generateTestCert(t)
	cert2, _ := generateTestCert(t) // Second, different certificate

	// First connect to the server using cert1
	ts1, wsURL1 := startTLSWSServer(t, cert1)
	conn1 := dialTLSWS(t, wsURL1)

	c := New("wss://localhost", "key")
	if err := c.checkTLSFingerprint(conn1); err != nil {
		t.Fatalf("first connection failed: %v", err)
	}
	_ = conn1.Close()
	ts1.Close()

	savedFP := c.CurrentTLSFingerprint()
	if savedFP == "" {
		t.Fatal("the fingerprint should have been recorded")
	}

	// Connect to a server using a different certificate -> the change should be detected
	ts2, wsURL2 := startTLSWSServer(t, cert2)
	defer ts2.Close()

	conn2 := dialTLSWS(t, wsURL2)
	defer func() { _ = conn2.Close() }()

	err := c.checkTLSFingerprint(conn2)
	if err == nil {
		t.Fatal("certificate changes should return an error")
	}
	if !strings.Contains(err.Error(), "TLS certificate fingerprint mismatch") {
		t.Errorf("error should contain 'TLS certificate fingerprint mismatch': %v", err)
	}

	// The fingerprint should not be updated
	if c.TLSFingerprint != savedFP {
		t.Error("the fingerprint should not be updated after detecting a change")
	}
}
