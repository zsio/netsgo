package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sort"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

const (
	p2pLeaseDuration       = 60 * time.Second
	p2pLeaseRenewEvery     = 20 * time.Second
	p2pCandidateWindow     = 10 * time.Second
	p2pCandidatesPerWindow = 64
	p2pFinalStatsGrace     = 15 * time.Second
)

type p2pCandidateRate struct {
	windowStart time.Time
	count       int
	total       int
}

type p2pGrantSpec struct {
	tunnelID, ingressClientID, targetClientID string
	revision                                  int64
	ingressGeneration, targetGeneration       uint64
	totalBPS                                  int64
}

type p2pGrant struct {
	sessionID, grantID, tunnelID    string
	revision                        int64
	ingressClientID, targetClientID string
	sequence                        uint64
	expiresAt                       time.Time
	totalBPS                        int64
}

func (g p2pGrant) forClient(clientID string) protocol.P2PTunnelGrant {
	localRole, peerRole := protocol.DataStreamRoleTarget, protocol.DataStreamRoleIngress
	if clientID == g.ingressClientID {
		localRole, peerRole = protocol.DataStreamRoleIngress, protocol.DataStreamRoleTarget
	}
	return protocol.P2PTunnelGrant{SessionID: g.sessionID, GrantID: g.grantID, TunnelID: g.tunnelID, Revision: g.revision, LocalRole: localRole, PeerRole: peerRole, LeaseSequence: g.sequence, ExpiresAt: g.expiresAt, TotalBPS: g.totalBPS}
}

type p2pPairSession struct {
	id                       string
	clientA, clientB         string
	generationA, generationB uint64
	leaseSequence            uint64
	expiresAt                time.Time
	grants                   map[string]p2pGrant
	lastSignal               map[string]uint64
	lastStatus               map[string]uint64
	candidates               map[string]p2pCandidateRate
	ready                    map[string]bool
	stats                    map[string]p2pStatsCursor
	creditDemand             map[string]p2pCreditCursor
	creditGrant              map[string]p2pCreditCursor
}

type p2pStatsCursor struct {
	epoch                     string
	sequence, ingress, egress uint64
}
type p2pCreditCursor struct{ sequence, cumulative uint64 }

type p2pClosedStatsGrant struct {
	grant            p2pGrant
	targetGeneration uint64
	cursor           p2pStatsCursor
	expiresAt        time.Time
}

type p2pOutbound struct {
	clientID    string
	messageType string
	payload     any
}

type p2pCoordinator struct {
	mu          sync.Mutex
	now         func() time.Time
	byPair      map[string]*p2pPairSession
	byID        map[string]*p2pPairSession
	closedStats map[string]p2pClosedStatsGrant
}

func newP2PCoordinator(now func() time.Time) *p2pCoordinator {
	if now == nil {
		now = time.Now
	}
	return &p2pCoordinator{now: now, byPair: make(map[string]*p2pPairSession), byID: make(map[string]*p2pPairSession), closedStats: make(map[string]p2pClosedStatsGrant)}
}

