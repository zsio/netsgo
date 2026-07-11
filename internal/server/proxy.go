package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

// ProxyTunnel represents an active proxy tunnel.
type ProxyTunnel struct {
	Config      protocol.ProxyConfig
	Listener    net.Listener   // public listener on RemotePort (TCP tunnels only)
	UDPState    *UDPProxyState // UDP proxy runtime state (nil for TCP tunnels)
	sourceCIDRs []*net.IPNet
	runtime     tunnelRuntimeSnapshot
	limits      *directionalBandwidthRuntime
	done        chan struct{}
	once        sync.Once
}

// proxyActivationSnapshot contains the immutable inputs used by one listener
// activation. Callers must build it while holding ClientConn.proxyMu so runtime
// goroutines never have to read mutable ProxyTunnel fields.
type proxyActivationSnapshot struct {
	config          protocol.ProxyConfig
	runtimeRevision uint64
	listener        net.Listener
	udpState        *UDPProxyState
	done            <-chan struct{}
	sourceCIDRs     []*net.IPNet
	limits          *directionalBandwidthRuntime
}

func proxyActivationSnapshotLocked(tunnel *ProxyTunnel) proxyActivationSnapshot {
	activation := proxyActivationSnapshotReadLocked(tunnel)
	if tunnel != nil {
		activation.runtimeRevision = ensureTunnelRuntimeRevision(tunnel)
	}
	return activation
}

func proxyActivationSnapshotReadLocked(tunnel *ProxyTunnel) proxyActivationSnapshot {
	if tunnel == nil {
		return proxyActivationSnapshot{}
	}
	sourceCIDRs := append([]*net.IPNet(nil), tunnel.sourceCIDRs...)
	if len(sourceCIDRs) == 0 {
		if policy, err := decodeIngressAccessPolicyFromProxyConfig(tunnel.Config); err == nil {
			sourceCIDRs = policy.sourceCIDRs
		}
	}
	return proxyActivationSnapshot{
		config:          tunnel.Config,
		runtimeRevision: tunnel.runtime.Revision,
		listener:        tunnel.Listener,
		udpState:        tunnel.UDPState,
		done:            tunnel.done,
		sourceCIDRs:     sourceCIDRs,
		limits:          tunnel.limits,
	}
}

