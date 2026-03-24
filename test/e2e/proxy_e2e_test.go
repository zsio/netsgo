//go:build e2e

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/internal/client"
	"netsgo/internal/server"
)

func TestProxyE2E(t *testing.T) {
	const setupToken = "proxy-e2e-setup-token"
	const managementHost = "panel.proxy.test"
	const tunnelHost = "app.proxy.test"

	proxyKind := getenvDefault("NETSGO_E2E_PROXY", "")
	composeFile := getenvDefault("NETSGO_E2E_COMPOSE_FILE", "")
	if proxyKind == "" || composeFile == "" {
		t.Skip("未设置 NETSGO_E2E_PROXY / NETSGO_E2E_COMPOSE_FILE，跳过代理 E2E")
	}

	proxyPort := getenvDefault("NETSGO_E2E_PROXY_PORT", "18080")
	upstreamPort := getenvDefault("NETSGO_E2E_UPSTREAM_PORT", "18081")
	projectName := fmt.Sprintf("netsgo-e2e-%s", proxyKind)

	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("未找到 docker CLI: %v", err)
	}

	composeEnv := append(os.Environ(),
		"PROXY_PORT="+proxyPort,
		"UPSTREAM_PORT="+upstreamPort,
	)
	runCompose(t, composeEnv, projectName, composeFile, "up", "-d", "--remove-orphans")
	defer runCompose(t, composeEnv, projectName, composeFile, "down", "-v", "--remove-orphans")

	serverPort := mustAtoi(t, upstreamPort)

	tmpDir := t.TempDir()
	srv := server.New(serverPort)
	srv.StorePath = filepath.Join(tmpDir, "tunnels.json")
	srv.SetupToken = setupToken
	srv.TLS = &server.TLSConfig{
		Mode:           server.TLSModeOff,
		TrustedProxies: []string{"127.0.0.1/32", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Start()
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	baseURL := "http://127.0.0.1:" + proxyPort
	waitForSetupReady(t, baseURL, managementHost, 20*time.Second)
	setupServer(t, baseURL, managementHost, "http://"+managementHost, setupToken)
	adminToken := waitForAdminToken(t, baseURL, managementHost, 10*time.Second)
	apiKey := createAPIKey(t, baseURL, managementHost, adminToken)

	backend := newHTTPDomainBackend(t)
	defer backend.Close()

	localPort := backend.Listener.Addr().(*net.TCPAddr).Port
	clientStatePath := filepath.Join(tmpDir, "client.json")
	c := client.New("ws://127.0.0.1:"+proxyPort, apiKey)
	c.StatePath = clientStatePath

	clientErr := make(chan error, 1)
	go func() {
		clientErr <- c.Start()
	}()

	waitForLiveClientCount(t, baseURL, managementHost, adminToken, 1, 20*time.Second)
	waitForClientID(t, &c.ClientID, 10*time.Second)
	createHTTPDomainTunnel(t, baseURL, managementHost, adminToken, c.ClientID, localPort, tunnelHost)
	waitForTunnel(t, baseURL+"/", tunnelHost, []byte("proxy e2e backend response"), 40*time.Second)
	assertForwardedHeaders(t, baseURL+"/headers", tunnelHost)
	assertBusinessWebSocket(t, baseURL, tunnelHost, "/ws")
	assertSSE(t, baseURL+"/sse", tunnelHost)

	time.Sleep(12 * time.Second)
	waitForTunnel(t, baseURL+"/", tunnelHost, []byte("proxy e2e backend response"), 20*time.Second)

	runCompose(t, composeEnv, projectName, composeFile, "restart", "proxy")
	waitForTunnel(t, baseURL+"/", tunnelHost, []byte("proxy e2e backend response"), 45*time.Second)
	waitForLiveClientCount(t, baseURL, managementHost, adminToken, 1, 20*time.Second)
	assertForwardedHeaders(t, baseURL+"/headers", tunnelHost)

	select {
	case err := <-serverErr:
		if err != nil && err.Error() != "http: Server closed" {
			t.Fatalf("服务端提前退出: %v", err)
		}
	default:
	}
	select {
	case err := <-clientErr:
		if err != nil {
			t.Fatalf("客户端提前退出: %v", err)
		}
	default:
	}
}

func TestWaitForSetupReady(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/setup/status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	waitForSetupReady(t, srv.URL, "panel.test", 2*time.Second)

	if attempts < 3 {
		t.Fatalf("expected retries before ready, got %d attempts", attempts)
	}
}

func newHTTPDomainBackend(t *testing.T) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("X-E2E-Test", "success")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("proxy e2e backend response"))
		case "/headers":
			payload := map[string]string{
				"Host":              r.Host,
				"X-Forwarded-Host":  r.Header.Get("X-Forwarded-Host"),
				"X-Forwarded-Proto": r.Header.Get("X-Forwarded-Proto"),
				"X-Forwarded-For":   r.Header.Get("X-Forwarded-For"),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "flusher required", http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, "data: hello\n\n")
			flusher.Flush()
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			mt, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.WriteMessage(mt, payload)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newRequest(method, url, host string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if host != "" {
		req.Host = host
	}
	return req, nil
}

