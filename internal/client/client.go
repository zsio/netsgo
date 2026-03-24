package client

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
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

	"netsgo/pkg/mux"
	"netsgo/pkg/netutil"
	"netsgo/pkg/protocol"
	buildversion "netsgo/pkg/version"
)

const wsDataMaxMessageSize = 512 * 1024

// Client 是客户端/Client 的核心结构体
type Client struct {
	ServerAddr      string // 服务器地址（支持 ws:// wss:// http:// https://，内部统一规范化）
	Key             string // 认证密钥（用于兑换 Token）
	Token           string // 客户端连接密钥（由 Key 兑换）
	InstallID       string // 稳定安装 ID
	StatePath       string // 安装 ID 持久化路径
	ClientID        string // Server 分配的稳定 Client ID
	TLSSkipVerify   bool
	TLSFingerprint  string
	dataToken       string
	conn            *websocket.Conn
	mu              sync.Mutex // 保护当前 runtime 与镜像字段
	done            chan struct{}
	dataSession     *yamux.Session // 数据通道 yamux Session
	dataMu          sync.RWMutex
	proxies         sync.Map // proxy_name -> ProxyNewRequest
	useTLS          bool
	startTime       time.Time // 程序启动时间，用于计算 process uptime
	publicIPv4      string    // 缓存的公网 IPv4
	publicIPv6      string    // 缓存的公网 IPv6
	publicIPFetched time.Time // 上次获取时间
	// ProxyConfigs 由服务端下发，Benchmark 测试也可手动设置
	ProxyConfigs []protocol.ProxyNewRequest
	// DisableReconnect 禁用自动重连（用于测试等场景）
	DisableReconnect bool

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

// New 创建一个新的 Client 实例
func New(serverAddr, key string) *Client {
	return &Client{
		ServerAddr:           serverAddr,
		Key:                  key,
		done:                 make(chan struct{}),
		startTime:            time.Now(),
		dataHandshakeTimeout: 10 * time.Second,
	}
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
	rt.dataMu.Lock()
	rt.dataSession = session
	rt.dataMu.Unlock()

	c.mu.Lock()
	shouldMirror := c.currentRuntime == rt || c.currentRuntime == nil
	c.mu.Unlock()
	if shouldMirror {
		c.dataMu.Lock()
		c.dataSession = session
		c.dataMu.Unlock()
	}
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

// normalizeServerAddr 将用户输入的地址规范化为统一格式。
// 支持输入: ws:// wss:// http:// https://
// 输出: http://host:port 或 https://host:port
// 同时设置 c.useTLS 标记。
func (c *Client) normalizeServerAddr() {
	addr := strings.TrimRight(c.ServerAddr, "/")

	switch {
	case strings.HasPrefix(addr, "wss://"):
		addr = "https://" + strings.TrimPrefix(addr, "wss://")
		c.useTLS = true
	case strings.HasPrefix(addr, "ws://"):
		addr = "http://" + strings.TrimPrefix(addr, "ws://")
		c.useTLS = false
	case strings.HasPrefix(addr, "https://"):
		c.useTLS = true
	case strings.HasPrefix(addr, "http://"):
		c.useTLS = false
	default:
		// 无协议前缀，默认 http
		addr = "http://" + addr
		c.useTLS = false
	}

	c.ServerAddr = addr
}

// deriveControlURL 从规范化后的 ServerAddr 推导控制通道 WebSocket URL
// http://host:port -> ws://host:port/ws/control
// https://host:port -> wss://host:port/ws/control
func (c *Client) deriveControlURL() string {
	addr := c.ServerAddr
	if c.useTLS {
		addr = "wss://" + strings.TrimPrefix(addr, "https://")
	} else {
		addr = "ws://" + strings.TrimPrefix(addr, "http://")
	}
	return addr + "/ws/control"
}

func (c *Client) deriveDataURL() string {
	addr := c.ServerAddr
	if c.useTLS {
		addr = "wss://" + strings.TrimPrefix(addr, "https://")
	} else {
		addr = "ws://" + strings.TrimPrefix(addr, "http://")
	}
	return addr + "/ws/data"
}

// buildTLSConfig 构建客户端 TLS 配置
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

// retryInterval 根据首次断连时间计算重试间隔
// 前 5 分钟每 3 秒重试，之后每 10 秒重试
func retryInterval(disconnectTime time.Time) time.Duration {
	elapsed := time.Since(disconnectTime)
	if elapsed < 5*time.Minute {
		return 3 * time.Second
	}
	return 10 * time.Second
}

// Start 启动客户端，连接 Server 并开始工作。
// 如果连接断开，自动重连（认证失败等致命错误除外）。
func (c *Client) Start() error {
	for {
		err := c.connectAndRun()
		if err != nil {
			// 认证失败是致命错误，不重连
			if c.DisableReconnect {
				return err
			}
			if isFatalError(err) {
				return err
			}

			log.Printf("⚠️ 连接断开: %v", err)
		}

		if c.DisableReconnect {
			return err
		}

		// 清理旧连接资源
		c.cleanup()

		// 重连循环
		disconnectTime := time.Now()
		for {
			interval := retryInterval(disconnectTime)
			log.Printf("🔄 将在 %v 后重连...", interval)
			time.Sleep(interval)

			log.Printf("🔄 正在尝试重连 %s ...", c.ServerAddr)
			err := c.connectAndRun()
			if err == nil {
				// connectAndRun 正常返回（连接又断了），开始新一轮重连
				break
			}
			if isFatalError(err) {
				return err
			}
			log.Printf("⚠️ 重连失败: %v", err)
			c.cleanup()
		}

		// connectAndRun 正常返回，准备再次重连
		c.cleanup()
	}
}

// isFatalError 判断是否为致命错误（不应重连）
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

// Shutdown 优雅关闭客户端连接
// 发送 WebSocket 正常关闭帧，让服务端知道是主动断开而非异常
func (c *Client) Shutdown() {
	log.Printf("🛑 客户端开始优雅关闭...")

	if rt := c.getCurrentRuntime(); rt != nil {
		rt.connMu.Lock()
		if rt.conn != nil {
			rt.conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client shutting down"),
			)
		}
		rt.connMu.Unlock()
	}

	time.Sleep(100 * time.Millisecond)

	c.cleanup()

	log.Printf("✅ 客户端优雅关闭完成")
}

func (c *Client) closeDone() {
	if rt := c.getCurrentRuntime(); rt != nil {
		rt.closeDone()
	}
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

	rt.connMu.Lock()
	conn := rt.conn
	rt.conn = nil
	rt.connMu.Unlock()
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

// cleanup 清理旧连接资源，为重连做准备
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

	c.ClientID = ""
	c.dataToken = ""
}

func (c *Client) failRuntime(rt *sessionRuntime, reason string) {
	c.stopRuntime(rt, reason)
}

func (c *Client) failCurrentSession(reason string) {
	c.failRuntime(c.getCurrentRuntime(), reason)
}

// connectAndRun 执行完整的连接流程并阻塞直到断连。
// 返回 nil 表示连接曾经成功但后来断开（可以重连），
// 返回 error 表示连接或认证失败。
func (c *Client) connectAndRun() error {
	if err := c.ensureInstallID(); err != nil {
		return fmt.Errorf("初始化客户端身份失败: %w", err)
	}

	c.normalizeServerAddr()
	rt := c.beginRuntime()

	// 1. 连接控制通道
	controlURL := c.deriveControlURL()
	log.Printf("🔌 正在连接 Server: %s", controlURL)

	u, err := url.Parse(c.ServerAddr)
	if err != nil {
		c.clearCurrentRuntime(rt)
		return fmt.Errorf("解析 ServerAddr 失败: %w", err)
	}

	dialer := c.newWSDialer(u.Hostname())
	dialer.Subprotocols = []string{protocol.WSSubProtocolControl}
	conn, _, err := dialer.Dial(controlURL, nil)
	if err != nil {
		c.clearCurrentRuntime(rt)
		return fmt.Errorf("连接 Server 失败: %w", err)
	}

	if c.useTLS && !c.TLSSkipVerify {
		if err := c.checkTLSFingerprint(conn); err != nil {
			conn.Close()
			c.clearCurrentRuntime(rt)
			return fmt.Errorf("TLS 证书指纹校验失败: %w", err)
		}
	}

	c.setRuntimeConn(rt, conn)

	log.Printf("✅ 已连接到 Server")

	// 2. 发送认证
	if err := c.authenticateRuntime(rt); err != nil {
		c.stopRuntime(rt, "")
		c.clearCurrentRuntime(rt)
		return err
	}
	log.Printf("✅ 认证成功，Client ID: %s", c.ClientID)

	// 3. 建立数据通道
	if err := c.connectDataChannelRuntime(rt); err != nil {
		log.Printf("⚠️ 数据通道建立失败，当前逻辑会话将重建: %v", err)
		c.failRuntime(rt, "data_channel_start_failed")
		return fmt.Errorf("数据通道建立失败: %w", err)
	}
	log.Printf("✅ 数据通道已建立")

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

// authenticate 发送认证请求
// 优先使用 Token，失败后降级到 Key
func (c *Client) authenticate() error {
	return c.authenticateRuntime(c.runtimeForStandaloneUse())
}

func (c *Client) authenticateRuntime(rt *sessionRuntime) error {
	hostname, _ := os.Hostname()
	localIP := netutil.GetOutboundIP()

	authReq := protocol.AuthRequest{
		Key:       c.Key,
		Token:     c.Token,
		InstallID: c.InstallID,
		Client: protocol.ClientInfo{
			Hostname: hostname,
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			IP:       localIP,
			Version:  buildversion.Current,
		},
	}

	// 如果有 Token，先只发 Token（不发 Key，避免服务端在 Token 无效时消耗 Key）
	if c.Token != "" {
		tokenReq := authReq
		tokenReq.Key = "" // 不发送 Key
		authResp, err := c.sendAuthRequestRuntime(rt, tokenReq)
		if err != nil {
			return fmt.Errorf("认证阶段连接失败: %w", err)
		}
		if authResp.Success {
			c.applyAuthSuccess(authResp)
			log.Printf("✅ Token 认证成功")
			return nil
		}

		return c.handleAuthFailure(authResp, true)
	}

	// 没有 Token，用 Key 认证
	authResp, err := c.sendAuthRequestRuntime(rt, authReq)
	if err != nil {
		return fmt.Errorf("认证阶段连接失败: %w", err)
	}
	if !authResp.Success {
		return c.handleAuthFailure(authResp, false)
	}

	c.applyAuthSuccess(authResp)
	return nil
}

func (c *Client) sendAuthRequest(authReq protocol.AuthRequest) (protocol.AuthResponse, error) {
	return c.sendAuthRequestRuntime(c.runtimeForStandaloneUse(), authReq)
}

func (c *Client) sendAuthRequestRuntime(rt *sessionRuntime, authReq protocol.AuthRequest) (protocol.AuthResponse, error) {
	msg, err := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err != nil {
		return protocol.AuthResponse{}, err
	}

	rt.connMu.Lock()
	conn := rt.conn
	rt.connMu.Unlock()
	if conn == nil {
		return protocol.AuthResponse{}, fmt.Errorf("控制通道不可用")
	}

	if err := conn.WriteJSON(msg); err != nil {
		return protocol.AuthResponse{}, fmt.Errorf("发送认证消息失败: %w", err)
	}

	var resp protocol.Message
	if err := conn.ReadJSON(&resp); err != nil {
		return protocol.AuthResponse{}, fmt.Errorf("读取认证响应失败: %w", err)
	}
	if resp.Type != protocol.MsgTypeAuthResp {
		return protocol.AuthResponse{}, fmt.Errorf("期望认证响应，收到: %s", resp.Type)
	}

	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		return protocol.AuthResponse{}, fmt.Errorf("解析认证响应失败: %w", err)
	}
	return authResp, nil
}

func (c *Client) applyAuthSuccess(authResp protocol.AuthResponse) {
	c.ClientID = authResp.ClientID
	c.dataToken = authResp.DataToken

	if authResp.Token != "" {
		c.Token = authResp.Token
		if err := c.saveToken(authResp.Token); err != nil {
			log.Printf("⚠️ 保存 Token 失败: %v", err)
		} else {
			log.Printf("🔑 Token 已保存，后续重连将自动使用")
		}
	}
}

func (c *Client) handleAuthFailure(authResp protocol.AuthResponse, attemptedWithToken bool) error {
	message := authResp.Message
	if message == "" {
		message = authResp.Code
	}

	if authResp.ClearToken {
		log.Printf("⚠️ 服务端要求清除本地 Token: code=%s", authResp.Code)
		c.Token = ""
		if err := c.clearToken(); err != nil {
			log.Printf("⚠️ 清除本地 Token 失败: %v", err)
		}
		if c.Key != "" {
			return &clientRunError{
				message: fmt.Sprintf("认证失败(%s)，已清除 Token，准备改用 Key 重连", authResp.Code),
				fatal:   false,
			}
		}
		return &clientRunError{
			message: fmt.Sprintf("认证失败: %s", message),
			fatal:   true,
		}
	}

	if authResp.Retryable {
		return &clientRunError{
			message: fmt.Sprintf("认证失败(%s)，稍后重试", authResp.Code),
			fatal:   false,
		}
	}

	if attemptedWithToken && c.Key != "" && (authResp.Code == protocol.AuthCodeInvalidToken || authResp.Code == protocol.AuthCodeRevokedToken) {
		return &clientRunError{
			message: fmt.Sprintf("认证失败(%s)，准备改用 Key 重连", authResp.Code),
			fatal:   false,
		}
	}

	return &clientRunError{
		message: fmt.Sprintf("认证失败: %s", message),
		fatal:   true,
	}
}

// connectDataChannel 建立数据通道。
// 通过 /ws/data 建立 WebSocket，发送首个 binary 握手帧，
// 然后在 WSConn 上建立 yamux Client Session。
func (c *Client) connectDataChannel() error {
	return c.connectDataChannelRuntime(c.runtimeForStandaloneUse())
}

func (c *Client) connectDataChannelRuntime(rt *sessionRuntime) error {
	c.normalizeServerAddr()

	u, err := url.Parse(c.ServerAddr)
	if err != nil {
		return fmt.Errorf("解析 ServerAddr 失败: %w", err)
	}

	dataURL := c.deriveDataURL()
	dialer := c.newWSDialer(u.Hostname())
	dialer.Subprotocols = []string{protocol.WSSubProtocolData}
	wsConn, _, err := dialer.Dial(dataURL, nil)
	if err != nil {
		return fmt.Errorf("建立数据通道 WebSocket 失败: %w", err)
	}
	wsConn.SetReadLimit(wsDataMaxMessageSize)
	wsConn.SetReadDeadline(time.Now().Add(c.dataHandshakeTimeout))

	handshake := protocol.EncodeDataHandshake(c.ClientID, c.dataToken)
	if err := wsConn.WriteMessage(websocket.BinaryMessage, handshake); err != nil {
		wsConn.Close()
		return fmt.Errorf("发送数据通道握手失败: %w", err)
	}

	messageType, payload, err := wsConn.ReadMessage()
	if err != nil {
		wsConn.Close()
		return fmt.Errorf("读取数据通道握手响应失败: %w", err)
	}
	if messageType != websocket.BinaryMessage {
		wsConn.Close()
		return fmt.Errorf("数据通道握手响应类型错误: %d", messageType)
	}
	if len(payload) != 1 {
		wsConn.Close()
		return fmt.Errorf("数据通道握手响应长度错误: %d", len(payload))
	}
	if payload[0] != protocol.DataHandshakeOK {
		wsConn.Close()
		return fmt.Errorf("数据通道握手被拒绝 (状态码: 0x%02x)", payload[0])
	}

	wsConn.SetReadDeadline(time.Time{})

	// 建立 yamux Client Session
	session, err := mux.NewClientSession(mux.NewWSConn(wsConn), mux.DefaultConfig())
	if err != nil {
		wsConn.Close()
		return fmt.Errorf("创建 yamux Session 失败: %w", err)
	}

	c.setRuntimeDataSession(rt, session)

	return nil
}

// checkTLSFingerprint 检查 TLS 连接的证书指纹 (TOFU)
func (c *Client) checkTLSFingerprint(conn *websocket.Conn) error {
	tlsConn, ok := conn.UnderlyingConn().(*tls.Conn)
	if !ok {
		return nil // 非 TLS 连接，跳过
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("服务器未提供证书")
	}

	// 计算服务端证书指纹
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

	if c.TLSFingerprint == "" {
		// TOFU: 首次连接，记录指纹
		c.TLSFingerprint = serverFP
		log.Printf("🔒 TOFU: 首次连接，记录服务器证书指纹")
		log.Printf("🔒 指纹: %s", serverFP)
		// 持久化指纹
		if err := c.saveTLSFingerprint(serverFP); err != nil {
			log.Printf("⚠️ 保存 TLS 指纹失败: %v", err)
		}
		return nil
	}

	// 已有指纹，严格比对
	if serverFP != c.TLSFingerprint {
		return fmt.Errorf(
			"\n⚠️ TLS 证书指纹不匹配！可能存在中间人攻击。"+
				"\n  期望: %s"+
				"\n  实际: %s"+
				"\n  如果服务器确实更换了证书，请删除客户端状态文件后重试。",
			c.TLSFingerprint, serverFP,
		)
	}

	log.Printf("🔒 TLS 证书指纹校验通过")
	return nil
}

// acceptStreamLoop 持续接收 Server 发来的 yamux Stream。
// 每个 Stream 代表一个外部连接需要转发到本地服务。
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
					log.Printf("⚠️ AcceptStream 失败: %v", err)
				}
				c.failRuntime(rt, "data_session_closed")
				return
			}
		}

		go c.handleStream(stream)
	}
}

