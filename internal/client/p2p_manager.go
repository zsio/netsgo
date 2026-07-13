package client

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	peertransport "netsgo/pkg/p2p"
	"netsgo/pkg/protocol"
)

type peerDataSession interface {
	CreateOffer() (string, error)
	AcceptOffer(string) (string, error)
	AcceptAnswer(string) error
	AddCandidate(protocol.P2PSignal) error
	Ready() <-chan struct{}
	Terminated() <-chan struct{}
	Err() error
	Available() bool
	Open() (net.Conn, error)
	Accept() (net.Conn, error)
	Close() error
}

type clientPeerSession struct {
	id, peerClientID, role          string
	localGeneration, peerGeneration uint64
	leaseSequence                   uint64
	expiresAt                       time.Time
	peer                            peerDataSession
	signalMu                        sync.Mutex
	signalSequence                  atomic.Uint64
	statusSequence                  atomic.Uint64
	grants                          map[string]protocol.P2PTunnelGrant
}

const (
	p2pEarlySignalTTL      = 10 * time.Second
	p2pEarlySignalSessions = 64
)

type pendingP2PSignals struct {
	expiresAt time.Time
	signals   []protocol.P2PSignal
}

type clientPeerManager struct {
	client        *Client
	runtime       *sessionRuntime
	now           func() time.Time
	newSession    func(string, []protocol.P2PICEServer, peertransport.SignalHandler) (peerDataSession, error)
	mu            sync.RWMutex
	sessions      map[string]*clientPeerSession
	streams       map[string]map[*trackedPeerConn]struct{}
	traffic       map[string]*peerTrafficCounter
	remoteCredits map[string]*remoteCreditAccount
	ownerCredits  map[string]*ownerCreditScheduler
	earlySignals  map[string]pendingP2PSignals
	closed        chan struct{}
	closeOnce     sync.Once
}

func newClientPeerManager(client *Client, runtime *sessionRuntime) *clientPeerManager {
	m := &clientPeerManager{client: client, runtime: runtime, now: time.Now, sessions: make(map[string]*clientPeerSession), streams: make(map[string]map[*trackedPeerConn]struct{}), traffic: make(map[string]*peerTrafficCounter), remoteCredits: make(map[string]*remoteCreditAccount), ownerCredits: make(map[string]*ownerCreditScheduler), earlySignals: make(map[string]pendingP2PSignals), closed: make(chan struct{})}
	m.newSession = func(role string, servers []protocol.P2PICEServer, handler peertransport.SignalHandler) (peerDataSession, error) {
		return peertransport.NewSession(role, servers, handler)
	}
	go m.expiryLoop()
	go m.statsLoop()
	return m
}

func (m *clientPeerManager) handlePrepare(prepare protocol.P2PSessionPrepare) error {
	now := m.now()
	if err := prepare.Validate(now); err != nil {
		return err
	}
	if prepare.LocalGeneration == 0 {
		return fmt.Errorf("missing local generation")
	}
	m.mu.Lock()
	if current := m.sessions[prepare.SessionID]; current != nil {
		if current.peerClientID != prepare.PeerClientID || current.role != prepare.Role || current.localGeneration != prepare.LocalGeneration || current.peerGeneration != prepare.PeerGeneration {
			m.mu.Unlock()
			return fmt.Errorf("p2p session identity changed")
		}
		if prepare.LeaseSequence <= current.leaseSequence {
			m.mu.Unlock()
			return fmt.Errorf("stale p2p lease sequence")
		}
		current.leaseSequence, current.expiresAt = prepare.LeaseSequence, prepare.ExpiresAt
		for _, grant := range prepare.Grants {
			current.grants[grant.TunnelID] = grant
			m.ensureTrafficCounterLocked(grant)
			m.ensureCreditAccountLocked(grant)
		}
		m.mu.Unlock()
		return nil
	}
	sessionID := prepare.SessionID
	iceServers := prepare.ICEServers
	if len(iceServers) == 0 {
		iceServers = defaultP2PICEServers(m.client.ServerAddr)
	}
	peer, err := m.newSession(prepare.Role, iceServers, func(signal protocol.P2PSignal) {
		m.sendSignal(sessionID, signal)
	})
	if err != nil {
		m.mu.Unlock()
		return err
	}
	s := &clientPeerSession{id: sessionID, peerClientID: prepare.PeerClientID, role: prepare.Role, localGeneration: prepare.LocalGeneration, peerGeneration: prepare.PeerGeneration, leaseSequence: prepare.LeaseSequence, expiresAt: prepare.ExpiresAt, peer: peer, grants: make(map[string]protocol.P2PTunnelGrant)}
	for _, grant := range prepare.Grants {
		s.grants[grant.TunnelID] = grant
		m.ensureTrafficCounterLocked(grant)
		m.ensureCreditAccountLocked(grant)
	}
	m.sessions[sessionID] = s
	pending := m.earlySignals[sessionID]
	delete(m.earlySignals, sessionID)
	m.mu.Unlock()
	for _, signal := range pending.signals {
		if err := m.handleSignal(signal); err != nil {
			m.removeAndClose(sessionID)
			return fmt.Errorf("apply early p2p signal: %w", err)
		}
	}
	go m.watchSession(s)
	if prepare.Role == protocol.P2PRoleOfferer {
		offer, err := peer.CreateOffer()
		if err != nil {
			m.removeAndClose(sessionID)
			return err
		}
		m.sendSignal(sessionID, protocol.P2PSignal{Kind: protocol.P2PSignalOffer, SDP: offer})
	}
	return nil
}

