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

// StartProxy 启动一条新的代理隧道。
// 在 RemotePort 上监听外部连接，每收到一个连接就通过 yamux 转发给 Client。
func (s *Server) StartProxy(client *ClientConn, req protocol.ProxyNewRequest) error {
	// 1. 策略校验
	if s.adminStore != nil {
		policy := s.adminStore.GetTunnelPolicy()

		// 校验 Client 白名单
		if len(policy.ClientWhitelist) > 0 {
			allowed := false
			for _, allowHost := range policy.ClientWhitelist {
				if client.Info.Hostname == allowHost {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("Client 不在允许创建隧道的白名单中")
			}
		}

		// 校验端口白名单（新版 AllowedPorts 优先；为空时回退旧逻辑）
		if req.RemotePort != 0 {
			if s.adminStore.IsInitialized() && !s.adminStore.IsPortAllowed(req.RemotePort) {
				return fmt.Errorf("端口 %d 不在允许范围内", req.RemotePort)
			}

			// 旧版策略回退（AllowedPorts 为空时，IsPortAllowed 返回 true，这里不会执行）
			if policy.MinPort > 0 && req.RemotePort < policy.MinPort {
				return fmt.Errorf("请求端口 %d 小于允许的最小端口 %d", req.RemotePort, policy.MinPort)
			}
			if policy.MaxPort > 0 && req.RemotePort > policy.MaxPort {
				return fmt.Errorf("请求端口 %d 大于允许的最大端口 %d", req.RemotePort, policy.MaxPort)
			}
			for _, blocked := range policy.BlockedPorts {
				if req.RemotePort == blocked {
					return fmt.Errorf("请求端口 %d 在黑名单中", req.RemotePort)
				}
			}
		}
	}

	// 检查 Client 数据通道
	client.dataMu.RLock()
	hasData := client.dataSession != nil && !client.dataSession.IsClosed()
	client.dataMu.RUnlock()
	if !hasData {
		return fmt.Errorf("Client [%s] 数据通道未建立，无法创建代理", client.ID)
	}

	// 检查是否已存在同名代理
	client.proxyMu.Lock()
	if client.proxies == nil {
		client.proxies = make(map[string]*ProxyTunnel)
	}
	if _, exists := client.proxies[req.Name]; exists {
		client.proxyMu.Unlock()
		return fmt.Errorf("代理隧道 %q 已存在", req.Name)
	}
	client.proxyMu.Unlock()

	// UDP 类型：走 startUDPProxy 分支
	if req.Type == protocol.ProxyTypeUDP {
		tunnel := &ProxyTunnel{
			Config: protocol.ProxyConfig{
				Name:       req.Name,
				Type:       req.Type,
				LocalIP:    req.LocalIP,
				LocalPort:  req.LocalPort,
				RemotePort: req.RemotePort,
				ClientID:    client.ID,
				Status:     protocol.ProxyStatusActive,
			},
			done: make(chan struct{}),
		}

		client.proxyMu.Lock()
		client.proxies[req.Name] = tunnel
		client.proxyMu.Unlock()

		if err := s.startUDPProxy(client, tunnel); err != nil {
			client.proxyMu.Lock()
			delete(client.proxies, req.Name)
			client.proxyMu.Unlock()
			return err
		}

		// 自动分配端口后：检查分配到的端口是否在白名单内
		if req.RemotePort == 0 && s.adminStore != nil && s.adminStore.IsInitialized() {
			actualPort := tunnel.Config.RemotePort
			if !s.adminStore.IsPortAllowed(actualPort) {
				_ = s.StopProxy(client, req.Name)
				return fmt.Errorf("自动分配的端口 %d 不在允许范围内", actualPort)
			}
		}

		return nil
	}

	// TCP 类型：监听公网端口
	addr := fmt.Sprintf(":%d", req.RemotePort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", req.RemotePort, err)
	}

	// 获取实际分配的端口（如果 RemotePort 为 0，系统会随机分配）
	actualPort := ln.Addr().(*net.TCPAddr).Port

	// 自动分配端口后：检查分配到的端口是否在白名单内
	if req.RemotePort == 0 && s.adminStore != nil && s.adminStore.IsInitialized() {
		if !s.adminStore.IsPortAllowed(actualPort) {
			ln.Close()
			return fmt.Errorf("自动分配的端口 %d 不在允许范围内", actualPort)
		}
	}

	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:       req.Name,
			Type:       req.Type,
			LocalIP:    req.LocalIP,
			LocalPort:  req.LocalPort,
			RemotePort: actualPort,
			ClientID:    client.ID,
			Status:     protocol.ProxyStatusActive,
		},
		Listener: ln,
		done:     make(chan struct{}),
	}

	client.proxyMu.Lock()
	client.proxies[req.Name] = tunnel
	client.proxyMu.Unlock()

	log.Printf("🚇 代理隧道已创建: %s [:%d → %s:%d] Client [%s]",
		req.Name, actualPort, req.LocalIP, req.LocalPort, client.ID)

	// 启动 Accept 循环
	go s.proxyAcceptLoop(client, tunnel)

	return nil
}