func (c *p2pCoordinator) ensureGrant(spec p2pGrantSpec) (p2pGrant, p2pLifecycleResult, error) {
	if spec.tunnelID == "" || spec.revision <= 0 || spec.ingressClientID == "" || spec.targetClientID == "" || spec.ingressClientID == spec.targetClientID {
		return p2pGrant{}, p2pLifecycleResult{}, fmt.Errorf("invalid p2p grant spec")
	}
	if spec.ingressGeneration == 0 || spec.targetGeneration == 0 {
		return p2pGrant{}, p2pLifecycleResult{}, fmt.Errorf("p2p client generations must be positive")
	}
	if spec.totalBPS < 0 {
		return p2pGrant{}, p2pLifecycleResult{}, fmt.Errorf("p2p total_bps must be non-negative")
	}
	a, b, genA, genB := normalizeP2PPair(spec)
	key := a + "\x00" + b
	c.mu.Lock()
	defer c.mu.Unlock()
	session := c.byPair[key]
	createdSession := false
	if session != nil && (session.generationA != genA || session.generationB != genB) {
		c.deleteSessionLocked(session)
		session = nil
	}
	if session == nil {
		createdSession = true
		id, err := newP2PID()
		if err != nil {
			return p2pGrant{}, p2pLifecycleResult{}, err
		}
		session = &p2pPairSession{id: id, clientA: a, clientB: b, generationA: genA, generationB: genB, leaseSequence: 1, expiresAt: c.now().Add(p2pLeaseDuration), grants: make(map[string]p2pGrant), lastSignal: make(map[string]uint64), lastStatus: make(map[string]uint64), candidates: make(map[string]p2pCandidateRate), ready: make(map[string]bool), stats: make(map[string]p2pStatsCursor), creditDemand: make(map[string]p2pCreditCursor), creditGrant: make(map[string]p2pCreditCursor)}
		c.byPair[key], c.byID[id] = session, session
	}
	if current, ok := session.grants[spec.tunnelID]; ok && current.revision == spec.revision {
		return current, p2pLifecycleResult{Session: snapshotP2PSession(session), Grant: snapshotP2PGrant(current), HasGrant: true}, nil
	}
	grantID, err := newP2PID()
	if err != nil {
		return p2pGrant{}, p2pLifecycleResult{}, err
	}
	grant := p2pGrant{sessionID: session.id, grantID: grantID, tunnelID: spec.tunnelID, revision: spec.revision, ingressClientID: spec.ingressClientID, targetClientID: spec.targetClientID, sequence: 1, expiresAt: c.now().Add(p2pLeaseDuration), totalBPS: spec.totalBPS}
	if !createdSession {
		session.leaseSequence++
		session.expiresAt = c.now().Add(p2pLeaseDuration)
		grant.expiresAt = session.expiresAt
	}
	session.grants[spec.tunnelID] = grant
	return grant, p2pLifecycleResult{Session: snapshotP2PSession(session), Grant: snapshotP2PGrant(grant), HasGrant: true, SessionCreated: createdSession, GrantCreated: true, Sequence: session.leaseSequence}, nil
}

func normalizeP2PPair(spec p2pGrantSpec) (string, string, uint64, uint64) {
	if spec.ingressClientID < spec.targetClientID {
		return spec.ingressClientID, spec.targetClientID, spec.ingressGeneration, spec.targetGeneration
	}
	return spec.targetClientID, spec.ingressClientID, spec.targetGeneration, spec.ingressGeneration
}

func (c *p2pCoordinator) authorizeSignal(clientID string, generation uint64, signal protocol.P2PSignal) (string, error) {
	if err := signal.Validate(); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.byID[signal.SessionID]
	if s == nil || !s.expiresAt.After(c.now()) {
		return "", fmt.Errorf("unknown or expired p2p session")
	}
	peer := ""
	if clientID == s.clientA && generation == s.generationA {
		peer = s.clientB
	} else if clientID == s.clientB && generation == s.generationB {
		peer = s.clientA
	} else {
		return "", fmt.Errorf("p2p signal sender does not match current session generation")
	}
	if signal.Sequence <= s.lastSignal[clientID] {
		return "", fmt.Errorf("stale p2p signal sequence")
	}
	if (signal.Kind == protocol.P2PSignalOffer && clientID != s.clientA) || (signal.Kind == protocol.P2PSignalAnswer && clientID != s.clientB) {
		return "", fmt.Errorf("p2p description sender role mismatch")
	}
	if signal.Kind == protocol.P2PSignalCandidate {
		now := c.now()
		rate := s.candidates[clientID]
		if rate.windowStart.IsZero() || now.Sub(rate.windowStart) >= p2pCandidateWindow {
			rate.windowStart, rate.count = now, 0
		}
		if rate.total >= protocol.P2PMaxCandidates {
			return "", fmt.Errorf("p2p candidate limit exceeded")
		}
		if rate.count >= p2pCandidatesPerWindow {
			return "", fmt.Errorf("p2p candidate rate limit exceeded")
		}
		rate.count++
		rate.total++
		s.candidates[clientID] = rate
	}
	s.lastSignal[clientID] = signal.Sequence
	return peer, nil
}

func (c *p2pCoordinator) prepareMessages(sessionID string) ([]p2pOutbound, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.byID[sessionID]
	if s == nil {
		return nil, fmt.Errorf("unknown p2p session")
	}
	return c.prepareMessagesLocked(s), nil
}

