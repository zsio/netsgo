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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
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
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if s.auth.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	user, err := s.auth.adminStore.ValidateAdminPassword(req.Username, req.Password)
	if err != nil {
		if s.auth.loginLimiter != nil {
			s.auth.loginLimiter.RecordFailure(ip)
		}
		http.Error(w, `{"error":"username or password incorrect"}`, http.StatusUnauthorized)
		return
	}

	// create session (automatically invalidates old sessions → single active session per user)
	session, err := s.auth.adminStore.CreateSession(user.ID, user.Username, user.Role, r.RemoteAddr, r.UserAgent())
	if err != nil {
		http.Error(w, `{"error":"failed to persist session"}`, http.StatusInternalServerError)
		return
	}

	token, err := s.GenerateAdminToken(session)
	if err != nil {
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	info := GetSessionFromContext(r.Context())
	if info == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusUnauthorized)
		return
	}

	if err := s.auth.adminStore.DeleteSession(info.SessionID); err != nil {
		http.Error(w, `{"error":"failed to persist logout"}`, http.StatusInternalServerError)
		return
	}
	slog.Info("Admin user logged out", "user", info.Username, "module", "auth")

	s.clearSessionCookie(w, r)

	encodeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ========= API Keys =========

func (s *Server) handleAPIAdminKeys(w http.ResponseWriter, r *http.Request) {
	if s.auth.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
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
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}

		// parse expiry duration
		var expiresAt *time.Time
		if req.ExpiresIn != "" && req.ExpiresIn != "0" {
			d, err := time.ParseDuration(req.ExpiresIn)
			if err != nil {
				http.Error(w, `{"error":"invalid expires_in format"}`, http.StatusBadRequest)
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
				"error": err.Error(),
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
			serverAddr = s.auth.adminStore.GetServerConfig().ServerAddr
		}

		// return the full response including the raw key (only visible at creation time!)
		encodeJSON(w, http.StatusCreated, map[string]any{
			"key":         sanitizeAPIKey(*key),
			"raw_key":     rawKey, // tell the frontend to display this to the user
			"server_addr": serverAddr,
		})

	default:
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIAdminKeyItem(w http.ResponseWriter, r *http.Request) {
	if s.auth.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
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
			http.Error(w, `{"error":"unknown action"}`, http.StatusNotFound)
			return
		}

		if err := s.auth.adminStore.SetAPIKeyActive(keyID, active); err != nil {
			http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
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
			http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
			return
		}

		slog.Info("API Key deleted", "key_id", keyID, "module", "admin")
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// ========= Server Config =========

func (s *Server) handleAPIAdminConfig(w http.ResponseWriter, r *http.Request) {
	if s.auth.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		config := s.auth.adminStore.GetServerConfig()
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
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}

		current := s.auth.adminStore.GetServerConfig()

		normalizedServerAddr, err := normalizeServerAddrForConfigUpdate(config.ServerAddr, current.ServerAddr)
		if err != nil {
			encodeJSON(w, http.StatusBadRequest, map[string]any{
				"error": err.Error(),
			})
			return
		}
		config.ServerAddr = normalizedServerAddr

		// validate port range
		for _, pr := range config.AllowedPorts {
			if pr.Start < 1 || pr.End > 65535 || pr.Start > pr.End {
				encodeJSON(w, http.StatusBadRequest, map[string]any{
					"error": "invalid port range: start must be >= 1, end must be <= 65535, and start <= end",
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
		affected := s.findTunnelsAffectedByPortChange(config.AllowedPorts)
		conflicts := conflictingHTTPDomainsForServerAddr(config.ServerAddr, s)

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
				ServerAddrLocked:       true,
				AffectedTunnels:        affected,
				ConflictingHTTPTunnels: conflicts,
			})
			return
		}

		if len(conflicts) > 0 {
			encodeJSON(w, http.StatusConflict, adminConfigUpdateResponse{
				Error:                  "server_addr conflicts with existing HTTP tunnel domains",
				AffectedTunnels:        affected,
				ConflictingHTTPTunnels: conflicts,
			})
			return
		}

		// save config
		if err := s.auth.adminStore.UpdateServerConfig(config); err != nil {
			http.Error(w, `{"error":"failed to update config"}`, http.StatusInternalServerError)
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
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
	}
}
