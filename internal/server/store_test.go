package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"netsgo/pkg/protocol"
)

func newTestTunnelStore(t *testing.T) *TunnelStore {
	t.Helper()

	return newTestTunnelStoreAt(t, filepath.Join(t.TempDir(), serverDBFileName))
}

func newTestTunnelStoreAt(t *testing.T, path string) *TunnelStore {
	t.Helper()

	store, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("NewTunnelStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustAddStableTunnel(t *testing.T, store *TunnelStore, tunnel StoredTunnel) {
	t.Helper()

	tunnel.Binding = TunnelBindingClientID
	if tunnel.ClientID == "" {
		t.Fatal("test tunnel must provide ClientID")
	}
	if tunnel.DesiredState == "" {
		tunnel.DesiredState = protocol.ProxyDesiredStateRunning
	}
	if tunnel.RuntimeState == "" {
		tunnel.RuntimeState = protocol.ProxyRuntimeStateExposed
	}
	if err := store.AddTunnel(tunnel); err != nil {
		t.Fatalf("AddTunnel failed: %v", err)
	}
}

func TestTunnelStore_NewEmpty(t *testing.T) {
	store := newTestTunnelStore(t)
	if len(store.GetAllTunnels()) != 0 {
		t.Errorf("new store should be empty, got %d records", len(store.GetAllTunnels()))
	}
}

func TestTunnelStore_UsesSQLiteAndNoJsonFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)
	store := newTestTunnelStoreAt(t, path)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 18080},
		ClientID:        "client-1",
		Hostname:        "host-1",
	})

	if _, err := os.Stat(filepath.Join(dir, "tunnels.json")); !os.IsNotExist(err) {
		t.Fatalf("tunnels.json should not exist, stat error = %v", err)
	}
	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("open sqlite database failed: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tunnels WHERE client_id = ? AND name = ?`, "client-1", "web").Scan(&count); err != nil {
		t.Fatalf("query sqlite tunnel failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("sqlite tunnels row count = %d, want 1", count)
	}

	reloaded := newTestTunnelStoreAt(t, path)
	stored, ok := reloaded.GetTunnel("client-1", "web")
	if !ok {
		t.Fatal("expected reloaded tunnel")
	}
	if stored.RemotePort != 18080 {
		t.Fatalf("RemotePort = %d, want 18080", stored.RemotePort)
	}
}

func TestTunnelStore_LoadExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)

	store1 := newTestTunnelStoreAt(t, path)
	mustAddStableTunnel(t, store1, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name: "t1", Type: "tcp", LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 8080,
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		ClientID:     "client-1",
		Hostname:     "host-1",
	})

	store2 := newTestTunnelStoreAt(t, path)
	tunnels := store2.GetAllTunnels()
	if len(tunnels) != 1 {
		t.Fatalf("expected to load 1 record, got %d", len(tunnels))
	}
	if tunnels[0].Name != "t1" {
		t.Errorf("expected loaded tunnel name to be t1, got %s", tunnels[0].Name)
	}
	if tunnels[0].Binding != TunnelBindingClientID {
		t.Errorf("expected Binding to be %s, got %s", TunnelBindingClientID, tunnels[0].Binding)
	}
}

func TestTunnelStore_BandwidthSettingsRoundTripAndUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)

	store1 := newTestTunnelStoreAt(t, path)
	mustAddStableTunnel(t, store1, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name:       "limited",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  80,
			RemotePort: 8080,
			BandwidthSettings: protocol.BandwidthSettings{
				IngressBPS: 1000,
				EgressBPS:  2000,
			},
		},
		ClientID: "client-1",
		Hostname: "host-1",
	})

	store2 := newTestTunnelStoreAt(t, path)
	loaded, ok := store2.GetTunnel("client-1", "limited")
	if !ok {
		t.Fatal("should find reloaded tunnel")
	}
	if loaded.IngressBPS != 1000 || loaded.EgressBPS != 2000 {
		t.Fatalf("stored bandwidth settings did not round-trip: %+v", loaded.BandwidthSettings)
	}

	if err := store2.UpdateTunnel("client-1", "limited", "127.0.0.2", 81, 8081, "", 0, 0); err != nil {
		t.Fatalf("UpdateTunnel failed: %v", err)
	}
	store3 := newTestTunnelStoreAt(t, path)
	updated, ok := store3.GetTunnel("client-1", "limited")
	if !ok {
		t.Fatal("should find updated tunnel")
	}
	if updated.IngressBPS != 0 || updated.EgressBPS != 0 {
		t.Fatalf("explicit zero bandwidth settings should persist as unlimited: %+v", updated.BandwidthSettings)
	}
}

