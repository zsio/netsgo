package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

// Server 是服务端的核心结构体
type Server struct {
	Port   int
	agents sync.Map // agentID -> *AgentConn
}

// AgentConn 代表一个已连接的 Agent
type AgentConn struct {
	ID       string
	Info     protocol.AgentInfo
	Stats    *protocol.SystemStats
	conn     *websocket.Conn
	mu       sync.Mutex
}

// New 创建一个新的 Server 实例
func New(port int) *Server {
	return &Server{Port: port}
}

// Start 启动服务端，监听单端口提供所有服务
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Web 面板 — 静态文件（go:embed）
	mux.HandleFunc("/", s.handleWeb)

	// API
	mux.HandleFunc("/api/status", s.handleAPIStatus)
	mux.HandleFunc("/api/agents", s.handleAPIAgents)

	// 控制通道 WebSocket
	mux.HandleFunc("/ws/control", s.handleControlWS)

	// 数据通道 WebSocket（Phase 1 占位）
	mux.HandleFunc("/ws/data", s.handleDataWS)

	addr := fmt.Sprintf(":%d", s.Port)
	log.Printf("🚀 NetsGo Server 已启动，监听 %s", addr)
	log.Printf("📊 Web 面板: http://localhost%s", addr)
	log.Printf("🔌 控制通道: ws://localhost%s/ws/control", addr)

	return http.ListenAndServe(addr, mux)
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
	defer func() {
		s.agents.Delete(agent.ID)
		log.Printf("🔌 Agent 已断开: %s [ID: %s]", agent.Info.Hostname, agent.ID)
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

	// Phase 1: 简单的 Token 验证（后续可接入更复杂的鉴权）
	// 目前先接受所有连接
	agentID := fmt.Sprintf("agent_%s_%d", authReq.Agent.Hostname, generateID())

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
		ID:   agentID,
		Info: authReq.Agent,
		conn: conn,
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
			agent.Stats = &stats
			log.Printf("📊 [%s] CPU: %.1f%% | 内存: %.1f%% | 磁盘: %.1f%%",
				agent.Info.Hostname, stats.CPUUsage, stats.MemUsage, stats.DiskUsage)

		default:
			log.Printf("⚠️ 未知消息类型 [%s]: %s", agent.ID, msg.Type)
		}
	}
}

// --- 数据通道处理（Phase 1 占位）---

func (s *Server) handleDataWS(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "数据通道尚未实现 (Phase 2)", http.StatusNotImplemented)
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
	s.agents.Range(func(_, _ any) bool {
		agentCount++
		return true
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":      "running",
		"agent_count": agentCount,
		"version":     "0.1.0",
	})
}

func (s *Server) handleAPIAgents(w http.ResponseWriter, r *http.Request) {
	type agentView struct {
		ID    string                `json:"id"`
		Info  protocol.AgentInfo    `json:"info"`
		Stats *protocol.SystemStats `json:"stats,omitempty"`
	}

	var agents []agentView
	s.agents.Range(func(_, value any) bool {
		a := value.(*AgentConn)
		agents = append(agents, agentView{
			ID:    a.ID,
			Info:  a.Info,
			Stats: a.Stats,
		})
		return true
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

// --- 辅助 ---

var idCounter int64
var idMu sync.Mutex

func generateID() int64 {
	idMu.Lock()
	defer idMu.Unlock()
	idCounter++
	return idCounter
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
        <div class="badge">Phase 1 — MVP</div>
    </div>
</body>
</html>`
