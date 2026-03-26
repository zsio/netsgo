package server

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"netsgo/pkg/netutil"
	"netsgo/pkg/protocol"
	"netsgo/pkg/sysinfo"
	buildversion "netsgo/pkg/version"
	"netsgo/web"
)

// Server 是服务端的核心结构体
type Server struct {
	Port                        int
	StorePath                   string // 隧道配置文件路径（空则使用默认）
	SetupToken                  string // 显式配置初始化 Token（空则启动时随机生成）
	AllowLoopbackManagementHost bool
	TLS                         *TLSConfig
	TLSFingerprint              string
	clients                     sync.Map          // stable clientID -> *ClientConn
	events                      *EventBus         // SSE 事件总线
	store                       *TunnelStore      // 隧道持久化存储
	startTime                   time.Time         // 服务器启动时间
	adminStore                  *AdminStore       // 系统管理后台数据存储
	webFS                       fs.FS             // 嵌入的前端静态资源 (nil 表示开发模式)
	webHandler                  http.Handler      // 缓存的 FileServer (nil 表示开发模式)
	cachedStatus                *serverStatusView // 后台采集的最新服务端状态
	cachedStatusMu              sync.RWMutex      // 保护 cachedStatus
	loginLimiter                *RateLimiter      // 管理员登录速率限制
	clientLimiter               *RateLimiter      // Client 认证速率限制
	setupLimiter                *RateLimiter      // 初始化接口速率限制
	authTimeout                 time.Duration     // WebSocket 认证阶段读超时（0 使用默认 30s）
	httpServer                  *http.Server
	listener                    net.Listener
	done                        chan struct{}
	setupToken                  string
	tlsEnabled                  bool
	publicIPv4                  string       // 缓存的公网 IPv4
	publicIPv6                  string       // 缓存的公网 IPv6
	publicIPMu                  sync.RWMutex // 保护公网 IP 缓存
	nextGeneration              atomic.Uint64
	pendingProvisionAckMu       sync.Mutex
	pendingProvisionAcks        map[pendingTunnelProvisionAckKey]chan provisionAckResult

	pendingDataTimeout      time.Duration
	dataHandshakeTimeout    time.Duration
	dataHandshakeAckTimeout time.Duration
	tunnelReadyTimeout      time.Duration
}

// ClientConn 代表一个已连接的 Client
type ClientConn struct {
	ID           string
	InstallID    string
	Info         protocol.ClientInfo
	RemoteAddr   string
	stats        *protocol.SystemStats
	prevStats    *protocol.SystemStats // 上一次探针快照（用于计算速率）
	prevStatsAt  time.Time             // 上一次快照时间
	statsMu      sync.RWMutex          // 保护 stats / prevStats
	conn         *websocket.Conn
	mu           sync.Mutex
	dataSession  *yamux.Session // 数据通道 yamux Session
	dataMu       sync.RWMutex   // 保护 dataSession
	dataToken    string
	generation   uint64
	state        clientState
	stateMu      sync.RWMutex
	pendingTimer *time.Timer
	proxies      map[string]*ProxyTunnel // 代理隧道 name -> tunnel
	proxyMu      sync.RWMutex            // 保护 proxies
}

