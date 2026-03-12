package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"netsgo/pkg/protocol"
)

// Server 是服务端的核心结构体
type Server struct {
	Port      int
	StorePath string       // 隧道配置文件路径（空则使用默认）
	agents    sync.Map     // agentID -> *AgentConn
	events    *EventBus    // SSE 事件总线
	store      *TunnelStore // 隧道持久化存储
	startTime  time.Time    // 服务器启动时间
	adminStore *AdminStore  // 系统管理后台数据存储
}

// AgentConn 代表一个已连接的 Agent
type AgentConn struct {
	ID          string
	Info        protocol.AgentInfo
	stats       *protocol.SystemStats
	statsMu     sync.RWMutex             // 保护 stats
	conn        *websocket.Conn // 控制通道
	mu          sync.Mutex
	dataSession *yamux.Session           // 数据通道 yamux Session
	dataMu      sync.RWMutex             // 保护 dataSession
	proxies     map[string]*ProxyTunnel  // 代理隧道 name -> tunnel
	proxyMu     sync.RWMutex             // 保护 proxies
}

// SetStats 安全地更新探针数据
func (a *AgentConn) SetStats(s *protocol.SystemStats) {
	a.statsMu.Lock()
	a.stats = s
	a.statsMu.Unlock()
}

// GetStats 安全地读取探针数据
func (a *AgentConn) GetStats() *protocol.SystemStats {
	a.statsMu.RLock()
	defer a.statsMu.RUnlock()
	return a.stats
}

// New 创建一个新的 Server 实例
func New(port int) *Server {
	return &Server{
		Port:      port,
		events:    NewEventBus(),
		startTime: time.Now(),
	}
}

// initStore 初始化持久化存储
func (s *Server) initStore() error {
	path := s.StorePath
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".netsgo", "tunnels.json")
	}
	store, err := NewTunnelStore(path)
	if err != nil {
		return err
	}
	s.store = store
	log.Printf("📦 隧道配置存储: %s", path)

	adminPath := s.StorePath
	if adminPath == "" {
		home, _ := os.UserHomeDir()
		adminPath = filepath.Join(home, ".netsgo", "admin.json")
	} else {
		adminPath = filepath.Join(filepath.Dir(s.StorePath), "admin.json")
	}
	adminStore, err := NewAdminStore(adminPath)
	if err != nil {
		return err
	}
	s.adminStore = adminStore
	log.Printf("📦 系统管理存储: %s", adminPath)

	return nil
}

// RangeAgents 遍历所有已连接的 Agent
func (s *Server) RangeAgents(fn func(id string, agent *AgentConn) bool) {
	s.agents.Range(func(key, value any) bool {
		return fn(key.(string), value.(*AgentConn))
	})
}

// RangeProxies 遍历 Agent 的所有代理隧道
func (a *AgentConn) RangeProxies(fn func(name string, tunnel *ProxyTunnel) bool) {
	a.proxyMu.RLock()
	defer a.proxyMu.RUnlock()
	for name, tunnel := range a.proxies {
		if !fn(name, tunnel) {
			return
		}
	}
}

