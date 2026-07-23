package server

import (
	"path/filepath"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestApplyP2PLifecycleProjectsCurrentTargetsAndSkipsStale(t *testing.T) {
	store := newTestTunnelStore(t)
	first := testStoredServerExposeTCPTunnel("p2p-projection-first", "first", "client-1", 8081, 18081, time.Time{})
	second := testStoredServerExposeTCPTunnel("p2p-projection-second", "second", "client-1", 8082, 18082, time.Time{})
	first.P2P = P2PState{State: protocol.P2PStateGathering, SessionID: "session-1"}
	second.P2P = P2PState{State: protocol.P2PStateGathering, SessionID: "session-1"}
	if err := store.AddTunnel(first); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTunnel(second); err != nil {
		t.Fatal(err)
	}

	result, err := store.ApplyP2PLifecycle([]p2pGrantSnapshot{
		{TunnelID: first.ID, Revision: first.Revision},
		{TunnelID: second.ID, Revision: second.Revision + 1},
	}, "session-1", P2PProjectionTransition{Mode: P2PProjectionReady, SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 1 || result.Changes[0].After.ID != first.ID || len(result.Stale) != 1 || result.Stale[0].TunnelID != second.ID {
		t.Fatalf("projection result = %+v", result)
	}
}

func TestApplyP2PLifecycleBatchRollsBackOnSQLFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverDBFileName)
	store, err := NewTunnelStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := testStoredServerExposeTCPTunnel("p2p-rollback-first", "first", "client-1", 8081, 18081, time.Time{})
	second := testStoredServerExposeTCPTunnel("p2p-rollback-second", "second", "client-1", 8082, 18082, time.Time{})
	first.P2P = P2PState{State: protocol.P2PStateGathering, SessionID: "session-1"}
	second.P2P = P2PState{State: protocol.P2PStateGathering, SessionID: "session-1"}
	if err := store.AddTunnel(first); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTunnel(second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_second_p2p_projection BEFORE UPDATE OF p2p_state ON tunnels WHEN OLD.id = '` + second.ID + `' BEGIN SELECT RAISE(ABORT, 'injected p2p projection failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = store.ApplyP2PLifecycle([]p2pGrantSnapshot{{TunnelID: first.ID, Revision: first.Revision}, {TunnelID: second.ID, Revision: second.Revision}}, "session-1", P2PProjectionTransition{Mode: P2PProjectionReady, SessionID: "session-1"})
	if err == nil {
		t.Fatal("batch projection should fail")
	}
	reloaded, err := store.GetTunnelByID(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.P2P.State != protocol.P2PStateGathering {
		t.Fatalf("first projection was not rolled back: %+v", reloaded.P2P)
	}
}
