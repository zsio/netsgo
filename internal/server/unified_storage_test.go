package server

import (
	"strings"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestUnifiedTunnelStorageRejectsFutureOnlyTargetTypes(t *testing.T) {
	db, err := openServerDB(t.TempDir() + "/netsgo.db")
	if err != nil {
		t.Fatalf("openServerDB failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	base := validUnifiedTunnelInsertArgs("future-target")
	for _, targetType := range []string{"unix_socket", "static_file", "serial_device"} {
		t.Run(targetType, func(t *testing.T) {
			args := append([]any(nil), base...)
			args[23] = targetType // target_type
			if _, err := db.Exec(unifiedTunnelInsertSQL(), args...); err == nil {
				t.Fatalf("target_type=%s should be rejected by DB CHECK", targetType)
			}
		})
	}
}

func TestTunnelStoreResourceLockConflictRollsBackTunnelInsert(t *testing.T) {
	store := newTestTunnelStore(t)

	first := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "tunnel-1", Name: "first", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 18080},
		ClientID:        "client-1",
		Hostname:        "host-1",
	}
	mustAddStableTunnel(t, store, first)

	second := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "tunnel-2", Name: "second", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 81, RemotePort: 18080},
		ClientID:        "client-1",
		Hostname:        "host-1",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	}
	err := store.AddTunnel(second)
	if err == nil {
		t.Fatal("duplicate server listen resource lock should reject insert")
	}
	if !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("expected SQLite constraint error, got %v", err)
	}

	if _, ok := store.GetTunnel("client-1", "second"); ok {
		t.Fatal("failed resource-lock insert should roll back tunnel row")
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM tunnel_resource_locks`).Scan(&count); err != nil {
		t.Fatalf("count resource locks: %v", err)
	}
	if count != 1 {
		t.Fatalf("resource lock count = %d, want 1", count)
	}
}

func TestTunnelStoreHardDeleteRemovesResourceLocksButKeepsTraffic(t *testing.T) {
	path := t.TempDir() + "/netsgo.db"
	store := newTestTunnelStoreAt(t, path)
	trafficStore, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("NewTrafficStore failed: %v", err)
	}
	t.Cleanup(func() { _ = trafficStore.Close() })

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: "tunnel-keep-traffic", Name: "delete-me", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 18080},
		ClientID:        "client-1",
		Hostname:        "host-1",
	})

	now := minuteFloorUTC(time.Now().UTC())
	trafficStore.ApplyDeltas([]TrafficDelta{{ClientID: "client-1", TunnelName: "delete-me", TunnelType: protocol.ProxyTypeTCP, MinuteStart: now.Unix(), IngressBytes: 10, EgressBytes: 5}})
	if err := trafficStore.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if err := store.RemoveTunnel("client-1", "delete-me"); err != nil {
		t.Fatalf("RemoveTunnel failed: %v", err)
	}
	if _, ok := store.GetTunnel("client-1", "delete-me"); ok {
		t.Fatal("tunnel row should be hard-deleted")
	}
	var lockCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM tunnel_resource_locks WHERE tunnel_id = ?`, "tunnel-keep-traffic").Scan(&lockCount); err != nil {
		t.Fatalf("count resource locks: %v", err)
	}
	if lockCount != 0 {
		t.Fatalf("resource locks should be deleted with tunnel, got %d", lockCount)
	}
	var trafficCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM traffic_buckets WHERE tunnel_id = ?`, "tunnel-keep-traffic").Scan(&trafficCount); err != nil {
		t.Fatalf("count traffic buckets: %v", err)
	}
	if trafficCount != 1 {
		t.Fatalf("traffic buckets should remain after hard delete, got %d", trafficCount)
	}
}

func unifiedTunnelInsertSQL() string {
	return `INSERT INTO tunnels (
		id, name, client_id, type, local_ip, local_port, remote_port, domain, hostname, binding,
		revision, topology, owner_client_id,
		ingress_location, ingress_client_id, ingress_type, ingress_config, ingress_bind_ip, ingress_port, ingress_domain, ingress_path,
		target_location, target_client_id, target_type, target_config, target_host, target_port, target_path, target_resource_key,
		transport_policy, actual_transport, p2p_state, p2p_error, p2p_session_id,
		ingress_bps, egress_bps, desired_state, runtime_state, error, created_by_user_id, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func validUnifiedTunnelInsertArgs(id string) []any {
	now := formatTime(time.Now().UTC())
	return []any{
		id, id, "client-1", protocol.ProxyTypeTCP, "127.0.0.1", 80, 18080, "", "host-1", TunnelBindingClientID,
		1, TunnelTopologyServerExpose, "client-1",
		"server", "", TunnelIngressTypeTCPListen, `{"bind_ip":"0.0.0.0","port":18080}`, "0.0.0.0", 18080, "", "",
		"client", "client-1", TunnelTargetTypeTCPService, `{"host":"127.0.0.1","port":80}`, "127.0.0.1", 80, "", "target:client:client-1:tcp_service:127.0.0.1:80",
		TunnelTransportServerRelayOnly, TunnelActualTransportUnknown, TunnelP2PStateIdle, "", "",
		0, 0, protocol.ProxyDesiredStateRunning, "active", "", "", now, now,
	}
}
