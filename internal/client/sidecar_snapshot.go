package client

import (
	"fmt"
	"net"
	"sort"
	"time"

	"netsgo/internal/connlimit"
	"netsgo/pkg/protocol"
)

// SidecarSnapshot describes the live link-layer and tunnel runtime state of a
// client process for local desktop supervisors. It reflects only NetsGo-owned
// link/runtime facts and never probes user target services.
type SidecarSnapshot struct {
	Server            string                  `json:"server"`
	ClientID          string                  `json:"client_id,omitempty"`
	Connected         bool                    `json:"connected"`
	ActiveConnections int64                   `json:"active_connections"`
	MaxConnections    int                     `json:"max_connections"`
	PublicIPv4        string                  `json:"public_ipv4,omitempty"`
	PublicIPv6        string                  `json:"public_ipv6,omitempty"`
	UptimeSeconds     int64                   `json:"uptime_seconds"`
	Tunnels           []SidecarTunnelSnapshot `json:"tunnels"`
}

// SidecarTunnelSnapshot summarizes one provisioned tunnel participant on this client.
type SidecarTunnelSnapshot struct {
	TunnelID          string `json:"tunnel_id"`
	Name              string `json:"name,omitempty"`
	Revision          int64  `json:"revision,omitempty"`
	Role              string `json:"role"`
	Topology          string `json:"topology,omitempty"`
	IngressType       string `json:"ingress_type,omitempty"`
	TargetType        string `json:"target_type,omitempty"`
	LocalEndpoint     string `json:"local_endpoint,omitempty"`
	TransportPolicy   string `json:"transport_policy,omitempty"`
	EffectivePath     string `json:"effective_path"`
	ActiveConnections int64  `json:"active_connections"`
}

