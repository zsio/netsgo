package server

import (
	"container/list"
	"strings"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

type unifiedTunnelRuntimeRegistry struct {
	mu           sync.RWMutex
	reports      map[string]runtimeReportFact
	serverIssues map[string]serverIssueBucket
	purged       map[string]purgedTunnelWatermark
	purgeOrder   *list.List
}

const maxPurgedTunnelWatermarks = 1024

type runtimeReportFact struct {
	clientID   string
	report     protocol.TunnelRuntimeReport
	observedAt time.Time
}

// serverIssueBucket keeps an internal revision watermark without exposing it
// through protocol.TunnelIssue. Late records and clears from older runtimes are
// ignored so they cannot affect the current tunnel revision.
type serverIssueBucket struct {
	revision int64
	issues   []protocol.TunnelIssue
}

type purgedTunnelWatermark struct {
	revision int64
	element  *list.Element
}

func newUnifiedTunnelRuntimeRegistry() *unifiedTunnelRuntimeRegistry {
	return &unifiedTunnelRuntimeRegistry{
		reports:      make(map[string]runtimeReportFact),
		serverIssues: make(map[string]serverIssueBucket),
		purged:       make(map[string]purgedTunnelWatermark),
		purgeOrder:   list.New(),
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
	if !r.acceptRevisionAfterPurgeLocked(report.TunnelID, report.Revision) {
		r.mu.Unlock()
		return
	}
	key := runtimeReportKey(report.TunnelID, report.Role)
	if current, ok := r.reports[key]; !ok || current.report.Revision <= report.Revision {
		r.reports[key] = runtimeReportFact{clientID: clientID, report: report, observedAt: observedAt.UTC()}
	}
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) clearTunnelIssues(tunnelID string, revision int64) {
	if r == nil || tunnelID == "" || revision <= 0 {
		return
	}
	r.mu.Lock()
	if !r.acceptRevisionAfterPurgeLocked(tunnelID, revision) {
		r.mu.Unlock()
		return
	}
	r.clearServerIssuesLocked(tunnelID, revision)
	for _, role := range []string{protocol.DataStreamRoleIngress, protocol.DataStreamRoleTarget} {
		key := runtimeReportKey(tunnelID, role)
		if fact, ok := r.reports[key]; ok && fact.report.Revision <= revision {
			delete(r.reports, key)
		}
	}
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) clearServerIssues(tunnelID string, revision int64) {
	if r == nil || tunnelID == "" || revision <= 0 {
		return
	}
	r.mu.Lock()
	if !r.acceptRevisionAfterPurgeLocked(tunnelID, revision) {
		r.mu.Unlock()
		return
	}
	r.clearServerIssuesLocked(tunnelID, revision)
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) clearServerIssuesLocked(tunnelID string, revision int64) {
	current, ok := r.serverIssues[tunnelID]
	if ok && current.revision > revision {
		return
	}
	r.serverIssues[tunnelID] = serverIssueBucket{revision: revision}
}

func (r *unifiedTunnelRuntimeRegistry) purgeTunnelIssues(tunnelID string, revision int64) {
	if r == nil || tunnelID == "" || revision <= 0 {
		return
	}
	r.mu.Lock()
	delete(r.serverIssues, tunnelID)
	delete(r.reports, runtimeReportKey(tunnelID, protocol.DataStreamRoleIngress))
	delete(r.reports, runtimeReportKey(tunnelID, protocol.DataStreamRoleTarget))
	r.recordPurgeLocked(tunnelID, revision)
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) acceptRevisionAfterPurgeLocked(tunnelID string, revision int64) bool {
	watermark, ok := r.purged[tunnelID]
	if !ok {
		return true
	}
	if revision <= watermark.revision {
		return false
	}
	r.purgeOrder.Remove(watermark.element)
	delete(r.purged, tunnelID)
	return true
}

func (r *unifiedTunnelRuntimeRegistry) recordPurgeLocked(tunnelID string, revision int64) {
	if watermark, ok := r.purged[tunnelID]; ok {
		if revision > watermark.revision {
			watermark.revision = revision
			r.purged[tunnelID] = watermark
		}
		r.purgeOrder.MoveToBack(watermark.element)
		return
	}
	if r.purgeOrder.Len() >= maxPurgedTunnelWatermarks {
		oldest := r.purgeOrder.Front()
		delete(r.purged, oldest.Value.(string))
		r.purgeOrder.Remove(oldest)
	}
	element := r.purgeOrder.PushBack(tunnelID)
	r.purged[tunnelID] = purgedTunnelWatermark{revision: revision, element: element}
}

func (r *unifiedTunnelRuntimeRegistry) recordServerIssue(tunnelID string, revision int64, issue protocol.TunnelIssue) {
	if r == nil || tunnelID == "" || revision <= 0 || issue.Code == "" || issue.Message == "" {
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
	if !r.acceptRevisionAfterPurgeLocked(tunnelID, revision) {
		r.mu.Unlock()
		return
	}
	bucket, ok := r.serverIssues[tunnelID]
	if ok && bucket.revision > revision {
		r.mu.Unlock()
		return
	}
	if !ok || bucket.revision < revision {
		bucket = serverIssueBucket{revision: revision}
	}
	issues := append([]protocol.TunnelIssue(nil), bucket.issues...)
	for idx := range issues {
		if sameTunnelIssueIdentity(issues[idx], issue) {
			issues[idx] = issue
			bucket.issues = issues
			r.serverIssues[tunnelID] = bucket
			r.mu.Unlock()
			return
		}
	}
	bucket.issues = append(issues, issue)
	r.serverIssues[tunnelID] = bucket
	r.mu.Unlock()
}

func (r *unifiedTunnelRuntimeRegistry) issuesForStoredTunnel(stored StoredTunnel, online bool) []protocol.TunnelIssue {
	if r == nil || !online || stored.Revision <= 0 || stored.DesiredState == protocol.ProxyDesiredStateStopped {
		return nil
	}
	issues := make([]protocol.TunnelIssue, 0, 3)
	r.mu.RLock()
	serverIssues := r.serverIssues[stored.ID]
	if serverIssues.revision == stored.Revision {
		issues = append(issues, serverIssues.issues...)
	}
	ingressFact, ingressOK := r.reports[runtimeReportKey(stored.ID, protocol.DataStreamRoleIngress)]
	targetFact, targetOK := r.reports[runtimeReportKey(stored.ID, protocol.DataStreamRoleTarget)]
	r.mu.RUnlock()
	if stored.Ingress.Location == tunnelEndpointLocationClient {
		issues = append(issues, issueForRuntimeReportFact(stored, protocol.DataStreamRoleIngress, ingressFact, ingressOK)...)
	}
	issues = append(issues, issueForRuntimeReportFact(stored, protocol.DataStreamRoleTarget, targetFact, targetOK)...)
	return issues
}

func (r *unifiedTunnelRuntimeRegistry) hasIssuesForStoredTunnel(stored StoredTunnel, online bool) bool {
	return len(r.issuesForStoredTunnel(stored, online)) > 0
}

func sameTunnelIssueIdentity(a, b protocol.TunnelIssue) bool {
	return a.Code == b.Code && a.Scope == b.Scope && a.ClientID == b.ClientID
}

func issueForRuntimeReportFact(stored StoredTunnel, role string, fact runtimeReportFact, ok bool) []protocol.TunnelIssue {
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
