package netsgo_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"netsgo/internal/client"
	"netsgo/internal/server"
	"netsgo/pkg/protocol"
)

func TestE2E_TCPProxyTunnel(t *testing.T) {
	publicProxyPort := reserveTCPPort(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预分配端口失败: %v", err)
	}
	serverPort := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("释放预分配端口失败: %v", err)
	}

	tmpDir := t.TempDir()
	adminStore, err := server.NewAdminStore(filepath.Join(tmpDir, "server", server.ServerDBFileName))
	if err != nil {
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}
	if err := adminStore.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("初始化 AdminStore 失败: %v", err)
	}
	if _, err := adminStore.AddAPIKey("e2e", "e2e-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("创建 E2E API Key 失败: %v", err)
	}
	if err := adminStore.Close(); err != nil {
		t.Fatalf("关闭 AdminStore 失败: %v", err)
	}

	srv := server.New(serverPort)
	srv.DataDir = tmpDir
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(fmt.Sprintf("Server 启动失败: %v", err))
		}
	}()
	time.Sleep(200 * time.Millisecond)

	serverWsAddr := fmt.Sprintf("ws://127.0.0.1:%d", serverPort)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-E2E-Test", "success")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("e2e backend response")); err != nil {
			t.Fatalf("写入 backend 响应失败: %v", err)
		}
	}))
	defer backend.Close()
	var localPort int
	if _, err := fmt.Sscanf(backend.Listener.Addr().String(), "127.0.0.1:%d", &localPort); err != nil {
		t.Fatalf("解析 backend 端口失败: %v", err)
	}

	c := client.New(serverWsAddr, "e2e-key")
	c.DataDir = tmpDir
	c.DisableReconnect = true
	proxyName := "e2e-tunnel"
	c.ProxyConfigs = []protocol.ProxyNewRequest{{
		Name:       proxyName,
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  localPort,
		RemotePort: publicProxyPort,
	}}

	go func() {
		if err := c.Start(); err != nil {
			panic(fmt.Sprintf("Client 启动失败: %v", err))
		}
	}()

	httpClient := http.Client{Timeout: 3 * time.Second}
	waitForTunnelExposed(t, srv, proxyName, publicProxyPort, 8*time.Second)
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", publicProxyPort)

	var resp *http.Response
	deadline := time.Now().Add(8 * time.Second)
	for {
		resp, err = httpClient.Get(proxyURL)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("请求外网代理端点失败: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("期望 200 OK，得到 %d", resp.StatusCode)
	}
	if resp.Header.Get("X-E2E-Test") != "success" {
		t.Errorf("未触达 Backend (未找到期望的 HTTP Header响应)")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 Body 失败: %v", err)
	}
	if !bytes.Contains(body, []byte("e2e backend response")) {
		t.Errorf("返回内容错误: %s", string(body))
	}
}

func waitForTunnelExposed(t *testing.T, srv *server.Server, proxyName string, publicProxyPort int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		exposed := false
		srv.RangeClients(func(_ string, client *server.ClientConn) bool {
			client.RangeProxies(func(name string, tunnel *server.ProxyTunnel) bool {
				if name == proxyName && tunnel.Config.RemotePort == publicProxyPort && tunnel.Config.RuntimeState == protocol.ProxyRuntimeStateExposed {
					exposed = true
					return false
				}
				return true
			})
			return !exposed
		})
		if exposed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待隧道 %s 在端口 %d 上进入 exposed 状态超时", proxyName, publicProxyPort)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预留端口失败: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}