// generateDataToken 生成 32 字节随机 hex 字符串，用作数据通道握手凭证
func generateDataToken() string {
	buf := make([]byte, 32)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

type clientView struct {
	ID          string                 `json:"id"`
	DisplayName string                 `json:"display_name,omitempty"`
	Info        protocol.ClientInfo    `json:"info"`
	Stats       *protocol.SystemStats  `json:"stats,omitempty"`
	Proxies     []protocol.ProxyConfig `json:"proxies"`
	Online      bool                   `json:"online"`
	LastSeen    *time.Time             `json:"last_seen,omitempty"`
	LastIP      string                 `json:"last_ip,omitempty"`
}

type serverStatusView struct {
	Status         string                   `json:"status"`
	ClientCount    int                      `json:"client_count"`
	Version        string                   `json:"version"`
	ListenPort     int                      `json:"listen_port"`
	Uptime         int64                    `json:"uptime"`
	SystemUptime   int64                    `json:"system_uptime"`
	OSInstallTime  int64                    `json:"os_install_time,omitempty"`
	StorePath      string                   `json:"store_path"`
	TunnelActive   int                      `json:"tunnel_active"`
	TunnelPaused   int                      `json:"tunnel_paused"`
	TunnelStopped  int                      `json:"tunnel_stopped"`
	ServerAddr     string                   `json:"server_addr"`
	AllowedPorts   []PortRange              `json:"allowed_ports"`
	OSArch         string                   `json:"os_arch"`
	GoVersion      string                   `json:"go_version"`
	Hostname       string                   `json:"hostname"`
	IPAddress      string                   `json:"ip_address"`
	CPUUsage       float64                  `json:"cpu_usage"`
	CPUCores       int                      `json:"cpu_cores"`
	MemUsed        uint64                   `json:"mem_used"`
	MemTotal       uint64                   `json:"mem_total"`
	AppMemUsed     uint64                   `json:"app_mem_used"`
	AppMemSys      uint64                   `json:"app_mem_sys"`
	DiskUsed       uint64                   `json:"disk_used"`
	DiskTotal      uint64                   `json:"disk_total"`
	DiskPartitions []protocol.DiskPartition `json:"disk_partitions"`
	GoroutineCount int                      `json:"goroutine_count"`
	PublicIPv4     string                   `json:"public_ipv4,omitempty"`
	PublicIPv6     string                   `json:"public_ipv6,omitempty"`
}

type consoleSnapshot struct {
	Clients      []clientView     `json:"clients"`
	ServerStatus serverStatusView `json:"server_status"`
}

// SetStats 安全地更新探针数据
func (a *ClientConn) SetStats(s *protocol.SystemStats) {
	a.statsMu.Lock()
	a.stats = s
	a.statsMu.Unlock()
}

// GetStats 安全地读取探针数据
func (a *ClientConn) GetStats() *protocol.SystemStats {
	a.statsMu.RLock()
	defer a.statsMu.RUnlock()
	return a.stats
}

// enrichStats 用上次快照计算派生指标（网络速率等），就地修改 stats
func (a *ClientConn) enrichStats(stats *protocol.SystemStats) {
	a.statsMu.RLock()
	prev := a.prevStats
	prevAt := a.prevStatsAt
	a.statsMu.RUnlock()

	if prev != nil {
		elapsed := time.Since(prevAt).Seconds()
		if elapsed > 0.5 {
			if stats.NetSent >= prev.NetSent {
				stats.NetSentSpeed = float64(stats.NetSent-prev.NetSent) / elapsed
			}
			if stats.NetRecv >= prev.NetRecv {
				stats.NetRecvSpeed = float64(stats.NetRecv-prev.NetRecv) / elapsed
			}
		}
	}
}

// New 创建一个新的 Server 实例
func New(port int) *Server {
	return &Server{
		Port:                    port,
		events:                  NewEventBus(),
		startTime:               time.Now(),
		done:                    make(chan struct{}),
		pendingProvisionAcks:    make(map[pendingTunnelProvisionAckKey]chan provisionAckResult),
		pendingDataTimeout:      15 * time.Second,
		dataHandshakeTimeout:    10 * time.Second,
		dataHandshakeAckTimeout: 2 * time.Second,
		tunnelReadyTimeout:      5 * time.Second,
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

// getDataDir 返回数据目录路径（用于存储 TLS 证书等）
func (s *Server) getDataDir() string {
	if s.StorePath != "" {
		return filepath.Dir(s.StorePath)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".netsgo")
}

// RangeClients 遍历所有已连接的 Client
func (s *Server) RangeClients(fn func(id string, client *ClientConn) bool) {
	s.clients.Range(func(key, value any) bool {
		return fn(key.(string), value.(*ClientConn))
	})
}

// RangeProxies 遍历 Client 的所有代理隧道
func (a *ClientConn) RangeProxies(fn func(name string, tunnel *ProxyTunnel) bool) {
	a.proxyMu.RLock()
	defer a.proxyMu.RUnlock()
	for name, tunnel := range a.proxies {
		if !fn(name, tunnel) {
			return
		}
	}
}

// Start 启动服务端，单端口同时处理 HTTP、控制通道和数据通道 WebSocket。
func (s *Server) Start() error {
	s.startTime = time.Now()
	s.done = make(chan struct{})

	// 初始化嵌入的前端资源
	webFS, err := web.DistFS()
	if err != nil {
		return fmt.Errorf("加载前端资源失败: %w", err)
	}
	s.webFS = webFS
	if s.webFS != nil {
		s.webHandler = http.FileServerFS(s.webFS)
	}

	if web.IsDevMode() {
		log.Printf("🔧 开发模式：前端资源未嵌入，请使用 cd web && bun run dev 独立启动前端")
	} else if s.webFS != nil {
		log.Printf("📦 前端资源已嵌入到二进制中")
	}

	// 初始化隧道持久化存储
	if err := s.initStore(); err != nil {
		return fmt.Errorf("初始化隧道存储失败: %w", err)
	}

	// 启动时清理过期 Token
	if s.adminStore != nil {
		s.adminStore.CleanExpiredTokens()
		go s.tokenCleanupLoop()
	}

	if s.adminStore != nil && !s.adminStore.IsInitialized() {
		if s.setupToken == "" {
			if s.SetupToken != "" {
				s.setupToken = s.SetupToken
			} else {
				buf := make([]byte, 32)
				if _, err := rand.Read(buf); err != nil {
					return fmt.Errorf("生成 Setup Token 失败: %w", err)
				}
				s.setupToken = hex.EncodeToString(buf)
			}
		}
		s.emitSetupTokenBanner(os.Stderr)
	}

	// 初始化速率限制器
	s.initRateLimiters()

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", s.Port, err)
	}
	s.listener = ln

	addr := ln.Addr().(*net.TCPAddr)
	if s.Port == 0 {
		s.Port = addr.Port // 更新为实际端口（当 Port=0 时有用）
	}

	var serveLn net.Listener = ln
	if s.TLS != nil && s.TLS.IsEnabled() {
		dataDir := s.getDataDir()
		tlsConfig, fingerprint, err := s.TLS.loadOrBuildTLSConfig(dataDir)
		if err != nil {
			ln.Close()
			return fmt.Errorf("TLS 初始化失败: %w", err)
		}
		s.TLSFingerprint = fingerprint
		s.tlsEnabled = true
		serveLn = tls.NewListener(ln, tlsConfig)
	}

	// 根据 TLS 状态输出启动信息
	log.Printf("🚀 NetsGo Server 已启动，监听 :%d", s.Port)
	if s.tlsEnabled {
		if s.webFS != nil {
			log.Printf("📊 Web 面板: https://localhost:%d", s.Port)
		}
		log.Printf("🔌 控制通道: wss://localhost:%d/ws/control", s.Port)
		log.Printf("🔗 数据通道: wss://localhost:%d/ws/data", s.Port)
	} else {
		if s.webFS != nil {
			log.Printf("📊 Web 面板: http://localhost:%d", s.Port)
		}
		log.Printf("🔌 控制通道: ws://localhost:%d/ws/control", s.Port)
		log.Printf("🔗 数据通道: ws://localhost:%d/ws/data", s.Port)
	}

	// 反代 / 代理头信任策略提示
	if s.TLS != nil && s.TLS.Mode == TLSModeOff && len(s.TLS.TrustedProxies) == 0 {
		log.Printf("⚠️ TLS 模式为 off（反向代理模式），但未配置 --trusted-proxies")
		log.Printf("⚠️ X-Forwarded-For 头将被忽略，速率限制将按代理 IP 而非真实客户端 IP 计算")
		log.Printf("⚠️ 如果在反向代理后运行，请配置: --trusted-proxies 127.0.0.1/32")
	}

	// HTTP 服务器（处理 WebSocket + API + Web 面板）
	// 注意：不设置 ReadTimeout / WriteTimeout，因为 WebSocket 和 SSE 是长连接
	// ReadHeaderTimeout 足以防御 Slowloris 攻击（限制请求头读取时间）
	s.httpServer = &http.Server{
		Handler:           s.newHTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 后台循环依赖 store/adminStore/listen port 等运行时状态，放到启动末尾统一拉起。
	go s.serverStatusLoop()

	return s.httpServer.Serve(serveLn)
}

func (s *Server) emitSetupTokenBanner(w io.Writer) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "┌──────────────────────────────────────────────────────────────────┐")
	fmt.Fprintln(w, "│  ⚠️  服务尚未初始化                                              │")
	fmt.Fprintln(w, "│  请使用以下 Setup Token 完成初始化:                               │")
	fmt.Fprintf(w, "│  SETUP_TOKEN=%s │\n", s.setupToken)
	fmt.Fprintln(w, "└──────────────────────────────────────────────────────────────────┘")
	fmt.Fprintln(w)
}

// Shutdown 优雅关闭服务端 (P15)
// 1. 通知后台 goroutine 退出
// 2. 关闭事件总线（让 SSE 连接退出）
// 3. 断开所有 Client 连接（让 WebSocket 连接退出）
// 4. 关闭 HTTP 服务器（等待活跃请求结束——此时 SSE/WS 已退出，不会阻塞）
func (s *Server) Shutdown(ctx context.Context) error {
	log.Printf("🛑 开始优雅关闭...")

	// 1. 通知所有后台 goroutine 退出
	close(s.done)

	// 2. 关闭事件总线（让 SSE handler 的 channel 读到 close，自然退出）
	if s.events != nil {
		s.events.Close()
		log.Printf("📡 SSE 事件总线已关闭")
	}

	// 3. 断开所有 Client 连接（让 WebSocket handler 的 ReadJSON 返回 error，自然退出）
	clientCount := 0
	s.clients.Range(func(key, value any) bool {
		client := value.(*ClientConn)
		clientCount++
		s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "server_shutdown")
		s.clients.Delete(key)
		return true
	})
	if clientCount > 0 {
		log.Printf("🔌 已断开 %d 个 Client 连接", clientCount)
	}

	// 短暂等待，让 SSE/WebSocket handler 有时间处理断开并从 ServeHTTP 返回
	time.Sleep(200 * time.Millisecond)

	// 4. 关闭 HTTP 服务器（此时长连接已断开，Shutdown 应能快速完成）
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			log.Printf("⚠️ HTTP 服务器关闭出错: %v", err)
			return err
		}
	}

	log.Printf("✅ 优雅关闭完成")
	return nil
}

