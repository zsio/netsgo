package server

import (
	"encoding/json"
	"fmt"
	"net"

	"netsgo/internal/ingresspolicy"
)

type ingressAccessPolicy struct {
	allowedSourceCIDRs []string
	sourceCIDRs        []*net.IPNet
}

func allowAllSourceCIDRs() []string {
	return ingresspolicy.AllowAllSourceCIDRs()
}

func parseIngressAccessPolicy(values []string, allowMissing bool) (ingressAccessPolicy, error) {
	policy, err := ingresspolicy.Parse(values, allowMissing)
	if err != nil {
		return ingressAccessPolicy{}, err
	}
	return ingressAccessPolicy{allowedSourceCIDRs: policy.AllowedSourceCIDRs, sourceCIDRs: policy.SourceCIDRs}, nil
}

func decodeIngressAccessPolicy(raw json.RawMessage, allowMissing bool) (ingressAccessPolicy, error) {
	policy, err := ingresspolicy.Decode(raw, allowMissing)
	if err != nil {
		return ingressAccessPolicy{}, err
	}
	return ingressAccessPolicy{allowedSourceCIDRs: policy.AllowedSourceCIDRs, sourceCIDRs: policy.SourceCIDRs}, nil
}

func sourceAddressAllowed(addr net.Addr, cidrs []*net.IPNet) bool {
	return ingresspolicy.SourceAllowed(addr, cidrs)
}

func rejectSourceAddressMessage(addr net.Addr) string {
	if addr == nil {
		return "source address denied by ingress policy"
	}
	return fmt.Sprintf("source address %s denied by ingress policy", addr.String())
}
