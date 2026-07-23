package server

import (
	"log"
	"math/rand/v2"
	"sort"
	"time"

	"netsgo/pkg/protocol"
)

type p2pRetryState struct {
	failures int
	next     time.Time
}

func clientSupportsP2P(client *ClientConn) bool {
	if client == nil {
		return false
	}
	caps := client.GetInfo().Capabilities
	return caps != nil && caps.P2P.Supported && caps.P2P.Impl == protocol.P2PImplWebRTCICE
}

func (s *Server) ensureP2PForTunnel(stored StoredTunnel, ingress, target *ClientConn) error {
	if stored.TransportPolicy == protocol.TransportPolicyServerRelayOnly || !clientSupportsP2P(ingress) || !clientSupportsP2P(target) {
		return nil
	}
	if !s.p2pRetryAllowed(ingress.ID, target.ID) {
		return nil
	}
	grant, lifecycle, err := s.p2p.ensureGrant(p2pGrantSpec{tunnelID: stored.ID, revision: stored.Revision, ingressClientID: ingress.ID, targetClientID: target.ID, ingressGeneration: ingress.generation, targetGeneration: target.generation, totalBPS: stored.TotalBPS})
	if err != nil {
		return err
	}
	if !lifecycle.GrantCreated {
		return nil
	}
	messages, err := s.p2p.prepareMessages(grant.sessionID)
	if err != nil {
		return err
	}
	lifecycle.Outbounds = messages
	lifecycle.Transition = P2PProjectionTransition{Mode: P2PProjectionGathering, SessionID: grant.sessionID}
	if s.p2p.sessionReady(grant.sessionID) {
		lifecycle.Transition.Mode = P2PProjectionReady
	}
	s.sendP2PLifecycleResult(lifecycle)
	return nil
}

func (s *Server) sendP2POutbounds(messages []p2pOutbound) {
	for _, outbound := range messages {
		client, ok := s.loadLiveClient(outbound.clientID)
		if !ok {
			continue
		}
		msg, err := protocol.NewMessage(outbound.messageType, outbound.payload)
		if err != nil {
			continue
		}
		if err := s.writeControlMessage(client, msg); err != nil {
			log.Printf("⚠️ send P2P control message failed [%s]: %v", outbound.clientID, err)
		}
	}
}
func (s *Server) sendP2PLifecycleResults(results []p2pLifecycleResult) {
	for _, result := range results {
		s.sendP2PLifecycleResult(result)
	}
}

func (s *Server) sendP2PLifecycleResult(result p2pLifecycleResult) {
	s.applyP2PLifecycleResult(result)
}

func (s *Server) p2pLeaseLoop() {
	ticker := time.NewTicker(p2pLeaseRenewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			result := s.p2p.renew(func(clientID string, generation uint64) bool { return s.isCurrentLive(clientID, generation) })
			s.sendP2PLifecycleResults(result.Closed)
			s.sendP2POutbounds(result.Outbounds)
		case <-s.done:
			return
		}
	}
}

func (s *Server) handleP2PSignalMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}
	var signal protocol.P2PSignal
	if err := msg.ParsePayload(&signal); err != nil {
		return
	}
	peerID, err := s.p2p.authorizeSignal(client.ID, client.generation, signal)
	if err != nil {
		log.Printf("⚠️ rejected P2P signal [%s]: %v", client.ID, err)
		return
	}
	peer, ok := s.loadLiveClient(peerID)
	if !ok {
		return
	}
	if s.p2pSignalDropHook != nil && s.p2pSignalDropHook(client.ID, peerID, signal) {
		return
	}
	forward, err := protocol.NewMessage(protocol.MsgTypeP2PSignal, signal)
	if err == nil {
		_ = s.writeControlMessage(peer, forward)
	}
}

func closeP2PAfterFailedStatus(result p2pLifecycleResult) p2pLifecycleResult {
	result.Transition = P2PProjectionTransition{}
	return result
}

func (s *Server) handleP2PStatusMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}
	var status protocol.P2PSessionStatus
	if err := msg.ParsePayload(&status); err != nil {
		return
	}
	lifecycle, err := s.p2p.recordReady(client.ID, client.generation, status)
	if err != nil {
		return
	}
	ready := lifecycle.Session.Ready
	if lifecycle.ReadyEdge {
		log.Printf("🔗 P2P pair ready: session=%s", status.SessionID)
	}
	tunnelIDs := make([]string, 0, len(lifecycle.Session.Grants))
	for _, grant := range lifecycle.Session.Grants {
		tunnelIDs = append(tunnelIDs, grant.TunnelID)
	}
	mode := P2PProjectionGathering
	if ready {
		mode = P2PProjectionReady
	} else if status.State == protocol.P2PStateFailed {
		mode = P2PProjectionFailed
	}
	if len(tunnelIDs) > 0 {
		lifecycle.Transition = P2PProjectionTransition{Mode: mode, SessionID: status.SessionID}
	}
	lifecycle.ExpectedSessionID = status.SessionID
	if lifecycle.FailedEdge {
		lifecycle.ActivityActions = make(map[string][]p2pGrantSnapshot, 2)
		for _, grant := range lifecycle.Session.Grants {
			action := "failed"
			if stored, ok, _ := s.findStoredTunnelByID(grant.TunnelID); ok && stored.Revision == grant.Revision && stored.TransportPolicy == protocol.TransportPolicyDirectPreferred {
				action = "fallback"
			}
			lifecycle.ActivityActions[action] = append(lifecycle.ActivityActions[action], grant)
		}
	}
	s.sendP2PLifecycleResult(lifecycle)
	if ready {
		if len(tunnelIDs) > 0 {
			if stored, ok, _ := s.findStoredTunnelByID(tunnelIDs[0]); ok {
				s.resetP2PRetry(stored.Ingress.ClientID, stored.Target.ClientID)
			}
		}
	} else if status.State == protocol.P2PStateFailed {
		closed := closeP2PAfterFailedStatus(s.p2p.closeSession(status.SessionID, status.Error))
		s.sendP2PLifecycleResult(closed)
		s.scheduleP2PRetry(tunnelIDs)
	}
}

