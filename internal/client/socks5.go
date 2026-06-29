package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"syscall"
	"time"

	"netsgo/internal/ingresspolicy"
	"netsgo/internal/socks5wire"
	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

const socks5HandshakeTimeout = 10 * time.Second

const socks5DialResultGraceSeconds = 5

type clientSOCKS5TargetRuntime struct {
	tunnelID    string
	revision    int64
	spec        protocol.TunnelSpec
	config      protocol.SOCKS5ConnectHandlerConfig
	targetCIDRs []*net.IPNet
	targetHosts map[string]struct{}
	targetPorts map[int]struct{}
}

type socks5ListenRuntimeConfig struct {
	config             protocol.SOCKS5ListenConfig
	sourceCIDRs        []*net.IPNet
	dialTimeoutSeconds int
}

func newClientSOCKS5TargetRuntime(req protocol.TunnelProvisionRequest) (clientSOCKS5TargetRuntime, error) {
	var cfg protocol.SOCKS5ConnectHandlerConfig
	if err := json.Unmarshal(req.Spec.Target.Config, &cfg); err != nil {
		return clientSOCKS5TargetRuntime{}, fmt.Errorf("decode SOCKS5 target config: %w", err)
	}
	if len(cfg.AllowedTargetCIDRs) == 0 {
		return clientSOCKS5TargetRuntime{}, fmt.Errorf("allowed_target_cidrs is required")
	}
	cidrs, err := parseCIDRs(cfg.AllowedTargetCIDRs)
	if err != nil {
		return clientSOCKS5TargetRuntime{}, err
	}
	if cfg.DialTimeoutSeconds <= 0 {
		cfg.DialTimeoutSeconds = 10
	}
	hostSet := make(map[string]struct{})
	for _, host := range cfg.AllowedTargetHosts {
		normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if normalized != "" {
			hostSet[normalized] = struct{}{}
		}
	}
	portSet := make(map[int]struct{})
	for _, port := range cfg.AllowedTargetPorts {
		if port < 1 || port > 65535 {
			return clientSOCKS5TargetRuntime{}, fmt.Errorf("allowed_target_ports values must be in range 1-65535")
		}
		portSet[port] = struct{}{}
	}
	return clientSOCKS5TargetRuntime{
		tunnelID:    req.TunnelID,
		revision:    req.Revision,
		spec:        req.Spec,
		config:      cfg,
		targetCIDRs: cidrs,
		targetHosts: hostSet,
		targetPorts: portSet,
	}, nil
}

func parseCIDRs(values []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, ipNet, err := net.ParseCIDR(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", value, err)
		}
		out = append(out, ipNet)
	}
	return out, nil
}

func dataStreamHeaderMatchesSOCKS5Target(header protocol.DataStreamHeader, target clientSOCKS5TargetRuntime) bool {
	if header.TunnelID != target.tunnelID || header.Revision != target.revision {
		return false
	}
	if header.TargetRole != protocol.DataStreamRoleTarget {
		return false
	}
	if header.SourceRole != protocol.DataStreamRoleServer && header.SourceRole != protocol.DataStreamRoleIngress {
		return false
	}
	if header.Direction != protocol.DataStreamDirectionIngressToTarget || header.Transport != protocol.ActualTransportServerRelay {
		return false
	}
	// Target runtimes only accept server-relay streams; direct-only tunnels must
	// not be matched through the server data channel.
	if target.spec.TransportPolicy == protocol.TransportPolicyDirectOnly {
		return false
	}
	if header.TargetHost == "" || header.TargetPort < 1 || header.TargetPort > 65535 {
		return false
	}
	return true
}

func (c *Client) handleSOCKS5TargetStream(stream net.Conn, header protocol.DataStreamHeader, target clientSOCKS5TargetRuntime) {
	localConn, result := dialSOCKS5Target(header, target)
	if err := socks5wire.WriteDialResult(stream, result); err != nil {
		log.Printf("⚠️ write SOCKS5 dial result failed [%s]: %v", header.TunnelID, err)
		if localConn != nil {
			_ = localConn.Close()
		}
		return
	}
	if result.Status != protocol.SOCKS5DialStatusSuccess {
		return
	}
	defer func() { _ = localConn.Close() }()
	mux.Relay(stream, localConn)
}

