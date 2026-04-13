package mux

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// ============================================================
// Session 创建测试
// ============================================================

func TestNewSession_ClientServer(t *testing.T) {
	// 通过 net.Pipe 模拟一条 TCP 连接
	serverConn, clientConn := net.Pipe()

	cfg := DefaultConfig()

	var serverSession, clientSession interface{ Close() error }
	var sErr, cErr error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		serverSession, sErr = NewServerSession(serverConn, cfg)
	}()
	go func() {
		defer wg.Done()
		clientSession, cErr = NewClientSession(clientConn, cfg)
	}()

	wg.Wait()

	if sErr != nil {
		t.Fatalf("创建 Server Session 失败: %v", sErr)
	}
	if cErr != nil {
		t.Fatalf("创建 Client Session 失败: %v", cErr)
	}

	defer func() { _ = serverSession.Close() }()
	defer func() { _ = clientSession.Close() }()
}

func TestNewSession_NilConfig(t *testing.T) {
	serverConn, clientConn := net.Pipe()

	var wg sync.WaitGroup
	wg.Add(2)

	var sErr, cErr error
	var serverSession, clientSession interface{ Close() error }

	go func() {
		defer wg.Done()
		serverSession, sErr = NewServerSession(serverConn, nil)
	}()
	go func() {
		defer wg.Done()
		clientSession, cErr = NewClientSession(clientConn, nil)
	}()

	wg.Wait()

	if sErr != nil {
		t.Fatalf("nil config 创建 Server Session 失败: %v", sErr)
	}
	if cErr != nil {
		t.Fatalf("nil config 创建 Client Session 失败: %v", cErr)
	}

	defer func() { _ = serverSession.Close() }()
	defer func() { _ = clientSession.Close() }()
}

// ============================================================
// Stream 读写测试
// ============================================================

func TestStream_ReadWrite(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	cfg := DefaultConfig()

	serverSess, clientSess := mustCreateSessions(t, serverConn, clientConn, cfg)
	defer func() { _ = serverSess.Close() }()
	defer func() { _ = clientSess.Close() }()

	// Client 打开 Stream，Server 接受
	var stream1, stream2 net.Conn
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		stream1, err1 = clientSess.Open()
	}()
	go func() {
		defer wg.Done()
		stream2, err2 = serverSess.Accept()
	}()

	wg.Wait()

	if err1 != nil {
		t.Fatalf("OpenStream 失败: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("AcceptStream 失败: %v", err2)
	}
	defer func() { _ = stream1.Close() }()
	defer func() { _ = stream2.Close() }()

	// 双向读写
	testData := []byte("hello from client")
	go func() {
		_, _ = stream1.Write(testData)
	}()

	buf := make([]byte, 256)
	n, err := stream2.Read(buf)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if string(buf[:n]) != string(testData) {
		t.Errorf("数据不匹配: 期望 %q, 得到 %q", testData, buf[:n])
	}

	// 反向
	replyData := []byte("hello from server")
	go func() {
		_, _ = stream2.Write(replyData)
	}()

	n, err = stream1.Read(buf)
	if err != nil {
		t.Fatalf("Read 反向失败: %v", err)
	}
	if string(buf[:n]) != string(replyData) {
		t.Errorf("反向数据不匹配: 期望 %q, 得到 %q", replyData, buf[:n])
	}
}

// ============================================================
// 并发 Stream 测试
// ============================================================

