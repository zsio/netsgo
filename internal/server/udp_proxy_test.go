package server

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// ============================================================
// Server 端 UDP 代理测试
// ============================================================

func TestStartProxy_UDP_Success(t *testing.T) {
	s := New(0)
	clientID := "udp-proxy-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	// 构建 yamux session
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{
		Name:       "udp-test-tunnel",
		Type:       protocol.ProxyTypeUDP,
		LocalIP:    "127.0.0.1",
		LocalPort:  5353,
		RemotePort: 0, // 系统随机分配
	}

	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy UDP 失败: %v", err)
	}

	// 检查内部状态
	client.proxyMu.RLock()
	tunnel, exists := client.proxies[req.Name]
	client.proxyMu.RUnlock()

	if !exists {
		t.Fatal("StartProxy 成功但没有将隧道加入 map")
	}
	if tunnel.Config.RemotePort <= 0 {
		t.Errorf("分配的端口无效: %d", tunnel.Config.RemotePort)
	}
	if tunnel.Config.Type != protocol.ProxyTypeUDP {
		t.Errorf("类型期望 udp，得到 %s", tunnel.Config.Type)
	}
	if tunnel.UDPState == nil {
		t.Fatal("UDP 隧道的 UDPState 不应为 nil")
	}
	if tunnel.Listener != nil {
		t.Error("UDP 隧道不应有 TCP Listener")
	}

	// 验证 UDP 端口确实在监听：发一个 UDP 报文不应报错
	testConn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", tunnel.Config.RemotePort), 100*time.Millisecond)
	if err != nil {
		t.Errorf("无法连接 UDP 端口: %v", err)
	} else {
		testConn.Write([]byte("probe"))
		testConn.Close()
	}

	// 清理
	s.StopAllProxies(client)
	cConn.Close()
	sConn.Close()
}

func TestStopProxy_UDP(t *testing.T) {
	s := New(0)
	clientID := "udp-stop-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{
		Name:       "udp-to-stop",
		Type:       protocol.ProxyTypeUDP,
		RemotePort: 0,
	}
	s.StartProxy(client, req)

	client.proxyMu.RLock()
	tunnel := client.proxies[req.Name]
	port := tunnel.Config.RemotePort
	client.proxyMu.RUnlock()

	// 停止
	if err := s.StopProxy(client, req.Name); err != nil {
		t.Fatalf("StopProxy UDP 出错: %v", err)
	}

	// 等待端口释放
	time.Sleep(50 * time.Millisecond)

	// UDP 端口已关闭：重新 ListenPacket 应该能成功（说明旧的已释放）
	probe, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Errorf("UDP 端口 %d 未被释放: %v", port, err)
	} else {
		probe.Close()
	}

	cConn.Close()
	sConn.Close()
}

func TestPauseResumeProxy_UDP(t *testing.T) {
	s := New(0)
	clientID := "udp-pause-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{
		Name:       "udp-pause-test",
		Type:       protocol.ProxyTypeUDP,
		RemotePort: 0,
	}
	s.StartProxy(client, req)

	client.proxyMu.RLock()
	port := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	// 暂停
	if err := s.PauseProxy(client, req.Name); err != nil {
		t.Fatalf("PauseProxy UDP 出错: %v", err)
	}

	client.proxyMu.RLock()
	status := client.proxies[req.Name].Config.Status
	client.proxyMu.RUnlock()
	if status != protocol.ProxyStatusPaused {
		t.Errorf("暂停后状态期望 paused，得到 %s", status)
	}

	// 等待端口释放
	time.Sleep(50 * time.Millisecond)

	// 恢复
	if err := s.ResumeProxy(client, req.Name); err != nil {
		t.Fatalf("ResumeProxy UDP 出错: %v", err)
	}

	client.proxyMu.RLock()
	status = client.proxies[req.Name].Config.Status
	newPort := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	if status != protocol.ProxyStatusActive {
		t.Errorf("恢复后状态期望 active，得到 %s", status)
	}
	if newPort != port {
		t.Errorf("恢复后端口期望 %d，得到 %d", port, newPort)
	}

	s.StopAllProxies(client)
	cConn.Close()
	sConn.Close()
}

// ============================================================
// UDP 代理端到端转发测试（模拟完整 yamux 通道）
// ============================================================

