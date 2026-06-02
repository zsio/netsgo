package server

import (
	"time"

	"netsgo/pkg/protocol"
)

func (s *Server) resolveTunnelProvisionAckWaiter(clientID string, generation uint64, resp provisionAckResult) bool {
	return s.tunnels.resolveProvisionAckWaiter(clientID, generation, resp)
}

func (s *Server) cancelTunnelProvisionAckWaiters(clientID string, generation uint64) {
	s.tunnels.cancelProvisionAckWaiters(clientID, generation)
	s.tunnels.cancelPreflightWaiters(clientID, generation)
}

func (s *Server) waitForTunnelProvisionAck(client *ClientConn, req protocol.ProxyNewRequest) (provisionAckResult, error) {
	return s.tunnels.waitForProvisionAck(s, client, req)
}

func (s *Server) prepareTunnelProvisionRequest(client *ClientConn, tunnel *ProxyTunnel) protocol.ProxyNewRequest {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()

	if tunnel == nil {
		return protocol.ProxyNewRequest{}
	}
	revision := newTunnelRuntimeRevision()
	req := tunnel.Config.ToProxyNewRequest()
	req.ProvisionRevision = revision
	markTunnelProvisionPending(tunnel, client.ID, revision, time.Now())
	return req
}