func p2pPairRetryKey(a, b string) string {
	pair := []string{a, b}
	sort.Strings(pair)
	return pair[0] + "\x00" + pair[1]
}
func (s *Server) p2pRetryAllowed(a, b string) bool {
	s.p2pRetryMu.Lock()
	defer s.p2pRetryMu.Unlock()
	state, ok := s.p2pRetries[p2pPairRetryKey(a, b)]
	return !ok || !state.next.After(time.Now())
}
func (s *Server) resetP2PRetry(a, b string) {
	s.p2pRetryMu.Lock()
	delete(s.p2pRetries, p2pPairRetryKey(a, b))
	s.p2pRetryMu.Unlock()
}
func (s *Server) scheduleP2PRetry(tunnelIDs []string) {
	if len(tunnelIDs) == 0 {
		return
	}
	stored, ok, _ := s.findStoredTunnelByID(tunnelIDs[0])
	if !ok {
		return
	}
	key := p2pPairRetryKey(stored.Ingress.ClientID, stored.Target.ClientID)
	s.p2pRetryMu.Lock()
	state := s.p2pRetries[key]
	state.failures++
	delay := 10 * time.Second
	if state.failures == 2 {
		delay = 30 * time.Second
	}
	if state.failures >= 3 {
		delay = time.Minute + time.Duration(rand.IntN(20001)-10000)*time.Millisecond
	}
	state.next = time.Now().Add(delay)
	s.p2pRetries[key] = state
	s.p2pRetryMu.Unlock()
	go func(ids []string, wait time.Duration) {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
			for _, id := range ids {
				if current, ok, _ := s.findStoredTunnelByID(id); ok {
					s.scheduleUnifiedTunnelReconcile(current, "p2p_retry")
				}
			}
		case <-s.done:
		}
	}(append([]string(nil), tunnelIDs...), delay)
}

func (s *Server) handleP2PStatsMessage(client *ClientConn, msg protocol.Message) {
	// A graceful disconnect can mark the logical session Closing before this
	// queued final report is read. acceptStats still authorizes the archived
	// grant against the authenticated connection's exact client generation.
	var report protocol.P2PStatsReport
	if err := msg.ParsePayload(&report); err != nil {
		return
	}
	ingress, egress, err := s.p2p.acceptStats(client.ID, client.generation, report)
	if err != nil {
		log.Printf("⚠️ rejected P2P traffic report [%s]: %v", client.ID, err)
		return
	}
	if ingress == 0 && egress == 0 {
		return
	}
	stored, ok, err := s.findStoredTunnelByID(report.TunnelID)
	ownerClientID := stored.OwnerClientID
	if ownerClientID == "" {
		ownerClientID = stored.Target.ClientID
	}
	if err != nil || !ok || stored.Revision != report.Revision || ownerClientID != client.ID {
		return
	}
	delta := trafficDeltaFromStoredTunnel(stored, ingress, egress)
	delta.Transport = protocol.ActualTransportPeerDirect
	s.recordTrafficDeltaAt(time.Now(), delta)
}

func (s *Server) handleP2PCreditDemandMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}
	var demand protocol.P2PCreditDemand
	if err := msg.ParsePayload(&demand); err != nil {
		return
	}
	peerID, err := s.p2p.authorizeCreditDemand(client.ID, client.generation, demand)
	if err != nil {
		return
	}
	s.forwardP2PControl(peerID, protocol.MsgTypeP2PCreditDemand, demand)
}

func (s *Server) handleP2PCreditGrantMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}
	var credit protocol.P2PCreditGrant
	if err := msg.ParsePayload(&credit); err != nil {
		return
	}
	peerID, err := s.p2p.authorizeCreditGrant(client.ID, client.generation, credit)
	if err != nil {
		return
	}
	s.forwardP2PControl(peerID, protocol.MsgTypeP2PCreditGrant, credit)
}

func (s *Server) forwardP2PControl(peerID, messageType string, payload any) {
	peer, ok := s.loadLiveClient(peerID)
	if !ok {
		return
	}
	forward, err := protocol.NewMessage(messageType, payload)
	if err == nil {
		_ = s.writeControlMessage(peer, forward)
	}
}
