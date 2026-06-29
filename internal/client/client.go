package client

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"netsgo/internal/clientaddr"
	"netsgo/internal/installmethod"
	"netsgo/internal/svcmgr"
	"netsgo/pkg/mux"
	"netsgo/pkg/netutil"
	"netsgo/pkg/protocol"
	buildversion "netsgo/pkg/version"
)

const wsDataMaxMessageSize = 512 * 1024

const (
	retryShortInterval     = 3 * time.Second
	retryLongInterval      = 10 * time.Second
	retryLongIntervalAfter = 5 * time.Minute
	retryJitterMultiplier  = 0.5
	publicIPRefreshAfter   = 2 * time.Hour
)

var (
	retryJitterFloat64 = rand.Float64
	fetchPublicIPs     = netutil.FetchPublicIPs
)

// Client is the core client structure.
type Client struct {
	ServerAddr          string // Server address (supports ws://, wss://, http://, and https://, normalized internally)
	Key                 string // Authentication key (used to exchange for a token)
	Token               string // Client connection token (exchanged from Key)
	InstallID           string // Stable installation ID
	DataDir             string
	ClientID            string // Stable client ID assigned by the server
	TLSSkipVerify       bool
	TLSFingerprint      string
	dataToken           string
	conn                *websocket.Conn
	mu                  sync.Mutex // Protects the current runtime and mirrored fields
	done                chan struct{}
	dataSession         *yamux.Session // yamux session for the data channel
	dataMu              sync.RWMutex
	proxies             sync.Map // proxy_name -> ProxyNewRequest
	socks5Targets       sync.Map // tunnel_id -> clientSOCKS5TargetRuntime
	fixedTargetRuntimes sync.Map // tunnel_id -> fixedServiceTargetRuntime
	tunnels             sync.Map // tunnel_id:role -> *clientTunnelRuntime
	useTLS              bool
	startTime           time.Time // Program start time, used to calculate process uptime
	publicIPv4          string    // Cached public IPv4 address
	publicIPv6          string    // Cached public IPv6 address
	publicIPFetched     time.Time // Last fetch time
	publicIPFetching    bool      // Public IP refresh is currently running
	// ProxyConfigs are delivered by the server and may also be set manually in benchmarks.
	ProxyConfigs []protocol.ProxyNewRequest
	// DisableReconnect disables automatic reconnect (used in tests and similar scenarios).
	DisableReconnect bool
	Logger           *EventLogger

	dataHandshakeTimeout time.Duration
	currentRuntime       *sessionRuntime
	nextRuntimeEpoch     atomic.Uint64
}

type clientRunError struct {
	message string
	fatal   bool
}

func (e *clientRunError) Error() string {
	return e.message
}

type sessionRuntime struct {
	epoch       uint64
	done        chan struct{}
	doneOnce    sync.Once
	wg          sync.WaitGroup
	conn        *websocket.Conn
	connMu      sync.Mutex
	dataSession *yamux.Session
	dataMu      sync.RWMutex
}

func (rt *sessionRuntime) writeJSON(v any) error {
	rt.connMu.Lock()
	defer rt.connMu.Unlock()
	if rt.conn == nil {
		return fmt.Errorf("control channel unavailable")
	}
	return rt.conn.WriteJSON(v)
}

func (rt *sessionRuntime) writeControl(messageType int, data []byte, deadline time.Time) error {
	rt.connMu.Lock()
	defer rt.connMu.Unlock()
	if rt.conn == nil {
		return fmt.Errorf("control channel unavailable")
	}
	return rt.conn.WriteControl(messageType, data, deadline)
}

func (rt *sessionRuntime) detachConn() *websocket.Conn {
	rt.connMu.Lock()
	defer rt.connMu.Unlock()
	conn := rt.conn
	rt.conn = nil
	return conn
}

func newSessionRuntime(epoch uint64) *sessionRuntime {
	return &sessionRuntime{
		epoch: epoch,
		done:  make(chan struct{}),
	}
}

func (rt *sessionRuntime) closeDone() {
	rt.doneOnce.Do(func() {
		close(rt.done)
	})
}

// New creates a new client instance.
func New(serverAddr, key string) *Client {
	return &Client{
		ServerAddr:           serverAddr,
		Key:                  key,
		done:                 make(chan struct{}),
		startTime:            time.Now(),
		dataHandshakeTimeout: 10 * time.Second,
	}
}

func (c *Client) logger() *EventLogger {
	if c.Logger == nil {
		c.Logger = NewEventLogger(LogFormatText, nil)
	}
	return c.Logger
}

func (c *Client) beginRuntime() *sessionRuntime {
	rt := newSessionRuntime(c.nextRuntimeEpoch.Add(1))

	c.mu.Lock()
	c.currentRuntime = rt
	c.conn = nil
	c.done = rt.done
	c.mu.Unlock()

	c.dataMu.Lock()
	c.dataSession = nil
	c.dataMu.Unlock()

	return rt
}

func (c *Client) getCurrentRuntime() *sessionRuntime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentRuntime
}

func (c *Client) clearCurrentRuntime(rt *sessionRuntime) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.currentRuntime != rt {
		return
	}
	c.currentRuntime = nil
	c.conn = nil
	c.done = make(chan struct{})
	c.dataMu.Lock()
	c.dataSession = nil
	c.dataMu.Unlock()
}

func (c *Client) setRuntimeConn(rt *sessionRuntime, conn *websocket.Conn) {
	rt.connMu.Lock()
	rt.conn = conn
	rt.connMu.Unlock()

	c.mu.Lock()
	if c.currentRuntime == rt || c.currentRuntime == nil {
		c.conn = conn
	}
	c.mu.Unlock()
}

