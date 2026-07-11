package server

import (
	"errors"
	"fmt"
	"net/http"

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
	if err := decodeJSONRequestBody(r, &req); err != nil {
		writeJSONRequestDecodeError(w, err)
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
	if err := decodeJSONRequestBody(r, &req); err != nil {
		writeJSONRequestDecodeError(w, err)
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
	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
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
		deletedTunnels, err := s.store.DeleteTunnelsByClientIDReturningDeleted(clientID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "client_tunnels_delete_failed", err.Error())
			return
		}
		for _, tunnel := range deletedTunnels {
			s.unifiedRuntime.purgeTunnelIssues(tunnel.ID, tunnel.Revision)
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
	case errors.Is(err, errStoredTunnelClientNotFound):
		status = http.StatusNotFound
		payload.Error = "client not found"
	case errors.Is(err, errStoredTunnelNotFound):
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