// StartHTTPOnly 仅返回最终 HTTP 入口（用于测试）
func (s *Server) StartHTTPOnly() http.Handler {
	return s.newHTTPHandler()
}

func (s *Server) newHTTPHandler() http.Handler {
	return s.hostDispatchHandler(s.securityHeadersHandler(s.newManagementMux()))
}

// newManagementMux 创建管理面 HTTP 路由。
func (s *Server) newManagementMux() *http.ServeMux {
	mux := http.NewServeMux()
	s.registerManagementRoutes(mux)
	return mux
}

// newHTTPMux 创建旧式组合路由，保留给需要直接测试内部 handler 的场景。
func (s *Server) newHTTPMux() *http.ServeMux {
	mux := s.newManagementMux()
	s.registerInternalWSRoutes(mux)
	return mux
}

func (s *Server) registerManagementRoutes(mux *http.ServeMux) {
	// Web 面板 — 静态文件（go:embed）
	mux.HandleFunc("/", s.handleWeb)

	// Setup API（初始化向导，无需鉴权）
	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/setup/init", s.handleSetupInit)

	// API
	mux.HandleFunc("GET /api/status", s.RequireAuth(s.handleAPIStatus))
	mux.HandleFunc("GET /api/clients", s.RequireAuth(s.handleAPIClients))
	mux.HandleFunc("PUT /api/clients/{id}/display-name", s.RequireAuth(s.handleUpdateDisplayName))
	mux.HandleFunc("POST /api/clients/{id}/tunnels", s.RequireAuth(s.handleCreateTunnel))
	mux.HandleFunc("PUT /api/clients/{id}/tunnels/{name}/pause", s.RequireAuth(s.handlePauseTunnel))
	mux.HandleFunc("PUT /api/clients/{id}/tunnels/{name}/resume", s.RequireAuth(s.handleResumeTunnel))
	mux.HandleFunc("PUT /api/clients/{id}/tunnels/{name}/stop", s.RequireAuth(s.handleStopTunnel))
	mux.HandleFunc("PUT /api/clients/{id}/tunnels/{name}", s.RequireAuth(s.handleUpdateTunnel))
	mux.HandleFunc("DELETE /api/clients/{id}/tunnels/{name}", s.RequireAuth(s.handleDeleteTunnel))

	// Admin API (JWT + Session Binding 鉴权)
	mux.HandleFunc("POST /api/auth/login", s.handleAPILogin)
	mux.HandleFunc("POST /api/auth/logout", s.RequireAuth(s.handleAPILogout))
	mux.HandleFunc("GET /api/admin/keys", s.RequireAuth(s.handleAPIAdminKeys))
	mux.HandleFunc("POST /api/admin/keys", s.RequireAuth(s.handleAPIAdminKeys))
	mux.HandleFunc("PUT /api/admin/keys/{id}/{action}", s.RequireAuth(s.handleAPIAdminKeyItem))
	mux.HandleFunc("DELETE /api/admin/keys/{id}", s.RequireAuth(s.handleAPIAdminKeyItem))
	mux.HandleFunc("GET /api/admin/config", s.RequireAuth(s.handleAPIAdminConfig))
	mux.HandleFunc("PUT /api/admin/config", s.RequireAuth(s.handleAPIAdminConfig))

	// SSE 实时事件流
	mux.HandleFunc("GET /api/events", s.RequireAuth(s.handleSSE))
}

func (s *Server) registerInternalWSRoutes(mux *http.ServeMux) {
	// 控制通道 WebSocket
	mux.HandleFunc("/ws/control", s.handleControlWS)

	// 数据通道 WebSocket
	mux.HandleFunc("/ws/data", s.handleDataWS)
}

// --- WebSocket 升级器 ---

// wsMaxMessageSize WebSocket 单条消息最大字节数（1 MiB），
// 防止恶意 Client 发送超大 JSON 消息导致服务端 OOM。
const wsMaxMessageSize = 1 << 20 // 1 MiB

const wsDataMaxMessageSize = 512 * 1024

func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Go 客户端不发 Origin → 放行
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

// 无 Origin 头（Go 客户端）→ 放行；有 Origin 头 → 严格校验 host 是否匹配
var controlUpgrader = websocket.Upgrader{
	CheckOrigin:  checkWSOrigin,
	Subprotocols: []string{protocol.WSSubProtocolControl},
}

