package server

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestP2PCoordinatorConcurrentGrantStormSharesOnePairSession(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	const grants = 128
	sessionIDs := make(chan string, grants)
	errors := make(chan error, grants)
	var wg sync.WaitGroup
	for i := 0; i < grants; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			grant, lifecycle, err := c.ensureGrant(p2pGrantSpec{tunnelID: fmt.Sprintf("storm-%03d", i), revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
			if err != nil || !lifecycle.GrantCreated {
				errors <- fmt.Errorf("grant %d: grantCreated=%v err=%v", i, lifecycle.GrantCreated, err)
				return
			}
			sessionIDs <- grant.sessionID
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	close(sessionIDs)
	var want string
	for id := range sessionIDs {
		if want == "" {
			want = id
		}
		if id != want {
			t.Fatalf("grant storm created multiple pair sessions: %q != %q", id, want)
		}
	}
	if c.sessionCount() != 1 {
		t.Fatalf("grant storm session count=%d want=1", c.sessionCount())
	}
	messages, err := c.prepareMessages(want)
	if err != nil || len(messages) != 2 {
		t.Fatalf("pair prepare messages=%d err=%v", len(messages), err)
	}
	for _, message := range messages {
		prepare := message.payload.(protocol.P2PSessionPrepare)
		if len(prepare.Grants) != grants {
			t.Fatalf("prepared grants=%d want=%d", len(prepare.Grants), grants)
		}
	}
}

func TestP2PCoordinatorSharesPairSessionAndKeepsTunnelRolesPerGrant(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })

	first, lifecycle, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	if err != nil || !lifecycle.GrantCreated {
		t.Fatalf("first grant: grantCreated=%v err=%v", lifecycle.GrantCreated, err)
	}
	if !lifecycle.SessionCreated {
		t.Fatal("first grant must create the pair session")
	}
	second, lifecycle, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t2", revision: 3, ingressClientID: "b", targetClientID: "a", ingressGeneration: 20, targetGeneration: 10})
	if err != nil || !lifecycle.GrantCreated {
		t.Fatalf("second grant: grantCreated=%v err=%v", lifecycle.GrantCreated, err)
	}
	if lifecycle.SessionCreated {
		t.Fatal("second grant on the same pair must not recreate the session")
	}
	if first.sessionID != second.sessionID {
		t.Fatalf("pair did not share session: %s != %s", first.sessionID, second.sessionID)
	}
	if first.forClient("a").LocalRole != protocol.DataStreamRoleIngress || second.forClient("a").LocalRole != protocol.DataStreamRoleTarget {
		t.Fatal("tunnel role was incorrectly fixed at pair scope")
	}
	if got := c.sessionCount(); got != 1 {
		t.Fatalf("session count: want 1 got %d", got)
	}
}
func TestP2PCoordinatorLifecycleSnapshotsAreImmutable(t *testing.T) {
	c := newP2PCoordinator(time.Now)
	first, started, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	if err != nil || !started.SessionCreated || !started.GrantCreated || len(started.Session.Grants) != 1 {
		t.Fatalf("started lifecycle = %+v, err=%v", started, err)
	}
	_, attached, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t2", revision: 2, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	if err != nil || attached.SessionCreated || !attached.GrantCreated || attached.Session.SessionID != first.sessionID || len(attached.Session.Grants) != 2 {
		t.Fatalf("attached lifecycle = %+v, err=%v", attached, err)
	}
	if len(started.Session.Grants) != 1 || started.Session.Grants[0].TunnelID != "t1" {
		t.Fatalf("past lifecycle snapshot mutated: %+v", started.Session.Grants)
	}
	statusA, err := c.recordReady("a", 10, protocol.P2PSessionStatus{SessionID: first.sessionID, Sequence: 1, State: protocol.P2PStateConnected})
	if err != nil || !statusA.ReportAccepted || statusA.ReadyEdge {
		t.Fatalf("first ready report = %+v, err=%v", statusA, err)
	}
	statusB, err := c.recordReady("b", 20, protocol.P2PSessionStatus{SessionID: first.sessionID, Sequence: 1, State: protocol.P2PStateConnected})
	if err != nil || !statusB.ReadyEdge || !statusB.Session.Ready {
		t.Fatalf("pair ready report = %+v, err=%v", statusB, err)
	}
	closed := c.closeSession(first.sessionID, "participant offline raw detail")
	if !closed.ClosedEdge || closed.ReasonCode != "participant_offline" || len(closed.Session.Grants) != 2 {
		t.Fatalf("closed lifecycle = %+v", closed)
	}
	if len(attached.Session.Grants) != 2 || attached.Session.Ready {
		t.Fatalf("attached snapshot mutated after ready/close: %+v", attached.Session)
	}
}

