package server

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestTunnelRuntimeActivityCommittedTransitionOnly(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()
	stored := testStoredServerExposeTCPTunnel("runtime-activity-id", "runtime-activity", "client-1", 8080, 18080, time.Now().UTC())
	stored.Revision = 4
	stored.RuntimeState = protocol.ProxyRuntimeStatePending
	stored.ActualTransport = protocol.ActualTransportUnknown
	mustAddStableTunnel(t, s.store, stored)

	updated, err := s.transitionStoredTunnelRuntimeIfCurrent(stored, protocol.ProxyRuntimeStatePending, protocol.ProxyRuntimeStateExposed, "")
	if err != nil || !updated {
		t.Fatalf("runtime activation = %v, %v", updated, err)
	}
	updated, err = s.transitionStoredTunnelRuntimeIfCurrent(stored, protocol.ProxyRuntimeStatePending, protocol.ProxyRuntimeStateExposed, "")
	if err != nil || updated {
		t.Fatalf("stale transition = %v, %v", updated, err)
	}

	page, err := s.activityStore.Query(ActivityQuery{Scope: ActivityScopeTunnel, ScopeID: stored.ID, Limit: 20, Severities: []ActivitySeverity{ActivitySeverityDebug, ActivitySeverityInfo, ActivitySeverityWarning, ActivitySeverityError}})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Action != "runtime_changed" || page.Items[0].Severity != ActivitySeverityDebug {
		t.Fatalf("runtime activity = %+v", page.Items)
	}
}

func TestTunnelRuntimeActivityFailureDoesNotRollbackRuntime(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()
	stored := testStoredServerExposeTCPTunnel("runtime-failure-id", "runtime-failure", "client-1", 8080, 18081, time.Now().UTC())
	stored.Revision = 2
	stored.RuntimeState = protocol.ProxyRuntimeStatePending
	stored.ActualTransport = protocol.ActualTransportUnknown
	mustAddStableTunnel(t, s.store, stored)
	s.ensureSharedStoreReferences()
	s.activityStore.failNextAppendsForTest(errors.New("injected runtime activity failure"), 1)

	updated, err := s.updateStoredTunnelRuntimeIfCurrent(stored, protocol.ProxyRuntimeStateError, "provision failed: raw detail")
	if err != nil || !updated {
		t.Fatalf("runtime error projection = %v, %v", updated, err)
	}
	reloaded, err := s.store.GetTunnelByID(stored.ID)
	if err != nil || reloaded.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("runtime state rolled back after activity failure: %+v, err=%v", reloaded, err)
	}
	page, err := s.activityStore.Query(ActivityQuery{Scope: ActivityScopeTunnel, ScopeID: stored.ID, Limit: 20, Severities: []ActivitySeverity{ActivitySeverityDebug, ActivitySeverityInfo, ActivitySeverityWarning, ActivitySeverityError}})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("activity after injected failure = %+v, %v", page.Items, err)
	}
}

func TestTunnelRuntimeActivityErrorAndRecovery(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()
	stored := testStoredServerExposeTCPTunnel("runtime-recovery-id", "runtime-recovery", "client-1", 8080, 18082, time.Now().UTC())
	stored.Revision = 3
	stored.RuntimeState = protocol.ProxyRuntimeStatePending
	stored.ActualTransport = protocol.ActualTransportUnknown
	mustAddStableTunnel(t, s.store, stored)
	if _, err := s.updateStoredTunnelRuntimeIfCurrent(stored, protocol.ProxyRuntimeStateError, "restore failed: do not persist this raw detail"); err != nil {
		t.Fatal(err)
	}
	stored, _, _ = s.findStoredTunnelByID(stored.ID)
	if _, err := s.updateStoredTunnelRuntimeIfCurrent(stored, protocol.ProxyRuntimeStateExposed, ""); err != nil {
		t.Fatal(err)
	}
	page, err := s.activityStore.Query(ActivityQuery{Scope: ActivityScopeTunnel, ScopeID: stored.ID, Limit: 20, Severities: []ActivitySeverity{ActivitySeverityDebug, ActivitySeverityInfo, ActivitySeverityWarning, ActivitySeverityError}})
	if err != nil || len(page.Items) != 2 || page.Items[1].Action != "runtime_error" || page.Items[0].Action != "runtime_recovered" {
		t.Fatalf("runtime error/recovery activity = %+v, %v", page.Items, err)
	}
	for _, item := range page.Items {
		if bytes.Contains(item.Payload, []byte("do not persist")) {
			t.Fatalf("raw runtime reason leaked into payload: %s", item.Payload)
		}
	}
}
