package server

import (
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"netsgo/pkg/protocol"
)

// Server 是服务端的核心结构体
type Server struct {
	Port                        int
	DataDir                     string
	AllowLoopbackManagementHost bool
	TLS                         *TLSConfig
	TLSFingerprint              string
	clients                     sync.Map          // stable clientID -> *ClientConn
	events                      *EventBus         // SSE 事件总线
	store                       *TunnelStore      // 隧道持久化存储
	trafficStore                *TrafficStore     // 流量历史存储
	startTime                   time.Time         // 服务器启动时间
	auth                        *AuthService      // 认证与访问控制（adminStore、速率限制）
	webFS                       fs.FS             // 嵌入的前端静态资源 (nil 表示开发模式)
	webHandler                  http.Handler      // 缓存的 FileServer (nil 表示开发模式)
	cachedStatus                *serverStatusView // 后台采集的最新服务端状态
	cachedStatusMu              sync.RWMutex      // 保护 cachedStatus
	sessions                    *SessionManager   // 连接生命周期（managedConns、longLivedHandlers、代际、data 超时）
	httpServer                  *http.Server
	listener                    net.Listener
	done                        chan struct{}
	tlsEnabled                  bool
	publicIPv4                  string          // 缓存的公网 IPv4
	publicIPv6                  string          // 缓存的公网 IPv6
	publicIPMu                  sync.RWMutex    // 保护公网 IP 缓存
	tunnels                     *TunnelRegistry // 隧道 provision 等待与超时
}

// ClientConn 代表一个已连接的 Client
type ClientConn struct {
	ID           string
	InstallID    string
	Info         protocol.ClientInfo
	infoMu       sync.RWMutex
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

// New 创建一个新的 Server 实例
func New(port int) *Server {
	return &Server{
		Port:      port,
		events:    NewEventBus(),
		auth:      newAuthService(),
		sessions:  newSessionManager(),
		tunnels:   newTunnelRegistry(),
		startTime: time.Now(),
		done:      make(chan struct{}),
	}
}

// RangeClients 遍历所有已连接的 Client
func (s *Server) RangeClients(fn func(id string, client *ClientConn) bool) {
	s.clients.Range(func(key, value any) bool {
		return fn(key.(string), value.(*ClientConn))
	})
}