// handleStream 处理单个 yamux Stream：
// 1. 读取 StreamHeader 获取 proxy_name
// 2. 根据 proxy_name 查找本地代理配置
// 3. Dial 本地服务
// 4. Relay(stream, localConn)
func (c *Client) handleStream(stream *yamux.Stream) {
	defer stream.Close()

	// 读取 StreamHeader: [2B name长度] [NB proxy_name]
	var lenBuf [2]byte
	if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
		log.Printf("⚠️ 读取 StreamHeader 失败: %v", err)
		return
	}
	nameLen := binary.BigEndian.Uint16(lenBuf[:])
	if nameLen == 0 || nameLen > 1024 {
		log.Printf("⚠️ StreamHeader 名称长度异常: %d", nameLen)
		return
	}

	nameBuf := make([]byte, nameLen)
	if _, err := io.ReadFull(stream, nameBuf); err != nil {
		log.Printf("⚠️ 读取 StreamHeader 名称失败: %v", err)
		return
	}
	proxyName := string(nameBuf)

	// 查找代理配置
	val, ok := c.proxies.Load(proxyName)
	if !ok {
		log.Printf("⚠️ 未知的代理名称: %s", proxyName)
		return
	}
	cfg := val.(protocol.ProxyNewRequest)

	// 按代理类型分发
	if cfg.Type == protocol.ProxyTypeUDP {
		c.handleUDPStream(stream, cfg)
		return
	}

	// TCP 类型：连接本地服务
	localAddr := net.JoinHostPort(cfg.LocalIP, fmt.Sprintf("%d", cfg.LocalPort))
	localConn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		log.Printf("⚠️ 连接本地服务失败 [%s → %s]: %v", proxyName, localAddr, err)
		return
	}

	// 双向转发
	mux.Relay(stream, localConn)
}

