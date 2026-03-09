package server

import (
	"fmt"
	"log"
	"net"
	"sync"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// ProxyTunnel 代表一条活跃的代理隧道
type ProxyTunnel struct {
	Config   protocol.ProxyConfig
	Listener net.Listener // 监听 RemotePort 的公网 listener
	done     chan struct{}
	once     sync.Once
}

// StartProxy 启动一条新的代理隧道。
// 在 RemotePort 上监听外部连接，每收到一个连接就通过 yamux 转发给 Agent。
func (s *Server) StartProxy(agent *AgentConn, req protocol.ProxyNewRequest) error {
	// 检查 Agent 数据通道
	agent.dataMu.RLock()
	hasData := agent.dataSession != nil && !agent.dataSession.IsClosed()
	agent.dataMu.RUnlock()
	if !hasData {
		return fmt.Errorf("Agent [%s] 数据通道未建立，无法创建代理", agent.ID)
	}

	// 检查是否已存在同名代理
	agent.proxyMu.Lock()
	if agent.proxies == nil {
		agent.proxies = make(map[string]*ProxyTunnel)
	}
	if _, exists := agent.proxies[req.Name]; exists {
		agent.proxyMu.Unlock()
		return fmt.Errorf("代理隧道 %q 已存在", req.Name)
	}
	agent.proxyMu.Unlock()

	// 监听公网端口
	addr := fmt.Sprintf(":%d", req.RemotePort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", req.RemotePort, err)
	}

	// 获取实际分配的端口（如果 RemotePort 为 0，系统会随机分配）
	actualPort := ln.Addr().(*net.TCPAddr).Port

	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:       req.Name,
			Type:       req.Type,
			LocalIP:    req.LocalIP,
			LocalPort:  req.LocalPort,
			RemotePort: actualPort,
			AgentID:    agent.ID,
			Status:     protocol.ProxyStatusActive,
		},
		Listener: ln,
		done:     make(chan struct{}),
	}

	agent.proxyMu.Lock()
	agent.proxies[req.Name] = tunnel
	agent.proxyMu.Unlock()

	log.Printf("🚇 代理隧道已创建: %s [:%d → %s:%d] Agent [%s]",
		req.Name, actualPort, req.LocalIP, req.LocalPort, agent.ID)

	// 启动 Accept 循环
	go s.proxyAcceptLoop(agent, tunnel)

	return nil
}

// proxyAcceptLoop 持续接受外部连接并通过 yamux 转发
func (s *Server) proxyAcceptLoop(agent *AgentConn, tunnel *ProxyTunnel) {
	defer tunnel.Listener.Close()

	for {
		extConn, err := tunnel.Listener.Accept()
		if err != nil {
			select {
			case <-tunnel.done:
				return // 正常关闭
			default:
				log.Printf("⚠️ 代理 [%s] Accept 失败: %v", tunnel.Config.Name, err)
				return
			}
		}

		go s.handleProxyConn(agent, tunnel, extConn)
	}
}

// handleProxyConn 处理单个外部连接：
// 1. 在 yamux Session 上 OpenStream
// 2. 向 Stream 写入 StreamHeader（proxyName）
// 3. Relay(stream, extConn) 双向搬运
func (s *Server) handleProxyConn(agent *AgentConn, tunnel *ProxyTunnel, extConn net.Conn) {
	defer extConn.Close()

	stream, err := s.openStreamToAgent(agent, tunnel.Config.Name)
	if err != nil {
		log.Printf("⚠️ 代理 [%s] 打开 Stream 失败: %v", tunnel.Config.Name, err)
		return
	}

	// Relay：双向搬运数据
	mux.Relay(stream, extConn)
}

// StopProxy 停止一条代理隧道
func (s *Server) StopProxy(agent *AgentConn, name string) error {
	agent.proxyMu.Lock()
	tunnel, exists := agent.proxies[name]
	if !exists {
		agent.proxyMu.Unlock()
		return fmt.Errorf("代理隧道 %q 不存在", name)
	}
	delete(agent.proxies, name)
	agent.proxyMu.Unlock()

	tunnel.once.Do(func() {
		close(tunnel.done)
		tunnel.Listener.Close()
	})

	log.Printf("🛑 代理隧道已停止: %s", name)
	return nil
}

// StopAllProxies 停止 Agent 的所有代理隧道
func (s *Server) StopAllProxies(agent *AgentConn) {
	agent.proxyMu.Lock()
	proxies := agent.proxies
	agent.proxies = nil
	agent.proxyMu.Unlock()

	for _, tunnel := range proxies {
		tunnel.once.Do(func() {
			close(tunnel.done)
			tunnel.Listener.Close()
		})
	}
}