// Start 启动服务端，单端口同时处理 HTTP/WebSocket 和数据通道。
// 通过 peek 首字节区分：HTTP 请求 vs 数据通道魔数 (0x4E)。
func (s *Server) Start() error {
	s.startTime = time.Now()

	// 启动持久化事件循环
	go s.persistEventsLoop()

	// 初始化隧道持久化存储
	if err := s.initStore(); err != nil {
		return fmt.Errorf("初始化隧道存储失败: %w", err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", s.Port, err)
	}

	addr := ln.Addr().(*net.TCPAddr)
	s.Port = addr.Port // 更新为实际端口（当 Port=0 时有用）

	log.Printf("🚀 NetsGo Server 已启动，监听 :%d", s.Port)
	log.Printf("📊 Web 面板: http://localhost:%d", s.Port)
	log.Printf("🔌 控制通道: ws://localhost:%d/ws/control", s.Port)
	log.Printf("🔗 数据通道: 同端口 (魔数 0x4E)")

	// HTTP 服务器（处理 WebSocket + API + Web 面板）
	httpServer := &http.Server{Handler: s.newHTTPMux()}

	// 包装 listener：peek 分发
	peekLn := &PeekListener{
		Listener: ln,
		server:   s,
	}

	return httpServer.Serve(peekLn)
}

// StartHTTPOnly 仅启动 HTTP 模式（用于测试，不做 peek 分发）
func (s *Server) StartHTTPOnly() *http.ServeMux {
	return s.newHTTPMux()
}

// newHTTPMux 创建 HTTP 路由
func (s *Server) newHTTPMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Web 面板 — 静态文件（go:embed）
	mux.HandleFunc("/", s.handleWeb)

	// API
	mux.HandleFunc("GET /api/status", s.handleAPIStatus)
	mux.HandleFunc("GET /api/agents", s.handleAPIAgents)
	// 隧道 CRUD (为了安全性，理想情况下这里也加上 Auth，但这里遵循按原始计划或先兼容旧调用)
	mux.HandleFunc("POST /api/agents/{id}/tunnels", s.handleCreateTunnel)
	mux.HandleFunc("PUT /api/agents/{id}/tunnels/{name}/pause", s.handlePauseTunnel)
	mux.HandleFunc("PUT /api/agents/{id}/tunnels/{name}/resume", s.handleResumeTunnel)
	mux.HandleFunc("PUT /api/agents/{id}/tunnels/{name}/stop", s.handleStopTunnel)
	mux.HandleFunc("DELETE /api/agents/{id}/tunnels/{name}", s.handleDeleteTunnel)

	// Admin API
	mux.HandleFunc("POST /api/auth/login", s.handleAPILogin)
	mux.Handle("GET /api/admin/keys", RequireAuth(s.handleAPIAdminKeys))
	mux.Handle("POST /api/admin/keys", RequireAuth(s.handleAPIAdminKeys))
	mux.Handle("GET /api/admin/policies", RequireAuth(s.handleAPIAdminPolicies))
	mux.Handle("PUT /api/admin/policies", RequireAuth(s.handleAPIAdminPolicies))
	mux.Handle("GET /api/admin/logs", RequireAuth(s.handleAPIAdminLogs))
	mux.Handle("GET /api/admin/events", RequireAuth(s.handleAPIAdminEvents))

	// SSE 实时事件流
	mux.HandleFunc("GET /api/events", s.handleSSE)

	// 控制通道 WebSocket
	mux.HandleFunc("/ws/control", s.handleControlWS)

	// 数据通道端点信息
	mux.HandleFunc("/ws/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"info": "数据通道已迁移到同端口 TCP 二进制协议，魔数 0x4E",
		})
	})

	return mux
}

// PeekListener 包装 net.Listener，peek 首字节区分 HTTP 和数据通道。
// HTTP 连接直接交给 http.Server，数据通道连接交给 handleDataConn。
type PeekListener struct {
	net.Listener
	server  *Server
	pending chan net.Conn
	once    sync.Once
}

func (pl *PeekListener) Accept() (net.Conn, error) {
	pl.once.Do(func() {
		pl.pending = make(chan net.Conn, 64)
		go pl.dispatchLoop()
	})

	conn, ok := <-pl.pending
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

// dispatchLoop 从底层 Listener Accept 连接，peek 首字节分发。
func (pl *PeekListener) dispatchLoop() {
	defer close(pl.pending)

	for {
		conn, err := pl.Listener.Accept()
		if err != nil {
			return
		}

		pc := &PeekConn{Conn: conn}
		b, err := pc.PeekByte()
		if err != nil {
			conn.Close()
			continue
		}

		if b == protocol.DataChannelMagic {
			// 数据通道：消费掉魔数字节，交给 handleDataConn
			pc.hasPeek = false // 消费掉已 peek 的魔数
			go pl.server.handleDataConn(pc)
		} else {
			// HTTP/WebSocket：送入 pending channel 交给 http.Server
			pl.pending <- pc
		}
	}
}

// --- WebSocket 升级器 ---

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // 开发阶段允许所有来源
	},
}

