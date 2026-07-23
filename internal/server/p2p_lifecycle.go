package server

import (
	"fmt"
	"sort"
	"strings"

	"netsgo/pkg/protocol"
)

type p2pGrantSnapshot struct {
	TunnelID        string
	Revision        int64
	IngressClientID string
	TargetClientID  string
}

type p2pSessionSnapshot struct {
	SessionID     string
	ClientA       string
	ClientB       string
	GenerationA   uint64
	GenerationB   uint64
	LeaseSequence uint64
	Ready         bool
	Grants        []p2pGrantSnapshot
}

type p2pLifecycleResult struct {
	Session           p2pSessionSnapshot
	Grant             p2pGrantSnapshot
	HasGrant          bool
	SessionCreated    bool
	GrantCreated      bool
	ReportAccepted    bool
	ReadyEdge         bool
	FailedEdge        bool
	DetachedEdge      bool
	ClosedEdge        bool
	StatusState       string
	ReasonCode        string
	ActivityActions   map[string][]p2pGrantSnapshot
	Sequence          uint64
	Transition        P2PProjectionTransition
	ExpectedSessionID string
	Outbounds         []p2pOutbound
}

type p2pRenewResult struct {
	Outbounds []p2pOutbound
	Closed    []p2pLifecycleResult
}

func (r p2pLifecycleResult) hasLifecycleEdge() bool {
	return r.SessionCreated || r.GrantCreated || r.ReportAccepted || r.DetachedEdge || r.ClosedEdge
}

func (r p2pLifecycleResult) projectionKey(action string) string {
	return fmt.Sprintf("p2p:%s:%s:%d", r.Session.SessionID, action, r.Sequence)
}

func snapshotP2PSession(session *p2pPairSession) p2pSessionSnapshot {
	if session == nil {
		return p2pSessionSnapshot{}
	}
	grants := make([]p2pGrantSnapshot, 0, len(session.grants))
	for _, grant := range session.grants {
		grants = append(grants, snapshotP2PGrant(grant))
	}
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].TunnelID != grants[j].TunnelID {
			return grants[i].TunnelID < grants[j].TunnelID
		}
		return grants[i].Revision < grants[j].Revision
	})
	return p2pSessionSnapshot{
		SessionID: session.id, ClientA: session.clientA, ClientB: session.clientB,
		GenerationA: session.generationA, GenerationB: session.generationB,
		LeaseSequence: session.leaseSequence,
		Ready:         session.ready[session.clientA] && session.ready[session.clientB],
		Grants:        grants,
	}
}

func snapshotP2PGrant(grant p2pGrant) p2pGrantSnapshot {
	return p2pGrantSnapshot{
		TunnelID: grant.tunnelID, Revision: grant.revision,
		IngressClientID: grant.ingressClientID, TargetClientID: grant.targetClientID,
	}
}

func p2pClosedOutbounds(session *p2pPairSession, reason string) []p2pOutbound {
	if session == nil {
		return nil
	}
	status := protocol.P2PSessionStatus{
		SessionID: session.id, Sequence: session.leaseSequence + 1,
		State: protocol.P2PStateClosed, Error: reason,
	}
	return []p2pOutbound{
		{clientID: session.clientA, messageType: protocol.MsgTypeP2PClosed, payload: status},
		{clientID: session.clientB, messageType: protocol.MsgTypeP2PClosed, payload: status},
	}
}

func normalizeP2PCloseReason(reason, state string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case state == protocol.P2PStateFailed:
		return "negotiation_failed"
	case strings.Contains(reason, "offline"), strings.Contains(reason, "disconnect"), strings.Contains(reason, "session_released"):
		return "participant_offline"
	case strings.Contains(reason, "unhealthy"):
		return "lease_unhealthy"
	case strings.Contains(reason, "expired"):
		return "lease_expired"
	case strings.Contains(reason, "stop"), strings.Contains(reason, "disabled"):
		return "tunnel_stopped"
	case strings.Contains(reason, "delete"):
		return "tunnel_deleted"
	case strings.Contains(reason, "revision"), strings.Contains(reason, "replace"):
		return "revision_replaced"
	default:
		return "unknown"
	}
}