func (c *Client) setRuntimeDataSession(rt *sessionRuntime, session *yamux.Session) {
	c.mu.Lock()
	shouldMirror := c.currentRuntime == rt || c.currentRuntime == nil
	if shouldMirror {
		c.dataMu.Lock()
		rt.dataMu.Lock()
		rt.dataSession = session
		rt.dataMu.Unlock()
		c.dataSession = session
		c.dataMu.Unlock()
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	rt.dataMu.Lock()
	rt.dataSession = session
	rt.dataMu.Unlock()
}

func (c *Client) runtimeForStandaloneUse() *sessionRuntime {
	rt := &sessionRuntime{}

	c.mu.Lock()
	rt.done = c.done
	rt.conn = c.conn
	c.mu.Unlock()
	c.dataMu.RLock()
	rt.dataSession = c.dataSession
	c.dataMu.RUnlock()
	if rt.done == nil {
		rt.done = make(chan struct{})
	}
	return rt
}

// normalizeServerAddr normalizes the user-provided address to a consistent format.
// Supported inputs: ws://, wss://, http://, https://
// Output: http://host:port or https://host:port
// It also sets the c.useTLS flag.
func (c *Client) normalizeServerAddr() {
	c.mu.Lock()
	defer c.mu.Unlock()

	normalized, err := clientaddr.Normalize(c.ServerAddr, clientaddr.ModeRuntime)
	if err != nil {
		return
	}
	c.ServerAddr = normalized.BaseURL
	c.useTLS = normalized.UseTLS
}

// deriveControlURL derives the control-channel WebSocket URL from the normalized ServerAddr.
// http://host:port -> ws://host:port/ws/control
// https://host:port -> wss://host:port/ws/control
func (c *Client) currentServerState() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ServerAddr, c.useTLS
}

func (c *Client) deriveControlURL() string {
	addr, useTLS := c.currentServerState()
	if normalized, err := clientaddr.Normalize(addr, clientaddr.ModeRuntime); err == nil {
		return normalized.ControlURL
	}
	if useTLS {
		addr = "wss://" + strings.TrimPrefix(addr, "https://")
	} else {
		addr = "ws://" + strings.TrimPrefix(addr, "http://")
	}
	return addr + "/ws/control"
}

func (c *Client) deriveDataURL() string {
	addr, useTLS := c.currentServerState()
	if normalized, err := clientaddr.Normalize(addr, clientaddr.ModeRuntime); err == nil {
		return normalized.DataURL
	}
	if useTLS {
		addr = "wss://" + strings.TrimPrefix(addr, "https://")
	} else {
		addr = "ws://" + strings.TrimPrefix(addr, "http://")
	}
	return addr + "/ws/data"
}

// buildTLSConfig builds the client TLS configuration.
func (c *Client) CurrentClientID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ClientID
}

func (c *Client) CurrentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Token
}

func (c *Client) CurrentTLSFingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.TLSFingerprint
}

func (c *Client) UsesTLS() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.useTLS
}

func (c *Client) currentAuthState() (clientID, dataToken, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ClientID, c.dataToken, c.Token
}

func (c *Client) setToken(token string) {
	c.mu.Lock()
	c.Token = token
	c.mu.Unlock()
}

func (c *Client) buildTLSConfig(host string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: c.TLSSkipVerify,
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
	}
}

func (c *Client) newWSDialer(host string) *websocket.Dialer {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = c.dataHandshakeTimeout
	dialer.ReadBufferSize = 32 * 1024
	dialer.WriteBufferSize = 32 * 1024
	dialer.EnableCompression = false
	if c.useTLS {
		dialer.TLSClientConfig = c.buildTLSConfig(host)
	}
	return &dialer
}

// retryInterval calculates the retry interval based on the initial disconnect time.
// It uses 3s for the first 5 minutes, then 10s afterward, with positive jitter
// to avoid large numbers of clients reconnecting in lockstep after a mass disconnect.
func retryInterval(disconnectTime time.Time) time.Duration {
	return retryIntervalWithJitter(disconnectTime, retryJitterFloat64())
}

func retryIntervalWithJitter(disconnectTime time.Time, jitter float64) time.Duration {
	elapsed := time.Since(disconnectTime)
	var base time.Duration
	if elapsed < retryLongIntervalAfter {
		base = retryShortInterval
	} else {
		base = retryLongInterval
	}

	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}

	return base + time.Duration(float64(base)*retryJitterMultiplier*jitter)
}

// Start starts the client, connects to the server, and begins work.
// If the connection drops, it reconnects automatically except for unrecoverable local errors.
func (c *Client) Start() error {
	for {
		err := c.connectAndRun()
		if err != nil {
			// Authentication failure is fatal and should not trigger reconnect.
			if c.DisableReconnect {
				return err
			}
			if isFatalError(err) {
				return err
			}

			c.logger().Warn("client.connection_lost", "Connection lost", map[string]any{"error": err.Error()})
		}

		if c.DisableReconnect {
			return err
		}

		// Clean up stale connection resources.
		c.cleanup()

		// Reconnect loop.
		disconnectTime := time.Now()
		for {
			interval := retryInterval(disconnectTime)
			c.logger().Info("client.reconnecting", "Reconnecting", map[string]any{"delay_ms": interval.Milliseconds()})
			time.Sleep(interval)

			serverAddr, _ := c.currentServerState()
			c.logger().Info("client.reconnect_attempt", "Attempting to reconnect", map[string]any{"server": serverAddr})
			err := c.connectAndRun()
			if err == nil {
				// connectAndRun returned normally (the connection dropped again), so start a new reconnect cycle.
				break
			}
			if isFatalError(err) {
				return err
			}
			c.logger().Warn("client.reconnect_failed", "Reconnect failed", map[string]any{"error": err.Error()})
			c.cleanup()
		}

		// connectAndRun returned normally, so prepare for another reconnect.
		c.cleanup()
	}
}

