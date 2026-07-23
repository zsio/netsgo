package server

import (
	"log"
	"time"
)

func p2pActivitySpec(result p2pLifecycleResult, action string, grants []p2pGrantSnapshot, relation string) ActivityEventSpec {
	clients := []ActivityClientSubject{
		{ClientID: result.Session.ClientA, Relation: "peer"},
		{ClientID: result.Session.ClientB, Relation: "peer"},
	}
	tunnels := make([]ActivityTunnelSubject, 0, len(grants))
	for _, grant := range grants {
		tunnels = append(tunnels, ActivityTunnelSubject{TunnelID: grant.TunnelID, Relation: relation})
	}
	payload := newActivityP2PPayload(action, result.ReasonCode, result.Session.SessionID, result.Sequence, ActivitySummaryArgs{Count: len(grants)})
	return ActivityEventSpec{
		OccurredAt: time.Now().UTC(), Category: ActivityCategoryP2P, Action: action, Source: "server",
		Actor: systemActivityActor(), DedupeKey: result.projectionKey(action), Payload: payload,
		Clients: clients, Tunnels: tunnels,
	}
}

func p2pActivitySpecs(result p2pLifecycleResult) []ActivityEventSpec {
	if result.Session.SessionID == "" {
		return nil
	}
	var specs []ActivityEventSpec
	if result.SessionCreated {
		specs = append(specs, p2pActivitySpec(result, "session_started", result.Session.Grants, "shared_session"))
	}
	if result.GrantCreated {
		specs = append(specs, p2pActivitySpec(result, "tunnel_attached", []p2pGrantSnapshot{result.Grant}, "subject"))
	}
	if result.ReportAccepted && !result.ReadyEdge && !result.FailedEdge {
		specs = append(specs, p2pActivitySpec(result, "checking", result.Session.Grants, "shared_session"))
	}
	if result.ReadyEdge {
		specs = append(specs, p2pActivitySpec(result, "connected", result.Session.Grants, "shared_session"))
	}
	if result.FailedEdge {
		if len(result.ActivityActions) == 0 {
			specs = append(specs, p2pActivitySpec(result, "failed", result.Session.Grants, "shared_session"))
		} else {
			for _, action := range []string{"failed", "fallback"} {
				if grants := result.ActivityActions[action]; len(grants) > 0 {
					specs = append(specs, p2pActivitySpec(result, action, grants, "shared_session"))
				}
			}
		}
	}
	if result.DetachedEdge {
		specs = append(specs, p2pActivitySpec(result, "tunnel_detached", []p2pGrantSnapshot{result.Grant}, "subject"))
	}
	if result.ClosedEdge {
		specs = append(specs, p2pActivitySpec(result, "session_closed", result.Session.Grants, "shared_session"))
	}
	return specs
}

func (s *Server) appendP2PActivities(result p2pLifecycleResult) {
	s.ensureSharedStoreReferences()
	if s.activityStore == nil {
		return
	}
	for _, spec := range p2pActivitySpecs(result) {
		id, err := s.activityStore.Append(spec)
		if err != nil {
			log.Printf("⚠️ Failed to persist P2P activity [%s/%s]: %v", result.Session.SessionID, spec.Action, err)
			continue
		}
		s.publishActivityID(id)
	}
}
