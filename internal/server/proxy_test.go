package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// ============================================================
// Proxy 管理与监听测试
// ============================================================

func TestStartProxy_Success(t *testing.T) {
	s := New(0)
	clientID := "proxy-client"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	// 欺骗其拥有活跃的 DataSession (使用 net.Pipe 作为占位)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	// 尝试启动一个对公网代理（分配随机端口）
	req := protocol.ProxyNewRequest{
		Name:       "random-port-tunnel",
		Type:       protocol.ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  8080,
		RemotePort: reserveTCPPort(t),
	}

	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy 失败: %v", err)
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

	// 确认监听器确实打开着
	testConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", tunnel.Config.RemotePort))
	if err != nil {
		t.Errorf("由于无法 Dial 绑定的公网端口，说明监听器异常: %v", err)
	} else {
		testConn.Close()
	}

	// 清理
	s.StopAllProxies(client)
	cConn.Close()
	sConn.Close()
}

func TestStartProxy_NoDataChannel(t *testing.T) {
	s := New(0)
	clientID := "proxy-no-data"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)

	req := protocol.ProxyNewRequest{
		Name: "fail-tunnel",
	}

	if err := s.StartProxy(client, req); err == nil {
		t.Error("缺少 Data 通道时应启动失败")
	}
}

func TestPrepareProxyTunnel_PreservesHTTPDomain(t *testing.T) {
	s := New(0)
	client := &ClientConn{
		ID:      "proxy-http-domain",
		proxies: make(map[string]*ProxyTunnel),
	}

	clientConn, serverConn := net.Pipe()
	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	client.dataSession = serverSession
	t.Cleanup(func() {
		s.StopAllProxies(client)
		_ = serverSession.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	req := protocol.ProxyNewRequest{
		Name:      "http-domain-preserve",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "app.example.com",
	}

	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		t.Fatalf("prepareProxyTunnel 失败: %v", err)
	}
	if tunnel.Config.Domain != req.Domain {
		t.Fatalf("Domain 应保留为 %q，得到 %q", req.Domain, tunnel.Config.Domain)
	}
}

func TestActivatePreparedTunnel_HTTPDoesNotBindListener(t *testing.T) {
	s := New(0)
	client := &ClientConn{
		ID:      "proxy-http-activate",
		proxies: make(map[string]*ProxyTunnel),
	}

	clientConn, serverConn := net.Pipe()
	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	client.dataSession = serverSession
	t.Cleanup(func() {
		s.StopAllProxies(client)
		_ = serverSession.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	req := protocol.ProxyNewRequest{
		Name:       "http-no-listen",
		Type:       protocol.ProxyTypeHTTP,
		LocalIP:    "127.0.0.1",
		LocalPort:  3000,
		RemotePort: 18080,
		Domain:     "svc.example.com",
	}

	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		t.Fatalf("prepareProxyTunnel 失败: %v", err)
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		t.Fatalf("activatePreparedTunnel 失败: %v", err)
	}
	if tunnel.Listener != nil {
		t.Fatal("HTTP 隧道不应创建 TCP listener")
	}
	if tunnel.Config.DesiredState != protocol.ProxyDesiredStateRunning || tunnel.Config.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("HTTP 隧道激活后状态应为 running/exposed，得到 %s/%s", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
	}
}

func TestActivatePreparedTunnel_HTTPDoesNotConflictWithSelf(t *testing.T) {
	s := New(0)
	client := &ClientConn{
		ID:      "proxy-http-self-conflict",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	clientConn, serverConn := net.Pipe()
	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	client.dataSession = serverSession
	t.Cleanup(func() {
		s.StopAllProxies(client)
		_ = serverSession.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	req := protocol.ProxyNewRequest{
		Name:      "http-self-ok",
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: 3000,
		Domain:    "self.example.com",
	}

	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		t.Fatalf("prepareProxyTunnel 失败: %v", err)
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		t.Fatalf("activatePreparedTunnel 不应因自身域名而冲突: %v", err)
	}
}

func TestStartProxy_DuplicateName(t *testing.T) {
	s := New(0)
	clientID := "proxy-dup"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{
		Name:       "dup-tunnel",
		RemotePort: reserveTCPPort(t),
	}

	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("首次启动应成功: %v", err)
	}

	if err := s.StartProxy(client, req); err == nil {
		t.Error("同名隧道第二次启动应当报错冲突")
	}

	s.StopAllProxies(client)
	cConn.Close()
	sConn.Close()
}

func TestStopProxy(t *testing.T) {
	s := New(0)
	clientID := "proxy-stop"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	req := protocol.ProxyNewRequest{Name: "to-be-stopped", RemotePort: reserveTCPPort(t)}
	if err := s.StartProxy(client, req); err != nil {
		t.Fatalf("StartProxy 失败: %v", err)
	}

	client.proxyMu.RLock()
	port := client.proxies[req.Name].Config.RemotePort
	client.proxyMu.RUnlock()

	// 执行 Stop
	if err := s.StopProxy(client, "to-be-stopped"); err != nil {
		t.Fatalf("StopProxy 出错: %v", err)
	}

	// 等待一小会儿确保 net.Listener close 生效
	time.Sleep(50 * time.Millisecond)

	// 测试 Dial 原来的端口应该会被拒绝
	_, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
	if err == nil {
		t.Errorf("代理已停止，但端口 %d 仍能被连接", port)
	}

	cConn.Close()
	sConn.Close()
}

func TestStopAllProxies(t *testing.T) {
	s := New(0)
	clientID := "proxy-stop-all"
	client := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, client)
	cConn, sConn := net.Pipe()
	sSession, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
	client.dataSession = sSession

	if err := s.StartProxy(client, protocol.ProxyNewRequest{Name: "t1", RemotePort: reserveTCPPort(t)}); err != nil {
		t.Fatalf("启动 t1 失败: %v", err)
	}
	if err := s.StartProxy(client, protocol.ProxyNewRequest{Name: "t2", RemotePort: reserveTCPPort(t)}); err != nil {
		t.Fatalf("启动 t2 失败: %v", err)
	}

	client.proxyMu.RLock()
	count := len(client.proxies)
	client.proxyMu.RUnlock()

	if count != 2 {
		t.Fatalf("期望有 2 个隧道，得到 %d", count)
	}

	s.StopAllProxies(client)

	client.proxyMu.RLock()
	countAf := len(client.proxies)
	client.proxyMu.RUnlock()

	if countAf != 0 {
		t.Errorf("StopAllProxies 后代理映射表应该清空，得到长度 %d", countAf)
	}
	cConn.Close()
	sConn.Close()
}

// ============================================================
// 完整的 Proxy 接收循环与转发行为测试
// ============================================================

func TestProxyAcceptLoop_And_HandleProxyConn(t *testing.T) {
	s := New(0)
	clientID := "forward-client"
	cc := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, cc)

	// 1. 模拟网络通道 (用于 Yamux multiplexing)
	pipeC, pipeS := net.Pipe()
	defer pipeC.Close()
	defer pipeS.Close()

	// 初始化 Yamux Server/Client session
	var serverSession *yamux.Session
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverSession, _ = mux.NewServerSession(pipeS, mux.DefaultConfig())
		wg.Done()
	}()
	clientSession, _ := mux.NewClientSession(pipeC, mux.DefaultConfig())
	wg.Wait()

	cc.dataSession = serverSession
	defer serverSession.Close()
	defer clientSession.Close()

	// 2. 启动代理监听
	tunnelName := "echo-http-tunnel"
	req := protocol.ProxyNewRequest{
		Name:       tunnelName,
		Type:       protocol.ProxyTypeTCP,
		RemotePort: reserveTCPPort(t),
	}

	err := s.StartProxy(cc, req)
	if err != nil {
		t.Fatalf("启动代理失败: %v", err)
	}
	defer s.StopProxy(cc, tunnelName)

	cc.proxyMu.RLock()
	remotePort := cc.proxies[tunnelName].Config.RemotePort
	cc.proxyMu.RUnlock()

	// 3. 在客户端一侧（Client侧）起一个 goroutine 处理 Yamux 连接
	// 期望将接收到的流量转发给本地的 HTTP Test Server
	localBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proxy-Target", "hit")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello from backend"))
	}))
	defer localBackend.Close()

	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			go func(stream net.Conn) {
				defer stream.Close()
				// 丢弃前面代理传入的 2Bytes length + Name 作为 header (mock Client parsing)
				var ln [2]byte
				stream.Read(ln[:])
				nameLen := int(ln[0])<<8 | int(ln[1])
				nameBuf := make([]byte, nameLen)
				stream.Read(nameBuf)

				// Dial 真实本地后端
				backendConn, err := net.Dial("tcp", localBackend.Listener.Addr().String())
				if err != nil {
					return
				}
				defer backendConn.Close()
				mux.Relay(stream, backendConn)
			}(stream)
		}
	}()

	// 4. 从真实网络请求 User 发起的请求 (连接 Server 分配的 RemotePort)
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d", remotePort))
	if err != nil {
		t.Fatalf("请求代理地址失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码期望 200，得到 %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Proxy-Target") != "hit" {
		t.Errorf("未正确触达后端 HTTP Server")
	}
}

