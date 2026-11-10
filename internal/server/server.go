package server

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"netsgo/internal/installmethod"
	"netsgo/pkg/protocol"
)

// Server is the core server struct.
type Server struct {
	Port                                int
	DataDir                             string
	AllowLoopbackManagementHost         bool
	TLS                                 *TLSConfig
	TLSFingerprint                      string
	clients                             sync.Map            // stable clientID -> *ClientConn
	events                              *EventBus           // SSE event bus
	store                               *TunnelStore        // tunnel persistent store
	trafficStore                        *TrafficStore       // traffic history store
	trafficAccumulator                  *trafficAccumulator // batched traffic observations waiting to be applied to trafficStore
	serverDB                            *sql.DB             // owned shared SQLite handle for borrowed server stores; close only via closeServerDB
	activityStore                       *ActivityStore      // durable activity timeline over serverDB
	webhookStore                        *WebhookStore       // user-owned activity Webhooks and durable delivery queue
	webhookDispatcher                   *webhookDispatcher  // single-instance outbound request scheduler
	activityBootID                      string              // random per complete server start; scopes lifecycle dedupe keys
	serverDBCloseOnce                   sync.Once
	serverDBCloseErr                    error
	sseConnectionMu                     sync.Mutex
	sseConnections                      *sseConnectionRegistry
	clientTunnelMutationMu              sync.Mutex   // serializes registered-client deletion with tunnel target migration
	userManagementMu                    sync.Mutex   // serializes user status/admin/delete transactions and last-admin checks
	adminAuthorizationMu                sync.RWMutex // serializes privileged commits with role, status, and session changes
	adminAuthorizationHook              func(stage string, principal *RequestPrincipal)
	userLifecycleLocks                  sync.Map // userID -> *userLifecycleGate; entries live for the Server lifetime
	userLifecycleHook                   func(stage, userID string)
	userConvergenceHook                 func(context.Context, string) error
	userConvergenceTimeout              time.Duration
	serverConfigMutationMu              sync.Mutex        // serializes config persistence with port-policy enforcement
	tunnelEventMu                       sync.Mutex        // preserves tunnel_changed ordering across state checks and publication
	startTime                           time.Time         // server start time
	auth                                *AuthService      // auth and access control (adminStore, rate limiting)
	webFS                               fs.FS             // embedded frontend static assets (nil in dev mode)
	webHandler                          http.Handler      // cached FileServer (nil in dev mode)
	devMode                             bool              // built with the dev tag; unknown non-tunnel hosts may enter the admin console
	cachedStatus                        *serverStatusView // latest server status collected in background
	cachedStatusMu                      sync.RWMutex      // protects cachedStatus
	sessions                            *SessionManager   // connection lifecycle (managedConns, longLivedHandlers, generations, data timeout)
	httpServer                          *http.Server
	sniListener                         *sniListener
	listener                            net.Listener
	stunConn                            net.PacketConn
	done                                chan struct{}
	doneCloseOnce                       sync.Once
	tlsEnabled                          bool
	publicIPv4                          string          // cached public IPv4
	publicIPv6                          string          // cached public IPv6
	publicIPMu                          sync.RWMutex    // protects public IP cache
	tunnels                             *TunnelRegistry // tunnel provision wait and timeout
	unifiedRuntime                      *unifiedTunnelRuntimeRegistry
	unifiedReconcile                    *unifiedTunnelReconcileRegistry
	tunnelRuntimeOps                    *tunnelRuntimeOperationRegistry
	c2c                                 *clientRelayRegistry
	p2p                                 *p2pCoordinator
	p2pRetryMu                          sync.Mutex
	p2pRetries                          map[string]p2pRetryState
	p2pProjectionMu                     sync.Mutex
	p2pProjectionRetries                map[string]p2pProjectionRetryItem
	p2pProjectionWake                   chan struct{}
	p2pProjectionStop                   chan struct{}
	p2pProjectionDone                   chan struct{}
	releaseIndexCache                   *releaseIndexCache
	updateCapabilityCache               *updateCapabilityCache // cached server install capability for status API
	serverExposeActivatedHook           func(StoredTunnel, *ProxyTunnel)
	runtimeErrorCleanupHook             func(protocol.ProxyConfig)
	portPolicyAfterConfigSaveHook       func()
	portPolicyAfterRuntimeCleanupHook   func(affectedTunnel)
	p2pSignalDropHook                   func(string, string, protocol.P2PSignal) bool
	restorePlaceholderBeforeInstallHook func(StoredTunnel, string)
	controlAuthBeforeResponseHook       func(*ClientConn)
}

