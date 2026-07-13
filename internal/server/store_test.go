package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func newTestTunnelStore(t *testing.T) *TunnelStore {
	t.Helper()

	return newTestTunnelStoreAt(t, filepath.Join(t.TempDir(), serverDBFileName))
}

func TestTunnelStore_BackfillsExplicitAllowAllSourceCIDRs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)
	store := newTestTunnelStoreAt(t, path)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "tcp-backfill",
			Name:       "tcp-backfill",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  80,
			RemotePort: 18080,
		},
		ClientID: "client-1",
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "0.0.0.0", Port: 18080, AllowedSourceCIDRs: allowAllSourceCIDRs()}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "client-1",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 80}),
		},
		Revision: 7,
	})
	_ = store.Close()

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("open sqlite database failed: %v", err)
	}
	if _, err := db.Exec(`UPDATE tunnels SET ingress_config = ? WHERE id = ?`, `{"bind_ip":"0.0.0.0","port":18080}`, "tcp-backfill"); err != nil {
		t.Fatalf("simulate old ingress_config: %v", err)
	}
	_ = db.Close()

	reloaded := newTestTunnelStoreAt(t, path)
	stored, ok := reloaded.GetTunnel("client-1", "tcp-backfill")
	if !ok {
		t.Fatal("expected backfilled tunnel")
	}
	if stored.Revision != 7 {
		t.Fatalf("backfill should not bump revision, got %d", stored.Revision)
	}
	var cfg tcpListenConfigAPI
	if err := json.Unmarshal(stored.Ingress.Config, &cfg); err != nil {
		t.Fatalf("decode backfilled ingress config: %v", err)
	}
	if got, want := strings.Join(cfg.AllowedSourceCIDRs, ","), "0.0.0.0/0,::/0"; got != want {
		t.Fatalf("backfilled source CIDRs: got %q want %q", got, want)
	}

	raw := queryTunnelIngressConfig(t, path, "tcp-backfill")
	if !strings.Contains(raw, `"allowed_source_cidrs":["0.0.0.0/0","::/0"]`) {
		t.Fatalf("expected persisted explicit allow-all, got %s", raw)
	}
}

func TestTunnelStore_BackfillSOCKS5KeepsAuth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)
	store := newTestTunnelStoreAt(t, path)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:        "socks5-backfill",
			Name:      "socks5-backfill",
			Type:      protocol.ProxyTypeTCP,
			LocalIP:   "127.0.0.1",
			LocalPort: 0,
		},
		ClientID: "client-1",
		Topology: protocol.TunnelTopologyServerExpose,
		Revision: 3,
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeSOCKS5Listen,
			Config: mustRawJSON(protocol.SOCKS5ListenConfig{
				BindIP:             "127.0.0.1",
				Port:               19080,
				AllowedSourceCIDRs: allowAllSourceCIDRs(),
				Auth: protocol.SOCKS5AuthConfig{
					Type:         protocol.SOCKS5AuthTypeUsernamePassword,
					Username:     "alice",
					PasswordHash: "$argon2id$test",
				},
			}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "client-1",
			Type:     protocol.TargetTypeSOCKS5ConnectHandler,
			Config: mustRawJSON(protocol.SOCKS5ConnectHandlerConfig{
				AllowedTargetCIDRs: []string{"0.0.0.0/0", "::/0"},
				DialTimeoutSeconds: 10,
			}),
		},
	})
	_ = store.Close()

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("open sqlite database failed: %v", err)
	}
	oldConfig := `{"bind_ip":"127.0.0.1","port":19080,"auth":{"type":"username_password","username":"alice","password_hash":"$argon2id$test"}}`
	if _, err := db.Exec(`UPDATE tunnels SET ingress_config = ? WHERE id = ?`, oldConfig, "socks5-backfill"); err != nil {
		t.Fatalf("simulate old socks5 ingress_config: %v", err)
	}
	_ = db.Close()

	reloaded := newTestTunnelStoreAt(t, path)
	stored, ok := reloaded.GetTunnel("client-1", "socks5-backfill")
	if !ok {
		t.Fatal("expected backfilled tunnel")
	}
	var cfg protocol.SOCKS5ListenConfig
	if err := json.Unmarshal(stored.Ingress.Config, &cfg); err != nil {
		t.Fatalf("decode backfilled socks5 ingress config: %v", err)
	}
	if cfg.Auth.Username != "alice" || cfg.Auth.PasswordHash != "$argon2id$test" {
		t.Fatalf("SOCKS5 auth was not preserved: %+v", cfg.Auth)
	}
	if got, want := strings.Join(cfg.AllowedSourceCIDRs, ","), "0.0.0.0/0,::/0"; got != want {
		t.Fatalf("backfilled socks5 source CIDRs: got %q want %q", got, want)
	}
}