// --- 控制通道处理 ---

func (s *Server) handleControlWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket 升级失败: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("📡 新的控制通道连接: %s", r.RemoteAddr)

	// 等待 Agent 发送认证消息
	agent, err := s.handleAuth(conn)
	if err != nil {
		log.Printf("❌ Agent 认证失败 [%s]: %v", r.RemoteAddr, err)
		return
	}

	log.Printf("✅ Agent 已连接: %s (%s/%s) [ID: %s]", agent.Info.Hostname, agent.Info.OS, agent.Info.Arch, agent.ID)

	// 保存 Agent 连接
	s.agents.Store(agent.ID, agent)

	// 迁移 store 中的旧 AgentID 到新的
	if s.store != nil {
		s.store.UpdateAgentID(agent.Info.Hostname, "", agent.ID)
	}

	// 发布 Agent 上线事件
	s.events.PublishJSON("agent_online", map[string]any{
		"agent_id": agent.ID,
		"info":     agent.Info,
	})

	// 启动隧道恢复（等数据通道建立后执行）
	go s.restoreTunnels(agent)

	defer func() {
		// 清理：停止所有活跃的代理隧道监听（保留 store 中的配置）
		s.PauseAllProxies(agent)
		agent.dataMu.Lock()
		if agent.dataSession != nil {
			agent.dataSession.Close()
		}
		agent.dataMu.Unlock()
		s.agents.Delete(agent.ID)
		log.Printf("🔌 Agent 已断开: %s [ID: %s]", agent.Info.Hostname, agent.ID)

		// 发布 Agent 离线事件
		s.events.PublishJSON("agent_offline", map[string]any{
			"agent_id": agent.ID,
		})
	}()

	// 持续读取控制消息
	s.controlLoop(agent)
}

// handleAuth 处理 Agent 的认证流程
func (s *Server) handleAuth(conn *websocket.Conn) (*AgentConn, error) {
	// 读取认证消息
	var msg protocol.Message
	if err := conn.ReadJSON(&msg); err != nil {
		return nil, fmt.Errorf("读取认证消息失败: %w", err)
	}
	if msg.Type != protocol.MsgTypeAuth {
		return nil, fmt.Errorf("期望认证消息，收到: %s", msg.Type)
	}

	var authReq protocol.AuthRequest
	if err := msg.ParsePayload(&authReq); err != nil {
		return nil, fmt.Errorf("解析认证数据失败: %w", err)
	}

	// 验证 Key
	if s.adminStore != nil {
		valid, err := s.adminStore.ValidateAgentKey(authReq.Key)
		if !valid {
			log.Printf("❌ Agent Key 验证失败 [%s]: %v", conn.RemoteAddr().String(), err)
			return nil, fmt.Errorf("认证失败: %w", err)
		}
	}

	// 接受连接
	agentID := generateUUID()

	// 发送认证响应
	resp, _ := protocol.NewMessage(protocol.MsgTypeAuthResp, protocol.AuthResponse{
		Success: true,
		Message: "认证成功",
		AgentID: agentID,
	})
	if err := conn.WriteJSON(resp); err != nil {
		return nil, fmt.Errorf("发送认证响应失败: %w", err)
	}

	return &AgentConn{
		ID:      agentID,
		Info:    authReq.Agent,
		conn:    conn,
		proxies: make(map[string]*ProxyTunnel),
	}, nil
}

