package server

import (
	"fmt"
	"net/http"
	"time"

	"netsgo/pkg/protocol"
)

func (s *Server) notifyClientTunnelPreflight(client *ClientConn, req protocol.TunnelPreflightRequest) error {
	msg, err := protocol.NewMessage(protocol.MsgTypeTunnelPreflight, req)
	if err != nil {
		return err
	}
	return s.writeControlMessage(client, msg)
}

func (s *Server) waitForClientTunnelPreflight(client *ClientConn, req protocol.TunnelPreflightRequest) (protocol.TunnelPreflightResponse, error) {
	if req.RequestID == "" {
		return protocol.TunnelPreflightResponse{}, fmt.Errorf("preflight request missing request_id")
	}
	ch, err := s.tunnels.registerPreflightWaiter(client, req.RequestID)
	if err != nil {
		return protocol.TunnelPreflightResponse{}, err
	}
	if err := s.notifyClientTunnelPreflight(client, req); err != nil {
		s.tunnels.unregisterPreflightWaiter(client, req.RequestID)
		return protocol.TunnelPreflightResponse{}, err
	}

	timeout := s.tunnels.preflightTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp, ok := <-ch:
		if !ok {
			return protocol.TunnelPreflightResponse{}, errTunnelPreflightCancelled
		}
		if err := validateTunnelPreflightResponse(req, resp); err != nil {
			return protocol.TunnelPreflightResponse{}, err
		}
		return resp, nil
	case <-timer.C:
		s.tunnels.unregisterPreflightWaiter(client, req.RequestID)
		return protocol.TunnelPreflightResponse{}, errTunnelPreflightTimeout
	}
}

func validateTunnelPreflightResponse(req protocol.TunnelPreflightRequest, resp protocol.TunnelPreflightResponse) error {
	if resp.RequestID != req.RequestID {
		return fmt.Errorf("preflight response request_id mismatch")
	}
	if resp.TunnelID != req.TunnelID {
		return fmt.Errorf("preflight response tunnel_id mismatch")
	}
	if resp.Revision != req.Revision {
		return fmt.Errorf("preflight response revision mismatch")
	}
	if resp.Role != req.Role {
		return fmt.Errorf("preflight response role mismatch")
	}
	return nil
}

func (s *Server) preflightClientIngress(req tunnelCreateRequestAPI, existingID string) error {
	if req.Topology != tunnelTopologyClientToClient || req.Ingress.Location != tunnelEndpointLocationClient {
		return nil
	}
	if req.Ingress.Type != tunnelIngressTypeTCPListen && req.Ingress.Type != tunnelIngressTypeUDPListen {
		return nil
	}
	client, ok := s.loadLiveClient(req.Ingress.ClientID)
	if !ok {
		return nil
	}

	revision := int64(1)
	if existingID != "" && s.store != nil {
		if current, ok, findErr := s.findStoredTunnelByID(existingID); findErr == nil && ok {
			revision = current.Revision + 1
			if sameClientIngressResource(current.Ingress, req.Ingress, current.Topology, req.Topology) {
				return nil
			}
		}
	}

	resp, err := s.waitForClientTunnelPreflight(client, protocol.TunnelPreflightRequest{
		RequestID: generateUUID(),
		TunnelID:  existingID,
		Revision:  revision,
		Role:      protocol.DataStreamRoleIngress,
		Ingress: protocol.EndpointSpec{
			Location: req.Ingress.Location,
			ClientID: req.Ingress.ClientID,
			Type:     req.Ingress.Type,
			Config:   req.Ingress.Config,
		},
	})
	if err != nil {
		code := protocol.TunnelMutationErrorCodeIngressPreflightRejected
		status := http.StatusBadGateway
		if err == errTunnelPreflightTimeout {
			code = protocol.TunnelMutationErrorCodeIngressPreflightTimeout
			status = http.StatusGatewayTimeout
		}
		return newProxyRequestValidationError(fmt.Errorf("ingress preflight failed: %w", err), "ingress.config.port", code, status)
	}
	if !resp.Accepted {
		code := resp.Code
		if code == "" {
			code = protocol.TunnelMutationErrorCodeIngressPreflightRejected
		}
		message := resp.Message
		if message == "" {
			message = "ingress preflight rejected"
		}
		status := http.StatusBadRequest
		if code == protocol.TunnelMutationErrorCodeIngressPortInUse || code == protocol.TunnelMutationErrorCodeIngressResourceConflict {
			status = http.StatusConflict
		}
		return newProxyRequestValidationError(fmt.Errorf("ingress preflight rejected: %s", message), "ingress.config.port", code, status)
	}
	return nil
}

func sameClientIngressResource(current EndpointSpec, next endpointSpecAPI, currentTopology, nextTopology string) bool {
	if current.Location != protocol.EndpointLocationClient || next.Location != protocol.EndpointLocationClient {
		return false
	}
	if current.ClientID != next.ClientID || current.Type != next.Type {
		return false
	}
	if current.Type != protocol.IngressTypeTCPListen && current.Type != protocol.IngressTypeUDPListen {
		return false
	}
	currentCfg, err := decodeListenEndpointConfig(endpointSpecAPI{
		Location: current.Location,
		ClientID: current.ClientID,
		Type:     current.Type,
		Config:   current.Config,
	}, currentTopology)
	if err != nil {
		return false
	}
	nextCfg, err := decodeListenEndpointConfig(next, nextTopology)
	if err != nil {
		return false
	}
	return currentCfg.BindIP == nextCfg.BindIP && currentCfg.Port == nextCfg.Port
}