var dataUpgrader = websocket.Upgrader{
	HandshakeTimeout:  10 * time.Second,
	ReadBufferSize:    32 * 1024,
	WriteBufferSize:   32 * 1024,
	CheckOrigin:       checkWSOrigin,
	EnableCompression: false,
	Subprotocols:      []string{protocol.WSSubProtocolData},
}

// securityHeadersHandler 统一注入安全响应头（P10）

func (s *Server) securityHeadersHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; font-src 'self' data:; "+
				"frame-ancestors 'none'; form-action 'self'")
		if s.isHTTPSRequest(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// --- 控制通道处理 ---

func (s *Server) handleControlWS(w http.ResponseWriter, r *http.Request) {
	conn, err := controlUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket 升级失败: %v", err)
		return
	}
	defer conn.Close()

	// 限制单条 WebSocket 消息大小，防止恶意客户端发送超大消息导致 OOM
	conn.SetReadLimit(wsMaxMessageSize)

	log.Printf("📡 新的控制通道连接: %s", r.RemoteAddr)

	// 等待 Client 发送认证消息
	client, err := s.handleAuth(conn, r.RemoteAddr)
	if err != nil {
		log.Printf("❌ Client 认证失败 [%s]: %v", r.RemoteAddr, err)
		return
	}

	log.Printf("✅ Client 已认证: %s (%s/%s) [ID: %s, generation=%d]", client.Info.Hostname, client.Info.OS, client.Info.Arch, client.ID, client.generation)

	if s.store != nil {
		if err := s.store.UpdateHostname(client.ID, client.Info.Hostname); err != nil {
			log.Printf("⚠️ 更新隧道展示主机名失败 [%s]: %v", client.ID, err)
		}
	}

	defer s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "control_loop_exit")

	// 持续读取控制消息
	s.controlLoop(client)
}

// handleAuth 处理 Client 的认证流程
// 未初始化时一律拒绝 Client 连接；初始化后认证优先级: Token > Key 兑换 Token
func (s *Server) handleAuth(conn *websocket.Conn, remoteAddr string) (*ClientConn, error) {
	// 速率限制检查
	ip := remoteIP(remoteAddr)
	if s.clientLimiter != nil {
		if allowed, retryAfter := s.clientLimiter.Allow(ip); !allowed {
			log.Printf("🚫 Client 认证被限速 [%s]: 需等待 %v", remoteAddr, retryAfter)
			slog.Warn("Client 认证被限速", "ip", ip, "module", "security")
			_ = writeAuthResult(conn, protocol.AuthResponse{
				Success:   false,
				Message:   "认证失败",
				Code:      protocol.AuthCodeRateLimited,
				Retryable: true,
			})
			return nil, fmt.Errorf("认证失败")
		}
	}

	authTimeout := s.authTimeout
	if authTimeout == 0 {
		authTimeout = 30 * time.Second
	}
	conn.SetReadDeadline(time.Now().Add(authTimeout))

	// 读取认证消息
	var msg protocol.Message
	if err := conn.ReadJSON(&msg); err != nil {
		return nil, fmt.Errorf("读取认证消息失败: %w", err)
	}

	// 认证消息已收到，清除读超时（后续 controlLoop 自行管理）
	conn.SetReadDeadline(time.Time{})
	if msg.Type != protocol.MsgTypeAuth {
		return nil, fmt.Errorf("期望认证消息，收到: %s", msg.Type)
	}

	var authReq protocol.AuthRequest
	if err := msg.ParsePayload(&authReq); err != nil {
		return nil, fmt.Errorf("解析认证数据失败: %w", err)
	}
	if authReq.InstallID == "" {
		return nil, fmt.Errorf("认证失败: install_id 不能为空")
	}

	var newToken string // 如果通过 Key 兑换，需要下发给客户端
	var clientID string

	if s.adminStore != nil {
		if !s.adminStore.IsInitialized() {
			log.Printf("⚠️ 服务未初始化，拒绝 Client 连接 [%s]", remoteAddr)
			slog.Warn("服务未初始化时拒绝 Client 连接", "ip", ip, "module", "security")
			if s.clientLimiter != nil {
				s.clientLimiter.RecordFailure(ip)
			}
			_ = writeAuthResult(conn, protocol.AuthResponse{
				Success:   false,
				Message:   "认证失败",
				Code:      protocol.AuthCodeServerUninitialized,
				Retryable: true,
			})
			return nil, fmt.Errorf("认证失败")
		}

		if authReq.Token != "" {
			clientToken, err := s.adminStore.ValidateClientToken(authReq.Token, authReq.InstallID)
			if err != nil {
				log.Printf("⚠️ Client Token 验证失败 [%s]: %v", remoteAddr, err)
				if s.clientLimiter != nil {
					s.clientLimiter.RecordFailure(ip)
				}
				code := protocol.AuthCodeInvalidToken
				if errors.Is(err, ErrClientTokenRevoked) {
					code = protocol.AuthCodeRevokedToken
				}
				_ = writeAuthResult(conn, protocol.AuthResponse{
					Success:    false,
					Message:    "认证失败",
					Code:       code,
					ClearToken: true,
				})
				return nil, fmt.Errorf("认证失败")
			}

			clientID = clientToken.ClientID
			if current, loaded := s.clients.Load(clientID); loaded {
				currentClient := current.(*ClientConn)
				if currentClient.getState() != clientStateClosing {
					log.Printf("⚠️ Token 并发连接被拒: client_id=%s, install_id=%s, remote=%s", clientID, authReq.InstallID, remoteAddr)
					_ = writeAuthResult(conn, protocol.AuthResponse{
						Success:   false,
						Message:   "认证失败",
						Code:      protocol.AuthCodeConcurrentSession,
						Retryable: true,
					})
					return nil, fmt.Errorf("认证失败")
				}
			}

			log.Printf("🔑 Client Token 认证通过 [install_id=%s]", authReq.InstallID)
			if s.clientLimiter != nil {
				s.clientLimiter.ResetFailures(ip)
			}
		} else {
			record, err := s.adminStore.GetOrCreateClient(authReq.InstallID, authReq.Client, remoteAddr)
			if err != nil {
				return nil, fmt.Errorf("登记 Client 失败: %w", err)
			}
			clientID = record.ID

			if current, loaded := s.clients.Load(clientID); loaded {
				currentClient := current.(*ClientConn)
				if currentClient.getState() != clientStateClosing {
					_ = writeAuthResult(conn, protocol.AuthResponse{
						Success:   false,
						Message:   "认证失败",
						Code:      protocol.AuthCodeConcurrentSession,
						Retryable: true,
					})
					return nil, fmt.Errorf("认证失败")
				}
			}

			tokenStr, _, err := s.adminStore.ExchangeToken(authReq.Key, authReq.InstallID, clientID, remoteAddr)
			if err != nil {
				log.Printf("❌ Client Key 兑换 Token 失败 [%s]: %v", remoteAddr, err)
				if s.clientLimiter != nil {
					s.clientLimiter.RecordFailure(ip)
				}
				_ = writeAuthResult(conn, protocol.AuthResponse{
					Success: false,
					Message: "认证失败",
					Code:    protocol.AuthCodeInvalidKey,
				})
				return nil, fmt.Errorf("认证失败")
			}
			newToken = tokenStr
			log.Printf("🔑 Client Key 兑换 Token 成功 [install_id=%s]", authReq.InstallID)
			if s.clientLimiter != nil {
				s.clientLimiter.ResetFailures(ip)
			}
		}
	}

	if clientID == "" {
		clientID = "unmanaged-" + authReq.InstallID
	}

	client := &ClientConn{
		ID:         clientID,
		InstallID:  authReq.InstallID,
		Info:       authReq.Client,
		RemoteAddr: remoteAddr,
		conn:       conn,
		proxies:    make(map[string]*ProxyTunnel),
		dataToken:  generateDataToken(),
		generation: s.nextClientGeneration(),
		state:      clientStatePendingData,
	}
	s.clients.Store(clientID, client)

	// 发送认证响应
	authResp := protocol.AuthResponse{
		Success:   true,
		Message:   "认证成功",
		ClientID:  clientID,
		Token:     newToken, // 仅 Key 兑换时非空
		DataToken: client.dataToken,
		Code:      protocol.AuthCodeOK,
	}
	if err := writeAuthResult(conn, authResp); err != nil {
		if current, ok := s.clients.Load(clientID); ok && current == client {
			_ = s.invalidateLogicalSessionIfCurrent(clientID, client.generation, "auth_response_failed")
		}
		return nil, fmt.Errorf("发送认证响应失败: %w", err)
	}

	s.startPendingDataTimer(client)
	return client, nil
}