func TestTunnelStore_LoadExistingStatesKeepsDesiredAndRuntimeState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)

	store1 := newTestTunnelStoreAt(t, path)
	mustAddStableTunnel(t, store1, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name: "legacy-active", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 8080,
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		ClientID:     "client-1",
		Hostname:     "host-1",
	})
	mustAddStableTunnel(t, store1, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name: "legacy-error", Type: protocol.ProxyTypeUDP, LocalIP: "127.0.0.1", LocalPort: 53, RemotePort: 8053,
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateError,
		Error:        "restore failed",
		ClientID:     "client-1",
		Hostname:     "host-1",
	})

	store2 := newTestTunnelStoreAt(t, path)

	active, ok := store2.GetTunnel("client-1", "legacy-active")
	if !ok {
		t.Fatal("should find legacy-active")
	}
	if active.DesiredState != protocol.ProxyDesiredStateRunning {
		t.Fatalf("legacy active desired_state expected running, got %s", active.DesiredState)
	}
	if active.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("legacy active runtime_state expected exposed, got %s", active.RuntimeState)
	}

	errored, ok := store2.GetTunnel("client-1", "legacy-error")
	if !ok {
		t.Fatal("should find legacy-error")
	}
	if errored.DesiredState != protocol.ProxyDesiredStateRunning {
		t.Fatalf("legacy error desired_state expected running, got %s", errored.DesiredState)
	}
	if errored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("legacy error runtime_state expected error, got %s", errored.RuntimeState)
	}
	if errored.Error != "restore failed" {
		t.Fatalf("legacy error error_reason expected to retain 'restore failed', got %q", errored.Error)
	}
}

func TestTunnelStore_AddTunnelRejectsNonClientIDBinding(t *testing.T) {
	store := newTestTunnelStore(t)

	err := store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "legacy-tunnel"},
		ClientID:        "client-1",
		Hostname:        "legacy-host",
		Binding:         "legacy_hostname",
		DesiredState:    protocol.ProxyDesiredStateStopped,
		RuntimeState:    protocol.ProxyRuntimeStateIdle,
	})
	if err == nil {
		t.Fatal("non-client_id binding should be rejected")
	}
}

func TestTunnelStore_PausedDesiredStateCanonicalizesToStopped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)
	store := newTestTunnelStoreAt(t, path)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "paused", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
		ClientID:        "client-1",
		Hostname:        "host-1",
		DesiredState:    "paused",
		RuntimeState:    protocol.ProxyRuntimeStateIdle,
	})

	reloaded := newTestTunnelStoreAt(t, path)
	stored, ok := reloaded.GetTunnel("client-1", "paused")
	if !ok {
		t.Fatal("should load paused tunnel")
	}
	if stored.DesiredState != protocol.ProxyDesiredStateStopped || stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("paused tunnel should canonicalize to stopped/idle, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
}

func TestTunnelStore_CorruptedSQLiteDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)

	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("failed to write corrupted SQLite database: %v", err)
	}

	if store, err := NewTunnelStore(path); err == nil {
		t.Cleanup(func() { _ = store.Close() })
		t.Fatal("corrupted SQLite database should cause NewTunnelStore to return an error")
	}
}

func TestTunnelStore_CloseIsIdempotent(t *testing.T) {
	store := newTestTunnelStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestTunnelStore_AddTunnel_Success(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web", RemotePort: 8080},
		ClientID:        "client-1",
		Hostname:        "myhost",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	})

	tunnels := store.GetAllTunnels()
	if len(tunnels) != 1 {
		t.Fatalf("expected 1, got %d", len(tunnels))
	}
	if tunnels[0].ClientID != "client-1" {
		t.Errorf("expected ClientID to be client-1, got %s", tunnels[0].ClientID)
	}
	if tunnels[0].Binding != TunnelBindingClientID {
		t.Errorf("expected Binding to be %s, got %s", TunnelBindingClientID, tunnels[0].Binding)
	}
}