func queryTunnelIngressConfig(t *testing.T, path, id string) string {
	t.Helper()
	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("open raw sqlite database failed: %v", err)
	}
	defer func() { _ = db.Close() }()
	var raw string
	if err := db.QueryRow(`SELECT ingress_config FROM tunnels WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatalf("query ingress_config: %v", err)
	}
	return raw
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

func tunnelTargetMigrationReplacement(t *testing.T, store *TunnelStore, stored StoredTunnel, targetClientID string) StoredTunnel {
	t.Helper()
	mustRegisterTunnelMigrationTarget(t, store, targetClientID)
	replacement := stored
	replacement.ClientID = targetClientID
	replacement.OwnerClientID = targetClientID
	replacement.Target.ClientID = targetClientID
	replacement.Revision = stored.Revision + 1
	replacement.UpdatedAt = time.Time{}
	return replacement
}

func mustRegisterTunnelMigrationTarget(t *testing.T, store *TunnelStore, clientID string) {
	t.Helper()
	now := formatTime(time.Now().UTC())
	if _, err := store.db.Exec(`INSERT OR IGNORE INTO registered_clients (id, install_id, created_at, last_seen) VALUES (?, ?, ?, ?)`, clientID, "install-"+clientID, now, now); err != nil {
		t.Fatalf("register migration target %q: %v", clientID, err)
	}
}

func queryTunnelTargetResourceKey(t *testing.T, store *TunnelStore, id string) string {
	t.Helper()

	var key string
	if err := store.db.QueryRow(`SELECT target_resource_key FROM tunnels WHERE id = ?`, id).Scan(&key); err != nil {
		t.Fatalf("query target_resource_key: %v", err)
	}
	return key
}

func TestTunnelStore_NewEmpty(t *testing.T) {
	store := newTestTunnelStore(t)
	allTunnels, err := store.GetAllTunnels()
	if err != nil {
		t.Fatalf("GetAllTunnels failed: %v", err)
	}
	if len(allTunnels) != 0 {
		t.Errorf("new store should be empty, got %d records", len(allTunnels))
	}
}

func TestTunnelStore_GetTunnelsByClientIDOrdersNewestFirst(t *testing.T) {
	store := newTestTunnelStore(t)
	base := time.Date(2026, 5, 8, 1, 0, 0, 0, time.UTC)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "old", Name: "old", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 18080},
		ClientID:        "client-1",
		Hostname:        "host-1",
		CreatedAt:       base,
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "new", Name: "new", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 81, RemotePort: 18081},
		ClientID:        "client-1",
		Hostname:        "host-1",
		CreatedAt:       base.Add(time.Hour),
	})

	tunnels, err := store.GetTunnelsByClientID("client-1")
	if err != nil {
		t.Fatalf("GetTunnelsByClientID failed: %v", err)
	}
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}
	if tunnels[0].Name != "new" || tunnels[1].Name != "old" {
		t.Fatalf("tunnels should be ordered newest first, got %s then %s", tunnels[0].Name, tunnels[1].Name)
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
	defer func() { _ = db.Close() }()
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
	tunnels, err := store2.GetAllTunnels()
	if err != nil {
		t.Fatalf("GetAllTunnels failed: %v", err)
	}
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
				TotalBPS:   2500,
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
	if loaded.IngressBPS != 1000 || loaded.EgressBPS != 2000 || loaded.TotalBPS != 2500 {
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
	if updated.TotalBPS != 2500 {
		t.Fatalf("legacy directional update unexpectedly changed total_bps: %+v", updated.BandwidthSettings)
	}
}

func TestTunnelStore_UpdateTunnelByIDCanRenameTunnel(t *testing.T) {
	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "tunnel-1",
			Name:       "old-name",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  80,
			RemotePort: 8080,
		},
		ClientID: "client-1",
		Hostname: "host-1",
	})

	if err := store.UpdateTunnelByID("client-1", "tunnel-1", "new-name", "127.0.0.2", 81, 8081, "", 1024, 2048); err != nil {
		t.Fatalf("UpdateTunnelByID failed: %v", err)
	}
	if _, ok := store.GetTunnel("client-1", "old-name"); ok {
		t.Fatal("old tunnel name should no longer resolve after rename")
	}
	updated, ok := store.GetTunnel("client-1", "new-name")
	if !ok {
		t.Fatal("renamed tunnel should resolve by new name")
	}
	if updated.ID != "tunnel-1" {
		t.Fatalf("stable id changed after rename: %s", updated.ID)
	}
	if updated.LocalIP != "127.0.0.2" || updated.LocalPort != 81 || updated.RemotePort != 8081 {
		t.Fatalf("renamed tunnel did not persist updated mapping: %+v", updated.ProxyNewRequest)
	}
	if updated.IngressBPS != 1024 || updated.EgressBPS != 2048 {
		t.Fatalf("renamed tunnel did not persist bandwidth settings: %+v", updated.BandwidthSettings)
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

	tunnels, err := store.GetAllTunnels()
	if err != nil {
		t.Fatalf("GetAllTunnels failed: %v", err)
	}
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
	allTunnels, err := store.GetAllTunnels()
	if err != nil {
		t.Fatalf("GetAllTunnels failed: %v", err)
	}
	if len(allTunnels) != 2 {
		t.Error("should have 2 records")
	}
}

func TestTunnelStore_GetTunnelByID(t *testing.T) {
	store := newTestTunnelStore(t)
	tunnel := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "stable-id-1", Name: "web"},
		ClientID:        "client-A",
		Hostname:        "host-A",
	}
	mustAddStableTunnel(t, store, tunnel)

	got, err := store.GetTunnelByID("stable-id-1")
	if err != nil {
		t.Fatalf("GetTunnelByID failed: %v", err)
	}
	if got.ID != tunnel.ID || got.ClientID != tunnel.ClientID || got.Name != tunnel.Name {
		t.Fatalf("GetTunnelByID mismatch: %+v", got)
	}
	if _, err := store.GetTunnelByID("missing-id"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("missing tunnel should return ErrTunnelNotFound, got %v", err)
	}
}

func TestTunnelStore_ReplaceTunnelByIDRejectsClientIDOwnerChange(t *testing.T) {
	store := newTestTunnelStore(t)
	original := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "replace-owner-guard", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 18080},
		ClientID:        "client-old",
		Hostname:        "host-old",
	}
	mustAddStableTunnel(t, store, original)
	stored, err := store.GetTunnelByIDE("client-old", "replace-owner-guard")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}

	replacement := stored
	replacement.ClientID = "client-new"
	replacement.OwnerClientID = "client-new"
	replacement.Target.ClientID = "client-new"
	replacement.Revision = stored.Revision + 1

	err = store.ReplaceTunnelByID("client-old", stored.ID, stored.Revision, replacement)
	if err == nil || !strings.Contains(err.Error(), "replacement client_id cannot change") {
		t.Fatalf("ReplaceTunnelByID should reject client_id/owner migration, got %v", err)
	}
	reloaded, err := store.GetTunnelByIDE("client-old", stored.ID)
	if err != nil {
		t.Fatalf("original tunnel should remain under old owner: %v", err)
	}
	if reloaded.ClientID != stored.ClientID || reloaded.OwnerClientID != stored.OwnerClientID || reloaded.Target.ClientID != stored.Target.ClientID || reloaded.Revision != stored.Revision {
		t.Fatalf("rejected replacement mutated tunnel: %+v", reloaded)
	}
	if _, err := store.GetTunnelByIDE("client-new", stored.ID); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("rejected replacement should not create new owner row, got %v", err)
	}
}

func TestTunnelStore_MigrateTunnelTargetByIDRejectsUnregisteredTargetWithoutMutation(t *testing.T) {
	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "migrate-unregistered", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
		ClientID:        "client-old",
		Hostname:        "host-old",
	})
	stored, err := store.GetTunnelByIDE("client-old", "migrate-unregistered")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}
	replacement := stored
	replacement.ClientID = "client-missing"
	replacement.OwnerClientID = "client-missing"
	replacement.Target.ClientID = "client-missing"

	_, _, err = store.MigrateTunnelTargetByID(stored.ID, stored.Revision, replacement)
	if !errors.Is(err, ErrTunnelTargetClientNotFound) {
		t.Fatalf("MigrateTunnelTargetByID error = %v, want ErrTunnelTargetClientNotFound", err)
	}
	reloaded, err := store.GetTunnelByIDE("client-old", stored.ID)
	if err != nil {
		t.Fatalf("unregistered target rejection removed original tunnel: %v", err)
	}
	if reloaded.Revision != stored.Revision || reloaded.OwnerClientID != stored.OwnerClientID || reloaded.Target.ClientID != stored.Target.ClientID {
		t.Fatalf("unregistered target rejection mutated tunnel: %+v", reloaded)
	}
}

func TestTunnelStore_MigrateTunnelTargetByIDMovesOwnerAndPreservesConfig(t *testing.T) {
	store := newTestTunnelStore(t)
	original := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "migrate-happy",
			Name:       "web",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  8080,
			RemotePort: 18080,
			BindIP:     "127.0.0.1",
			BandwidthSettings: protocol.BandwidthSettings{
				IngressBPS: 1234,
				EgressBPS:  5678,
			},
		},
		ClientID:        "client-old",
		Hostname:        "host-old",
		CreatedByUserID: "user-1",
		TransportPolicy: TunnelTransportDirectPreferred,
	}
	mustAddStableTunnel(t, store, original)
	stored, err := store.GetTunnelByIDE("client-old", "migrate-happy")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}
	replacement := tunnelTargetMigrationReplacement(t, store, stored, "client-new")
	replacement.CreatedAt = stored.CreatedAt.Add(time.Hour)
	replacement.CreatedByUserID = "changed-user"
	replacement.Hostname = "changed-host"

	before, after, err := store.MigrateTunnelTargetByID(stored.ID, stored.Revision, replacement)
	if err != nil {
		t.Fatalf("MigrateTunnelTargetByID failed: %v", err)
	}
	if before.ID != stored.ID || before.ClientID != "client-old" || before.OwnerClientID != "client-old" || before.Target.ClientID != "client-old" {
		t.Fatalf("before tunnel mismatch: %+v", before)
	}
	if after.ID != stored.ID || after.ClientID != "client-new" || after.OwnerClientID != "client-new" || after.Target.ClientID != "client-new" {
		t.Fatalf("after tunnel did not move target owner: %+v", after)
	}
	if after.Revision != stored.Revision+1 {
		t.Fatalf("revision = %d, want %d", after.Revision, stored.Revision+1)
	}
	if !after.CreatedAt.Equal(stored.CreatedAt) || after.CreatedByUserID != stored.CreatedByUserID || after.Hostname != stored.Hostname || after.Binding != stored.Binding {
		t.Fatalf("returned migrated tunnel changed immutable fields: after=%+v before=%+v", after, stored)
	}
	if _, err := store.GetTunnelByIDE("client-old", stored.ID); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("old owner lookup should miss after migration, got %v", err)
	}
	reloaded, err := store.GetTunnelByIDE("client-new", stored.ID)
	if err != nil {
		t.Fatalf("new owner lookup should load migrated tunnel: %v", err)
	}
	if after.ClientID != reloaded.ClientID || after.OwnerClientID != reloaded.OwnerClientID || after.Target.ClientID != reloaded.Target.ClientID || after.Revision != reloaded.Revision || after.RuntimeState != reloaded.RuntimeState || after.ActualTransport != reloaded.ActualTransport || !after.UpdatedAt.Equal(reloaded.UpdatedAt) {
		t.Fatalf("returned after tunnel must match committed row: after=%+v reloaded=%+v", after, reloaded)
	}
	if reloaded.Name != stored.Name || reloaded.Ingress.Type != stored.Ingress.Type || string(reloaded.Ingress.Config) != string(stored.Ingress.Config) || string(reloaded.Target.Config) != string(stored.Target.Config) {
		t.Fatalf("migration should preserve name and endpoint config, got %+v want ingress %s target %s", reloaded, stored.Ingress.Config, stored.Target.Config)
	}
	if reloaded.RemotePort != stored.RemotePort || reloaded.LocalIP != stored.LocalIP || reloaded.LocalPort != stored.LocalPort || reloaded.IngressBPS != stored.IngressBPS || reloaded.EgressBPS != stored.EgressBPS || reloaded.TransportPolicy != stored.TransportPolicy || !reloaded.CreatedAt.Equal(stored.CreatedAt) || reloaded.CreatedByUserID != stored.CreatedByUserID {
		t.Fatalf("migration should preserve tunnel config, got %+v want %+v", reloaded, stored)
	}
	byID, err := store.GetTunnelByID(stored.ID)
	if err != nil {
		t.Fatalf("GetTunnelByID failed after migration: %v", err)
	}
	if byID.ClientID != "client-new" || byID.OwnerClientID != "client-new" || byID.Target.ClientID != "client-new" {
		t.Fatalf("stable id lookup should return migrated owner: %+v", byID)
	}
}

func TestTunnelStore_MigrateTunnelTargetByIDClearsAllTrafficLayersAndStartsNewRevisionAtZero(t *testing.T) {
	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "migrate-traffic", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
		ClientID:        "client-old",
		Hostname:        "host-old",
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "keep-traffic", Name: "keep", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8081, RemotePort: 18081},
		ClientID:        "client-other",
		Hostname:        "host-other",
	})
	stored, err := store.GetTunnelByIDE("client-old", "migrate-traffic")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}
	other, err := store.GetTunnelByIDE("client-other", "keep-traffic")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed for unaffected tunnel: %v", err)
	}

	trafficStore := newTrafficStoreWithDB(store.path, store.db, false)
	store.attachTrafficStore(trafficStore)
	now := secondFloorUTC(time.Now().UTC())
	oldDelta := trafficDeltaFromStoredTunnel(stored, 10, 4)
	oldDelta.SecondStart = now.Unix()
	oldDelta.MinuteStart = minuteFloorUTC(now).Unix()
	trafficStore.ApplyDeltas([]TrafficDelta{oldDelta})
	if err := trafficStore.Flush(); err != nil {
		t.Fatalf("flush durable traffic: %v", err)
	}
	oldPending := oldDelta
	oldPending.IngressBytes = 5
	oldPending.EgressBytes = 2
	otherDelta := trafficDeltaFromStoredTunnel(other, 21, 8)
	otherDelta.SecondStart = now.Unix()
	otherDelta.MinuteStart = minuteFloorUTC(now).Unix()
	trafficStore.ApplyDeltas([]TrafficDelta{oldPending, otherDelta})

	_, migrated, err := store.MigrateTunnelTargetByID(stored.ID, stored.Revision, tunnelTargetMigrationReplacement(t, store, stored, "client-new"))
	if err != nil {
		t.Fatalf("MigrateTunnelTargetByID failed: %v", err)
	}

	var durableCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM traffic_buckets WHERE tunnel_id = ?`, stored.ID).Scan(&durableCount); err != nil {
		t.Fatalf("count migrated traffic buckets: %v", err)
	}
	if durableCount != 0 {
		t.Fatalf("migrated durable traffic rows = %d, want 0", durableCount)
	}
	for _, resolution := range []TrafficResolution{TrafficResolutionSecond, TrafficResolutionMinute} {
		result := mustQueryWithResolution(t, trafficStore, "client-old", stored.ID, now.Add(-time.Minute), now.Add(time.Minute), resolution)
		if len(result.Items) != 0 {
			t.Fatalf("old-owner %s traffic should be empty after migration, got %+v", resolution, result.Items)
		}
	}
	if got := trafficStore.minimumRevisionByTunnel[stored.ID]; got != migrated.Revision {
		t.Fatalf("minimum accepted revision = %d, want %d", got, migrated.Revision)
	}

	lateOld := oldDelta
	lateOld.IngressBytes = 100
	lateOld.EgressBytes = 50
	trafficStore.ApplyDeltas([]TrafficDelta{lateOld})
	oldResult := mustQueryWithResolution(t, trafficStore, "client-old", stored.ID, now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	if len(oldResult.Items) != 0 {
		t.Fatalf("late old-revision traffic should be discarded, got %+v", oldResult.Items)
	}

	fresh := trafficDeltaFromStoredTunnel(migrated, 7, 3)
	fresh.SecondStart = now.Unix()
	fresh.MinuteStart = minuteFloorUTC(now).Unix()
	trafficStore.ApplyDeltas([]TrafficDelta{fresh})
	freshResult := mustQueryWithResolution(t, trafficStore, "client-new", migrated.ID, now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	freshSeries := mustSingleSeries(t, freshResult, migrated.Name)
	if len(freshSeries.Points) != 1 || freshSeries.Points[0].IngressBytes != 7 || freshSeries.Points[0].EgressBytes != 3 {
		t.Fatalf("new revision should start from zero, got %+v", freshSeries.Points)
	}
	otherResult := mustQueryWithResolution(t, trafficStore, "client-other", other.ID, now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	otherSeries := mustSingleSeries(t, otherResult, other.Name)
	if len(otherSeries.Points) != 1 || otherSeries.Points[0].IngressBytes != 21 || otherSeries.Points[0].EgressBytes != 8 {
		t.Fatalf("migration should not clear unrelated traffic, got %+v", otherSeries.Points)
	}
}

func TestTunnelStore_MigrateTunnelTargetByIDTrafficDeleteFailureRollsBackWithoutClearingMemory(t *testing.T) {
	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "migrate-traffic-rollback", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
		ClientID:        "client-old",
		Hostname:        "host-old",
	})
	stored, err := store.GetTunnelByIDE("client-old", "migrate-traffic-rollback")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}
	trafficStore := newTrafficStoreWithDB(store.path, store.db, false)
	store.attachTrafficStore(trafficStore)
	now := secondFloorUTC(time.Now().UTC())
	delta := trafficDeltaFromStoredTunnel(stored, 10, 4)
	delta.SecondStart = now.Unix()
	delta.MinuteStart = minuteFloorUTC(now).Unix()
	trafficStore.ApplyDeltas([]TrafficDelta{delta})
	if err := trafficStore.Flush(); err != nil {
		t.Fatalf("flush durable traffic: %v", err)
	}
	pending := delta
	pending.IngressBytes = 5
	pending.EgressBytes = 2
	trafficStore.ApplyDeltas([]TrafficDelta{pending})
	if _, err := store.db.Exec(`CREATE TRIGGER reject_migration_traffic_delete
		BEFORE DELETE ON traffic_buckets
		WHEN OLD.tunnel_id = 'migrate-traffic-rollback'
		BEGIN
			SELECT RAISE(ABORT, 'blocked traffic delete');
		END`); err != nil {
		t.Fatalf("create traffic delete failure trigger: %v", err)
	}

	_, _, err = store.MigrateTunnelTargetByID(stored.ID, stored.Revision, tunnelTargetMigrationReplacement(t, store, stored, "client-new"))
	if err == nil || !strings.Contains(err.Error(), "blocked traffic delete") {
		t.Fatalf("MigrateTunnelTargetByID error = %v, want blocked traffic delete", err)
	}
	reloaded, err := store.GetTunnelByIDE("client-old", stored.ID)
	if err != nil {
		t.Fatalf("failed migration should keep old owner: %v", err)
	}
	if reloaded.Revision != stored.Revision || reloaded.OwnerClientID != stored.OwnerClientID || reloaded.Target.ClientID != stored.Target.ClientID {
		t.Fatalf("failed migration mutated tunnel: %+v", reloaded)
	}
	if _, ok := trafficStore.minimumRevisionByTunnel[stored.ID]; ok {
		t.Fatalf("failed migration should not advance minimum traffic revision: %+v", trafficStore.minimumRevisionByTunnel)
	}
	result := mustQueryWithResolution(t, trafficStore, "client-old", stored.ID, now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	series := mustSingleSeries(t, result, stored.Name)
	if len(series.Points) != 1 || series.Points[0].IngressBytes != 15 || series.Points[0].EgressBytes != 6 {
		t.Fatalf("failed migration should preserve durable and pending traffic, got %+v", series.Points)
	}
}

