package server

import (
	"log"
	"net/http"
	"time"
)

func systemActivityActor() ActivityActor {
	return ActivityActor{Type: "system"}
}

func (s *Server) activityActorForRequest(r *http.Request) ActivityActor {
	info := GetSessionFromContext(r.Context())
	if info == nil {
		return ActivityActor{Type: "unknown"}
	}
	secret := ""
	if s.auth != nil && s.auth.adminStore != nil {
		if raw, err := s.auth.adminStore.GetJWTSecret(); err == nil {
			secret = string(raw)
		}
	}
	return NewActivityActor("admin", info.UserID, info.Username, s.clientIP(r), secret)
}

func tunnelActivitySpec(action string, tunnel StoredTunnel, actor ActivityActor) ActivityEventSpec {
	if actor.Type == "" {
		actor = systemActivityActor()
	}
	args := ActivitySummaryArgs{
		TunnelName: tunnel.Name,
		Topology:   tunnel.Topology,
		Transport:  tunnel.ActualTransport,
	}
	payload := newActivityPayload(ActivityCategoryTunnel, action, args)
	if tunnel.Revision > 0 {
		payload.Revision = uint64(tunnel.Revision)
	}
	clients := make([]ActivityClientSubject, 0, 3)
	ownerID := tunnel.OwnerClientID
	if ownerID == "" {
		ownerID = tunnel.ClientID
	}
	if ownerID != "" {
		clients = append(clients, ActivityClientSubject{
			ClientID: ownerID, Relation: "owner", Hostname: tunnel.Hostname,
		})
	}
	if tunnel.Ingress.ClientID != "" {
		clients = append(clients, ActivityClientSubject{
			ClientID: tunnel.Ingress.ClientID, Relation: "ingress",
		})
	}
	if tunnel.Target.ClientID != "" {
		clients = append(clients, ActivityClientSubject{
			ClientID: tunnel.Target.ClientID, Relation: "target", Hostname: tunnel.Hostname,
		})
	}
	return ActivityEventSpec{
		OccurredAt: time.Now().UTC(),
		Category:   ActivityCategoryTunnel,
		Action:     action,
		Source:     "server",
		Actor:      actor,
		Payload:    payload,
		Clients:    clients,
		Tunnels: []ActivityTunnelSubject{{
			TunnelID: tunnel.ID,
			Relation: "subject",
			Name:     tunnel.Name,
			Type:     tunnel.Type,
			Topology: tunnel.Topology,
		}},
	}
}

func tunnelTransitionActivitySpec(action string, before, after StoredTunnel, actor ActivityActor) ActivityEventSpec {
	spec := tunnelActivitySpec(action, after, actor)
	payload := newActivityTransitionPayload(ActivityCategoryTunnel, action, ActivitySummaryArgs{
		TunnelName: after.Name,
		Before:     before.DesiredState,
		After:      after.DesiredState,
		Topology:   after.Topology,
		Transport:  after.ActualTransport,
	}, before.DesiredState, after.DesiredState)
	if after.Revision > 0 {
		payload.Revision = uint64(after.Revision)
	}
	spec.Payload = payload
	return spec
}

func tunnelMigrationActivitySpec(before, after StoredTunnel, actor ActivityActor) ActivityEventSpec {
	spec := tunnelActivitySpec("migrated", after, actor)
	payload := newActivityTransitionPayload(ActivityCategoryTunnel, "migrated", ActivitySummaryArgs{
		TunnelName: after.Name,
		Before:     before.Target.ClientID,
		After:      after.Target.ClientID,
		Topology:   after.Topology,
	}, before.Target.ClientID, after.Target.ClientID)
	if after.Revision > 0 {
		payload.Revision = uint64(after.Revision)
	}
	spec.Payload = payload
	return spec
}

func (s *Server) publishActivityID(id int64) {
	s.ensureSharedStoreReferences()
	if id <= 0 || s.activityStore == nil || s.events == nil {
		return
	}
	item, err := s.activityStore.GetByID(id)
	if err != nil {
		log.Printf("⚠️ Failed to load committed activity event %d: %v", id, err)
		return
	}
	s.events.PublishJSON("activity_event", item)
}

func (s *Server) publishActivityIDs(ids ...int64) {
	for _, id := range ids {
		s.publishActivityID(id)
	}
}
