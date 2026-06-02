package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"netsgo/pkg/protocol"
)

func (s *Server) handleUpdateDisplayName(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	if clientID == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_client_id", "missing client id")
		return
	}

	var req struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if s.auth.adminStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_store_unavailable", "admin store unavailable")
		return
	}

	if err := s.auth.adminStore.UpdateClientDisplayName(clientID, req.DisplayName); err != nil {
		writeAPIError(w, http.StatusNotFound, "client_not_found", err.Error())
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"display_name": req.DisplayName,
	})
}

func validateBandwidthSettings(settings protocol.BandwidthSettings) error {
	if settings.IngressBPS < 0 {
		return fmt.Errorf("ingress_bps must be non-negative")
	}
	if settings.EgressBPS < 0 {
		return fmt.Errorf("egress_bps must be non-negative")
	}
	return nil
}

func (s *Server) handleUpdateBandwidthSettings(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	if clientID == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_client_id", "missing client id")
		return
	}

	var req struct {
		IngressBPS *int64 `json:"ingress_bps"`
		EgressBPS  *int64 `json:"egress_bps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if req.IngressBPS == nil || req.EgressBPS == nil {
		writeAPIError(w, http.StatusBadRequest, "bandwidth_fields_required", "ingress_bps and egress_bps are required")
		return
	}

	settings := protocol.BandwidthSettings{
		IngressBPS: *req.IngressBPS,
		EgressBPS:  *req.EgressBPS,
	}
	if err := validateBandwidthSettings(settings); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_bandwidth_settings", err.Error())
		return
	}

	if s.auth.adminStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_store_unavailable", "admin store unavailable")
		return
	}

	if err := s.auth.adminStore.UpdateClientBandwidthSettings(clientID, settings); err != nil {
		switch {
		case errors.Is(err, ErrRegisteredClientNotFound):
			writeAPIError(w, http.StatusNotFound, "client_not_found", "client not found")
		default:
			writeAPIError(w, http.StatusInternalServerError, "client_bandwidth_update_failed", err.Error())
		}
		return
	}

	if current, ok := s.clients.Load(clientID); ok {
		if err := current.(*ClientConn).SetBandwidthSettings(settings); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "client_bandwidth_apply_failed", err.Error())
			return
		}
	}

	encodeJSON(w, http.StatusOK, map[string]any{
		"success":            true,
		"bandwidth_settings": settings,
	})
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	if clientID == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_client_id", "missing client id")
		return
	}
	if s.auth.adminStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_store_unavailable", "admin store unavailable")
		return
	}
	if value, ok := s.clients.Load(clientID); ok {
		client := value.(*ClientConn)
		if client.getState() != clientStateClosing {
			writeAPIError(w, http.StatusConflict, "client_online_delete_forbidden", "client is online and cannot be deleted")
			return
		}
	}
	if _, ok := s.auth.adminStore.GetRegisteredClient(clientID); !ok {
		writeAPIError(w, http.StatusNotFound, "client_not_found", "client not found")
		return
	}

	if s.store != nil {
		if err := s.store.DeleteTunnelsByClientID(clientID); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "client_tunnels_delete_failed", err.Error())
			return
		}
	}
	if s.trafficStore != nil {
		if err := s.trafficStore.EvictClient(clientID); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "client_traffic_delete_failed", err.Error())
			return
		}
	}
	if err := s.auth.adminStore.DeleteRegisteredClient(clientID); err != nil {
		switch {
		case errors.Is(err, ErrRegisteredClientNotFound):
			writeAPIError(w, http.StatusNotFound, "client_not_found", "client not found")
		default:
			writeAPIError(w, http.StatusInternalServerError, "client_delete_failed", err.Error())
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func tunnelMutationErrorStatusAndBody(err error) (int, tunnelMutationErrorResponse) {
	status := http.StatusInternalServerError
	payload := tunnelMutationErrorResponse{
		Success: false,
		Error:   err.Error(),
		Message: err.Error(),
	}

	var ruleErr *httpTunnelRuleError
	var validationErr *proxyRequestValidationError
	var rejected *tunnelProvisionRejectedError
	switch {
	case errors.Is(err, errManagedTunnelClientNotFound):
		status = http.StatusNotFound
		payload.Error = "client not found"
	case errors.Is(err, errManagedTunnelNotFound):
		status = http.StatusNotFound
		payload.Error = "tunnel not found"
	case errors.Is(err, errTunnelProvisionAckTimeout):
		status = http.StatusGatewayTimeout
	case errors.As(err, &rejected):
		status = http.StatusBadGateway
	case errors.As(err, &ruleErr):
		status = http.StatusConflict
		payload.ErrorCode = ruleErr.ErrorCode()
		payload.Code = ruleErr.ErrorCode()
		payload.Field = ruleErr.Field()
		payload.ConflictingTunnels = ruleErr.ConflictingTunnels()
	case errors.As(err, &validationErr):
		status = validationErr.StatusCode()
		payload.ErrorCode = validationErr.ErrorCode()
		payload.Code = validationErr.ErrorCode()
		payload.Field = validationErr.Field()
	}

	return status, payload
}

func tunnelSelectorFromPath(r *http.Request) string {
	if selector := r.PathValue("tunnel_id"); selector != "" {
		return selector
	}
	return r.PathValue("name")
}

func (s *Server) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")

	var req protocol.ProxyNewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	// Tunnel IDs are server-owned stable identifiers. Ignore any client-supplied
	// value on creation so callers cannot collide with or spoof existing tunnels.
	req.ID = ""
	req.Name = strings.TrimSpace(req.Name)

	var (
		config protocol.ProxyConfig
		err    error
	)
	if client, ok := s.loadLiveClient(clientID); ok {
		config, err = s.createManagedTunnel(client, req, true, "created")
	} else {
		config, err = s.createOfflineManagedTunnel(clientID, req)
	}
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}

	encodeJSON(w, http.StatusCreated, map[string]any{
		"success":     true,
		"message":     "tunnel created successfully",
		"remote_port": config.RemotePort,
	})
}

func (s *Server) handleResumeTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelSelector := tunnelSelectorFromPath(r)

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		if _, err := s.resumeOfflineManagedTunnel(clientID, tunnelSelector); err != nil {
			switch {
			case errors.Is(err, errManagedTunnelClientNotFound):
				writeAPIError(w, http.StatusNotFound, "client_not_found", "client not found")
			case errors.Is(err, errManagedTunnelNotFound):
				writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
			case err.Error() == "only stopped or error tunnels can be resumed":
				writeAPIError(w, http.StatusBadRequest, "tunnel_resume_not_allowed", err.Error())
			default:
				writeAPIError(w, http.StatusInternalServerError, "tunnel_resume_failed", err.Error())
			}
			return
		}

		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel resumed"})
		return
	}

	tunnelName, tunnel, exists := findTunnelBySelector(client, tunnelSelector)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
		return
	}
	if !canResumeTunnel(tunnel.Config) {
		writeAPIError(w, http.StatusBadRequest, "tunnel_resume_not_allowed", "only stopped/idle or running/error tunnels can be resumed")
		return
	}

	if err := s.resumeManagedTunnel(client, tunnelName); err != nil {
		status := http.StatusInternalServerError
		var rejected *tunnelProvisionRejectedError
		switch {
		case errors.Is(err, errTunnelProvisionAckTimeout):
			status = http.StatusGatewayTimeout
		case errors.As(err, &rejected):
			status = http.StatusBadGateway
		}
		writeAPIError(w, status, "tunnel_resume_failed", err.Error())
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel resumed"})
}

func (s *Server) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelSelector := tunnelSelectorFromPath(r)

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		if _, err := s.stopOfflineManagedTunnel(clientID, tunnelSelector); err != nil {
			switch {
			case errors.Is(err, errManagedTunnelClientNotFound):
				writeAPIError(w, http.StatusNotFound, "client_not_found", "client not found")
			case errors.Is(err, errManagedTunnelNotFound):
				writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
			default:
				writeAPIError(w, http.StatusInternalServerError, "tunnel_stop_failed", err.Error())
			}
			return
		}

		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel stopped"})
		return
	}

	tunnelName, _, exists := findTunnelBySelector(client, tunnelSelector)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
		return
	}

	if err := s.stopManagedTunnel(client, tunnelName); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_stop_failed", err.Error())
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel stopped"})
}

func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelSelector := tunnelSelectorFromPath(r)

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		if err := s.deleteOfflineManagedTunnel(clientID, tunnelSelector); err != nil {
			switch {
			case errors.Is(err, errManagedTunnelClientNotFound):
				writeAPIError(w, http.StatusNotFound, "client_not_found", "client not found")
			case errors.Is(err, errManagedTunnelNotFound):
				writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
			default:
				writeAPIError(w, http.StatusInternalServerError, "tunnel_delete_failed", err.Error())
			}
			return
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}

	tunnelName, tunnel, exists := findTunnelBySelector(client, tunnelSelector)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
		return
	}

	if !canEditOrDeleteLiveTunnel(tunnel.Config) {
		encodeJSON(w, http.StatusBadRequest, tunnelDeleteBlockedErrorBody(tunnel.Config))
		return
	}

	if err := s.deleteManagedTunnel(client, tunnelName); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_delete_failed", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func tunnelDeleteBlockedErrorBody(config protocol.ProxyConfig) map[string]any {
	message := "This tunnel cannot be deleted right now"
	if config.DesiredState == protocol.ProxyDesiredStateRunning && config.RuntimeState == protocol.ProxyRuntimeStateExposed {
		message = "Stop the tunnel before deleting it"
	} else if config.RuntimeState == protocol.ProxyRuntimeStatePending {
		message = "Tunnel is still processing. Try deleting it later"
	}
	return map[string]any{
		"error":      message,
		"message":    message,
		"code":       protocol.TunnelMutationErrorCodeTunnelBusy,
		"error_code": protocol.TunnelMutationErrorCodeTunnelBusy,
	}
}

func (s *Server) handleUpdateTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelSelector := tunnelSelectorFromPath(r)

	var req struct {
		Name       string `json:"name"`
		LocalIP    string `json:"local_ip"`
		LocalPort  int    `json:"local_port"`
		RemotePort int    `json:"remote_port"`
		Domain     string `json:"domain"`
		IngressBPS int64  `json:"ingress_bps"`
		EgressBPS  int64  `json:"egress_bps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		updated, err := s.updateOfflineManagedTunnel(clientID, tunnelSelector, req.Name, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain, req.IngressBPS, req.EgressBPS)
		if err != nil {
			status, payload := tunnelMutationErrorStatusAndBody(err)
			encodeJSON(w, status, payload)
			return
		}
		updated = proxyConfigForClientView(updated, false)

		encodeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"message": "tunnel configuration updated",
			"tunnel":  updated,
		})
		return
	}

	_, tunnel, exists := findTunnelBySelector(client, tunnelSelector)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
		return
	}

	if !canEditOrDeleteLiveTunnel(tunnel.Config) {
		message := fmt.Sprintf("tunnel is currently in state %s/%s; only stopped/idle or running/error tunnels can be edited", tunnel.Config.DesiredState, tunnel.Config.RuntimeState)
		encodeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   message,
			"message": message,
			"code":    protocol.TunnelMutationErrorCodeTunnelBusy,
		})
		return
	}

	updated, err := s.updateManagedTunnel(client, tunnelSelector, req.Name, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain, req.IngressBPS, req.EgressBPS)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	updated = proxyConfigForClientView(updated, true)

	encodeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "tunnel configuration updated",
		"tunnel":  updated,
	})
}
