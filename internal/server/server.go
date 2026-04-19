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

// Server is the core server struct.
type Server struct {
	Port                        int
	DataDir                     string
	AllowLoopbackManagementHost bool
	TLS                         *TLSConfig
	TLSFingerprint              string
	clients                     sync.Map          // stable clientID -> *ClientConn
	events                      *EventBus         // SSE event bus
	store                       *TunnelStore      // tunnel persistent store
	trafficStore                *TrafficStore     // traffic history store
	startTime                   time.Time         // server start time
	auth                        *AuthService      // auth and access control (adminStore, rate limiting)
	webFS                       fs.FS             // embedded frontend static assets (nil in dev mode)
	webHandler                  http.Handler      // cached FileServer (nil in dev mode)
	cachedStatus                *serverStatusView // latest server status collected in background
	cachedStatusMu              sync.RWMutex      // protects cachedStatus
	sessions                    *SessionManager   // connection lifecycle (managedConns, longLivedHandlers, generations, data timeout)
	httpServer                  *http.Server
	listener                    net.Listener
	done                        chan struct{}
	tlsEnabled                  bool
	publicIPv4                  string          // cached public IPv4
	publicIPv6                  string          // cached public IPv6
	publicIPMu                  sync.RWMutex    // protects public IP cache
	tunnels                     *TunnelRegistry // tunnel provision wait and timeout
}

// ClientConn represents a connected client.
type ClientConn struct {
	ID           string
	InstallID    string
	Info         protocol.ClientInfo
	infoMu       sync.RWMutex
	RemoteAddr   string
	bandwidthMu  sync.RWMutex
	bandwidth    protocol.BandwidthSettings
	bandwidthRT  *directionalBandwidthRuntime
	stats        *protocol.SystemStats
	prevStats    *protocol.SystemStats // previous probe snapshot (used to compute rates)
	prevStatsAt  time.Time             // time of previous snapshot
	statsMu      sync.RWMutex          // protects stats / prevStats
	conn         *websocket.Conn
	mu           sync.Mutex
	dataSession  *yamux.Session // data channel yamux session
	dataMu       sync.RWMutex   // protects dataSession
	dataToken    string
	generation   uint64
	state        clientState
	stateMu      sync.RWMutex
	pendingTimer *time.Timer
	proxies      map[string]*ProxyTunnel // proxy tunnels: name -> tunnel
	proxyMu      sync.RWMutex            // protects proxies
}

// New creates a new Server instance.
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

// RangeClients iterates over all connected clients.
func (s *Server) RangeClients(fn func(id string, client *ClientConn) bool) {
	s.clients.Range(func(key, value any) bool {
		return fn(key.(string), value.(*ClientConn))
	})
}
