package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"netsgo/pkg/protocol"
)

func (s *Server) handleUpdateDisplayName(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	if clientID == "" {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing client id"})
		return
	}

	var req struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	if s.auth.adminStore == nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": "admin store unavailable"})
		return
	}

	if err := s.auth.adminStore.UpdateClientDisplayName(clientID, req.DisplayName); err != nil {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"display_name": req.DisplayName,
	})
}

func tunnelMutationErrorStatusAndBody(err error) (int, tunnelMutationErrorResponse) {
	status := http.StatusInternalServerError
	payload := tunnelMutationErrorResponse{
		Success: false,
		Error:   err.Error(),
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
		payload.Field = ruleErr.Field()
		payload.ConflictingTunnels = ruleErr.ConflictingTunnels()
	case errors.As(err, &validationErr):
		status = validationErr.StatusCode()
		payload.ErrorCode = validationErr.ErrorCode()
		payload.Field = validationErr.Field()
	}

	return status, payload
}

func (s *Server) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")

	var req protocol.ProxyNewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

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

func (s *Server) handlePauseTunnel(w http.ResponseWriter, r *http.Request) {
	s.handleStopTunnel(w, r)
}

func (s *Server) handleResumeTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		if _, err := s.resumeOfflineManagedTunnel(clientID, tunnelName); err != nil {
			switch {
			case errors.Is(err, errManagedTunnelClientNotFound):
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "client not found"})
			case errors.Is(err, errManagedTunnelNotFound):
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
			case err.Error() == "only stopped or error tunnels can be resumed":
				encodeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			default:
				encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}

		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel resumed"})
		return
	}

	client.proxyMu.RLock()
	tunnel, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "tunnel not found"})
		return
	}
	if !canResumeTunnel(tunnel.Config) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "only stopped/idle or running/error tunnels can be resumed"})
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
		encodeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel resumed"})
}

func (s *Server) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		if _, err := s.stopOfflineManagedTunnel(clientID, tunnelName); err != nil {
			switch {
			case errors.Is(err, errManagedTunnelClientNotFound):
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "client not found"})
			case errors.Is(err, errManagedTunnelNotFound):
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
			default:
				encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}

		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel stopped"})
		return
	}

	client.proxyMu.RLock()
	_, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "tunnel not found"})
		return
	}

	if err := s.stopManagedTunnel(client, tunnelName); err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel stopped"})
}

func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		if err := s.deleteOfflineManagedTunnel(clientID, tunnelName); err != nil {
			switch {
			case errors.Is(err, errManagedTunnelClientNotFound):
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "client not found"})
			case errors.Is(err, errManagedTunnelNotFound):
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
			default:
				encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}

	client.proxyMu.RLock()
	tunnel, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "tunnel not found"})
		return
	}

	if !canEditOrDeleteLiveTunnel(tunnel.Config) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("tunnel is currently in state %s/%s; only paused/idle, stopped/idle, or running/error tunnels can be deleted", tunnel.Config.DesiredState, tunnel.Config.RuntimeState),
		})
		return
	}

	if err := s.deleteManagedTunnel(client, tunnelName); err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	var req struct {
		LocalIP    string `json:"local_ip"`
		LocalPort  int    `json:"local_port"`
		RemotePort int    `json:"remote_port"`
		Domain     string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		updated, err := s.updateOfflineManagedTunnel(clientID, tunnelName, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain)
		if err != nil {
			status, payload := tunnelMutationErrorStatusAndBody(err)
			encodeJSON(w, status, payload)
			return
		}

		encodeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"message": "tunnel configuration updated",
			"tunnel":  updated,
		})
		return
	}

	client.proxyMu.RLock()
	tunnel, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
		return
	}

	if !canEditOrDeleteLiveTunnel(tunnel.Config) {
		encodeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("tunnel is currently in state %s/%s; only paused/idle, stopped/idle, or running/error tunnels can be edited", tunnel.Config.DesiredState, tunnel.Config.RuntimeState),
		})
		return
	}

	updated, err := s.updateManagedTunnel(client, tunnelName, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "tunnel configuration updated",
		"tunnel":  updated,
	})
}