// controlLoop 持续处理控制通道上的消息
func (s *Server) controlLoop(client *ClientConn) {
	client.mu.Lock()
	conn := client.conn
	client.mu.Unlock()
	if conn == nil {
		return
	}

	for {
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("⚠️ Client [%s] 连接异常: %v", client.ID, err)
			}
			return
		}

		switch msg.Type {
		case protocol.MsgTypePing:
			if !s.isCurrentLive(client.ID, client.generation) {
				continue
			}
			// 收到心跳，回复 Pong
			pong, _ := protocol.NewMessage(protocol.MsgTypePong, nil)
			client.mu.Lock()
			_ = conn.WriteJSON(pong)
			client.mu.Unlock()

		case protocol.MsgTypeProbeReport:
			if !s.isCurrentLive(client.ID, client.generation) {
				continue
			}
			// 收到探针数据
			var stats protocol.SystemStats
			if err := msg.ParsePayload(&stats); err != nil {
				log.Printf("⚠️ 解析探针数据失败 [%s]: %v", client.ID, err)
				continue
			}
			// 计算派生指标（网络速率等）
			client.enrichStats(&stats)
			client.SetStats(&stats)
			// 合并公网 IP 到 ClientInfo（探针附带）
			if stats.PublicIPv4 != "" {
				client.Info.PublicIPv4 = stats.PublicIPv4
			}
			if stats.PublicIPv6 != "" {
				client.Info.PublicIPv6 = stats.PublicIPv6
			}
			// 更新基准快照
			client.statsMu.Lock()
			client.prevStats = cloneSystemStats(&stats)
			client.prevStatsAt = time.Now()
			client.statsMu.Unlock()
			if s.adminStore != nil {
				if err := s.adminStore.UpdateClientStats(client.ID, client.Info, stats, client.RemoteAddr); err != nil {
					log.Printf("⚠️ 持久化 Client 最新状态失败 [%s]: %v", client.ID, err)
				}
			}
			log.Printf("📊 [%s] CPU: %.1f%% | 内存: %.1f%% | 磁盘: %.1f%%",
				client.Info.Hostname, stats.CPUUsage, stats.MemUsage, stats.DiskUsage)

			// 发布探针数据更新事件
			s.events.PublishJSON("stats_update", map[string]any{
				"client_id": client.ID,
				"stats":     stats,
			})

		case protocol.MsgTypeProxyCreate:
			if !s.isCurrentLive(client.ID, client.generation) {
				continue
			}
			// 收到创建代理隧道请求
			var req protocol.ProxyNewRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ 解析代理请求失败 [%s]: %v", client.ID, err)
				continue
			}

			err := s.StartProxy(client, req)
			var resp *protocol.Message
			if err != nil {
				log.Printf("❌ 创建代理失败 [%s]: %v", client.ID, err)
				resp, _ = protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
					Name:    req.Name,
					Success: false,
					Message: err.Error(),
				})
			} else {
				client.proxyMu.RLock()
				tunnel := client.proxies[req.Name]
				actualPort := tunnel.Config.RemotePort
				config := tunnel.Config
				client.proxyMu.RUnlock()

				resp, _ = protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
					Name:       req.Name,
					Success:    true,
					Message:    "代理隧道创建成功",
					RemotePort: actualPort,
				})

				s.emitTunnelChanged(client.ID, config, "created_by_client")
			}

			client.mu.Lock()
			_ = conn.WriteJSON(resp)
			client.mu.Unlock()

		case protocol.MsgTypeProxyProvisionAck:
			var ack protocol.ProxyProvisionAck
			if err := msg.ParsePayload(&ack); err != nil {
				log.Printf("⚠️ 解析 provisioning ack 失败 [%s]: %v", client.ID, err)
				continue
			}
			resp := provisionAckResult{
				name:     ack.Name,
				accepted: ack.Accepted,
				message:  ack.Message,
			}
			if s.resolveTunnelProvisionAckWaiter(client.ID, client.generation, resp) {
				continue
			}
			log.Printf("📩 收到未匹配的 provisioning ack [%s]: name=%s accepted=%v", client.ID, resp.name, resp.accepted)

		case protocol.MsgTypeProxyClose:
			if !s.isCurrentLive(client.ID, client.generation) {
				continue
			}
			var req protocol.ProxyCloseRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ 解析关闭代理请求失败 [%s]: %v", client.ID, err)
				continue
			}
			if err := s.StopProxy(client, req.Name); err != nil {
				log.Printf("⚠️ 关闭代理失败 [%s]: %v", client.ID, err)
			} else {
				s.emitTunnelChanged(client.ID, protocol.ProxyConfig{
					Name:         req.Name,
					ClientID:     client.ID,
					DesiredState: protocol.ProxyDesiredStateStopped,
					RuntimeState: protocol.ProxyRuntimeStateIdle,
				}, "closed_by_client")
			}

		default:
			log.Printf("⚠️ 未知消息类型 [%s]: %s", client.ID, msg.Type)
		}
	}
}

