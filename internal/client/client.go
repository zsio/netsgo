package client

import (
	"encoding/binary"
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
	"netsgo/pkg/protocol"
	buildversion "netsgo/pkg/version"
)

// Client 是客户端/Agent 的核心结构体
type Client struct {
	ServerAddr  string // Server 的 WebSocket 地址 (ws://host:port)
	Key         string // 认证密钥
	InstallID   string // 稳定安装 ID
	StatePath   string // 安装 ID 持久化路径
	AgentID     string // Server 分配的稳定 Agent ID
	conn        *websocket.Conn
	mu          sync.Mutex
	done        chan struct{}
	dataSession *yamux.Session // 数据通道 yamux Session
	dataMu      sync.RWMutex
	proxies     sync.Map // proxy_name -> ProxyNewRequest
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

// cleanup 清理旧连接资源，为重连做准备
func (c *Client) cleanup() {
	// 先关闭 done channel（如果还没关闭），通知所有 goroutine 退出
	select {
	case <-c.done:
		// 已经关闭
	default:
		close(c.done)
	}

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

	// 重置 AgentID
	c.AgentID = ""
}

// connectAndRun 执行完整的连接流程并阻塞直到断连。
// 返回 nil 表示连接曾经成功但后来断开（可以重连），
// 返回 error 表示连接或认证失败。
func (c *Client) connectAndRun() error {
	if err := c.ensureInstallID(); err != nil {
		return fmt.Errorf("初始化客户端身份失败: %w", err)
	}

	// 1. 连接控制通道
	controlURL := fmt.Sprintf("%s/ws/control", c.ServerAddr)
	log.Printf("🔌 正在连接 Server: %s", controlURL)

	conn, _, err := websocket.DefaultDialer.Dial(controlURL, nil)
	if err != nil {
		return fmt.Errorf("连接 Server 失败: %w", err)
	}
	c.conn = conn

	log.Printf("✅ 已连接到 Server")

	// 2. 发送认证
	if err := c.authenticate(); err != nil {
		conn.Close()
		return fmt.Errorf("认证失败: %w", err)
	}
	log.Printf("✅ 认证成功，Agent ID: %s", c.AgentID)

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

	// 6. 如果有预设代理配置（Benchmark 模式），主动请求创建
	for _, cfg := range c.ProxyConfigs {
		go c.requestProxy(cfg)
	}

	// 7. 监听控制消息（阻塞，断连后返回）
	c.controlLoop()

	return nil
}

// authenticate 发送认证请求
func (c *Client) authenticate() error {
	hostname, _ := os.Hostname()

	authReq := protocol.AuthRequest{
		Key:       c.Key,
		InstallID: c.InstallID,
		Agent: protocol.AgentInfo{
			Hostname: hostname,
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			Version:  buildversion.Current,
		},
	}

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

	c.AgentID = authResp.AgentID
	return nil
}

// connectDataChannel 建立数据通道。
// 从 ServerAddr (ws://host:port) 提取 host:port，建立 TCP 连接，
// 发送握手包（魔数 + AgentID），然后在该连接上建立 yamux Client Session。
func (c *Client) connectDataChannel() error {
	// 解析 ServerAddr 获取 host:port
	u, err := url.Parse(c.ServerAddr)
	if err != nil {
		return fmt.Errorf("解析 ServerAddr 失败: %w", err)
	}

	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "80")
	}

	// 建立 TCP 连接
	tcpConn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return fmt.Errorf("TCP 连接失败: %w", err)
	}

	// 设置握手超时（Server 应当即时回复，2s 足够）
	tcpConn.SetDeadline(time.Now().Add(2 * time.Second))

	// 发送握手包: [1B 魔数] [2B AgentID长度] [NB AgentID]
	agentIDBytes := []byte(c.AgentID)
	handshake := make([]byte, 1+2+len(agentIDBytes))
	handshake[0] = protocol.DataChannelMagic
	binary.BigEndian.PutUint16(handshake[1:3], uint16(len(agentIDBytes)))
	copy(handshake[3:], agentIDBytes)

	if _, err := tcpConn.Write(handshake); err != nil {
		tcpConn.Close()
		return fmt.Errorf("发送握手失败: %w", err)
	}

	// 读取握手响应 (1 byte 状态码)
	var statusBuf [1]byte
	if _, err := io.ReadFull(tcpConn, statusBuf[:]); err != nil {
		tcpConn.Close()
		return fmt.Errorf("读取握手响应失败: %w", err)
	}

	// 清除 deadline
	tcpConn.SetDeadline(time.Time{})

	if statusBuf[0] != protocol.DataHandshakeOK {
		tcpConn.Close()
		return fmt.Errorf("数据通道握手被拒绝 (状态码: 0x%02x)", statusBuf[0])
	}

	// 建立 yamux Client Session
	session, err := mux.NewClientSession(tcpConn, mux.DefaultConfig())
	if err != nil {
		tcpConn.Close()
		return fmt.Errorf("创建 yamux Session 失败: %w", err)
	}

	c.dataMu.Lock()
	c.dataSession = session
	c.dataMu.Unlock()

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

// controlLoop 监听 Server 下发的控制消息
func (c *Client) controlLoop() {
	for {
		var msg protocol.Message
		if err := c.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("⚠️ 控制通道连接异常: %v", err)
			}
			close(c.done)
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
