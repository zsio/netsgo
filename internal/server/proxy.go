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
	Listener net.Listener   // 监听 RemotePort 的公网 listener（TCP 隧道使用）
	UDPState *UDPProxyState // UDP 代理运行时状态（TCP 隧道为 nil）
	done     chan struct{}
	once     sync.Once
}

func (s *Server) validateProxyRequest(client *ClientConn, req protocol.ProxyNewRequest) error {
	if s.adminStore != nil {
		// 校验端口白名单
		if req.RemotePort != 0 {
			if s.adminStore.IsInitialized() && !s.adminStore.IsPortAllowed(req.RemotePort) {
				return fmt.Errorf("端口 %d 不在允许范围内", req.RemotePort)
			}
		}
	}

	return nil
}

func (s *Server) ensureClientDataReady(client *ClientConn) error {
	client.dataMu.RLock()
	hasData := client.dataSession != nil && !client.dataSession.IsClosed()
	client.dataMu.RUnlock()
	if !hasData {
		return fmt.Errorf("Client [%s] 数据通道未建立，无法创建代理", client.ID)
	}
	return nil
}

func (s *Server) prepareProxyTunnel(client *ClientConn, req protocol.ProxyNewRequest, status string) (*ProxyTunnel, error) {
	if err := s.validateProxyRequest(client, req); err != nil {
		return nil, err
	}
	if err := s.ensureClientDataReady(client); err != nil {
		return nil, err
	}

	// 检查是否已存在同名代理
	client.proxyMu.Lock()
	if client.proxies == nil {
		client.proxies = make(map[string]*ProxyTunnel)
	}
	if _, exists := client.proxies[req.Name]; exists {
		client.proxyMu.Unlock()
		return nil, fmt.Errorf("代理隧道 %q 已存在", req.Name)
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:       req.Name,
			Type:       req.Type,
			LocalIP:    req.LocalIP,
			LocalPort:  req.LocalPort,
			RemotePort: req.RemotePort,
			ClientID:   client.ID,
			Status:     status,
		},
		done: make(chan struct{}),
	}

	client.proxies[req.Name] = tunnel
	client.proxyMu.Unlock()
	return tunnel, nil
}

func (s *Server) activatePreparedTunnel(client *ClientConn, tunnel *ProxyTunnel) error {
	if err := s.validateProxyRequest(client, tunnel.Config.ToProxyNewRequest()); err != nil {
		return err
	}
	if err := s.ensureClientDataReady(client); err != nil {
		return err
	}

	if tunnel.Config.Type == protocol.ProxyTypeUDP {
		tunnel.done = make(chan struct{})
		tunnel.once = sync.Once{}
		tunnel.Config.Error = ""
		if err := s.startUDPProxy(client, tunnel); err != nil {
			return err
		}
		if tunnel.Config.RemotePort != 0 && s.adminStore != nil && s.adminStore.IsInitialized() && !s.adminStore.IsPortAllowed(tunnel.Config.RemotePort) {
			s.removeTunnelRuntime(client, tunnel.Config.Name)
			return fmt.Errorf("自动分配的端口 %d 不在允许范围内", tunnel.Config.RemotePort)
		}
		tunnel.Config.Status = protocol.ProxyStatusActive
		return nil
	}

	addr := fmt.Sprintf(":%d", tunnel.Config.RemotePort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", tunnel.Config.RemotePort, err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	if tunnel.Config.RemotePort == 0 && s.adminStore != nil && s.adminStore.IsInitialized() && !s.adminStore.IsPortAllowed(actualPort) {
		ln.Close()
		return fmt.Errorf("自动分配的端口 %d 不在允许范围内", actualPort)
	}

	client.proxyMu.Lock()
	current, exists := client.proxies[tunnel.Config.Name]
	if !exists || current != tunnel {
		client.proxyMu.Unlock()
		ln.Close()
		return fmt.Errorf("代理隧道 %q 不存在", tunnel.Config.Name)
	}
	tunnel.Listener = ln
	tunnel.done = make(chan struct{})
	tunnel.once = sync.Once{}
	tunnel.Config.RemotePort = actualPort
	tunnel.Config.Status = protocol.ProxyStatusActive
	tunnel.Config.Error = ""
	listener := tunnel.Listener
	done := tunnel.done
	proxyName := tunnel.Config.Name
	localIP := tunnel.Config.LocalIP
	localPort := tunnel.Config.LocalPort
	client.proxyMu.Unlock()

	log.Printf("🚇 代理隧道已创建: %s [:%d → %s:%d] Client [%s]",
		proxyName, actualPort, localIP, localPort, client.ID)

	go s.proxyAcceptLoop(client, proxyName, listener, done)
	return nil
}

func (s *Server) removeTunnelRuntime(client *ClientConn, name string) {
	client.proxyMu.Lock()
	tunnel, exists := client.proxies[name]
	if exists {
		delete(client.proxies, name)
	}
	client.proxyMu.Unlock()
	if !exists {
		return
	}

	tunnel.once.Do(func() {
		close(tunnel.done)
		if tunnel.UDPState != nil {
			tunnel.UDPState.Close()
		}
		if tunnel.Listener != nil {
			tunnel.Listener.Close()
		}
	})
}

func (s *Server) stageTunnelPending(client *ClientConn, name string) (protocol.ProxyConfig, error) {
	if err := s.ensureClientDataReady(client); err != nil {
		return protocol.ProxyConfig{}, err
	}

	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()

	tunnel, exists := client.proxies[name]
	if !exists {
		return protocol.ProxyConfig{}, fmt.Errorf("代理隧道 %q 不存在", name)
	}
	tunnel.Config.Status = protocol.ProxyStatusPending
	tunnel.Config.Error = ""
	return tunnel.Config, nil
}

