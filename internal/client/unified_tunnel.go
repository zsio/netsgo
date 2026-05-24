package client

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
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
	udpAssociations     sync.Map
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
			_ = a.stream.Close()
		}
	})
}

const (
	clientUDPAssociationTimeout   = 60 * time.Second
	clientUDPAssociationReapEvery = 10 * time.Second
	clientMaxUDPAssociations      = 1024
)

func (rt *clientTunnelRuntime) close() {
	if rt == nil {
		return
	}
	rt.once.Do(func() {
		close(rt.done)
		if rt.listener != nil {
			_ = rt.listener.Close()
		}
		if rt.packetConn != nil {
			_ = rt.packetConn.Close()
		}
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
			stale := isRuntime && req.Revision > 0 && runtime.revision > 0 && req.Revision != runtime.revision
			if !stale && c.tunnels.CompareAndDelete(key, value) && isRuntime {
				runtime.close()
			}
		}
	}

	if req.Role == protocol.DataStreamRoleTarget || req.Role == "" {
		if value, ok := c.proxies.Load(req.TunnelID); ok {
			proxy, isProxy := value.(protocol.ProxyNewRequest)
			stale := isProxy && req.Revision > 0 && proxy.ProvisionRevision > 0 && uint64(req.Revision) != proxy.ProvisionRevision
			if !stale {
				c.proxies.CompareAndDelete(req.TunnelID, value)
			}
		}
	}
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
		go c.acceptIngressTCP(rt, req, runtime)
	case protocol.IngressTypeUDPListen:
		go c.acceptIngressUDP(rt, req, runtime)
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
				log.Printf("⚠️ tunnel ingress accept failed [%s]: %v", req.TunnelID, err)
				return
			}
		}
		go c.handleIngressTCPConn(rt, req, conn)
	}
}

func (c *Client) acceptIngressUDP(rt *sessionRuntime, req protocol.TunnelProvisionRequest, runtime *clientTunnelRuntime) {
	go c.reapIngressUDPAssociations(runtime)

	buf := make([]byte, mux.MaxUDPPayload)
	for {
		n, srcAddr, err := runtime.packetConn.ReadFrom(buf)
		if err != nil {
			select {
			case <-runtime.done:
				return
			default:
				log.Printf("⚠️ tunnel UDP ingress read failed [%s]: %v", req.TunnelID, err)
				return
			}
		}

		payload := make([]byte, n)
		copy(payload, buf[:n])
		c.handleIngressUDPDatagram(rt, req, runtime, srcAddr, payload)
	}
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
	if runtime.udpAssociationCount.Load() >= clientMaxUDPAssociations {
		log.Printf("⚠️ tunnel UDP ingress association limit reached [%s], dropping packet from %s", req.TunnelID, key)
		return nil, false
	}

	rt.dataMu.RLock()
	session := rt.dataSession
	rt.dataMu.RUnlock()
	if session == nil || session.IsClosed() {
		log.Printf("⚠️ data session unavailable for UDP tunnel ingress [%s]", req.TunnelID)
		return nil, false
	}
	stream, err := session.Open()
	if err != nil {
		log.Printf("⚠️ open UDP tunnel ingress stream failed [%s]: %v", req.TunnelID, err)
		return nil, false
	}

	header := ingressDataStreamHeader(req, c.CurrentClientID())
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		_ = stream.Close()
		log.Printf("⚠️ write UDP tunnel ingress stream header failed [%s]: %v", req.TunnelID, err)
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
	go c.readIngressUDPAssociationReplies(runtime, assoc)
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
			return
		}
		assoc.touch()
		_, _ = runtime.packetConn.WriteTo(payload, assoc.srcAddr)
	}
}

func ingressDataStreamHeader(req protocol.TunnelProvisionRequest, openClientID string) protocol.DataStreamHeader {
	header := protocol.DataStreamHeader{
		Kind:         protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:     req.TunnelID,
		Revision:     req.Revision,
		StreamID:     protocol.NewDataStreamID(),
		OpenClientID: openClientID,
		SourceRole:   protocol.DataStreamRoleIngress,
		TargetRole:   protocol.DataStreamRoleTarget,
		Direction:    protocol.DataStreamDirectionIngressToTarget,
		Transport:    protocol.ActualTransportServerRelay,
		OpenToken:    "server-relay",
	}
	if header.StreamID == "" {
		header.StreamID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return header
}

func (c *Client) handleIngressTCPConn(rt *sessionRuntime, req protocol.TunnelProvisionRequest, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	rt.dataMu.RLock()
	session := rt.dataSession
	rt.dataMu.RUnlock()
	if session == nil || session.IsClosed() {
		log.Printf("⚠️ data session unavailable for tunnel ingress [%s]", req.TunnelID)
		return
	}
	stream, err := session.Open()
	if err != nil {
		log.Printf("⚠️ open tunnel ingress stream failed [%s]: %v", req.TunnelID, err)
		return
	}
	defer func() { _ = stream.Close() }()

	header := ingressDataStreamHeader(req, c.CurrentClientID())
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		log.Printf("⚠️ write tunnel ingress stream header failed [%s]: %v", req.TunnelID, err)
		return
	}
	mux.Relay(stream, conn)
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