func (c *p2pCoordinator) prepareMessagesLocked(s *p2pPairSession) []p2pOutbound {
	forClient := func(clientID, peerID, role string, localGen, peerGen uint64) protocol.P2PSessionPrepare {
		grants := make([]protocol.P2PTunnelGrant, 0, len(s.grants))
		for _, grant := range s.grants {
			grants = append(grants, grant.forClient(clientID))
		}
		sort.Slice(grants, func(i, j int) bool { return grants[i].TunnelID < grants[j].TunnelID })
		return protocol.P2PSessionPrepare{SessionID: s.id, PeerClientID: peerID, Role: role, LocalGeneration: localGen, PeerGeneration: peerGen, LeaseSequence: s.leaseSequence, ExpiresAt: s.expiresAt, Grants: grants}
	}
	return []p2pOutbound{
		{clientID: s.clientA, messageType: protocol.MsgTypeP2PSessionPrepare, payload: forClient(s.clientA, s.clientB, protocol.P2PRoleOfferer, s.generationA, s.generationB)},
		{clientID: s.clientB, messageType: protocol.MsgTypeP2PSessionPrepare, payload: forClient(s.clientB, s.clientA, protocol.P2PRoleAnswerer, s.generationB, s.generationA)},
	}
}

func (c *p2pCoordinator) renew(healthy func(string, uint64) bool) p2pRenewResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.expireClosedStatsLocked(now)
	var result p2pRenewResult
	for _, s := range c.byID {
		if healthy != nil && (!healthy(s.clientA, s.generationA) || !healthy(s.clientB, s.generationB)) {
			result.Closed = append(result.Closed, p2pLifecycleResult{
				Session: snapshotP2PSession(s), ClosedEdge: true, StatusState: protocol.P2PStateClosed,
				ReasonCode: "lease_unhealthy", Sequence: s.leaseSequence + 1,
				Outbounds:  p2pClosedOutbounds(s, "lease_unhealthy"),
				Transition: p2pClosedProjectionTransition("lease_unhealthy"), ExpectedSessionID: s.id,
			})
			c.deleteSessionLocked(s)
			continue
		}
		if !s.expiresAt.After(now) {
			result.Closed = append(result.Closed, p2pLifecycleResult{
				Session: snapshotP2PSession(s), ClosedEdge: true, StatusState: protocol.P2PStateClosed,
				ReasonCode: "lease_expired", Sequence: s.leaseSequence + 1,
				Outbounds:  p2pClosedOutbounds(s, "lease_expired"),
				Transition: p2pClosedProjectionTransition("lease_expired"), ExpectedSessionID: s.id,
			})
			c.deleteSessionLocked(s)
			continue
		}
		s.leaseSequence++
		s.expiresAt = now.Add(p2pLeaseDuration)
		lease := protocol.P2PLease{SessionID: s.id, Sequence: s.leaseSequence, ExpiresAt: s.expiresAt}
		result.Outbounds = append(result.Outbounds, p2pOutbound{clientID: s.clientA, messageType: protocol.MsgTypeP2PLease, payload: lease}, p2pOutbound{clientID: s.clientB, messageType: protocol.MsgTypeP2PLease, payload: lease})
		for tunnelID, grant := range s.grants {
			grant.sequence++
			grant.expiresAt = s.expiresAt
			s.grants[tunnelID] = grant
			result.Outbounds = append(result.Outbounds, p2pOutbound{clientID: s.clientA, messageType: protocol.MsgTypeP2PTunnelGrant, payload: grant.forClient(s.clientA)}, p2pOutbound{clientID: s.clientB, messageType: protocol.MsgTypeP2PTunnelGrant, payload: grant.forClient(s.clientB)})
		}
	}
	return result
}

