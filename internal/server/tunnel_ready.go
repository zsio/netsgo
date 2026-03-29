package server

import "netsgo/pkg/protocol"

func (s *Server) registerTunnelProvisionAckWaiter(client *ClientConn, name string) (<-chan provisionAckResult, error) {
	return s.tunnels.registerProvisionAckWaiter(client, name)
}

func (s *Server) unregisterTunnelProvisionAckWaiter(client *ClientConn, name string) {
	s.tunnels.unregisterProvisionAckWaiter(client, name)
}

func (s *Server) resolveTunnelProvisionAckWaiter(clientID string, generation uint64, resp provisionAckResult) bool {
	return s.tunnels.resolveProvisionAckWaiter(clientID, generation, resp)
}

func (s *Server) cancelTunnelProvisionAckWaiters(clientID string, generation uint64) {
	s.tunnels.cancelProvisionAckWaiters(clientID, generation)
}

func (s *Server) waitForTunnelProvisionAck(client *ClientConn, req protocol.ProxyNewRequest) (provisionAckResult, error) {
	return s.tunnels.waitForProvisionAck(s, client, req)
}