func writeAuthResult(conn *websocket.Conn, authResp protocol.AuthResponse) error {
	message, err := protocol.NewMessage(protocol.MsgTypeAuthResp, authResp)
	if err != nil {
		return err
	}
	return conn.WriteJSON(message)
}

// tokenCleanupLoop 定期清理过期 Token（每 6 小时执行一次）
func (s *Server) tokenCleanupLoop() {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if s.adminStore != nil {
				s.adminStore.CleanExpiredTokens()
			}
		}
	}
}

// --- Web 面板 ---

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	// 如果前端资源未嵌入（开发模式），返回提示信息
	if s.webFS == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, devModeHTML)
		return
	}

	// 生产模式：从 embed.FS 服务前端 SPA
	// 尝试直接匹配文件路径
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	// 去掉前导 /
	filePath := strings.TrimPrefix(path, "/")

	// 尝试打开文件
	f, err := s.webFS.Open(filePath)
	if err == nil {
		f.Close()
		// 文件存在，直接服务
		s.webHandler.ServeHTTP(w, r)
		return
	}

	// 文件不存在 → SPA fallback：返回 index.html
	// 这样前端路由 (/dashboard, /admin/keys 等) 都能正常工作
	indexFile, err := s.webFS.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer indexFile.Close()

	// 读取 index.html 的信息
	stat, err := indexFile.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 使用 http.ServeContent 以便正确设置 Content-Type 和缓存头
	rs, ok := indexFile.(readSeeker)
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, "index.html", stat.ModTime(), rs)
}

// --- API ---

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.getCachedServerStatus())
}

func (s *Server) collectSnapshot() consoleSnapshot {
	return consoleSnapshot{
		Clients:      s.collectClientViews(),
		ServerStatus: s.getCachedServerStatus(),
	}
}

func (s *Server) collectClientViews() []clientView {
	views := make(map[string]clientView)

	if s.adminStore != nil {
		for _, registered := range s.adminStore.GetRegisteredClients() {
			lastSeen := registered.LastSeen
			view := clientView{
				ID:          registered.ID,
				DisplayName: registered.DisplayName,
				Info:        registered.Info,
				Stats:       registered.Stats,
				Proxies:     []protocol.ProxyConfig{},
				Online:      false,
				LastSeen:    &lastSeen,
				LastIP:      registered.LastIP,
			}
			if s.store != nil {
				stored := s.store.GetTunnelsByClientID(registered.ID)
				view.Proxies = make([]protocol.ProxyConfig, 0, len(stored))
				for _, tunnel := range stored {
					view.Proxies = append(view.Proxies, proxyConfigForClientView(storedTunnelToProxyConfig(tunnel), false))
				}
			}
			views[registered.ID] = view
		}
	}

	s.clients.Range(func(_, value any) bool {
		client := value.(*ClientConn)
		if !client.isLive() {
			return true
		}
		proxies := make([]protocol.ProxyConfig, 0)
		client.RangeProxies(func(_ string, tunnel *ProxyTunnel) bool {
			proxies = append(proxies, proxyConfigForClientView(tunnel.Config, true))
			return true
		})
		sort.Slice(proxies, func(i, j int) bool { return proxies[i].Name < proxies[j].Name })

		view, ok := views[client.ID]
		if !ok {
			view = clientView{
				ID:      client.ID,
				Info:    client.Info,
				Proxies: []protocol.ProxyConfig{},
			}
		}
		now := time.Now()
		view.Info = client.Info
		if liveStats := client.GetStats(); liveStats != nil {
			view.Stats = liveStats
		}
		view.Proxies = proxies
		view.Online = true
		view.LastSeen = &now
		view.LastIP = remoteIP(client.RemoteAddr)
		views[client.ID] = view
		return true
	})

	clients := make([]clientView, 0, len(views))
	for _, client := range views {
		if client.Proxies == nil {
			client.Proxies = []protocol.ProxyConfig{}
		}
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].Info.Hostname < clients[j].Info.Hostname })

	return clients
}

// serverStatusLoop 后台定时采集服务端状态并缓存
func (s *Server) serverStatusLoop() {
	// 异步获取公网 IP（不阻塞首次状态采集）
	go s.refreshPublicIPs()

	// 首次立即采集
	status := s.collectServerStatus()
	s.cachedStatusMu.Lock()
	s.cachedStatus = &status
	s.cachedStatusMu.Unlock()

	statusTicker := time.NewTicker(10 * time.Second)
	defer statusTicker.Stop()

	publicIPTicker := time.NewTicker(5 * time.Minute)
	defer publicIPTicker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-statusTicker.C:
			status := s.collectServerStatus()
			s.cachedStatusMu.Lock()
			s.cachedStatus = &status
			s.cachedStatusMu.Unlock()
		case <-publicIPTicker.C:
			s.refreshPublicIPs()
		}
	}
}

// refreshPublicIPs 获取公网 IP 并更新缓存
func (s *Server) refreshPublicIPs() {
	ipv4, ipv6 := netutil.FetchPublicIPs()
	s.publicIPMu.Lock()
	if ipv4 != "" {
		s.publicIPv4 = ipv4
	}
	if ipv6 != "" {
		s.publicIPv6 = ipv6
	}
	s.publicIPMu.Unlock()
	if ipv4 != "" || ipv6 != "" {
		log.Printf("🌐 公网 IP 已刷新: IPv4=%s IPv6=%s", ipv4, ipv6)
	}
}