func (c *p2pCoordinator) closeClient(clientID string, generation uint64, reason string) []p2pLifecycleResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	var results []p2pLifecycleResult
	for _, s := range c.byID {
		if (s.clientA != clientID || s.generationA != generation) && (s.clientB != clientID || s.generationB != generation) {
			continue
		}
		sequence := s.leaseSequence + 1
		outbounds := p2pClosedOutbounds(s, reason)
		if s.clientA == clientID {
			outbounds = outbounds[1:]
		} else {
			outbounds = outbounds[:1]
		}
		results = append(results, p2pLifecycleResult{
			Session: snapshotP2PSession(s), ClosedEdge: true, StatusState: protocol.P2PStateClosed,
			ReasonCode: normalizeP2PCloseReason(reason, protocol.P2PStateClosed), Sequence: sequence,
			Transition: p2pClosedProjectionTransition(reason), ExpectedSessionID: s.id,
			Outbounds: outbounds,
		})
		c.deleteSessionLocked(s)
	}
	return results
}

func (c *p2pCoordinator) closeSession(sessionID, reason string) p2pLifecycleResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.byID[sessionID]
	if s == nil {
		return p2pLifecycleResult{}
	}
	result := p2pLifecycleResult{
		Session: snapshotP2PSession(s), ClosedEdge: true, StatusState: protocol.P2PStateClosed,
		ReasonCode: normalizeP2PCloseReason(reason, protocol.P2PStateClosed), Sequence: s.leaseSequence + 1,
		Transition: p2pClosedProjectionTransition(reason), ExpectedSessionID: s.id,
		Outbounds: p2pClosedOutbounds(s, reason),
	}
	c.deleteSessionLocked(s)
	return result
}

