package client

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

type clientTunnelRuntime struct {
	tunnelID            string
	revision            int64
	role                string
	listener            net.Listener
	packetConn          net.PacketConn
	wg                  sync.WaitGroup
	runMu               sync.Mutex
	closing             bool
	tcpConns            sync.Map
	udpAssociations     sync.Map
	udpAssociationMu    sync.Mutex
	udpAssociationCount atomic.Int64
	done                chan struct{}
	once                sync.Once
}

type clientUDPAssociation struct {
	key        string
	srcAddr    net.Addr
	stream     net.Conn
	lastActive atomic.Int64
	done       chan struct{}
	closeOnce  sync.Once
	writeMu    sync.Mutex
}

func newClientUDPAssociation(key string, srcAddr net.Addr, stream net.Conn) *clientUDPAssociation {
	assoc := &clientUDPAssociation{
		key:     key,
		srcAddr: srcAddr,
		stream:  stream,
		done:    make(chan struct{}),
	}
	assoc.touch()
	return assoc
}

func (a *clientUDPAssociation) touch() {
	a.lastActive.Store(time.Now().UnixNano())
}

func (a *clientUDPAssociation) idleDuration() time.Duration {
	last := a.lastActive.Load()
	if last == 0 {
		return 0
	}
	return time.Since(time.Unix(0, last))
}

func (a *clientUDPAssociation) close() {
	a.closeOnce.Do(func() {
		close(a.done)
		if a.stream != nil {
			_ = a.stream.SetDeadline(time.Now())
			_ = a.stream.Close()
		}
	})
}

const (
	clientUDPAssociationTimeout   = 2 * time.Minute
	clientUDPAssociationReapEvery = 10 * time.Second
	clientMaxUDPAssociations      = 4096
)

func (rt *clientTunnelRuntime) close() {
	if rt == nil {
		return
	}
	rt.shutdown()
	rt.wg.Wait()
}

func (rt *clientTunnelRuntime) shutdown() {
	if rt == nil {
		return
	}
	rt.runMu.Lock()
	rt.closing = true
	rt.runMu.Unlock()
	rt.once.Do(func() {
		close(rt.done)
		if rt.listener != nil {
			_ = rt.listener.Close()
		}
		if rt.packetConn != nil {
			_ = rt.packetConn.Close()
		}
		rt.tcpConns.Range(func(key, value any) bool {
			if conn, ok := value.(net.Conn); ok {
				_ = conn.Close()
			}
			rt.tcpConns.Delete(key)
			return true
		})
		rt.udpAssociations.Range(func(key, value any) bool {
			if assoc, ok := value.(*clientUDPAssociation); ok {
				assoc.close()
			}
			rt.udpAssociations.Delete(key)
			return true
		})
		rt.udpAssociationCount.Store(0)
	})
}

func (rt *clientTunnelRuntime) run(fn func()) bool {
	if rt == nil || fn == nil {
		return false
	}
	rt.runMu.Lock()
	if rt.closing {
		rt.runMu.Unlock()
		return false
	}
	rt.wg.Add(1)
	rt.runMu.Unlock()
	go func() {
		defer rt.wg.Done()
		fn()
	}()
	return true
}

func (rt *clientTunnelRuntime) trackTCPConn(conn net.Conn) bool {
	if rt == nil || conn == nil {
		return false
	}
	rt.runMu.Lock()
	defer rt.runMu.Unlock()
	if rt.closing {
		return false
	}
	rt.tcpConns.Store(conn, conn)
	return true
}

func (rt *clientTunnelRuntime) removeTCPConn(conn net.Conn) {
	if rt == nil || conn == nil {
		return
	}
	rt.tcpConns.Delete(conn)
}

func (rt *clientTunnelRuntime) packetConnForWrite() (net.PacketConn, bool) {
	if rt == nil {
		return nil, false
	}
	rt.runMu.Lock()
	defer rt.runMu.Unlock()
	if rt.closing || rt.packetConn == nil {
		return nil, false
	}
	return rt.packetConn, true
}