// RuntimeSnapshot captures a consistent point-in-time summary of the client's
// link and provisioned tunnel state.
func (c *Client) RuntimeSnapshot() SidecarSnapshot {
	serverAddr, _ := c.currentServerState()
	clientID := c.CurrentClientID()

	rt := c.getCurrentRuntime()
	connected := false
	var peerMgr *clientPeerManager
	if rt != nil {
		rt.dataMu.RLock()
		ds := rt.dataSession
		rt.dataMu.RUnlock()
		connected = ds != nil && !ds.IsClosed()
		peerMgr = rt.peerManager
	}

	maxConns := c.MaxConnections
	if maxConns <= 0 {
		maxConns = connlimit.Default
	}

	c.mu.Lock()
	pubV4 := c.publicIPv4
	pubV6 := c.publicIPv6
	uptime := int64(0)
	if !c.startTime.IsZero() {
		uptime = int64(time.Since(c.startTime).Seconds())
	}
	c.mu.Unlock()

	directTunnels := activeDirectGrantMap(peerMgr)
	tunnels := make([]SidecarTunnelSnapshot, 0)

	c.tunnels.Range(func(_, value any) bool {
		ing, ok := value.(*clientTunnelRuntime)
		if !ok || ing == nil {
			return true
		}
		active := ing.tcpConnCount.Load() + ing.udpAssociationCount.Load()
		tunnels = append(tunnels, SidecarTunnelSnapshot{
			TunnelID:          ing.tunnelID,
			Name:              ing.name,
			Revision:          ing.revision,
			Role:              ing.role,
			Topology:          ing.topology,
			IngressType:       ing.ingressType,
			TargetType:        ing.targetType,
			LocalEndpoint:     ing.listenAddr,
			TransportPolicy:   ing.transportPolicy,
			EffectivePath:     effectiveTransportForTunnel(ing.tunnelID, ing.revision, protocol.DataStreamRoleIngress, ing.transportPolicy, connected, directTunnels),
			ActiveConnections: active,
		})
		return true
	})

	c.fixedTargetRuntimes.Range(func(_, value any) bool {
		target, ok := value.(*fixedServiceTargetRuntime)
		if !ok || target == nil {
			return true
		}
		tunnels = append(tunnels, SidecarTunnelSnapshot{
			TunnelID:        target.tunnelID,
			Name:            target.name,
			Revision:        target.revision,
			Role:            protocol.DataStreamRoleTarget,
			Topology:        target.topology,
			IngressType:     target.ingressType,
			TargetType:      target.targetType,
			LocalEndpoint:   target.address(),
			TransportPolicy: target.transportPolicy,
			EffectivePath:   effectiveTransportForTunnel(target.tunnelID, target.revision, protocol.DataStreamRoleTarget, target.transportPolicy, connected, directTunnels),
		})
		return true
	})

	c.socks5Targets.Range(func(_, value any) bool {
		target, ok := value.(*clientSOCKS5TargetRuntime)
		if !ok || target == nil {
			return true
		}
		tunnels = append(tunnels, SidecarTunnelSnapshot{
			TunnelID:        target.tunnelID,
			Name:            target.spec.Name,
			Revision:        target.revision,
			Role:            protocol.DataStreamRoleTarget,
			Topology:        target.spec.Topology,
			IngressType:     target.spec.Ingress.Type,
			TargetType:      protocol.TargetTypeSOCKS5ConnectHandler,
			LocalEndpoint:   "SOCKS5 Connect",
			TransportPolicy: target.spec.TransportPolicy,
			EffectivePath:   effectiveTransportForTunnel(target.tunnelID, target.revision, protocol.DataStreamRoleTarget, target.spec.TransportPolicy, connected, directTunnels),
		})
		return true
	})

	c.proxies.Range(func(key, value any) bool {
		cfg, ok := value.(protocol.ProxyNewRequest)
		if !ok {
			return true
		}
		tunnelID := cfg.ID
		if tunnelID == "" {
			if name, isStr := key.(string); isStr {
				tunnelID = name
			} else {
				tunnelID = cfg.Name
			}
		}
		localEndpoint := ""
		if cfg.LocalIP != "" && cfg.LocalPort > 0 {
			localEndpoint = net.JoinHostPort(cfg.LocalIP, fmt.Sprintf("%d", cfg.LocalPort))
		}
		path := cfg.ActualTransport
		if path == "" || path == protocol.ActualTransportUnknown {
			path = effectiveTransportForTunnel(tunnelID, int64(cfg.ProvisionRevision), protocol.DataStreamRoleTarget, cfg.TransportPolicy, connected, directTunnels)
		}
		tunnels = append(tunnels, SidecarTunnelSnapshot{
			TunnelID:        tunnelID,
			Name:            cfg.Name,
			Revision:        int64(cfg.ProvisionRevision),
			Role:            protocol.DataStreamRoleTarget,
			Topology:        protocol.TunnelTopologyServerExpose,
			TargetType:      cfg.Type,
			LocalEndpoint:   localEndpoint,
			TransportPolicy: cfg.TransportPolicy,
			EffectivePath:   path,
		})
		return true
	})

	sort.Slice(tunnels, func(i, j int) bool {
		if tunnels[i].Name != tunnels[j].Name {
			return tunnels[i].Name < tunnels[j].Name
		}
		if tunnels[i].TunnelID != tunnels[j].TunnelID {
			return tunnels[i].TunnelID < tunnels[j].TunnelID
		}
		return tunnels[i].Role < tunnels[j].Role
	})

	return SidecarSnapshot{
		Server:            serverAddr,
		ClientID:          clientID,
		Connected:         connected,
		ActiveConnections: c.dataConnections.Active(),
		MaxConnections:    maxConns,
		PublicIPv4:        pubV4,
		PublicIPv6:        pubV6,
		UptimeSeconds:     uptime,
		Tunnels:           tunnels,
	}
}

func (c *Client) emitSidecarSnapshot(reason string) {
	logger := c.logger()
	if logger.Format() != LogFormatJSON {
		return
	}
	snap := c.RuntimeSnapshot()
	logger.Info("client.runtime_snapshot", "Client runtime snapshot updated", map[string]any{
		"reason":   reason,
		"snapshot": snap,
	})
}

type directGrantKey struct {
	tunnelID string
	role     string
}

func activeDirectGrantMap(m *clientPeerManager) map[directGrantKey]int64 {
	result := make(map[directGrantKey]int64)
	if m == nil {
		return result
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := m.now()
	for _, s := range m.sessions {
		if s == nil || !s.expiresAt.After(now) || s.peer == nil || !s.peer.Available() {
			continue
		}
		for _, g := range s.grants {
			if g.ExpiresAt.After(now) {
				result[directGrantKey{tunnelID: g.TunnelID, role: g.LocalRole}] = g.Revision
			}
		}
	}
	return result
}

func effectiveTransportForTunnel(
	tunnelID string,
	revision int64,
	role string,
	policy string,
	connected bool,
	direct map[directGrantKey]int64,
) string {
	if !connected {
		return protocol.ActualTransportUnknown
	}
	if grantRev, ok := direct[directGrantKey{tunnelID: tunnelID, role: role}]; ok {
		if revision <= 0 || grantRev == revision {
			return protocol.ActualTransportPeerDirect
		}
	}
	if policy == protocol.TransportPolicyDirectOnly {
		return protocol.ActualTransportUnknown
	}
	return protocol.ActualTransportServerRelay
}
