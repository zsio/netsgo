package server

import (
	"log"
	"time"
)

const (
	p2pProjectionRetryCapacity = 256
	p2pProjectionRetryBase     = time.Second
	p2pProjectionRetryMax      = 30 * time.Second
)

type p2pProjectionRetryItem struct {
	Result     p2pLifecycleResult
	Transition P2PProjectionTransition
	Expected   string
	Attempts   int
	Next       time.Time
}

func p2pProjectionRetryKey(result p2pLifecycleResult) string {
	action := ""
	switch {
	case result.ClosedEdge:
		action = "session_closed"
	case result.DetachedEdge:
		action = "tunnel_detached"
	case result.ReadyEdge:
		action = "connected"
	case result.FailedEdge:
		action = "failed"
	default:
		action = result.StatusState
	}
	return result.projectionKey(action)
}

func (s *Server) enqueueP2PProjectionRetry(item p2pProjectionRetryItem) bool {
	key := p2pProjectionRetryKey(item.Result)
	if key == "" {
		return false
	}
	s.p2pProjectionMu.Lock()
	if _, exists := s.p2pProjectionRetries[key]; !exists && len(s.p2pProjectionRetries) >= p2pProjectionRetryCapacity {
		s.p2pProjectionMu.Unlock()
		log.Printf("⚠️ P2P projection retry queue full; reconciling session %s", item.Result.Session.SessionID)
		s.reconcileP2PProjectionTargets(item.Result.Session.Grants)
		return false
	}
	if existing, exists := s.p2pProjectionRetries[key]; exists && existing.Attempts > item.Attempts {
		item.Attempts = existing.Attempts
	}
	item.Next = time.Now().Add(p2pProjectionRetryDelay(item.Attempts))
	s.p2pProjectionRetries[key] = item
	s.p2pProjectionMu.Unlock()
	select {
	case s.p2pProjectionWake <- struct{}{}:
	default:
	}
	return true
}

func p2pProjectionRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := p2pProjectionRetryBase << min(attempt-1, 5)
	if delay > p2pProjectionRetryMax {
		return p2pProjectionRetryMax
	}
	return delay
}

func (s *Server) p2pProjectionRetryLoop() {
	defer close(s.p2pProjectionDone)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.p2pProjectionStop:
			return
		case <-s.p2pProjectionWake:
			s.retryDueP2PProjections(time.Now())
		case now := <-ticker.C:
			s.retryDueP2PProjections(now)
		}
	}
}

func (s *Server) retryDueP2PProjections(now time.Time) {
	s.p2pProjectionMu.Lock()
	due := make(map[string]p2pProjectionRetryItem)
	for key, item := range s.p2pProjectionRetries {
		if !item.Next.After(now) {
			due[key] = item
		}
	}
	s.p2pProjectionMu.Unlock()
	for key, item := range due {
		item.Attempts++
		projection, err := s.store.ApplyP2PLifecycle(item.Result.Session.Grants, item.Expected, item.Transition)
		if err != nil {
			log.Printf("⚠️ P2P projection retry failed [%s]: %v", key, err)
			s.enqueueP2PProjectionRetry(item)
			continue
		}
		s.emitP2PProjectionChanges(projection.Changes)
		s.p2pProjectionMu.Lock()
		delete(s.p2pProjectionRetries, key)
		s.p2pProjectionMu.Unlock()
	}
}

func p2pClosedProjectionTransition(_ string) P2PProjectionTransition {
	return P2PProjectionTransition{Mode: P2PProjectionClosed}
}

func (s *Server) applyP2PLifecycleResult(result p2pLifecycleResult) {
	if s.store != nil && len(result.Session.Grants) > 0 && result.Transition.Mode != "" {
		if _, err := s.store.ApplyP2PLifecycle(result.Session.Grants, result.ExpectedSessionID, result.Transition); err != nil {
			queued := s.enqueueP2PProjectionRetry(p2pProjectionRetryItem{Result: result, Transition: result.Transition, Expected: result.ExpectedSessionID, Attempts: 1})
			log.Printf("⚠️ Failed to project P2P lifecycle [%s], queued=%v: %v", result.Session.SessionID, queued, err)
		}
	}
	s.appendP2PActivities(result)
	s.sendP2POutbounds(result.Outbounds)
	for _, grant := range result.Session.Grants {
		if refreshed, ok, _ := s.findStoredTunnelByID(grant.TunnelID); ok && refreshed.Revision == grant.Revision {
			s.emitTunnelChangedIfStored(refreshed.OwnerClientID, storedTunnelToProxyConfig(refreshed), "p2p_status")
		}
	}
}

func (s *Server) emitP2PProjectionChanges(changes []P2PProjectionChange) {
	for _, change := range changes {
		s.emitTunnelChangedIfStored(change.After.OwnerClientID, storedTunnelToProxyConfig(change.After), "p2p_status")
	}
}

func (s *Server) reconcileP2PProjectionTargets(grants []p2pGrantSnapshot) {
	for _, grant := range grants {
		if stored, ok, _ := s.findStoredTunnelByID(grant.TunnelID); ok && stored.Revision == grant.Revision {
			s.scheduleUnifiedTunnelReconcile(stored, "p2p_projection_retry_overflow")
		}
	}
}
