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
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

// ============================================================
// 测试辅助函数
// ============================================================

// generateTestCert 为测试生成自签名 ECDSA P-256 证书
func generateTestCert(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
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
		t.Fatalf("创建证书失败: %v", err)
	}

	x509Cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("解析证书失败: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}, x509Cert
}

// computeTestFingerprint 计算证书指纹（与 client checkTLSFingerprint 格式一致）
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

// startTLSWSServer 启动 TLS WebSocket 服务器，返回 server 和 wss:// URL
func startTLSWSServer(t *testing.T, cert tls.Certificate) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
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

// dialTLSWS 连接 TLS WebSocket（InsecureSkipVerify 跳过 CA 校验）
func dialTLSWS(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("TLS WebSocket 连接失败: %v", err)
	}
	return conn
}

// ============================================================
// Part 1: buildTLSConfig 单元测试
// ============================================================

func TestBuildTLSConfig_Defaults(t *testing.T) {
	c := New("wss://example.com", "key")
	cfg := c.buildTLSConfig("example.com")

	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion 应为 TLS 1.2，得到 0x%04x", cfg.MinVersion)
	}
	if cfg.InsecureSkipVerify {
		t.Error("默认 InsecureSkipVerify 应为 false")
	}
	if cfg.ServerName != "example.com" {
		t.Errorf("ServerName 应为 'example.com'，得到 %q", cfg.ServerName)
	}
}

func TestBuildTLSConfig_SkipVerify(t *testing.T) {
	c := New("wss://example.com", "key")
	c.TLSSkipVerify = true
	cfg := c.buildTLSConfig("example.com")

	if !cfg.InsecureSkipVerify {
		t.Error("TLSSkipVerify=true 时 InsecureSkipVerify 应为 true")
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
			t.Errorf("buildTLSConfig(%q): ServerName = %q，期望 %q", tt.host, cfg.ServerName, tt.expectName)
		}
	}
}

// ============================================================
// Part 2: checkTLSFingerprint 单元测试
// ============================================================

func TestCheckTLSFingerprint_NonTLSConn_Skipped(t *testing.T) {
	// 普通 HTTP WS 服务器（非 TLS）
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
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
		t.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	c := New("ws://localhost", "key")
	// 非 TLS 连接调用 checkTLSFingerprint 应直接返回 nil
	if err := c.checkTLSFingerprint(conn); err != nil {
		t.Errorf("非 TLS 连接不应报错，得到: %v", err)
	}
}

func TestCheckTLSFingerprint_TOFU_FirstConnect_RecordsFingerprint(t *testing.T) {
	cert, x509Cert := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	conn := dialTLSWS(t, wsURL)
	defer conn.Close()

	c := New("wss://localhost", "key")
	// TLSFingerprint 为空 → TOFU 首次连接
	if err := c.checkTLSFingerprint(conn); err != nil {
		t.Fatalf("TOFU 首次连接不应报错: %v", err)
	}

	expectedFP := computeTestFingerprint(x509Cert.Raw)
	if c.CurrentTLSFingerprint() != expectedFP {
		t.Errorf("TOFU 应记录指纹:\n期望: %s\n实际: %s", expectedFP, c.TLSFingerprint)
	}
}

func TestCheckTLSFingerprint_TOFU_SameFingerprint_Passes(t *testing.T) {
	cert, x509Cert := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	expectedFP := computeTestFingerprint(x509Cert.Raw)

	conn := dialTLSWS(t, wsURL)
	defer conn.Close()

	c := New("wss://localhost", "key")
	c.TLSFingerprint = expectedFP // 模拟已有指纹

	if err := c.checkTLSFingerprint(conn); err != nil {
		t.Errorf("匹配的指纹不应报错: %v", err)
	}
}

func TestCheckTLSFingerprint_TOFU_DifferentFingerprint_Rejects(t *testing.T) {
	cert, _ := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	conn := dialTLSWS(t, wsURL)
	defer conn.Close()

	c := New("wss://localhost", "key")
	c.TLSFingerprint = "AA:BB:CC:DD:FAKE:FINGERPRINT" // 伪造的旧指纹

	err := c.checkTLSFingerprint(conn)
	if err == nil {
		t.Fatal("指纹不匹配应报错")
	}
	if !strings.Contains(err.Error(), "指纹不匹配") {
		t.Errorf("错误信息应包含'指纹不匹配'，得到: %v", err)
	}
	if !strings.Contains(err.Error(), "中间人攻击") {
		t.Errorf("错误信息应提到中间人攻击: %v", err)
	}
}