func defaultP2PICEServers(serverAddr string) []protocol.P2PICEServer {
	u, err := url.Parse(serverAddr)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" || u.Scheme == "wss" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil
	}
	return []protocol.P2PICEServer{{URLs: []string{"stun:" + net.JoinHostPort(u.Hostname(), port)}}}
}

func (m *clientPeerManager) sendSignal(sessionID string, signal protocol.P2PSignal) {
	m.mu.RLock()
	session := m.sessions[sessionID]
	m.mu.RUnlock()
	if session == nil {
		return
	}
	session.signalMu.Lock()
	defer session.signalMu.Unlock()
	signal.SessionID = sessionID
	signal.Sequence = session.signalSequence.Add(1)
	msg, err := protocol.NewMessage(protocol.MsgTypeP2PSignal, signal)
	if err != nil {
		return
	}
	if err := m.runtime.writeJSON(msg); err != nil {
		log.Printf("⚠️ send P2P signal failed: %v", err)
	}
}

func (m *clientPeerManager) handleSignal(signal protocol.P2PSignal) error {
	if err := signal.Validate(); err != nil {
		return err
	}
	now := m.now()
	m.mu.Lock()
	session := m.sessions[signal.SessionID]
	if session == nil {
		pending, exists := m.earlySignals[signal.SessionID]
		if !exists && len(m.earlySignals) >= p2pEarlySignalSessions {
			m.mu.Unlock()
			return fmt.Errorf("too many early p2p signal sessions")
		}
		if !exists || !pending.expiresAt.After(now) {
			pending = pendingP2PSignals{expiresAt: now.Add(p2pEarlySignalTTL)}
		}
		if len(pending.signals) >= protocol.P2PMaxCandidates+2 {
			m.mu.Unlock()
			return fmt.Errorf("too many early p2p signals")
		}
		pending.signals = append(pending.signals, signal)
		m.earlySignals[signal.SessionID] = pending
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if !session.expiresAt.After(now) {
		return fmt.Errorf("unknown or expired p2p session")
	}
	switch signal.Kind {
	case protocol.P2PSignalOffer:
		if session.role != protocol.P2PRoleAnswerer {
			return fmt.Errorf("offer sent to non-answerer")
		}
		answer, err := session.peer.AcceptOffer(signal.SDP)
		if err != nil {
			return err
		}
		m.sendSignal(session.id, protocol.P2PSignal{Kind: protocol.P2PSignalAnswer, SDP: answer})
	case protocol.P2PSignalAnswer:
		if session.role != protocol.P2PRoleOfferer {
			return fmt.Errorf("answer sent to non-offerer")
		}
		return session.peer.AcceptAnswer(signal.SDP)
	case protocol.P2PSignalCandidate:
		return session.peer.AddCandidate(signal)
	}
	return nil
}

func (m *clientPeerManager) watchSession(session *clientPeerSession) {
	select {
	case <-session.peer.Ready():
		if session.peer.Available() {
			m.sendStatus(session.id, protocol.P2PStateConnected, "")
			go m.acceptLoop(session)
		} else {
			m.sendStatus(session.id, protocol.P2PStateFailed, "data channel unavailable")
			m.removeAndClose(session.id)
			return
		}
	case <-m.closed:
		return
	}
	select {
	case <-session.peer.Terminated():
		m.mu.RLock()
		current := m.sessions[session.id] == session
		m.mu.RUnlock()
		if !current {
			return
		}
		reason := "peer connection closed"
		if err := session.peer.Err(); err != nil {
			reason = err.Error()
		}
		m.reportSessionTraffic(session)
		m.sendStatus(session.id, protocol.P2PStateFailed, reason)
		m.removeAndClose(session.id)
	case <-m.closed:
		return
	}
}

func (m *clientPeerManager) reportSessionTraffic(session *clientPeerSession) {
	if session == nil {
		return
	}
	for _, grant := range session.grants {
		m.reportGrant(grant.GrantID)
	}
}

func (m *clientPeerManager) acceptLoop(session *clientPeerSession) {
	for session.peer.Available() {
		stream, err := session.peer.Accept()
		if err != nil {
			break
		}
		go m.client.handleDirectStream(m, session.id, stream)
	}
}

func (m *clientPeerManager) sendStatus(sessionID, state, message string) {
	m.mu.RLock()
	session := m.sessions[sessionID]
	m.mu.RUnlock()
	if session == nil {
		return
	}
	msg, _ := protocol.NewMessage(protocol.MsgTypeP2PSessionReady, protocol.P2PSessionStatus{SessionID: sessionID, Sequence: session.statusSequence.Add(1), State: state, Error: message})
	_ = m.runtime.writeJSON(msg)
}

func (m *clientPeerManager) handleLease(lease protocol.P2PLease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[lease.SessionID]
	if s == nil {
		return fmt.Errorf("unknown p2p session")
	}
	if lease.Sequence <= s.leaseSequence || !lease.ExpiresAt.After(m.now()) {
		return fmt.Errorf("stale or expired p2p lease")
	}
	s.leaseSequence, s.expiresAt = lease.Sequence, lease.ExpiresAt
	return nil
}

func (m *clientPeerManager) handleGrant(grant protocol.P2PTunnelGrant) error {
	if err := grant.Validate(m.now()); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[grant.SessionID]
	if s == nil {
		return fmt.Errorf("unknown p2p session")
	}
	if current, ok := s.grants[grant.TunnelID]; ok && grant.LeaseSequence <= current.LeaseSequence {
		return fmt.Errorf("stale p2p grant")
	}
	s.grants[grant.TunnelID] = grant
	m.ensureTrafficCounterLocked(grant)
	m.ensureCreditAccountLocked(grant)
	return nil
}

func (m *clientPeerManager) ensureCreditAccountLocked(grant protocol.P2PTunnelGrant) {
	if grant.TotalBPS <= 0 {
		return
	}
	if grant.LocalRole == protocol.DataStreamRoleIngress && m.remoteCredits[grant.GrantID] == nil {
		m.remoteCredits[grant.GrantID] = newRemoteCreditAccount(m, grant)
	}
	if grant.LocalRole == protocol.DataStreamRoleTarget && m.ownerCredits[grant.GrantID] == nil {
		m.ownerCredits[grant.GrantID] = newOwnerCreditScheduler(m, grant)
	}
}

func (m *clientPeerManager) handleRevoke(revoke protocol.P2PTunnelRevoke) {
	m.reportGrant(revoke.GrantID)
	m.mu.Lock()
	s := m.sessions[revoke.SessionID]
	var streams []*trackedPeerConn
	var remote *remoteCreditAccount
	var owner *ownerCreditScheduler
	if s != nil {
		if g, ok := s.grants[revoke.TunnelID]; ok && g.GrantID == revoke.GrantID && g.Revision <= revoke.Revision {
			delete(s.grants, revoke.TunnelID)
			for stream := range m.streams[g.GrantID] {
				streams = append(streams, stream)
			}
			delete(m.streams, g.GrantID)
			delete(m.traffic, g.GrantID)
			remote, owner = m.remoteCredits[g.GrantID], m.ownerCredits[g.GrantID]
			delete(m.remoteCredits, g.GrantID)
			delete(m.ownerCredits, g.GrantID)
		}
	}
	m.mu.Unlock()
	for _, stream := range streams {
		_ = stream.closeWithoutUnregister()
	}
	if remote != nil {
		remote.Close()
	}
	if owner != nil {
		owner.Close()
	}
}

func (m *clientPeerManager) sendCreditDemand(demand protocol.P2PCreditDemand) error {
	msg, err := protocol.NewMessage(protocol.MsgTypeP2PCreditDemand, demand)
	if err != nil {
		return err
	}
	return m.runtime.writeJSON(msg)
}
func (m *clientPeerManager) sendCreditGrant(grant protocol.P2PCreditGrant) error {
	msg, err := protocol.NewMessage(protocol.MsgTypeP2PCreditGrant, grant)
	if err != nil {
		return err
	}
	return m.runtime.writeJSON(msg)
}
func (m *clientPeerManager) handleCreditDemand(demand protocol.P2PCreditDemand) error {
	m.mu.RLock()
	scheduler := m.ownerCredits[demand.GrantID]
	m.mu.RUnlock()
	if scheduler == nil {
		return fmt.Errorf("unknown P2P owner credit scheduler")
	}
	return scheduler.ApplyDemand(demand)
}
func (m *clientPeerManager) handleCreditGrant(grant protocol.P2PCreditGrant) error {
	m.mu.RLock()
	account := m.remoteCredits[grant.GrantID]
	m.mu.RUnlock()
	if account == nil {
		return fmt.Errorf("unknown P2P remote credit account")
	}
	return account.Apply(grant)
}

func (m *clientPeerManager) limitConn(grantID string, conn net.Conn, frameAware bool, owner bool) net.Conn {
	m.mu.RLock()
	remote, scheduler := m.remoteCredits[grantID], m.ownerCredits[grantID]
	m.mu.RUnlock()
	if owner && scheduler != nil {
		return &creditLimitedConn{Conn: conn, reserve: scheduler.Reserve, frameAware: frameAware}
	}
	if !owner && remote != nil {
		return &creditLimitedConn{Conn: conn, reserve: remote.Reserve, frameAware: frameAware}
	}
	return conn
}

func (m *clientPeerManager) transportFor(req protocol.TunnelProvisionRequest) ingressStreamTransport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := m.now()
	for _, session := range m.sessions {
		grant, ok := session.grants[req.TunnelID]
		if ok && grant.Revision == req.Revision && grant.LocalRole == protocol.DataStreamRoleIngress && grant.ExpiresAt.After(now) && session.expiresAt.After(now) && session.peer.Available() {
			return peerDirectIngressTransport{manager: m, sessionID: session.id, grantID: grant.GrantID}
		}
	}
	return nil
}

