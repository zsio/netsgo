package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

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

	token, err := GenerateAdminToken(user)
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
		
		claims := GetAdminFromContext(r.Context())
		adminName := "unknown"
		if claims != nil {
		    adminName = claims.Username
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