func proxyActivationDoneOpen(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
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
	if strings.TrimSpace(req.Name) == "" {
		return newProxyRequestValidationError(fmt.Errorf("tunnel name is required"), protocol.TunnelMutationFieldName, "", http.StatusBadRequest)
	}

	if err := validateBandwidthSettings(req.BandwidthSettings); err != nil {
		switch {
		case req.IngressBPS < 0:
			return newProxyRequestValidationError(err, protocol.TunnelMutationFieldIngressBPS, "", http.StatusBadRequest)
		case req.EgressBPS < 0:
			return newProxyRequestValidationError(err, protocol.TunnelMutationFieldEgressBPS, "", http.StatusBadRequest)
		default:
			return newProxyRequestValidationError(err, "", "", http.StatusBadRequest)
		}
	}

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
	if err := validateServerBindIP(req.BindIP); err != nil {
		return newProxyRequestValidationError(err, "bind_ip", protocol.TunnelMutationErrorCodeInvalidBindIP, http.StatusBadRequest)
	}
	if listenPort := serverListenPort(s); listenPort > 0 && req.RemotePort == listenPort {
		return newProxyRequestValidationError(fmt.Errorf("port %d conflicts with the NetsGo management service listen port", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusConflict)
	}

	if s.auth.adminStore != nil {
		initialized, err := s.auth.adminStore.IsInitializedE()
		if err != nil {
			return newProxyRequestValidationError(fmt.Errorf("failed to read initialization state: %w", err), protocol.TunnelMutationFieldRemotePort, "", http.StatusServiceUnavailable)
		}
		if initialized && !s.auth.adminStore.IsPortAllowed(req.RemotePort) {
			return newProxyRequestValidationError(fmt.Errorf("port %d is not in the allowed range", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusBadRequest)
		}
	}

	conflicts, err := findTCPUDPPortConflictNames(req.RemotePort, req.BindIP, excludeName, excludeClientID, s)
	if err != nil {
		return newProxyRequestValidationError(fmt.Errorf("failed to check port conflicts: %w", err), protocol.TunnelMutationFieldRemotePort, "", http.StatusServiceUnavailable)
	}
	if len(conflicts) > 0 {
		return newProxyRequestValidationError(fmt.Errorf("port %d is already in use by another tunnel", req.RemotePort), protocol.TunnelMutationFieldRemotePort, "", http.StatusConflict)
	}

	return nil
}

func validateServerBindIP(bindIP string) error {
	bindIP = strings.TrimSpace(bindIP)
	if bindIP == "" {
		return nil
	}
	ip := net.ParseIP(bindIP)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("bind_ip must be a valid IPv4 address")
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

func findTCPUDPPortConflictNames(port int, bindIP, excludeName, excludeClientID string, server *Server) ([]string, error) {
	if port <= 0 || server == nil {
		return []string{}, nil
	}

	bindIP = normalizeServerBindIP(bindIP)
	conflicts := []string{}
	seen := map[string]struct{}{}
	matchAndAppend := func(clientID, name, tunnelType string, remotePort int, tunnelBindIP string) {
		if tunnelType != protocol.ProxyTypeTCP && tunnelType != protocol.ProxyTypeUDP {
			return
		}
		if excludeName != "" && excludeClientID != "" && name == excludeName && clientID == excludeClientID {
			return
		}
		if remotePort != port {
			return
		}
		if !serverBindIPsConflict(bindIP, tunnelBindIP) {
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
			matchAndAppend(clientID, name, tunnel.Config.Type, tunnel.Config.RemotePort, tunnel.Config.BindIP)
			return true
		})
		return true
	})

	if server.store != nil {
		allTunnels, err := server.store.GetAllTunnels()
		if err != nil {
			return nil, fmt.Errorf("load persisted tunnels for proxy conflict detection: %w", err)
		}
		for _, tunnel := range allTunnels {
			matchAndAppend(tunnel.ClientID, tunnel.Name, tunnel.Type, tunnel.RemotePort, tunnel.BindIP)
		}
	}

	return conflicts, nil
}

func serverListenAddress(bindIP string, port int) string {
	host := normalizeServerBindIP(bindIP)
	if host == "0.0.0.0" {
		host = ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func serverBindIPsConflict(a, b string) bool {
	a = normalizeServerBindIP(a)
	b = normalizeServerBindIP(b)
	return a == "0.0.0.0" || b == "0.0.0.0" || a == b
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
	return s.prepareProxyTunnelWithExclusions(client, req, desiredState, runtimeState, "", "", time.Time{})
}

func (s *Server) prepareProxyTunnelWithExclusions(client *ClientConn, req protocol.ProxyNewRequest, desiredState, runtimeState, excludeName, excludeClientID string, createdAt time.Time) (*ProxyTunnel, error) {
	if req.ID == "" {
		id, err := generateUUIDE()
		if err != nil {
			return nil, err
		}
		req.ID = id
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
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
			ID:                req.ID,
			Name:              req.Name,
			Revision:          1,
			Type:              req.Type,
			LocalIP:           req.LocalIP,
			LocalPort:         req.LocalPort,
			RemotePort:        req.RemotePort,
			BindIP:            normalizeServerBindIP(req.BindIP),
			Domain:            req.Domain,
			ClientID:          client.ID,
			BandwidthSettings: req.BandwidthSettings,
			CreatedAt:         createdAt.UTC(),
		},
		limits: newDirectionalBandwidthRuntime(req.BandwidthSettings, realBandwidthClock{}),
		done:   make(chan struct{}),
	}
	setProxyConfigStates(&tunnel.Config, desiredState, runtimeState, "")
	initializeTunnelRuntimeFromState(tunnel, client.ID, time.Now())

	client.proxies[req.Name] = tunnel
	client.proxyMu.Unlock()
	return tunnel, nil
}

func (s *Server) activatePreparedTunnel(client *ClientConn, tunnel *ProxyTunnel) error {
	client.proxyMu.RLock()
	config := tunnel.Config
	current, exists := client.proxies[config.Name]
	client.proxyMu.RUnlock()
	if !exists || current != tunnel {
		return fmt.Errorf("proxy tunnel %q not found", config.Name)
	}

	req := config.ToProxyNewRequest()
	name := config.Name
	if err := s.validateProxyRequestWithExclusions(client, req, name, client.ID); err != nil {
		return err
	}
	if err := s.ensureClientDataReady(client); err != nil {
		return err
	}

	if isSOCKS5ServerExpose(config) {
		return s.activatePreparedSOCKS5ServerExposeTunnel(client, tunnel, config)
	}

	if config.Type == protocol.ProxyTypeHTTP {
		policy, err := decodeIngressAccessPolicyFromProxyConfig(config)
		if err != nil {
			return fmt.Errorf("decode HTTP ingress policy: %w", err)
		}
		client.proxyMu.Lock()
		current, exists := client.proxies[name]
		if !exists || current != tunnel {
			client.proxyMu.Unlock()
			return fmt.Errorf("proxy tunnel %q not found", name)
		}
		if !s.proxyActivationClientCurrent(client) {
			client.proxyMu.Unlock()
			return fmt.Errorf("client [%s] session changed before proxy activation", client.ID)
		}
		current.done = make(chan struct{})
		current.once = sync.Once{}
		current.sourceCIDRs = policy.sourceCIDRs
		setProxyConfigStates(&current.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
		markTunnelServerRelayActive(current, client.ID, time.Now())
		client.proxyMu.Unlock()
		return nil
	}

	if config.Type == protocol.ProxyTypeUDP {
		runtime, err := s.bindUDPProxyRuntime(config)
		if err != nil {
			return err
		}
		activation, state, err := s.publishUDPProxyRuntime(client, tunnel, name, runtime)
		if err != nil {
			return err
		}
		log.Printf("🚇 UDP proxy tunnel created: %s [:%d → %s:%d] Client [%s]",
			activation.config.Name, activation.config.RemotePort, activation.config.LocalIP, activation.config.LocalPort, client.ID)

		go s.udpReadLoop(client, tunnel, state, activation)
		go s.udpReaper(state)
		return nil
	}

	addr := serverListenAddress(config.BindIP, config.RemotePort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	policy, err := decodeIngressAccessPolicyFromProxyConfig(config)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("decode TCP ingress policy: %w", err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port

	client.proxyMu.Lock()
	current, exists = client.proxies[name]
	if !exists || current != tunnel {
		client.proxyMu.Unlock()
		_ = ln.Close()
		return fmt.Errorf("proxy tunnel %q not found", name)
	}
	if !s.proxyActivationClientCurrent(client) {
		client.proxyMu.Unlock()
		_ = ln.Close()
		return fmt.Errorf("client [%s] session changed before proxy activation", client.ID)
	}
	current.Listener = ln
	current.done = make(chan struct{})
	current.once = sync.Once{}
	current.sourceCIDRs = policy.sourceCIDRs
	current.Config.RemotePort = actualPort
	setProxyConfigStates(&current.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
	markTunnelServerRelayActive(current, client.ID, time.Now())
	listener := current.Listener
	done := current.done
	activation := proxyActivationSnapshotLocked(current)
	client.proxyMu.Unlock()

	log.Printf("🚇 proxy tunnel created: %s [%s:%d → %s:%d] Client [%s]",
		activation.config.Name, normalizeServerBindIP(activation.config.BindIP), actualPort, activation.config.LocalIP, activation.config.LocalPort, client.ID)

	go s.proxyAcceptLoop(client, tunnel, listener, done, activation)
	return nil
}

func (s *Server) proxyActivationClientCurrent(client *ClientConn) bool {
	if client == nil {
		return false
	}
	if client.generation == 0 {
		return true
	}
	value, ok := s.clients.Load(client.ID)
	if !ok || value.(*ClientConn) != client {
		return false
	}
	return client.isLive()
}

func closeTunnelRuntimeResources(tunnel *ProxyTunnel) {
	if tunnel == nil {
		return
	}
	tunnel.once.Do(func() {
		if tunnel.done != nil {
			close(tunnel.done)
		}
		if tunnel.UDPState != nil {
			tunnel.UDPState.Close()
			tunnel.UDPState = nil
		}
		if tunnel.Listener != nil {
			_ = tunnel.Listener.Close()
			tunnel.Listener = nil
		}
	})
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

// discardTunnelRuntimeIfCurrent always closes expected, but removes the map
// entry only when it still points at that exact activation. This prevents a
// late restore from deleting a newer runtime that reused the tunnel name.
func (s *Server) discardTunnelRuntimeIfCurrent(client *ClientConn, name string, expected *ProxyTunnel, expectedID string, expectedRevision int64) bool {
	if client == nil || expected == nil {
		return false
	}
	client.proxyMu.Lock()
	current, exists := client.proxies[name]
	matches := exists && current == expected && current.Config.ID == expectedID && current.Config.Revision == expectedRevision
	detached := !exists || current != expected
	if matches {
		delete(client.proxies, name)
	}
	client.proxyMu.Unlock()

	if matches || detached {
		closeTunnelRuntimeResources(expected)
	}
	return matches
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
	markTunnelProvisionPending(tunnel, client.ID, tunnel.runtime.Revision, time.Now())
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
	proxyName string,
	tunnel *ProxyTunnel,
	listener net.Listener,
	message string,
) {
	client.proxyMu.RLock()
	current, exists := client.proxies[proxyName]
	if !exists ||
		current != tunnel ||
		current.Listener != listener ||
		!isTunnelExposed(current.Config) {
		client.proxyMu.RUnlock()
		return
	}
	operationConfig := current.Config
	client.proxyMu.RUnlock()

	releaseRuntimeOperation := s.tunnelRuntimeOps.lock(tunnelRuntimeOperationKey(operationConfig.ID, client.ID, proxyName))
	defer releaseRuntimeOperation()

	client.proxyMu.Lock()
	current, exists = client.proxies[proxyName]
	if !exists ||
		current != tunnel ||
		current.Listener != listener ||
		!isTunnelExposed(current.Config) {
		client.proxyMu.Unlock()
		return
	}
	closeTunnelRuntimeResources(current)
	setProxyConfigStates(&current.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	markTunnelRuntimeError(current, client.ID, message, time.Now())
	config := current.Config
	client.proxyMu.Unlock()

	updated, err := s.updateProxyConfigRuntimeIfCurrent(client.ID, config, protocol.ProxyRuntimeStateError, message)
	if err != nil {
		log.Printf("⚠️ TCP proxy [%s] failed to persist error state: %v", proxyName, err)
	}
	if s.runtimeErrorCleanupHook != nil {
		s.runtimeErrorCleanupHook(config)
	}
	if notifyErr := s.notifyRuntimeErrorUnprovision(client, tunnel, config); notifyErr != nil {
		log.Printf("⚠️ TCP proxy [%s] failed to notify client of close: %v", proxyName, notifyErr)
	}
	if err != nil || !updated {
		return
	}
	s.recordServerExposeIngressIssue(config.ID, config.Revision, config.Type, message)
	s.emitTunnelChangedIfStored(client.ID, config, "error")
}

// proxyAcceptLoop continuously accepts external connections and forwards them via yamux.
// It holds a snapshot of the listener/done for this activation to prevent stale loops from interfering with newer runtimes.
func (s *Server) proxyAcceptLoop(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, done <-chan struct{}, activation proxyActivationSnapshot) {
	defer func() { _ = listener.Close() }()

	for {
		extConn, err := listener.Accept()
		if err != nil {
			select {
			case <-done:
				return // normal shutdown
			default:
				log.Printf("⚠️ proxy [%s] Accept failed: %v", activation.config.Name, err)
				s.markTCPProxyRuntimeErrorIfCurrent(client, activation.config.Name, tunnel, listener, fmt.Sprintf("TCP proxy listener failed: %v", err))
				return
			}
		}

		go s.handleProxyConn(client, tunnel, listener, extConn, activation)
	}
}

// handleProxyConn handles a single external connection: opens a stream on the yamux session,
// writes the DataStreamHeader with tunnel/revision metadata, then relays data bidirectionally.
func (s *Server) handleProxyConn(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, extConn net.Conn, activation proxyActivationSnapshot) {
	defer func() { _ = extConn.Close() }()

	if !sourceAddressAllowed(extConn.RemoteAddr(), activation.sourceCIDRs) {
		log.Printf("⚠️ proxy [%s] rejected source: %s", activation.config.Name, rejectSourceAddressMessage(extConn.RemoteAddr()))
		return
	}

	stream, err := s.openStreamToClientForActivation(client, tunnel, activation)
	if err != nil {
		log.Printf("⚠️ proxy [%s] open stream failed: %v", activation.config.Name, err)
		s.markTCPProxyRuntimeErrorIfCurrent(client, activation.config.Name, tunnel, listener, fmt.Sprintf("TCP proxy forwarding channel failed: %v", err))
		return
	}

	var recordTraffic tunnelTrafficObserver
	if s.trafficStore != nil {
		recordTraffic = func(ingressBytes, egressBytes uint64) {
			s.recordTunnelTraffic(client.ID, activation.config, ingressBytes, egressBytes)
		}
	}
	_, _ = relayTunnelPayload(stream, extConn, client.BandwidthRuntime(), activation.limits, recordTraffic)
}

func decodeIngressAccessPolicyFromProxyConfig(config protocol.ProxyConfig) (ingressAccessPolicy, error) {
	if config.Ingress == nil {
		return parseIngressAccessPolicy(nil, true)
	}
	return decodeIngressAccessPolicy(config.Ingress.Config, true)
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
	config := tunnel.Config
	client.proxyMu.RUnlock()

	if config.RemotePort != 0 && s.auth.adminStore != nil {
		initialized, err := s.auth.adminStore.IsInitializedE()
		if err != nil {
			return fmt.Errorf("failed to read initialization state: %w", err)
		}
		if initialized && !s.auth.adminStore.IsPortAllowed(config.RemotePort) {
			return fmt.Errorf("port %d is no longer in the allowed range, cannot resume", config.RemotePort)
		}
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		return err
	}

	client.proxyMu.RLock()
	if current := client.proxies[name]; current == tunnel {
		config = current.Config
	}
	client.proxyMu.RUnlock()
	log.Printf("▶️ proxy tunnel runtime reopened: %s [:%d]", name, config.RemotePort)
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
			markTunnelRuntimeOffline(tunnel, client.ID, time.Now())
		}
	}
}