func (m *clientPeerManager) authorizeIncoming(sessionID string, header protocol.DataStreamHeader) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.sessions[sessionID]
	if s == nil || !s.expiresAt.After(m.now()) {
		return false
	}
	g, ok := s.grants[header.TunnelID]
	return ok && g.GrantID == header.OpenToken && g.Revision == header.Revision && g.LocalRole == protocol.DataStreamRoleTarget && g.PeerRole == protocol.DataStreamRoleIngress && g.ExpiresAt.After(m.now()) && header.OpenClientID == s.peerClientID && header.SourceRole == protocol.DataStreamRoleIngress && header.TargetRole == protocol.DataStreamRoleTarget && header.Transport == protocol.ActualTransportPeerDirect
}

func (m *clientPeerManager) observeTraffic(grantID string) func(uint64, uint64) {
	return func(ingress, egress uint64) {
		m.mu.RLock()
		counter := m.traffic[grantID]
		m.mu.RUnlock()
		if counter == nil {
			return
		}
		counter.ingress.Add(ingress)
		counter.egress.Add(egress)
	}
}

func (m *clientPeerManager) ensureTrafficCounterLocked(grant protocol.P2PTunnelGrant) {
	if grant.LocalRole != protocol.DataStreamRoleTarget || m.traffic[grant.GrantID] != nil {
		return
	}
	m.traffic[grant.GrantID] = &peerTrafficCounter{sessionID: grant.SessionID, grantID: grant.GrantID, tunnelID: grant.TunnelID, revision: grant.Revision, epoch: grant.SessionID + "." + grant.GrantID}
}