func (s *Server) setTunnelError(client *ClientConn, name, message string) (protocol.ProxyConfig, bool) {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()

	tunnel, exists := client.proxies[name]
	if !exists {
		return protocol.ProxyConfig{}, false
	}
	tunnel.Config.Status = protocol.ProxyStatusError
	tunnel.Config.Error = message
	return tunnel.Config, true
}

// StartProxy 启动一条新的代理隧道。
// 在 RemotePort 上监听外部连接，每收到一个连接就通过 yamux 转发给 Client。
func (s *Server) StartProxy(client *ClientConn, req protocol.ProxyNewRequest) error {
	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyStatusPending)
	if err != nil {
		return err
	}
	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		s.removeTunnelRuntime(client, req.Name)
		return err
	}

	return nil
}

// proxyAcceptLoop 持续接受外部连接并通过 yamux 转发。
// 它只持有本次激活对应的 listener/done 快照，避免旧 loop 误操作新一代 runtime。
func (s *Server) proxyAcceptLoop(client *ClientConn, proxyName string, listener net.Listener, done <-chan struct{}) {
	defer listener.Close()

	for {
		extConn, err := listener.Accept()
		if err != nil {
			select {
			case <-done:
				return // 正常关闭
			default:
				log.Printf("⚠️ 代理 [%s] Accept 失败: %v", proxyName, err)
				return
			}
		}

		go s.handleProxyConn(client, proxyName, extConn)
	}
}

// handleProxyConn 处理单个外部连接：
// 1. 在 yamux Session 上 OpenStream
// 2. 向 Stream 写入 StreamHeader（proxyName）
// 3. Relay(stream, extConn) 双向搬运
func (s *Server) handleProxyConn(client *ClientConn, proxyName string, extConn net.Conn) {
	defer extConn.Close()

	stream, err := s.openStreamToClient(client, proxyName)
	if err != nil {
		log.Printf("⚠️ 代理 [%s] 打开 Stream 失败: %v", proxyName, err)
		return
	}

	// Relay：双向搬运数据
	mux.Relay(stream, extConn)
}

// StopProxy 停止一条代理隧道
func (s *Server) StopProxy(client *ClientConn, name string) error {
	client.proxyMu.RLock()
	_, exists := client.proxies[name]
	client.proxyMu.RUnlock()
	if !exists {
		return fmt.Errorf("代理隧道 %q 不存在", name)
	}
	s.removeTunnelRuntime(client, name)

	log.Printf("🛑 代理隧道已停止: %s", name)
	return nil
}

// StopAllProxies 停止 Client 的所有代理隧道
func (s *Server) StopAllProxies(client *ClientConn) {
	client.proxyMu.Lock()
	proxies := client.proxies
	client.proxies = nil
	client.proxyMu.Unlock()

	for _, tunnel := range proxies {
		tunnel.once.Do(func() {
			close(tunnel.done)
			if tunnel.UDPState != nil {
				tunnel.UDPState.Close()
			}
			if tunnel.Listener != nil {
				tunnel.Listener.Close()
			}
		})
	}
}

// PauseProxy 暂停一条代理隧道（关闭 Listener 但保留配置记录）
func (s *Server) PauseProxy(client *ClientConn, name string) error {
	client.proxyMu.Lock()
	tunnel, exists := client.proxies[name]
	if !exists {
		client.proxyMu.Unlock()
		return fmt.Errorf("代理隧道 %q 不存在", name)
	}

	// 关闭 Listener
	tunnel.once.Do(func() {
		close(tunnel.done)
		if tunnel.UDPState != nil {
			tunnel.UDPState.Close()
		}
		if tunnel.Listener != nil {
			tunnel.Listener.Close()
		}
	})
	tunnel.Config.Status = protocol.ProxyStatusPaused
	client.proxyMu.Unlock()

	log.Printf("⏸️ 代理隧道已暂停: %s", name)
	return nil
}

// ResumeProxy 恢复一条暂停的代理隧道（重新监听端口）
func (s *Server) ResumeProxy(client *ClientConn, name string) error {
	client.proxyMu.RLock()
	tunnel, exists := client.proxies[name]
	if !exists {
		client.proxyMu.RUnlock()
		return fmt.Errorf("代理隧道 %q 不存在", name)
	}
	client.proxyMu.RUnlock()

	// 检查端口是否仍在白名单范围内
	if tunnel.Config.RemotePort != 0 && s.adminStore != nil && s.adminStore.IsInitialized() && !s.adminStore.IsPortAllowed(tunnel.Config.RemotePort) {
		return fmt.Errorf("端口 %d 不在当前允许范围内，无法恢复", tunnel.Config.RemotePort)
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		return err
	}

	log.Printf("▶️ 代理隧道已恢复: %s [:%d]", name, tunnel.Config.RemotePort)
	return nil
}

// PauseAllProxies 暂停 Client 的所有活跃代理隧道（保留配置，断连时使用）
func (s *Server) PauseAllProxies(client *ClientConn) {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()

	for _, tunnel := range client.proxies {
		if tunnel.Config.Status == protocol.ProxyStatusActive {
			tunnel.once.Do(func() {
				close(tunnel.done)
				if tunnel.UDPState != nil {
					tunnel.UDPState.Close()
				}
				if tunnel.Listener != nil {
					tunnel.Listener.Close()
				}
			})
			tunnel.Config.Status = protocol.ProxyStatusPaused
		}
	}
}
