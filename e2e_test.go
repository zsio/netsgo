package netsgo_test

import (
	"bytes"
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

// ============================================================
// 端到端 (E2E) 集成测试: Server + Client + Backend
// ============================================================

func TestE2E_TCPProxyTunnel(t *testing.T) {
	// 为了确保公网端口可知，预留一个空闲端口
	publicProxyPort := reserveTCPPort(t)

	// 1. 启动 Server（预分配端口以避免竞态）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预分配端口失败: %v", err)
	}
	serverPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // 释放端口给 Server

	tmpDir := t.TempDir()
	adminStore, err := server.NewAdminStore(filepath.Join(tmpDir, "server", "admin.json"))
	if err != nil {
		t.Fatalf("创建 AdminStore 失败: %v", err)
	}
	if err := adminStore.Initialize("admin", "password123", "localhost", nil); err != nil {
		t.Fatalf("初始化 AdminStore 失败: %v", err)
	}
	if _, err := adminStore.AddAPIKey("e2e", "e2e-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("创建 E2E API Key 失败: %v", err)
	}

	srv := server.New(serverPort)
	srv.DataDir = tmpDir

	go func() {
		err := srv.Start()
		if err != nil {
			panic(fmt.Sprintf("Server 启动失败: %v", err))
		}
	}()
	time.Sleep(200 * time.Millisecond) // 等待监听端口分配完毕

	serverWsAddr := fmt.Sprintf("ws://127.0.0.1:%d", serverPort)

	// 2. 启动本地被代理的 Backend HTTP 服务
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-E2E-Test", "success")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("e2e backend response"))
	}))
	defer backend.Close()
	var localPort int
	fmt.Sscanf(backend.Listener.Addr().String(), "127.0.0.1:%d", &localPort)

	// 3. 启动 Client，并自动请求创建一个代理
	c := client.New(serverWsAddr, "e2e-key")
	c.DataDir = tmpDir
	c.DisableReconnect = true
	proxyName := "e2e-tunnel"

	c.ProxyConfigs = []protocol.ProxyNewRequest{
		{
			Name:       proxyName,
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  localPort,
			RemotePort: publicProxyPort,
		},
	}

	go func() {
		err := c.Start()
		if err != nil {
			panic(fmt.Sprintf("Client 启动失败: %v", err))
		}
	}()

	// 4. 等待完整的握手、数据通道建立、代理创建
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
	defer resp.Body.Close()

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
				if name == proxyName &&
					tunnel.Config.RemotePort == publicProxyPort &&
					tunnel.Config.RuntimeState == protocol.ProxyRuntimeStateExposed {
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
	defer ln.Close()

	return ln.Addr().(*net.TCPAddr).Port
}