func TestTunnelStore_MigrateTunnelTargetByIDRejectsRevisionConflictWithoutMutation(t *testing.T) {
	store := newTestTunnelStore(t)
	original := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "migrate-revision-conflict", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
		ClientID:        "client-old",
		Hostname:        "host-old",
	}
	mustAddStableTunnel(t, store, original)
	stored, err := store.GetTunnelByIDE("client-old", "migrate-revision-conflict")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}
	replacement := tunnelTargetMigrationReplacement(t, store, stored, "client-new")

	_, _, err = store.MigrateTunnelTargetByID(stored.ID, stored.Revision+1, replacement)
	if !errors.Is(err, ErrTunnelRevisionConflict) {
		t.Fatalf("MigrateTunnelTargetByID should reject stale revision, got %v", err)
	}
	reloaded, err := store.GetTunnelByIDE("client-old", stored.ID)
	if err != nil {
		t.Fatalf("original tunnel should remain under old owner: %v", err)
	}
	if reloaded.ClientID != stored.ClientID || reloaded.OwnerClientID != stored.OwnerClientID || reloaded.Target.ClientID != stored.Target.ClientID || reloaded.Revision != stored.Revision {
		t.Fatalf("revision conflict mutated tunnel: %+v", reloaded)
	}
	if _, err := store.GetTunnelByIDE("client-new", stored.ID); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("revision conflict should not create new owner row, got %v", err)
	}
}

