package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ========= Setup API (初始化) =========

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	initialized := false
	if s.adminStore != nil {
		initialized = s.adminStore.IsInitialized()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"initialized": initialized,
	})
}

func (s *Server) handleSetupInit(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
		return
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

	// 执行初始化（内部持有锁，重复调用会返回错误）
	if err := s.adminStore.Initialize(req.Admin.Username, req.Admin.Password, req.ServerAddr, req.AllowedPorts); err != nil {
		w.Header().Set("Content-Type", "application/json")
		// 区分"已初始化"和其他错误（如密码不合规）
		if s.adminStore.IsInitialized() {
			w.WriteHeader(http.StatusForbidden)
		} else {
			w.WriteHeader(http.StatusBadRequest)
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
		if keys == nil {
			keys = []APIKey{}
		}
		json.NewEncoder(w).Encode(keys)

	case http.MethodPost:
		var req struct {
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}

		// 后端生成一个随机字符串作为原始 key
		rawKey := "sk-" + generateUUID()
		key, err := s.adminStore.AddAPIKey(req.Name, rawKey, req.Permissions, nil)
		if err != nil {
			http.Error(w, `{"error":"failed to create key"}`, http.StatusInternalServerError)
			return
		}

		s.adminStore.AddSystemLog("INFO", "Created new API Key: "+req.Name, "admin")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// 返回包含了原始 key 的完整响应 (仅创建时可见！)
		json.NewEncoder(w).Encode(map[string]any{
			"key":     key,
			"raw_key": rawKey, // 告诉前端显示给用户
		})

	default:
		http.Error(w, `{"error":"not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// 修改/删除 Key （省略详细实现，重点先搭建主流程，这里留作扩展）
func (s *Server) handleAPIAdminKeyItem(w http.ResponseWriter, r *http.Request) {
    // 处理 PUT (禁用/启用), DELETE
    http.Error(w, `{"error":"not implemented"}`, http.StatusNotImplemented)
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
		s.adminStore.AddSystemLog("INFO", "Tunnel policy updated by " + adminName, "admin")
		
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