func (m *clientPeerManager) statsLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.reportAllTraffic()
		case <-m.closed:
			return
		}
	}
}

func (m *clientPeerManager) reportAllTraffic() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.traffic))
	for id := range m.traffic {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		m.reportGrant(id)
	}
}

func (m *clientPeerManager) reportGrant(grantID string) {
	m.mu.RLock()
	counter := m.traffic[grantID]
	m.mu.RUnlock()
	if counter == nil || m.runtime == nil {
		return
	}
	ingress, egress := counter.ingress.Load(), counter.egress.Load()
	if ingress == counter.lastIngress.Load() && egress == counter.lastEgress.Load() {
		return
	}
	report := protocol.P2PStatsReport{SessionID: counter.sessionID, GrantID: counter.grantID, TunnelID: counter.tunnelID, Revision: counter.revision, Epoch: counter.epoch, Sequence: counter.sequence.Add(1), IngressBytes: ingress, EgressBytes: egress}
	msg, err := protocol.NewMessage(protocol.MsgTypeP2PStatsReport, report)
	if err != nil {
		return
	}
	if err := m.runtime.writeJSON(msg); err == nil {
		counter.lastIngress.Store(ingress)
		counter.lastEgress.Store(egress)
	}
}

func (m *clientPeerManager) trackStream(grantID string, conn net.Conn) net.Conn {
	tracked := &trackedPeerConn{Conn: conn, manager: m, grantID: grantID}
	m.mu.Lock()
	set := m.streams[grantID]
	if set == nil {
		set = make(map[*trackedPeerConn]struct{})
		m.streams[grantID] = set
	}
	set[tracked] = struct{}{}
	m.mu.Unlock()
	return tracked
}

