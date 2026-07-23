package server

import (
	"log"
	"strings"
	"time"

	"netsgo/pkg/protocol"
)

func runtimeActivityAction(before, after StoredTunnel) (string, string, bool) {
	beforeState := protocolRuntimeStateFromStorage(before.RuntimeState)
	afterState := protocolRuntimeStateFromStorage(after.RuntimeState)
	if beforeState == afterState && before.Error == after.Error && before.ActualTransport == after.ActualTransport {
		return "", "", false
	}
	if afterState == protocol.ProxyRuntimeStateError {
		return "runtime_error", runtimeActivityReason(after.Error), true
	}
	if beforeState == protocol.ProxyRuntimeStateError && afterState != protocol.ProxyRuntimeStateError {
		return "runtime_recovered", "", true
	}
	return "runtime_changed", "", true
}

func runtimeActivityReason(message string) string {
	message = strings.ToLower(message)
	switch {
	case strings.Contains(message, "restore"):
		return "restore_failed"
	case strings.Contains(message, "reconcile"), strings.Contains(message, "capability"), strings.Contains(message, "provision"):
		return "reconcile_failed"
	case message != "":
		return "start_failed"
	default:
		return "unknown"
	}
}

func tunnelRuntimeActivitySpec(before, after StoredTunnel) (ActivityEventSpec, bool) {
	action, reason, changed := runtimeActivityAction(before, after)
	if !changed {
		return ActivityEventSpec{}, false
	}
	payload := newActivityTransitionPayload(ActivityCategoryTunnel, action, ActivitySummaryArgs{
		TunnelName: after.Name,
		Before:     protocolRuntimeStateFromStorage(before.RuntimeState),
		After:      protocolRuntimeStateFromStorage(after.RuntimeState),
		Transport:  after.ActualTransport,
		Topology:   after.Topology,
	}, protocolRuntimeStateFromStorage(before.RuntimeState), protocolRuntimeStateFromStorage(after.RuntimeState))
	payload.ReasonCode = normalizeActivityReason(action, reason)
	if after.Revision > 0 {
		payload.Revision = uint64(after.Revision)
	}
	spec := tunnelActivitySpec(action, after, systemActivityActor())
	spec.OccurredAt = time.Now().UTC()
	spec.Payload = payload
	return spec, true
}

func (s *Server) appendTunnelRuntimeActivity(before, after StoredTunnel) int64 {
	s.ensureSharedStoreReferences()
	if s.activityStore == nil {
		return 0
	}
	spec, changed := tunnelRuntimeActivitySpec(before, after)
	if !changed {
		return 0
	}
	id, err := s.activityStore.Append(spec)
	if err != nil {
		log.Printf("⚠️ Failed to persist tunnel runtime activity [tunnel_id=%s revision=%d]: %v", after.ID, after.Revision, err)
		return 0
	}
	s.publishActivityID(id)
	return id
}

func (s *Server) updateStoredTunnelRuntimeObserved(stored StoredTunnel, runtimeState, message string) (bool, error) {
	if s.store == nil {
		return true, nil
	}
	before, ok, err := s.findStoredTunnelByID(stored.ID)
	if err != nil || !ok {
		return false, err
	}
	desired := stored.DesiredState
	if desired == "" {
		desired = protocol.ProxyDesiredStateRunning
	}
	ownerID := stored.OwnerClientID
	if ownerID == "" {
		ownerID = stored.ClientID
	}
	updated, err := s.store.UpdateStatesIfCurrent(ownerID, stored.ID, stored.Revision, desired, runtimeState, message)
	if err != nil || !updated {
		return updated, err
	}
	after, ok, err := s.findStoredTunnelByID(stored.ID)
	if err != nil || !ok {
		return true, err
	}
	s.appendTunnelRuntimeActivity(before, after)
	return true, nil
}

func (s *Server) transitionStoredTunnelRuntimeObserved(stored StoredTunnel, expectedRuntimeState, runtimeState, message string) (bool, error) {
	if s.store == nil {
		return true, nil
	}
	before, ok, err := s.findStoredTunnelByID(stored.ID)
	if err != nil || !ok {
		return false, err
	}
	desired := stored.DesiredState
	if desired == "" {
		desired = protocol.ProxyDesiredStateRunning
	}
	ownerID := stored.OwnerClientID
	if ownerID == "" {
		ownerID = stored.ClientID
	}
	updated, err := s.store.TransitionRuntimeStateIfCurrent(ownerID, stored.ID, stored.Revision, desired, expectedRuntimeState, runtimeState, message)
	if err != nil || !updated {
		return updated, err
	}
	after, ok, err := s.findStoredTunnelByID(stored.ID)
	if err != nil || !ok {
		return true, err
	}
	s.appendTunnelRuntimeActivity(before, after)
	return true, nil
}