func TestP2PCoordinatorExistingReadySessionRemainsReadyWhenGrantAdded(t *testing.T) {
	now := time.Now()
	c := newP2PCoordinator(func() time.Time { return now })
	first, _, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := c.recordReady("a", 10, protocol.P2PSessionStatus{SessionID: first.sessionID, Sequence: 1, State: protocol.P2PStateConnected}); err != nil || result.ReadyEdge {
		t.Fatalf("first peer ready: ready=%v err=%v", result.ReadyEdge, err)
	}
	if result, err := c.recordReady("b", 20, protocol.P2PSessionStatus{SessionID: first.sessionID, Sequence: 1, State: protocol.P2PStateConnected}); err != nil || !result.ReadyEdge {
		t.Fatalf("pair ready: ready=%v err=%v", result.ReadyEdge, err)
	}
	if !c.sessionReady(first.sessionID) {
		t.Fatal("pair should report ready after both peers connect")
	}
	second, lifecycle, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t2", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	if err != nil || !lifecycle.GrantCreated {
		t.Fatalf("add grant to ready pair: grantCreated=%v err=%v", lifecycle.GrantCreated, err)
	}
	if second.sessionID != first.sessionID {
		t.Fatalf("new grant created a different pair session: first=%q second=%q", first.sessionID, second.sessionID)
	}
	if !c.sessionReady(first.sessionID) {
		t.Fatal("adding a grant must not hide the existing connected pair state")
	}
}

func TestP2PCoordinatorRejectsStaleSignalSequenceAndGeneration(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	grant, _, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	if err != nil {
		t.Fatal(err)
	}
	signal := protocol.P2PSignal{SessionID: grant.sessionID, Sequence: 1, Kind: protocol.P2PSignalOffer, SDP: "v=0"}
	peer, err := c.authorizeSignal("a", 10, signal)
	if err != nil || peer != "b" {
		t.Fatalf("valid signal rejected: peer=%s err=%v", peer, err)
	}
	if _, err := c.authorizeSignal("a", 10, signal); err == nil {
		t.Fatal("replayed signal accepted")
	}
	signal.Sequence++
	if _, err := c.authorizeSignal("a", 11, signal); err == nil {
		t.Fatal("wrong generation accepted")
	}
}

func TestP2PCoordinatorRevokesOneGrantWithoutClosingSharedPair(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	first, _, _ := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 1, targetGeneration: 2})
	_, _, _ = c.ensureGrant(p2pGrantSpec{tunnelID: "t2", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 1, targetGeneration: 2})
	result := c.revokeTunnel("t1", 1, "disabled")
	if result.ClosedEdge || len(result.Outbounds) != 2 {
		t.Fatalf("first revoke closed=%v messages=%d", result.ClosedEdge, len(result.Outbounds))
	}
	if _, ok := c.session(first.sessionID); !ok {
		t.Fatal("shared pair closed while another grant remained")
	}
	result = c.revokeTunnel("t2", 1, "deleted")
	if !result.ClosedEdge || len(result.Outbounds) != 4 {
		t.Fatalf("last revoke closed=%v messages=%d", result.ClosedEdge, len(result.Outbounds))
	}
}

func TestP2PCoordinatorExpiresPairAtHardLeaseBoundary(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	grant, _, _ := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 1, targetGeneration: 2})
	now = now.Add(p2pLeaseDuration)
	if expired := c.expire(); len(expired) != 1 || expired[0] != grant.sessionID {
		t.Fatalf("expired sessions: %v", expired)
	}
	if c.sessionCount() != 0 {
		t.Fatal("expired pair remained registered")
	}
}