func TestUDPProxy_E2E_ForwardAndReply(t *testing.T) {
	s := New(0)
	clientID := "udp-e2e-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	// 1. 启动本地 UDP echo 服务（模拟内网服务）
	echoConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 echo 服务失败: %v", err)
	}
	defer echoConn.Close()
	echoPort := echoConn.LocalAddr().(*net.UDPAddr).Port

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			echoConn.WriteTo(buf[:n], addr)
		}
	}()

	// 2. 构建 yamux session（模拟 Server ↔ Client 数据通道）
	pipeC, pipeS := net.Pipe()
	defer pipeC.Close()
	defer pipeS.Close()

	var serverSession *yamux.Session
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverSession, _ = mux.NewServerSession(pipeS, mux.DefaultConfig())
		wg.Done()
	}()
	clientSession, _ := mux.NewClientSession(pipeC, mux.DefaultConfig())
	wg.Wait()

	client.dataSession = serverSession
	defer serverSession.Close()
	defer clientSession.Close()

	// 3. 启动 Client 侧 stream 接收循环（模拟 Client 的 acceptStreamLoop）
	go func() {
		for {
			stream, err := clientSession.AcceptStream()
			if err != nil {
				return
			}
			go func(s *yamux.Stream) {
				defer s.Close()

				// 读取 StreamHeader
				var lenBuf [2]byte
				s.Read(lenBuf[:])
				nameLen := int(lenBuf[0])<<8 | int(lenBuf[1])
				nameBuf := make([]byte, nameLen)
				s.Read(nameBuf)

				// 连接本地 UDP 服务（echo）
				localConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", echoPort))
				if err != nil {
					return
				}
				defer localConn.Close()

				mux.UDPRelay(s, localConn)
			}(stream)
		}
	}()

	// 4. 启动 UDP 代理
	tunnelName := "udp-e2e-tunnel"
	req := protocol.ProxyNewRequest{
		Name:       tunnelName,
		Type:       protocol.ProxyTypeUDP,
		LocalIP:    "127.0.0.1",
		LocalPort:  echoPort,
		RemotePort: 0,
	}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("启动 UDP 代理失败: %v", err)
	}
	defer s.StopProxy(client, tunnelName)

	client.proxyMu.RLock()
	remotePort := client.proxies[tunnelName].Config.RemotePort
	client.proxyMu.RUnlock()

	// 5. 模拟外部 UDP 客户端：发消息并等待 echo 回复
	extConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("外部客户端连接失败: %v", err)
	}
	defer extConn.Close()

	testMsg := []byte("hello from external client")
	if _, err := extConn.Write(testMsg); err != nil {
		t.Fatalf("发送 UDP 报文失败: %v", err)
	}

	// 读取 echo 回复
	buf := make([]byte, 65535)
	extConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := extConn.Read(buf)
	if err != nil {
		t.Fatalf("读取回复超时: %v", err)
	}

	if string(buf[:n]) != string(testMsg) {
		t.Errorf("回复数据不匹配: 期望 %q，得到 %q", testMsg, buf[:n])
	}
}

func TestUDPProxy_MultipleSessions(t *testing.T) {
	s := New(0)
	clientID := "udp-multi-sess"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	// 启动 UDP echo 服务
	echoConn, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer echoConn.Close()
	echoPort := echoConn.LocalAddr().(*net.UDPAddr).Port

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			echoConn.WriteTo(buf[:n], addr)
		}
	}()

	// 构建 yamux
	pipeC, pipeS := net.Pipe()
	defer pipeC.Close()
	defer pipeS.Close()

	var serverSession *yamux.Session
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverSession, _ = mux.NewServerSession(pipeS, mux.DefaultConfig())
		wg.Done()
	}()
	clientSession, _ := mux.NewClientSession(pipeC, mux.DefaultConfig())
	wg.Wait()

	client.dataSession = serverSession
	defer serverSession.Close()
	defer clientSession.Close()

	// Client 侧 stream 接收
	go func() {
		for {
			stream, err := clientSession.AcceptStream()
			if err != nil {
				return
			}
			go func(s *yamux.Stream) {
				defer s.Close()
				var lenBuf [2]byte
				s.Read(lenBuf[:])
				nameLen := int(lenBuf[0])<<8 | int(lenBuf[1])
				nameBuf := make([]byte, nameLen)
				s.Read(nameBuf)
				localConn, _ := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", echoPort))
				defer localConn.Close()
				mux.UDPRelay(s, localConn)
			}(stream)
		}
	}()

	// 启动 UDP 代理
	req := protocol.ProxyNewRequest{
		Name:       "udp-multi-tunnel",
		Type:       protocol.ProxyTypeUDP,
		RemotePort: 0,
	}
	s.StartProxy(client, req)
	defer s.StopProxy(client, req.Name)

	client.proxyMu.RLock()
	remotePort := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	// 使用多个本地端口模拟多个外部客户端（不同 srcAddr）
	const numClients = 3
	var clientWg sync.WaitGroup
	errors := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		clientWg.Add(1)
		go func(idx int) {
			defer clientWg.Done()

			conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", remotePort))
			if err != nil {
				errors <- fmt.Errorf("client #%d dial: %v", idx, err)
				return
			}
			defer conn.Close()

			msg := fmt.Sprintf("client-%d-msg", idx)
			conn.Write([]byte(msg))

			buf := make([]byte, 1024)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				errors <- fmt.Errorf("client #%d read: %v", idx, err)
				return
			}
			if string(buf[:n]) != msg {
				errors <- fmt.Errorf("client #%d: 期望 %q，得到 %q", idx, msg, buf[:n])
			}
		}(i)
	}

	clientWg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}
