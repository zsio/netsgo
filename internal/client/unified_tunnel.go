package client

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

type clientTunnelRuntime struct {
	tunnelID string
	revision int64
	role     string
	listener net.Listener
	done     chan struct{}
	once     sync.Once
}

func (rt *clientTunnelRuntime) close() {
	if rt == nil {
		return
	}
	rt.once.Do(func() {
		close(rt.done)
		if rt.listener != nil {
			_ = rt.listener.Close()
		}
	})
}

func tunnelRuntimeKey(tunnelID, role string) string {
	return tunnelID + ":" + role
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
	key := tunnelRuntimeKey(req.TunnelID, req.Role)
	if value, ok := c.tunnels.LoadAndDelete(key); ok {
		if runtime, ok := value.(*clientTunnelRuntime); ok {
			runtime.close()
		}
	}
	if req.Role == protocol.DataStreamRoleTarget || req.Role == "" {
		c.proxies.Delete(req.TunnelID)
	}
}

func (c *Client) startIngressTunnelRuntime(rt *sessionRuntime, req protocol.TunnelProvisionRequest) error {
	if req.Spec.Ingress.Type != protocol.IngressTypeTCPListen {
		return fmt.Errorf("unsupported ingress type %s", req.Spec.Ingress.Type)
	}
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
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	runtime := &clientTunnelRuntime{
		tunnelID: req.TunnelID,
		revision: req.Revision,
		role:     req.Role,
		listener: ln,
		done:     make(chan struct{}),
	}
	key := tunnelRuntimeKey(req.TunnelID, req.Role)
	if old, loaded := c.tunnels.Swap(key, runtime); loaded {
		if oldRuntime, ok := old.(*clientTunnelRuntime); ok {
			oldRuntime.close()
		}
	}
	go c.acceptIngressTCP(rt, req, runtime)
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

	header := protocol.DataStreamHeader{
		Kind:         protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:     req.TunnelID,
		Revision:     req.Revision,
		StreamID:     protocol.NewDataStreamID(),
		OpenClientID: c.CurrentClientID(),
		SourceRole:   protocol.DataStreamRoleIngress,
		TargetRole:   protocol.DataStreamRoleTarget,
		Direction:    protocol.DataStreamDirectionIngressToTarget,
		Transport:    protocol.ActualTransportServerRelay,
		OpenToken:    "server-relay",
	}
	if header.StreamID == "" {
		header.StreamID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
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