func TestCheckTLSFingerprint_TOFU_PersistsToStateFile(t *testing.T) {
	cert, x509Cert := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	conn := dialTLSWS(t, wsURL)
	defer conn.Close()

	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client.json")

	c := New("wss://localhost", "key")
	c.InstallID = "test-install-id"
	c.StatePath = statePath

	if err := c.checkTLSFingerprint(conn); err != nil {
		t.Fatalf("TOFU 首次连接不应报错: %v", err)
	}

	// 验证指纹已持久化到文件
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("读取状态文件失败: %v", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("解析状态文件失败: %v", err)
	}

	expectedFP := computeTestFingerprint(x509Cert.Raw)
	if state.TLSFingerprint != expectedFP {
		t.Errorf("状态文件中的指纹不正确:\n期望: %s\n实际: %s", expectedFP, state.TLSFingerprint)
	}
}

// ============================================================
// Part 3: TLS 指纹持久化测试
// ============================================================

func TestSaveTLSFingerprint_WritesCorrectly(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client.json")

	c := New("wss://localhost", "key")
	c.InstallID = "install-abc"
	c.Token = "token-xyz"
	c.StatePath = statePath

	fp := "AA:BB:CC:DD:EE:FF"
	if err := c.saveTLSFingerprint(fp); err != nil {
		t.Fatalf("保存指纹失败: %v", err)
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("读取状态文件失败: %v", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if state.TLSFingerprint != fp {
		t.Errorf("指纹不正确: 期望 %q，得到 %q", fp, state.TLSFingerprint)
	}
	if state.InstallID != "install-abc" {
		t.Errorf("InstallID 应保留: 期望 %q，得到 %q", "install-abc", state.InstallID)
	}
	if state.Token != "token-xyz" {
		t.Errorf("Token 应保留: 期望 %q，得到 %q", "token-xyz", state.Token)
	}
}

func TestSaveTLSFingerprint_EmptyStatePath_NoOp(t *testing.T) {
	c := New("wss://localhost", "key")
	c.StatePath = "" // 空路径

	// 不应报错，也不应写文件
	if err := c.saveTLSFingerprint("AA:BB"); err != nil {
		t.Errorf("空 StatePath 应直接返回 nil，得到: %v", err)
	}
}

func TestEnsureInstallID_LoadsTLSFingerprint(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client.json")

	// 先写一个包含指纹的状态文件
	state := persistedState{
		InstallID:      "saved-install-id",
		Token:          "saved-token",
		TLSFingerprint: "11:22:33:44:55:66",
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(statePath, data, 0o600)

	// 新 Client 加载状态
	c := New("wss://localhost", "key")
	c.StatePath = statePath

	if err := c.ensureInstallID(); err != nil {
		t.Fatalf("ensureInstallID 失败: %v", err)
	}

	if c.InstallID != "saved-install-id" {
		t.Errorf("InstallID 应加载: 期望 %q，得到 %q", "saved-install-id", c.InstallID)
	}
	if c.CurrentToken() != "saved-token" {
		t.Errorf("Token 应加载: 期望 %q，得到 %q", "saved-token", c.CurrentToken())
	}
	if c.CurrentTLSFingerprint() != "11:22:33:44:55:66" {
		t.Errorf("TLSFingerprint 应加载: 期望 %q，得到 %q", "11:22:33:44:55:66", c.CurrentTLSFingerprint())
	}
}

func TestEnsureInstallID_DoesNotOverwriteExistingFingerprint(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client.json")

	// 状态文件中有一个旧指纹
	state := persistedState{
		InstallID:      "install-old",
		TLSFingerprint: "OLD:FP",
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(statePath, data, 0o600)

	// Client 已有一个新指纹
	c := New("wss://localhost", "key")
	c.StatePath = statePath
	c.TLSFingerprint = "NEW:FP" // 已有值

	if err := c.ensureInstallID(); err != nil {
		t.Fatalf("ensureInstallID 失败: %v", err)
	}

	// 不应被文件中的旧值覆盖
	if c.CurrentTLSFingerprint() != "NEW:FP" {
		t.Errorf("已存在的 TLSFingerprint 不应被覆盖: 期望 %q，得到 %q", "NEW:FP", c.CurrentTLSFingerprint())
	}
}

func TestState_AllFieldsRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client.json")

	// 保存完整状态
	c1 := New("wss://localhost", "key")
	c1.InstallID = "rt-install"
	c1.Token = "rt-token"
	c1.StatePath = statePath

	if err := c1.saveTLSFingerprint("RT:FP:AA:BB"); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	// 新 Client 加载
	c2 := New("wss://localhost", "key")
	c2.StatePath = statePath

	if err := c2.ensureInstallID(); err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	if c2.InstallID != "rt-install" {
		t.Errorf("InstallID 丢失: %q", c2.InstallID)
	}
	if c2.Token != "rt-token" {
		t.Errorf("Token 丢失: %q", c2.Token)
	}
	if c2.TLSFingerprint != "RT:FP:AA:BB" {
		t.Errorf("TLSFingerprint 丢失: %q", c2.TLSFingerprint)
	}
}

// ============================================================
// Part 4: 端到端 TLS 场景测试
// ============================================================

// 场景：使用 wss:// 连接并完成认证
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
	c.StatePath = filepath.Join(t.TempDir(), "client.json")

	go c.Start()
	time.Sleep(3 * time.Second)

	if c.CurrentClientID() != "mock_client_1" {
		t.Errorf("TLS 认证后 ClientID 期望 'mock_client_1'，得到 %q", c.CurrentClientID())
	}
	if !c.UsesTLS() {
		t.Error("wss:// 应设置 useTLS = true")
	}

	// Server 应收到认证消息
	msgs := ms.getReceivedMsgs()
	authFound := false
	for _, msg := range msgs {
		if msg.Type == protocol.MsgTypeAuth {
			authFound = true
			break
		}
	}
	if !authFound {
		t.Error("Server 应收到 auth 消息")
	}
}

// 场景：ws:// 连接不使用 TLS
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
	c.StatePath = filepath.Join(t.TempDir(), "client.json")

	go c.Start()
	time.Sleep(2 * time.Second)

	if c.UsesTLS() {
		t.Error("ws:// 不应设置 useTLS")
	}
	if c.CurrentClientID() != "mock_client_1" {
		t.Errorf("明文认证后 ClientID 期望 'mock_client_1'，得到 %q", c.CurrentClientID())
	}
}

// 场景：TLSSkipVerify=true 时跳过 TOFU 指纹检查
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
	c.StatePath = filepath.Join(t.TempDir(), "client.json")

	go c.Start()
	time.Sleep(3 * time.Second)

	// TLSSkipVerify=true → checkTLSFingerprint 不会被调用 → 指纹不会被记录
	if c.CurrentTLSFingerprint() != "" {
		t.Errorf("TLSSkipVerify=true 时不应记录指纹，得到: %q", c.CurrentTLSFingerprint())
	}
	if c.CurrentClientID() != "mock_client_1" {
		t.Errorf("认证应成功，ClientID 期望 'mock_client_1'，得到 %q", c.CurrentClientID())
	}
}