// ============================================================
// 并发端口竞争测试
// ============================================================

func TestStartProxy_ConcurrentPortConflict(t *testing.T) {
	s := New(0)

	// 先分配一个固定端口用于竞争
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预分配端口失败: %v", err)
	}
	contestedPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // 释放端口让两个 Client 去抢

	// 创建两个 Client，各自有自己的 data session
	makeClient := func(id string) *ClientConn {
		client := &ClientConn{
			ID:      id,
			proxies: make(map[string]*ProxyTunnel),
		}
		s.clients.Store(id, client)
		cConn, sConn := net.Pipe()
		session, _ := mux.NewServerSession(sConn, mux.DefaultConfig())
		client.dataSession = session
		t.Cleanup(func() {
			cConn.Close()
			sConn.Close()
			session.Close()
		})
		return client
	}

	client1 := makeClient("race-client-1")
	client2 := makeClient("race-client-2")

	// 并发启动代理抢同一端口
	var wg sync.WaitGroup
	results := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		results <- s.StartProxy(client1, protocol.ProxyNewRequest{
			Name:       "race-tunnel",
			RemotePort: contestedPort,
		})
	}()
	go func() {
		defer wg.Done()
		results <- s.StartProxy(client2, protocol.ProxyNewRequest{
			Name:       "race-tunnel",
			RemotePort: contestedPort,
		})
	}()

	wg.Wait()
	close(results)

	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}

	if successes != 1 {
		t.Errorf("抢同一端口应只有 1 个成功，实际成功 %d 个", successes)
	}
	if failures != 1 {
		t.Errorf("抢同一端口应有 1 个失败，实际失败 %d 个", failures)
	}

	// 清理
	s.StopAllProxies(client1)
	s.StopAllProxies(client2)
}

