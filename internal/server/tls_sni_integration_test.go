package server

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Optional integration dependencies are supplied by the caller; ordinary Go
// tests still exercise the same payload assertions with the in-process client.
func startSNIProxy(t *testing.T, kind string, upstreamPort int) string {
	t.Helper()
	dir := t.TempDir()
	if kind == "nginx" {
		config := fmt.Sprintf("events {}\nstream { server { listen 8443; ssl_preread on; proxy_pass host.docker.internal:%d; } }\n", upstreamPort)
		path := filepath.Join(dir, "nginx.conf")
		if err := os.WriteFile(path, []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		docker := func(args ...string) string {
			t.Helper()
			output, err := exec.Command("docker", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("docker %v: %v\n%s", args, err, output)
			}
			return strings.TrimSpace(string(output))
		}
		id := docker("create", "--add-host=host.docker.internal:host-gateway", "-p", "127.0.0.1::8443", "--entrypoint", "nginx", "nginx:1.27-alpine", "-c", "/sni.conf", "-g", "daemon off;")
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
		docker("cp", path, id+":/sni.conf")
		docker("start", id)
		address := docker("port", id, "8443/tcp")
		waitSNIProxyReady(t, address)
		return address
	}
	address := fmt.Sprintf("127.0.0.1:%d", reserveTCPPort(t))
	config := fmt.Sprintf(`{"admin":{"disabled":true},"apps":{"layer4":{"servers":{"sni":{"listen":[%q],"routes":[{"handle":[{"handler":"proxy","upstreams":[{"dial":["127.0.0.1:%d"]}]}]}]}}}}}`, address, upstreamPort)
	path := filepath.Join(dir, "caddy.json")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	startSNIProcess(t, os.Getenv("NETSGO_TEST_SNI_CADDY_BINARY"), "run", "--config", path)
	waitSNIProxyReady(t, address)
	return address
}

func waitSNIProxyReady(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("SNI proxy not listening at %s", address)
}

func startSNIProcess(t *testing.T, binary string, args ...string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "process.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			t.Logf("%s log:\n%s", binary, data)
		}
	})
}

func startOldSNIClient(t *testing.T, s *Server, binary, serverURL string) string {
	t.Helper()
	startSNIProcess(t, binary, "client", "--server", serverURL, "--key", "test-key", "--data-dir", t.TempDir(), "--tls-skip-verify")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var clientID string
		s.RangeClients(func(id string, client *ClientConn) bool {
			if client.isLive() && clientHasDataSession(client) {
				clientID = id
				return false
			}
			return true
		})
		if clientID != "" {
			return clientID
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("old client did not establish its control and data sessions")
	return ""
}