func TestP2PCoordinatorClientDisconnectClosesPairOnlyForCurrentGeneration(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	grant, _, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	if err != nil {
		t.Fatal(err)
	}
	if results := c.closeClient("a", 9, "stale disconnect"); len(results) != 0 || c.sessionCount() != 1 {
		t.Fatalf("stale generation closed current pair: results=%+v sessions=%d", results, c.sessionCount())
	}
	results := c.closeClient("a", 10, "control lost")
	out := results[0].Outbounds
	if len(out) != 1 || out[0].clientID != "b" || out[0].messageType != protocol.MsgTypeP2PClosed {
		t.Fatalf("current disconnect close notification=%+v", out)
	}
	status, ok := out[0].payload.(protocol.P2PSessionStatus)
	if !ok || status.SessionID != grant.sessionID || status.State != protocol.P2PStateClosed || status.Error != "control lost" {
		t.Fatalf("current disconnect status=%+v", out[0].payload)
	}
	if c.sessionCount() != 0 {
		t.Fatal("current Client disconnect left pair session alive")
	}
}

func TestP2PCoordinatorStatsAreOwnerOnlyAndIdempotent(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	grant, _, _ := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	report := protocol.P2PStatsReport{SessionID: grant.sessionID, GrantID: grant.grantID, TunnelID: "t1", Revision: 1, Epoch: "epoch", Sequence: 1, IngressBytes: 100, EgressBytes: 40}
	if _, _, err := c.acceptStats("a", 10, report); err == nil {
		t.Fatal("ingress client was allowed to report owner traffic")
	}
	in, out, err := c.acceptStats("b", 20, report)
	if err != nil || in != 100 || out != 40 {
		t.Fatalf("first report delta=(%d,%d) err=%v", in, out, err)
	}
	if _, _, err := c.acceptStats("b", 20, report); err == nil {
		t.Fatal("duplicate report accepted")
	}
	report.Sequence = 2
	report.IngressBytes = 125
	report.EgressBytes = 50
	in, out, err = c.acceptStats("b", 20, report)
	if err != nil || in != 25 || out != 10 {
		t.Fatalf("second report delta=(%d,%d) err=%v", in, out, err)
	}
}

func TestP2PCoordinatorAuthorizesCreditDirectionAndCumulativeBounds(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	grant, _, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20, totalBPS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	demand := protocol.P2PCreditDemand{SessionID: grant.sessionID, GrantID: grant.grantID, TunnelID: "t1", Revision: 1, Sequence: 1, DesiredBytes: 100}
	if _, err := c.authorizeCreditDemand("b", 20, demand); err == nil {
		t.Fatal("owner was allowed to send ingress demand")
	}
	peer, err := c.authorizeCreditDemand("a", 10, demand)
	if err != nil || peer != "b" {
		t.Fatalf("valid demand peer=%s err=%v", peer, err)
	}
	credit := protocol.P2PCreditGrant{SessionID: grant.sessionID, GrantID: grant.grantID, TunnelID: "t1", Revision: 1, Sequence: 1, GrantedBytes: 101}
	if _, err := c.authorizeCreditGrant("b", 20, credit); err == nil {
		t.Fatal("grant exceeding demand accepted")
	}
	credit.GrantedBytes = 50
	peer, err = c.authorizeCreditGrant("b", 20, credit)
	if err != nil || peer != "a" {
		t.Fatalf("valid grant peer=%s err=%v", peer, err)
	}
}