func TestTunnelStore_MigrateTunnelTargetByIDRejectsPendingInsideStoreLock(t *testing.T) {
	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "migrate-pending-store", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
		ClientID:        "client-old",
		Hostname:        "host-old",
		RuntimeState:    protocol.ProxyRuntimeStatePending,
	})
	stored, err := store.GetTunnelByIDE("client-old", "migrate-pending-store")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}

	_, _, err = store.MigrateTunnelTargetByID(stored.ID, stored.Revision, tunnelTargetMigrationReplacement(t, store, stored, "client-new"))
	if !errors.Is(err, ErrTunnelMigrationPending) {
		t.Fatalf("MigrateTunnelTargetByID pending error = %v, want ErrTunnelMigrationPending", err)
	}
	reloaded, err := store.GetTunnelByIDE("client-old", stored.ID)
	if err != nil {
		t.Fatalf("pending migration should keep old owner: %v", err)
	}
	if reloaded.Revision != stored.Revision || reloaded.RuntimeState != protocol.ProxyRuntimeStatePending {
		t.Fatalf("pending migration mutated tunnel: %+v", reloaded)
	}
}

func TestTunnelStore_MigrateTunnelTargetByIDRejectsNewOwnerNameConflictWithoutDeletingOriginal(t *testing.T) {
	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "migrate-name-conflict-original", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
		ClientID:        "client-old",
		Hostname:        "host-old",
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "migrate-name-conflict-existing", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8081, RemotePort: 18081},
		ClientID:        "client-new",
		Hostname:        "host-new",
	})
	stored, err := store.GetTunnelByIDE("client-old", "migrate-name-conflict-original")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}
	replacement := tunnelTargetMigrationReplacement(t, store, stored, "client-new")

	_, _, err = store.MigrateTunnelTargetByID(stored.ID, stored.Revision, replacement)
	if !errors.Is(err, ErrTunnelOwnerNameConflict) {
		t.Fatalf("MigrateTunnelTargetByID name conflict error = %v, want ErrTunnelOwnerNameConflict", err)
	}
	reloaded, err := store.GetTunnelByIDE("client-old", stored.ID)
	if err != nil {
		t.Fatalf("original tunnel should remain under old owner: %v", err)
	}
	if reloaded.ClientID != stored.ClientID || reloaded.OwnerClientID != stored.OwnerClientID || reloaded.Target.ClientID != stored.Target.ClientID || reloaded.Revision != stored.Revision {
		t.Fatalf("name conflict mutated original tunnel: %+v", reloaded)
	}
	conflict, err := store.GetTunnelByIDE("client-new", "migrate-name-conflict-existing")
	if err != nil {
		t.Fatalf("conflicting tunnel should remain under new owner: %v", err)
	}
	if conflict.Name != "web" || conflict.ID != "migrate-name-conflict-existing" {
		t.Fatalf("conflicting tunnel mutated: %+v", conflict)
	}
}