// requestProxy 通过控制通道请求创建代理隧道
func (c *Client) requestProxy(cfg protocol.ProxyNewRequest) {
	c.requestProxyRuntime(c.runtimeForStandaloneUse(), cfg)
}

func (c *Client) requestProxyRuntime(rt *sessionRuntime, cfg protocol.ProxyNewRequest) {
	// 先注册本地代理配置
	c.proxies.Store(cfg.Name, cfg)

	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyNew, cfg)
	rt.connMu.Lock()
	conn := rt.conn
	rt.connMu.Unlock()
	if conn == nil {
		return
	}
	err := conn.WriteJSON(msg)
	if err != nil {
		log.Printf("❌ 发送代理请求失败 [%s]: %v", cfg.Name, err)
		return
	}
	log.Printf("📤 已请求创建代理隧道: %s (本地 %s:%d → 公网 :%d)",
		cfg.Name, cfg.LocalIP, cfg.LocalPort, cfg.RemotePort)
}

// heartbeatLoop 定时发送心跳
func (c *Client) heartbeatLoop() {
	c.heartbeatLoopRuntime(c.runtimeForStandaloneUse())
}

func (c *Client) heartbeatLoopRuntime(rt *sessionRuntime) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			msg, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
			rt.connMu.Lock()
			conn := rt.conn
			rt.connMu.Unlock()
			if conn == nil {
				return
			}
			err := conn.WriteJSON(msg)
			if err != nil {
				log.Printf("⚠️ 发送心跳失败: %v", err)
				c.failRuntime(rt, "heartbeat_write_failed")
				return
			}
		case <-rt.done:
			return
		}
	}
}

