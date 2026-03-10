package server

import (
	"crypto/rand"
	"net"
	"sync"
	"testing"
	"time"

	"netsgo/pkg/mux"
)

// TestDisconnect_DuringActiveRelay 模拟数据正在转发时底层连接突然断开
// 验证: Relay goroutine 正常退出、无泄漏、无死锁
func TestDisconnect_DuringActiveRelay(t *testing.T) {
	s := New(0)
	agentID := "disconnect-agent"
	agent := &AgentConn{
		ID:      agentID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.agents.Store(agentID, agent)

	// 建立数据通道 (Yamux Session)
	clientPipe, serverPipe := net.Pipe()

	var sessionWg sync.WaitGroup
	sessionWg.Add(1)
	go func() {
		defer sessionWg.Done()
		session, err := mux.NewServerSession(serverPipe, mux.DefaultConfig())
		if err != nil {
			return
		}
		agent.dataMu.Lock()
		agent.dataSession = session
		agent.dataMu.Unlock()

		// 阻塞直到 Session 关闭
		<-session.CloseChan()
	}()

	clientSession, err := mux.NewClientSession(clientPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("创建客户端 Session 失败: %v", err)
	}

	// 等待 Server 端 Session 建立
	time.Sleep(100 * time.Millisecond)

	// 打开一个 Stream 并开始传输数据
	stream, err := clientSession.Open()
	if err != nil {
		t.Fatalf("打开 Stream 失败: %v", err)
	}

	// 在后台持续向 stream 写入数据（模拟活跃流量）
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		buf := make([]byte, 4096)
		rand.Read(buf)
		for {
			_, err := stream.Write(buf)
			if err != nil {
				return // 连接断开，正常退出
			}
		}
	}()

	// 在后台从 server 侧 Accept 并读取（模拟消费端）
	agent.dataMu.RLock()
	serverSession := agent.dataSession
	agent.dataMu.RUnlock()

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		sStream, err := serverSession.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 4096)
		for {
			_, err := sStream.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// 等待数据开始流动
	time.Sleep(100 * time.Millisecond)

	// 🔥 突然断开底层连接（模拟网络抖动）
	clientPipe.Close()
	serverPipe.Close()

	// 验证所有 goroutine 在合理时间内退出
	timeout := time.After(5 * time.Second)

	select {
	case <-writerDone:
	case <-timeout:
		t.Fatal("writer goroutine 没有在 5 秒内退出 — 可能死锁")
	}

	select {
	case <-readerDone:
	case <-timeout:
		t.Fatal("reader goroutine 没有在 5 秒内退出 — 可能死锁")
	}

	// 验证 Session 已关闭
	agent.dataMu.RLock()
	sess := agent.dataSession
	agent.dataMu.RUnlock()

	if sess != nil && !sess.IsClosed() {
		t.Error("底层连接断开后 dataSession 应该已关闭")
	}

	sessionWg.Wait() // 确保 server goroutine 也退出了
}

// TestDisconnect_RelayGoroutineCleanup 验证 Relay 转发过程中一端关闭后两个 goroutine 都能退出
func TestDisconnect_RelayGoroutineCleanup(t *testing.T) {
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()

	// 启动 Relay
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		mux.Relay(a2, b1)
	}()

	// 开始传输数据
	go func() {
		buf := make([]byte, 1024)
		rand.Read(buf)
		for {
			if _, err := a1.Write(buf); err != nil {
				return
			}
		}
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := b2.Read(buf); err != nil {
				return
			}
		}
	}()

	// 等到数据流动
	time.Sleep(50 * time.Millisecond)

	// 断开一端
	a1.Close()

	// Relay 应在合理时间内退出
	select {
	case <-relayDone:
		// ✅ 正常退出
	case <-time.After(3 * time.Second):
		t.Fatal("Relay goroutine 未在 3 秒内退出 — goroutine 泄漏")
	}

	b2.Close()
}