type scriptedListener struct {
	addr      net.Addr
	acceptCh  chan error
	closeOnce sync.Once
}

func newScriptedListener(t *testing.T) *scriptedListener {
	t.Helper()
	return &scriptedListener{
		addr:     &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: reserveTCPPort(t)},
		acceptCh: make(chan error, 1),
	}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	err, ok := <-l.acceptCh
	if !ok {
		return nil, net.ErrClosed
	}
	return nil, err
}

func (l *scriptedListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.acceptCh)
	})
	return nil
}

func (l *scriptedListener) Addr() net.Addr { return l.addr }

func TestProxyAcceptLoop_UnexpectedAcceptFailureMarksTunnelError(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:      "accept-error-client",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "accept-error-tunnel",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   listener.addr.(*net.TCPAddr).Port,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntime(client, tunnel))

	listener.acceptCh <- errors.New("boom")
	s.proxyAcceptLoop(client, tunnel, listener, tunnel.done)

	client.proxyMu.RLock()
	got := client.proxies[tunnel.Config.Name].Config
	currentListener := client.proxies[tunnel.Config.Name].Listener
	client.proxyMu.RUnlock()

	if got.DesiredState != protocol.ProxyDesiredStateRunning || got.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("意外 Accept 失败后状态应为 running/error，得到 %s/%s", got.DesiredState, got.RuntimeState)
	}
	if got.Error == "" {
		t.Fatal("意外 Accept 失败后 error 不应为空")
	}
	if currentListener != nil {
		t.Fatal("意外 Accept 失败后 listener 应已清理")
	}

	stored, ok := s.store.GetTunnel(client.ID, tunnel.Config.Name)
	if !ok {
		t.Fatal("store 中应仍存在该隧道")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("store 状态应为 running/error，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if stored.Error == "" {
		t.Fatal("store error 不应为空")
	}
}