// proxyAcceptLoop 持续接受外部连接并通过 yamux 转发
func (s *Server) proxyAcceptLoop(client *ClientConn, tunnel *ProxyTunnel) {
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

		go s.handleProxyConn(client, tunnel, extConn)
	}
}

// handleProxyConn 处理单个外部连接：
// 1. 在 yamux Session 上 OpenStream
// 2. 向 Stream 写入 StreamHeader（proxyName）
// 3. Relay(stream, extConn) 双向搬运
func (s *Server) handleProxyConn(client *ClientConn, tunnel *ProxyTunnel, extConn net.Conn) {
	defer extConn.Close()

	stream, err := s.openStreamToClient(client, tunnel.Config.Name)
	if err != nil {
		log.Printf("⚠️ 代理 [%s] 打开 Stream 失败: %v", tunnel.Config.Name, err)
		return
	}

	// Relay：双向搬运数据
	mux.Relay(stream, extConn)
}

// StopProxy 停止一条代理隧道
func (s *Server) StopProxy(client *ClientConn, name string) error {
	client.proxyMu.Lock()
	tunnel, exists := client.proxies[name]
	if !exists {
		client.proxyMu.Unlock()
		return fmt.Errorf("代理隧道 %q 不存在", name)
	}
	delete(client.proxies, name)
	client.proxyMu.Unlock()

	tunnel.once.Do(func() {
		close(tunnel.done)
		if tunnel.UDPState != nil {
			tunnel.UDPState.Close()
		}
		if tunnel.Listener != nil {
			tunnel.Listener.Close()
		}
	})

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
	client.proxyMu.Lock()
	tunnel, exists := client.proxies[name]
	if !exists {
		client.proxyMu.Unlock()
		return fmt.Errorf("代理隧道 %q 不存在", name)
	}
	client.proxyMu.Unlock()

	// 检查端口是否仍在白名单范围内
	if tunnel.Config.RemotePort != 0 && s.adminStore != nil && s.adminStore.IsInitialized() && !s.adminStore.IsPortAllowed(tunnel.Config.RemotePort) {
		return fmt.Errorf("端口 %d 不在当前允许范围内，无法恢复", tunnel.Config.RemotePort)
	}

	// 检查 Client 数据通道
	client.dataMu.RLock()
	hasData := client.dataSession != nil && !client.dataSession.IsClosed()
	client.dataMu.RUnlock()
	if !hasData {
		return fmt.Errorf("Client [%s] 数据通道未建立，无法恢复代理", client.ID)
	}

	// UDP 类型：重新启动 UDP 代理
	if tunnel.Config.Type == protocol.ProxyTypeUDP {
		addr := fmt.Sprintf(":%d", tunnel.Config.RemotePort)
		packetConn, err := net.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("重新监听 UDP 端口 %d 失败: %w", tunnel.Config.RemotePort, err)
		}

		state := &UDPProxyState{
			packetConn: packetConn,
			done:       make(chan struct{}),
		}

		client.proxyMu.Lock()
		tunnel.UDPState = state
		tunnel.done = make(chan struct{})
		tunnel.once = sync.Once{}
		tunnel.Config.Status = protocol.ProxyStatusActive
		client.proxyMu.Unlock()

		go s.udpReadLoop(client, tunnel, state)
		go s.udpReaper(state)

		log.Printf("▶️ UDP 代理隧道已恢复: %s [:%d]", name, tunnel.Config.RemotePort)
		return nil
	}

	// TCP 类型：重新监听端口
	addr := fmt.Sprintf(":%d", tunnel.Config.RemotePort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("重新监听端口 %d 失败: %w", tunnel.Config.RemotePort, err)
	}

	// 重置 tunnel 状态
	client.proxyMu.Lock()
	tunnel.Listener = ln
	tunnel.done = make(chan struct{})
	tunnel.once = sync.Once{}
	tunnel.Config.Status = protocol.ProxyStatusActive
	client.proxyMu.Unlock()

	// 启动 Accept 循环
	go s.proxyAcceptLoop(client, tunnel)

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
