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

// ProxyTunnel represents an active proxy tunnel.
type ProxyTunnel struct {
	Config   protocol.ProxyConfig
	Listener net.Listener   // public listener on RemotePort (TCP tunnels only)
	UDPState *UDPProxyState // UDP proxy runtime state (nil for TCP tunnels)
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
		return newProxyRequestValidationError(fmt.Errorf("TCP/UDP tunnels require an explicit remote port"), protocol.TunnelMutationFieldRemotePort, "", http.StatusBadRequest)
	}
	if req.RemotePort == 80 || req.RemotePort == 443 {
		return newProxyRequestValidationError(fmt.Errorf("TCP/UDP tunnels cannot use reserved port %d", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusBadRequest)
	}
	if listenPort := serverListenPort(s); listenPort > 0 && req.RemotePort == listenPort {
		return newProxyRequestValidationError(fmt.Errorf("port %d conflicts with the NetsGo management service listen port", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusConflict)
	}

	if s.auth.adminStore != nil {
		if s.auth.adminStore.IsInitialized() && !s.auth.adminStore.IsPortAllowed(req.RemotePort) {
			return newProxyRequestValidationError(fmt.Errorf("port %d is not in the allowed range", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusBadRequest)
		}
	}

	if conflicts := findTCPUDPPortConflictNames(req.RemotePort, excludeName, excludeClientID, s); len(conflicts) > 0 {
		return newProxyRequestValidationError(fmt.Errorf("port %d is already in use by another tunnel", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusConflict)
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
		return fmt.Errorf("client [%s] data channel not established, cannot create proxy", client.ID)
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

	client.proxyMu.Lock()
	if client.proxies == nil {
		client.proxies = make(map[string]*ProxyTunnel)
	}
	if _, exists := client.proxies[req.Name]; exists {
		client.proxyMu.Unlock()
		return nil, fmt.Errorf("proxy tunnel %q already exists", req.Name)
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
		return fmt.Errorf("failed to listen on port %d: %w", tunnel.Config.RemotePort, err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port

	client.proxyMu.Lock()
	current, exists := client.proxies[tunnel.Config.Name]
	if !exists || current != tunnel {
		client.proxyMu.Unlock()
		_ = ln.Close()
		return fmt.Errorf("proxy tunnel %q not found", tunnel.Config.Name)
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

	log.Printf("🚇 proxy tunnel created: %s [:%d → %s:%d] Client [%s]",
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
			_ = tunnel.Listener.Close()
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
		return protocol.ProxyConfig{}, fmt.Errorf("proxy tunnel %q not found", name)
	}
	setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, "")
	return tunnel.Config, nil
}

// StartProxy starts a new proxy tunnel, listening on RemotePort and forwarding each connection to the client via yamux.
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
		log.Printf("⚠️ TCP proxy [%s] failed to persist error state: %v", tunnel.Config.Name, err)
	}
	s.emitTunnelChanged(client.ID, config, "error")
	if err := s.notifyClientProxyClose(client, tunnel.Config.Name, "runtime_error"); err != nil {
		log.Printf("⚠️ TCP proxy [%s] failed to notify client of close: %v", tunnel.Config.Name, err)
	}
}

// proxyAcceptLoop continuously accepts external connections and forwards them via yamux.
// It holds a snapshot of the listener/done for this activation to prevent stale loops from interfering with newer runtimes.
func (s *Server) proxyAcceptLoop(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, done <-chan struct{}) {
	defer func() { _ = listener.Close() }()

	for {
		extConn, err := listener.Accept()
		if err != nil {
			select {
			case <-done:
				return // normal shutdown
			default:
				log.Printf("⚠️ proxy [%s] Accept failed: %v", tunnel.Config.Name, err)
				s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel, listener, fmt.Sprintf("TCP proxy listener failed: %v", err))
				return
			}
		}

		go s.handleProxyConn(client, tunnel, listener, extConn)
	}
}

// handleProxyConn handles a single external connection: opens a stream on the yamux session,
// writes the StreamHeader (proxyName), then relays data bidirectionally.
func (s *Server) handleProxyConn(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, extConn net.Conn) {
	defer func() { _ = extConn.Close() }()

	stream, err := s.openStreamToClient(client, tunnel.Config.Name)
	if err != nil {
		log.Printf("⚠️ proxy [%s] open stream failed: %v", tunnel.Config.Name, err)
		s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel, listener, fmt.Sprintf("TCP proxy forwarding channel failed: %v", err))
		return
	}

	atob, btoa := mux.Relay(stream, extConn)
	if s.trafficStore != nil {
		s.trafficStore.RecordBytes(client.ID, tunnel.Config.Name, tunnel.Config.Type, uint64(btoa), uint64(atob))
	}
}

// StopProxy stops a proxy tunnel.
func (s *Server) StopProxy(client *ClientConn, name string) error {
	client.proxyMu.RLock()
	_, exists := client.proxies[name]
	client.proxyMu.RUnlock()
	if !exists {
		return fmt.Errorf("proxy tunnel %q not found", name)
	}
	s.removeTunnelRuntime(client, name)

	log.Printf("🛑 proxy tunnel stopped: %s", name)
	return nil
}

// StopAllProxies stops all proxy tunnels for a client.
func (s *Server) StopAllProxies(client *ClientConn) {
	client.proxyMu.Lock()
	proxies := client.proxies
	client.proxies = nil
	client.proxyMu.Unlock()

	for _, tunnel := range proxies {
		closeTunnelRuntimeResources(tunnel)
	}
}

// CloseProxyRuntime closes runtime resources for an existing tunnel; the manager layer owns state transitions.
func (s *Server) CloseProxyRuntime(client *ClientConn, name string) error {
	client.proxyMu.Lock()
	tunnel, exists := client.proxies[name]
	if !exists {
		client.proxyMu.Unlock()
		return fmt.Errorf("proxy tunnel %q not found", name)
	}
	closeTunnelRuntimeResources(tunnel)
	client.proxyMu.Unlock()

	log.Printf("🛑 proxy tunnel runtime closed: %s", name)
	return nil
}

// ReopenProxyRuntime re-binds runtime resources for an existing tunnel.
func (s *Server) ReopenProxyRuntime(client *ClientConn, name string) error {
	client.proxyMu.RLock()
	tunnel, exists := client.proxies[name]
	if !exists {
		client.proxyMu.RUnlock()
		return fmt.Errorf("proxy tunnel %q not found", name)
	}
	client.proxyMu.RUnlock()

	if tunnel.Config.RemotePort != 0 && s.auth.adminStore != nil && s.auth.adminStore.IsInitialized() && !s.auth.adminStore.IsPortAllowed(tunnel.Config.RemotePort) {
		return fmt.Errorf("port %d is no longer in the allowed range, cannot resume", tunnel.Config.RemotePort)
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		return err
	}

	log.Printf("▶️ proxy tunnel runtime reopened: %s [:%d]", name, tunnel.Config.RemotePort)
	return nil
}

// CloseExposedProxyRuntime marks all exposed tunnels offline after the client disconnects.
func (s *Server) CloseExposedProxyRuntime(client *ClientConn) {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()

	for _, tunnel := range client.proxies {
		if isTunnelExposed(tunnel.Config) {
			closeTunnelRuntimeResources(tunnel)
			setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, "")
		}
	}
}
