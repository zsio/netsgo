package client

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/netutil"
	"netsgo/pkg/protocol"
	buildversion "netsgo/pkg/version"
)

// Client 是客户端/Client 的核心结构体
type Client struct {
	ServerAddr     string // 服务器地址（支持 ws:// wss:// http:// https://，内部统一规范化）
	Key            string // 认证密钥（用于兑换 Token）
	Token          string // 客户端连接密钥（由 Key 兑换）
	InstallID      string // 稳定安装 ID
	StatePath      string // 安装 ID 持久化路径
	ClientID       string // Server 分配的稳定 Client ID
	TLSSkipVerify  bool
	TLSFingerprint string
	dataToken      string
	conn           *websocket.Conn
	mu             sync.Mutex
	done           chan struct{}
	doneMu         sync.Mutex     // 保护 done channel 的关闭操作
	dataSession    *yamux.Session // 数据通道 yamux Session
	dataMu         sync.RWMutex
	proxies        sync.Map // proxy_name -> ProxyNewRequest
	useTLS         bool
	publicIPv4     string       // 缓存的公网 IPv4
	publicIPv6     string       // 缓存的公网 IPv6
	publicIPMu     sync.RWMutex // 保护公网 IP 缓存
	// ProxyConfigs 由服务端下发，Benchmark 测试也可手动设置
	ProxyConfigs []protocol.ProxyNewRequest
	// DisableReconnect 禁用自动重连（用于测试等场景）
	DisableReconnect bool
}

// New 创建一个新的 Client 实例
func New(serverAddr, key string) *Client {
	return &Client{
		ServerAddr: serverAddr,
		Key:        key,
		done:       make(chan struct{}),
	}
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

// buildTLSConfig 构建客户端 TLS 配置
func (c *Client) buildTLSConfig(host string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: c.TLSSkipVerify,
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
	}
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
	errMsg := err.Error()
	// 认证失败不应重连
	if strings.HasPrefix(errMsg, "认证") {
		return true
	}
	return false
}

// Shutdown 优雅关闭客户端连接
// 发送 WebSocket 正常关闭帧，让服务端知道是主动断开而非异常
func (c *Client) Shutdown() {
	log.Printf("🛑 客户端开始优雅关闭...")

	// 发送 WebSocket 正常关闭帧
	c.mu.Lock()
	if c.conn != nil {
		c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client shutting down"),
		)
	}
	c.mu.Unlock()

	// 等待服务端处理关闭帧
	time.Sleep(100 * time.Millisecond)

	c.cleanup()

	log.Printf("✅ 客户端优雅关闭完成")
}

// closeDone 安全关闭 done channel（用 mutex 保证原子性，防止并发 double-close panic）
func (c *Client) closeDone() {
	c.doneMu.Lock()
	defer c.doneMu.Unlock()
	select {
	case <-c.done:
		// 已经关闭
	default:
		close(c.done)
	}
}

// cleanup 清理旧连接资源，为重连做准备
func (c *Client) cleanup() {
	// 先关闭 done channel，通知所有 goroutine 退出
	c.closeDone()

	// 等待 goroutine 退出（它们通过 done channel 感知退出信号）
	time.Sleep(100 * time.Millisecond)

	// 关闭 dataSession
	c.dataMu.Lock()
	if c.dataSession != nil && !c.dataSession.IsClosed() {
		c.dataSession.Close()
	}
	c.dataSession = nil
	c.dataMu.Unlock()

	// 关闭 WebSocket
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()

	// 清空 proxies
	c.proxies.Range(func(key, _ any) bool {
		c.proxies.Delete(key)
		return true
	})

	// 重置 done channel
	c.done = make(chan struct{})

	// 重置 ClientID
	c.ClientID = ""
	c.dataToken = ""
}