func TestP2PCoordinatorValidatesSignalRoleAndCandidateLimits(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	grant, _, err := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.authorizeSignal("b", 20, protocol.P2PSignal{SessionID: grant.sessionID, Sequence: 1, Kind: protocol.P2PSignalOffer, SDP: "v=0"}); err == nil {
		t.Fatal("answerer was allowed to send an offer")
	}
	for i := 1; i <= p2pCandidatesPerWindow; i++ {
		signal := protocol.P2PSignal{SessionID: grant.sessionID, Sequence: uint64(i), Kind: protocol.P2PSignalCandidate, Candidate: "candidate:1"}
		if _, err := c.authorizeSignal("a", 10, signal); err != nil {
			t.Fatalf("candidate %d rejected: %v", i, err)
		}
	}
	if _, err := c.authorizeSignal("a", 10, protocol.P2PSignal{SessionID: grant.sessionID, Sequence: p2pCandidatesPerWindow + 1, Kind: protocol.P2PSignalCandidate, Candidate: "candidate:1"}); err == nil {
		t.Fatal("candidate rate limit was not enforced")
	}
	now = now.Add(p2pCandidateWindow)
	for i := p2pCandidatesPerWindow + 1; i <= protocol.P2PMaxCandidates; i++ {
		signal := protocol.P2PSignal{SessionID: grant.sessionID, Sequence: uint64(i + 1), Kind: protocol.P2PSignalCandidate, Candidate: "candidate:1"}
		if _, err := c.authorizeSignal("a", 10, signal); err != nil {
			t.Fatalf("candidate %d after window rejected: %v", i, err)
		}
		if (i-p2pCandidatesPerWindow)%p2pCandidatesPerWindow == 0 {
			now = now.Add(p2pCandidateWindow)
		}
	}
	if _, err := c.authorizeSignal("a", 10, protocol.P2PSignal{SessionID: grant.sessionID, Sequence: protocol.P2PMaxCandidates + 2, Kind: protocol.P2PSignalCandidate, Candidate: "candidate:1"}); err == nil {
		t.Fatal("candidate session limit was not enforced")
	}
}

func TestP2PCoordinatorRejectsInvalidAndReplayedStatus(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	grant, _, _ := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	status := protocol.P2PSessionStatus{SessionID: grant.sessionID, Sequence: 1, State: protocol.P2PStateConnected}
	if _, err := c.recordReady("a", 10, status); err != nil {
		t.Fatalf("valid status rejected: %v", err)
	}
	if _, err := c.recordReady("a", 10, status); err == nil {
		t.Fatal("replayed status accepted")
	}
	status.Sequence = 2
	status.State = protocol.P2PStateClosed
	if _, err := c.recordReady("a", 10, status); err == nil {
		t.Fatal("client-reported closed status accepted")
	}
}

func TestP2PCoordinatorAcceptsOneFinalOwnerStatsReportAfterSessionClose(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	c := newP2PCoordinator(func() time.Time { return now })
	grant, _, _ := c.ensureGrant(p2pGrantSpec{tunnelID: "t1", revision: 1, ingressClientID: "a", targetClientID: "b", ingressGeneration: 10, targetGeneration: 20})
	initial := protocol.P2PStatsReport{SessionID: grant.sessionID, GrantID: grant.grantID, TunnelID: "t1", Revision: 1, Epoch: "epoch", Sequence: 1, IngressBytes: 10, EgressBytes: 4}
	if _, _, err := c.acceptStats("b", 20, initial); err != nil {
		t.Fatal(err)
	}
	_ = c.closeSession(grant.sessionID, "failed")
	final := initial
	final.Sequence = 2
	final.IngressBytes = 17
	final.EgressBytes = 9
	ingress, egress, err := c.acceptStats("b", 20, final)
	if err != nil || ingress != 7 || egress != 5 {
		t.Fatalf("final delta=(%d,%d) err=%v", ingress, egress, err)
	}
	if _, _, err := c.acceptStats("b", 20, final); err == nil {
		t.Fatal("replayed final stats accepted")
	}
	if _, _, err := c.acceptStats("a", 10, protocol.P2PStatsReport{SessionID: grant.sessionID, GrantID: grant.grantID, TunnelID: "t1", Revision: 1, Epoch: "epoch", Sequence: 3, IngressBytes: 18, EgressBytes: 10}); err == nil {
		t.Fatal("non-owner final stats accepted")
	}
	now = now.Add(p2pFinalStatsGrace)
	final.Sequence = 3
	final.IngressBytes++
	if _, _, err := c.acceptStats("b", 20, final); err == nil {
		t.Fatal("expired final stats grace accepted")
	}
}