func (m *clientPeerManager) unregisterStream(grantID string, stream *trackedPeerConn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.streams[grantID]
	delete(set, stream)
	if len(set) == 0 {
		delete(m.streams, grantID)
	}
}

func (m *clientPeerManager) expiryLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.expireNow()
		case <-m.closed:
			return
		}
	}
}

func (m *clientPeerManager) expireNow() {
	now := m.now()
	var peers []peerDataSession
	var streams []*trackedPeerConn
	var remotes []*remoteCreditAccount
	var owners []*ownerCreditScheduler
	m.mu.Lock()
	for id, pending := range m.earlySignals {
		if !pending.expiresAt.After(now) {
			delete(m.earlySignals, id)
		}
	}
	for id, session := range m.sessions {
		if !session.expiresAt.After(now) {
			delete(m.sessions, id)
			peers = append(peers, session.peer)
			for _, grant := range session.grants {
				for stream := range m.streams[grant.GrantID] {
					streams = append(streams, stream)
				}
				delete(m.streams, grant.GrantID)
				delete(m.traffic, grant.GrantID)
				if account := m.remoteCredits[grant.GrantID]; account != nil {
					remotes = append(remotes, account)
				}
				if scheduler := m.ownerCredits[grant.GrantID]; scheduler != nil {
					owners = append(owners, scheduler)
				}
				delete(m.remoteCredits, grant.GrantID)
				delete(m.ownerCredits, grant.GrantID)
			}
			continue
		}
		for tunnelID, grant := range session.grants {
			if !grant.ExpiresAt.After(now) {
				delete(session.grants, tunnelID)
				for stream := range m.streams[grant.GrantID] {
					streams = append(streams, stream)
				}
				delete(m.streams, grant.GrantID)
				delete(m.traffic, grant.GrantID)
				if account := m.remoteCredits[grant.GrantID]; account != nil {
					remotes = append(remotes, account)
				}
				if scheduler := m.ownerCredits[grant.GrantID]; scheduler != nil {
					owners = append(owners, scheduler)
				}
				delete(m.remoteCredits, grant.GrantID)
				delete(m.ownerCredits, grant.GrantID)
			}
		}
	}
	m.mu.Unlock()
	for _, stream := range streams {
		_ = stream.closeWithoutUnregister()
	}
	for _, peer := range peers {
		_ = peer.Close()
	}
	for _, account := range remotes {
		account.Close()
	}
	for _, scheduler := range owners {
		scheduler.Close()
	}
}

