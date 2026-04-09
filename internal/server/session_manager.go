package server

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// SessionManager holds all state related to client connection lifecycles:
//   - managedConns: all currently managed WebSocket connections (for graceful shutdown)
//   - longLivedHandlers: goroutine count for long-lived connections (waited on during Shutdown)
//   - nextGeneration: monotonically increasing client generation counter
//   - timeout durations for the three phases of the data channel
//
// Other files in the same package access this directly via s.sessions.*; no interface is exported.
type SessionManager struct {
	managedConnMu     sync.Mutex
	managedConns      map[*websocket.Conn]struct{}
	longLivedHandlers sync.WaitGroup
	nextGeneration    atomic.Uint64

	pendingDataTimeout      time.Duration
	dataHandshakeTimeout    time.Duration
	dataHandshakeAckTimeout time.Duration
}

// newSessionManager creates a SessionManager with default timeouts.
func newSessionManager() *SessionManager {
	return &SessionManager{
		managedConns:            make(map[*websocket.Conn]struct{}),
		pendingDataTimeout:      15 * time.Second,
		dataHandshakeTimeout:    10 * time.Second,
		dataHandshakeAckTimeout: 2 * time.Second,
	}
}

// beginLongLivedHandler registers a long-lived connection goroutine and returns a done callback.
func (sm *SessionManager) beginLongLivedHandler() func() {
	sm.longLivedHandlers.Add(1)
	return sm.longLivedHandlers.Done
}

// trackManagedConn adds conn to the managed connection set and registers a longLivedHandler.
// The returned function should be called when the handler goroutine exits.
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

// closeManagedConns sends CloseGoingAway to all managed connections and closes them.
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

// waitForLongLivedHandlers waits for all long-lived goroutines to exit until ctx times out.
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

// nextClientGeneration returns the next monotonically increasing client generation number.
func (sm *SessionManager) nextClientGeneration() uint64 {
	return sm.nextGeneration.Add(1)
}