// controlLoop 持续处理控制通道上的消息
func (s *Server) controlLoop(agent *AgentConn) {
	for {
		var msg protocol.Message
		if err := agent.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("⚠️ Agent [%s] 连接异常: %v", agent.ID, err)
			}
			return
		}

		switch msg.Type {
		case protocol.MsgTypePing:
			// 收到心跳，回复 Pong
			pong, _ := protocol.NewMessage(protocol.MsgTypePong, nil)
			agent.mu.Lock()
			agent.conn.WriteJSON(pong)
			agent.mu.Unlock()

		case protocol.MsgTypeProbeReport:
			// 收到探针数据
			var stats protocol.SystemStats
			if err := msg.ParsePayload(&stats); err != nil {
				log.Printf("⚠️ 解析探针数据失败 [%s]: %v", agent.ID, err)
				continue
			}
			agent.SetStats(&stats)
			log.Printf("📊 [%s] CPU: %.1f%% | 内存: %.1f%% | 磁盘: %.1f%%",
				agent.Info.Hostname, stats.CPUUsage, stats.MemUsage, stats.DiskUsage)

			// 发布探针数据更新事件
			s.events.PublishJSON("stats_update", map[string]any{
				"agent_id": agent.ID,
				"stats":    stats,
			})

		case protocol.MsgTypeProxyNew:
			// 收到创建代理隧道请求
			var req protocol.ProxyNewRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ 解析代理请求失败 [%s]: %v", agent.ID, err)
				continue
			}

			err := s.StartProxy(agent, req)
			var resp *protocol.Message
			if err != nil {
				log.Printf("❌ 创建代理失败 [%s]: %v", agent.ID, err)
				resp, _ = protocol.NewMessage(protocol.MsgTypeProxyNewResp, protocol.ProxyNewResponse{
					Success: false,
					Message: err.Error(),
				})
			} else {
				agent.proxyMu.RLock()
				tunnel := agent.proxies[req.Name]
				actualPort := tunnel.Config.RemotePort
				config := tunnel.Config
				agent.proxyMu.RUnlock()

				resp, _ = protocol.NewMessage(protocol.MsgTypeProxyNewResp, protocol.ProxyNewResponse{
					Success:    true,
					Message:    "代理隧道创建成功",
					RemotePort: actualPort,
				})

				// 发布隧道创建事件（通知前端）
				s.events.PublishJSON("tunnel_changed", map[string]any{
					"agent_id": agent.ID,
					"tunnel":   config,
				})
			}

			agent.mu.Lock()
			agent.conn.WriteJSON(resp)
			agent.mu.Unlock()

		case protocol.MsgTypeProxyClose:
			var req protocol.ProxyCloseRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ 解析关闭代理请求失败 [%s]: %v", agent.ID, err)
				continue
			}
			if err := s.StopProxy(agent, req.Name); err != nil {
				log.Printf("⚠️ 关闭代理失败 [%s]: %v", agent.ID, err)
			} else {
				// 发布隧道关闭事件（通知前端）
				s.events.PublishJSON("tunnel_changed", map[string]any{
					"agent_id": agent.ID,
					"tunnel":   map[string]any{"name": req.Name},
				})
			}

		default:
			log.Printf("⚠️ 未知消息类型 [%s]: %s", agent.ID, msg.Type)
		}
	}
}

// persistEventsLoop 订阅事件总线并将关键事件持久化到 AdminStore
func (s *Server) persistEventsLoop() {
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	for event := range ch {
		if s.adminStore != nil {
			// 过滤掉探针数据，避免日志过多
			if event.Type != "stats_update" {
				s.adminStore.AddEvent(event.Type, event.Data)
			}
		}
	}
}

// --- Web 面板 ---

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	// Phase 1: 返回简单的占位页面
	// Phase 2 将使用 go:embed 嵌入真正的前端构建产物
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, placeholderHTML)
}

// --- API ---

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	agentCount := 0
	tunnelActive := 0
	tunnelPaused := 0
	tunnelStopped := 0

	s.agents.Range(func(_, value any) bool {
		agentCount++
		a := value.(*AgentConn)
		a.RangeProxies(func(_ string, t *ProxyTunnel) bool {
			switch t.Config.Status {
			case protocol.ProxyStatusActive:
				tunnelActive++
			case protocol.ProxyStatusPaused:
				tunnelPaused++
			case protocol.ProxyStatusStopped:
				tunnelStopped++
			}
			return true
		})
		return true
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "running",
		"agent_count":    agentCount,
		"version":        "0.1.0",
		"listen_port":    s.Port,
		"uptime":         int64(time.Since(s.startTime).Seconds()),
		"store_path":     s.getStorePath(),
		"tunnel_active":  tunnelActive,
		"tunnel_paused":  tunnelPaused,
		"tunnel_stopped": tunnelStopped,
	})
}

