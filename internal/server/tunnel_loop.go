package server

import (
	"net/http"

	"netsgo/internal/tunnelloop"
	"netsgo/pkg/protocol"
)

func (s *Server) ingressNetworkIdentity(ingress protocol.EndpointSpec) string {
	if ingress.Location == protocol.EndpointLocationServer {
		return s.networkNamespaceID
	}
	if client, ok := s.loadLiveClient(ingress.ClientID); ok {
		return client.GetInfo().NetworkNamespaceID
	}
	return ""
}

func (s *Server) validateTunnelLoop(ingress, target endpointSpecAPI, host string, port int) error {
	client, ok := s.loadLiveClient(target.ClientID)
	if !ok {
		return nil
	}
	ingressSpec := protocol.EndpointSpec{Location: ingress.Location, ClientID: ingress.ClientID, Type: ingress.Type, Config: ingress.Config}
	targetSpec := protocol.EndpointSpec{Location: target.Location, ClientID: target.ClientID, Type: target.Type, Config: target.Config}
	guard := tunnelloop.New(protocol.TunnelSpec{Ingress: ingressSpec, Target: targetSpec}, s.ingressNetworkIdentity(ingressSpec), client.GetInfo().NetworkNamespaceID)
	if err := guard.CheckLiteral(host, port); err != nil {
		return newProxyRequestValidationError(err, "target.config", protocol.TunnelMutationErrorCodeTunnelLoop, http.StatusBadRequest)
	}
	return nil
}
