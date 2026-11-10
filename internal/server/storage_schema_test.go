package server

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"netsgo/internal/storage"
	"netsgo/pkg/protocol"
)

func TestOpenServerDBCreatesExpectedTables(t *testing.T) {
	db, err := openServerDB(filepath.Join(t.TempDir(), "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	wantTables := []string{
		"server_config",
		"allowed_ports",
		"users",
		"api_keys",
		"api_key_permissions",
		"registered_clients",
		"client_stats",
		"client_disk_partitions",
		"client_tokens",
		"user_sessions",
		"tunnels",
		"traffic_buckets",
		"activity_webhooks",
		"activity_webhook_events",
		"activity_webhook_targets",
		"activity_webhook_deliveries",
		"activity_webhook_delivery_attempts",
		"activity_webhook_dispatch_slots",
		"activity_webhook_notification_state",
	}
	for _, table := range wantTables {
		if !sqliteTableExists(t, db, table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}

	for _, column := range []string{"initialized", "jwt_secret", "client_auth_rate_limit_enabled", "client_auth_rate_limit_per_minute", "webhook_allow_private_targets", "webhook_daily_delivery_cap"} {
		if !sqliteTableColumnExists(t, db, "server_config", column) {
			t.Fatalf("expected server_config.%s to exist", column)
		}
	}

	if got := countTunnelRegisteredClientFKs(t, db); got != 0 {
		t.Fatalf("tunnels.client_id should not reference registered_clients, got %d FK(s)", got)
	}
}

func TestOpenServerDBMigratesEmptyDatabaseToExpectedSchema(t *testing.T) {
	db, err := openServerDB(filepath.Join(t.TempDir(), "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	wantTables := map[string][]sqliteColumn{
		"schema_migrations": {
			{name: "name", typ: "TEXT", notNull: false, primaryKey: true},
			{name: "applied_at", typ: "TEXT", notNull: true},
		},
		serverCompatibleMigrationTable: {
			{name: "name", typ: "TEXT", notNull: false, primaryKey: true},
			{name: "applied_at", typ: "TEXT", notNull: true},
		},
		"server_config": {
			{name: "id", typ: "INTEGER", primaryKey: true},
			{name: "initialized", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "jwt_secret", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "server_addr", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "client_auth_rate_limit_enabled", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "client_auth_rate_limit_per_minute", typ: "INTEGER", notNull: true, defaultValue: "20"},
			{name: "activity_debug_retention_days", typ: "INTEGER", notNull: true, defaultValue: "1"},
			{name: "activity_debug_min_count", typ: "INTEGER", notNull: true, defaultValue: "200"},
			{name: "activity_info_retention_days", typ: "INTEGER", notNull: true, defaultValue: "7"},
			{name: "activity_info_min_count", typ: "INTEGER", notNull: true, defaultValue: "100"},
			{name: "activity_warning_retention_days", typ: "INTEGER", notNull: true, defaultValue: "30"},
			{name: "activity_warning_min_count", typ: "INTEGER", notNull: true, defaultValue: "100"},
			{name: "activity_error_retention_days", typ: "INTEGER", notNull: true, defaultValue: "180"},
			{name: "activity_error_min_count", typ: "INTEGER", notNull: true, defaultValue: "100"},
			{name: "webhook_allow_private_targets", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "webhook_daily_delivery_cap", typ: "INTEGER", notNull: true, defaultValue: "50"},
		},
		"allowed_ports": {
			{name: "id", typ: "INTEGER", primaryKey: true},
			{name: "start_port", typ: "INTEGER", notNull: true},
			{name: "end_port", typ: "INTEGER", notNull: true},
		},
		"users": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "username", typ: "TEXT", notNull: true},
			{name: "password_hash", typ: "TEXT", notNull: true},
			{name: "is_admin", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "status", typ: "TEXT", notNull: true, defaultValue: "'active'"},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "updated_at", typ: "TEXT", notNull: true},
			{name: "last_login", typ: "TEXT"},
			{name: "totp_enabled", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "totp_secret", typ: "TEXT", notNull: true, defaultValue: "''"},
		},
		"admin_totp_recovery_codes": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "user_id", typ: "TEXT", notNull: true},
			{name: "code_hash", typ: "TEXT", notNull: true},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "used_at", typ: "TEXT"},
		},
		"admin_passkeys": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "user_id", typ: "TEXT", notNull: true},
			{name: "name", typ: "TEXT", notNull: true},
			{name: "credential_id", typ: "TEXT", notNull: true},
			{name: "credential_json", typ: "TEXT", notNull: true},
			{name: "rp_id", typ: "TEXT", notNull: true},
			{name: "origin", typ: "TEXT", notNull: true},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "last_used_at", typ: "TEXT"},
		},
		"admin_auth_challenges": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "user_id", typ: "TEXT"},
			{name: "kind", typ: "TEXT", notNull: true},
			{name: "session_json", typ: "TEXT", notNull: true},
			{name: "metadata_json", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "expires_at", typ: "TEXT", notNull: true},
		},
		"api_keys": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "name", typ: "TEXT", notNull: true},
			{name: "key_hash", typ: "TEXT", notNull: true},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "expires_at", typ: "TEXT"},
			{name: "is_active", typ: "INTEGER", notNull: true},
			{name: "max_uses", typ: "INTEGER", notNull: true},
			{name: "use_count", typ: "INTEGER", notNull: true},
			{name: "lookup_digest", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "owner_user_id", typ: "TEXT"},
		},
		"api_key_permissions": {
			{name: "api_key_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "permission", typ: "TEXT", notNull: true, primaryKey: true},
		},
		"registered_clients": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "install_id", typ: "TEXT", notNull: true},
			{name: "display_name", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "hostname", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "os", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "arch", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "version", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "public_ipv4", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "public_ipv6", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "egress_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "last_seen", typ: "TEXT", notNull: true},
			{name: "last_ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "last_capabilities", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "owner_user_id", typ: "TEXT"},
		},
		"client_stats": {
			{name: "client_id", typ: "TEXT", primaryKey: true},
			{name: "cpu_usage", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "mem_total", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "mem_used", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "mem_usage", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "disk_total", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "disk_used", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "disk_usage", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "net_sent", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "net_recv", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "net_sent_speed", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "net_recv_speed", typ: "REAL", notNull: true, defaultValue: "0"},
			{name: "uptime", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "process_uptime", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "os_install_time", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "num_cpu", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "app_mem_used", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "app_mem_sys", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "public_ipv4", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "public_ipv6", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "updated_at", typ: "TEXT"},
			{name: "fresh_until", typ: "TEXT"},
		},
		"client_disk_partitions": {
			{name: "client_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "path", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "used", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "total", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"client_tokens": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "token_hash", typ: "TEXT", notNull: true},
			{name: "install_id", typ: "TEXT", notNull: true},
			{name: "key_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "last_active_at", typ: "TEXT", notNull: true},
			{name: "last_ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "is_revoked", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"user_sessions": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "user_id", typ: "TEXT", notNull: true},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "expires_at", typ: "TEXT", notNull: true},
			{name: "ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "user_agent", typ: "TEXT", notNull: true, defaultValue: "''"},
		},
		"tunnels": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "name", typ: "TEXT", notNull: true},
			{name: "client_id", typ: "TEXT", notNull: true},
			{name: "type", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "local_ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "local_port", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "remote_port", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "domain", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "hostname", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "binding", typ: "TEXT", notNull: true, defaultValue: "'client_id'"},
			{name: "revision", typ: "INTEGER", notNull: true, defaultValue: "1"},
			{name: "topology", typ: "TEXT", notNull: true},
			{name: "owner_client_id", typ: "TEXT", notNull: true},
			{name: "ingress_location", typ: "TEXT", notNull: true},
			{name: "ingress_client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_type", typ: "TEXT", notNull: true},
			{name: "ingress_config", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "ingress_bind_ip", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_port", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "ingress_domain", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_path", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_location", typ: "TEXT", notNull: true},
			{name: "target_client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_type", typ: "TEXT", notNull: true},
			{name: "target_config", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "target_host", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_port", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "target_path", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_resource_key", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "transport_policy", typ: "TEXT", notNull: true},
			{name: "actual_transport", typ: "TEXT", notNull: true, defaultValue: "'unknown'"},
			{name: "p2p_state", typ: "TEXT", notNull: true, defaultValue: "'idle'"},
			{name: "p2p_error", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "p2p_session_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "ingress_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "egress_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "desired_state", typ: "TEXT", notNull: true},
			{name: "runtime_state", typ: "TEXT", notNull: true},
			{name: "error", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "created_by_user_id", typ: "TEXT"},
			{name: "created_at", typ: "TEXT", notNull: true},
			{name: "updated_at", typ: "TEXT", notNull: true},
			{name: "total_bps", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "owner_user_id", typ: "TEXT"},
		},
		"activity_events": {
			{name: "id", typ: "INTEGER", primaryKey: true},
			{name: "occurred_at_ns", typ: "INTEGER", notNull: true},
			{name: "recorded_at_ns", typ: "INTEGER", notNull: true},
			{name: "severity", typ: "TEXT", notNull: true},
			{name: "category", typ: "TEXT", notNull: true},
			{name: "action", typ: "TEXT", notNull: true},
			{name: "source", typ: "TEXT", notNull: true},
			{name: "actor_type", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "actor_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "actor_name", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "actor_ip_hash", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "actor_ip_prefix", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "dedupe_key", typ: "TEXT"},
			{name: "payload_version", typ: "INTEGER", notNull: true, defaultValue: "1"},
			{name: "payload_json", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "scope_user_id", typ: "TEXT"},
			{name: "subject_user_id", typ: "TEXT"},
		},
		"activity_event_clients": {
			{name: "event_id", typ: "INTEGER", notNull: true, primaryKey: true},
			{name: "client_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "relation", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "display_name", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "hostname", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "is_truncated", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"activity_event_tunnels": {
			{name: "event_id", typ: "INTEGER", notNull: true, primaryKey: true},
			{name: "tunnel_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "relation", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "name", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "tunnel_type", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "topology", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "is_truncated", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"activity_webhooks": {
			{name: "owner_user_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "revision", typ: "INTEGER", notNull: true, defaultValue: "1"},
			{name: "name", typ: "TEXT", notNull: true},
			{name: "enabled", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "target_kind", typ: "TEXT", notNull: true},
			{name: "target_mode", typ: "TEXT", notNull: true},
			{name: "method", typ: "TEXT", notNull: true},
			{name: "url_template", typ: "TEXT", notNull: true},
			{name: "headers_json", typ: "TEXT", notNull: true, defaultValue: "'[]'"},
			{name: "body_template", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "last_status", typ: "TEXT", notNull: true, defaultValue: "'idle'"},
			{name: "consecutive_failures", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "last_called_at_ns", typ: "INTEGER"},
			{name: "created_at_ns", typ: "INTEGER", notNull: true},
			{name: "updated_at_ns", typ: "INTEGER", notNull: true},
			{name: "min_interval_seconds", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"activity_webhook_events": {
			{name: "owner_user_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "webhook_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "event_type", typ: "TEXT", notNull: true, primaryKey: true},
		},
		"activity_webhook_notification_state": {
			{name: "owner_user_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "webhook_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "event_type", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "target_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "last_enqueued_at_ns", typ: "INTEGER", notNull: true},
		},
		"activity_webhook_targets": {
			{name: "owner_user_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "webhook_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "target_id", typ: "TEXT", notNull: true, primaryKey: true},
		},
		"activity_webhook_deliveries": {
			{name: "id", typ: "TEXT", primaryKey: true},
			{name: "owner_user_id", typ: "TEXT", notNull: true},
			{name: "webhook_id", typ: "TEXT", notNull: true},
			{name: "webhook_name", typ: "TEXT", notNull: true},
			{name: "origin", typ: "TEXT", notNull: true},
			{name: "source_event_id", typ: "INTEGER"},
			{name: "event_type", typ: "TEXT", notNull: true},
			{name: "event_occurred_at_ns", typ: "INTEGER", notNull: true},
			{name: "status", typ: "TEXT", notNull: true},
			{name: "attempt_count", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "max_attempts", typ: "INTEGER", notNull: true},
			{name: "next_attempt_at_ns", typ: "INTEGER", notNull: true},
			{name: "lease_until_ns", typ: "INTEGER"},
			{name: "config_revision", typ: "INTEGER", notNull: true},
			{name: "config_snapshot_json", typ: "TEXT", notNull: true},
			{name: "event_snapshot_json", typ: "TEXT", notNull: true},
			{name: "values_snapshot_json", typ: "TEXT", notNull: true},
			{name: "request_method", typ: "TEXT", notNull: true},
			{name: "request_url", typ: "TEXT", notNull: true},
			{name: "request_headers_json", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "request_body", typ: "TEXT"},
			{name: "response_status", typ: "INTEGER"},
			{name: "response_headers_json", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "response_body", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "error", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "duration_ms", typ: "INTEGER"},
			{name: "created_at_ns", typ: "INTEGER", notNull: true},
			{name: "started_at_ns", typ: "INTEGER"},
			{name: "completed_at_ns", typ: "INTEGER"},
			{name: "updated_at_ns", typ: "INTEGER", notNull: true},
		},
		"activity_webhook_delivery_attempts": {
			{name: "delivery_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "attempt_number", typ: "INTEGER", notNull: true, primaryKey: true},
			{name: "status", typ: "TEXT", notNull: true},
			{name: "started_at_ns", typ: "INTEGER", notNull: true},
			{name: "completed_at_ns", typ: "INTEGER"},
			{name: "duration_ms", typ: "INTEGER"},
			{name: "response_status", typ: "INTEGER"},
			{name: "response_headers_json", typ: "TEXT", notNull: true, defaultValue: "'{}'"},
			{name: "response_body", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "error", typ: "TEXT", notNull: true, defaultValue: "''"},
		},
		"activity_webhook_dispatch_slots": {
			{name: "owner_user_id", typ: "TEXT", primaryKey: true},
			{name: "next_allowed_at_ns", typ: "INTEGER", notNull: true, defaultValue: "0"},
		},
		"traffic_buckets": {
			{name: "tunnel_id", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "owner_client_id", typ: "TEXT", notNull: true},
			{name: "ingress_client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "target_client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "topology", typ: "TEXT", notNull: true},
			{name: "transport", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "tunnel_name", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "tunnel_type", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "resolution", typ: "TEXT", notNull: true, primaryKey: true},
			{name: "bucket_start", typ: "INTEGER", notNull: true, primaryKey: true},
			{name: "ingress_bytes", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "egress_bytes", typ: "INTEGER", notNull: true, defaultValue: "0"},
			{name: "owner_user_id", typ: "TEXT"},
		},
		"tunnel_resource_locks": {
			{name: "resource_key", typ: "TEXT", primaryKey: true},
			{name: "tunnel_id", typ: "TEXT", notNull: true},
			{name: "resource_kind", typ: "TEXT", notNull: true},
			{name: "client_id", typ: "TEXT", notNull: true, defaultValue: "''"},
			{name: "created_at", typ: "TEXT", notNull: true},
		},
	}
	assertSQLiteTables(t, db, wantTables)

	wantIndexes := map[string][]sqliteIndex{
		"schema_migrations": {{name: "sqlite_autoindex_schema_migrations_1", unique: true, columns: []string{"name"}}},
		"users": {
			{name: "idx_users_page", unique: false, columns: []string{"created_at", "id"}},
			{name: "idx_users_status_page", unique: false, columns: []string{"status", "created_at", "id"}},
			{name: "sqlite_autoindex_users_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_users_2", unique: true, columns: []string{"username"}},
		},
		"admin_totp_recovery_codes": {
			{name: "idx_admin_totp_recovery_codes_user_unused", unique: false, columns: []string{"user_id", "used_at"}},
			{name: "sqlite_autoindex_admin_totp_recovery_codes_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_admin_totp_recovery_codes_2", unique: true, columns: []string{"code_hash"}},
		},
		"admin_passkeys": {
			{name: "idx_admin_passkeys_rp", unique: false, columns: []string{"rp_id", "origin"}},
			{name: "idx_admin_passkeys_user", unique: false, columns: []string{"user_id"}},
			{name: "sqlite_autoindex_admin_passkeys_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_admin_passkeys_2", unique: true, columns: []string{"credential_id"}},
		},
		"admin_auth_challenges": {
			{name: "idx_admin_auth_challenges_expires", unique: false, columns: []string{"expires_at"}},
			{name: "idx_admin_auth_challenges_user_kind", unique: false, columns: []string{"user_id", "kind"}},
			{name: "sqlite_autoindex_admin_auth_challenges_1", unique: true, columns: []string{"id"}},
		},
		"api_keys": {
			{name: "idx_api_keys_lookup_digest", unique: false, columns: []string{"lookup_digest"}},
			{name: "idx_api_keys_owner", unique: false, columns: []string{"owner_user_id", "created_at"}},
			{name: "sqlite_autoindex_api_keys_1", unique: true, columns: []string{"id"}},
		},
		"api_key_permissions": {
			{name: "sqlite_autoindex_api_key_permissions_1", unique: true, columns: []string{"api_key_id", "permission"}},
		},
		"registered_clients": {
			{name: "idx_registered_clients_owner", unique: false, columns: []string{"owner_user_id", "created_at"}},
			{name: "sqlite_autoindex_registered_clients_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_registered_clients_2", unique: true, columns: []string{"install_id"}},
		},
		"client_stats":           {{name: "sqlite_autoindex_client_stats_1", unique: true, columns: []string{"client_id"}}},
		"client_disk_partitions": {{name: "sqlite_autoindex_client_disk_partitions_1", unique: true, columns: []string{"client_id", "path"}}},
		"client_tokens": {
			{name: "idx_client_tokens_install_active", unique: false, columns: []string{"install_id", "is_revoked", "last_active_at"}},
			{name: "sqlite_autoindex_client_tokens_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_client_tokens_2", unique: true, columns: []string{"token_hash"}},
		},
		"user_sessions": {
			{name: "idx_user_sessions_expires", unique: false, columns: []string{"expires_at"}},
			{name: "idx_user_sessions_user", unique: false, columns: []string{"user_id"}},
			{name: "sqlite_autoindex_user_sessions_1", unique: true, columns: []string{"id"}},
		},
		"activity_events": {
			{name: "idx_activity_events_category_id", unique: false, columns: []string{"category", "id"}},
			{name: "idx_activity_events_category_occurred", unique: false, columns: []string{"category", "occurred_at_ns", "id"}},
			{name: "idx_activity_events_dedupe_key", unique: true, columns: []string{"dedupe_key"}},
			{name: "idx_activity_events_occurred", unique: false, columns: []string{"occurred_at_ns", "id"}},
			{name: "idx_activity_events_severity_id", unique: false, columns: []string{"severity", "id"}},
			{name: "idx_activity_events_severity_occurred", unique: false, columns: []string{"severity", "occurred_at_ns", "id"}},
			{name: "idx_activity_events_subject_user", unique: false, columns: []string{"subject_user_id", "occurred_at_ns", "id"}},
			{name: "idx_activity_events_user", unique: false, columns: []string{"scope_user_id", "occurred_at_ns", "id"}},
		},
		"activity_event_clients": {
			{name: "idx_activity_event_clients_client", unique: false, columns: []string{"client_id", "event_id"}},
			{name: "sqlite_autoindex_activity_event_clients_1", unique: true, columns: []string{"event_id", "client_id", "relation"}},
		},
		"activity_event_tunnels": {
			{name: "idx_activity_event_tunnels_tunnel", unique: false, columns: []string{"tunnel_id", "event_id"}},
			{name: "sqlite_autoindex_activity_event_tunnels_1", unique: true, columns: []string{"event_id", "tunnel_id", "relation"}},
		},
		"activity_webhooks": {
			{name: "idx_activity_webhooks_owner_updated", unique: false, columns: []string{"owner_user_id", "updated_at_ns", "id"}},
			{name: "sqlite_autoindex_activity_webhooks_1", unique: true, columns: []string{"owner_user_id", "id"}},
		},
		"activity_webhook_events": {
			{name: "idx_activity_webhook_events_match", unique: false, columns: []string{"owner_user_id", "event_type", "webhook_id"}},
			{name: "sqlite_autoindex_activity_webhook_events_1", unique: true, columns: []string{"owner_user_id", "webhook_id", "event_type"}},
		},
		"activity_webhook_targets": {
			{name: "sqlite_autoindex_activity_webhook_targets_1", unique: true, columns: []string{"owner_user_id", "webhook_id", "target_id"}},
		},
		"activity_webhook_notification_state": {
			{name: "sqlite_autoindex_activity_webhook_notification_state_1", unique: true, columns: []string{"owner_user_id", "webhook_id", "event_type", "target_id"}},
		},
		"activity_webhook_deliveries": {
			{name: "idx_activity_webhook_deliveries_due", unique: false, columns: []string{"status", "next_attempt_at_ns", "created_at_ns", "id"}},
			{name: "idx_activity_webhook_deliveries_event_dedupe", unique: true, columns: []string{"owner_user_id", "webhook_id", "source_event_id"}},
			{name: "idx_activity_webhook_deliveries_owner_webhook_page", unique: false, columns: []string{"owner_user_id", "webhook_id", "created_at_ns", "id"}},
			{name: "idx_activity_webhook_deliveries_terminal_cleanup", unique: false, columns: []string{"owner_user_id", "webhook_id", "completed_at_ns", "id"}},
			{name: "sqlite_autoindex_activity_webhook_deliveries_1", unique: true, columns: []string{"id"}},
		},
		"activity_webhook_delivery_attempts": {
			{name: "sqlite_autoindex_activity_webhook_delivery_attempts_1", unique: true, columns: []string{"delivery_id", "attempt_number"}},
		},
		"activity_webhook_dispatch_slots": {
			{name: "sqlite_autoindex_activity_webhook_dispatch_slots_1", unique: true, columns: []string{"owner_user_id"}},
		},
		"tunnels": {
			{name: "idx_tunnels_hostname", unique: false, columns: []string{"hostname"}},
			{name: "idx_tunnels_ingress_client", unique: false, columns: []string{"ingress_client_id"}},
			{name: "idx_tunnels_ingress_domain", unique: false, columns: []string{"ingress_domain"}},
			{name: "idx_tunnels_ingress_port", unique: false, columns: []string{"ingress_location", "ingress_client_id", "ingress_type", "ingress_bind_ip", "ingress_port"}},
			{name: "idx_tunnels_owner", unique: false, columns: []string{"owner_client_id", "created_at"}},
			{name: "idx_tunnels_runtime_state", unique: false, columns: []string{"runtime_state"}},
			{name: "idx_tunnels_target_client", unique: false, columns: []string{"target_client_id"}},
			{name: "idx_tunnels_target_resource", unique: false, columns: []string{"target_location", "target_client_id", "target_type", "target_resource_key"}},
			{name: "idx_tunnels_topology", unique: false, columns: []string{"topology"}},
			{name: "idx_tunnels_user_topology", unique: false, columns: []string{"owner_user_id", "topology", "created_at"}},
			{name: "sqlite_autoindex_tunnels_1", unique: true, columns: []string{"id"}},
			{name: "sqlite_autoindex_tunnels_2", unique: true, columns: []string{"client_id", "name"}},
			{name: "sqlite_autoindex_tunnels_3", unique: true, columns: []string{"owner_client_id", "name"}},
		},
		"traffic_buckets": {
			{name: "idx_traffic_compat_query", unique: false, columns: []string{"client_id", "tunnel_name", "resolution", "bucket_start"}},
			{name: "idx_traffic_ingress_query", unique: false, columns: []string{"ingress_client_id", "resolution", "bucket_start"}},
			{name: "idx_traffic_owner_query", unique: false, columns: []string{"owner_client_id", "resolution", "bucket_start"}},
			{name: "idx_traffic_target_query", unique: false, columns: []string{"target_client_id", "resolution", "bucket_start"}},
			{name: "idx_traffic_user_query", unique: false, columns: []string{"owner_user_id", "resolution", "bucket_start"}},
			{name: "sqlite_autoindex_traffic_buckets_1", unique: true, columns: []string{"tunnel_id", "transport", "resolution", "bucket_start"}},
		},
		"tunnel_resource_locks": {
			{name: "idx_tunnel_resource_locks_client", unique: false, columns: []string{"client_id"}},
			{name: "idx_tunnel_resource_locks_tunnel", unique: false, columns: []string{"tunnel_id"}},
			{name: "sqlite_autoindex_tunnel_resource_locks_1", unique: true, columns: []string{"resource_key"}},
		},
	}
	assertSQLiteIndexes(t, db, wantIndexes)

	wantStrictMigrationNames := []string{
		"001_server_runtime_schema",
		"002_rebuild_tunnels_without_registered_client_fk",
		"003_tunnel_stable_id",
		"004_tunnel_created_at",
		"005_unified_tunnel_storage",
		"006_admin_security",
		"007_api_key_lookup_digest",
		"008_socks5_endpoint_types",
		"009_tunnel_total_bandwidth",
		"012_multi_user_ownership",
		"013_global_passkey_challenges",
		"014_activity_webhooks",
		"015_webhook_delivery_policy",
		"016_webhook_notification_interval",
	}
	if got := appliedMigrationNames(t, db, "schema_migrations"); !reflect.DeepEqual(got, wantStrictMigrationNames) {
		t.Fatalf("strict applied migrations = %#v, want %#v", got, wantStrictMigrationNames)
	}
	if got := appliedMigrationNames(t, db, serverCompatibleMigrationTable); !reflect.DeepEqual(got, []string{"010_client_auth_control", "011_activity_events"}) {
		t.Fatalf("compatible applied migrations = %#v", got)
	}
	if got := countTunnelRegisteredClientFKs(t, db); got != 0 {
		t.Fatalf("tunnels.client_id should not reference registered_clients, got %d FK(s)", got)
	}
}

func TestServerMigrationsLoadsEmbeddedFiles(t *testing.T) {
	migrations, err := serverMigrations()
	if err != nil {
		t.Fatalf("serverMigrations() error = %v", err)
	}

	var gotNames []string
	for _, migration := range migrations {
		gotNames = append(gotNames, migration.Name)
		if migration.Description == "" {
			t.Fatalf("migration %s should have Description", migration.Name)
		}
		if migration.CreatedAt == "" {
			t.Fatalf("migration %s should have CreatedAt", migration.Name)
		}
		if migration.Up == "" {
			t.Fatalf("migration %s should have Up SQL", migration.Name)
		}
	}
	wantNames := []string{
		"001_server_runtime_schema",
		"002_rebuild_tunnels_without_registered_client_fk",
		"003_tunnel_stable_id",
		"004_tunnel_created_at",
		"005_unified_tunnel_storage",
		"006_admin_security",
		"007_api_key_lookup_digest",
		"008_socks5_endpoint_types",
		"009_tunnel_total_bandwidth",
		"010_client_auth_control",
		"011_activity_events",
		"012_multi_user_ownership",
		"013_global_passkey_challenges",
		"014_activity_webhooks",
		"015_webhook_delivery_policy",
		"016_webhook_notification_interval",
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("migration names = %#v, want %#v", gotNames, wantNames)
	}
}

func TestLoadMigrationsRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name    string
		files   fstest.MapFS
		wantErr string
	}{
		{
			name: "bad file name",
			files: fstest.MapFS{
				"migrations/1_bad.sql": {Data: []byte(validMigrationSQL("001_bad"))},
			},
			wantErr: "invalid migration file name",
		},
		{
			name: "name mismatch",
			files: fstest.MapFS{
				"migrations/001_good.sql": {Data: []byte(validMigrationSQL("001_other"))},
			},
			wantErr: "must match file name stem",
		},
		{
			name: "missing up",
			files: fstest.MapFS{
				"migrations/001_missing_up.sql": {Data: []byte(`-- Name: 001_missing_up
-- Description: Missing up.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Down:
`)},
			},
			wantErr: "-- Down: before -- Up",
		},
		{
			name: "duplicate version",
			files: fstest.MapFS{
				"migrations/001_first.sql":  {Data: []byte(validMigrationSQL("001_first"))},
				"migrations/001_second.sql": {Data: []byte(validMigrationSQL("001_second"))},
			},
			wantErr: "duplicate migration version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadMigrations(tt.files, "migrations")
			if err == nil {
				t.Fatal("loadMigrations() error = nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("loadMigrations() error = %q, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseMigrationFileAcceptsFlexibleSectionAndHeaderWhitespace(t *testing.T) {
	migration, err := parseMigrationFile("001_flexible_header.sql", `--  Name: 001_flexible_header
--	Description: Test flexible migration formatting.
-- CreatedAt: 2026-05-15T00:00:00Z

   -- Up:
SELECT 1;

	-- Down:
SELECT 0;
`)
	if err != nil {
		t.Fatalf("parseMigrationFile() error = %v", err)
	}
	if migration.Name != "001_flexible_header" {
		t.Fatalf("migration.Name = %q", migration.Name)
	}
	if migration.Up != "SELECT 1;" {
		t.Fatalf("migration.Up = %q", migration.Up)
	}
	if migration.Down != "SELECT 0;" {
		t.Fatalf("migration.Down = %q", migration.Down)
	}
}

func TestParseMigrationFileAllowsEmptyDownSQL(t *testing.T) {
	migration, err := parseMigrationFile("001_empty_down.sql", `-- Name: 001_empty_down
-- Description: Empty down SQL.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
SELECT 1;

-- Down:
`)
	if err != nil {
		t.Fatalf("parseMigrationFile() error = %v", err)
	}
	if migration.Down != "" {
		t.Fatalf("migration.Down = %q, want empty", migration.Down)
	}
}

func TestParseMigrationFileAcceptsUpAtFileStart(t *testing.T) {
	_, err := parseMigrationFile("001_no_header.sql", `-- Up:
SELECT 1;

-- Down:
`)
	if err == nil {
		t.Fatal("parseMigrationFile() error = nil")
	}
	if !strings.Contains(err.Error(), "missing Name header") {
		t.Fatalf("parseMigrationFile() error = %q, want missing Name header", err)
	}
}

func TestParseMigrationFileRejectsBareHeaderFields(t *testing.T) {
	_, err := parseMigrationFile("001_bare_header.sql", `Name: 001_bare_header
-- Description: Bare header.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
SELECT 1;

-- Down:
`)
	if err == nil {
		t.Fatal("parseMigrationFile() error = nil")
	}
	if !strings.Contains(err.Error(), "invalid header line") {
		t.Fatalf("parseMigrationFile() error = %q, want invalid header line", err)
	}
}

func TestOpenServerDBSkipsAppliedEmbeddedMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("first openServerDB() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	db, err = openServerDB(path)
	if err != nil {
		t.Fatalf("second openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var strictCount, compatibleCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&strictCount); err != nil {
		t.Fatalf("count schema_migrations failed: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + serverCompatibleMigrationTable).Scan(&compatibleCount); err != nil {
		t.Fatalf("count compatible migrations failed: %v", err)
	}
	if strictCount != 14 || compatibleCount != 2 {
		t.Fatalf("migration counts = strict %d, compatible %d; want 14 and 2", strictCount, compatibleCount)
	}
}

func TestOpenServerDBKeepsClientAuthMigrationOutOfLegacyLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	migrations, err := serverMigrations()
	if err != nil {
		t.Fatalf("serverMigrations() error = %v", err)
	}
	_, strict := partitionServerMigrations(migrations)
	legacyDB, err := storage.Open(path, strict)
	if err != nil {
		t.Fatalf("legacy strict migration ledger should remain readable: %v", err)
	}
	defer func() { _ = legacyDB.Close() }()

	if !sqliteTableColumnExists(t, legacyDB, "server_config", "client_auth_rate_limit_enabled") {
		t.Fatal("compatible client auth column should remain available")
	}
}

func TestOlderStrictMigrationSetRejects012BeforeWritableOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close upgraded DB: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgraded DB before old-binary simulation: %v", err)
	}

	migrations, err := serverMigrations()
	if err != nil {
		t.Fatalf("serverMigrations() error = %v", err)
	}
	_, strict := partitionServerMigrations(migrations)
	olderStrict := make([]storage.Migration, 0, len(strict)-1)
	for _, migration := range strict {
		if migration.Name != "012_multi_user_ownership" {
			olderStrict = append(olderStrict, migration)
		}
	}
	olderDB, err := storage.Open(path, olderStrict)
	if err == nil {
		_ = olderDB.Close()
		t.Fatal("older strict migration set should reject 012")
	}
	if !strings.Contains(err.Error(), `unknown applied migration "012_multi_user_ownership"`) {
		t.Fatalf("older strict migration set error = %q", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgraded DB after old-binary simulation: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("old strict migration rejection should occur before writable SQLite open")
	}
}

func TestOpenServerDBAcceptsExisting009StrictMigrationLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	legacyDB, err := storage.Open(path, strictServerMigrationsBeforeMultiUser(t))
	if err != nil {
		t.Fatalf("open database through migration 009: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close database through migration 009: %v", err)
	}

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("upgrade database with 009 in strict ledger: %v", err)
	}
	defer func() { _ = db.Close() }()
	if got := appliedMigrationNames(t, db, "schema_migrations"); !slices.Contains(got, "009_tunnel_total_bandwidth") {
		t.Fatalf("strict migration ledger lost 009: %#v", got)
	}
}

func TestOpenServerDBMigratesLegacyAdministratorsAndResourceOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	legacyDB := openServerDBThroughMigration011(t, path)
	if _, err := legacyDB.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr)
		VALUES (1, 1, 'legacy-jwt-secret', 'https://example.test')`); err != nil {
		t.Fatalf("seed initialized config: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO admin_users
		(id, username, password_hash, role, created_at, last_login, totp_enabled, totp_secret)
		VALUES
		('admin-earliest', 'first-admin', 'hash-first', 'admin', '2026-01-01T00:00:00Z', NULL, 1, 'totp-first'),
		('admin-later', 'second-admin', 'hash-second', 'admin', '2026-01-02T00:00:00Z', '2026-01-03T00:00:00Z', 0, '')`); err != nil {
		t.Fatalf("seed legacy administrators: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO admin_sessions
		(id, user_id, username, role, created_at, expires_at, ip, user_agent)
		VALUES ('legacy-session', 'admin-earliest', 'first-admin', 'admin', '2026-01-01T00:00:00Z', '2030-01-01T00:00:00Z', '127.0.0.1', 'test-agent')`); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO admin_totp_recovery_codes (id, user_id, code_hash, created_at, used_at)
		VALUES ('recovery-1', 'admin-earliest', 'recovery-hash-1', '2026-01-01T00:00:00Z', NULL)`); err != nil {
		t.Fatalf("seed recovery code: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO admin_passkeys
		(id, user_id, name, credential_id, credential_json, rp_id, origin, created_at, last_used_at)
		VALUES ('passkey-1', 'admin-earliest', 'Security key', 'credential-1', '{}', 'example.test', 'https://example.test', '2026-01-01T00:00:00Z', NULL)`); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO admin_auth_challenges
		(id, user_id, kind, session_json, metadata_json, created_at, expires_at)
		VALUES ('challenge-1', 'admin-earliest', 'totp_login', '{}', '{}', '2026-01-01T00:00:00Z', '2030-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed auth challenge: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO api_keys
		(id, name, key_hash, created_at, expires_at, is_active, max_uses, use_count, lookup_digest)
		VALUES ('key-1', 'Legacy key', 'hash', '2026-01-01T00:00:00Z', NULL, 1, 0, 0, 'digest')`); err != nil {
		t.Fatalf("seed API key: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO registered_clients
		(id, install_id, created_at, last_seen)
		VALUES ('client-1', 'install-1', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("seed registered client: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO tunnels (
		id, name, client_id, topology, owner_client_id,
		ingress_location, ingress_type, target_location, target_client_id, target_type,
		transport_policy, desired_state, runtime_state, created_at, updated_at
	) VALUES (
		'tunnel-1', 'Legacy tunnel', 'client-1', 'server_expose', 'client-1',
		'server', 'tcp_listen', 'client', 'client-1', 'tcp_service',
		'server_relay_only', 'running', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'
	)`); err != nil {
		t.Fatalf("seed tunnel: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO traffic_buckets
		(tunnel_id, owner_client_id, topology, transport, resolution, bucket_start)
		VALUES ('tunnel-1', 'client-1', 'server_expose', 'server_relay', 'minute', 1700000000)`); err != nil {
		t.Fatalf("seed traffic bucket: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO activity_events
		(occurred_at_ns, recorded_at_ns, severity, category, action, source, dedupe_key)
		VALUES
		(1, 1, 'warning', 'security', 'session_environment_mismatch', 'server', 'security:session_environment_mismatch:environment_mismatch:admin-earliest:1'),
		(2, 2, 'warning', 'security', 'session_environment_mismatch', 'server', 'security:session_environment_mismatch:environment_mismatch:deleted-user:2'),
		(3, 3, 'info', 'client', 'client_connected', 'server', NULL)`); err != nil {
		t.Fatalf("seed activity events: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO activity_events
		(occurred_at_ns, recorded_at_ns, severity, category, action, source, actor_type, actor_id)
		VALUES (4, 4, 'info', 'client', 'client_connected', 'server', 'client', 'admin-earliest')`); err != nil {
		t.Fatalf("seed colliding client actor: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO activity_event_clients (event_id, client_id, relation)
		VALUES (3, 'client-1', 'subject')`); err != nil {
		t.Fatalf("seed client activity relation: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy DB: %v", err)
	}

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("upgrade legacy DB through 012: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, oldTable := range []string{"admin_users", "admin_sessions"} {
		if sqliteTableExists(t, db, oldTable) {
			t.Fatalf("%s should be removed by 012", oldTable)
		}
	}
	for _, table := range []string{"users", "user_sessions"} {
		if !sqliteTableExists(t, db, table) {
			t.Fatalf("%s should be created by 012", table)
		}
	}

	var adminCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1 AND status = 'active'`).Scan(&adminCount); err != nil {
		t.Fatalf("count migrated administrators: %v", err)
	}
	if adminCount != 2 {
		t.Fatalf("migrated active administrators = %d, want 2", adminCount)
	}
	var sessionUserID string
	if err := db.QueryRow(`SELECT user_id FROM user_sessions WHERE id = 'legacy-session'`).Scan(&sessionUserID); err != nil {
		t.Fatalf("load migrated session: %v", err)
	}
	if sessionUserID != "admin-earliest" {
		t.Fatalf("migrated session user = %q, want admin-earliest", sessionUserID)
	}

	for _, query := range []string{
		`SELECT owner_user_id FROM api_keys WHERE id = 'key-1'`,
		`SELECT owner_user_id FROM registered_clients WHERE id = 'client-1'`,
		`SELECT owner_user_id FROM tunnels WHERE id = 'tunnel-1'`,
		`SELECT owner_user_id FROM traffic_buckets WHERE tunnel_id = 'tunnel-1'`,
	} {
		var owner string
		if err := db.QueryRow(query).Scan(&owner); err != nil {
			t.Fatalf("load migrated owner: %v", err)
		}
		if owner != "admin-earliest" {
			t.Fatalf("legacy owner = %q, want admin-earliest", owner)
		}
	}
	var createdBy sql.NullString
	if err := db.QueryRow(`SELECT created_by_user_id FROM tunnels WHERE id = 'tunnel-1'`).Scan(&createdBy); err != nil {
		t.Fatalf("load migrated tunnel creator: %v", err)
	}
	if createdBy.Valid {
		t.Fatalf("empty legacy tunnel creator should become NULL, got %q", createdBy.String)
	}

	var subjectUserID string
	if err := db.QueryRow(`SELECT subject_user_id FROM activity_events WHERE id = 1`).Scan(&subjectUserID); err != nil {
		t.Fatalf("load migrated session-environment activity: %v", err)
	}
	if subjectUserID != "admin-earliest" {
		t.Fatalf("migrated activity subject = %q, want admin-earliest", subjectUserID)
	}
	var invalidActivityCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM activity_events WHERE id = 2`).Scan(&invalidActivityCount); err != nil {
		t.Fatalf("check invalid activity removal: %v", err)
	}
	if invalidActivityCount != 0 {
		t.Fatal("unresolvable session-environment activity should be removed")
	}
	var scopedUserID string
	if err := db.QueryRow(`SELECT scope_user_id FROM activity_events WHERE id = 3`).Scan(&scopedUserID); err != nil {
		t.Fatalf("load client-scoped activity: %v", err)
	}
	if scopedUserID != "admin-earliest" {
		t.Fatalf("client activity scope = %q, want admin-earliest", scopedUserID)
	}
	var collidingSubjectUserID sql.NullString
	if err := db.QueryRow(`SELECT subject_user_id FROM activity_events WHERE id = 4`).Scan(&collidingSubjectUserID); err != nil {
		t.Fatalf("load colliding client actor activity: %v", err)
	}
	if collidingSubjectUserID.Valid {
		t.Fatalf("client actor ID collision must not become a user subject, got %q", collidingSubjectUserID.String)
	}

	assertNoSQLiteForeignKeyViolations(t, db)
	if !slices.Contains(appliedMigrationNames(t, db, "schema_migrations"), "012_multi_user_ownership") {
		t.Fatal("012 should be recorded in the strict migration ledger")
	}
}

func TestMultiUserMigrationValidationFailureRollsBackBeforeStrictLedgerRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	db := openServerDBThroughMigration011(t, path)
	defer func() { _ = db.Close() }()

	migrations, err := serverMigrations()
	if err != nil {
		t.Fatalf("serverMigrations() error = %v", err)
	}
	for index := range migrations {
		if migrations[index].Name != "012_multi_user_ownership" {
			continue
		}
		migrations[index].Up = strings.Replace(
			migrations[index].Up,
			"CREATE INDEX idx_users_page ON users(created_at DESC, id DESC);",
			"",
			1,
		)
	}

	err = storage.ApplyMigrationPlan(db, serverMigrationPlan(migrations))
	if err == nil || !strings.Contains(err.Error(), "index users.idx_users_page is missing") {
		t.Fatalf("ApplyMigrationPlan() error = %v, want 012 validation failure", err)
	}
	if !sqliteTableExists(t, db, "admin_users") || !sqliteTableExists(t, db, "admin_sessions") {
		t.Fatal("failed 012 should roll back source-table deletion")
	}
	if sqliteTableExists(t, db, "users") || sqliteTableExists(t, db, "user_sessions") {
		t.Fatal("failed 012 should roll back unified user tables")
	}
	if slices.Contains(appliedMigrationNames(t, db, "schema_migrations"), "012_multi_user_ownership") {
		t.Fatal("failed 012 must not be recorded in the strict migration ledger")
	}
}

func TestMultiUserMigrationRejectsOrphanedAdminSecurityRowsWithoutDataLoss(t *testing.T) {
	tests := []struct {
		name    string
		table   string
		seedSQL string
	}{
		{
			name:  "recovery code",
			table: "admin_totp_recovery_codes",
			seedSQL: `INSERT INTO admin_totp_recovery_codes (id, user_id, code_hash, created_at, used_at) VALUES
				('valid-row', 'admin-valid', 'valid-recovery-hash', '2026-01-01T00:00:00Z', NULL),
				('orphan-row', 'missing-user', 'orphan-recovery-hash', '2026-01-02T00:00:00Z', NULL)`,
		},
		{
			name:  "passkey",
			table: "admin_passkeys",
			seedSQL: `INSERT INTO admin_passkeys
				(id, user_id, name, credential_id, credential_json, rp_id, origin, created_at, last_used_at) VALUES
				('valid-row', 'admin-valid', 'Valid key', 'valid-credential', '{}', 'example.test', 'https://example.test', '2026-01-01T00:00:00Z', NULL),
				('orphan-row', 'missing-user', 'Orphan key', 'orphan-credential', '{}', 'example.test', 'https://example.test', '2026-01-02T00:00:00Z', NULL)`,
		},
		{
			name:  "authentication challenge",
			table: "admin_auth_challenges",
			seedSQL: `INSERT INTO admin_auth_challenges
				(id, user_id, kind, session_json, metadata_json, created_at, expires_at) VALUES
				('valid-row', 'admin-valid', 'mfa_login', '{}', '{}', '2026-01-01T00:00:00Z', '2030-01-01T00:00:00Z'),
				('orphan-row', 'missing-user', 'mfa_login', '{}', '{}', '2026-01-02T00:00:00Z', '2030-01-02T00:00:00Z')`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server", "netsgo.db")
			legacyDB := openServerDBThroughMigration011(t, path)
			if _, err := legacyDB.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr)
				VALUES (1, 1, 'legacy-jwt-secret', 'https://example.test')`); err != nil {
				t.Fatalf("seed initialized config: %v", err)
			}
			if _, err := legacyDB.Exec(`INSERT INTO admin_users
				(id, username, password_hash, role, created_at, last_login, totp_enabled, totp_secret)
				VALUES ('admin-valid', 'valid-admin', 'hash', 'admin', '2026-01-01T00:00:00Z', NULL, 0, '')`); err != nil {
				t.Fatalf("seed valid administrator: %v", err)
			}
			if _, err := legacyDB.Exec(tt.seedSQL); err != nil {
				t.Fatalf("seed %s rows: %v", tt.table, err)
			}
			if err := legacyDB.Close(); err != nil {
				t.Fatalf("close legacy database: %v", err)
			}

			upgradedDB, err := openServerDB(path)
			if upgradedDB != nil {
				_ = upgradedDB.Close()
				t.Fatal("openServerDB() returned a database despite orphaned authentication rows")
			}
			if err == nil {
				t.Fatal("openServerDB() should reject orphaned authentication rows")
			}
			var orphanErr *multiUserMigrationOrphanRowsError
			if !errors.As(err, &orphanErr) {
				t.Fatalf("openServerDB() error = %v, want multiUserMigrationOrphanRowsError", err)
			}
			if orphanErr.Table != tt.table || orphanErr.OrphanCount != 1 {
				t.Fatalf("orphan error = %+v, want table %q and count 1", orphanErr, tt.table)
			}
			for _, fragment := range []string{path, tt.table, "rolled back", "was not recorded", "back up the database", "restore missing users"} {
				if !strings.Contains(err.Error(), fragment) {
					t.Fatalf("openServerDB() error = %q, want fragment %q", err, fragment)
				}
			}

			inspectDB, err := storage.OpenReadOnly(path)
			if err != nil {
				t.Fatalf("open rolled-back database read-only: %v", err)
			}
			for _, table := range []string{"admin_users", "admin_sessions", "admin_totp_recovery_codes", "admin_passkeys", "admin_auth_challenges"} {
				if !sqliteTableExists(t, inspectDB, table) {
					t.Fatalf("failed 012 should preserve source table %s", table)
				}
			}
			for _, table := range []string{"users", "user_sessions"} {
				if sqliteTableExists(t, inspectDB, table) {
					t.Fatalf("failed 012 should roll back target table %s", table)
				}
			}
			var preserved int
			preservedQuery := fmt.Sprintf(`SELECT COUNT(*) FROM %s
				WHERE (id = 'valid-row' AND user_id = 'admin-valid')
					OR (id = 'orphan-row' AND user_id = 'missing-user')`, tt.table)
			if err := inspectDB.QueryRow(preservedQuery).Scan(&preserved); err != nil {
				t.Fatalf("count preserved %s rows: %v", tt.table, err)
			}
			if preserved != 2 {
				t.Fatalf("preserved %s rows = %d, want 2", tt.table, preserved)
			}
			if slices.Contains(appliedMigrationNames(t, inspectDB, "schema_migrations"), "012_multi_user_ownership") {
				t.Fatal("failed 012 must not be recorded in the strict migration ledger")
			}
			if err := inspectDB.Close(); err != nil {
				t.Fatalf("close read-only inspection database: %v", err)
			}

			repairDB, err := storage.OpenConfigured(path)
			if err != nil {
				t.Fatalf("open database for simulated operator repair: %v", err)
			}
			repairQuery := fmt.Sprintf(`UPDATE %s SET user_id = 'admin-valid' WHERE id = 'orphan-row'`, tt.table)
			if _, err := repairDB.Exec(repairQuery); err != nil {
				_ = repairDB.Close()
				t.Fatalf("repair orphaned %s row: %v", tt.table, err)
			}
			if err := repairDB.Close(); err != nil {
				t.Fatalf("close repaired database: %v", err)
			}

			migratedDB, err := openServerDB(path)
			if err != nil {
				t.Fatalf("retry 012 after repairing %s: %v", tt.table, err)
			}
			defer func() { _ = migratedDB.Close() }()
			var migrated int
			migratedQuery := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE user_id = 'admin-valid'`, tt.table)
			if err := migratedDB.QueryRow(migratedQuery).Scan(&migrated); err != nil {
				t.Fatalf("count migrated %s rows: %v", tt.table, err)
			}
			if migrated != 2 {
				t.Fatalf("migrated %s rows = %d, want 2", tt.table, migrated)
			}
			if sqliteTableExists(t, migratedDB, "admin_users") || sqliteTableExists(t, migratedDB, "admin_sessions") {
				t.Fatal("successful retry should remove legacy identity tables")
			}
			if !slices.Contains(appliedMigrationNames(t, migratedDB, "schema_migrations"), "012_multi_user_ownership") {
				t.Fatal("successful retry should record 012 in the strict migration ledger")
			}
			assertNoSQLiteForeignKeyViolations(t, migratedDB)
		})
	}
}

func TestOpenServerDBDoesNotRenewExpiredTokensDuringClientAuthMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	legacyDB, err := storage.Open(path, strictServerMigrationsBeforeMultiUser(t))
	if err != nil {
		t.Fatalf("open legacy schema: %v", err)
	}
	oldActivity := "2020-01-02T03:04:05Z"
	if _, err := legacyDB.Exec(`INSERT INTO client_tokens
		(id, token_hash, install_id, key_id, client_id, created_at, last_active_at, last_ip, is_revoked)
		VALUES ('expired-token', 'expired-hash', 'expired-install', 'key', 'expired-client', ?, ?, '', 0)`,
		oldActivity, oldActivity); err != nil {
		t.Fatalf("seed expired token: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy DB: %v", err)
	}

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var lastActivity string
	if err := db.QueryRow(`SELECT last_active_at FROM client_tokens WHERE id = 'expired-token'`).Scan(&lastActivity); err != nil {
		t.Fatalf("load expired token: %v", err)
	}
	if lastActivity != oldActivity {
		t.Fatalf("expired token activity = %q, want unchanged %q", lastActivity, oldActivity)
	}
}

func TestOpenServerDBRebuildsOldTunnelsFKSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	oldDB, err := storage.Open(path, []storage.Migration{{
		Name: "001_server_runtime_schema",
		Up: `
CREATE TABLE server_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	initialized INTEGER NOT NULL DEFAULT 0,
	jwt_secret TEXT NOT NULL DEFAULT '',
	server_addr TEXT NOT NULL DEFAULT ''
);
CREATE TABLE client_tokens (
	id TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL,
	install_id TEXT NOT NULL,
	key_id TEXT NOT NULL DEFAULT '',
	client_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	last_active_at TEXT NOT NULL,
	last_ip TEXT NOT NULL DEFAULT '',
	is_revoked INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE admin_sessions (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	username TEXT NOT NULL,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT ''
);
CREATE TABLE registered_clients (
	id TEXT PRIMARY KEY,
	install_id TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	last_seen TEXT NOT NULL
);
CREATE TABLE tunnels (
	client_id TEXT NOT NULL REFERENCES registered_clients(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT '',
	local_ip TEXT NOT NULL DEFAULT '',
	local_port INTEGER NOT NULL DEFAULT 0,
	remote_port INTEGER NOT NULL DEFAULT 0,
	domain TEXT NOT NULL DEFAULT '',
	ingress_bps INTEGER NOT NULL DEFAULT 0,
	egress_bps INTEGER NOT NULL DEFAULT 0,
	desired_state TEXT NOT NULL,
	runtime_state TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	binding TEXT NOT NULL,
	PRIMARY KEY (client_id, name)
);
CREATE INDEX idx_tunnels_hostname ON tunnels(hostname);
INSERT INTO registered_clients (id, install_id, created_at, last_seen)
VALUES ('client-existing', 'install-existing', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tunnels (client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, desired_state, runtime_state, error, hostname, binding)
VALUES ('client-existing', 'existing', 'tcp', '127.0.0.1', 80, 18080, '', 100, 200, 'running', 'exposed', '', 'host-existing', 'client_id');
`,
	}})
	if err != nil {
		t.Fatalf("create old schema failed: %v", err)
	}
	if got := countTunnelRegisteredClientFKs(t, oldDB); got != 1 {
		t.Fatalf("old schema should have registered_clients FK, got %d", got)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatalf("oldDB.Close() error = %v", err)
	}

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	if got := countTunnelRegisteredClientFKs(t, db); got != 0 {
		t.Fatalf("migration should remove registered_clients FK, got %d", got)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	store, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("NewTunnelStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, ok := store.GetTunnel("client-existing", "existing"); !ok {
		t.Fatal("existing tunnel should survive FK rebuild")
	}
	if err := store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "orphan", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 81, RemotePort: 18081},
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		ClientID:        "client-without-registered-row",
		Hostname:        "host-orphan",
		Binding:         TunnelBindingClientID,
	}); err == nil {
		t.Fatal("application ownership validation should reject an unregistered tunnel client even after the legacy database FK is removed")
	}
}

func TestOpenServerDBPreservesLegacyTrafficWithoutTunnelMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server", "netsgo.db")
	oldDB, err := storage.Open(path, []storage.Migration{{
		Name: "001_server_runtime_schema",
		Up: `
CREATE TABLE server_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	initialized INTEGER NOT NULL DEFAULT 0,
	jwt_secret TEXT NOT NULL DEFAULT '',
	server_addr TEXT NOT NULL DEFAULT ''
);
CREATE TABLE client_tokens (
	id TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL,
	install_id TEXT NOT NULL,
	key_id TEXT NOT NULL DEFAULT '',
	client_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	last_active_at TEXT NOT NULL,
	last_ip TEXT NOT NULL DEFAULT '',
	is_revoked INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE admin_sessions (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	username TEXT NOT NULL,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT ''
);
CREATE TABLE registered_clients (
	id TEXT PRIMARY KEY,
	install_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	last_seen TEXT NOT NULL
);
CREATE TABLE tunnels (
	id TEXT NOT NULL DEFAULT '',
	client_id TEXT NOT NULL,
	name TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT '',
	local_ip TEXT NOT NULL DEFAULT '',
	local_port INTEGER NOT NULL DEFAULT 0,
	remote_port INTEGER NOT NULL DEFAULT 0,
	domain TEXT NOT NULL DEFAULT '',
	ingress_bps INTEGER NOT NULL DEFAULT 0,
	egress_bps INTEGER NOT NULL DEFAULT 0,
	desired_state TEXT NOT NULL,
	runtime_state TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	binding TEXT NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (client_id, name)
);
CREATE INDEX idx_tunnels_hostname ON tunnels(hostname);
CREATE TABLE traffic_buckets (
	client_id TEXT NOT NULL,
	tunnel_name TEXT NOT NULL,
	tunnel_type TEXT NOT NULL,
	resolution TEXT NOT NULL,
	bucket_start INTEGER NOT NULL,
	ingress_bytes INTEGER NOT NULL DEFAULT 0,
	egress_bytes INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (client_id, tunnel_name, tunnel_type, resolution, bucket_start)
);
CREATE INDEX idx_traffic_query ON traffic_buckets(client_id, tunnel_name, resolution, bucket_start);
INSERT INTO registered_clients (id, install_id, created_at, last_seen)
VALUES ('client-existing', 'install-existing', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO traffic_buckets (client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes)
VALUES ('client-existing', 'deleted-tunnel', 'tcp', 'minute', 1700000000, 123, 456);
`,
	}})
	if err != nil {
		t.Fatalf("create old schema failed: %v", err)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatalf("oldDB.Close() error = %v", err)
	}

	db, err := openServerDB(path)
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var tunnelID, ownerClientID, targetClientID, topology, transport string
	var ingressBytes, egressBytes int64
	if err := db.QueryRow(`SELECT tunnel_id, owner_client_id, target_client_id, topology, transport, ingress_bytes, egress_bytes FROM traffic_buckets WHERE client_id = ? AND tunnel_name = ?`,
		"client-existing", "deleted-tunnel").Scan(&tunnelID, &ownerClientID, &targetClientID, &topology, &transport, &ingressBytes, &egressBytes); err != nil {
		t.Fatalf("query migrated orphan traffic: %v", err)
	}
	if tunnelID != "legacy:client-existing:deleted-tunnel:tcp" {
		t.Fatalf("synthetic tunnel_id = %q", tunnelID)
	}
	if ownerClientID != "client-existing" || targetClientID != "client-existing" || topology != "server_expose" || transport != "server_relay" {
		t.Fatalf("migrated metadata mismatch: owner=%q target=%q topology=%q transport=%q", ownerClientID, targetClientID, topology, transport)
	}
	if ingressBytes != 123 || egressBytes != 456 {
		t.Fatalf("migrated bytes mismatch: ingress=%d egress=%d", ingressBytes, egressBytes)
	}
}

func TestOpenServerDBDoesNotCreateJsonFiles(t *testing.T) {
	root := t.TempDir()
	db, err := openServerDB(filepath.Join(root, "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, name := range []string{"admin.json", "tunnels.json", "traffic.json"} {
		if pathExists(filepath.Join(root, "server", name)) {
			t.Fatalf("%s should not be created by SQLite storage", name)
		}
	}
}

func strictServerMigrationsBeforeMultiUser(t *testing.T) []storage.Migration {
	t.Helper()
	migrations, err := serverMigrations()
	if err != nil {
		t.Fatalf("serverMigrations() error = %v", err)
	}
	_, strict := partitionServerMigrations(migrations)
	legacyStrict := make([]storage.Migration, 0, len(strict))
	for _, migration := range strict {
		if migration.Name != "012_multi_user_ownership" &&
			migration.Name != "013_global_passkey_challenges" &&
			migration.Name != "014_activity_webhooks" &&
			migration.Name != "016_webhook_notification_interval" {
			legacyStrict = append(legacyStrict, migration)
		}
	}
	return legacyStrict
}

func openServerDBThroughMigration011(t *testing.T, path string) *sql.DB {
	t.Helper()
	migrations, err := serverMigrations()
	if err != nil {
		t.Fatalf("serverMigrations() error = %v", err)
	}
	legacyMigrations := make([]storage.Migration, 0, len(migrations)-1)
	for _, migration := range migrations {
		if migration.Name == "012_multi_user_ownership" ||
			migration.Name == "013_global_passkey_challenges" ||
			migration.Name == "014_activity_webhooks" ||
			migration.Name == "016_webhook_notification_interval" {
			continue
		}
		legacyMigrations = append(legacyMigrations, migration)
	}
	db, err := storage.OpenConfigured(path)
	if err != nil {
		t.Fatalf("OpenConfigured() error = %v", err)
	}
	if err := storage.ApplyMigrationPlan(db, serverMigrationPlan(legacyMigrations)); err != nil {
		_ = db.Close()
		t.Fatalf("apply migrations through 011: %v", err)
	}
	return db
}

func assertNoSQLiteForeignKeyViolations(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var table, rowID, parent, foreignKeyID any
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			t.Fatalf("scan foreign_key_check row: %v", err)
		}
		t.Fatalf("foreign_key_check violation: table=%v row=%v parent=%v foreign_key=%v", table, rowID, parent, foreignKeyID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_check: %v", err)
	}
}

func TestOpenServerDBRejectsInitializedConfigWithoutJWTSecret(t *testing.T) {
	db, err := openServerDB(filepath.Join(t.TempDir(), "server", "netsgo.db"))
	if err != nil {
		t.Fatalf("openServerDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr) VALUES (1, 1, '', 'https://example.com')`); err == nil {
		t.Fatal("initialized server_config without jwt_secret should be rejected")
	}
	if _, err := db.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr) VALUES (1, 2, 'secret', 'https://example.com')`); err == nil {
		t.Fatal("non-boolean initialized value should be rejected")
	}
	if _, err := db.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr) VALUES (1, 1, 'secret', 'https://example.com')`); err != nil {
		t.Fatalf("valid initialized server_config should be accepted: %v", err)
	}
}

func sqliteTableExists(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, table string) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	return err == nil && name == table
}

func sqliteTableColumnExists(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, table, column string) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&name)
	return err == nil && name == column
}

func countTunnelRegisteredClientFKs(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list('tunnels') WHERE "table" = 'registered_clients'`).Scan(&count); err != nil {
		t.Fatalf("query tunnels foreign keys failed: %v", err)
	}
	return count
}

type sqliteColumn struct {
	name         string
	typ          string
	notNull      bool
	defaultValue string
	primaryKey   bool
}

type sqliteIndex struct {
	name    string
	unique  bool
	columns []string
}

func assertSQLiteTables(t *testing.T, db *sql.DB, want map[string][]sqliteColumn) {
	t.Helper()
	gotTables := sqliteUserTables(t, db)
	wantTableNames := sortedKeys(want)
	if !reflect.DeepEqual(gotTables, wantTableNames) {
		t.Fatalf("sqlite tables = %#v, want %#v", gotTables, wantTableNames)
	}

	for table, wantColumns := range want {
		gotColumns := sqliteColumns(t, db, table)
		if !reflect.DeepEqual(gotColumns, wantColumns) {
			t.Fatalf("sqlite columns for %s = %#v, want %#v", table, gotColumns, wantColumns)
		}
	}
}

func assertSQLiteIndexes(t *testing.T, db *sql.DB, want map[string][]sqliteIndex) {
	t.Helper()
	for table, wantIndexes := range want {
		gotIndexes := sqliteIndexes(t, db, table)
		if !reflect.DeepEqual(gotIndexes, wantIndexes) {
			t.Fatalf("sqlite indexes for %s = %#v, want %#v", table, gotIndexes, wantIndexes)
		}
	}
}

func sqliteUserTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("query sqlite tables: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan sqlite table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite tables: %v", err)
	}
	return tables
}

func sqliteColumns(t *testing.T, db *sql.DB, table string) []sqliteColumn {
	t.Helper()
	rows, err := db.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, ''), pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatalf("query sqlite columns for %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var columns []sqliteColumn
	for rows.Next() {
		var column sqliteColumn
		var notNull int
		var primaryKey int
		if err := rows.Scan(&column.name, &column.typ, &notNull, &column.defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan sqlite column for %s: %v", table, err)
		}
		column.notNull = notNull == 1
		column.primaryKey = primaryKey > 0
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite columns for %s: %v", table, err)
	}
	return columns
}

func sqliteIndexes(t *testing.T, db *sql.DB, table string) []sqliteIndex {
	t.Helper()
	rows, err := db.Query(`SELECT name, "unique" FROM pragma_index_list(?) ORDER BY name`, table)
	if err != nil {
		t.Fatalf("query sqlite indexes for %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var indexes []sqliteIndex
	for rows.Next() {
		var index sqliteIndex
		var unique int
		if err := rows.Scan(&index.name, &unique); err != nil {
			t.Fatalf("scan sqlite index for %s: %v", table, err)
		}
		index.unique = unique == 1
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite indexes for %s: %v", table, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close sqlite indexes for %s: %v", table, err)
	}
	for i := range indexes {
		indexes[i].columns = sqliteIndexColumns(t, db, indexes[i].name)
	}
	return indexes
}

func sqliteIndexColumns(t *testing.T, db *sql.DB, indexName string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, indexName)
	if err != nil {
		t.Fatalf("query sqlite index columns for %s: %v", indexName, err)
	}
	defer func() { _ = rows.Close() }()

	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan sqlite index column for %s: %v", indexName, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite index columns for %s: %v", indexName, err)
	}
	return columns
}

func appliedMigrationNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM ` + table + ` ORDER BY name`)
	if err != nil {
		t.Fatalf("query applied migrations from %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan applied migration: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate applied migrations: %v", err)
	}
	return names
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	return true
}

func validMigrationSQL(name string) string {
	return `-- Name: ` + name + `
-- Description: Test migration.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
SELECT 1;

-- Down:
`
}
