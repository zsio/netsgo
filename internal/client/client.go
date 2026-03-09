package client

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

// Client 是客户端/Agent 的核心结构体
type Client struct {
	ServerAddr string            // Server 的 WebSocket 地址
	Token      string            // 认证令牌
	AgentID    string            // Server 分配的 Agent ID
	conn       *websocket.Conn   // 控制通道连接
	mu         sync.Mutex
	done       chan struct{}
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

	// 3. 启动心跳协程
	go c.heartbeatLoop()

	// 4. 启动探针上报协程
	go c.probeLoop()

	// 5. 监听控制消息
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
			Version:  "0.1.0",
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
		default:
			log.Printf("📩 收到控制消息: %s", msg.Type)
		}
	}
}