// isFatalError reports whether an error is fatal and should not trigger reconnect.
func isFatalError(err error) bool {
	if err == nil {
		return false
	}
	var runErr *clientRunError
	if errors.As(err, &runErr) {
		return runErr.fatal
	}
	return false
}

// Shutdown gracefully closes the client connection.
// It sends a normal WebSocket close frame so the server knows the disconnect was intentional.
func (c *Client) Shutdown() {
	c.logger().Info("client.shutdown_started", "Starting graceful client shutdown", nil)

	if rt := c.getCurrentRuntime(); rt != nil {
		_ = rt.writeControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client shutting down"),
			time.Now().Add(time.Second),
		)
	}

	c.cleanup()

	c.logger().Info("client.shutdown_complete", "Graceful client shutdown complete", nil)
}

func (c *Client) stopRuntime(rt *sessionRuntime, reason string) {
	if rt == nil {
		return
	}

	rt.closeDone()

	rt.dataMu.Lock()
	session := rt.dataSession
	rt.dataSession = nil
	rt.dataMu.Unlock()
	if session != nil && !session.IsClosed() {
		_ = session.Close()
	}

	conn := rt.detachConn()
	if conn != nil {
		if reason != "" {
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, reason),
				time.Now().Add(time.Second),
			)
		}
		_ = conn.Close()
	}
}

// cleanup clears stale connection resources in preparation for reconnect.
func (c *Client) cleanup() {
	rt := c.getCurrentRuntime()
	c.stopRuntime(rt, "")
	if rt != nil {
		rt.wg.Wait()
	}
	c.clearCurrentRuntime(rt)

	c.proxies.Range(func(key, _ any) bool {
		c.proxies.Delete(key)
		return true
	})
	c.socks5Targets.Range(func(key, _ any) bool {
		c.socks5Targets.Delete(key)
		return true
	})
	c.fixedTargetRuntimes.Range(func(key, _ any) bool {
		c.fixedTargetRuntimes.Delete(key)
		return true
	})
	c.tunnels.Range(func(key, value any) bool {
		if rt, ok := value.(*clientTunnelRuntime); ok {
			rt.close()
		}
		c.tunnels.Delete(key)
		return true
	})

	c.mu.Lock()
	c.ClientID = ""
	c.dataToken = ""
	c.mu.Unlock()
}

func (c *Client) failRuntime(rt *sessionRuntime, reason string) {
	c.stopRuntime(rt, reason)
}

// connectAndRun performs the full connection flow and blocks until disconnected.
// A nil return means the connection was established and later dropped (reconnect is allowed).
// A non-nil error means connection or authentication failed.
func (c *Client) connectAndRun() error {
	if err := c.ensureInstallID(); err != nil {
		return fmt.Errorf("failed to initialize client identity: %w", err)
	}

	c.normalizeServerAddr()
	rt := c.beginRuntime()

	// 1. Connect the control channel.
	controlURL := c.deriveControlURL()
	c.logger().Info("client.connecting", "Connecting to server", map[string]any{"control_url": controlURL})

	serverAddr, useTLS := c.currentServerState()
	u, err := url.Parse(serverAddr)
	if err != nil {
		c.clearCurrentRuntime(rt)
		return fmt.Errorf("failed to parse ServerAddr: %w", err)
	}

	dialer := c.newWSDialer(u.Hostname())
	dialer.Subprotocols = []string{protocol.WSSubProtocolControl}
	conn, _, err := dialer.Dial(controlURL, nil)
	if err != nil {
		c.clearCurrentRuntime(rt)
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	if useTLS {
		if err := c.checkTLSFingerprint(conn); err != nil {
			_ = conn.Close()
			c.clearCurrentRuntime(rt)
			return fmt.Errorf("TLS certificate fingerprint verification failed: %w", err)
		}
	}

	c.setRuntimeConn(rt, conn)

	c.logger().Info("client.connected", "Connected to server", nil)

	// 2. Send authentication.
	if err := c.authenticateRuntime(rt); err != nil {
		c.stopRuntime(rt, "")
		c.clearCurrentRuntime(rt)
		return err
	}
	c.logger().Info("client.authenticated", "Authentication succeeded", map[string]any{"client_id": c.CurrentClientID()})

	// 3. Establish the data channel.
	if err := c.connectDataChannelRuntime(rt); err != nil {
		c.logger().Warn("client.data_channel_failed", "Failed to establish data channel", map[string]any{"error": err.Error()})
		c.failRuntime(rt, "data_channel_start_failed")
		return fmt.Errorf("failed to establish data channel: %w", err)
	}
	c.logger().Info("client.data_channel_established", "Data channel established", nil)
	c.refreshPublicIPsAsync(true)

	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		c.acceptStreamLoopRuntime(rt)
	}()

	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		c.heartbeatLoopRuntime(rt)
	}()

	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		c.probeLoopRuntime(rt)
	}()

	for _, cfg := range c.ProxyConfigs {
		cfg := cfg
		rt.wg.Add(1)
		go func() {
			defer rt.wg.Done()
			c.requestProxyRuntime(rt, cfg)
		}()
	}

	c.controlLoopRuntime(rt)
	return nil
}