// getStorePath 获取实际的 store 路径
func (s *Server) getStorePath() string {
	if s.store != nil {
		return s.store.path
	}
	return s.StorePath
}

func (s *Server) handleAPIAgents(w http.ResponseWriter, r *http.Request) {
	type agentView struct {
		ID      string                 `json:"id"`
		Info    protocol.AgentInfo     `json:"info"`
		Stats   *protocol.SystemStats  `json:"stats,omitempty"`
		Proxies []protocol.ProxyConfig `json:"proxies"`
	}

	var agents []agentView
	s.agents.Range(func(_, value any) bool {
		a := value.(*AgentConn)
		// 收集 Agent 的所有隧道配置
		var proxies []protocol.ProxyConfig
		a.RangeProxies(func(_ string, tunnel *ProxyTunnel) bool {
			proxies = append(proxies, tunnel.Config)
			return true
		})
		if proxies == nil {
			proxies = []protocol.ProxyConfig{} // 确保 JSON 输出 [] 而非 null
		}
		agents = append(agents, agentView{
			ID:      a.ID,
			Info:    a.Info,
			Stats:   a.GetStats(),
			Proxies: proxies,
		})
		return true
	})
	if agents == nil {
		agents = []agentView{} // 确保 JSON 输出 [] 而非 null
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

// --- 隧道 CRUD API ---

func (s *Server) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	val, ok := s.agents.Load(agentID)
	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	agent := val.(*AgentConn)

	var req protocol.ProxyNewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := s.StartProxy(agent, req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// 获取实际分配的端口
	agent.proxyMu.RLock()
	tunnel := agent.proxies[req.Name]
	actualPort := tunnel.Config.RemotePort
	config := tunnel.Config
	agent.proxyMu.RUnlock()

	// 通知 Agent 注册本地代理配置
	proxyMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyNew, req)
	agent.mu.Lock()
	if err := agent.conn.WriteJSON(proxyMsg); err != nil {
		log.Printf("⚠️ 通知 Agent 代理配置失败 [%s]: %v", agent.ID, err)
	}
	agent.mu.Unlock()

	// 持久化到 store
	if s.store != nil {
		s.store.AddTunnel(StoredTunnel{
			ProxyNewRequest: req,
			Status:          protocol.ProxyStatusActive,
			AgentID:         agentID,
			Hostname:        agent.Info.Hostname,
		})
	}

	// 发布隧道创建事件
	s.events.PublishJSON("tunnel_changed", map[string]any{
		"agent_id": agentID,
		"tunnel":   config,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"success":     true,
		"message":     "代理隧道创建成功",
		"remote_port": actualPort,
	})
}

func (s *Server) handlePauseTunnel(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	val, ok := s.agents.Load(agentID)
	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	agent := val.(*AgentConn)

	// 检查隧道是否存在且为 active 状态
	agent.proxyMu.RLock()
	tunnel, exists := agent.proxies[tunnelName]
	agent.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}
	if tunnel.Config.Status != protocol.ProxyStatusActive {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "只有 active 状态的隧道才能暂停"})
		return
	}

	// 暂停：关闭 Listener 但保留配置
	if err := s.PauseProxy(agent, tunnelName); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	// 通知 Agent 移除本地代理配置
	closeMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyClose, protocol.ProxyCloseRequest{
		Name:   tunnelName,
		Reason: "paused",
	})
	agent.mu.Lock()
	agent.conn.WriteJSON(closeMsg)
	agent.mu.Unlock()

	// 更新 store
	if s.store != nil {
		s.store.UpdateStatus(agent.Info.Hostname, tunnelName, protocol.ProxyStatusPaused)
	}

	// 发布事件
	s.events.PublishJSON("tunnel_changed", map[string]any{
		"agent_id": agentID,
		"tunnel": protocol.ProxyConfig{
			Name:    tunnelName,
			AgentID: agentID,
			Status:  protocol.ProxyStatusPaused,
		},
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "隧道已暂停"})
}