func (rt *clientTunnelRuntime) removeUDPAssociation(key string) {
	if rt == nil || key == "" {
		return
	}
	if value, loaded := rt.udpAssociations.LoadAndDelete(key); loaded {
		rt.udpAssociationCount.Add(-1)
		if assoc, ok := value.(*clientUDPAssociation); ok {
			assoc.close()
		}
	}
}

func (rt *clientTunnelRuntime) removeOldestUDPAssociation() bool {
	if rt == nil {
		return false
	}
	var oldestKey string
	var oldestAt int64
	rt.udpAssociations.Range(func(key, value any) bool {
		assoc, ok := value.(*clientUDPAssociation)
		if !ok {
			if keyString, ok := key.(string); ok {
				rt.udpAssociations.Delete(keyString)
			}
			return true
		}
		lastActive := assoc.lastActive.Load()
		if oldestKey == "" || lastActive < oldestAt {
			oldestKey = assoc.key
			oldestAt = lastActive
		}
		return true
	})
	if oldestKey == "" {
		return false
	}
	rt.removeUDPAssociation(oldestKey)
	return true
}

func tunnelRuntimeKey(tunnelID, role string) string {
	return tunnelID + ":" + role
}

func (c *Client) handleTunnelPreflight(req protocol.TunnelPreflightRequest) protocol.TunnelPreflightResponse {
	resp := protocol.TunnelPreflightResponse{
		RequestID: req.RequestID,
		TunnelID:  req.TunnelID,
		Revision:  req.Revision,
		Role:      req.Role,
		Accepted:  true,
		Message:   "preflight accepted",
	}
	if req.RequestID == "" {
		resp.Accepted = false
		resp.Code = protocol.TunnelMutationErrorCodeIngressPreflightRejected
		resp.Message = "missing request_id"
		return resp
	}
	if req.Role != protocol.DataStreamRoleIngress {
		resp.Accepted = false
		resp.Code = protocol.TunnelMutationErrorCodeIngressPreflightRejected
		resp.Message = "unsupported preflight role"
		return resp
	}
	var cfg struct {
		BindIP string `json:"bind_ip"`
		Port   int    `json:"port"`
	}
	if err := decodeEndpointConfig(req.Ingress.Config, &cfg); err != nil {
		resp.Accepted = false
		resp.Code = protocol.TunnelMutationErrorCodeIngressPreflightRejected
		resp.Message = err.Error()
		return resp
	}
	if cfg.BindIP == "" || cfg.Port <= 0 {
		resp.Accepted = false
		resp.Code = protocol.TunnelMutationErrorCodeInvalidBindIP
		resp.Message = "ingress bind_ip and port are required"
		return resp
	}
	addr := net.JoinHostPort(cfg.BindIP, fmt.Sprintf("%d", cfg.Port))
	switch req.Ingress.Type {
	case protocol.IngressTypeTCPListen:
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			resp.Accepted = false
			resp.Code = protocol.TunnelMutationErrorCodeIngressPortInUse
			resp.Message = err.Error()
			return resp
		}
		_ = ln.Close()
	case protocol.IngressTypeUDPListen:
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			resp.Accepted = false
			resp.Code = protocol.TunnelMutationErrorCodeIngressPortInUse
			resp.Message = err.Error()
			return resp
		}
		_ = conn.Close()
	default:
		resp.Accepted = false
		resp.Code = protocol.TunnelMutationErrorCodeUnsupportedEndpointType
		resp.Message = fmt.Sprintf("unsupported ingress type %s", req.Ingress.Type)
	}
	return resp
}

func (c *Client) handleTunnelProvision(rt *sessionRuntime, req protocol.TunnelProvisionRequest) protocol.TunnelProvisionAck {
	ack := protocol.TunnelProvisionAck{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     req.Role,
		Accepted: true,
		Message:  "provision accepted",
	}
	if req.TunnelID == "" || req.Revision <= 0 {
		ack.Accepted = false
		ack.Message = "missing tunnel identity"
		return ack
	}

	switch req.Role {
	case protocol.DataStreamRoleTarget:
		proxyReq, err := proxyRequestFromTunnelSpec(req.Spec)
		if err != nil {
			ack.Accepted = false
			ack.Message = err.Error()
			return ack
		}
		c.proxies.Store(req.TunnelID, proxyReq)
		return ack
	case protocol.DataStreamRoleIngress:
		if err := c.startIngressTunnelRuntime(rt, req); err != nil {
			ack.Accepted = false
			ack.Message = err.Error()
		}
		return ack
	default:
		ack.Accepted = false
		ack.Message = "unsupported tunnel role"
		return ack
	}
}