func doRequest(method, url, host, token string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := newRequest(method, url, host, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	return client.Do(req)
}

func waitForTunnel(t *testing.T, url, host string, expected []byte, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := doRequest(http.MethodGet, url, host, "", nil, "")
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK && bytes.Contains(body, expected) {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("在 %v 内未观察到可用隧道: %s host=%s", timeout, url, host)
}

func waitForSetupReady(t *testing.T, baseURL, managementHost string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := doRequest(http.MethodGet, baseURL+"/api/setup/status", managementHost, "", nil, "")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("在 %v 内未观察到 setup ready: %s/api/setup/status", timeout, baseURL)
}

func waitForLiveClientCount(t *testing.T, baseURL, managementHost, adminToken string, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		count, err := fetchClientCount(baseURL, managementHost, adminToken)
		if err == nil && count == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("在 %v 内未观察到 live client 数量 = %d", timeout, want)
}

func waitForClientID(t *testing.T, clientID *string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(*clientID) != "" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("在限定时间内未拿到 client ID")
}

func loginAdmin(baseURL, managementHost string) (string, error) {
	body := bytes.NewBufferString(`{"username":"admin","password":"password123"}`)
	resp, err := doRequest(http.MethodPost, baseURL+"/api/auth/login", managementHost, "", body, "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login status=%d", resp.StatusCode)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token == "" {
		return "", fmt.Errorf("empty admin token")
	}
	return payload.Token, nil
}

func fetchClientCount(baseURL, managementHost, token string) (int, error) {
	resp, err := doRequest(http.MethodGet, baseURL+"/api/clients", managementHost, token, nil, "")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("clients status=%d", resp.StatusCode)
	}
	var payload []any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func setupServer(t *testing.T, baseURL, managementHost, serverAddr, setupToken string) {
	t.Helper()

	body := bytes.NewBufferString(fmt.Sprintf(`{"admin":{"username":"admin","password":"password123"},"server_addr":"%s","allowed_ports":[],"setup_token":"%s"}`, serverAddr, setupToken))
	resp, err := doRequest(http.MethodPost, baseURL+"/api/setup/init", managementHost, "", body, "application/json")
	if err != nil {
		t.Fatalf("初始化服务失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("初始化服务状态异常: %d body=%s", resp.StatusCode, string(payload))
	}
}

func waitForAdminToken(t *testing.T, baseURL, managementHost string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		token, err := loginAdmin(baseURL, managementHost)
		if err == nil && token != "" {
			return token
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("在 %v 内未拿到 admin token", timeout)
	return ""
}

func createAPIKey(t *testing.T, baseURL, managementHost, adminToken string) string {
	t.Helper()

	reqBody := bytes.NewBufferString(`{"name":"e2e","permissions":["connect"]}`)
	resp, err := doRequest(http.MethodPost, baseURL+"/api/admin/keys", managementHost, adminToken, reqBody, "application/json")
	if err != nil {
		t.Fatalf("创建 API Key 请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("创建 API Key 状态异常: %d body=%s", resp.StatusCode, string(payload))
	}

	var payload struct {
		RawKey string `json:"raw_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析 API Key 响应失败: %v", err)
	}
	if payload.RawKey == "" {
		t.Fatal("创建 API Key 响应未返回 raw_key")
	}
	return payload.RawKey
}

func createHTTPDomainTunnel(t *testing.T, baseURL, managementHost, adminToken, clientID string, localPort int, domain string) {
	t.Helper()

	body := bytes.NewBufferString(fmt.Sprintf(`{"name":"proxy-e2e-http","type":"http","local_ip":"127.0.0.1","local_port":%d,"domain":"%s"}`, localPort, domain))
	resp, err := doRequest(http.MethodPost, baseURL+"/api/clients/"+clientID+"/tunnels", managementHost, adminToken, body, "application/json")
	if err != nil {
		t.Fatalf("创建 HTTP 域名隧道失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("创建 HTTP 域名隧道状态异常: %d body=%s", resp.StatusCode, string(payload))
	}
}

func assertForwardedHeaders(t *testing.T, url, host string) {
	t.Helper()

	resp, err := doRequest(http.MethodGet, url, host, "", nil, "")
	if err != nil {
		t.Fatalf("请求 headers upstream 失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("headers upstream 状态异常: %d body=%s", resp.StatusCode, string(payload))
	}

	var payload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析 headers 响应失败: %v", err)
	}
	if payload["Host"] != host {
		t.Fatalf("Host 透传错误，期望 %q，得到 %q", host, payload["Host"])
	}
	if payload["X-Forwarded-Host"] != host {
		t.Fatalf("X-Forwarded-Host 错误，期望 %q，得到 %q", host, payload["X-Forwarded-Host"])
	}
	if payload["X-Forwarded-Proto"] != "http" {
		t.Fatalf("X-Forwarded-Proto 期望 http，得到 %q", payload["X-Forwarded-Proto"])
	}
	if strings.TrimSpace(payload["X-Forwarded-For"]) == "" {
		t.Fatal("X-Forwarded-For 不应为空")
	}
}

func assertBusinessWebSocket(t *testing.T, baseURL, host, path string) {
	t.Helper()

	targetHost := strings.TrimPrefix(baseURL, "http://")
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var nd net.Dialer
		return nd.DialContext(ctx, network, targetHost)
	}

	conn, _, err := dialer.Dial("ws://"+host+path, nil)
	if err != nil {
		t.Fatalf("业务 WebSocket 建连失败: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("发送业务 WebSocket 消息失败: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取业务 WebSocket 响应失败: %v", err)
	}
	if string(payload) != "ping" {
		t.Fatalf("业务 WebSocket echo 期望 ping，得到 %q", payload)
	}
}

func assertSSE(t *testing.T, url, host string) {
	t.Helper()

	resp, err := doRequest(http.MethodGet, url, host, "", nil, "")
	if err != nil {
		t.Fatalf("请求 SSE upstream 失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("SSE upstream 状态异常: %d body=%s", resp.StatusCode, string(payload))
	}

	reader := bufio.NewReader(resp.Body)
	done := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		done <- line
	}()

	select {
	case line := <-done:
		if strings.TrimSpace(line) != "data: hello" {
			t.Fatalf("SSE 首行期望 data: hello，得到 %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SSE 首条事件未能及时到达")
	}
}

func runCompose(t *testing.T, env []string, projectName, composeFile string, args ...string) {
	t.Helper()

	runComposeFiles(t, env, projectName, []string{composeFile}, args...)
}

func composeCommandArgs(projectName string, composeFiles []string, args ...string) []string {
	cmdArgs := []string{"compose"}
	for _, composeFile := range composeFiles {
		if strings.TrimSpace(composeFile) == "" {
			continue
		}
		cmdArgs = append(cmdArgs, "-f", composeFile)
	}
	cmdArgs = append(cmdArgs, "-p", projectName)
	cmdArgs = append(cmdArgs, args...)
	return cmdArgs
}

func runComposeFiles(t *testing.T, env []string, projectName string, composeFiles []string, args ...string) {
	t.Helper()

	cmdArgs := composeCommandArgs(projectName, composeFiles, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v 失败: %v\n%s", cmdArgs, err, string(output))
	}
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	var out int
	if _, err := fmt.Sscanf(value, "%d", &out); err != nil {
		t.Fatalf("解析整数失败 %q: %v", value, err)
	}
	return out
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