func (s *Server) handleResumeTunnel(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	val, ok := s.agents.Load(agentID)
	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	agent := val.(*AgentConn)

	// 检查隧道是否为 paused 或 stopped 状态
	agent.proxyMu.RLock()
	tunnel, exists := agent.proxies[tunnelName]
	agent.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}
	if tunnel.Config.Status != protocol.ProxyStatusPaused && tunnel.Config.Status != protocol.ProxyStatusStopped {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "只有 paused 或 stopped 状态的隧道才能恢复"})
		return
	}

	// 恢复：重新监听端口
	if err := s.ResumeProxy(agent, tunnelName); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	// 通知 Agent 重新注册代理配置
	proxyMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyNew, tunnel.Config.ToProxyNewRequest())
	agent.mu.Lock()
	agent.conn.WriteJSON(proxyMsg)
	agent.mu.Unlock()

	// 更新 store
	if s.store != nil {
		s.store.UpdateStatus(agent.Info.Hostname, tunnelName, protocol.ProxyStatusActive)
	}

	// 发布事件
	s.events.PublishJSON("tunnel_changed", map[string]any{
		"agent_id": agentID,
		"tunnel": protocol.ProxyConfig{
			Name:    tunnelName,
			AgentID: agentID,
			Status:  protocol.ProxyStatusActive,
		},
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "隧道已恢复"})
}

func (s *Server) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	val, ok := s.agents.Load(agentID)
	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	agent := val.(*AgentConn)

	agent.proxyMu.RLock()
	tunnel, exists := agent.proxies[tunnelName]
	agent.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}

	// 如果是 active 状态需要先关闭 Listener
	if tunnel.Config.Status == protocol.ProxyStatusActive {
		s.PauseProxy(agent, tunnelName)
		// 通知 Agent 移除本地配置
		closeMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyClose, protocol.ProxyCloseRequest{
			Name:   tunnelName,
			Reason: "stopped",
		})
		agent.mu.Lock()
		agent.conn.WriteJSON(closeMsg)
		agent.mu.Unlock()
	}

	// 更新状态为 stopped
	agent.proxyMu.Lock()
	if t, ok := agent.proxies[tunnelName]; ok {
		t.Config.Status = protocol.ProxyStatusStopped
	}
	agent.proxyMu.Unlock()

	// 更新 store
	if s.store != nil {
		s.store.UpdateStatus(agent.Info.Hostname, tunnelName, protocol.ProxyStatusStopped)
	}

	// 发布事件
	s.events.PublishJSON("tunnel_changed", map[string]any{
		"agent_id": agentID,
		"tunnel": protocol.ProxyConfig{
			Name:    tunnelName,
			AgentID: agentID,
			Status:  protocol.ProxyStatusStopped,
		},
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "隧道已停止"})
}

