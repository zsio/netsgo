package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"netsgo/internal/ingresspolicy"
	"netsgo/internal/socks5wire"
	"netsgo/pkg/protocol"
)

type socks5ServerListenRuntimeConfig struct {
	config             protocol.SOCKS5ListenConfig
	sourceCIDRs        []*net.IPNet
	dialTimeoutSeconds int
}

const (
	socks5HandshakeTimeout       = 10 * time.Second
	socks5DialResultGraceSeconds = 5
)

func isSOCKS5ServerExpose(config protocol.ProxyConfig) bool {
	return config.Topology == protocol.TunnelTopologyServerExpose &&
		config.Ingress != nil &&
		config.Ingress.Type == protocol.IngressTypeSOCKS5Listen &&
		config.Target != nil &&
		config.Target.Type == protocol.TargetTypeSOCKS5ConnectHandler
}

func decodeSOCKS5ServerListenRuntimeConfigFromSpec(raw []byte, targetRaw []byte) (socks5ServerListenRuntimeConfig, error) {
	var cfg protocol.SOCKS5ListenConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return socks5ServerListenRuntimeConfig{}, err
	}
	cidrs, err := ingresspolicy.ParseCIDRs(cfg.AllowedSourceCIDRs)
	if err != nil {
		return socks5ServerListenRuntimeConfig{}, err
	}
	dialTimeoutSeconds := 10
	if len(targetRaw) > 0 {
		var targetCfg protocol.SOCKS5ConnectHandlerConfig
		if err := json.Unmarshal(targetRaw, &targetCfg); err != nil {
			return socks5ServerListenRuntimeConfig{}, err
		}
		if targetCfg.DialTimeoutSeconds > 0 {
			dialTimeoutSeconds = targetCfg.DialTimeoutSeconds
		}
	}
	return socks5ServerListenRuntimeConfig{config: cfg, sourceCIDRs: cidrs, dialTimeoutSeconds: dialTimeoutSeconds}, nil
}

func (s *Server) activatePreparedSOCKS5ServerExposeTunnel(client *ClientConn, tunnel *ProxyTunnel, config protocol.ProxyConfig) error {
	if config.Ingress == nil {
		return fmt.Errorf("SOCKS5 tunnel %q missing ingress endpoint config", config.Name)
	}
	var targetConfig []byte
	if config.Target != nil {
		targetConfig = config.Target.Config
	}
	listenCfg, err := decodeSOCKS5ServerListenRuntimeConfigFromSpec(config.Ingress.Config, targetConfig)
	if err != nil {
		return fmt.Errorf("decode SOCKS5 ingress config: %w", err)
	}
	addr := net.JoinHostPort(listenCfg.config.BindIP, fmt.Sprintf("%d", listenCfg.config.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on SOCKS5 endpoint %s: %w", addr, err)
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port

	client.proxyMu.Lock()
	current, exists := client.proxies[config.Name]
	if !exists || current != tunnel {
		client.proxyMu.Unlock()
		_ = ln.Close()
		return fmt.Errorf("proxy tunnel %q not found", config.Name)
	}
	if !s.proxyActivationClientCurrent(client) {
		client.proxyMu.Unlock()
		_ = ln.Close()
		return fmt.Errorf("client [%s] session changed before proxy activation", client.ID)
	}
	current.Listener = ln
	current.done = make(chan struct{})
	current.once = sync.Once{}
	current.sourceCIDRs = listenCfg.sourceCIDRs
	current.Config.RemotePort = actualPort
	setProxyConfigStates(&current.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
	markTunnelServerRelayActive(current, client.ID, time.Now())
	listener := current.Listener
	done := current.done
	activation := proxyActivationSnapshotLocked(current)
	client.proxyMu.Unlock()

	log.Printf("🚇 SOCKS5 proxy tunnel created: %s [%s] Client [%s]", activation.config.Name, listener.Addr().String(), client.ID)
	go s.socks5ProxyAcceptLoop(client, tunnel, listener, done, listenCfg, activation)
	return nil
}

func (s *Server) socks5ProxyAcceptLoop(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, done <-chan struct{}, listenCfg socks5ServerListenRuntimeConfig, activation proxyActivationSnapshot) {
	defer func() { _ = listener.Close() }()

	for {
		extConn, err := listener.Accept()
		if err != nil {
			select {
			case <-done:
				return
			default:
				log.Printf("⚠️ SOCKS5 proxy [%s] Accept failed: %v", activation.config.Name, err)
				s.markTCPProxyRuntimeErrorIfCurrent(client, activation.config.Name, tunnel, listener, fmt.Sprintf("SOCKS5 proxy listener failed: %v", err))
				return
			}
		}

		go s.handleSOCKS5ProxyConn(client, tunnel, listener, extConn, listenCfg, activation)
	}
}

func (s *Server) handleSOCKS5ProxyConn(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, extConn net.Conn, listenCfg socks5ServerListenRuntimeConfig, activation proxyActivationSnapshot) {
	defer func() { _ = extConn.Close() }()

	if !sourceAddressAllowed(extConn.RemoteAddr(), activation.sourceCIDRs) {
		return
	}
	_ = extConn.SetDeadline(time.Now().Add(socks5HandshakeTimeout))
	request, ok := socks5wire.ServeHandshake(extConn, listenCfg.config)
	if !ok {
		return
	}
	_ = extConn.SetDeadline(time.Time{})
	stream, err := s.openSOCKS5StreamToClient(client, tunnel, activation, request)
	if err != nil {
		log.Printf("⚠️ SOCKS5 proxy [%s] open stream failed: %v", activation.config.Name, err)
		_ = socks5wire.WriteReply(extConn, socks5wire.RepGeneralFailure, "", 0)
		s.markTCPProxyRuntimeErrorIfCurrent(client, activation.config.Name, tunnel, listener, fmt.Sprintf("SOCKS5 proxy forwarding channel failed: %v", err))
		return
	}
	defer func() { _ = stream.Close() }()

	_ = stream.SetReadDeadline(time.Now().Add(socks5DialResultWaitTimeout(listenCfg.dialTimeoutSeconds)))
	result, err := socks5wire.ReadDialResult(stream)
	_ = stream.SetReadDeadline(time.Time{})
	if err != nil {
		_ = socks5wire.WriteReply(extConn, socks5wire.RepGeneralFailure, "", 0)
		return
	}
	if result.Status != protocol.SOCKS5DialStatusSuccess {
		_ = socks5wire.WriteReply(extConn, socks5wire.ReplyForDialStatus(result.Status), "", 0)
		return
	}
	if err := socks5wire.WriteReply(extConn, socks5wire.RepSuccess, result.BoundAddr, result.BoundPort); err != nil {
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

func socks5DialResultWaitTimeout(dialTimeoutSeconds int) time.Duration {
	if dialTimeoutSeconds <= 0 {
		dialTimeoutSeconds = defaultSOCKS5DialTimeoutSeconds
	}
	return time.Duration(dialTimeoutSeconds+socks5DialResultGraceSeconds) * time.Second
}

func (s *Server) openSOCKS5StreamToClient(client *ClientConn, tunnel *ProxyTunnel, activation proxyActivationSnapshot, request socks5wire.ConnectRequest) (net.Conn, error) {
	return s.openStreamToClientWithHeaderForActivation(client, tunnel, activation, func(header *protocol.DataStreamHeader) {
		header.TargetHost = request.Host
		header.TargetPort = request.Port
		header.TargetAddrType = request.AddrType
		header.OriginalHost = request.OriginalHost
	})
}