// 场景：数据通道在 useTLS=true 时使用 TLS
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
		t.Fatalf("TLS 数据通道连接失败: %v", err)
	}

	c.dataMu.RLock()
	hasSession := c.dataSession != nil
	c.dataMu.RUnlock()

	if !hasSession {
		t.Error("TLS 数据通道握手成功后 dataSession 不应为 nil")
	}
}

// 场景：数据通道在 useTLS=false 时拒绝 TLS 连接
func TestScenario_PlainWS_DataChannelUsesPlainWS(t *testing.T) {
	// TLS-only 服务器，客户端用明文连接 → 应失败
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

	// 明文 WS 连接 TLS 服务器 → 握手失败
	err := c.connectDataChannel()
	if err == nil {
		t.Error("明文 WS 连接 TLS 服务器应失败")
	}
}

// 场景：指纹持久化后新 Client 实例能加载并通过校验
func TestScenario_TLS_FingerprintPersistedAndLoadedOnRestart(t *testing.T) {
	cert, x509Cert := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "client.json")
	expectedFP := computeTestFingerprint(x509Cert.Raw)

	// ---- 第一次连接：记录指纹 ----
	conn1 := dialTLSWS(t, wsURL)
	c1 := New("wss://localhost", "key")
	c1.InstallID = "persist-test"
	c1.StatePath = statePath

	if err := c1.checkTLSFingerprint(conn1); err != nil {
		t.Fatalf("首次连接不应报错: %v", err)
	}
	conn1.Close()

	if c1.TLSFingerprint != expectedFP {
		t.Fatalf("首次连接后应记录指纹")
	}

	// ---- 模拟重启：新 Client 实例加载状态 ----
	c2 := New("wss://localhost", "key")
	c2.StatePath = statePath
	if err := c2.ensureInstallID(); err != nil {
		t.Fatalf("加载状态失败: %v", err)
	}

	if c2.TLSFingerprint != expectedFP {
		t.Errorf("重启后应加载指纹:\n期望: %s\n实际: %s", expectedFP, c2.TLSFingerprint)
	}

	// ---- 重新连接同一服务器：指纹应匹配 ----
	conn2 := dialTLSWS(t, wsURL)
	defer conn2.Close()

	if err := c2.checkTLSFingerprint(conn2); err != nil {
		t.Errorf("重启后重连同一服务器应通过指纹校验: %v", err)
	}
}