func (c *Client) authenticateRuntime(rt *sessionRuntime) error {
	hostname, _ := os.Hostname()
	localIP := netutil.GetOutboundIP()
	_, _, token := c.currentAuthState()
	capabilities := protocol.DefaultClientCapabilities()

	authReq := protocol.AuthRequest{
		Key:       c.Key,
		Token:     token,
		InstallID: c.InstallID,
		Client: protocol.ClientInfo{
			Hostname: hostname,
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			IP:       localIP,
			Version:  buildversion.Current,
			UpdateCapability: &protocol.UpdateCapability{
				InstallMethod: installmethod.Detect(svcmgr.RoleClient),
			},
			Capabilities: &capabilities,
		},
	}

	// If a token exists, send only the token first.
	// Do not send the key to avoid consuming it when the token is invalid.
	if token != "" {
		tokenReq := authReq
		tokenReq.Key = "" // Do not send the key.
		authResp, err := c.sendAuthRequestRuntime(rt, tokenReq)
		if err != nil {
			return fmt.Errorf("connection failed during authentication: %w", err)
		}
		if authResp.Success {
			c.applyAuthSuccess(authResp)
			c.logger().Info("client.token_auth_succeeded", "Token authentication succeeded", nil)
			return nil
		}

		return c.handleAuthFailure(authResp, true)
	}

	// No token available, authenticate with the key.
	authResp, err := c.sendAuthRequestRuntime(rt, authReq)
	if err != nil {
		return fmt.Errorf("connection failed during authentication: %w", err)
	}
	if !authResp.Success {
		return c.handleAuthFailure(authResp, false)
	}

	c.applyAuthSuccess(authResp)
	return nil
}

func (c *Client) sendAuthRequestRuntime(rt *sessionRuntime, authReq protocol.AuthRequest) (protocol.AuthResponse, error) {
	msg, err := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err != nil {
		return protocol.AuthResponse{}, err
	}

	if err := rt.writeJSON(msg); err != nil {
		return protocol.AuthResponse{}, fmt.Errorf("failed to send authentication message: %w", err)
	}

	rt.connMu.Lock()
	conn := rt.conn
	rt.connMu.Unlock()
	if conn == nil {
		return protocol.AuthResponse{}, fmt.Errorf("control channel unavailable")
	}

	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		return protocol.AuthResponse{}, fmt.Errorf("failed to read authentication response: %w", err)
	}
	if resp.Type != protocol.MsgTypeAuthResp {
		return protocol.AuthResponse{}, fmt.Errorf("expected auth response, got: %s", resp.Type)
	}

	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		return protocol.AuthResponse{}, fmt.Errorf("failed to parse authentication response: %w", err)
	}
	return authResp, nil
}

func (c *Client) applyAuthSuccess(authResp protocol.AuthResponse) {
	c.mu.Lock()
	c.ClientID = authResp.ClientID
	c.dataToken = authResp.DataToken
	if authResp.Token != "" {
		c.Token = authResp.Token
	}
	c.mu.Unlock()

	if authResp.Token != "" {
		if err := c.saveToken(authResp.Token); err != nil {
			c.logger().Warn("client.token_save_failed", "Failed to save token", map[string]any{"error": err.Error()})
		} else {
			c.logger().Info("client.token_saved", "Token saved and will be reused for future reconnects", nil)
		}
	}
}

func (c *Client) handleAuthFailure(authResp protocol.AuthResponse, attemptedWithToken bool) error {
	message := authResp.Message
	if message == "" {
		message = authResp.Code
	}

	if authResp.ClearToken {
		c.logger().Warn("client.token_clear_requested", "Server requested local token cleanup", map[string]any{"code": authResp.Code})
		c.setToken("")
		if err := c.clearToken(); err != nil {
			c.logger().Warn("client.token_clear_failed", "Failed to clear local token", map[string]any{"error": err.Error()})
		}
		if c.Key != "" {
			return &clientRunError{
				message: fmt.Sprintf("authentication failed (%s), token cleared, retrying with key", authResp.Code),
				fatal:   false,
			}
		}
		return &clientRunError{
			message: fmt.Sprintf("authentication failed: %s", message),
			fatal:   true,
		}
	}

	if authResp.Retryable {
		c.logger().Warn("client.auth_failed", "Authentication failed", map[string]any{"code": authResp.Code, "message": message, "retryable": true})
		return &clientRunError{
			message: fmt.Sprintf("authentication failed (%s): %s", authResp.Code, message),
			fatal:   false,
		}
	}

	if attemptedWithToken && c.Key != "" && (authResp.Code == protocol.AuthCodeInvalidToken || authResp.Code == protocol.AuthCodeRevokedToken) {
		c.logger().Warn("client.auth_failed", "Authentication failed", map[string]any{"code": authResp.Code, "message": message, "attempted_with_token": true})
		return &clientRunError{
			message: fmt.Sprintf("authentication failed (%s), retrying with key", authResp.Code),
			fatal:   false,
		}
	}

	c.logger().Warn("client.auth_failed", "Authentication failed", map[string]any{"code": authResp.Code, "message": message, "attempted_with_token": attemptedWithToken})
	return &clientRunError{
		message: fmt.Sprintf("authentication failed: %s", message),
		fatal:   true,
	}
}

