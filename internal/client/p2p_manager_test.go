package client

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	peertransport "netsgo/pkg/p2p"
	"netsgo/pkg/protocol"
)

type fakePeerDataSession struct {
	ready      chan struct{}
	terminated chan struct{}
	closeOnce  sync.Once
	closed     bool
}

func newFakePeerDataSession() *fakePeerDataSession {
	ready := make(chan struct{})
	close(ready)
	return &fakePeerDataSession{ready: ready, terminated: make(chan struct{})}
}

func (p *fakePeerDataSession) CreateOffer() (string, error)          { return "", nil }
func (p *fakePeerDataSession) AcceptOffer(string) (string, error)    { return "", nil }
func (p *fakePeerDataSession) AcceptAnswer(string) error             { return nil }
func (p *fakePeerDataSession) AddCandidate(protocol.P2PSignal) error { return nil }
func (p *fakePeerDataSession) Ready() <-chan struct{}                { return p.ready }
func (p *fakePeerDataSession) Terminated() <-chan struct{}           { return p.terminated }
func (p *fakePeerDataSession) Err() error                            { return nil }
func (p *fakePeerDataSession) Available() bool                       { return !p.closed }
func (p *fakePeerDataSession) Open() (net.Conn, error)               { return nil, fmt.Errorf("unused") }
func (p *fakePeerDataSession) Accept() (net.Conn, error)             { return nil, fmt.Errorf("unused") }
func (p *fakePeerDataSession) Close() error {
	p.closeOnce.Do(func() {
		p.closed = true
		close(p.terminated)
	})
	return nil
}

func testPeerManager(now *time.Time) *clientPeerManager {
	return &clientPeerManager{now: func() time.Time { return *now }, sessions: make(map[string]*clientPeerSession), streams: make(map[string]map[*trackedPeerConn]struct{}), earlySignals: make(map[string]pendingP2PSignals), closed: make(chan struct{})}
}

type recordingPeerDataSession struct {
	*fakePeerDataSession
	offers     []string
	candidates []protocol.P2PSignal
}

func (p *recordingPeerDataSession) AcceptOffer(offer string) (string, error) {
	p.offers = append(p.offers, offer)
	return "answer", nil
}

func (p *recordingPeerDataSession) AddCandidate(signal protocol.P2PSignal) error {
	p.candidates = append(p.candidates, signal)
	return nil
}

func TestClientPeerManagerBuffersSignalsThatRaceAheadOfPrepare(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	m := testPeerManager(&now)
	peer := &recordingPeerDataSession{fakePeerDataSession: &fakePeerDataSession{ready: make(chan struct{}), terminated: make(chan struct{})}}
	m.newSession = func(string, []protocol.P2PICEServer, peertransport.SignalHandler) (peerDataSession, error) {
		return peer, nil
	}
	candidate := protocol.P2PSignal{SessionID: "session", Sequence: 1, Kind: protocol.P2PSignalCandidate, Candidate: "candidate:1"}
	if err := m.handleSignal(candidate); err != nil {
		t.Fatalf("buffer candidate: %v", err)
	}
	prepare := protocol.P2PSessionPrepare{SessionID: "session", PeerClientID: "peer", Role: protocol.P2PRoleAnswerer, LocalGeneration: 1, PeerGeneration: 2, LeaseSequence: 1, ExpiresAt: now.Add(time.Minute), ICEServers: []protocol.P2PICEServer{{URLs: []string{"stun:127.0.0.1:3478"}}}}
	if err := m.handlePrepare(prepare); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(peer.offers) != 0 || len(peer.candidates) != 1 {
		t.Fatalf("early signals were not replayed in order: offers=%v candidates=%v", peer.offers, peer.candidates)
	}
	if len(m.earlySignals) != 0 {
		t.Fatalf("early signal buffer leaked: %+v", m.earlySignals)
	}
}

func TestClientPeerManagerExpiresUnmatchedEarlySignals(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	m := testPeerManager(&now)
	signal := protocol.P2PSignal{SessionID: "missing", Sequence: 1, Kind: protocol.P2PSignalOffer, SDP: "v=0"}
	if err := m.handleSignal(signal); err != nil {
		t.Fatalf("buffer signal: %v", err)
	}
	now = now.Add(p2pEarlySignalTTL)
	m.expireNow()
	if len(m.earlySignals) != 0 {
		t.Fatalf("expired early signal buffer leaked: %+v", m.earlySignals)
	}
}