// getCachedServerStatus 返回后台采集的服务端状态缓存
func (s *Server) getCachedServerStatus() serverStatusView {
	s.cachedStatusMu.RLock()
	defer s.cachedStatusMu.RUnlock()
	if s.cachedStatus != nil {
		return *s.cachedStatus
	}
	// fallback: 如果还没来得及采集，现场采集一次
	return s.collectServerStatus()
}

func (s *Server) collectServerStatus() serverStatusView {
	clientCount := 0
	tunnelActive := 0
	tunnelPaused := 0
	tunnelStopped := 0

	s.clients.Range(func(_, value any) bool {
		clientCount++
		a := value.(*ClientConn)
		a.RangeProxies(func(_ string, t *ProxyTunnel) bool {
			switch {
			case isTunnelExposed(t.Config):
				tunnelActive++
			case t.Config.DesiredState == protocol.ProxyDesiredStatePaused && t.Config.RuntimeState == protocol.ProxyRuntimeStateIdle:
				tunnelPaused++
			case t.Config.DesiredState == protocol.ProxyDesiredStateStopped && t.Config.RuntimeState == protocol.ProxyRuntimeStateIdle:
				tunnelStopped++
			}
			return true
		})
		return true
	})

	serverAddr := ""
	var allowedPorts []PortRange
	if s.adminStore != nil {
		config := s.adminStore.GetServerConfig()
		serverAddr = config.ServerAddr
		allowedPorts = config.AllowedPorts
	}
	if allowedPorts == nil {
		allowedPorts = []PortRange{}
	}

	osArch := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	goVersion := runtime.Version()
	goroutines := runtime.NumGoroutine()
	hostname, _ := os.Hostname()

	ipAddr := netutil.GetOutboundIP()

	// 读取缓存的公网 IP（由后台 publicIPLoop 定时更新）
	s.publicIPMu.RLock()
	pubV4 := s.publicIPv4
	pubV6 := s.publicIPv6
	s.publicIPMu.RUnlock()

	cpuPercents, _ := cpu.Percent(0, false)
	cpuUsage := 0.0
	if len(cpuPercents) > 0 {
		cpuUsage = cpuPercents[0]
	}
	cpuCores, _ := cpu.Counts(true)

	v, _ := mem.VirtualMemory()
	memUsed := uint64(0)
	memTotal := uint64(0)
	if v != nil {
		memUsed = v.Used
		memTotal = v.Total
	}

	var diskPartitions []protocol.DiskPartition
	diskUsed := uint64(0)
	diskTotal := uint64(0)

	partitions, err := disk.Partitions(false)
	if err == nil {
		seenDevices := map[string]bool{}
		for _, p := range partitions {
			// Filter virtual/pseudo filesystems
			switch p.Fstype {
			case "tmpfs", "devtmpfs", "devfs", "squashfs", "overlay", "proc", "sysfs",
				"cgroup", "cgroup2", "pstore", "securityfs", "debugfs", "tracefs", "autofs":
				continue
			}
			if strings.HasPrefix(p.Fstype, "fuse.") {
				continue
			}

			// Determine dedup key:
			// - APFS (macOS): multiple volumes share one physical disk's capacity,
			//   so dedup by base disk name (e.g. /dev/disk3s1s1 → "disk3")
			// - Other FS (Linux ext4/xfs, Windows NTFS): each partition has its own
			//   real capacity, so dedup by exact device path to avoid double counting
			//   bind-mounts of the same partition only.
			dedupKey := p.Device
			if p.Fstype == "apfs" {
				dedupKey = baseDiskName(p.Device)
			}
			if seenDevices[dedupKey] {
				continue
			}

			d, err := disk.Usage(p.Mountpoint)
			if err == nil && d.Total > 0 {
				seenDevices[dedupKey] = true
				diskPartitions = append(diskPartitions, protocol.DiskPartition{
					Path:  p.Mountpoint,
					Used:  d.Used,
					Total: d.Total,
				})
				diskUsed += d.Used
				diskTotal += d.Total
			}
		}
	}

	// Fallback if no valid partitions found
	if len(diskPartitions) == 0 {
		d, _ := disk.Usage(filepath.Dir(s.getStorePath()))
		if d == nil {
			d, _ = disk.Usage("/")
		}
		if d != nil {
			diskUsed = d.Used
			diskTotal = d.Total
			diskPartitions = append(diskPartitions, protocol.DiskPartition{
				Path:  d.Path,
				Used:  d.Used,
				Total: d.Total,
			})
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	appMemUsed := m.Alloc
	appMemSys := m.Sys

	// 服务端系统开机时长
	sysUptime, _ := host.Uptime()

	// 系统安装时间
	osInstallTime := int64(sysinfo.GetOSInstallTime())

	return serverStatusView{
		Status:         "running",
		ClientCount:    clientCount,
		Version:        buildversion.Current,
		ListenPort:     s.Port,
		Uptime:         int64(time.Since(s.startTime).Seconds()),
		SystemUptime:   int64(sysUptime),
		OSInstallTime:  osInstallTime,
		StorePath:      s.getStorePath(),
		TunnelActive:   tunnelActive,
		TunnelPaused:   tunnelPaused,
		TunnelStopped:  tunnelStopped,
		ServerAddr:     serverAddr,
		AllowedPorts:   allowedPorts,
		OSArch:         osArch,
		GoVersion:      goVersion,
		Hostname:       hostname,
		IPAddress:      ipAddr,
		CPUUsage:       cpuUsage,
		CPUCores:       cpuCores,
		MemUsed:        memUsed,
		MemTotal:       memTotal,
		AppMemUsed:     appMemUsed,
		AppMemSys:      appMemSys,
		DiskUsed:       diskUsed,
		DiskTotal:      diskTotal,
		DiskPartitions: diskPartitions,
		GoroutineCount: goroutines,
		PublicIPv4:     pubV4,
		PublicIPv6:     pubV6,
	}
}

// baseDiskName 从设备路径提取物理磁盘基础名，用于去重。
// macOS APFS: /dev/disk3s1s1, /dev/disk3s5 → "disk3"
// Linux SCSI: /dev/sda1 → "sda"
// Linux NVMe: /dev/nvme0n1p1 → "nvme0n1"
var reDiskBase = regexp.MustCompile(`(disk\d+|sd[a-z]+|nvme\d+n\d+|[A-Z]:)`)

func baseDiskName(device string) string {
	m := reDiskBase.FindString(device)
	if m != "" {
		return m
	}
	return device // fallback: 用完整路径
}

// getStorePath 获取实际的 store 路径
func (s *Server) getStorePath() string {
	if s.store != nil {
		return s.store.path
	}
	return s.StorePath
}

func (s *Server) handleAPIClients(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.collectClientViews())
}

// --- Client 展示名 API ---

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

	if s.adminStore == nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": "admin store unavailable"})
		return
	}

	if err := s.adminStore.UpdateClientDisplayName(clientID, req.DisplayName); err != nil {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}

	encodeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"display_name": req.DisplayName,
	})
}

