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
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// Client 是客户端/Agent 的核心结构体
type Client struct {
	ServerAddr  string // Server 的 WebSocket 地址 (ws://host:port)
	Token       string // 认证令牌
	AgentID     string // Server 分配的 Agent ID
	conn        *websocket.Conn
	mu          sync.Mutex
	done        chan struct{}
	dataSession *yamux.Session // 数据通道 yamux Session
	dataMu      sync.RWMutex
	proxies     sync.Map // proxy_name -> ProxyNewRequest
	// ProxyConfigs 启动时自动请求创建的代理配置
	ProxyConfigs []protocol.ProxyNewRequest
}

// New 创建一个新的 Client 实例
func New(serverAddr, token string) *Client {
	return &Client{
		ServerAddr: serverAddr,
		Token:      token,
		done:       make(chan struct{}),
	}
}

// Start 启动客户端，连接 Server 并开始工作
func (c *Client) Start() error {
	// 1. 连接控制通道
	controlURL := fmt.Sprintf("%s/ws/control", c.ServerAddr)
	log.Printf("🔌 正在连接 Server: %s", controlURL)

	conn, _, err := websocket.DefaultDialer.Dial(controlURL, nil)
	if err != nil {
		return fmt.Errorf("连接 Server 失败: %w", err)
	}
	c.conn = conn
	defer conn.Close()

	log.Printf("✅ 已连接到 Server")

	// 2. 发送认证
	if err := c.authenticate(); err != nil {
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

	// 6. 自动请求创建代理隧道
	for _, cfg := range c.ProxyConfigs {
		go c.requestProxy(cfg)
	}

	// 7. 监听控制消息（阻塞）
	c.controlLoop()

	return nil
}

// authenticate 发送认证请求
func (c *Client) authenticate() error {
	hostname, _ := os.Hostname()

	authReq := protocol.AuthRequest{
		Token: c.Token,
		Agent: protocol.AgentInfo{
			Hostname: hostname,
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			Version:  "0.2.0",
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

	// 连接本地服务
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

		case protocol.MsgTypeProxyNewResp:
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

		default:
			log.Printf("📩 收到控制消息: %s", msg.Type)
		}
	}
}
