package server

import (
	"encoding/json"
	"net/http"
	"strconv"
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

// ========= Setup API (初始化) =========

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	initialized := false
	if s.adminStore != nil {
		initialized = s.adminStore.IsInitialized()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"initialized":          initialized,
		"setup_token_required": !initialized && s.setupToken != "", // P8: 告知前端是否需要 Setup Token
	})
}

func (s *Server) handleSetupInit(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	// 速率限制检查
	ip := clientIP(r)
	if s.setupLimiter != nil {
		if allowed, retryAfter := s.setupLimiter.Allow(ip); !allowed {
			s.adminStore.AddSystemLog("WARN", "初始化接口被限速: IP="+ip, "security")
			writeRateLimitResponse(w, retryAfter)
			return
		}
	}

	// 直接尝试初始化，由 Initialize 内部的互斥锁保证原子性和幂等性
	// 不做前置检查以避免 TOCTOU 竞态
	var req struct {
		Admin struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"admin"`
		ServerAddr   string      `json:"server_addr"`
		AllowedPorts []PortRange `json:"allowed_ports"`
		SetupToken   string      `json:"setup_token"` // P8: 一次性初始化令牌
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// 基本校验
	if req.Admin.Username == "" {
		http.Error(w, `{"error":"用户名不能为空"}`, http.StatusBadRequest)
		return
	}

	// P8: 校验 Setup Token
	if s.setupToken != "" {
		if req.SetupToken != s.setupToken {
			if s.setupLimiter != nil {
				s.setupLimiter.RecordFailure(ip)
			}
			s.adminStore.AddSystemLog("WARN", "Setup Token 验证失败: IP="+ip, "security")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{
				"error": "Setup Token 无效，请检查服务端控制台输出",
			})
			return
		}
	}

	// 执行初始化（内部持有锁，重复调用会返回错误）
	if err := s.adminStore.Initialize(req.Admin.Username, req.Admin.Password, req.ServerAddr, req.AllowedPorts); err != nil {
		w.Header().Set("Content-Type", "application/json")
		// 区分"已初始化"和其他错误（如密码不合规）
		if s.adminStore.IsInitialized() {
			w.WriteHeader(http.StatusForbidden)
		} else {
			w.WriteHeader(http.StatusBadRequest)
			if s.setupLimiter != nil {
				s.setupLimiter.RecordFailure(ip)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"error": err.Error(),
		})
		return
	}

	// 验证新建的管理员并创建 session
	user, err := s.adminStore.ValidateAdminPassword(req.Admin.Username, req.Admin.Password)
	if err != nil {
		http.Error(w, `{"error":"internal error: cannot validate newly created admin"}`, http.StatusInternalServerError)
		return
	}

	session := s.adminStore.CreateSession(user.ID, user.Username, user.Role, r.RemoteAddr, r.UserAgent())
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	// 自动生成首个 Agent Key
	rawKey := "sk-" + generateUUID()
	agentKey, err := s.adminStore.AddAPIKey("default", rawKey, []string{"connect"}, nil)
	if err != nil {
		// Key 创建失败不阻塞初始化
		rawKey = ""
		agentKey = nil
	}

	s.adminStore.AddSystemLog("INFO", "服务初始化完成，管理员: "+req.Admin.Username, "setup")
	s.setupToken = "" // P8: 初始化成功，清空一次性 Token

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	resp := map[string]any{
		"success": true,
		"token":   token,
		"message": "初始化成功",
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"role":     user.Role,
		},
	}
	if agentKey != nil {
		resp["agent_key"] = map[string]any{
			"name":    agentKey.Name,
			"raw_key": rawKey,
		}
	}
	json.NewEncoder(w).Encode(resp)
}

// ========= Auth API =========

func (s *Server) handleAPILogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 速率限制检查
	ip := clientIP(r)
	if s.loginLimiter != nil {
		if allowed, retryAfter := s.loginLimiter.Allow(ip); !allowed {
			if s.adminStore != nil {
				s.adminStore.AddSystemLog("WARN", "登录接口被限速: IP="+ip, "security")
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

	if s.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	user, err := s.adminStore.ValidateAdminPassword(req.Username, req.Password)
	if err != nil {
		if s.loginLimiter != nil {
			s.loginLimiter.RecordFailure(ip)
		}
		http.Error(w, `{"error":"username or password incorrect"}`, http.StatusUnauthorized)
		return
	}

	// 创建 session（会自动踢出旧 session → 单端登录）
	session := s.adminStore.CreateSession(user.ID, user.Username, user.Role, r.RemoteAddr, r.UserAgent())

	token, err := s.GenerateAdminToken(session)
	if err != nil {
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	s.adminStore.UpdateAdminLoginTime(user.ID)
	s.adminStore.AddSystemLog("INFO", "Admin user logged in: "+user.Username, "auth")
	if s.loginLimiter != nil {
		s.loginLimiter.ResetFailures(ip)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
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

	s.adminStore.DeleteSession(info.SessionID)
	s.adminStore.AddSystemLog("INFO", "Admin user logged out: "+info.Username, "auth")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// ========= API Keys =========

func (s *Server) handleAPIAdminKeys(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		keys := s.adminStore.GetAPIKeys()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sanitizeAPIKeys(keys))

	case http.MethodPost:
		var req struct {
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
			MaxUses     int      `json:"max_uses"`   // 0 = 无限制
			ExpiresIn   string   `json:"expires_in"` // "1h","3h","24h","168h","" 或 "0" 表示不限制
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}

		// 解析过期时间
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

		// 后端生成一个随机字符串作为原始 key
		rawKey := "sk-" + generateUUID()
		key, err := s.adminStore.AddAPIKey(req.Name, rawKey, req.Permissions, expiresAt)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"error": err.Error(),
			})
			return
		}

		// 设置 MaxUses
		if req.MaxUses > 0 {
			if err := s.adminStore.SetAPIKeyMaxUses(key.ID, req.MaxUses); err != nil {
				s.adminStore.AddSystemLog("WARN", "Failed to set max_uses for key: "+key.ID, "admin")
			}
			key.MaxUses = req.MaxUses
		}

		s.adminStore.AddSystemLog("INFO", "Created new API Key: "+req.Name, "admin")

		// 获取 server_addr
		serverAddr := ""
		if s.adminStore != nil {
			serverAddr = s.adminStore.GetServerConfig().ServerAddr
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// 返回包含了原始 key 的完整响应 (仅创建时可见！)
		json.NewEncoder(w).Encode(map[string]any{
			"key":         sanitizeAPIKey(*key),
			"raw_key":     rawKey, // 告诉前端显示给用户
			"server_addr": serverAddr,
		})

	default:
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIAdminKeyItem(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
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

		if err := s.adminStore.SetAPIKeyActive(keyID, active); err != nil {
			http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
			return
		}

		actionText := "disabled"
		if active {
			actionText = "enabled"
		}
		s.adminStore.AddSystemLog("INFO", "API Key "+actionText+": "+keyID, "admin")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"success": true})

	case http.MethodDelete:
		if err := s.adminStore.DeleteAPIKey(keyID); err != nil {
			http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
			return
		}

		s.adminStore.AddSystemLog("INFO", "API Key deleted: "+keyID, "admin")
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// ========= Server Config =========

func (s *Server) handleAPIAdminConfig(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		config := s.adminStore.GetServerConfig()
		if config.AllowedPorts == nil {
			config.AllowedPorts = []PortRange{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)

	case http.MethodPut:
		var config ServerConfig
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}

		// 校验端口范围合法性
		for _, pr := range config.AllowedPorts {
			if pr.Start < 1 || pr.End > 65535 || pr.Start > pr.End {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{
					"error": "端口范围无效: start 必须 >= 1, end 必须 <= 65535, 且 start <= end",
				})
				return
			}
		}
		if config.AllowedPorts == nil {
			config.AllowedPorts = []PortRange{}
		}

		if err := s.adminStore.UpdateServerConfig(config); err != nil {
			http.Error(w, `{"error":"failed to update config"}`, http.StatusInternalServerError)
			return
		}

		info := GetSessionFromContext(r.Context())
		adminName := "unknown"
		if info != nil {
			adminName = info.Username
		}
		s.adminStore.AddSystemLog("INFO", "Server config updated by "+adminName, "admin")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"success": true})

	default:
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// ========= Tunnel Policies =========

func (s *Server) handleAPIAdminPolicies(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	if r.Method == http.MethodGet {
		policy := s.adminStore.GetTunnelPolicy()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(policy)
		return
	}

	if r.Method == http.MethodPut {
		var policy TunnelPolicy
		if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if err := s.adminStore.UpdateTunnelPolicy(policy); err != nil {
			http.Error(w, `{"error":"failed to update policy"}`, http.StatusInternalServerError)
			return
		}

		info := GetSessionFromContext(r.Context())
		adminName := "unknown"
		if info != nil {
			adminName = info.Username
		}
		s.adminStore.AddSystemLog("INFO", "Tunnel policy updated by "+adminName, "admin")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"success": true})
		return
	}

	http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
}

// ========= Action Logs and Events =========

func (s *Server) handleAPIAdminLogs(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}

	logs := s.adminStore.GetSystemLogs(limit)
	w.Header().Set("Content-Type", "application/json")
	if logs == nil {
		logs = []SystemLogEntry{}
	}
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleAPIAdminEvents(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}

	events := s.adminStore.GetEvents(limit)
	w.Header().Set("Content-Type", "application/json")
	if events == nil {
		events = []EventRecord{}
	}
	json.NewEncoder(w).Encode(events)
}