// connectAndRun 执行完整的连接流程并阻塞直到断连。
// 返回 nil 表示连接曾经成功但后来断开（可以重连），
// 返回 error 表示连接或认证失败。
func (c *Client) connectAndRun() error {
	if err := c.ensureInstallID(); err != nil {
		return fmt.Errorf("初始化客户端身份失败: %w", err)
	}

	c.normalizeServerAddr()

	// 1. 连接控制通道
	controlURL := c.deriveControlURL()
	log.Printf("🔌 正在连接 Server: %s", controlURL)

	dialer := websocket.DefaultDialer
	if c.useTLS {
		u, _ := url.Parse(c.ServerAddr)
		dialer = &websocket.Dialer{
			TLSClientConfig: c.buildTLSConfig(u.Hostname()),
		}
	}

	conn, _, err := dialer.Dial(controlURL, nil)
	if err != nil {
		return fmt.Errorf("连接 Server 失败: %w", err)
	}

	if c.useTLS && !c.TLSSkipVerify {
		if err := c.checkTLSFingerprint(conn); err != nil {
			conn.Close()
			return fmt.Errorf("TLS 证书指纹校验失败: %w", err)
		}
	}

	c.conn = conn

	log.Printf("✅ 已连接到 Server")

	// 2. 发送认证
	if err := c.authenticate(); err != nil {
		conn.Close()
		return fmt.Errorf("认证失败: %w", err)
	}
	log.Printf("✅ 认证成功，Client ID: %s", c.ClientID)

	// 3. 建立数据通道
	if err := c.connectDataChannel(); err != nil {
		log.Printf("⚠️ 数据通道建立失败（代理功能不可用）: %v", err)
	} else {
		log.Printf("✅ 数据通道已建立")
		// 启动 Stream 接收循环
		go c.acceptStreamLoop()
	}

	// 4. 启动心跳协程
	go c.heartbeatLoop()

	// 5. 启动探针上报协程
	go c.probeLoop()

	// 6. 启动公网 IP 定时刷新（首次已在认证前完成，此处负责后续刷新）
	go c.publicIPLoop()

	// 6. 如果有预设代理配置（Benchmark 模式），主动请求创建
	for _, cfg := range c.ProxyConfigs {
		go c.requestProxy(cfg)
	}

	// 7. 监听控制消息（阻塞，断连后返回）
	c.controlLoop()

	return nil
}

// authenticate 发送认证请求
// 优先使用 Token，失败后降级到 Key
func (c *Client) authenticate() error {
	hostname, _ := os.Hostname()
	localIP := netutil.GetOutboundIP()

	// 首次认证前同步获取公网 IP（保证首次查询有值）
	c.refreshPublicIPs()

	c.publicIPMu.RLock()
	ipv4 := c.publicIPv4
	ipv6 := c.publicIPv6
	c.publicIPMu.RUnlock()

	authReq := protocol.AuthRequest{
		Key:       c.Key,
		Token:     c.Token,
		InstallID: c.InstallID,
		Client: protocol.ClientInfo{
			Hostname:   hostname,
			OS:         runtime.GOOS,
			Arch:       runtime.GOARCH,
			IP:         localIP,
			Version:    buildversion.Current,
			PublicIPv4: ipv4,
			PublicIPv6: ipv6,
		},
	}

	// 如果有 Token，先只发 Token（不发 Key，避免服务端在 Token 无效时消耗 Key）
	if c.Token != "" {
		tokenReq := authReq
		tokenReq.Key = "" // 不发送 Key
		msg, err := protocol.NewMessage(protocol.MsgTypeAuth, tokenReq)
		if err != nil {
			return err
		}

		if err := c.conn.WriteJSON(msg); err != nil {
			return fmt.Errorf("发送认证消息失败: %w", err)
		}

		// 等待认证响应
		var resp protocol.Message
		if err := c.conn.ReadJSON(&resp); err != nil {
			return fmt.Errorf("读取认证响应失败: %w", err)
		}

		if resp.Type == protocol.MsgTypeAuthResp {
			var authResp protocol.AuthResponse
			if err := resp.ParsePayload(&authResp); err != nil {
				return fmt.Errorf("解析认证响应失败: %w", err)
			}
			if authResp.Success {
				c.ClientID = authResp.ClientID
				c.dataToken = authResp.DataToken
				log.Printf("✅ Token 认证成功")
				return nil
			}
			// Token 认证失败，清除本地 Token
			log.Printf("⚠️ Token 认证失败: %s，将尝试 Key 认证", authResp.Message)
			c.clearToken()
		} else {
			// 连接被关闭或异常响应，清除 Token 并重试
			log.Printf("⚠️ Token 认证异常响应: %s，清除本地 Token", resp.Type)
			c.clearToken()
			return fmt.Errorf("认证失败: 服务端异常响应")
		}

		// Token 失败，需要重新建立连接后用 Key 重试
		// 返回特定错误，由 connectAndRun 层面重连
		return fmt.Errorf("认证失败: Token 无效，需要重新连接")
	}

	// 没有 Token，用 Key 认证
	msg, err := protocol.NewMessage(protocol.MsgTypeAuth, authReq)
	if err != nil {
		return err
	}

	if err := c.conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("发送认证消息失败: %w", err)
	}

	// 等待认证响应
	var resp protocol.Message
	if err := c.conn.ReadJSON(&resp); err != nil {
		return fmt.Errorf("读取认证响应失败: %w", err)
	}

	if resp.Type != protocol.MsgTypeAuthResp {
		return fmt.Errorf("期望认证响应，收到: %s", resp.Type)
	}

	var authResp protocol.AuthResponse
	if err := resp.ParsePayload(&authResp); err != nil {
		return fmt.Errorf("解析认证响应失败: %w", err)
	}

	if !authResp.Success {
		return fmt.Errorf("认证被拒绝: %s", authResp.Message)
	}

	c.ClientID = authResp.ClientID
	c.dataToken = authResp.DataToken

	// 如果服务端返回了新 Token，保存它
	if authResp.Token != "" {
		c.Token = authResp.Token
		if err := c.saveToken(authResp.Token); err != nil {
			log.Printf("⚠️ 保存 Token 失败: %v", err)
		} else {
			log.Printf("🔑 Token 已保存，后续重连将自动使用")
		}
	}

	return nil
}