func TestTunnelStore_MigrateTunnelTargetByIDRefreshesTargetResourceLock(t *testing.T) {
	store := newTestTunnelStore(t)
	original := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "migrate-target-resource", Name: "web", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
		ClientID:        "client-old",
		Hostname:        "host-old",
	}
	mustAddStableTunnel(t, store, original)
	stored, err := store.GetTunnelByIDE("client-old", "migrate-target-resource")
	if err != nil {
		t.Fatalf("GetTunnelByIDE failed: %v", err)
	}
	oldKey := queryTunnelTargetResourceKey(t, store, stored.ID)
	replacement := tunnelTargetMigrationReplacement(t, store, stored, "client-new")

	_, after, err := store.MigrateTunnelTargetByID(stored.ID, stored.Revision, replacement)
	if err != nil {
		t.Fatalf("MigrateTunnelTargetByID failed: %v", err)
	}
	newKey := queryTunnelTargetResourceKey(t, store, stored.ID)
	wantKey := "target:client:client-new:tcp_service:127.0.0.1:8080"
	if oldKey == newKey || newKey != wantKey {
		t.Fatalf("target_resource_key = %q, old %q, want %q", newKey, oldKey, wantKey)
	}
	reloaded, err := store.GetTunnelByIDE("client-new", stored.ID)
	if err != nil {
		t.Fatalf("new owner lookup should load migrated tunnel: %v", err)
	}
	if reloaded.Target.ClientID != "client-new" || after.Target.ClientID != "client-new" {
		t.Fatalf("reloaded target client mismatch: after=%+v reloaded=%+v", after, reloaded)
	}
	var lockCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM tunnel_resource_locks WHERE tunnel_id = ?`, stored.ID).Scan(&lockCount); err != nil {
		t.Fatalf("count resource locks: %v", err)
	}
	if lockCount != 1 {
		t.Fatalf("resource lock count = %d, want 1", lockCount)
	}
}

func TestTunnelStore_DeleteTunnelsByClientIDDeletesAnyClientParticipation(t *testing.T) {
	store := newTestTunnelStore(t)
	for _, tunnel := range []StoredTunnel{
		{
			ProxyNewRequest: protocol.ProxyNewRequest{ID: "delete-target-participant", Name: "target", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8080, RemotePort: 18080},
			ClientID:        "delete-client",
			Hostname:        "target-host",
		},
		{
			ProxyNewRequest: protocol.ProxyNewRequest{ID: "delete-ingress-participant", Name: "relay", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8081},
			ClientID:        "target-owner",
			OwnerClientID:   "target-owner",
			Topology:        TunnelTopologyClientToClient,
			Ingress:         EndpointSpec{Location: protocol.EndpointLocationClient, ClientID: "delete-client", Type: TunnelIngressTypeTCPListen, Config: mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: 18081, AllowedSourceCIDRs: allowAllSourceCIDRs()})},
			Target:          EndpointSpec{Location: protocol.EndpointLocationClient, ClientID: "target-owner", Type: TunnelTargetTypeTCPService, Config: mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 8081})},
			TransportPolicy: TunnelTransportServerRelayOnly,
		},
		{
			ProxyNewRequest: protocol.ProxyNewRequest{ID: "keep-unrelated", Name: "keep", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 8082, RemotePort: 18082},
			ClientID:        "unrelated",
			Hostname:        "unrelated-host",
		},
	} {
		mustAddStableTunnel(t, store, tunnel)
	}

	if err := store.DeleteTunnelsByClientID("delete-client"); err != nil {
		t.Fatalf("DeleteTunnelsByClientID failed: %v", err)
	}
	for _, id := range []string{"delete-target-participant", "delete-ingress-participant"} {
		if _, err := store.GetTunnelByID(id); !errors.Is(err, ErrTunnelNotFound) {
			t.Fatalf("participating tunnel %q still exists: %v", id, err)
		}
	}
	if _, err := store.GetTunnelByID("keep-unrelated"); err != nil {
		t.Fatalf("unrelated tunnel was deleted: %v", err)
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
	allTunnels, err := store.GetAllTunnels()
	if err != nil {
		t.Fatalf("GetAllTunnels failed: %v", err)
	}
	if len(allTunnels) != 0 {
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

func TestTunnelStore_TransitionRuntimeStateIfCurrentRequiresExpectedRuntime(t *testing.T) {
	store := newTestTunnelStore(t)
	stored := testStoredServerExposeTCPTunnel("runtime-transition-id", "runtime-transition", "client-1", 8080, 18080, time.Now().UTC())
	stored.Revision = 4
	stored.RuntimeState = protocol.ProxyRuntimeStatePending
	stored.ActualTransport = protocol.ActualTransportUnknown
	mustAddStableTunnel(t, store, stored)

	updated, err := store.TransitionRuntimeStateIfCurrent(
		stored.OwnerClientID,
		stored.ID,
		stored.Revision,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStateOffline,
		protocol.ProxyRuntimeStateExposed,
		"",
	)
	if err != nil {
		t.Fatalf("mismatched runtime transition: %v", err)
	}
	if updated {
		t.Fatal("transition should not update when expected runtime state mismatches")
	}

	updated, err = store.TransitionRuntimeStateIfCurrent(
		stored.OwnerClientID,
		stored.ID,
		stored.Revision,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStatePending,
		protocol.ProxyRuntimeStateExposed,
		"",
	)
	if err != nil || !updated {
		t.Fatalf("pending to exposed transition: updated=%v err=%v", updated, err)
	}

	if _, err := store.UpdateStatesIfCurrent(
		stored.OwnerClientID,
		stored.ID,
		stored.Revision,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStateError,
		"runtime failed",
	); err != nil {
		t.Fatalf("seed runtime error: %v", err)
	}
	updated, err = store.TransitionRuntimeStateIfCurrent(
		stored.OwnerClientID,
		stored.ID,
		stored.Revision,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStatePending,
		protocol.ProxyRuntimeStateExposed,
		"",
	)
	if err != nil {
		t.Fatalf("late activation transition: %v", err)
	}
	if updated {
		t.Fatal("late activation must not overwrite a same-revision runtime error")
	}
	reloaded, err := store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("reload runtime transition tunnel: %v", err)
	}
	if reloaded.RuntimeState != protocol.ProxyRuntimeStateError || reloaded.Error != "runtime failed" {
		t.Fatalf("runtime error was overwritten: %+v", reloaded)
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

func TestTunnelStore_GetTunnelE_DistinguishesNotFoundAndStorageError(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "find-me", RemotePort: 9090},
		ClientID:        "client-1",
		Hostname:        "host",
	})

	st, err := store.GetTunnelE("client-1", "find-me")
	if err != nil {
		t.Fatalf("GetTunnelE returned error: %v", err)
	}
	if st.RemotePort != 9090 {
		t.Fatalf("RemotePort = %d, want 9090", st.RemotePort)
	}

	if _, err := store.GetTunnelE("client-1", "not-exist"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("missing tunnel error = %v, want ErrTunnelNotFound", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := store.GetTunnelE("client-1", "find-me"); err == nil || errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("closed store error = %v, want storage error distinct from ErrTunnelNotFound", err)
	}
	if _, found := store.GetTunnel("client-1", "find-me"); found {
		t.Fatal("best-effort GetTunnel wrapper should report not found on storage error")
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

	result, err := store.GetTunnelsByHostname("host-A")
	if err != nil {
		t.Fatalf("GetTunnelsByHostname failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}

	empty, err := store.GetTunnelsByHostname("no-host")
	if err != nil {
		t.Fatalf("GetTunnelsByHostname failed: %v", err)
	}
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

	result, err := store.GetAllTunnels()
	if err != nil {
		t.Fatalf("GetAllTunnels failed: %v", err)
	}
	result[0].Name = "mutated"

	original, err := store.GetAllTunnels()
	if err != nil {
		t.Fatalf("GetAllTunnels failed: %v", err)
	}
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
			_, _ = store.GetAllTunnels()
			_, _ = store.GetTunnelsByHostname(hostname)
			_, _ = store.GetTunnelsByClientID(clientID)
		}(i)
	}
	wg.Wait()
}
