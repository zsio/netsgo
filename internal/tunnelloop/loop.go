// Package tunnelloop rejects proven direct TCP self-loops. It never probes a
// target service, and unknown network identities remain compatible.
package tunnelloop

import (
	"encoding/json"
	"errors"
	"net"
	"strconv"

	"netsgo/pkg/protocol"
)

var ErrLoop = errors.New("target points back to this tunnel's own TCP ingress")

type Guard struct {
	bind net.IP
	port int
}

func New(spec protocol.TunnelSpec, ingressNetworkID, targetNetworkID string) *Guard {
	if ingressNetworkID == "" || ingressNetworkID != targetNetworkID ||
		spec.Ingress.Type != protocol.IngressTypeTCPListen || spec.Target.Type != protocol.TargetTypeTCPService {
		return nil
	}
	var cfg struct {
		BindIP string `json:"bind_ip"`
		Port   int    `json:"port"`
	}
	if json.Unmarshal(spec.Ingress.Config, &cfg) != nil || cfg.Port <= 0 {
		return nil
	}
	if cfg.BindIP == "" {
		cfg.BindIP = "0.0.0.0"
	}
	bind := net.ParseIP(cfg.BindIP)
	if bind == nil {
		return nil
	}
	return &Guard{bind: bind, port: cfg.Port}
}

// CheckLiteral is suitable for configuration/provision validation. Hostnames
// are checked after resolution by the target dialer.
func (g *Guard) CheckLiteral(host string, port int) error {
	if g == nil || port != g.port {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if !g.bind.IsUnspecified() {
		if g.bind.Equal(ip) {
			return ErrLoop
		}
		return nil
	}
	// NetsGo uses net.Listen("tcp", wildcard), which can also bind IPv6.
	if ip.IsLoopback() || ip.IsUnspecified() {
		return ErrLoop
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, addr := range addresses {
		if local, _, err := net.ParseCIDR(addr.String()); err == nil && local.Equal(ip) {
			return ErrLoop
		}
	}
	return nil
}

// CheckAddress is invoked by net.Dialer.Control after DNS resolution but before
// connecting. This closes the hostname/DNS-change gap without extra traffic.
func (g *Guard) CheckAddress(address string) error {
	if g == nil {
		return nil
	}
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		return err
	}
	return g.CheckLiteral(host, port)
}
