package server

import (
	"fmt"
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

	registry.recordServerIssue("tun-1", stored.Revision, protocol.TunnelIssue{
		Code:       "provision_ack_timeout",
		Scope:      "target_client",
		ClientID:   "target-client",
		Message:    "first timeout",
		ObservedAt: time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC),
	})
	registry.recordServerIssue("tun-1", stored.Revision, protocol.TunnelIssue{
		Code:       "provision_ack_timeout",
		Scope:      "target_client",
		ClientID:   "target-client",
		Message:    "second timeout",
		ObservedAt: time.Date(2026, 5, 28, 10, 1, 0, 0, time.UTC),
	})
	registry.recordServerIssue("tun-1", stored.Revision, protocol.TunnelIssue{
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

func TestUnifiedTunnelRuntimeServerIssuesAreRevisionScoped(t *testing.T) {
	registry := newUnifiedTunnelRuntimeRegistry()
	storedV1 := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "tun-revision"},
		DesiredState:    protocol.ProxyDesiredStateRunning,
		Revision:        1,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		Target:          EndpointSpec{ClientID: "target-client"},
	}
	storedV2 := storedV1
	storedV2.Revision = 2

	registry.recordServerIssue(storedV1.ID, storedV1.Revision, protocol.TunnelIssue{
		Code:     "revision-one",
		Scope:    "server",
		Message:  "old runtime failed",
		Severity: "error",
	})
	if issues := registry.issuesForStoredTunnel(storedV2, true); len(issues) != 0 {
		t.Fatalf("revision 1 issue must not project onto revision 2: %+v", issues)
	}

	registry.recordServerIssue(storedV2.ID, storedV2.Revision, protocol.TunnelIssue{
		Code:     "revision-two",
		Scope:    "server",
		Message:  "current runtime failed",
		Severity: "error",
	})
	registry.recordServerIssue(storedV1.ID, storedV1.Revision, protocol.TunnelIssue{
		Code:     "late-revision-one",
		Scope:    "server",
		Message:  "late old failure",
		Severity: "error",
	})
	issues := registry.issuesForStoredTunnel(storedV2, true)
	if len(issues) != 1 || issues[0].Code != "revision-two" {
		t.Fatalf("late old issue must not replace current revision issue: %+v", issues)
	}

	registry.clearServerIssues(storedV1.ID, storedV1.Revision)
	if issues := registry.issuesForStoredTunnel(storedV2, true); len(issues) != 1 || issues[0].Code != "revision-two" {
		t.Fatalf("old revision clear must not clear current issues: %+v", issues)
	}
	registry.clearServerIssues(storedV2.ID, storedV2.Revision)
	if issues := registry.issuesForStoredTunnel(storedV2, true); len(issues) != 0 {
		t.Fatalf("current revision clear should remove its issues: %+v", issues)
	}

	registry.recordServerIssue(storedV1.ID, storedV1.Revision, protocol.TunnelIssue{
		Code:    "late-after-clear",
		Scope:   "server",
		Message: "late after watermark",
	})
	if issues := registry.issuesForStoredTunnel(storedV2, true); len(issues) != 0 {
		t.Fatalf("current revision clear watermark must reject late old issues: %+v", issues)
	}
}

func TestUnifiedTunnelRuntimePurgeRejectsLateRevisionAndAllowsNewerReuse(t *testing.T) {
	registry := newUnifiedTunnelRuntimeRegistry()
	tunnelID := "purged-runtime"
	issue := protocol.TunnelIssue{Code: "runtime-error", Scope: "server", Message: "failed"}
	report := protocol.TunnelRuntimeReport{
		TunnelID: tunnelID,
		Revision: 2,
		Role:     protocol.DataStreamRoleTarget,
		Message:  "target failed",
	}
	registry.recordServerIssue(tunnelID, 2, issue)
	registry.recordReport("target-client", report, time.Now())

	registry.purgeTunnelIssues(tunnelID, 2)
	registry.recordServerIssue(tunnelID, 2, issue)
	registry.recordReport("target-client", report, time.Now())
	registry.clearTunnelIssues(tunnelID, 2)

	registry.mu.RLock()
	_, hasIssues := registry.serverIssues[tunnelID]
	_, hasReport := registry.reports[runtimeReportKey(tunnelID, protocol.DataStreamRoleTarget)]
	watermark, hasWatermark := registry.purged[tunnelID]
	registry.mu.RUnlock()
	if hasIssues || hasReport {
		t.Fatalf("late purged revision recreated registry state: issues=%v report=%v", hasIssues, hasReport)
	}
	if !hasWatermark || watermark.revision != 2 {
		t.Fatalf("purge watermark mismatch: %+v", watermark)
	}

	registry.recordServerIssue(tunnelID, 3, protocol.TunnelIssue{Code: "revision-three", Scope: "server", Message: "new revision"})
	report.Revision = 3
	registry.recordReport("target-client", report, time.Now())
	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: tunnelID},
		DesiredState:    protocol.ProxyDesiredStateRunning,
		Revision:        3,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		Target:          EndpointSpec{ClientID: "target-client"},
	}
	if issues := registry.issuesForStoredTunnel(stored, true); len(issues) != 2 {
		t.Fatalf("newer revision should be accepted after purge: %+v", issues)
	}
}

func TestUnifiedTunnelRuntimePurgeWatermarksAreBounded(t *testing.T) {
	registry := newUnifiedTunnelRuntimeRegistry()
	for i := 0; i < maxPurgedTunnelWatermarks+25; i++ {
		registry.purgeTunnelIssues(fmt.Sprintf("purged-%d", i), 1)
	}

	registry.mu.RLock()
	purgedCount := len(registry.purged)
	orderCount := registry.purgeOrder.Len()
	registry.mu.RUnlock()
	if purgedCount != maxPurgedTunnelWatermarks || orderCount != maxPurgedTunnelWatermarks {
		t.Fatalf("purge watermark bound mismatch: map=%d order=%d limit=%d", purgedCount, orderCount, maxPurgedTunnelWatermarks)
	}
}