func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tunnelName := r.PathValue("name")

	val, ok := s.agents.Load(agentID)
	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	agent := val.(*AgentConn)

	// 检查隧道是否存在
	agent.proxyMu.RLock()
	tunnel, exists := agent.proxies[tunnelName]
	agent.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}

	// 只有 stopped 状态才能删除
	if tunnel.Config.Status != protocol.ProxyStatusStopped {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("隧道当前状态为 %q，只有 stopped 状态才能删除", tunnel.Config.Status),
		})
		return
	}

	// 从内存中移除
	agent.proxyMu.Lock()
	delete(agent.proxies, tunnelName)
	agent.proxyMu.Unlock()

	// 从 store 中移除
	if s.store != nil {
		s.store.RemoveTunnel(agent.Info.Hostname, tunnelName)
	}

	// 发布隧道删除事件
	s.events.PublishJSON("tunnel_changed", map[string]any{
		"agent_id": agentID,
		"tunnel": protocol.ProxyConfig{
			Name:    tunnelName,
			AgentID: agentID,
			Status:  protocol.ProxyStatusStopped,
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

// restoreTunnels 在 Agent 重连后恢复之前的隧道配置
func (s *Server) restoreTunnels(agent *AgentConn) {
	if s.store == nil {
		return
	}

	tunnels := s.store.GetTunnelsByHostname(agent.Info.Hostname)
	if len(tunnels) == 0 {
		return
	}

	// 等待数据通道建立
	for i := 0; i < 30; i++ {
		agent.dataMu.RLock()
		hasData := agent.dataSession != nil && !agent.dataSession.IsClosed()
		agent.dataMu.RUnlock()
		if hasData {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	restoredCount := 0
	for _, st := range tunnels {
		switch st.Status {
		case protocol.ProxyStatusActive:
			// 恢复 active 隧道
			log.Printf("🔄 恢复隧道: %s (:%d → %s:%d)", st.Name, st.RemotePort, st.LocalIP, st.LocalPort)
			if err := s.StartProxy(agent, st.ProxyNewRequest); err != nil {
				log.Printf("⚠️ 恢复隧道失败 [%s]: %v", st.Name, err)
				continue
			}
			// 通知 Agent
			proxyMsg, _ := protocol.NewMessage(protocol.MsgTypeProxyNew, st.ProxyNewRequest)
			agent.mu.Lock()
			agent.conn.WriteJSON(proxyMsg)
			agent.mu.Unlock()
			restoredCount++

		case protocol.ProxyStatusPaused, protocol.ProxyStatusStopped:
			// paused/stopped 隧道只恢复配置记录，不启动监听
			agent.proxyMu.Lock()
			agent.proxies[st.Name] = &ProxyTunnel{
				Config: protocol.ProxyConfig{
					Name:       st.Name,
					Type:       st.Type,
					LocalIP:    st.LocalIP,
					LocalPort:  st.LocalPort,
					RemotePort: st.RemotePort,
					AgentID:    agent.ID,
					Status:     st.Status,
				},
				done: make(chan struct{}),
			}
			agent.proxyMu.Unlock()
			restoredCount++
		}
	}

	// 恢复完成后一次性通知前端刷新
	if restoredCount > 0 {
		s.events.PublishJSON("tunnel_changed", map[string]any{
			"agent_id": agent.ID,
			"action":   "restored",
			"count":    restoredCount,
		})
	}
}

// --- 辅助 ---

// generateUUID 生成一个 UUID v4 (基于 crypto/rand，不可预测)
func generateUUID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	// 设置 version 4 (bits 12-15 of time_hi_and_version)
	buf[6] = (buf[6] & 0x0f) | 0x40
	// 设置 variant (bits 6-7 of clk_seq_hi_and_reserved)
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// 占位 HTML 页面
const placeholderHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>NetsGo 管控平台</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
            background: linear-gradient(135deg, #0f0c29, #302b63, #24243e);
            color: #fff; min-height: 100vh;
            display: flex; align-items: center; justify-content: center;
        }
        .container {
            text-align: center; padding: 2rem;
            background: rgba(255,255,255,0.05);
            border-radius: 16px; backdrop-filter: blur(10px);
            border: 1px solid rgba(255,255,255,0.1);
        }
        h1 { font-size: 2.5rem; margin-bottom: 0.5rem; }
        h1 span { color: #7c3aed; }
        p { color: #a0a0b0; font-size: 1.1rem; margin: 0.5rem 0; }
        .badge {
            display: inline-block; margin-top: 1rem; padding: 0.4rem 1rem;
            background: #7c3aed; border-radius: 20px; font-size: 0.85rem;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>🚀 <span>NetsGo</span></h1>
        <p>新一代内网穿透与边缘管控平台</p>
        <p>服务端已启动，Web 面板正在开发中…</p>
        <div class="badge">Phase 2 — yamux 数据面</div>
    </div>
</body>
</html>`
