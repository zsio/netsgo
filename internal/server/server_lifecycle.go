package server

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
)

func (s *Server) beginLongLivedHandler() func() {
	s.longLivedHandlers.Add(1)
	return s.longLivedHandlers.Done
}

func (s *Server) trackManagedConn(conn *websocket.Conn) func() {
	release := s.beginLongLivedHandler()
	s.managedConnMu.Lock()
	if s.managedConns == nil {
		s.managedConns = make(map[*websocket.Conn]struct{})
	}
	s.managedConns[conn] = struct{}{}
	s.managedConnMu.Unlock()

	return func() {
		s.managedConnMu.Lock()
		delete(s.managedConns, conn)
		s.managedConnMu.Unlock()
		release()
	}
}

func (s *Server) closeManagedConns(reason string) {
	s.managedConnMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.managedConns))
	for conn := range s.managedConns {
		conns = append(conns, conn)
	}
	s.managedConnMu.Unlock()

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

func (s *Server) waitForLongLivedHandlers(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.longLivedHandlers.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
