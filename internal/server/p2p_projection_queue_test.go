package server

import (
	"testing"
	"time"
)

func TestP2PProjectionRetryQueueIsBoundedAndDeduplicated(t *testing.T) {
	s := New(0)
	base := p2pProjectionRetryItem{Result: p2pLifecycleResult{Session: p2pSessionSnapshot{SessionID: "shared"}, ClosedEdge: true, Sequence: 1}}
	if !s.enqueueP2PProjectionRetry(base) || !s.enqueueP2PProjectionRetry(base) {
		t.Fatal("same projection should remain enqueueable")
	}
	if len(s.p2pProjectionRetries) != 1 {
		t.Fatalf("deduplicated queue size = %d", len(s.p2pProjectionRetries))
	}
	for i := 1; i < p2pProjectionRetryCapacity; i++ {
		item := base
		item.Result.Session.SessionID = string(rune(i + 1))
		if !s.enqueueP2PProjectionRetry(item) {
			t.Fatalf("queue rejected item %d before capacity", i)
		}
	}
	overflow := base
	overflow.Result.Session.SessionID = "overflow"
	if s.enqueueP2PProjectionRetry(overflow) {
		t.Fatal("queue accepted an item over capacity")
	}
	if len(s.p2pProjectionRetries) != p2pProjectionRetryCapacity {
		t.Fatalf("bounded queue size = %d", len(s.p2pProjectionRetries))
	}
}

func TestP2PProjectionRetryDelayCaps(t *testing.T) {
	if got := p2pProjectionRetryDelay(1); got != time.Second {
		t.Fatalf("first delay = %v", got)
	}
	if got := p2pProjectionRetryDelay(100); got != p2pProjectionRetryMax {
		t.Fatalf("capped delay = %v", got)
	}
}

func TestP2PProjectionRetryPublishesCommittedChanges(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testStoredServerExposeTCPTunnel("p2p-retry-event", "retry-event", "client-1", 8080, 18080, time.Time{})
	stored.P2P = P2PState{State: "gathering", SessionID: "session-1"}
	if err := s.store.AddTunnel(stored); err != nil {
		t.Fatal(err)
	}
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)
	result := p2pLifecycleResult{
		Session:   p2pSessionSnapshot{SessionID: "session-1", Grants: []p2pGrantSnapshot{{TunnelID: stored.ID, Revision: stored.Revision}}},
		ReadyEdge: true, Sequence: 2,
	}
	key := p2pProjectionRetryKey(result)
	s.p2pProjectionRetries[key] = p2pProjectionRetryItem{
		Result: result, Transition: P2PProjectionTransition{Mode: P2PProjectionReady, SessionID: "session-1"},
		Expected: "session-1", Attempts: 1, Next: time.Now().Add(-time.Second),
	}
	s.retryDueP2PProjections(time.Now())
	payload := waitForTunnelChangedEvent(t, ch, "p2p_status", stored.Name)
	if payload["actual_transport"] != "peer_direct" {
		t.Fatalf("retry event actual_transport = %#v", payload["actual_transport"])
	}
	if _, exists := s.p2pProjectionRetries[key]; exists {
		t.Fatal("successful retry remained queued")
	}
}
