package server

import (
	"strings"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

type unifiedTunnelRuntimeRegistry struct {
	mu           sync.RWMutex
	reports      map[string]runtimeReportFact
	serverIssues map[string][]protocol.TunnelIssue
}

type runtimeReportFact struct {
	clientID   string
	report     protocol.TunnelRuntimeReport
	observedAt time.Time
}

func newUnifiedTunnelRuntimeRegistry() *unifiedTunnelRuntimeRegistry {
	return &unifiedTunnelRuntimeRegistry{
		reports:      make(map[string]runtimeReportFact),
		serverIssues: make(map[string][]protocol.TunnelIssue),
	}
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

func (r *unifiedTunnelRuntimeRegistry) clearTunnelIssues(tunnelID string) {
	if r == nil || tunnelID == "" {
		return
	}
	r.mu.Lock()
	delete(r.serverIssues, tunnelID)
	delete(r.reports, runtimeReportKey(tunnelID, protocol.DataStreamRoleIngress))
	delete(r.reports, runtimeReportKey(tunnelID, protocol.DataStreamRoleTarget))
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) clearServerIssues(tunnelID string) {
	if r == nil || tunnelID == "" {
		return
	}
	r.mu.Lock()
	delete(r.serverIssues, tunnelID)
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) recordServerIssue(tunnelID string, issue protocol.TunnelIssue) {
	if r == nil || tunnelID == "" || issue.Code == "" || issue.Message == "" {
		return
	}
	if issue.Severity == "" {
		issue.Severity = "error"
	}
	if issue.ObservedAt.IsZero() {
		issue.ObservedAt = time.Now().UTC()
	} else {
		issue.ObservedAt = issue.ObservedAt.UTC()
	}
	r.mu.Lock()
	r.serverIssues[tunnelID] = []protocol.TunnelIssue{issue}
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) issuesForStoredTunnel(stored StoredTunnel, online bool) []protocol.TunnelIssue {
	if r == nil || !online || stored.Revision <= 0 || stored.DesiredState == protocol.ProxyDesiredStateStopped {
		return nil
	}
	issues := make([]protocol.TunnelIssue, 0, 3)
	r.mu.RLock()
	issues = append(issues, r.serverIssues[stored.ID]...)
	r.mu.RUnlock()
	if stored.Ingress.Location == tunnelEndpointLocationClient {
		issues = append(issues, r.issueForRole(stored, protocol.DataStreamRoleIngress)...)
	}
	issues = append(issues, r.issueForRole(stored, protocol.DataStreamRoleTarget)...)
	return issues
}

func (r *unifiedTunnelRuntimeRegistry) hasIssuesForStoredTunnel(stored StoredTunnel, online bool) bool {
	return len(r.issuesForStoredTunnel(stored, online)) > 0
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
		Code:       protocol.TunnelIssueCodeRuntimeReport,
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