func TestProxyAcceptLoop_ClosedDoneDoesNotMarkTunnelError(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:      "accept-shutdown-client",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "accept-shutdown-tunnel",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   listener.addr.(*net.TCPAddr).Port,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntime(client, tunnel))

	close(tunnel.done)
	listener.acceptCh <- net.ErrClosed
	s.proxyAcceptLoop(client, tunnel, listener, tunnel.done)

	client.proxyMu.RLock()
	got := client.proxies[tunnel.Config.Name].Config
	client.proxyMu.RUnlock()

	if got.DesiredState != protocol.ProxyDesiredStateRunning || got.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("正常关闭不应把状态降级为 error，得到 %s/%s", got.DesiredState, got.RuntimeState)
	}

	stored, ok := s.store.GetTunnel(client.ID, tunnel.Config.Name)
	if !ok {
		t.Fatal("store 中应仍存在该隧道")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("正常关闭后 store 状态应保持 running/exposed，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
	}
}

func TestMarkTCPProxyRuntimeErrorIfCurrent_StaleListenerDoesNotDemote(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:      "stale-listener-client",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	oldListener := newScriptedListener(t)
	currentListener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "stale-listener-tunnel",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   currentListener.addr.(*net.TCPAddr).Port,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: currentListener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntime(client, tunnel))

	s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel, oldListener, "stale accept failure")

	client.proxyMu.RLock()
	got := client.proxies[tunnel.Config.Name].Config
	gotListener := client.proxies[tunnel.Config.Name].Listener
	client.proxyMu.RUnlock()

	if got.DesiredState != protocol.ProxyDesiredStateRunning || got.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("stale listener 不应把状态降级为 error，得到 %s/%s", got.DesiredState, got.RuntimeState)
	}
	if gotListener != currentListener {
		t.Fatal("stale listener 不应清理当前 listener")
	}

	stored, ok := s.store.GetTunnel(client.ID, tunnel.Config.Name)
	if !ok {
		t.Fatal("store 中应仍存在该隧道")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("stale listener 后 store 状态应保持 running/exposed，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
	}
}

func TestHandleProxyConn_OpenStreamFailureMarksTunnelError(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	client := &ClientConn{
		ID:      "open-stream-error-client",
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)

	listener := newScriptedListener(t)
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:         "open-stream-error-tunnel",
			Type:         protocol.ProxyTypeTCP,
			LocalIP:      "127.0.0.1",
			LocalPort:    8080,
			RemotePort:   listener.addr.(*net.TCPAddr).Port,
			ClientID:     client.ID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateExposed,
		},
		Listener: listener,
		done:     make(chan struct{}),
	}
	client.proxies[tunnel.Config.Name] = tunnel

	mustAddStableTunnel(t, s.store, storedTunnelFromRuntime(client, tunnel))

	peerConn, extConn := net.Pipe()
	defer peerConn.Close()

	done := make(chan struct{})
	go func() {
		s.handleProxyConn(client, tunnel, listener, extConn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleProxyConn 在 OpenStream 失败后未及时退出")
	}

	client.proxyMu.RLock()
	got := client.proxies[tunnel.Config.Name].Config
	currentListener := client.proxies[tunnel.Config.Name].Listener
	client.proxyMu.RUnlock()

	if got.DesiredState != protocol.ProxyDesiredStateRunning || got.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("OpenStream 失败后状态应为 running/error，得到 %s/%s", got.DesiredState, got.RuntimeState)
	}
	if !strings.Contains(got.Error, "数据通道未建立") {
		t.Fatalf("OpenStream 失败后 error 应包含数据通道原因，得到 %q", got.Error)
	}
	if currentListener != nil {
		t.Fatal("OpenStream 失败后 listener 应已清理")
	}

	stored, ok := s.store.GetTunnel(client.ID, tunnel.Config.Name)
	if !ok {
		t.Fatal("store 中应仍存在该隧道")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateRunning || stored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("store 状态应为 running/error，得到 %s/%s", stored.DesiredState, stored.RuntimeState)
	}
	if !strings.Contains(stored.Error, "数据通道未建立") {
		t.Fatalf("store error 应包含数据通道原因，得到 %q", stored.Error)
	}
}