// connectDataChannel 建立数据通道。
// 从 ServerAddr 提取 host:port，建立 TCP/TLS 连接，
// 发送握手包（魔数 + ClientID + DataToken），然后在该连接上建立 yamux Client Session。
func (c *Client) connectDataChannel() error {
	// 解析 ServerAddr 获取 host:port
	u, err := url.Parse(c.ServerAddr)
	if err != nil {
		return fmt.Errorf("解析 ServerAddr 失败: %w", err)
	}

	host := u.Host
	if u.Port() == "" {
		if c.useTLS {
			host = net.JoinHostPort(u.Hostname(), "443")
		} else {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
	}

	var dataConn net.Conn
	if c.useTLS {
		log.Printf("🔒 数据通道使用 TLS 连接: %s", host)
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		tlsConn, err := tls.DialWithDialer(dialer, "tcp", host, c.buildTLSConfig(u.Hostname()))
		if err != nil {
			return fmt.Errorf("TLS 连接失败: %w", err)
		}
		dataConn = tlsConn
	} else {
		tcpConn, err := net.DialTimeout("tcp", host, 5*time.Second)
		if err != nil {
			return fmt.Errorf("TCP 连接失败: %w", err)
		}
		dataConn = tcpConn
	}

	// 设置握手超时（Server 应当即时回复，2s 足够）
	dataConn.SetDeadline(time.Now().Add(2 * time.Second))

	// 发送握手包: [1B 魔数] [2B ClientID长度] [NB ClientID] [2B DataToken长度] [NB DataToken]
	clientIDBytes := []byte(c.ClientID)
	dataTokenBytes := []byte(c.dataToken)
	handshake := make([]byte, 1+2+len(clientIDBytes)+2+len(dataTokenBytes))
	handshake[0] = protocol.DataChannelMagic
	binary.BigEndian.PutUint16(handshake[1:3], uint16(len(clientIDBytes)))
	copy(handshake[3:], clientIDBytes)
	offset := 3 + len(clientIDBytes)
	binary.BigEndian.PutUint16(handshake[offset:offset+2], uint16(len(dataTokenBytes)))
	copy(handshake[offset+2:], dataTokenBytes)

	if _, err := dataConn.Write(handshake); err != nil {
		dataConn.Close()
		return fmt.Errorf("发送握手失败: %w", err)
	}

	// 读取握手响应 (1 byte 状态码)
	var statusBuf [1]byte
	if _, err := io.ReadFull(dataConn, statusBuf[:]); err != nil {
		dataConn.Close()
		return fmt.Errorf("读取握手响应失败: %w", err)
	}

	// 清除 deadline
	dataConn.SetDeadline(time.Time{})

	if statusBuf[0] != protocol.DataHandshakeOK {
		dataConn.Close()
		return fmt.Errorf("数据通道握手被拒绝 (状态码: 0x%02x)", statusBuf[0])
	}

	// 建立 yamux Client Session
	session, err := mux.NewClientSession(dataConn, mux.DefaultConfig())
	if err != nil {
		dataConn.Close()
		return fmt.Errorf("创建 yamux Session 失败: %w", err)
	}

	c.dataMu.Lock()
	c.dataSession = session
	c.dataMu.Unlock()

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
	c.dataMu.RLock()
	session := c.dataSession
	c.dataMu.RUnlock()

	if session == nil {
		return
	}

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			select {
			case <-c.done:
				return
			default:
				if !session.IsClosed() {
					log.Printf("⚠️ AcceptStream 失败: %v", err)
				}
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
	// 等待数据通道就绪
	time.Sleep(500 * time.Millisecond)

	// 先注册本地代理配置
	c.proxies.Store(cfg.Name, cfg)

	msg, _ := protocol.NewMessage(protocol.MsgTypeProxyNew, cfg)
	c.mu.Lock()
	err := c.conn.WriteJSON(msg)
	c.mu.Unlock()
	if err != nil {
		log.Printf("❌ 发送代理请求失败 [%s]: %v", cfg.Name, err)
		return
	}
	log.Printf("📤 已请求创建代理隧道: %s (本地 %s:%d → 公网 :%d)",
		cfg.Name, cfg.LocalIP, cfg.LocalPort, cfg.RemotePort)
}

// heartbeatLoop 定时发送心跳
func (c *Client) heartbeatLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			msg, _ := protocol.NewMessage(protocol.MsgTypePing, nil)
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()
			if conn == nil {
				return
			}
			c.mu.Lock()
			err := c.conn.WriteJSON(msg)
			c.mu.Unlock()
			if err != nil {
				log.Printf("⚠️ 发送心跳失败: %v", err)
				return
			}
		case <-c.done:
			return
		}
	}
}