// --- 隧道 CRUD API ---

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

	// 检查隧道是否存在且为 active 状态
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

	// 检查隧道是否为 paused 或 stopped 状态
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

	// 检查隧道是否存在
	client.proxyMu.RLock()
	tunnel, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "隧道不存在"})
		return
	}

	// 只有暂停、已停止或异常状态才能删除
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

	// 检查隧道是否存在
	client.proxyMu.RLock()
	tunnel, exists := client.proxies[tunnelName]
	client.proxyMu.RUnlock()
	if !exists {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": "隧道不存在"})
		return
	}

	// 只有暂停、已停止或异常状态才能编辑
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

// restoreTunnels 在 Client 重连后恢复之前的隧道配置
func (s *Server) restoreTunnels(client *ClientConn) {
	if s.store == nil {
		return
	}
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}

	tunnels := s.store.GetTunnelsByClientID(client.ID)
	if len(tunnels) == 0 {
		return
	}

	restoredCount := 0
	for _, st := range tunnels {
		if !s.isCurrentLive(client.ID, client.generation) {
			return
		}
		// 检查端口是否仍在白名单范围内
		if st.RemotePort != 0 && s.adminStore != nil && s.adminStore.IsInitialized() && !s.adminStore.IsPortAllowed(st.RemotePort) {
			log.Printf("⚠️ 隧道 %s 端口 %d 不在当前允许范围内，标记为 error", st.Name, st.RemotePort)
			errMsg := fmt.Sprintf("端口 %d 不在允许范围内", st.RemotePort)
			client.proxyMu.Lock()
			config := protocol.ProxyConfig{
				Name:       st.Name,
				Type:       st.Type,
				LocalIP:    st.LocalIP,
				LocalPort:  st.LocalPort,
				RemotePort: st.RemotePort,
				Domain:     st.Domain,
				ClientID:   client.ID,
			}
			setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			client.proxies[st.Name] = &ProxyTunnel{
				Config: config,
				done:   make(chan struct{}),
			}
			client.proxyMu.Unlock()
			_ = s.persistTunnelStates(client.ID, st.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			eventConfig := protocol.ProxyConfig{
				Name:       st.Name,
				Type:       st.Type,
				RemotePort: st.RemotePort,
				Domain:     st.Domain,
				ClientID:   client.ID,
			}
			setProxyConfigStates(&eventConfig, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			s.emitTunnelChanged(client.ID, eventConfig, "port_not_allowed")
			restoredCount++
			continue
		}

		switch {
		case st.DesiredState == protocol.ProxyDesiredStateRunning &&
			(st.RuntimeState == protocol.ProxyRuntimeStateExposed || st.RuntimeState == protocol.ProxyRuntimeStatePending || st.RuntimeState == protocol.ProxyRuntimeStateOffline):
			log.Printf("🔄 恢复隧道: %s (:%d → %s:%d)", st.Name, st.RemotePort, st.LocalIP, st.LocalPort)
			if err := s.restoreManagedTunnel(client, st); err != nil {
				log.Printf("⚠️ 恢复隧道失败 [%s]: %v", st.Name, err)
				continue
			}
			restoredCount++

		default:
			config := protocol.ProxyConfig{
				Name:       st.Name,
				Type:       st.Type,
				LocalIP:    st.LocalIP,
				LocalPort:  st.LocalPort,
				RemotePort: st.RemotePort,
				Domain:     st.Domain,
				ClientID:   client.ID,
			}
			setProxyConfigStates(&config, st.DesiredState, st.RuntimeState, st.Error)
			client.proxyMu.Lock()
			client.proxies[st.Name] = &ProxyTunnel{
				Config: config,
				done:   make(chan struct{}),
			}
			client.proxyMu.Unlock()
			restoredCount++
		}
	}

	// 恢复完成后一次性通知前端刷新
	if restoredCount > 0 && s.isCurrentLive(client.ID, client.generation) {
		s.events.PublishJSON("tunnel_changed", map[string]any{
			"client_id": client.ID,
			"action":    "restored_batch",
			"count":     restoredCount,
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

// readSeeker 是 io.ReadSeeker 接口的别名，用于 http.ServeContent
type readSeeker interface {
	Read(p []byte) (n int, err error)
	Seek(offset int64, whence int) (int64, error)
}

// devModeHTML 开发模式占位页面
const devModeHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>NetsGo — 开发模式</title>
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
            max-width: 520px;
        }
        h1 { font-size: 2.5rem; margin-bottom: 0.5rem; }
        h1 span { color: #7c3aed; }
        p { color: #a0a0b0; font-size: 1.1rem; margin: 0.5rem 0; }
        .badge {
            display: inline-block; margin-top: 1rem; padding: 0.4rem 1rem;
            background: #7c3aed; border-radius: 20px; font-size: 0.85rem;
        }
        code {
            display: block; margin-top: 1rem; padding: 0.8rem 1.2rem;
            background: rgba(255,255,255,0.08); border-radius: 8px;
            font-family: 'JetBrains Mono', 'Fira Code', monospace;
            font-size: 0.9rem; color: #c4b5fd; text-align: left;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>🚀 <span>NetsGo</span></h1>
        <p>服务端已启动 — 开发模式</p>
        <p>前端资源未嵌入，请独立启动 Vite 开发服务器：</p>
        <code>cd web && bun run dev</code>
        <p>然后访问 Vite 管理面板地址（默认 <a href="http://localhost:5173" style="color:#a78bfa">localhost:5173</a>）。</p>
        <div class="badge">Dev Mode 🔧</div>
    </div>
</body>
</html>`