func dialSOCKS5Target(header protocol.DataStreamHeader, target clientSOCKS5TargetRuntime) (net.Conn, protocol.SOCKS5DialResult) {
	host := strings.TrimSpace(header.TargetHost)
	port := header.TargetPort
	if !targetAllowsHostPort(host, port, target) {
		return nil, protocol.SOCKS5DialResult{Status: protocol.SOCKS5DialStatusTargetDenied, Message: "target denied by policy"}
	}
	timeout := time.Duration(target.config.DialTimeoutSeconds) * time.Second
	ips, err := resolveTargetIPs(host, timeout)
	if err != nil {
		return nil, protocol.SOCKS5DialResult{Status: protocol.SOCKS5DialStatusHostUnreachable, Message: err.Error()}
	}
	if !targetAllowsResolvedIPs(ips, target.targetCIDRs) {
		return nil, protocol.SOCKS5DialResult{Status: protocol.SOCKS5DialStatusTargetDenied, Message: "resolved target IP denied by policy"}
	}
	conn, err := dialResolvedSOCKS5TargetIPs(ips, port, timeout)
	if err != nil {
		return nil, socks5DialResultFromError(err)
	}
	result := protocol.SOCKS5DialResult{Status: protocol.SOCKS5DialStatusSuccess}
	// Keep RFC 1928 BND.ADDR/BND.PORT semantics: report the target client's
	// actual local socket endpoint. This may expose an internal address by
	// design; callers that need privacy should enforce that policy above SOCKS5.
	if addr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		result.BoundAddr = addr.IP.String()
		result.BoundPort = addr.Port
	}
	return conn, result
}

func dialResolvedSOCKS5TargetIPs(ips []net.IP, port int, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for _, ip := range ips {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)), remaining)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("dial timeout")
}

func targetAllowsHostPort(host string, port int, target clientSOCKS5TargetRuntime) bool {
	if len(target.targetPorts) > 0 {
		if _, ok := target.targetPorts[port]; !ok {
			return false
		}
	}
	if len(target.targetHosts) == 0 {
		return true
	}
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	_, ok := target.targetHosts[normalized]
	return ok
}

func resolveTargetIPs(host string, timeout time.Duration) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	resolver := net.Resolver{}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return resolver.LookupIP(ctx, "ip", host)
}