func TestMultipleStreams_Concurrent(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	cfg := DefaultConfig()

	serverSess, clientSess := mustCreateSessions(t, serverConn, clientConn, cfg)
	defer func() { _ = serverSess.Close() }()
	defer func() { _ = clientSess.Close() }()

	const numStreams = 10
	errors := make(chan error, numStreams*2)

	// Server 端：接收 Stream 并回显
	go func() {
		for i := 0; i < numStreams; i++ {
			stream, err := serverSess.Accept()
			if err != nil {
				errors <- fmt.Errorf("Server Accept #%d: %v", i, err)
				return
			}
			go func(s net.Conn) {
				defer func() { _ = s.Close() }()
				_, _ = io.Copy(s, s) // echo
			}(stream)
		}
	}()

	// Client 端：并发打开 N 个 Stream，写入数据并读取回显
	var wg sync.WaitGroup
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stream, err := clientSess.Open()
			if err != nil {
				errors <- fmt.Errorf("Client Open #%d: %v", idx, err)
				return
			}
			defer func() { _ = stream.Close() }()

			msg := fmt.Sprintf("stream-%d-data", idx)
			if _, err := stream.Write([]byte(msg)); err != nil {
				errors <- fmt.Errorf("Client Write #%d: %v", idx, err)
				return
			}

			buf := make([]byte, 256)
			n, err := stream.Read(buf)
			if err != nil {
				errors <- fmt.Errorf("Client Read #%d: %v", idx, err)
				return
			}
			if string(buf[:n]) != msg {
				errors <- fmt.Errorf("Stream #%d 回显不匹配: 期望 %q, 得到 %q", idx, msg, buf[:n])
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

// ============================================================
// Relay 测试
// ============================================================

func TestRelay_BidirectionalCopy(t *testing.T) {
	// 创建两对 pipe 模拟 "外部连接" 和 "内部连接"
	// extClient ←→ [extServer | localClient] ←→ localServer
	// Relay 连接 extServer 和 localClient
	extClient, extServer := net.Pipe()
	localClient, localServer := net.Pipe()

	// 启动 Relay（extServer ↔ localClient）
	relayDone := make(chan struct{})
	go func() {
		Relay(extServer, localClient)
		close(relayDone)
	}()

	// 外部用户写入请求
	testReq := []byte("GET / HTTP/1.1\r\n\r\n")
	go func() { _, _ = extClient.Write(testReq) }()

	// 本地服务读取请求
	buf := make([]byte, 256)
	n, err := localServer.Read(buf)
	if err != nil {
		t.Fatalf("本地服务读取失败: %v", err)
	}
	if !bytes.Equal(buf[:n], testReq) {
		t.Errorf("请求数据不匹配: 得到 %q", buf[:n])
	}

	// 本地服务回复并关闭
	testResp := []byte("HTTP/1.1 200 OK\r\n\r\nhello")
	_, _ = localServer.Write(testResp)
	_ = localServer.Close()

	// 外部用户读取回复
	result, err := io.ReadAll(extClient)
	if err != nil {
		t.Fatalf("外部用户读取失败: %v", err)
	}
	if !bytes.Equal(result, testResp) {
		t.Errorf("响应数据不匹配: 期望 %q, 得到 %q", testResp, result)
	}

	// extClient 的读端已经 EOF（因为 Relay 关闭了 extServer）
	// 关闭 extClient 让另一方向也结束
	_ = extClient.Close()

	// 等待 Relay 结束
	select {
	case <-relayDone:
	case <-time.After(5 * time.Second):
		t.Error("Relay 超时未结束")
	}
}

func TestSession_Close(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	cfg := DefaultConfig()

	serverSess, clientSess := mustCreateSessions(t, serverConn, clientConn, cfg)

	// 关闭 Client Session
	if err := clientSess.Close(); err != nil {
		t.Fatalf("关闭 Client Session 失败: %v", err)
	}

	// Server 侧 Accept 应失败
	_, err := serverSess.Accept()
	if err == nil {
		t.Error("Session 关闭后 Accept 应返回 error")
	}

	_ = serverSess.Close()
}

// ============================================================
// DefaultConfig 测试
// ============================================================

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.KeepAlive {
		t.Error("默认应启用 KeepAlive")
	}
	if cfg.KeepAliveInterval != 30*time.Second {
		t.Errorf("默认 KeepAliveInterval 期望 30s，得到 %v", cfg.KeepAliveInterval)
	}
	if cfg.MaxStreamWindow != 256*1024 {
		t.Errorf("默认 MaxStreamWindow 期望 256KB，得到 %d", cfg.MaxStreamWindow)
	}
}

// ============================================================
// 辅助函数
// ============================================================

func mustCreateSessions(t *testing.T, serverConn, clientConn net.Conn, cfg *Config) (server, client interface {
	Open() (net.Conn, error)
	Accept() (net.Conn, error)
	Close() error
}) {
	t.Helper()
	var sErr, cErr error
	var wg sync.WaitGroup
	wg.Add(2)

	var s, c interface {
		Open() (net.Conn, error)
		Accept() (net.Conn, error)
		Close() error
	}

	go func() {
		defer wg.Done()
		sess, err := NewServerSession(serverConn, cfg)
		s = sess
		sErr = err
	}()
	go func() {
		defer wg.Done()
		sess, err := NewClientSession(clientConn, cfg)
		c = sess
		cErr = err
	}()

	wg.Wait()

	if sErr != nil {
		t.Fatalf("创建 Server Session 失败: %v", sErr)
	}
	if cErr != nil {
		t.Fatalf("创建 Client Session 失败: %v", cErr)
	}
	return s, c
}
