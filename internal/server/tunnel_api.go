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
		payload.Error = "隧道不存在"
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
		"message":     "代理隧道创建成功",
		"remote_port": config.RemotePort,
	})
}

func (s *Server) handlePauseTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	client, ok := s.loadLiveClient(clientID)
	if !ok {
		_, err := s.pauseOfflineManagedTunnel(clientID, tunnelName)
		if err != nil {
			switch {
			case errors.Is(err, errManagedTunnelClientNotFound):
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "client not found"})
			case errors.Is(err, errManagedTunnelNotFound):
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "隧道不存在"})
			case err.Error() == "只有 active 状态的隧道才能暂停":
				encodeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			default:
				encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}

		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "隧道已暂停"})
		return
	}

	client.proxyMu.RLock()
	tunnel, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}
	if !canPauseTunnel(tunnel.Config) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "只有 running/exposed 状态的隧道才能暂停"})
		return
	}

	if err := s.pauseManagedTunnel(client, tunnelName); err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "隧道已暂停"})
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
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "隧道不存在"})
			case err.Error() == "只有 paused、stopped 或 error 状态的隧道才能恢复":
				encodeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			default:
				encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}

		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "隧道已恢复"})
		return
	}

	client.proxyMu.RLock()
	tunnel, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}
	if !canResumeTunnel(tunnel.Config) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "只有 paused/idle、stopped/idle 或 running/error 状态的隧道才能恢复"})
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

	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "隧道已恢复"})
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
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "隧道不存在"})
			default:
				encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}

		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "隧道已停止"})
		return
	}

	client.proxyMu.RLock()
	_, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}

	if err := s.stopManagedTunnel(client, tunnelName); err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "隧道已停止"})
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
				encodeJSON(w, http.StatusNotFound, map[string]any{"error": "隧道不存在"})
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
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}

	if !canEditOrDeleteLiveTunnel(tunnel.Config) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("隧道当前状态为 %s/%s，只有 paused/idle、stopped/idle 或 running/error 状态才能删除", tunnel.Config.DesiredState, tunnel.Config.RuntimeState),
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
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体无效"})
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
			"message": "隧道配置已更新",
			"tunnel":  updated,
		})
		return
	}

	client.proxyMu.RLock()
	tunnel, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": "隧道不存在"})
		return
	}

	if !canEditOrDeleteLiveTunnel(tunnel.Config) {
		encodeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("隧道当前状态为 %s/%s，只有 paused/idle、stopped/idle 或 running/error 状态才能编辑", tunnel.Config.DesiredState, tunnel.Config.RuntimeState),
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
		"message": "隧道配置已更新",
		"tunnel":  updated,
	})
}