// connectDataChannel establishes the data channel.
// It opens a WebSocket to /ws/data, sends the initial binary handshake frame,
// and then creates a yamux client session on top of the WSConn.
func (c *Client) connectDataChannel() error {
	return c.connectDataChannelRuntime(c.runtimeForStandaloneUse())
}

func (c *Client) connectDataChannelRuntime(rt *sessionRuntime) error {
	c.normalizeServerAddr()

	serverAddr, _ := c.currentServerState()
	u, err := url.Parse(serverAddr)
	if err != nil {
		return fmt.Errorf("failed to parse ServerAddr: %w", err)
	}

	dataURL := c.deriveDataURL()
	dialer := c.newWSDialer(u.Hostname())
	dialer.Subprotocols = []string{protocol.WSSubProtocolData}
	wsConn, _, err := dialer.Dial(dataURL, nil)
	if err != nil {
		return fmt.Errorf("failed to open data-channel WebSocket: %w", err)
	}

	if c.UsesTLS() {
		if err := c.checkTLSFingerprint(wsConn); err != nil {
			_ = wsConn.Close()
			return fmt.Errorf("TLS certificate fingerprint verification failed on data channel: %w", err)
		}
	}

	wsConn.SetReadLimit(wsDataMaxMessageSize)
	if err := wsConn.SetReadDeadline(time.Now().Add(c.dataHandshakeTimeout)); err != nil {
		_ = wsConn.Close()
		return fmt.Errorf("failed to set data-channel handshake read deadline: %w", err)
	}

	clientID, dataToken, _ := c.currentAuthState()
	handshake := protocol.EncodeDataHandshake(clientID, dataToken)
	if err := wsConn.WriteMessage(websocket.BinaryMessage, handshake); err != nil {
		_ = wsConn.Close()
		return fmt.Errorf("failed to send data-channel handshake: %w", err)
	}

	messageType, payload, err := wsConn.ReadMessage()
	if err != nil {
		_ = wsConn.Close()
		return fmt.Errorf("failed to read data-channel handshake response: %w", err)
	}
	if messageType != websocket.BinaryMessage {
		_ = wsConn.Close()
		return fmt.Errorf("invalid data-channel handshake response type: %d", messageType)
	}
	if len(payload) != 1 {
		_ = wsConn.Close()
		return fmt.Errorf("invalid data-channel handshake response length: %d", len(payload))
	}
	if payload[0] != protocol.DataHandshakeOK {
		_ = wsConn.Close()
		return fmt.Errorf("data-channel handshake rejected (status: 0x%02x)", payload[0])
	}

	if err := wsConn.SetReadDeadline(time.Time{}); err != nil {
		_ = wsConn.Close()
		return fmt.Errorf("failed to clear data-channel read deadline: %w", err)
	}

	// Create the yamux client session.
	session, err := mux.NewClientSession(mux.NewWSConn(wsConn), mux.DefaultConfig())
	if err != nil {
		_ = wsConn.Close()
		return fmt.Errorf("failed to create yamux session: %w", err)
	}

	c.setRuntimeDataSession(rt, session)

	return nil
}

// checkTLSFingerprint checks the TLS certificate fingerprint (TOFU).
func (c *Client) checkTLSFingerprint(conn *websocket.Conn) error {
	tlsConn, ok := conn.UnderlyingConn().(*tls.Conn)
	if !ok {
		return nil // Non-TLS connection; skip.
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("server did not provide a certificate")
	}

	// Calculate the server certificate fingerprint.
	certDER := state.PeerCertificates[0].Raw
	hash := sha256.Sum256(certDER)
	hexStr := strings.ToUpper(hex.EncodeToString(hash[:]))
	parts := make([]string, 0, len(hexStr)/2)
	for i := 0; i < len(hexStr); i += 2 {
		end := i + 2
		if end > len(hexStr) {
			end = len(hexStr)
		}
		parts = append(parts, hexStr[i:end])
	}
	serverFP := strings.Join(parts, ":")

	c.mu.Lock()
	currentFingerprint := c.TLSFingerprint
	if currentFingerprint == "" {
		// TOFU: first connection, record the fingerprint.
		c.TLSFingerprint = serverFP
		c.mu.Unlock()
		c.logger().Info("client.tls_fingerprint_recorded", "TLS fingerprint recorded", map[string]any{"fingerprint": serverFP})
		// Persist the fingerprint.
		if err := c.saveTLSFingerprint(serverFP); err != nil {
			c.logger().Warn("client.tls_fingerprint_save_failed", "Failed to save TLS fingerprint", map[string]any{"error": err.Error()})
		}
		return nil
	}
	c.mu.Unlock()

	// A fingerprint already exists; compare strictly.
	if serverFP != currentFingerprint {
		return fmt.Errorf(
			"TLS certificate fingerprint mismatch! A man-in-the-middle attack may be in progress"+
				" (expected: %s, actual: %s); "+
				"if the server really changed its certificate, delete the client state database and try again",
			currentFingerprint, serverFP,
		)
	}

	c.logger().Info("client.tls_fingerprint_verified", "TLS certificate fingerprint verified", nil)
	return nil
}

// acceptStreamLoop continuously accepts yamux streams from the server.
// Each stream represents an external connection that must be forwarded to a local service.
func (c *Client) acceptStreamLoop() {
	c.acceptStreamLoopRuntime(c.runtimeForStandaloneUse())
}

