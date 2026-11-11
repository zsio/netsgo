package server

import (
	"log"
	"net"

	"netsgo/internal/connlimit"
)

func (s *Server) admitForwardingConn(conn net.Conn, budget *connlimit.Limiter) (net.Conn, bool) {
	admitted, ok := budget.Admit(conn, s.MaxConnections)
	if !ok {
		s.reportConnectionLimit(budget)
	}
	return admitted, ok
}

func (s *Server) reportConnectionLimit(budget *connlimit.Limiter) {
	if !budget.ShouldReportRejection() {
		return
	}
	maximum := s.MaxConnections
	if maximum <= 0 {
		maximum = connlimit.Default
	}
	log.Printf("⚠️ forwarding connection limit reached: active=%d limit=%d; rejected new connection", budget.Active(), maximum)
}