func TestTunnelStore_AddTunnel_DuplicateRejected(t *testing.T) {
	store := newTestTunnelStore(t)

	tunnel := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "dup"},
		ClientID:        "client-1",
		Hostname:        "host-1",
		Binding:         TunnelBindingClientID,
	}
	mustAddStableTunnel(t, store, tunnel)

	if err := store.AddTunnel(tunnel); err == nil {
		t.Error("duplicate addition with same client_id+name should be rejected")
	}
}

func TestTunnelStore_AddTunnel_DiffClientSameNameAllowed(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web"},
		ClientID:        "client-A",
		Hostname:        "host-A",
	})
	if err := store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web"},
		ClientID:        "client-B",
		Hostname:        "host-B",
		Binding:         TunnelBindingClientID,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	}); err != nil {
		t.Errorf("same name with different client_id should be allowed: %v", err)
	}
	if len(store.GetAllTunnels()) != 2 {
		t.Error("should have 2 records")
	}
}

func TestTunnelStore_RemoveTunnel_Success(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "rm-me"},
		ClientID:        "client-1",
		Hostname:        "host",
	})

	if err := store.RemoveTunnel("client-1", "rm-me"); err != nil {
		t.Fatalf("RemoveTunnel failed: %v", err)
	}
	if len(store.GetAllTunnels()) != 0 {
		t.Error("should be empty after deletion")
	}
}

func TestTunnelStore_RemoveTunnel_NotFound(t *testing.T) {
	store := newTestTunnelStore(t)
	if err := store.RemoveTunnel("ghost", "not-exist"); err == nil {
		t.Error("deleting non-existent tunnel should return an error")
	}
}

func TestTunnelStore_UpdateStates(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
		ClientID:        "client-1",
		Hostname:        "host",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	})

	if err := store.UpdateStates("client-1", "t1", protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		t.Fatalf("UpdateStates failed: %v", err)
	}
	st, _ := store.GetTunnel("client-1", "t1")
	if st.DesiredState != protocol.ProxyDesiredStateStopped || st.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("expected state stopped/idle, got %s/%s", st.DesiredState, st.RuntimeState)
	}

	if err := store.UpdateStates("client-1", "t1", protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		t.Fatalf("UpdateStates failed: %v", err)
	}
	st2, _ := store.GetTunnel("client-1", "t1")
	if st2.DesiredState != protocol.ProxyDesiredStateStopped || st2.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("expected state stopped/idle, got %s/%s", st2.DesiredState, st2.RuntimeState)
	}
}

func TestTunnelStore_UpdateStates_NotFound(t *testing.T) {
	store := newTestTunnelStore(t)
	if err := store.UpdateStates("ghost", "no-tunnel", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); err == nil {
		t.Error("updating non-existent tunnel should return an error")
	}
}

func TestTunnelStore_UpdateStates_NotFoundBeforeInvalidState(t *testing.T) {
	store := newTestTunnelStore(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("UpdateStates should return not-found before validating invalid state, panicked: %v", r)
		}
	}()

	if err := store.UpdateStates("ghost", "no-tunnel", "bad-desired", "bad-runtime", ""); err == nil {
		t.Fatal("updating non-existent tunnel should return an error")
	}
}

func TestTunnelStore_UpdateState_ErrorRoundTrip(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t-error"},
		ClientID:        "client-1",
		Hostname:        "host",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	})

	if err := store.UpdateStates("client-1", "t-error", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "restore failed"); err != nil {
		t.Fatalf("UpdateStates setting error failed: %v", err)
	}
	st, _ := store.GetTunnel("client-1", "t-error")
	if st.DesiredState != protocol.ProxyDesiredStateRunning || st.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("expected state running/error, got %s/%s", st.DesiredState, st.RuntimeState)
	}
	if st.Error != "restore failed" {
		t.Fatalf("expected error reason %q, got %q", "restore failed", st.Error)
	}

	if err := store.UpdateStates("client-1", "t-error", protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		t.Fatalf("UpdateStates clearing error failed: %v", err)
	}
	st, _ = store.GetTunnel("client-1", "t-error")
	if st.DesiredState != protocol.ProxyDesiredStateStopped || st.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("expected state stopped/idle, got %s/%s", st.DesiredState, st.RuntimeState)
	}
	if st.Error != "" {
		t.Fatalf("error reason should be cleared in stopped state, got %q", st.Error)
	}
}

