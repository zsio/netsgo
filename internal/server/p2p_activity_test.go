package server

import (
	"path/filepath"
	"reflect"
	"testing"

	"netsgo/pkg/protocol"
)

func TestP2PActivitySpecsCheckingAndConnectedEdges(t *testing.T) {
	session := p2pSessionSnapshot{
		SessionID: "session-1", ClientA: "client-a", ClientB: "client-b",
		Grants: []p2pGrantSnapshot{{TunnelID: "tunnel-1", Revision: 3}},
	}
	checking := p2pLifecycleResult{Session: session, ReportAccepted: true, StatusState: protocol.P2PStateChecking, Sequence: 4}
	connected := p2pLifecycleResult{Session: session, ReportAccepted: true, ReadyEdge: true, StatusState: protocol.P2PStateConnected, Sequence: 5}

	assertP2PActivityActions(t, checking, []string{"checking"})
	assertP2PActivityActions(t, connected, []string{"connected"})
}

func TestP2PActivitySpecsSplitMixedFailureOutcomes(t *testing.T) {
	failed := p2pGrantSnapshot{TunnelID: "direct-only", Revision: 1}
	fallback := p2pGrantSnapshot{TunnelID: "direct-preferred", Revision: 1}
	result := p2pLifecycleResult{
		Session:         p2pSessionSnapshot{SessionID: "session-1", ClientA: "client-a", ClientB: "client-b", Grants: []p2pGrantSnapshot{failed, fallback}},
		FailedEdge:      true,
		ActivityActions: map[string][]p2pGrantSnapshot{"failed": {failed}, "fallback": {fallback}},
		Sequence:        9,
	}
	specs := p2pActivitySpecs(result)
	if len(specs) != 2 || specs[0].Action != "failed" || specs[1].Action != "fallback" {
		t.Fatalf("mixed failure specs = %+v", specs)
	}
	if len(specs[0].Tunnels) != 1 || specs[0].Tunnels[0].TunnelID != failed.TunnelID {
		t.Fatalf("failed subjects = %+v", specs[0].Tunnels)
	}
	if len(specs[1].Tunnels) != 1 || specs[1].Tunnels[0].TunnelID != fallback.TunnelID {
		t.Fatalf("fallback subjects = %+v", specs[1].Tunnels)
	}
}

func TestAppendP2PActivitiesDeduplicatesLifecycleEdges(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverDBFileName)
	db, err := openServerDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := newActivityStoreWithDB(path, db, false)
	s := New(0)
	s.serverDB = db
	s.activityStore = store

	result := p2pLifecycleResult{
		Session:        p2pSessionSnapshot{SessionID: "session-1", ClientA: "client-a", ClientB: "client-b", Grants: []p2pGrantSnapshot{{TunnelID: "tunnel-1", Revision: 1}}},
		ReportAccepted: true, ReadyEdge: true, StatusState: protocol.P2PStateConnected, Sequence: 7,
	}
	s.appendP2PActivities(result)
	s.appendP2PActivities(result)

	page, err := store.Query(ActivityQuery{Scope: ActivityScopeTunnel, ScopeID: "tunnel-1", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Action != "connected" {
		t.Fatalf("deduplicated connected activity = %+v", page.Items)
	}
}

func assertP2PActivityActions(t *testing.T, result p2pLifecycleResult, want []string) {
	t.Helper()
	specs := p2pActivitySpecs(result)
	got := make([]string, len(specs))
	for i := range specs {
		got[i] = specs[i].Action
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
}