func (c *Client) acceptStreamLoopRuntime(rt *sessionRuntime) {
	rt.dataMu.RLock()
	session := rt.dataSession
	rt.dataMu.RUnlock()
	if session == nil {
		return
	}

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			select {
			case <-rt.done:
				return
			default:
				if !session.IsClosed() {
					log.Printf("⚠️ AcceptStream failed: %v", err)
				}
				c.failRuntime(rt, "data_session_closed")
				return
			}
		}

		go c.handleStream(stream)
	}
}

// handleStream handles a single yamux stream:
// 1. Read the DataStreamHeader to get the stable tunnel identity
// 2. Look up the local proxy configuration by tunnel id or legacy name
// 3. Dial the local service
// 4. Relay(stream, localConn)
func (c *Client) handleStream(stream *yamux.Stream) {
	defer func() { _ = stream.Close() }()

	header, err := protocol.DecodeDataStreamHeader(stream)
	if err != nil {
		log.Printf("⚠️ Failed to read DataStreamHeader: %v", err)
		return
	}
	if target, ok := c.socks5TargetForDataStreamHeader(header); ok {
		if !dataStreamHeaderMatchesSOCKS5Target(header, target) {
			log.Printf("⚠️ SOCKS5 DataStreamHeader rejected: tunnel=%s revision=%d source=%s target=%s direction=%s transport=%s", header.TunnelID, header.Revision, header.SourceRole, header.TargetRole, header.Direction, header.Transport)
			return
		}
		c.handleSOCKS5TargetStream(stream, header, target)
		return
	}
	if target, ok := c.fixedTargetForDataStreamHeader(header); ok {
		if !dataStreamHeaderMatchesFixedTarget(header, target) {
			log.Printf("⚠️ fixed target DataStreamHeader rejected: tunnel=%s revision=%d source=%s target=%s direction=%s transport=%s", header.TunnelID, header.Revision, header.SourceRole, header.TargetRole, header.Direction, header.Transport)
			return
		}
		c.handleFixedTargetStream(stream, header, target)
		return
	}
	proxyName, cfg, ok := c.proxyForDataStreamHeader(header)
	if !ok {
		log.Printf("⚠️ Unknown tunnel id: %s", header.TunnelID)
		return
	}
	if !dataStreamHeaderMatchesProxyConfig(header, cfg) {
		log.Printf("⚠️ DataStreamHeader rejected for %s: tunnel=%s revision=%d source=%s target=%s direction=%s transport=%s", proxyName, header.TunnelID, header.Revision, header.SourceRole, header.TargetRole, header.Direction, header.Transport)
		return
	}

	// Dispatch by proxy type.
	if cfg.Type == protocol.ProxyTypeUDP {
		c.handleUDPStream(stream, cfg)
		return
	}

	// TCP type: connect to the local service.
	localAddr := net.JoinHostPort(cfg.LocalIP, fmt.Sprintf("%d", cfg.LocalPort))
	localConn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		log.Printf("⚠️ Failed to connect to local service [%s → %s]: %v", proxyName, localAddr, err)
		return
	}

	// Relay traffic in both directions.
	mux.Relay(stream, localConn)
}

func (c *Client) socks5TargetForDataStreamHeader(header protocol.DataStreamHeader) (clientSOCKS5TargetRuntime, bool) {
	if val, ok := c.socks5Targets.Load(header.TunnelID); ok {
		target, ok := val.(*clientSOCKS5TargetRuntime)
		if !ok || target == nil {
			log.Printf("⚠️ invalid SOCKS5 target cache entry for tunnel %s: %T", header.TunnelID, val)
			return clientSOCKS5TargetRuntime{}, false
		}
		return *target, true
	}
	return clientSOCKS5TargetRuntime{}, false
}

func (c *Client) fixedTargetForDataStreamHeader(header protocol.DataStreamHeader) (fixedServiceTargetRuntime, bool) {
	if val, ok := c.fixedTargetRuntimes.Load(header.TunnelID); ok {
		target, ok := val.(*fixedServiceTargetRuntime)
		if !ok || target == nil {
			log.Printf("⚠️ invalid fixed target cache entry for tunnel %s: %T", header.TunnelID, val)
			return fixedServiceTargetRuntime{}, false
		}
		return *target, true
	}
	return fixedServiceTargetRuntime{}, false
}

func dataStreamHeaderMatchesFixedTarget(header protocol.DataStreamHeader, target fixedServiceTargetRuntime) bool {
	if target.revision != 0 && header.Revision != target.revision {
		return false
	}
	if header.TargetRole != protocol.DataStreamRoleTarget {
		return false
	}
	if header.SourceRole != protocol.DataStreamRoleServer && header.SourceRole != protocol.DataStreamRoleIngress {
		return false
	}
	if header.Direction != protocol.DataStreamDirectionIngressToTarget {
		return false
	}
	if header.Transport != protocol.ActualTransportServerRelay {
		return false
	}
	if target.transportPolicy == protocol.TransportPolicyDirectOnly {
		return false
	}
	return true
}

func (c *Client) handleFixedTargetStream(stream *yamux.Stream, header protocol.DataStreamHeader, target fixedServiceTargetRuntime) {
	switch target.targetType {
	case protocol.TargetTypeUDPService:
		conn, err := net.DialTimeout("udp", target.address(), 5*time.Second)
		if err != nil {
			log.Printf("⚠️ Failed to connect to fixed UDP target [%s → %s]: %v", header.TunnelID, target.address(), err)
			return
		}
		mux.UDPRelay(stream, conn)
	default:
		conn, err := net.DialTimeout("tcp", target.address(), 5*time.Second)
		if err != nil {
			log.Printf("⚠️ Failed to connect to fixed TCP target [%s → %s]: %v", header.TunnelID, target.address(), err)
			return
		}
		mux.Relay(stream, conn)
	}
}

