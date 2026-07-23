package server

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"netsgo/pkg/protocol"
)

func newClientActivityServer(t *testing.T) *Server {
	t.Helper()
	s := New(0)
	path := filepath.Join(t.TempDir(), serverDBFileName)
	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.serverDB = db
	s.activityStore = newActivityStoreWithDB(path, db, false)
	return s
}

func TestClientActivityOnlineBeforeOfflineAndDeduplicated(t *testing.T) {
	s := newClientActivityServer(t)
	client := &ClientConn{
		ID: "client-activity", InstallID: "install", Info: protocol.ClientInfo{Hostname: "activity-host"},
		generation: 1, state: clientStatePendingData, proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(client.ID, client)
	if !s.promotePendingToLiveIfCurrent(client) {
		t.Fatal("promotion should succeed")
	}
	if s.promotePendingToLiveIfCurrent(client) {
		t.Fatal("duplicate promotion should fail")
	}
	if !s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "data_session_closed") {
		t.Fatal("invalidation should succeed")
	}
	if s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "control_loop_exit") {
		t.Fatal("duplicate invalidation should fail")
	}

	page, err := s.activityStore.Query(ActivityQuery{Scope: ActivityScopeClient, ScopeID: client.ID, Limit: 50})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("activity item count = %d, want 2", len(page.Items))
	}
	if got := []string{page.Items[1].Action, page.Items[0].Action}; !reflect.DeepEqual(got, []string{"online", "offline"}) {
		t.Fatalf("lifecycle actions = %#v", got)
	}
	if page.Items[0].Severity != ActivitySeverityWarning {
		t.Fatalf("unexpected disconnect severity = %q", page.Items[0].Severity)
	}
}

func TestClientActivityStaleGenerationProducesNoEvent(t *testing.T) {
	s := newClientActivityServer(t)
	current := &ClientConn{ID: "client-stale", generation: 2, state: clientStatePendingData, proxies: make(map[string]*ProxyTunnel)}
	stale := &ClientConn{ID: current.ID, generation: 1, state: clientStatePendingData, proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(current.ID, current)
	if s.promotePendingToLiveIfCurrent(stale) {
		t.Fatal("stale generation promotion should fail")
	}
	if s.invalidateLogicalSessionIfCurrent(stale.ID, stale.generation, "control_loop_exit") {
		t.Fatal("stale generation invalidation should fail")
	}
	maxID, err := s.activityStore.MaxID()
	if err != nil {
		t.Fatalf("MaxID() error = %v", err)
	}
	if maxID != 0 {
		t.Fatalf("stale generation produced activity ID %d", maxID)
	}
}

func TestClientActivityBootIDScopesGenerationDedupe(t *testing.T) {
	s := newClientActivityServer(t)
	for index, bootID := range []string{"boot-one", "boot-two"} {
		s.activityBootID = bootID
		client := &ClientConn{ID: "client-restart", generation: 1, state: clientStatePendingData, proxies: make(map[string]*ProxyTunnel)}
		s.clients.Store(client.ID, client)
		if !s.promotePendingToLiveIfCurrent(client) {
			t.Fatalf("promotion %d should succeed", index)
		}
		if !s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "normal_closure") {
			t.Fatalf("invalidation %d should succeed", index)
		}
	}
	page, err := s.activityStore.Query(ActivityQuery{Scope: ActivityScopeClient, ScopeID: "client-restart", Limit: 50})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("cross-boot activity count = %d, want 4", len(page.Items))
	}
}

func TestClientActivityPromotionAndInvalidationRacePreservesOrder(t *testing.T) {
	s := newClientActivityServer(t)
	for i := 0; i < 50; i++ {
		client := &ClientConn{ID: "client-race-" + fmt.Sprint(i), generation: 1, state: clientStatePendingData, proxies: make(map[string]*ProxyTunnel)}
		s.clients.Store(client.ID, client)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			s.promotePendingToLiveIfCurrent(client)
		}()
		go func() {
			defer wg.Done()
			<-start
			s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "data_session_closed")
		}()
		close(start)
		wg.Wait()

		page, err := s.activityStore.Query(ActivityQuery{Scope: ActivityScopeClient, ScopeID: client.ID, Limit: 10})
		if err != nil {
			t.Fatalf("iteration %d Query() error = %v", i, err)
		}
		switch len(page.Items) {
		case 0:
			// Invalidation won while PendingData; no lifecycle edge is observable.
		case 2:
			if page.Items[1].Action != "online" || page.Items[0].Action != "offline" || page.Items[1].ID >= page.Items[0].ID {
				t.Fatalf("iteration %d lifecycle order = %+v", i, page.Items)
			}
		default:
			t.Fatalf("iteration %d partial lifecycle events = %+v", i, page.Items)
		}
	}
}