func TestClientPeerManagerRevokeClosesOnlyGrantStreams(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	m := testPeerManager(&now)
	m.sessions["session"] = &clientPeerSession{id: "session", expiresAt: now.Add(time.Minute), grants: map[string]protocol.P2PTunnelGrant{
		"t1": {SessionID: "session", GrantID: "g1", TunnelID: "t1", Revision: 1, LocalRole: protocol.DataStreamRoleTarget, PeerRole: protocol.DataStreamRoleIngress, ExpiresAt: now.Add(time.Minute)},
		"t2": {SessionID: "session", GrantID: "g2", TunnelID: "t2", Revision: 1, LocalRole: protocol.DataStreamRoleTarget, PeerRole: protocol.DataStreamRoleIngress, ExpiresAt: now.Add(time.Minute)},
	}}
	a1, b1 := net.Pipe()
	defer func() { _ = b1.Close() }()
	a2, b2 := net.Pipe()
	defer func() { _ = b2.Close() }()
	stream1 := m.trackStream("g1", a1)
	stream2 := m.trackStream("g2", a2)
	m.handleRevoke(protocol.P2PTunnelRevoke{SessionID: "session", GrantID: "g1", TunnelID: "t1", Revision: 1})
	if _, err := stream1.Write([]byte("x")); err == nil {
		t.Fatal("revoked grant stream remained writable")
	}
	done := make(chan error, 1)
	go func() { _, err := stream2.Write([]byte("x")); done <- err }()
	buf := make([]byte, 1)
	if _, err := b2.Read(buf); err != nil {
		t.Fatalf("other grant stream closed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("other grant write failed: %v", err)
	}
	_ = stream2.Close()
}

func TestClientPeerManagerExpiredGrantClosesStreams(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	m := testPeerManager(&now)
	m.sessions["session"] = &clientPeerSession{id: "session", expiresAt: now.Add(time.Minute), grants: map[string]protocol.P2PTunnelGrant{"t1": {GrantID: "g1", TunnelID: "t1", ExpiresAt: now.Add(time.Second)}}}
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	stream := m.trackStream("g1", a)
	now = now.Add(time.Second)
	m.expireNow()
	if _, err := stream.Write([]byte("x")); err == nil {
		t.Fatal("expired grant stream remained writable")
	}
}

func TestDefaultP2PICEServersUsesConfiguredServerEndpoint(t *testing.T) {
	servers := defaultP2PICEServers("wss://example.com:8443/base")
	if len(servers) != 1 || len(servers[0].URLs) != 1 || servers[0].URLs[0] != "stun:example.com:8443" {
		t.Fatalf("servers=%+v", servers)
	}
	servers = defaultP2PICEServers("https://[2001:db8::1]")
	if servers[0].URLs[0] != "stun:[2001:db8::1]:443" {
		t.Fatalf("IPv6 server=%+v", servers)
	}
}

func TestClientPeerManagerExpiredPairClosesPeerAndEveryGrantStream(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	m := testPeerManager(&now)
	peer := newFakePeerDataSession()
	m.sessions["session"] = &clientPeerSession{id: "session", expiresAt: now.Add(time.Second), peer: peer, grants: map[string]protocol.P2PTunnelGrant{
		"t1": {GrantID: "g1", TunnelID: "t1", ExpiresAt: now.Add(time.Minute)},
		"t2": {GrantID: "g2", TunnelID: "t2", ExpiresAt: now.Add(time.Minute)},
	}}
	a1, b1 := net.Pipe()
	defer func() { _ = b1.Close() }()
	a2, b2 := net.Pipe()
	defer func() { _ = b2.Close() }()
	stream1 := m.trackStream("g1", a1)
	stream2 := m.trackStream("g2", a2)
	now = now.Add(time.Second)
	m.expireNow()
	if !peer.closed {
		t.Fatal("expired pair peer was not closed")
	}
	if _, err := stream1.Write([]byte("x")); err == nil {
		t.Fatal("first expired pair stream remained writable")
	}
	if _, err := stream2.Write([]byte("x")); err == nil {
		t.Fatal("second expired pair stream remained writable")
	}
	if len(m.sessions) != 0 || len(m.streams) != 0 {
		t.Fatalf("expired pair state leaked: sessions=%d streams=%d", len(m.sessions), len(m.streams))
	}
}

func TestClientPeerManagerLeaseRejectsReplayAndExtendsOnlyForward(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	m := testPeerManager(&now)
	m.sessions["session"] = &clientPeerSession{id: "session", leaseSequence: 4, expiresAt: now.Add(time.Second), grants: map[string]protocol.P2PTunnelGrant{}}
	if err := m.handleLease(protocol.P2PLease{SessionID: "session", Sequence: 4, ExpiresAt: now.Add(time.Minute)}); err == nil {
		t.Fatal("replayed lease accepted")
	}
	if err := m.handleLease(protocol.P2PLease{SessionID: "session", Sequence: 5, ExpiresAt: now}); err == nil {
		t.Fatal("expired lease accepted")
	}
	wantExpiry := now.Add(time.Minute)
	if err := m.handleLease(protocol.P2PLease{SessionID: "session", Sequence: 5, ExpiresAt: wantExpiry}); err != nil {
		t.Fatalf("forward lease rejected: %v", err)
	}
	if got := m.sessions["session"]; got.leaseSequence != 5 || !got.expiresAt.Equal(wantExpiry) {
		t.Fatalf("lease not applied: sequence=%d expiry=%v", got.leaseSequence, got.expiresAt)
	}
}
