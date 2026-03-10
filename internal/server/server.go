package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"netsgo/pkg/protocol"
)

// Server 是服务端的核心结构体
type Server struct {
	Port   int
	agents sync.Map // agentID -> *AgentConn
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
	return &Server{Port: port}
}

// Start 启动服务端，单端口同时处理 HTTP/WebSocket 和数据通道。
// 通过 peek 首字节区分：HTTP 请求 vs 数据通道魔数 (0x4E)。
func (s *Server) Start() error {
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
	mux.HandleFunc("/api/status", s.handleAPIStatus)
	mux.HandleFunc("/api/agents", s.handleAPIAgents)

	// 控制通道 WebSocket
	mux.HandleFunc("/ws/control", s.handleControlWS)

	// 数据通道端点信息（不再是 501）
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
	defer func() {
		// 清理：停止所有代理隧道，关闭数据通道
		s.StopAllProxies(agent)
		agent.dataMu.Lock()
		if agent.dataSession != nil {
			agent.dataSession.Close()
		}
		agent.dataMu.Unlock()
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
				agent.proxyMu.RUnlock()

				resp, _ = protocol.NewMessage(protocol.MsgTypeProxyNewResp, protocol.ProxyNewResponse{
					Success:    true,
					Message:    "代理隧道创建成功",
					RemotePort: actualPort,
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
			}

		default:
			log.Printf("⚠️ 未知消息类型 [%s]: %s", agent.ID, msg.Type)
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
			Stats: a.GetStats(),
		})
		return true
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
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
