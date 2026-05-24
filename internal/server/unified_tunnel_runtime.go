package server

import (
	"strings"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

type unifiedTunnelRuntimeRegistry struct {
	mu      sync.RWMutex
	reports map[string]runtimeReportFact
}

type runtimeReportFact struct {
	clientID   string
	report     protocol.TunnelRuntimeReport
	observedAt time.Time
}

func newUnifiedTunnelRuntimeRegistry() *unifiedTunnelRuntimeRegistry {
	return &unifiedTunnelRuntimeRegistry{reports: make(map[string]runtimeReportFact)}
}

func (r *unifiedTunnelRuntimeRegistry) recordReport(clientID string, report protocol.TunnelRuntimeReport, observedAt time.Time) {
	if r == nil || report.TunnelID == "" || report.Revision <= 0 || report.Role == "" {
		return
	}
	if report.Transport.Actual != "" && report.Transport.Actual != protocol.ActualTransportServerRelay {
		return
	}
	r.mu.Lock()
	r.reports[runtimeReportKey(report.TunnelID, report.Role)] = runtimeReportFact{clientID: clientID, report: report, observedAt: observedAt.UTC()}
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) issuesForStoredTunnel(stored StoredTunnel, online bool) []protocol.TunnelIssue {
	if r == nil || !online || stored.Revision <= 0 {
		return nil
	}
	issues := make([]protocol.TunnelIssue, 0, 2)
	if stored.Ingress.Location == tunnelEndpointLocationClient {
		issues = append(issues, r.issueForRole(stored, protocol.DataStreamRoleIngress)...)
	}
	issues = append(issues, r.issueForRole(stored, protocol.DataStreamRoleTarget)...)
	return issues
}

func (r *unifiedTunnelRuntimeRegistry) issueForRole(stored StoredTunnel, role string) []protocol.TunnelIssue {
	r.mu.RLock()
	fact, ok := r.reports[runtimeReportKey(stored.ID, role)]
	r.mu.RUnlock()
	if !ok || fact.report.Revision != stored.Revision || fact.report.Role != role {
		return nil
	}
	if stored.TransportPolicy != protocol.TransportPolicyServerRelayOnly {
		return nil
	}
	if fact.report.Transport.Actual != "" && fact.report.Transport.Actual != protocol.ActualTransportServerRelay {
		return nil
	}
	expectedClientID := stored.Target.ClientID
	scope := "target_client"
	if role == protocol.DataStreamRoleIngress {
		expectedClientID = stored.Ingress.ClientID
		scope = "ingress_client"
	}
	if expectedClientID == "" || fact.clientID != expectedClientID {
		return nil
	}
	message := strings.TrimSpace(fact.report.Message)
	if message == "" {
		message = strings.TrimSpace(fact.report.Participant.Error)
	}
	if message == "" {
		message = strings.TrimSpace(fact.report.Transport.P2PError)
	}
	if message == "" {
		return nil
	}
	return []protocol.TunnelIssue{{
		Code:       "runtime_report",
		Scope:      scope,
		ClientID:   expectedClientID,
		Severity:   "error",
		Message:    message,
		Retryable:  true,
		ObservedAt: fact.observedAt,
	}}
}

func runtimeReportKey(tunnelID, role string) string {
	return tunnelID + ":" + role
}
