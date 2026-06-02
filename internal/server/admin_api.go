package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type apiKeyResponse struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Permissions []string   `json:"permissions"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IsActive    bool       `json:"is_active"`
	MaxUses     int        `json:"max_uses"`
	UseCount    int        `json:"use_count"`
}

func sanitizeAPIKey(key APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID:          key.ID,
		Name:        key.Name,
		Permissions: append([]string(nil), key.Permissions...),
		CreatedAt:   key.CreatedAt,
		ExpiresAt:   key.ExpiresAt,
		IsActive:    key.IsActive,
		MaxUses:     key.MaxUses,
		UseCount:    key.UseCount,
	}
}

func sanitizeAPIKeys(keys []APIKey) []apiKeyResponse {
	if len(keys) == 0 {
		return []apiKeyResponse{}
	}

	result := make([]apiKeyResponse, 0, len(keys))
	for _, key := range keys {
		result = append(result, sanitizeAPIKey(key))
	}
	return result
}

// ========= Auth API =========

func (s *Server) handleAPILogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	// rate limit check
	ip := s.clientIP(r)
	if s.auth.loginLimiter != nil {
		if allowed, retryAfter := s.auth.loginLimiter.Allow(ip); !allowed {
			if s.auth.adminStore != nil {
				slog.Warn("Login endpoint rate limited", "ip", ip, "module", "security")
			}
			writeRateLimitResponse(w, retryAfter)
			return
		}
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if s.auth.adminStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_store_unavailable", "admin store not initialized")
		return
	}

	user, err := s.auth.adminStore.ValidateAdminPassword(req.Username, req.Password)
	if err != nil {
		if s.auth.loginLimiter != nil {
			s.auth.loginLimiter.RecordFailure(ip)
		}
		writeAPIError(w, http.StatusUnauthorized, "username_or_password_incorrect", "username or password incorrect")
		return
	}

	// create session (automatically invalidates old sessions → single active session per user)
	session, err := s.auth.adminStore.CreateSession(user.ID, user.Username, user.Role, r.RemoteAddr, r.UserAgent())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session_persist_failed", "failed to persist session")
		return
	}

	token, err := s.GenerateAdminToken(session)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "token_generate_failed", "failed to generate token")
		return
	}

	slog.Info("Admin user logged in", "user", user.Username, "module", "auth")
	if s.auth.loginLimiter != nil {
		s.auth.loginLimiter.ResetFailures(ip)
	}

	s.setSessionCookie(w, r, token, int(sessionDefaultTTL.Seconds()))

	encodeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"role":     user.Role,
		},
	})
}

func (s *Server) handleAPILogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	info := GetSessionFromContext(r.Context())
	if info == nil {
		writeAPIError(w, http.StatusUnauthorized, "session_not_found", "session not found")
		return
	}

	if err := s.auth.adminStore.DeleteSession(info.SessionID); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "logout_persist_failed", "failed to persist logout")
		return
	}
	slog.Info("Admin user logged out", "user", info.Username, "module", "auth")

	s.clearSessionCookie(w, r)

	encodeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ========= API Keys =========

func (s *Server) handleAPIAdminKeys(w http.ResponseWriter, r *http.Request) {
	if s.auth.adminStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_store_unavailable", "admin store not initialized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		keys := s.auth.adminStore.GetAPIKeys()
		encodeJSON(w, http.StatusOK, sanitizeAPIKeys(keys))

	case http.MethodPost:
		var req struct {
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
			MaxUses     int      `json:"max_uses"`   // 0 = unlimited
			ExpiresIn   string   `json:"expires_in"` // "1h","3h","24h","168h","" or "0" means no expiry
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid body")
			return
		}

		// parse expiry duration
		var expiresAt *time.Time
		if req.ExpiresIn != "" && req.ExpiresIn != "0" {
			d, err := time.ParseDuration(req.ExpiresIn)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_expires_in", "invalid expires_in format")
				return
			}
			t := time.Now().Add(d)
			expiresAt = &t
		}

		// generate a random string as the raw key on the server side
		rawKey := "sk-" + generateUUID()
		key, err := s.auth.adminStore.AddAPIKey(req.Name, rawKey, req.Permissions, expiresAt)
		if err != nil {
			encodeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   err.Error(),
				"message": err.Error(),
				"code":    "api_key_create_failed",
			})
			return
		}

		// set MaxUses
		if req.MaxUses > 0 {
			if err := s.auth.adminStore.SetAPIKeyMaxUses(key.ID, req.MaxUses); err != nil {
				slog.Warn("Failed to set max_uses for key", "key_id", key.ID, "module", "admin")
			}
			key.MaxUses = req.MaxUses
		}

		slog.Info("Created new API Key", "name", req.Name, "module", "admin")

		// get server_addr
		serverAddr := ""
		if s.auth.adminStore != nil {
			// Best-effort response enrichment only: API key creation already
			// succeeded, so config read failure should not roll it back.
			serverAddr = s.auth.adminStore.GetServerConfig().ServerAddr
		}

		// return the full response including the raw key (only visible at creation time!)
		encodeJSON(w, http.StatusCreated, map[string]any{
			"key":         sanitizeAPIKey(*key),
			"raw_key":     rawKey, // tell the frontend to display this to the user
			"server_addr": serverAddr,
		})

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "not allowed")
	}
}

func (s *Server) handleAPIAdminKeyItem(w http.ResponseWriter, r *http.Request) {
	if s.auth.adminStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_store_unavailable", "admin store not initialized")
		return
	}

	keyID := r.PathValue("id")
	action := r.PathValue("action")

	switch r.Method {
	case http.MethodPut:
		var active bool
		switch action {
		case "enable":
			active = true
		case "disable":
			active = false
		default:
			writeAPIError(w, http.StatusNotFound, "unknown_action", "unknown action")
			return
		}

		if err := s.auth.adminStore.SetAPIKeyActive(keyID, active); err != nil {
			writeAPIError(w, http.StatusNotFound, "api_key_not_found", "key not found")
			return
		}

		actionText := "disabled"
		if active {
			actionText = "enabled"
		}
		slog.Info("API Key status changed", "action", actionText, "key_id", keyID, "module", "admin")

		encodeJSON(w, http.StatusOK, map[string]any{"success": true})

	case http.MethodDelete:
		if err := s.auth.adminStore.DeleteAPIKey(keyID); err != nil {
			writeAPIError(w, http.StatusNotFound, "api_key_not_found", "key not found")
			return
		}

		slog.Info("API Key deleted", "key_id", keyID, "module", "admin")
		w.WriteHeader(http.StatusNoContent)

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "not allowed")
	}
}

// ========= Server Config =========

func (s *Server) handleAPIAdminConfig(w http.ResponseWriter, r *http.Request) {
	if s.auth.adminStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_store_unavailable", "admin store not initialized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		config, err := s.auth.adminStore.GetServerConfigE()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "config_load_failed", "failed to load config")
			return
		}
		if config.AllowedPorts == nil {
			config.AllowedPorts = []PortRange{}
		}
		encodeJSON(w, http.StatusOK, adminConfigResponse{
			ServerAddr:          config.ServerAddr,
			AllowedPorts:        config.AllowedPorts,
			EffectiveServerAddr: effectiveManagementHost(&config, serverListenAddr(s)),
			ServerAddrLocked:    isServerAddrLocked(),
		})

	case http.MethodPut:
		var config ServerConfig
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid body")
			return
		}

		current, err := s.auth.adminStore.GetServerConfigE()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "config_load_failed", "failed to load config")
			return
		}

		normalizedServerAddr, err := normalizeServerAddrForConfigUpdate(config.ServerAddr, current.ServerAddr)
		if err != nil {
			encodeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   err.Error(),
				"message": err.Error(),
				"code":    "invalid_server_addr",
			})
			return
		}
		config.ServerAddr = normalizedServerAddr

		// validate port range
		for _, pr := range config.AllowedPorts {
			if pr.Start < 1 || pr.End > 65535 || pr.Start > pr.End {
				encodeJSON(w, http.StatusBadRequest, map[string]any{
					"error":   "invalid port range: start must be >= 1, end must be <= 65535, and start <= end",
					"message": "invalid port range: start must be >= 1, end must be <= 65535, and start <= end",
					"code":    "invalid_port_range",
				})
				return
			}
		}
		if config.AllowedPorts == nil {
			config.AllowedPorts = []PortRange{}
		}

		currentServerAddr := strings.TrimSpace(current.ServerAddr)
		if normalizedCurrent, err := validateServerAddr(current.ServerAddr); err == nil {
			currentServerAddr = normalizedCurrent
		}
		// check affected tunnels (when new allowlist is non-empty)
		affected, err := s.findTunnelsAffectedByPortChange(config.AllowedPorts)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "affected_tunnels_check_failed", "failed to check affected tunnels")
			return
		}
		conflicts, err := conflictingHTTPDomainsForServerAddr(config.ServerAddr, s)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "domain_conflict_check_failed", "failed to check domain conflicts")
			return
		}

		// dry_run mode: preview affected tunnels without saving
		if r.URL.Query().Get("dry_run") == "true" {
			encodeJSON(w, http.StatusOK, adminConfigUpdateResponse{
				AffectedTunnels:        affected,
				ConflictingHTTPTunnels: conflicts,
			})
			return
		}

		// when locked by environment variable, only allow saving server_addr that matches the persisted value.
		if isServerAddrLocked() && config.ServerAddr != currentServerAddr {
			encodeJSON(w, http.StatusConflict, adminConfigUpdateResponse{
				Error:                  "server_addr is locked by the NETSGO_SERVER_ADDR environment variable",
				Message:                "server_addr is locked by the NETSGO_SERVER_ADDR environment variable",
				Code:                   "server_addr_locked",
				ServerAddrLocked:       true,
				AffectedTunnels:        affected,
				ConflictingHTTPTunnels: conflicts,
			})
			return
		}

		if len(conflicts) > 0 {
			encodeJSON(w, http.StatusConflict, adminConfigUpdateResponse{
				Error:                  "server_addr conflicts with existing HTTP tunnel domains",
				Message:                "server_addr conflicts with existing HTTP tunnel domains",
				Code:                   "server_addr_conflict",
				AffectedTunnels:        affected,
				ConflictingHTTPTunnels: conflicts,
			})
			return
		}

		// save config
		if err := s.auth.adminStore.UpdateServerConfig(config); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "config_update_failed", "failed to update config")
			return
		}

		info := GetSessionFromContext(r.Context())
		adminName := "unknown"
		if info != nil {
			adminName = info.Username
		}
		slog.Info("Server config updated", "admin", adminName, "module", "admin")

		// mark affected runtime tunnels as error
		if len(affected) > 0 {
			s.markTunnelsPortNotAllowed(affected)
			slog.Warn("Port allowlist change caused tunnels to be marked as errored",
				"affected_count", len(affected), "module", "admin")
		}

		encodeJSON(w, http.StatusOK, adminConfigUpdateResponse{
			Success:                true,
			AffectedTunnels:        affected,
			ConflictingHTTPTunnels: conflicts,
		})

	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "not allowed")
	}
}