func (c *Client) handleTunnelUnprovision(req protocol.TunnelUnprovisionRequest) {
	if req.Role == protocol.DataStreamRoleIngress || req.Role == "" {
		key := tunnelRuntimeKey(req.TunnelID, protocol.DataStreamRoleIngress)
		if value, ok := c.tunnels.Load(key); ok {
			runtime, isRuntime := value.(*clientTunnelRuntime)
			if isRuntime && tunnelUnprovisionCoversRevision(req.Revision, runtime.revision) && c.tunnels.CompareAndDelete(key, value) {
				runtime.close()
			}
		}
	}

	if req.Role == protocol.DataStreamRoleTarget || req.Role == "" {
		c.deleteProxyByTunnelUnprovision(req)
	}
}

func (c *Client) deleteProxyByTunnelUnprovision(req protocol.TunnelUnprovisionRequest) {
	if req.TunnelID == "" {
		return
	}
	if value, ok := c.proxies.Load(req.TunnelID); ok {
		if proxyUnprovisionMatchesRevision(value, req.Revision) {
			c.proxies.CompareAndDelete(req.TunnelID, value)
		}
		return
	}
	c.proxies.Range(func(key, value any) bool {
		proxy, ok := value.(protocol.ProxyNewRequest)
		if !ok || proxy.ID != req.TunnelID || !proxyUnprovisionMatchesRevision(value, req.Revision) {
			return true
		}
		c.proxies.CompareAndDelete(key, value)
		return false
	})
}

func proxyUnprovisionMatchesRevision(value any, revision int64) bool {
	proxy, ok := value.(protocol.ProxyNewRequest)
	if !ok {
		return false
	}
	return proxyUnprovisionCoversRevision(revision, proxy.ProvisionRevision)
}

func tunnelUnprovisionCoversRevision(requestRevision, runtimeRevision int64) bool {
	return requestRevision <= 0 || runtimeRevision <= 0 || requestRevision >= runtimeRevision
}

func proxyUnprovisionCoversRevision(requestRevision int64, provisionRevision uint64) bool {
	return requestRevision <= 0 || provisionRevision == 0 || uint64(requestRevision) >= provisionRevision
}

func (c *Client) startIngressTunnelRuntime(rt *sessionRuntime, req protocol.TunnelProvisionRequest) error {
	var cfg struct {
		BindIP string `json:"bind_ip"`
		Port   int    `json:"port"`
	}
	if err := decodeEndpointConfig(req.Spec.Ingress.Config, &cfg); err != nil {
		return fmt.Errorf("decode ingress config: %w", err)
	}
	if cfg.BindIP == "" || cfg.Port <= 0 {
		return fmt.Errorf("ingress bind_ip and port are required")
	}
	addr := net.JoinHostPort(cfg.BindIP, fmt.Sprintf("%d", cfg.Port))

	runtime := &clientTunnelRuntime{
		tunnelID: req.TunnelID,
		revision: req.Revision,
		role:     req.Role,
		done:     make(chan struct{}),
	}

	switch req.Spec.Ingress.Type {
	case protocol.IngressTypeTCPListen:
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", addr, err)
		}
		runtime.listener = ln
	case protocol.IngressTypeUDPListen:
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("listen udp %s: %w", addr, err)
		}
		runtime.packetConn = conn
	default:
		return fmt.Errorf("unsupported ingress type %s", req.Spec.Ingress.Type)
	}

	key := tunnelRuntimeKey(req.TunnelID, req.Role)
	if old, loaded := c.tunnels.Swap(key, runtime); loaded {
		if oldRuntime, ok := old.(*clientTunnelRuntime); ok {
			oldRuntime.close()
		}
	}
	switch req.Spec.Ingress.Type {
	case protocol.IngressTypeTCPListen:
		runtime.run(func() { c.acceptIngressTCP(rt, req, runtime) })
	case protocol.IngressTypeUDPListen:
		runtime.run(func() { c.acceptIngressUDP(rt, req, runtime) })
	}
	return nil
}