func dataStreamHeaderMatchesProxyConfig(header protocol.DataStreamHeader, cfg protocol.ProxyNewRequest) bool {
	if cfg.ProvisionRevision != 0 && header.Revision != int64(cfg.ProvisionRevision) {
		return false
	}
	if header.TargetRole != protocol.DataStreamRoleTarget {
		return false
	}
	if header.SourceRole != protocol.DataStreamRoleServer && header.SourceRole != protocol.DataStreamRoleIngress {
		return false
	}
	if header.Direction != protocol.DataStreamDirectionIngressToTarget {
		return false
	}
	if header.Transport != protocol.ActualTransportServerRelay {
		return false
	}
	if cfg.TransportPolicy == protocol.TransportPolicyDirectOnly {
		return false
	}
	if cfg.ActualTransport != "" && cfg.ActualTransport != protocol.ActualTransportUnknown && header.Transport != cfg.ActualTransport {
		return false
	}
	return true
}

func (c *Client) proxyForDataStreamHeader(header protocol.DataStreamHeader) (string, protocol.ProxyNewRequest, bool) {
	if val, ok := c.proxies.Load(header.TunnelID); ok {
		cfg, ok := val.(protocol.ProxyNewRequest)
		if !ok {
			log.Printf("⚠️ invalid proxy cache entry for tunnel %s: %T", header.TunnelID, val)
			return "", protocol.ProxyNewRequest{}, false
		}
		return cfg.Name, cfg, true
	}

	var proxyName string
	var cfg protocol.ProxyNewRequest
	found := false
	c.proxies.Range(func(key, value any) bool {
		candidate, ok := value.(protocol.ProxyNewRequest)
		if !ok {
			return true
		}
		if candidate.ID != "" && candidate.ID == header.TunnelID {
			if name, ok := key.(string); ok && name != "" {
				proxyName = name
			} else {
				proxyName = candidate.Name
			}
			cfg = candidate
			found = true
			return false
		}
		return true
	})
	return proxyName, cfg, found
}

// requestProxy requests creation of a proxy tunnel over the control channel.
func (c *Client) requestProxy(cfg protocol.ProxyNewRequest) {
	c.requestProxyRuntime(c.runtimeForStandaloneUse(), cfg)
}

func (c *Client) requestProxyRuntime(rt *sessionRuntime, cfg protocol.ProxyNewRequest) {
	// Register the local proxy configuration first.
	c.proxies.Store(cfg.Name, cfg)

	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyCreate, protocol.ProxyCreateRequest(cfg))
	err := rt.writeJSON(msg)
	if err != nil {
		log.Printf("❌ Failed to send proxy request [%s]: %v", cfg.Name, err)
		return
	}
	log.Printf("📤 Requested proxy tunnel creation: %s (local %s:%d → public :%d)",
		cfg.Name, cfg.LocalIP, cfg.LocalPort, cfg.RemotePort)
}

func (c *Client) heartbeatLoopRuntime(rt *sessionRuntime) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			msg, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
			err := rt.writeJSON(msg)
			if err != nil {
				log.Printf("⚠️ Failed to send heartbeat: %v", err)
				c.failRuntime(rt, "heartbeat_write_failed")
				return
			}
		case <-rt.done:
			return
		}
	}
}

func (c *Client) probeLoopRuntime(rt *sessionRuntime) {
	// Report once immediately on startup.
	c.reportProbeRuntime(rt)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.reportProbeRuntime(rt)
		case <-rt.done:
			return
		}
	}
}

func (c *Client) reportProbeRuntime(rt *sessionRuntime) {
	stats, err := CollectSystemStats(c.startTime)
	if err != nil {
		log.Printf("⚠️ Failed to collect system status: %v", err)
		return
	}

	// Public IP probing can be slow or unavailable on some networks. Do it in
	// the background so routine health probes are never blocked by third-party
	// probe services.
	c.refreshPublicIPsAsync(false)
	c.mu.Lock()
	stats.PublicIPv4 = c.publicIPv4
	stats.PublicIPv6 = c.publicIPv6
	c.mu.Unlock()

	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	err = rt.writeJSON(msg)
	if err != nil {
		log.Printf("⚠️ Failed to report probe data: %v", err)
		c.failRuntime(rt, "probe_write_failed")
	}
}

// refreshPublicIPsAsync fetches and caches public IPs in the background.
// It only performs a real request if forced or if the cached result is stale.
func (c *Client) refreshPublicIPsAsync(force bool) {
	c.mu.Lock()
	if c.publicIPFetching || (!force && !c.publicIPFetched.IsZero() && time.Since(c.publicIPFetched) < publicIPRefreshAfter) {
		c.mu.Unlock()
		return // Cache is still fresh.
	}
	c.publicIPFetching = true
	c.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("⚠️ Public IP refresh panicked: %v", r)
			}
			c.mu.Lock()
			c.publicIPFetching = false
			c.mu.Unlock()
		}()

		ipv4, ipv6 := fetchPublicIPs()

		c.mu.Lock()
		if ipv4 != "" {
			c.publicIPv4 = ipv4
		}
		if ipv6 != "" {
			c.publicIPv6 = ipv6
		}
		c.publicIPFetched = time.Now()
		c.mu.Unlock()
	}()
}