func (m *clientPeerManager) removeAndClose(id string) {
	m.mu.RLock()
	snapshot := m.sessions[id]
	var grantIDs []string
	if snapshot != nil {
		for _, grant := range snapshot.grants {
			grantIDs = append(grantIDs, grant.GrantID)
		}
	}
	m.mu.RUnlock()
	for _, grantID := range grantIDs {
		m.reportGrant(grantID)
	}
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	var streams []*trackedPeerConn
	var remotes []*remoteCreditAccount
	var owners []*ownerCreditScheduler
	if s != nil {
		for _, grant := range s.grants {
			for stream := range m.streams[grant.GrantID] {
				streams = append(streams, stream)
			}
			delete(m.streams, grant.GrantID)
			delete(m.traffic, grant.GrantID)
			if account := m.remoteCredits[grant.GrantID]; account != nil {
				remotes = append(remotes, account)
			}
			if scheduler := m.ownerCredits[grant.GrantID]; scheduler != nil {
				owners = append(owners, scheduler)
			}
			delete(m.remoteCredits, grant.GrantID)
			delete(m.ownerCredits, grant.GrantID)
		}
	}
	m.mu.Unlock()
	for _, stream := range streams {
		_ = stream.closeWithoutUnregister()
	}
	for _, account := range remotes {
		account.Close()
	}
	for _, scheduler := range owners {
		scheduler.Close()
	}
	if s != nil {
		_ = s.peer.Close()
	}
}
func (m *clientPeerManager) Close() {
	m.closeOnce.Do(func() {
		close(m.closed)
		m.reportAllTraffic()
		m.mu.Lock()
		sessions := m.sessions
		streams := m.streams
		remotes := m.remoteCredits
		owners := m.ownerCredits
		m.sessions = make(map[string]*clientPeerSession)
		m.streams = make(map[string]map[*trackedPeerConn]struct{})
		m.traffic = make(map[string]*peerTrafficCounter)
		m.remoteCredits = make(map[string]*remoteCreditAccount)
		m.ownerCredits = make(map[string]*ownerCreditScheduler)
		m.mu.Unlock()
		for _, set := range streams {
			for stream := range set {
				_ = stream.closeWithoutUnregister()
			}
		}
		for _, s := range sessions {
			_ = s.peer.Close()
		}
		for _, account := range remotes {
			account.Close()
		}
		for _, scheduler := range owners {
			scheduler.Close()
		}
	})
}

type peerTrafficCounter struct {
	sessionID, grantID, tunnelID, epoch string
	revision                            int64
	ingress, egress                     atomic.Uint64
	lastIngress, lastEgress             atomic.Uint64
	sequence                            atomic.Uint64
}

type trackedPeerConn struct {
	net.Conn
	manager *clientPeerManager
	grantID string
	once    sync.Once
}

func (c *trackedPeerConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		c.manager.reportGrant(c.grantID)
		c.manager.unregisterStream(c.grantID, c)
	})
	return err
}

func (c *trackedPeerConn) closeWithoutUnregister() error {
	var err error
	c.once.Do(func() { err = c.Conn.Close() })
	return err
}

type peerDirectIngressTransport struct {
	manager            *clientPeerManager
	sessionID, grantID string
}

func (t peerDirectIngressTransport) Name() string { return protocol.ActualTransportPeerDirect }
func (t peerDirectIngressTransport) Available() bool {
	t.manager.mu.RLock()
	defer t.manager.mu.RUnlock()
	s := t.manager.sessions[t.sessionID]
	return s != nil && s.peer.Available() && s.expiresAt.After(t.manager.now())
}
func (t peerDirectIngressTransport) Open(req protocol.TunnelProvisionRequest, openClientID string, mutate func(*protocol.DataStreamHeader)) (net.Conn, error) {
	t.manager.mu.RLock()
	s := t.manager.sessions[t.sessionID]
	var grant protocol.P2PTunnelGrant
	if s != nil {
		grant = s.grants[req.TunnelID]
	}
	t.manager.mu.RUnlock()
	if s == nil || grant.GrantID != t.grantID || grant.Revision != req.Revision || !grant.ExpiresAt.After(t.manager.now()) {
		return nil, fmt.Errorf("p2p tunnel grant unavailable")
	}
	stream, err := s.peer.Open()
	if err != nil {
		return nil, err
	}
	header, err := ingressDataStreamHeader(req, openClientID, protocol.ActualTransportPeerDirect)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	header.OpenToken = grant.GrantID
	if mutate != nil {
		mutate(&header)
	}
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		_ = stream.Close()
		return nil, err
	}
	tracked := t.manager.trackStream(grant.GrantID, stream)
	return t.manager.limitConn(grant.GrantID, tracked, req.Spec.Ingress.Type == protocol.IngressTypeUDPListen, false), nil
}