func (c *Client) acceptIngressTCP(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime) {
	for {
		conn, err := runtime.listener.Accept()
		if err != nil {
			select {
			case <-runtime.done:
				return
			default:
				message := fmt.Sprintf("tunnel ingress accept failed [%s]: %v", req.TunnelID, err)
				log.Printf("⚠️ %s", message)
				c.failIngressTunnelRuntime(rt, req, runtime, message)
				return
			}
		}
		if !runtime.trackTCPConn(conn) {
			_ = conn.Close()
			return
		}
		go c.handleIngressTCPConn(rt, req, runtime, conn)
	}
}

func (c *Client) acceptIngressUDP(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime) {
	runtime.run(func() { c.reapIngressUDPAssociations(runtime) })

	buf := make([]byte, mux.MaxUDPPayload)
	for {
		n, srcAddr, err := runtime.packetConn.ReadFrom(buf)
		if err != nil {
			select {
			case <-runtime.done:
				return
			default:
				if isTemporaryPacketReadError(err) {
					log.Printf("⚠️ temporary UDP ingress read error [%s]: %v", req.TunnelID, err)
					continue
				}
				message := fmt.Sprintf("tunnel UDP ingress read failed [%s]: %v", req.TunnelID, err)
				log.Printf("⚠️ %s", message)
				c.failIngressTunnelRuntime(rt, req, runtime, message)
				return
			}
		}

		payload := make([]byte, n)
		copy(payload, buf[:n])
		c.handleIngressUDPDatagram(rt, req, runtime, srcAddr, payload)
	}
}

func isTemporaryPacketReadError(err error) bool {
	if err == nil {
		return false
	}
	netErr, ok := err.(net.Error)
	return ok && (netErr.Timeout() || netErr.Temporary())
}

func (c *Client) failIngressTunnelRuntime(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime, message string) {
	c.reportTunnelRuntimeError(rt, req, message)
	if runtime == nil {
		return
	}
	key := tunnelRuntimeKey(req.TunnelID, req.Role)
	c.tunnels.CompareAndDelete(key, runtime)
	runtime.shutdown()
}

func (c *Client) reapIngressUDPAssociations(runtime *clientTunnelRuntime) {
	ticker := time.NewTicker(clientUDPAssociationReapEvery)
	defer ticker.Stop()
	for {
		select {
		case <-runtime.done:
			return
		case <-ticker.C:
			runtime.udpAssociations.Range(func(key, value any) bool {
				assoc, ok := value.(*clientUDPAssociation)
				if !ok {
					runtime.udpAssociations.Delete(key)
					return true
				}
				if assoc.idleDuration() > clientUDPAssociationTimeout {
					runtime.removeUDPAssociation(key.(string))
				}
				return true
			})
		}
	}
}

func (c *Client) handleIngressUDPDatagram(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime, srcAddr net.Addr, payload []byte) {
	if runtime == nil || runtime.packetConn == nil || srcAddr == nil {
		return
	}
	assoc, ok := c.getOrCreateIngressUDPAssociation(rt, req, runtime, srcAddr)
	if !ok {
		return
	}
	assoc.touch()

	assoc.writeMu.Lock()
	err := mux.WriteUDPFrame(assoc.stream, payload)
	assoc.writeMu.Unlock()
	if err != nil {
		log.Printf("⚠️ write UDP tunnel ingress frame failed [%s]: %v", req.TunnelID, err)
		runtime.removeUDPAssociation(assoc.key)
	}
}

