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

func (s *Server) activatePreparedSOCKS5ServerExposeTunnel(client *ClientConn, tunnel *ProxyTunnel) error {
	if tunnel.Config.Ingress == nil {
		return fmt.Errorf("SOCKS5 tunnel %q missing ingress endpoint config", tunnel.Config.Name)
	}
	var targetConfig []byte
	if tunnel.Config.Target != nil {
		targetConfig = tunnel.Config.Target.Config
	}
	listenCfg, err := decodeSOCKS5ServerListenRuntimeConfigFromSpec(tunnel.Config.Ingress.Config, targetConfig)
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
	markTunnelServerRelayActive(tunnel, client.ID, time.Now())
	listener := tunnel.Listener
	done := tunnel.done
	proxyName := tunnel.Config.Name
	client.proxyMu.Unlock()

	log.Printf("🚇 SOCKS5 proxy tunnel created: %s [%s] Client [%s]", proxyName, listener.Addr().String(), client.ID)
	go s.socks5ProxyAcceptLoop(client, tunnel, listener, done, listenCfg)
	return nil
}

func (s *Server) socks5ProxyAcceptLoop(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, done <-chan struct{}, listenCfg socks5ServerListenRuntimeConfig) {
	defer func() { _ = listener.Close() }()

	for {
		extConn, err := listener.Accept()
		if err != nil {
			select {
			case <-done:
				return
			default:
				log.Printf("⚠️ SOCKS5 proxy [%s] Accept failed: %v", tunnel.Config.Name, err)
				s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel, listener, fmt.Sprintf("SOCKS5 proxy listener failed: %v", err))
				return
			}
		}

		go s.handleSOCKS5ProxyConn(client, tunnel, listener, extConn, listenCfg)
	}
}

func (s *Server) handleSOCKS5ProxyConn(client *ClientConn, tunnel *ProxyTunnel, listener net.Listener, extConn net.Conn, listenCfg socks5ServerListenRuntimeConfig) {
	defer func() { _ = extConn.Close() }()

	if !sourceAddressAllowed(extConn.RemoteAddr(), listenCfg.sourceCIDRs) {
		return
	}
	_ = extConn.SetDeadline(time.Now().Add(socks5HandshakeTimeout))
	request, ok := socks5wire.ServeHandshake(extConn, listenCfg.config)
	if !ok {
		return
	}
	_ = extConn.SetDeadline(time.Time{})
	stream, err := s.openSOCKS5StreamToClient(client, tunnel, request)
	if err != nil {
		log.Printf("⚠️ SOCKS5 proxy [%s] open stream failed: %v", tunnel.Config.Name, err)
		_ = socks5wire.WriteReply(extConn, socks5wire.RepGeneralFailure, "", 0)
		s.markTCPProxyRuntimeErrorIfCurrent(client, tunnel, listener, fmt.Sprintf("SOCKS5 proxy forwarding channel failed: %v", err))
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
			s.recordTunnelTraffic(client.ID, tunnel.Config, ingressBytes, egressBytes)
		}
	}
	_, _ = relayTunnelPayload(stream, extConn, client.BandwidthRuntime(), tunnel.limits, recordTraffic)
}

func socks5DialResultWaitTimeout(dialTimeoutSeconds int) time.Duration {
	if dialTimeoutSeconds <= 0 {
		dialTimeoutSeconds = defaultSOCKS5DialTimeoutSeconds
	}
	return time.Duration(dialTimeoutSeconds+socks5DialResultGraceSeconds) * time.Second
}

func (s *Server) openSOCKS5StreamToClient(client *ClientConn, tunnel *ProxyTunnel, request socks5wire.ConnectRequest) (net.Conn, error) {
	return s.openStreamToClientWithHeader(client, tunnel.Config.Name, func(header *protocol.DataStreamHeader) {
		header.TargetHost = request.Host
		header.TargetPort = request.Port
		header.TargetAddrType = request.AddrType
		header.OriginalHost = request.OriginalHost
	})
}
