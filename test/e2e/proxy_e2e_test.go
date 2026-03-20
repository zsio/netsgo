//go:build e2e

package e2e_test

import (
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

	"netsgo/internal/client"
	"netsgo/internal/server"
	"netsgo/pkg/protocol"
)

func TestProxyE2E(t *testing.T) {
	const setupToken = "proxy-e2e-setup-token"

	proxyKind := getenvDefault("NETSGO_E2E_PROXY", "")
	composeFile := getenvDefault("NETSGO_E2E_COMPOSE_FILE", "")
	if proxyKind == "" || composeFile == "" {
		t.Skip("未设置 NETSGO_E2E_PROXY / NETSGO_E2E_COMPOSE_FILE，跳过代理 E2E")
	}

	proxyPort := getenvDefault("NETSGO_E2E_PROXY_PORT", "18080")
	upstreamPort := getenvDefault("NETSGO_E2E_UPSTREAM_PORT", "18081")
	publicPort := getenvDefault("NETSGO_E2E_PUBLIC_PROXY_PORT", "18082")
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
	proxyListenPort := mustAtoi(t, publicPort)

	tmpDir := t.TempDir()
	srv := server.New(serverPort)
	srv.StorePath = filepath.Join(tmpDir, "tunnels.json")
	srv.SetupToken = setupToken

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
	waitForSetupReady(t, baseURL, 20*time.Second)
	setupServer(t, baseURL, setupToken)
	adminToken := waitForAdminToken(t, baseURL, 10*time.Second)
	apiKey := createAPIKey(t, baseURL, adminToken)

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-E2E-Test", "success")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxy e2e backend response"))
	})
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建后端监听器失败: %v", err)
	}
	backendServer := &http.Server{Handler: backend}
	go func() { _ = backendServer.Serve(backendListener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = backendServer.Shutdown(ctx)
	}()

	localPort := backendListener.Addr().(*net.TCPAddr).Port
	clientStatePath := filepath.Join(tmpDir, "client.json")
	c := client.New("ws://127.0.0.1:"+proxyPort, apiKey)
	c.StatePath = clientStatePath
	c.ProxyConfigs = []protocol.ProxyNewRequest{{
		Name:       "proxy-e2e-tunnel",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  localPort,
		RemotePort: proxyListenPort,
	}}

	clientErr := make(chan error, 1)
	go func() {
		clientErr <- c.Start()
	}()

	tunnelURL := fmt.Sprintf("http://127.0.0.1:%d", proxyListenPort)
	waitForTunnel(t, tunnelURL, []byte("proxy e2e backend response"), 40*time.Second)
	waitForLiveClientCount(t, baseURL, adminToken, 1, 20*time.Second)

	time.Sleep(12 * time.Second)
	waitForTunnel(t, tunnelURL, []byte("proxy e2e backend response"), 20*time.Second)

	runCompose(t, composeEnv, projectName, composeFile, "restart", "proxy")
	waitForTunnel(t, tunnelURL, []byte("proxy e2e backend response"), 45*time.Second)
	waitForLiveClientCount(t, baseURL, adminToken, 1, 20*time.Second)

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

	waitForSetupReady(t, srv.URL, 2*time.Second)

	if attempts < 3 {
		t.Fatalf("expected retries before ready, got %d attempts", attempts)
	}
}

func waitForTunnel(t *testing.T, url string, expected []byte, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK && bytes.Contains(body, expected) {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("在 %v 内未观察到可用隧道: %s", timeout, url)
}

func waitForSetupReady(t *testing.T, baseURL string, timeout time.Duration) {
	t.Helper()

	client := http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/api/setup/status")
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

func waitForLiveClientCount(t *testing.T, baseURL, adminToken string, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		count, err := fetchClientCount(baseURL, adminToken)
		if err == nil && count == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("在 %v 内未观察到 live client 数量 = %d", timeout, want)
}

func loginAdmin(baseURL string) (string, error) {
	body := bytes.NewBufferString(`{"username":"admin","password":"password123"}`)
	resp, err := http.Post(baseURL+"/api/auth/login", "application/json", body)
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

func fetchClientCount(baseURL, token string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/clients", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
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

func setupServer(t *testing.T, baseURL, setupToken string) {
	t.Helper()

	body := bytes.NewBufferString(fmt.Sprintf(`{"admin":{"username":"admin","password":"password123"},"server_addr":"localhost","allowed_ports":[],"setup_token":"%s"}`, setupToken))
	resp, err := http.Post(baseURL+"/api/setup/init", "application/json", body)
	if err != nil {
		t.Fatalf("初始化服务失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("初始化服务状态异常: %d body=%s", resp.StatusCode, string(payload))
	}
}

func waitForAdminToken(t *testing.T, baseURL string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		token, err := loginAdmin(baseURL)
		if err == nil && token != "" {
			return token
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("在 %v 内未拿到 admin token", timeout)
	return ""
}

func createAPIKey(t *testing.T, baseURL, adminToken string) string {
	t.Helper()

	reqBody := bytes.NewBufferString(`{"name":"e2e","permissions":["connect"]}`)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/admin/keys", reqBody)
	if err != nil {
		t.Fatalf("创建 API Key 请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
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
