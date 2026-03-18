//go:build e2e

package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestComposeStackE2E(t *testing.T) {
	projectName, composeFiles, composeEnv, baseURL, tunnelURL := mustComposeStackConfig(t)
	registerComposeCleanup(t, composeEnv, projectName, composeFiles)

	runComposeFiles(t, composeEnv, projectName, composeFiles, "up", "-d", "--build", "--remove-orphans")
	waitForHTTP200(t, baseURL+"/api/setup/status", 90*time.Second)
	waitForTunnel(t, tunnelURL, []byte("compose backend response"), 2*time.Minute)

	runComposeFiles(t, composeEnv, projectName, composeFiles, "restart", "proxy")
	waitForTunnel(t, tunnelURL, []byte("compose backend response"), 2*time.Minute)
}

func TestComposeStackSoak(t *testing.T) {
	projectName, composeFiles, composeEnv, baseURL, tunnelURL := mustComposeStackConfig(t)
	registerComposeCleanup(t, composeEnv, projectName, composeFiles)

	runComposeFiles(t, composeEnv, projectName, composeFiles, "up", "-d", "--build", "--remove-orphans")
	waitForHTTP200(t, baseURL+"/api/setup/status", 90*time.Second)
	waitForTunnel(t, tunnelURL, []byte("compose backend response"), 2*time.Minute)

	adminToken := waitForAdminToken(t, baseURL, 45*time.Second)
	waitForLiveClientCount(t, baseURL, adminToken, 1, 45*time.Second)

	idleDuration := mustParseDuration(t, getenvDefault("NETSGO_E2E_SOAK_IDLE", "45s"))
	cycles := mustPositiveInt(t, getenvDefault("NETSGO_E2E_SOAK_CYCLES", "3"))

	for i := 0; i < cycles; i++ {
		time.Sleep(idleDuration)
		waitForTunnel(t, tunnelURL, []byte("compose backend response"), 45*time.Second)
		waitForLiveClientCount(t, baseURL, adminToken, 1, 30*time.Second)

		switch i {
		case 0:
			runComposeFiles(t, composeEnv, projectName, composeFiles, "restart", "proxy")
		case 1:
			runComposeFiles(t, composeEnv, projectName, composeFiles, "restart", "client")
		default:
			continue
		}

		waitForTunnel(t, tunnelURL, []byte("compose backend response"), 2*time.Minute)
		waitForLiveClientCount(t, baseURL, adminToken, 1, 45*time.Second)
	}
}

func mustComposeStackConfig(t *testing.T) (projectName string, composeFiles []string, composeEnv []string, baseURL string, tunnelURL string) {
	t.Helper()

	composeFilesRaw := getenvDefault("NETSGO_E2E_STACK_COMPOSE_FILES", "")
	if composeFilesRaw == "" {
		t.Skip("未设置 NETSGO_E2E_STACK_COMPOSE_FILES，跳过完整 Compose E2E")
	}

	composeFiles = splitComposeFiles(composeFilesRaw)
	if len(composeFiles) == 0 {
		t.Skip("NETSGO_E2E_STACK_COMPOSE_FILES 为空，跳过完整 Compose E2E")
	}

	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("未找到 docker CLI: %v", err)
	}

	projectName = getenvDefault("NETSGO_E2E_COMPOSE_PROJECT", "netsgo-stack")
	proxyPort := getenvDefault("PROXY_PORT", "19080")
	upstreamPort := getenvDefault("UPSTREAM_PORT", "19081")
	tunnelPort := getenvDefault("TUNNEL_REMOTE_PORT", "19082")

	composeEnv = append(os.Environ(),
		"PROXY_PORT="+proxyPort,
		"UPSTREAM_PORT="+upstreamPort,
		"TUNNEL_REMOTE_PORT="+tunnelPort,
	)

	baseURL = fmt.Sprintf("http://127.0.0.1:%s", proxyPort)
	tunnelURL = fmt.Sprintf("http://127.0.0.1:%s", tunnelPort)
	return
}

func registerComposeCleanup(t *testing.T, composeEnv []string, projectName string, composeFiles []string) {
	t.Helper()

	keepStack := strings.EqualFold(getenvDefault("NETSGO_E2E_KEEP_STACK", ""), "true") || getenvDefault("NETSGO_E2E_KEEP_STACK", "") == "1"
	t.Cleanup(func() {
		if t.Failed() {
			dumpComposeOutput(t, composeEnv, projectName, composeFiles, "ps")
			dumpComposeOutput(t, composeEnv, projectName, composeFiles, "logs", "--no-color", "--tail", "200")
		}
		if keepStack {
			t.Logf("保留 Compose 环境，项目名: %s", projectName)
			return
		}
		runComposeFiles(t, composeEnv, projectName, composeFiles, "down", "-v", "--remove-orphans")
	})
}

func splitComposeFiles(raw string) []string {
	parts := strings.Split(raw, ",")
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			files = append(files, part)
		}
	}
	return files
}

func mustParseDuration(t *testing.T, raw string) time.Duration {
	t.Helper()
	d, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("解析 duration 失败 %q: %v", raw, err)
	}
	if d <= 0 {
		t.Fatalf("duration 必须 > 0，得到 %q", raw)
	}
	return d
}

func mustPositiveInt(t *testing.T, raw string) int {
	t.Helper()
	var out int
	if _, err := fmt.Sscanf(raw, "%d", &out); err != nil {
		t.Fatalf("解析整数失败 %q: %v", raw, err)
	}
	if out <= 0 {
		t.Fatalf("整数必须 > 0，得到 %q", raw)
	}
	return out
}

func waitForHTTP200(t *testing.T, url string, timeout time.Duration) {
	t.Helper()

	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("在 %v 内未观察到 HTTP 200: %s", timeout, url)
}

func dumpComposeOutput(t *testing.T, env []string, projectName string, composeFiles []string, args ...string) {
	t.Helper()

	cmdArgs := composeCommandArgs(projectName, composeFiles, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("docker %v 失败: %v", cmdArgs, err)
	}
	if len(output) > 0 {
		t.Logf("docker %v 输出:\n%s", cmdArgs, string(output))
	}
}
