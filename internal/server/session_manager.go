package server

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// SessionManager 持有客户端连接生命周期相关的全部状态：
//   - managedConns：当前所有受管 WebSocket 连接（用于优雅关闭）
//   - longLivedHandlers：长连接 goroutine 计数（用于 Shutdown 等待）
//   - nextGeneration：单调递增的客户端代际计数器
//   - 数据通道三个阶段的超时时间
//
// 同包内的其他文件通过 s.sessions.* 直接访问；不对外暴露接口。
type SessionManager struct {
	managedConnMu     sync.Mutex
	managedConns      map[*websocket.Conn]struct{}
	longLivedHandlers sync.WaitGroup
	nextGeneration    atomic.Uint64

	pendingDataTimeout      time.Duration
	dataHandshakeTimeout    time.Duration
	dataHandshakeAckTimeout time.Duration
}

// newSessionManager 创建 SessionManager 并设置默认超时。
func newSessionManager() *SessionManager {
	return &SessionManager{
		managedConns:            make(map[*websocket.Conn]struct{}),
		pendingDataTimeout:      15 * time.Second,
		dataHandshakeTimeout:    10 * time.Second,
		dataHandshakeAckTimeout: 2 * time.Second,
	}
}

// beginLongLivedHandler 注册一个长连接 goroutine，返回完成回调。
func (sm *SessionManager) beginLongLivedHandler() func() {
	sm.longLivedHandlers.Add(1)
	return sm.longLivedHandlers.Done
}

// trackManagedConn 记录 conn 到受管连接集合，并注册 longLivedHandler；
// 返回的函数应在 handler goroutine 退出时调用。
func (sm *SessionManager) trackManagedConn(conn *websocket.Conn) func() {
	release := sm.beginLongLivedHandler()
	sm.managedConnMu.Lock()
	if sm.managedConns == nil {
		sm.managedConns = make(map[*websocket.Conn]struct{})
	}
	sm.managedConns[conn] = struct{}{}
	sm.managedConnMu.Unlock()

	return func() {
		sm.managedConnMu.Lock()
		delete(sm.managedConns, conn)
		sm.managedConnMu.Unlock()
		release()
	}
}

// closeManagedConns 向所有受管连接发送 CloseGoingAway 并关闭。
func (sm *SessionManager) closeManagedConns(reason string) {
	sm.managedConnMu.Lock()
	conns := make([]*websocket.Conn, 0, len(sm.managedConns))
	for conn := range sm.managedConns {
		conns = append(conns, conn)
	}
	sm.managedConnMu.Unlock()

	deadline := time.Now().Add(time.Second)
	for _, conn := range conns {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, reason),
			deadline,
		)
		_ = conn.Close()
	}
}

// waitForLongLivedHandlers 等待所有长连接 goroutine 退出，直到 ctx 超时。
func (sm *SessionManager) waitForLongLivedHandlers(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		sm.longLivedHandlers.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// nextClientGeneration 返回下一个单调递增的客户端代际编号。
func (sm *SessionManager) nextClientGeneration() uint64 {
	return sm.nextGeneration.Add(1)
}
