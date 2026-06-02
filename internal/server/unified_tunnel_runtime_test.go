package server

import (
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestUnifiedTunnelRuntimeRecordServerIssueMergesByIdentity(t *testing.T) {
	registry := newUnifiedTunnelRuntimeRegistry()
	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "tun-1"},
		DesiredState:    protocol.ProxyDesiredStateRunning,
		Revision:        1,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		Target:          EndpointSpec{ClientID: "target-client"},
	}

	registry.recordServerIssue("tun-1", protocol.TunnelIssue{
		Code:       "provision_ack_timeout",
		Scope:      "target_client",
		ClientID:   "target-client",
		Message:    "first timeout",
		ObservedAt: time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC),
	})
	registry.recordServerIssue("tun-1", protocol.TunnelIssue{
		Code:       "provision_ack_timeout",
		Scope:      "target_client",
		ClientID:   "target-client",
		Message:    "second timeout",
		ObservedAt: time.Date(2026, 5, 28, 10, 1, 0, 0, time.UTC),
	})
	registry.recordServerIssue("tun-1", protocol.TunnelIssue{
		Code:     "ingress_preflight_rejected",
		Scope:    "ingress_client",
		ClientID: "ingress-client",
		Message:  "port unavailable",
	})

	issues := registry.issuesForStoredTunnel(stored, true)
	if len(issues) != 2 {
		t.Fatalf("expected two distinct issues, got %+v", issues)
	}
	if issues[0].Message != "second timeout" {
		t.Fatalf("expected same-identity issue to be replaced, got %+v", issues[0])
	}
	if issues[1].Code != "ingress_preflight_rejected" {
		t.Fatalf("expected distinct issue to be retained, got %+v", issues[1])
	}
}
