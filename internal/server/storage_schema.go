package server

import (
	"database/sql"

	"netsgo/internal/storage"
)

const serverDBFileName = "netsgo.db"

func openServerDB(path string) (*sql.DB, error) {
	return storage.Open(path, serverMigrations())
}

func serverMigrations() []storage.Migration {
	return []storage.Migration{{
		Name: "001_server_runtime_schema",
		Up: `
CREATE TABLE server_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	initialized INTEGER NOT NULL DEFAULT 0 CHECK (initialized IN (0, 1)),
	jwt_secret TEXT NOT NULL DEFAULT '',
	server_addr TEXT NOT NULL DEFAULT '',
	CHECK (initialized = 0 OR jwt_secret <> '')
);
CREATE TABLE allowed_ports (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	start_port INTEGER NOT NULL,
	end_port INTEGER NOT NULL,
	CHECK (start_port >= 1 AND start_port <= 65535),
	CHECK (end_port >= 1 AND end_port <= 65535),
	CHECK (start_port <= end_port)
);
CREATE TABLE admin_users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	last_login TEXT
);
CREATE TABLE api_keys (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	key_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT,
	is_active INTEGER NOT NULL,
	max_uses INTEGER NOT NULL,
	use_count INTEGER NOT NULL
);
CREATE TABLE api_key_permissions (
	api_key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
	permission TEXT NOT NULL,
	PRIMARY KEY (api_key_id, permission)
);
CREATE TABLE registered_clients (
	id TEXT PRIMARY KEY,
	install_id TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	os TEXT NOT NULL DEFAULT '',
	arch TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	version TEXT NOT NULL DEFAULT '',
	public_ipv4 TEXT NOT NULL DEFAULT '',
	public_ipv6 TEXT NOT NULL DEFAULT '',
	ingress_bps INTEGER NOT NULL DEFAULT 0,
	egress_bps INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	last_seen TEXT NOT NULL,
	last_ip TEXT NOT NULL DEFAULT ''
);
CREATE TABLE client_stats (
	client_id TEXT PRIMARY KEY REFERENCES registered_clients(id) ON DELETE CASCADE,
	cpu_usage REAL NOT NULL DEFAULT 0,
	mem_total INTEGER NOT NULL DEFAULT 0,
	mem_used INTEGER NOT NULL DEFAULT 0,
	mem_usage REAL NOT NULL DEFAULT 0,
	disk_total INTEGER NOT NULL DEFAULT 0,
	disk_used INTEGER NOT NULL DEFAULT 0,
	disk_usage REAL NOT NULL DEFAULT 0,
	net_sent INTEGER NOT NULL DEFAULT 0,
	net_recv INTEGER NOT NULL DEFAULT 0,
	net_sent_speed REAL NOT NULL DEFAULT 0,
	net_recv_speed REAL NOT NULL DEFAULT 0,
	uptime INTEGER NOT NULL DEFAULT 0,
	process_uptime INTEGER NOT NULL DEFAULT 0,
	os_install_time INTEGER NOT NULL DEFAULT 0,
	num_cpu INTEGER NOT NULL DEFAULT 0,
	app_mem_used INTEGER NOT NULL DEFAULT 0,
	app_mem_sys INTEGER NOT NULL DEFAULT 0,
	public_ipv4 TEXT NOT NULL DEFAULT '',
	public_ipv6 TEXT NOT NULL DEFAULT '',
	updated_at TEXT,
	fresh_until TEXT
);
CREATE TABLE client_disk_partitions (
	client_id TEXT NOT NULL REFERENCES registered_clients(id) ON DELETE CASCADE,
	path TEXT NOT NULL,
	used INTEGER NOT NULL DEFAULT 0,
	total INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (client_id, path)
);
CREATE TABLE client_tokens (
	id TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	install_id TEXT NOT NULL,
	key_id TEXT NOT NULL DEFAULT '',
	client_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	last_active_at TEXT NOT NULL,
	last_ip TEXT NOT NULL DEFAULT '',
	is_revoked INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_client_tokens_install_active ON client_tokens(install_id, is_revoked, last_active_at);
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
CREATE INDEX idx_admin_sessions_user ON admin_sessions(user_id);
CREATE INDEX idx_admin_sessions_expires ON admin_sessions(expires_at);
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
`,
	}}
}