func (c *p2pCoordinator) recordReady(clientID string, generation uint64, status protocol.P2PSessionStatus) (p2pLifecycleResult, error) {
	if err := status.Validate(); err != nil {
		return p2pLifecycleResult{}, err
	}
	if status.State == protocol.P2PStateClosed {
		return p2pLifecycleResult{}, fmt.Errorf("client cannot report closed p2p status")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.byID[status.SessionID]
	if s == nil || !s.expiresAt.After(c.now()) {
		return p2pLifecycleResult{}, fmt.Errorf("unknown or expired p2p session")
	}
	if (clientID != s.clientA || generation != s.generationA) && (clientID != s.clientB || generation != s.generationB) {
		return p2pLifecycleResult{}, fmt.Errorf("p2p status sender mismatch")
	}
	if status.Sequence <= s.lastStatus[clientID] {
		return p2pLifecycleResult{}, fmt.Errorf("stale p2p status sequence")
	}
	wasReady := s.ready[s.clientA] && s.ready[s.clientB]
	s.lastStatus[clientID] = status.Sequence
	s.ready[clientID] = status.State == protocol.P2PStateConnected
	isReady := s.ready[s.clientA] && s.ready[s.clientB]
	return p2pLifecycleResult{
		Session: snapshotP2PSession(s), ReportAccepted: true, ReadyEdge: !wasReady && isReady,
		FailedEdge: status.State == protocol.P2PStateFailed, StatusState: status.State,
		ReasonCode: normalizeP2PCloseReason(status.Error, status.State), Sequence: status.Sequence,
	}, nil
}

func (c *p2pCoordinator) acceptStats(clientID string, generation uint64, report protocol.P2PStatsReport) (uint64, uint64, error) {
	if err := report.Validate(); err != nil {
		return 0, 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.byID[report.SessionID]
	if s == nil || !s.expiresAt.After(c.now()) {
		return c.acceptClosedStatsLocked(clientID, generation, report)
	}
	grant, ok := s.grants[report.TunnelID]
	if !ok || grant.grantID != report.GrantID || grant.revision != report.Revision || !grant.expiresAt.After(c.now()) {
		return c.acceptClosedStatsLocked(clientID, generation, report)
	}
	targetGeneration := s.generationA
	if grant.targetClientID == s.clientB {
		targetGeneration = s.generationB
	}
	if clientID != grant.targetClientID || generation != targetGeneration {
		return 0, 0, fmt.Errorf("p2p stats sender is not tunnel owner")
	}
	cursor := s.stats[grant.grantID]
	if cursor.sequence != 0 && cursor.epoch != report.Epoch {
		return 0, 0, fmt.Errorf("p2p stats epoch changed")
	}
	if report.Sequence <= cursor.sequence {
		return 0, 0, fmt.Errorf("stale p2p stats sequence")
	}
	if report.IngressBytes < cursor.ingress || report.EgressBytes < cursor.egress {
		return 0, 0, fmt.Errorf("p2p cumulative counters decreased")
	}
	ingress, egress := report.IngressBytes-cursor.ingress, report.EgressBytes-cursor.egress
	s.stats[grant.grantID] = p2pStatsCursor{epoch: report.Epoch, sequence: report.Sequence, ingress: report.IngressBytes, egress: report.EgressBytes}
	return ingress, egress, nil
}

func (c *p2pCoordinator) acceptClosedStatsLocked(clientID string, generation uint64, report protocol.P2PStatsReport) (uint64, uint64, error) {
	closed, ok := c.closedStats[report.GrantID]
	if !ok || !closed.expiresAt.After(c.now()) || closed.grant.sessionID != report.SessionID || closed.grant.tunnelID != report.TunnelID || closed.grant.revision != report.Revision {
		return 0, 0, fmt.Errorf("unknown or expired p2p session")
	}
	if clientID != closed.grant.targetClientID || generation != closed.targetGeneration {
		return 0, 0, fmt.Errorf("p2p final stats sender is not tunnel owner")
	}
	cursor := closed.cursor
	if cursor.sequence != 0 && cursor.epoch != report.Epoch {
		return 0, 0, fmt.Errorf("p2p final stats epoch changed")
	}
	if report.Sequence <= cursor.sequence || report.IngressBytes < cursor.ingress || report.EgressBytes < cursor.egress {
		return 0, 0, fmt.Errorf("stale p2p final stats")
	}
	ingress, egress := report.IngressBytes-cursor.ingress, report.EgressBytes-cursor.egress
	closed.cursor = p2pStatsCursor{epoch: report.Epoch, sequence: report.Sequence, ingress: report.IngressBytes, egress: report.EgressBytes}
	c.closedStats[report.GrantID] = closed
	return ingress, egress, nil
}

func (c *p2pCoordinator) authorizeCreditDemand(clientID string, generation uint64, demand protocol.P2PCreditDemand) (string, error) {
	if err := demand.Validate(); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, grant, err := c.creditGrantLocked(demand.SessionID, demand.GrantID, demand.TunnelID, demand.Revision)
	if err != nil {
		return "", err
	}
	if clientID != grant.ingressClientID || !c.matchesGenerationLocked(s, clientID, generation) {
		return "", fmt.Errorf("p2p credit demand sender is not ingress")
	}
	cursor := s.creditDemand[grant.grantID]
	if demand.Sequence <= cursor.sequence || demand.DesiredBytes < cursor.cumulative {
		return "", fmt.Errorf("stale p2p credit demand")
	}
	s.creditDemand[grant.grantID] = p2pCreditCursor{sequence: demand.Sequence, cumulative: demand.DesiredBytes}
	return grant.targetClientID, nil
}

func (c *p2pCoordinator) authorizeCreditGrant(clientID string, generation uint64, credit protocol.P2PCreditGrant) (string, error) {
	if err := credit.Validate(); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, grant, err := c.creditGrantLocked(credit.SessionID, credit.GrantID, credit.TunnelID, credit.Revision)
	if err != nil {
		return "", err
	}
	if clientID != grant.targetClientID || !c.matchesGenerationLocked(s, clientID, generation) {
		return "", fmt.Errorf("p2p credit grant sender is not owner")
	}
	cursor := s.creditGrant[grant.grantID]
	if credit.Sequence <= cursor.sequence || credit.GrantedBytes < cursor.cumulative || credit.GrantedBytes > s.creditDemand[grant.grantID].cumulative {
		return "", fmt.Errorf("invalid or stale p2p credit grant")
	}
	s.creditGrant[grant.grantID] = p2pCreditCursor{sequence: credit.Sequence, cumulative: credit.GrantedBytes}
	return grant.ingressClientID, nil
}

func (c *p2pCoordinator) creditGrantLocked(sessionID, grantID, tunnelID string, revision int64) (*p2pPairSession, p2pGrant, error) {
	s := c.byID[sessionID]
	if s == nil || !s.expiresAt.After(c.now()) {
		return nil, p2pGrant{}, fmt.Errorf("unknown or expired p2p session")
	}
	grant, ok := s.grants[tunnelID]
	if !ok || grant.grantID != grantID || grant.revision != revision || grant.totalBPS <= 0 || !grant.expiresAt.After(c.now()) {
		return nil, p2pGrant{}, fmt.Errorf("unknown, unlimited, or expired p2p grant")
	}
	return s, grant, nil
}

func (c *p2pCoordinator) matchesGenerationLocked(s *p2pPairSession, clientID string, generation uint64) bool {
	return (s.clientA == clientID && s.generationA == generation) || (s.clientB == clientID && s.generationB == generation)
}

func (c *p2pCoordinator) revokeTunnel(tunnelID string, revision int64, reason string) p2pLifecycleResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	var session *p2pPairSession
	var grant p2pGrant
	for _, candidate := range c.byID {
		if g, ok := candidate.grants[tunnelID]; ok && (revision <= 0 || g.revision <= revision) {
			session, grant = candidate, g
			break
		}
	}
	if session == nil {
		return p2pLifecycleResult{}
	}
	before := snapshotP2PSession(session)
	delete(session.grants, tunnelID)
	c.archiveGrantLocked(session, grant)
	revoke := protocol.P2PTunnelRevoke{SessionID: session.id, GrantID: grant.grantID, TunnelID: grant.tunnelID, Revision: grant.revision, Reason: reason}
	outbounds := []p2pOutbound{{clientID: session.clientA, messageType: protocol.MsgTypeP2PTunnelRevoke, payload: revoke}, {clientID: session.clientB, messageType: protocol.MsgTypeP2PTunnelRevoke, payload: revoke}}
	closed := len(session.grants) == 0
	sequence := session.leaseSequence + 1
	if closed {
		outbounds = append(outbounds, p2pClosedOutbounds(session, reason)...)
		c.deleteSessionLocked(session)
	}
	return p2pLifecycleResult{
		Session: before, Grant: snapshotP2PGrant(grant), HasGrant: true,
		DetachedEdge: true, ClosedEdge: closed, StatusState: protocol.P2PStateClosed,
		ReasonCode: normalizeP2PCloseReason(reason, protocol.P2PStateClosed), Sequence: sequence,
		Transition: p2pClosedProjectionTransition(reason), ExpectedSessionID: session.id,
		Outbounds: outbounds,
	}
}

func (c *p2pCoordinator) expire() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.expireClosedStatsLocked(now)
	var expired []string
	for _, session := range c.byID {
		if !session.expiresAt.After(now) {
			expired = append(expired, session.id)
			c.deleteSessionLocked(session)
		}
	}
	sort.Strings(expired)
	return expired
}

