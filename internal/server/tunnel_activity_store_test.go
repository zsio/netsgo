package server

import (
	"errors"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestTunnelActivityMutationsRollbackWithEventFailure(t *testing.T) {
	actor := ActivityActor{Type: "admin", ID: "user", Name: "admin"}

	t.Run("create", func(t *testing.T) {
		store := newTestTunnelStore(t)
		tunnel := testStoredServerExposeTCPTunnel("activity-create", "activity-create", "client-create", 8080, 18080, zeroTime())
		store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
		if _, err := store.AddTunnelWithActivity(tunnel, actor); err == nil {
			t.Fatal("create should fail when activity append fails")
		}
		if _, ok := store.GetTunnel(tunnel.ClientID, tunnel.Name); ok {
			t.Fatal("failed create persisted tunnel")
		}
	})

	t.Run("update", func(t *testing.T) {
		store := newTestTunnelStore(t)
		before := testStoredServerExposeTCPTunnel("activity-update", "activity-update", "client-update", 8080, 18081, zeroTime())
		mustAddStableTunnel(t, store, before)
		replacement := before
		replacement.Name = "activity-update-new"
		replacement.Revision = before.Revision + 1
		store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
		if _, err := store.ReplaceTunnelByIDWithActivity(before.ClientID, before.ID, before.Revision, replacement, actor); err == nil {
			t.Fatal("update should fail when activity append fails")
		}
		reloaded, err := store.GetTunnelByIDE(before.ClientID, before.ID)
		if err != nil || reloaded.Name != before.Name || reloaded.Revision != before.Revision {
			t.Fatalf("update rollback = %+v, %v", reloaded, err)
		}
	})

	t.Run("stop", func(t *testing.T) {
		store := newTestTunnelStore(t)
		before := testStoredServerExposeTCPTunnel("activity-stop", "activity-stop", "client-stop", 8080, 18082, zeroTime())
		mustAddStableTunnel(t, store, before)
		store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
		if _, _, err := store.UpdateTunnelStatesWithActivity(before.ClientID, before.ID, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, "", "stopped", actor); err == nil {
			t.Fatal("stop should fail when activity append fails")
		}
		reloaded, err := store.GetTunnelByIDE(before.ClientID, before.ID)
		if err != nil || reloaded.DesiredState != before.DesiredState || reloaded.RuntimeState != before.RuntimeState {
			t.Fatalf("stop rollback = %+v, %v", reloaded, err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		store := newTestTunnelStore(t)
		before := testStoredServerExposeTCPTunnel("activity-delete", "activity-delete", "client-delete", 8080, 18083, zeroTime())
		mustAddStableTunnel(t, store, before)
		store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
		if _, err := store.RemoveTunnelByIDWithActivity(before.ClientID, before.ID, actor); err == nil {
			t.Fatal("delete should fail when activity append fails")
		}
		if _, err := store.GetTunnelByIDE(before.ClientID, before.ID); err != nil {
			t.Fatalf("delete rollback lost tunnel: %v", err)
		}
	})

	t.Run("migrate", func(t *testing.T) {
		store := newTestTunnelStore(t)
		before := testStoredServerExposeTCPTunnel("activity-migrate", "activity-migrate", "client-old", 8080, 18084, zeroTime())
		mustAddStableTunnel(t, store, before)
		replacement := tunnelTargetMigrationReplacement(t, store, before, "client-new")
		store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
		if _, _, _, err := store.MigrateTunnelTargetByIDWithActivity(before.ID, before.Revision, replacement, actor); err == nil {
			t.Fatal("migration should fail when activity append fails")
		}
		reloaded, err := store.GetTunnelByIDE(before.ClientID, before.ID)
		if err != nil || reloaded.OwnerClientID != before.OwnerClientID || reloaded.Revision != before.Revision {
			t.Fatalf("migration rollback = %+v, %v", reloaded, err)
		}
	})
}

func TestTunnelActivityNoopAndStaleRevisionDoNotAppend(t *testing.T) {
	store := newTestTunnelStore(t)
	actor := ActivityActor{Type: "admin"}
	before := testStoredServerExposeTCPTunnel("activity-noop", "activity-noop", "client-noop", 8080, 18085, zeroTime())
	mustAddStableTunnel(t, store, before)

	unchanged, activityID, err := store.UpdateTunnelStatesWithActivity(before.ClientID, before.ID, before.DesiredState, before.RuntimeState, before.Error, "resumed", actor)
	if err != nil || activityID != 0 || unchanged.Revision != before.Revision {
		t.Fatalf("no-op state mutation = %+v id=%d err=%v", unchanged, activityID, err)
	}
	replacement := before
	replacement.Name = "should-not-apply"
	if _, err := store.ReplaceTunnelByIDWithActivity(before.ClientID, before.ID, before.Revision+1, replacement, actor); !errors.Is(err, ErrTunnelRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	maxID, err := store.activityStore.MaxID()
	if err != nil || maxID != 0 {
		t.Fatalf("no-op/stale mutation activity max id = %d, %v", maxID, err)
	}
}

func zeroTime() (value time.Time) { return value }