// 场景：TOFU 重连同一证书 → 通过
func TestScenario_TLS_TOFU_ReconnectSameCert_Passes(t *testing.T) {
	cert, _ := generateTestCert(t)
	ts, wsURL := startTLSWSServer(t, cert)
	defer ts.Close()

	c := New("wss://localhost", "key")

	// 第一次连接 → 记录指纹
	conn1 := dialTLSWS(t, wsURL)
	if err := c.checkTLSFingerprint(conn1); err != nil {
		t.Fatalf("首次连接失败: %v", err)
	}
	conn1.Close()

	fp := c.CurrentTLSFingerprint()
	if fp == "" {
		t.Fatal("首次连接后应已记录指纹")
	}

	// 第二次连接同一服务器 → 应通过
	conn2 := dialTLSWS(t, wsURL)
	defer conn2.Close()

	if err := c.checkTLSFingerprint(conn2); err != nil {
		t.Errorf("重连同一证书应通过: %v", err)
	}

	if c.CurrentTLSFingerprint() != fp {
		t.Error("指纹不应改变")
	}
}

// 场景：服务器证书变更（模拟 MITM 或合法换证）→ 拒绝连接
func TestScenario_TLS_TOFU_DetectsCertChange(t *testing.T) {
	cert1, _ := generateTestCert(t)
	cert2, _ := generateTestCert(t) // 第二张不同的证书

	// 先连接 cert1 的服务器
	ts1, wsURL1 := startTLSWSServer(t, cert1)
	conn1 := dialTLSWS(t, wsURL1)

	c := New("wss://localhost", "key")
	if err := c.checkTLSFingerprint(conn1); err != nil {
		t.Fatalf("首次连接失败: %v", err)
	}
	conn1.Close()
	ts1.Close()

	savedFP := c.CurrentTLSFingerprint()
	if savedFP == "" {
		t.Fatal("应已记录指纹")
	}

	// 连接使用不同证书的服务器 → 应检测到变更
	ts2, wsURL2 := startTLSWSServer(t, cert2)
	defer ts2.Close()

	conn2 := dialTLSWS(t, wsURL2)
	defer conn2.Close()

	err := c.checkTLSFingerprint(conn2)
	if err == nil {
		t.Fatal("证书变更后应报错")
	}
	if !strings.Contains(err.Error(), "指纹不匹配") {
		t.Errorf("错误应包含'指纹不匹配': %v", err)
	}

	// 指纹不应被更新
	if c.TLSFingerprint != savedFP {
		t.Error("检测到变更后指纹不应被更新")
	}
}