func (c *Client) getOrCreateIngressUDPAssociation(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime, srcAddr net.Addr) (*clientUDPAssociation, bool) {
	key := srcAddr.String()
	if value, ok := runtime.udpAssociations.Load(key); ok {
		assoc, ok := value.(*clientUDPAssociation)
		return assoc, ok
	}

	runtime.udpAssociationMu.Lock()
	defer runtime.udpAssociationMu.Unlock()
	if value, ok := runtime.udpAssociations.Load(key); ok {
		assoc, ok := value.(*clientUDPAssociation)
		return assoc, ok
	}
	if runtime.udpAssociationCount.Load() >= clientMaxUDPAssociations {
		if !runtime.removeOldestUDPAssociation() || runtime.udpAssociationCount.Load() >= clientMaxUDPAssociations {
			log.Printf("⚠️ tunnel UDP ingress association limit reached [%s], dropping packet from %s", req.TunnelID, key)
			return nil, false
		}
	}

	rt.dataMu.RLock()
	session := rt.dataSession
	rt.dataMu.RUnlock()
	if session == nil || session.IsClosed() {
		message := fmt.Sprintf("data session unavailable for UDP tunnel ingress [%s]", req.TunnelID)
		log.Printf("⚠️ %s", message)
		c.reportTunnelRuntimeError(rt, req, message)
		return nil, false
	}
	stream, err := session.Open()
	if err != nil {
		message := fmt.Sprintf("open UDP tunnel ingress stream failed [%s]: %v", req.TunnelID, err)
		log.Printf("⚠️ %s", message)
		c.reportTunnelRuntimeError(rt, req, message)
		return nil, false
	}

	header, err := ingressDataStreamHeader(req, c.CurrentClientID())
	if err != nil {
		_ = stream.Close()
		message := fmt.Sprintf("build UDP tunnel ingress stream header failed [%s]: %v", req.TunnelID, err)
		log.Printf("⚠️ %s", message)
		c.reportTunnelRuntimeError(rt, req, message)
		return nil, false
	}
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		_ = stream.Close()
		message := fmt.Sprintf("write UDP tunnel ingress stream header failed [%s]: %v", req.TunnelID, err)
		log.Printf("⚠️ %s", message)
		c.reportTunnelRuntimeError(rt, req, message)
		return nil, false
	}

	assoc := newClientUDPAssociation(key, srcAddr, stream)
	actual, loaded := runtime.udpAssociations.LoadOrStore(key, assoc)
	if loaded {
		assoc.close()
		assoc, ok := actual.(*clientUDPAssociation)
		return assoc, ok
	}
	runtime.udpAssociationCount.Add(1)
	if !runtime.run(func() { c.readIngressUDPAssociationReplies(runtime, assoc) }) {
		runtime.removeUDPAssociation(key)
		return nil, false
	}
	return assoc, true
}

func (c *Client) readIngressUDPAssociationReplies(runtime *clientTunnelRuntime, assoc *clientUDPAssociation) {
	defer runtime.removeUDPAssociation(assoc.key)
	for {
		select {
		case <-runtime.done:
			return
		case <-assoc.done:
			return
		default:
		}
		payload, err := mux.ReadUDPFrame(assoc.stream)
		if err != nil {
			select {
			case <-runtime.done:
				return
			case <-assoc.done:
				return
			default:
			}
			log.Printf("⚠️ read UDP tunnel ingress reply failed [%s src=%s]: %v", runtime.tunnelID, assoc.srcAddr, err)
			return
		}
		assoc.touch()
		packetConn, ok := runtime.packetConnForWrite()
		if !ok {
			return
		}
		if _, err := packetConn.WriteTo(payload, assoc.srcAddr); err != nil {
			select {
			case <-runtime.done:
				return
			case <-assoc.done:
				return
			default:
			}
			log.Printf("⚠️ write UDP tunnel ingress reply failed [%s src=%s]: %v", runtime.tunnelID, assoc.srcAddr, err)
			return
		}
	}
}

func ingressDataStreamHeader(req protocol.TunnelProvisionRequest, openClientID string) (protocol.DataStreamHeader, error) {
	streamID, err := protocol.NewDataStreamID()
	if err != nil {
		return protocol.DataStreamHeader{}, err
	}
	header := protocol.DataStreamHeader{
		Kind:         protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:     req.TunnelID,
		Revision:     req.Revision,
		StreamID:     streamID,
		OpenClientID: openClientID,
		SourceRole:   protocol.DataStreamRoleIngress,
		TargetRole:   protocol.DataStreamRoleTarget,
		Direction:    protocol.DataStreamDirectionIngressToTarget,
		Transport:    protocol.ActualTransportServerRelay,
		OpenToken:    "server-relay",
	}
	return header, nil
}