// ClientConn represents a connected client.
type ClientConn struct {
	ID string
	// OwnerUserID is resolved exclusively by Server control-channel
	// authentication. It is never accepted from a Client protocol message.
	OwnerUserID    string
	OwnerEpoch     uint64
	InstallID      string
	Info           protocol.ClientInfo
	infoMu         sync.RWMutex
	RemoteAddr     string
	bandwidthMu    sync.RWMutex
	bandwidth      protocol.BandwidthSettings
	bandwidthRT    *directionalBandwidthRuntime
	stats          *protocol.SystemStats
	prevStats      *protocol.SystemStats // previous probe snapshot (used to compute rates)
	prevStatsAt    time.Time             // time of previous snapshot
	statsMu        sync.RWMutex          // protects stats / prevStats
	conn           *websocket.Conn
	mu             sync.Mutex     // protects the control connection pointer
	writeMu        sync.Mutex     // serializes control-channel writers without blocking connection teardown
	dataSession    *yamux.Session // data channel yamux session
	dataMu         sync.RWMutex   // protects dataSession
	dataToken      string
	clientTokenID  string
	tokenTouchMu   sync.Mutex
	nextTokenTouch time.Time
	generation     uint64
	lifecycleMu    sync.Mutex // serializes promotion and invalidation for one generation
	state          clientState
	stateMu        sync.RWMutex
	pendingTimer   *time.Timer
	proxies        map[string]*ProxyTunnel // proxy tunnels: name -> tunnel
	proxyMu        sync.RWMutex            // protects proxies
}

// New creates a new Server instance.
func New(port int) *Server {
	s := &Server{
		Port:                        port,
		AllowLoopbackManagementHost: true,
		events:                      NewEventBus(),
		sseConnections:              newSSEConnectionRegistry(),
		trafficAccumulator:          newTrafficAccumulator(),
		auth:                        newAuthService(),
		sessions:                    newSessionManager(),
		tunnels:                     newTunnelRegistry(),
		unifiedRuntime:              newUnifiedTunnelRuntimeRegistry(),
		unifiedReconcile:            newUnifiedTunnelReconcileRegistry(),
		tunnelRuntimeOps:            newTunnelRuntimeOperationRegistry(),
		c2c:                         newClientRelayRegistry(),
		p2p:                         newP2PCoordinator(time.Now),
		p2pRetries:                  make(map[string]p2pRetryState),
		p2pProjectionRetries:        make(map[string]p2pProjectionRetryItem),
		p2pProjectionWake:           make(chan struct{}, 1),
		p2pProjectionStop:           make(chan struct{}),
		p2pProjectionDone:           make(chan struct{}),
		startTime:                   time.Now(),
		done:                        make(chan struct{}),
		activityBootID:              generateUUID(),
	}
	s.releaseIndexCache = newReleaseIndexCache(fetchDefaultReleaseIndex)
	s.updateCapabilityCache = newUpdateCapabilityCache(installmethod.Detect)
	return s
}

// RangeClients iterates over all connected clients.
func (s *Server) RangeClients(fn func(id string, client *ClientConn) bool) {
	s.clients.Range(func(key, value any) bool {
		return fn(key.(string), value.(*ClientConn))
	})
}