// probeLoop 定时采集并上报系统状态
func (c *Client) probeLoop() {
	c.probeLoopRuntime(c.runtimeForStandaloneUse())
}

func (c *Client) probeLoopRuntime(rt *sessionRuntime) {
	// 启动时立即上报一次
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

// reportProbe 采集系统状态并上报
func (c *Client) reportProbe() {
	c.reportProbeRuntime(c.runtimeForStandaloneUse())
}

func (c *Client) reportProbeRuntime(rt *sessionRuntime) {
	stats, err := CollectSystemStats(c.startTime)
	if err != nil {
		log.Printf("⚠️ 采集系统状态失败: %v", err)
		return
	}

	// 刷新公网 IP（内部有 5 分钟 TTL 控制）并附加到探针数据
	c.refreshPublicIPs()
	stats.PublicIPv4 = c.publicIPv4
	stats.PublicIPv6 = c.publicIPv6

	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	rt.connMu.Lock()
	conn := rt.conn
	rt.connMu.Unlock()
	if conn == nil {
		return
	}
	err = conn.WriteJSON(msg)
	if err != nil {
		log.Printf("⚠️ 上报探针数据失败: %v", err)
		c.failRuntime(rt, "probe_write_failed")
	}
}

// refreshPublicIPs 获取公网 IP 并缓存（仅当距上次获取超过 5 分钟时才实际请求）
func (c *Client) refreshPublicIPs() {
	if !c.publicIPFetched.IsZero() && time.Since(c.publicIPFetched) < 5*time.Minute {
		return // 还没过期，使用缓存
	}
	ipv4, ipv6 := netutil.FetchPublicIPs()
	if ipv4 != "" {
		c.publicIPv4 = ipv4
	}
	if ipv6 != "" {
		c.publicIPv6 = ipv6
	}
	c.publicIPFetched = time.Now()
}

// controlLoop 监听 Server 下发的控制消息
func (c *Client) controlLoop() {
	c.controlLoopRuntime(c.runtimeForStandaloneUse())
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
				log.Printf("⚠️ 控制通道连接异常: %v", err)
			}
			rt.closeDone()
			return
		}

		switch msg.Type {
		case protocol.MsgTypePong:
			// 心跳回复，忽略

		case protocol.MsgTypeProxyNew:
			// 服务端下发: 创建代理隧道
			var req protocol.ProxyNewRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ 解析代理指令失败: %v", err)
				continue
			}
			log.Printf("📥 收到服务端代理指令: %s (本地 %s:%d → 公网 :%d)",
				req.Name, req.LocalIP, req.LocalPort, req.RemotePort)
			c.proxies.Store(req.Name, req)
			resp, _ := protocol.NewMessage(protocol.MsgTypeProxyNewResp, protocol.ProxyNewResponse{
				Name:       req.Name,
				Success:    true,
				Message:    "proxy ready",
				RemotePort: req.RemotePort,
			})
			if err := conn.WriteJSON(resp); err != nil {
				log.Printf("⚠️ 返回代理 ready 失败 [%s]: %v", req.Name, err)
				c.failRuntime(rt, "proxy_ready_write_failed")
				return
			}

		case protocol.MsgTypeProxyNewResp:
			// 代理创建响应（客户端主动请求场景，如 Benchmark）
			var resp protocol.ProxyNewResponse
			if err := msg.ParsePayload(&resp); err != nil {
				log.Printf("⚠️ 解析代理响应失败: %v", err)
				continue
			}
			if resp.Success {
				log.Printf("✅ 代理隧道创建成功 [%s]，公网端口: %d", resp.Name, resp.RemotePort)
			} else {
				log.Printf("❌ 代理隧道创建失败 [%s]: %s", resp.Name, resp.Message)
			}

		case protocol.MsgTypeProxyClose:
			// 服务端下发: 关闭代理隧道
			var req protocol.ProxyCloseRequest
			if err := msg.ParsePayload(&req); err != nil {
				log.Printf("⚠️ 解析关闭代理指令失败: %v", err)
				continue
			}
			c.proxies.Delete(req.Name)
			log.Printf("🔌 代理隧道已关闭: %s (原因: %s)", req.Name, req.Reason)

		default:
			log.Printf("📩 收到控制消息: %s", msg.Type)
		}
	}
}
