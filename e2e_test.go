package netsgo_test

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	// 为了确保公网端口可知，我们指定一个未使用的端口
	publicProxyPort := 64321

	// 1. 启动 Server（预分配端口以避免竞态）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预分配端口失败: %v", err)
	}
	serverPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // 释放端口给 Server

	srv := server.New(serverPort)

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
	c := client.New(serverWsAddr, "e2e-token")
	proxyName := "e2e-tunnel"
	
	c.ProxyConfigs = []protocol.ProxyNewRequest{
		{
			Name:       proxyName,
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  localPort,
			RemotePort: publicProxyPort, // 固定分配以便测试请求
		},
	}

	go func() {
		err := c.Start()
		if err != nil {
			panic(fmt.Sprintf("Client 启动失败: %v", err))
		}
	}()

	// 4. 等待完整的握手、数据通道建立、代理创建
	time.Sleep(2 * time.Second)

	// 5. 进行外网访问模拟: 对准 publicProxyPort 发起 HTTP 请求
	httpClient := http.Client{Timeout: 3 * time.Second}
	resp, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d", publicProxyPort))
	if err != nil {
		t.Fatalf("请求外网代理端点失败: %v", err)
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