func (c *p2pCoordinator) deleteSessionLocked(session *p2pPairSession) {
	for _, grant := range session.grants {
		c.archiveGrantLocked(session, grant)
	}
	delete(c.byID, session.id)
	delete(c.byPair, session.clientA+"\x00"+session.clientB)
}

func (c *p2pCoordinator) archiveGrantLocked(session *p2pPairSession, grant p2pGrant) {
	targetGeneration := session.generationA
	if grant.targetClientID == session.clientB {
		targetGeneration = session.generationB
	}
	c.closedStats[grant.grantID] = p2pClosedStatsGrant{
		grant:            grant,
		targetGeneration: targetGeneration,
		cursor:           session.stats[grant.grantID],
		expiresAt:        c.now().Add(p2pFinalStatsGrace),
	}
}

func (c *p2pCoordinator) expireClosedStatsLocked(now time.Time) {
	for grantID, closed := range c.closedStats {
		if !closed.expiresAt.After(now) {
			delete(c.closedStats, grantID)
		}
	}
}
func (c *p2pCoordinator) sessionCount() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.byID) }
func (c *p2pCoordinator) session(id string) (*p2pPairSession, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.byID[id]
	return s, ok
}

func (c *p2pCoordinator) sessionReady(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.byID[sessionID]
	return s != nil && s.expiresAt.After(c.now()) && s.ready[s.clientA] && s.ready[s.clientB]
}

func (c *p2pCoordinator) statsForTunnel(sessionID, tunnelID string) (uint64, uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.byID[sessionID]
	if s == nil {
		return 0, 0, false
	}
	grant, ok := s.grants[tunnelID]
	if !ok {
		return 0, 0, false
	}
	cursor, ok := s.stats[grant.grantID]
	return cursor.ingress, cursor.egress, ok
}

func newP2PID() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
