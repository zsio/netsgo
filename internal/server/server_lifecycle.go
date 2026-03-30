package server

import (
	"context"

	"github.com/gorilla/websocket"
)

func (s *Server) beginLongLivedHandler() func() {
	return s.sessions.beginLongLivedHandler()
}

func (s *Server) trackManagedConn(conn *websocket.Conn) func() {
	return s.sessions.trackManagedConn(conn)
}

func (s *Server) closeManagedConns(reason string) {
	s.sessions.closeManagedConns(reason)
}

func (s *Server) waitForLongLivedHandlers(ctx context.Context) error {
	return s.sessions.waitForLongLivedHandlers(ctx)
}