func (c *Client) handleIngressTCPConn(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime, conn net.Conn) {
	defer func() {
		runtime.removeTCPConn(conn)
		_ = conn.Close()
	}()

	rt.dataMu.RLock()
	session := rt.dataSession
	rt.dataMu.RUnlock()
	if session == nil || session.IsClosed() {
		message := fmt.Sprintf("data session unavailable for tunnel ingress [%s]", req.TunnelID)
		log.Printf("⚠️ %s", message)
		c.reportTunnelRuntimeError(rt, req, message)
		return
	}
	stream, err := session.Open()
	if err != nil {
		message := fmt.Sprintf("open tunnel ingress stream failed [%s]: %v", req.TunnelID, err)
		log.Printf("⚠️ %s", message)
		c.reportTunnelRuntimeError(rt, req, message)
		return
	}
	defer func() { _ = stream.Close() }()

	header, err := ingressDataStreamHeader(req, c.CurrentClientID())
	if err != nil {
		message := fmt.Sprintf("build tunnel ingress stream header failed [%s]: %v", req.TunnelID, err)
		log.Printf("⚠️ %s", message)
		c.reportTunnelRuntimeError(rt, req, message)
		return
	}
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		message := fmt.Sprintf("write tunnel ingress stream header failed [%s]: %v", req.TunnelID, err)
		log.Printf("⚠️ %s", message)
		c.reportTunnelRuntimeError(rt, req, message)
		return
	}
	mux.Relay(stream, conn)
}

func (c *Client) reportTunnelRuntimeError(rt *sessionRuntime, req protocol.TunnelProvisionRequest, message string) {
	if rt == nil || req.TunnelID == "" || req.Revision <= 0 || req.Role == "" || strings.TrimSpace(message) == "" {
		return
	}
	clientID := c.CurrentClientID()
	report := protocol.TunnelRuntimeReport{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     req.Role,
		Participant: protocol.ParticipantRuntime{
			ClientID: clientID,
			Role:     req.Role,
			State:    protocol.ProxyRuntimeStateError,
			Revision: req.Revision,
			Error:    message,
		},
		Transport: protocol.TransportRuntime{
			Policy: req.Spec.TransportPolicy,
			Actual: protocol.ActualTransportServerRelay,
		},
		Message: message,
	}
	msg, err := protocol.NewMessage(protocol.MsgTypeTunnelRuntimeReport, report)
	if err != nil {
		log.Printf("⚠️ build tunnel runtime report failed [%s]: %v", req.TunnelID, err)
		return
	}
	if err := rt.writeJSON(msg); err != nil {
		log.Printf("⚠️ send tunnel runtime report failed [%s]: %v", req.TunnelID, err)
	}
}

func proxyRequestFromTunnelSpec(spec protocol.TunnelSpec) (protocol.ProxyNewRequest, error) {
	var target struct {
		IP   string `json:"ip"`
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := decodeEndpointConfig(spec.Target.Config, &target); err != nil {
		return protocol.ProxyNewRequest{}, err
	}
	host := target.Host
	if host == "" {
		host = target.IP
	}
	if host == "" || target.Port <= 0 {
		return protocol.ProxyNewRequest{}, fmt.Errorf("target host and port are required")
	}
	proxyType := protocol.ProxyTypeTCP
	if spec.Target.Type == protocol.TargetTypeUDPService {
		proxyType = protocol.ProxyTypeUDP
	}
	return protocol.ProxyNewRequest{
		ID:                spec.ID,
		Name:              spec.Name,
		Type:              proxyType,
		LocalIP:           host,
		LocalPort:         target.Port,
		TransportPolicy:   spec.TransportPolicy,
		ActualTransport:   protocol.ActualTransportServerRelay,
		ProvisionRevision: uint64(spec.Revision),
		BandwidthSettings: spec.BandwidthSettings,
	}, nil
}

func decodeEndpointConfig(raw []byte, target any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	return json.Unmarshal(raw, target)
}