func targetAllowsResolvedIPs(ips []net.IP, cidrs []*net.IPNet) bool {
	if len(ips) == 0 || len(cidrs) == 0 {
		return false
	}
	// Be conservative for multi-answer DNS: every returned address must be
	// allowed so a mixed DNS response cannot bypass the target CIDR policy.
	for _, ip := range ips {
		allowed := false
		for _, cidr := range cidrs {
			if cidr.Contains(ip) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

func socks5DialResultFromError(err error) protocol.SOCKS5DialResult {
	if err == nil {
		return protocol.SOCKS5DialResult{Status: protocol.SOCKS5DialStatusSuccess}
	}
	status := protocol.SOCKS5DialStatusGeneralFailure
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		status = protocol.SOCKS5DialStatusDialTimeout
	} else if errors.Is(err, syscall.ECONNREFUSED) {
		status = protocol.SOCKS5DialStatusConnectionRefused
	}
	return protocol.SOCKS5DialResult{Status: status, Message: err.Error()}
}

func (c *Client) acceptIngressSOCKS5(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime) {
	listenCfg, err := decodeSOCKS5ListenRuntimeConfigFromSpec(req.Spec.Ingress.Config, req.Spec.Target.Config)
	if err != nil {
		c.failIngressTunnelRuntime(rt, req, runtime, fmt.Sprintf("decode SOCKS5 ingress config failed: %v", err))
		return
	}
	for {
		conn, err := runtime.listener.Accept()
		if err != nil {
			select {
			case <-runtime.done:
				return
			default:
				message := fmt.Sprintf("SOCKS5 ingress accept failed [%s]: %v", req.TunnelID, err)
				log.Printf("⚠️ %s", message)
				c.failIngressTunnelRuntime(rt, req, runtime, message)
				return
			}
		}
		if !runtime.trackTCPConn(conn) {
			_ = conn.Close()
			return
		}
		go c.handleIngressSOCKS5Conn(rt, req, runtime, conn, listenCfg)
	}
}

func decodeSOCKS5ListenRuntimeConfigFromSpec(raw []byte, targetRaw []byte) (socks5ListenRuntimeConfig, error) {
	var cfg protocol.SOCKS5ListenConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return socks5ListenRuntimeConfig{}, err
	}
	cidrs, err := ingresspolicy.ParseCIDRs(cfg.AllowedSourceCIDRs)
	if err != nil {
		return socks5ListenRuntimeConfig{}, err
	}
	dialTimeoutSeconds := 10
	if len(targetRaw) > 0 {
		var targetCfg protocol.SOCKS5ConnectHandlerConfig
		if err := json.Unmarshal(targetRaw, &targetCfg); err != nil {
			return socks5ListenRuntimeConfig{}, err
		}
		if targetCfg.DialTimeoutSeconds > 0 {
			dialTimeoutSeconds = targetCfg.DialTimeoutSeconds
		}
	}
	return socks5ListenRuntimeConfig{config: cfg, sourceCIDRs: cidrs, dialTimeoutSeconds: dialTimeoutSeconds}, nil
}

func (c *Client) handleIngressSOCKS5Conn(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime, conn net.Conn, listenCfg socks5ListenRuntimeConfig) {
	defer func() {
		runtime.removeTCPConn(conn)
		_ = conn.Close()
	}()
	if !sourceAddrAllowed(conn.RemoteAddr(), listenCfg.sourceCIDRs) {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(socks5HandshakeTimeout))
	request, ok := socks5wire.ServeHandshake(conn, listenCfg.config)
	if !ok {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	stream, err := openIngressSOCKS5Stream(rt, req, c.CurrentClientID(), request)
	if err != nil {
		_ = socks5wire.WriteReply(conn, socks5wire.RepGeneralFailure, "", 0)
		c.reportTunnelRuntimeError(rt, req, fmt.Sprintf("open SOCKS5 ingress stream failed [%s]: %v", req.TunnelID, err))
		return
	}
	defer func() { _ = stream.Close() }()
	_ = stream.SetReadDeadline(time.Now().Add(socks5DialResultWaitTimeout(listenCfg.dialTimeoutSeconds)))
	result, err := socks5wire.ReadDialResult(stream)
	_ = stream.SetReadDeadline(time.Time{})
	if err != nil {
		_ = socks5wire.WriteReply(conn, socks5wire.RepGeneralFailure, "", 0)
		return
	}
	if result.Status != protocol.SOCKS5DialStatusSuccess {
		_ = socks5wire.WriteReply(conn, socks5wire.ReplyForDialStatus(result.Status), "", 0)
		return
	}
	if err := socks5wire.WriteReply(conn, socks5wire.RepSuccess, result.BoundAddr, result.BoundPort); err != nil {
		return
	}
	mux.Relay(stream, conn)
}

func socks5DialResultWaitTimeout(dialTimeoutSeconds int) time.Duration {
	if dialTimeoutSeconds <= 0 {
		dialTimeoutSeconds = 10
	}
	return time.Duration(dialTimeoutSeconds+socks5DialResultGraceSeconds) * time.Second
}

func openIngressSOCKS5Stream(rt *sessionRuntime, req protocol.TunnelProvisionRequest, openClientID string, request socks5wire.ConnectRequest) (net.Conn, error) {
	rt.dataMu.RLock()
	session := rt.dataSession
	rt.dataMu.RUnlock()
	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("data session unavailable")
	}
	stream, err := session.Open()
	if err != nil {
		return nil, err
	}
	header, err := ingressDataStreamHeader(req, openClientID)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	header.TargetHost = request.Host
	header.TargetPort = request.Port
	header.TargetAddrType = request.AddrType
	header.OriginalHost = request.OriginalHost
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}
