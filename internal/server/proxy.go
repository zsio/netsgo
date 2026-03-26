package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
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

type proxyRequestValidationError struct {
	err    error
	code   string
	field  string
	status int
}

func (e *proxyRequestValidationError) Error() string {
	return e.err.Error()
}

func (e *proxyRequestValidationError) ErrorCode() string {
	return e.code
}

func (e *proxyRequestValidationError) Field() string {
	return e.field
}

func (e *proxyRequestValidationError) StatusCode() int {
	if e.status == 0 {
		return http.StatusConflict
	}
	return e.status
}

func newProxyRequestValidationError(err error, field, code string, status int) *proxyRequestValidationError {
	return &proxyRequestValidationError{
		err:    err,
		code:   code,
		field:  field,
		status: status,
	}
}

func (s *Server) validateProxyRequest(client *ClientConn, req protocol.ProxyNewRequest) error {
	return s.validateProxyRequestWithExclusions(client, req, "", "")
}

func (s *Server) validateProxyRequestWithExclusions(client *ClientConn, req protocol.ProxyNewRequest, excludeName, excludeClientID string) error {
	if req.Type == protocol.ProxyTypeHTTP {
		if err := validateDomain(req.Domain); err != nil {
			return newProxyRequestValidationError(err, protocol.TunnelMutationFieldDomain, protocol.TunnelMutationErrorCodeDomainInvalid, http.StatusBadRequest)
		}
		if err := checkDomainConflict(req.Domain, excludeName, excludeClientID, s); err != nil {
			return err
		}
		return nil
	}

	if req.RemotePort <= 0 {
		return newProxyRequestValidationError(fmt.Errorf("TCP/UDP 隧道必须填写明确的公网端口"), protocol.TunnelMutationFieldRemotePort, "", http.StatusBadRequest)
	}
	if req.RemotePort == 80 || req.RemotePort == 443 {
		return newProxyRequestValidationError(fmt.Errorf("TCP/UDP 隧道不能使用保留端口 %d", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusBadRequest)
	}
	if listenPort := serverListenPort(s); listenPort > 0 && req.RemotePort == listenPort {
		return newProxyRequestValidationError(fmt.Errorf("端口 %d 与 NetsGo 管理服务监听端口冲突", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusConflict)
	}

	if s.adminStore != nil {
		// 校验端口白名单
		if s.adminStore.IsInitialized() && !s.adminStore.IsPortAllowed(req.RemotePort) {
			return newProxyRequestValidationError(fmt.Errorf("端口 %d 不在允许范围内", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusBadRequest)
		}
	}

	if conflicts := findTCPUDPPortConflictNames(req.RemotePort, excludeName, excludeClientID, s); len(conflicts) > 0 {
		return newProxyRequestValidationError(fmt.Errorf("端口 %d 已被隧道占用", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusConflict)
	}

	return nil
}

func serverListenPort(s *Server) int {
	if s == nil {
		return 0
	}
	if s.listener != nil {
		switch addr := s.listener.Addr().(type) {
		case *net.TCPAddr:
			return addr.Port
		default:
			_, port, err := net.SplitHostPort(addr.String())
			if err == nil {
				value, _ := strconv.Atoi(port)
				return value
			}
		}
	}
	if s.Port > 0 {
		return s.Port
	}
	return 0
}

func findTCPUDPPortConflictNames(port int, excludeName, excludeClientID string, server *Server) []string {
	if port <= 0 || server == nil {
		return []string{}
	}

	conflicts := []string{}
	seen := map[string]struct{}{}
	matchAndAppend := func(clientID, name, tunnelType string, remotePort int) {
		if tunnelType != protocol.ProxyTypeTCP && tunnelType != protocol.ProxyTypeUDP {
			return
		}
		if excludeName != "" && excludeClientID != "" && name == excludeName && clientID == excludeClientID {
			return
		}
		if remotePort != port {
			return
		}

		key := clientID + ":" + name
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		conflicts = append(conflicts, key)
	}

	server.RangeClients(func(clientID string, client *ClientConn) bool {
		client.RangeProxies(func(name string, tunnel *ProxyTunnel) bool {
			matchAndAppend(clientID, name, tunnel.Config.Type, tunnel.Config.RemotePort)
			return true
		})
		return true
	})

	if server.store != nil {
		for _, tunnel := range server.store.GetAllTunnels() {
			matchAndAppend(tunnel.ClientID, tunnel.Name, tunnel.Type, tunnel.RemotePort)
		}
	}

	return conflicts
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

func (s *Server) prepareProxyTunnel(client *ClientConn, req protocol.ProxyNewRequest, desiredState, runtimeState string) (*ProxyTunnel, error) {
	return s.prepareProxyTunnelWithExclusions(client, req, desiredState, runtimeState, "", "")
}

func (s *Server) prepareProxyTunnelWithExclusions(client *ClientConn, req protocol.ProxyNewRequest, desiredState, runtimeState, excludeName, excludeClientID string) (*ProxyTunnel, error) {
	if err := s.validateProxyRequestWithExclusions(client, req, excludeName, excludeClientID); err != nil {
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
	if req.Type == protocol.ProxyTypeHTTP {
		req.RemotePort = 0
	}
	tunnel := &ProxyTunnel{
		Config: protocol.ProxyConfig{
			Name:       req.Name,
			Type:       req.Type,
			LocalIP:    req.LocalIP,
			LocalPort:  req.LocalPort,
			RemotePort: req.RemotePort,
			Domain:     req.Domain,
			ClientID:   client.ID,
		},
		done: make(chan struct{}),
	}
	setProxyConfigStates(&tunnel.Config, desiredState, runtimeState, "")

	client.proxies[req.Name] = tunnel
	client.proxyMu.Unlock()
	return tunnel, nil
}

func (s *Server) activatePreparedTunnel(client *ClientConn, tunnel *ProxyTunnel) error {
	if err := s.validateProxyRequestWithExclusions(client, tunnel.Config.ToProxyNewRequest(), tunnel.Config.Name, client.ID); err != nil {
		return err
	}
	if err := s.ensureClientDataReady(client); err != nil {
		return err
	}

	if tunnel.Config.Type == protocol.ProxyTypeHTTP {
		// HTTP 隧道不绑定公网端口，通过 HTTP 路由层分发
		setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
		return nil
	}

	if tunnel.Config.Type == protocol.ProxyTypeUDP {
		tunnel.done = make(chan struct{})
		tunnel.once = sync.Once{}
		tunnel.Config.Error = ""
		if err := s.startUDPProxy(client, tunnel); err != nil {
			return err
		}
		setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
		return nil
	}

	addr := fmt.Sprintf(":%d", tunnel.Config.RemotePort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", tunnel.Config.RemotePort, err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port

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
	setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
	listener := tunnel.Listener
	done := tunnel.done
	proxyName := tunnel.Config.Name
	localIP := tunnel.Config.LocalIP
	localPort := tunnel.Config.LocalPort
	client.proxyMu.Unlock()

	log.Printf("🚇 代理隧道已创建: %s [:%d → %s:%d] Client [%s]",
		proxyName, actualPort, localIP, localPort, client.ID)

	go s.proxyAcceptLoop(client, tunnel, listener, done)
	return nil
}

func closeTunnelRuntimeResources(tunnel *ProxyTunnel) {
	if tunnel == nil {
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
	tunnel.Listener = nil
	tunnel.UDPState = nil
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

	closeTunnelRuntimeResources(tunnel)
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
	setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, "")
	return tunnel.Config, nil
}

func (s *Server) setTunnelError(client *ClientConn, name, message string) (protocol.ProxyConfig, bool) {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()

	tunnel, exists := client.proxies[name]
	if !exists {
		return protocol.ProxyConfig{}, false
	}
	setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	return tunnel.Config, true
}

// StartProxy 启动一条新的代理隧道。
// 在 RemotePort 上监听外部连接，每收到一个连接就通过 yamux 转发给 Client。
func (s *Server) StartProxy(client *ClientConn, req protocol.ProxyNewRequest) error {
	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		return err
	}
	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		s.removeTunnelRuntime(client, req.Name)
		return err
	}

	return nil
}

func (s *Server) markTCPProxyRuntimeErrorIfCurrent(
	client *ClientConn,
	tunnel *ProxyTunnel,
	listener net.Listener,
	message string,
) {
	client.proxyMu.Lock()
	current, exists := client.proxies[tunnel.Config.Name]
	if !exists ||
		current != tunnel ||
		current.Listener != listener ||
		!isTunnelExposed(current.Config) {
		client.proxyMu.Unlock()
		return
	}
	closeTunnelRuntimeResources(current)
	setProxyConfigStates(&current.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	config := current.Config
	client.proxyMu.Unlock()

	if err := s.persistTunnelStates(client.ID, tunnel.Config.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message); err != nil {
		log.Printf("⚠️ TCP 代理 [%s] 持久化 error 状态失败: %v", tunnel.Config.Name, err)
	}
	s.emitTunnelChanged(client.ID, config, "error")
	if err := s.notifyClientProxyClose(client, tunnel.Config.Name, "runtime_error"); err != nil {
		log.Printf("⚠️ TCP 代理 [%s] 通知 client 关闭失败: %v", tunnel.Config.Name, err)
	}
}

// proxyAcceptLoop 持续接受外部连接并通过 yamux 转发。
// 它只持有本次激活对应的 listener/done 快照，避免旧 loop 误操作新一代 runtime。
func (s *Server) proxyAcceptLoop(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, done <-chan struct{}) {
	defer listener.Close()

	for {
		extConn, err := listener.Accept()
		if err != nil {
			select {
			case <-done:
				return // 正常关闭
			default:
				log.Printf("⚠️ 代理 [%s] Accept 失败: %v", tunnel.Config.Name, err)
				s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel, listener, fmt.Sprintf("TCP 代理监听失败: %v", err))
				return
			}
		}

		go s.handleProxyConn(client, tunnel.Config.Name, extConn)
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
		closeTunnelRuntimeResources(tunnel)
	}
}

// PauseProxy 暂停一条代理隧道（只关闭运行时资源，业务状态由 manager 层写入）
func (s *Server) PauseProxy(client *ClientConn, name string) error {
	client.proxyMu.Lock()
	tunnel, exists := client.proxies[name]
	if !exists {
		client.proxyMu.Unlock()
		return fmt.Errorf("代理隧道 %q 不存在", name)
	}
	closeTunnelRuntimeResources(tunnel)
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
		if isTunnelExposed(tunnel.Config) {
			closeTunnelRuntimeResources(tunnel)
			setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, "")
		}
	}
}