// probeLoop 定时采集并上报系统状态
func (c *Client) probeLoop() {
	// 启动时立即上报一次
	c.reportProbe()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.reportProbe()
		case <-c.done:
			return
		}
	}
}

// reportProbe 采集系统状态并上报
func (c *Client) reportProbe() {
	stats, err := CollectSystemStats()
	if err != nil {
		log.Printf("⚠️ 采集系统状态失败: %v", err)
		return
	}

	msg, _ := protocol.NewMessage(protocol.MsgTypeProbeReport, stats)
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return
	}
	c.mu.Lock()
	err = c.conn.WriteJSON(msg)
	c.mu.Unlock()
	if err != nil {
		log.Printf("⚠️ 上报探针数据失败: %v", err)
	}
}

// refreshPublicIPs 获取公网 IP 并缓存
func (c *Client) refreshPublicIPs() {
	ipv4, ipv6 := netutil.FetchPublicIPs()
	c.publicIPMu.Lock()
	if ipv4 != "" {
		c.publicIPv4 = ipv4
	}
	if ipv6 != "" {
		c.publicIPv6 = ipv6
	}
	c.publicIPMu.Unlock()
}

// publicIPLoop 每 5 分钟刷新一次公网 IP
func (c *Client) publicIPLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.refreshPublicIPs()
		case <-c.done:
			return
		}
	}
}

// controlLoop 监听 Server 下发的控制消息
func (c *Client) controlLoop() {
	for {
		var msg protocol.Message
		if err := c.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("⚠️ 控制通道连接异常: %v", err)
			}
			// 安全关闭 done channel（可能已被 Shutdown/cleanup 关闭）
			c.closeDone()
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
			// 注册本地代理配置，后续 Stream 会根据 proxy_name 查找
			c.proxies.Store(req.Name, req)

		case protocol.MsgTypeProxyNewResp:
			// 代理创建响应（客户端主动请求场景，如 Benchmark）
			var resp protocol.ProxyNewResponse
			if err := msg.ParsePayload(&resp); err != nil {
				log.Printf("⚠️ 解析代理响应失败: %v", err)
				continue
			}
			if resp.Success {
				log.Printf("✅ 代理隧道创建成功，公网端口: %d", resp.RemotePort)
			} else {
				log.Printf("❌ 代理隧道创建失败: %s", resp.Message)
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