func (c *Client) controlLoopRuntime(rt *sessionRuntime) {
	rt.connMu.Lock()
	conn := rt.conn
	rt.connMu.Unlock()
	if conn == nil {
		return
	}

	for {
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("⚠️ Control channel connection error: %v", err)
			}
			rt.closeDone()
			return
		}

		switch msg.Type {
		case protocol.MsgTypePong:
			// Heartbeat reply, ignore.

		case protocol.MsgTypeProxyProvision:
			var meta struct {
				TunnelID string `json:"tunnel_id"`
			}
			if err := msg.ParsePayload(&meta); err == nil && meta.TunnelID != "" {
				var req protocol.TunnelProvisionRequest
				if err := msg.ParsePayload(&req); err != nil {
					log.Printf("⚠️ Failed to parse tunnel provision instruction: %v", err)
					continue
				}
				ack := c.handleTunnelProvision(rt, req)
				resp, _ := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, ack)
				if err := rt.writeJSON(resp); err != nil {
					log.Printf("⚠️ Failed to send tunnel provisioning ACK [%s]: %v", req.TunnelID, err)
					c.failRuntime(rt, "tunnel_provision_ack_write_failed")
					return
				}
				if ack.Accepted {
					log.Printf("✅ Accepted tunnel provisioning config from server [%s] role=%s", req.TunnelID, req.Role)
				} else {
					log.Printf("⚠️ Rejected tunnel provisioning config from server [%s] role=%s: %s", req.TunnelID, req.Role, ack.Message)
				}
				continue
			}

			// Sent by the server: asks the client to accept/provision a proxy tunnel configuration.
			var req protocol.ProxyProvisionRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ Failed to parse proxy instruction: %v", err)
				continue
			}
			log.Printf("📥 Received tunnel provisioning config from server: %s (local %s:%d → public :%d)",
				req.Name, req.LocalIP, req.LocalPort, req.RemotePort)

			c.proxies.Store(req.Name, protocol.ProxyNewRequest(req))
			resp, _ := protocol.NewMessage(protocol.MsgTypeProxyProvisionAck, protocol.ProxyProvisionAck{
				Name:              req.Name,
				ProvisionRevision: req.ProvisionRevision,
				Accepted:          true,
				Message:           "provision accepted",
			})
			log.Printf("✅ Accepted tunnel provisioning config from server [%s]", req.Name)

			if err := rt.writeJSON(resp); err != nil {
				log.Printf("⚠️ Failed to send provisioning ACK [%s]: %v", req.Name, err)
				c.failRuntime(rt, "proxy_provision_ack_write_failed")
				return
			}

		case protocol.MsgTypeTunnelPreflight:
			var req protocol.TunnelPreflightRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ Failed to parse tunnel preflight request: %v", err)
				continue
			}
			resp, _ := protocol.NewMessage(protocol.MsgTypeTunnelPreflightResp, c.handleTunnelPreflight(req))
			if err := rt.writeJSON(resp); err != nil {
				log.Printf("⚠️ Failed to send tunnel preflight response [%s]: %v", req.RequestID, err)
				c.failRuntime(rt, "tunnel_preflight_resp_write_failed")
				return
			}

		case protocol.MsgTypeProxyCreateResp:
			// Proxy creation result (for client-initiated requests such as benchmarks).
			var resp protocol.ProxyCreateResponse
			if err := msg.ParsePayload(&resp); err != nil {
				log.Printf("⚠️ Failed to parse proxy response: %v", err)
				continue
			}
			if resp.Success {
				c.applyProxyCreateResponse(resp)
				log.Printf("✅ Proxy tunnel created successfully [%s], public port: %d", resp.Name, resp.RemotePort)
			} else {
				log.Printf("❌ Failed to create proxy tunnel [%s]: %s", resp.Name, resp.Message)
			}

		case protocol.MsgTypeProxyClose:
			var meta struct {
				TunnelID string `json:"tunnel_id"`
			}
			if err := msg.ParsePayload(&meta); err == nil && meta.TunnelID != "" {
				var req protocol.TunnelUnprovisionRequest
				if err := msg.ParsePayload(&req); err != nil {
					log.Printf("⚠️ Failed to parse tunnel unprovision instruction: %v", err)
					continue
				}
				c.handleTunnelUnprovision(req)
				log.Printf("🔌 Tunnel unprovisioned: %s role=%s (reason: %s)", req.TunnelID, req.Role, req.Reason)
				continue
			}

			// Sent by the server: close the proxy tunnel.
			var req protocol.ProxyCloseRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ Failed to parse proxy close instruction: %v", err)
				continue
			}
			c.proxies.Delete(req.Name)
			log.Printf("🔌 Proxy tunnel closed: %s (reason: %s)", req.Name, req.Reason)

		default:
			log.Printf("📩 Received control message: %s", msg.Type)
		}
	}
}

func (c *Client) applyProxyCreateResponse(resp protocol.ProxyCreateResponse) {
	if resp.Name == "" {
		return
	}
	val, ok := c.proxies.Load(resp.Name)
	if !ok {
		return
	}
	cfg, ok := val.(protocol.ProxyNewRequest)
	if !ok {
		return
	}
	if resp.ID != "" {
		cfg.ID = resp.ID
	}
	if resp.RemotePort != 0 {
		cfg.RemotePort = resp.RemotePort
	}
	if resp.TransportPolicy != "" {
		cfg.TransportPolicy = resp.TransportPolicy
	}
	if resp.ActualTransport != "" {
		cfg.ActualTransport = resp.ActualTransport
	}
	if resp.ProvisionRevision != 0 {
		cfg.ProvisionRevision = resp.ProvisionRevision
	}
	c.proxies.Store(resp.Name, cfg)
}