func TestTunnelStore_UpdateState_RollbackOnSaveFailure(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t-rollback"},
		ClientID:        "client-1",
		Hostname:        "host",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	})

	store.mu.Lock()
	store.failSaveErr = errors.New("injected save failure")
	store.failSaveCount = 1
	store.mu.Unlock()

	if err := store.UpdateStates("client-1", "t-rollback", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "boom"); err == nil {
		t.Fatal("expected UpdateStates to return error when injected save fails")
	}

	st, _ := store.GetTunnel("client-1", "t-rollback")
	if st.DesiredState != protocol.ProxyDesiredStateRunning || st.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("state should rollback to running/exposed when save fails, got %s/%s", st.DesiredState, st.RuntimeState)
	}
	if st.Error != "" {
		t.Fatalf("error field should remain empty when save fails, got %q", st.Error)
	}

	if err := store.UpdateStates("client-1", "t-rollback", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "boom"); err != nil {
		t.Fatalf("UpdateStates should succeed after injected failures are exhausted, got: %v", err)
	}
	st, _ = store.GetTunnel("client-1", "t-rollback")
	if st.DesiredState != protocol.ProxyDesiredStateRunning || st.RuntimeState != protocol.ProxyRuntimeStateError || st.Error != "boom" {
		t.Fatalf("final state should be running/error + boom, got state=%s/%s error=%q", st.DesiredState, st.RuntimeState, st.Error)
	}
}

func TestTunnelStore_GetTunnel(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "find-me", RemotePort: 9090},
		ClientID:        "client-1",
		Hostname:        "host",
	})

	st, found := store.GetTunnel("client-1", "find-me")
	if !found {
		t.Fatal("should find tunnel")
	}
	if st.RemotePort != 9090 {
		t.Errorf("expected RemotePort to be 9090, got %d", st.RemotePort)
	}

	if _, found := store.GetTunnel("client-1", "not-exist"); found {
		t.Error("non-existent tunnel should not be found")
	}
}

func TestTunnelStore_GetTunnelsByHostname(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
		ClientID:        "client-1",
		Hostname:        "host-A",
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t2"},
		ClientID:        "client-2",
		Hostname:        "host-A",
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t3"},
		ClientID:        "client-3",
		Hostname:        "host-B",
	})

	result := store.GetTunnelsByHostname("host-A")
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}

	empty := store.GetTunnelsByHostname("no-host")
	if len(empty) != 0 {
		t.Errorf("non-existent host should return empty, got %d", len(empty))
	}
}

func TestTunnelStore_GetAllTunnels_ReturnsCopy(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "original"},
		ClientID:        "client-1",
		Hostname:        "host",
	})

	result := store.GetAllTunnels()
	result[0].Name = "mutated"

	original := store.GetAllTunnels()
	if original[0].Name != "original" {
		t.Error("GetAllTunnels should return a copy, modifications should not affect original data")
	}
}

func TestTunnelStore_ConcurrentAccess(t *testing.T) {
	store := newTestTunnelStore(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("tunnel-%d", idx)
			hostname := fmt.Sprintf("host-%d", idx%5)
			clientID := fmt.Sprintf("client-%d", idx)
			_ = store.AddTunnel(StoredTunnel{
				ProxyNewRequest: protocol.ProxyNewRequest{Name: name},
				ClientID:        clientID,
				Hostname:        hostname,
				Binding:         TunnelBindingClientID,
				DesiredState:    protocol.ProxyDesiredStateRunning,
				RuntimeState:    protocol.ProxyRuntimeStateExposed,
			})
			store.GetAllTunnels()
			store.GetTunnelsByHostname(hostname)
			store.GetTunnelsByClientID(clientID)
		}(i)
	}
	wg.Wait()
}
