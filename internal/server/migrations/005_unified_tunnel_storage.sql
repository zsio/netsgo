-- Name: 005_unified_tunnel_storage
-- Description: Rebuild tunnel and traffic storage around the unified TunnelSpec model.
-- CreatedAt: 2026-05-17T00:00:00Z

-- Up:
ALTER TABLE registered_clients ADD COLUMN last_capabilities TEXT NOT NULL DEFAULT '{}';

DROP INDEX IF EXISTS idx_tunnels_hostname;
DROP INDEX IF EXISTS idx_tunnels_id;
ALTER TABLE tunnels RENAME TO tunnels_legacy_unified_migration;

CREATE TABLE tunnels (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	client_id TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT '',
	local_ip TEXT NOT NULL DEFAULT '',
	local_port INTEGER NOT NULL DEFAULT 0,
	remote_port INTEGER NOT NULL DEFAULT 0,
	domain TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	binding TEXT NOT NULL DEFAULT 'client_id',
	revision INTEGER NOT NULL DEFAULT 1,
	topology TEXT NOT NULL,
	owner_client_id TEXT NOT NULL,
	ingress_location TEXT NOT NULL,
	ingress_client_id TEXT NOT NULL DEFAULT '',
	ingress_type TEXT NOT NULL,
	ingress_config TEXT NOT NULL DEFAULT '{}',
	ingress_bind_ip TEXT NOT NULL DEFAULT '',
	ingress_port INTEGER NOT NULL DEFAULT 0,
	ingress_domain TEXT NOT NULL DEFAULT '',
	ingress_path TEXT NOT NULL DEFAULT '',
	target_location TEXT NOT NULL,
	target_client_id TEXT NOT NULL DEFAULT '',
	target_type TEXT NOT NULL,
	target_config TEXT NOT NULL DEFAULT '{}',
	target_host TEXT NOT NULL DEFAULT '',
	target_port INTEGER NOT NULL DEFAULT 0,
	target_path TEXT NOT NULL DEFAULT '',
	target_resource_key TEXT NOT NULL DEFAULT '',
	transport_policy TEXT NOT NULL,
	actual_transport TEXT NOT NULL DEFAULT 'unknown',
	p2p_state TEXT NOT NULL DEFAULT 'idle',
	p2p_error TEXT NOT NULL DEFAULT '',
	p2p_session_id TEXT NOT NULL DEFAULT '',
	ingress_bps INTEGER NOT NULL DEFAULT 0,
	egress_bps INTEGER NOT NULL DEFAULT 0,
	desired_state TEXT NOT NULL,
	runtime_state TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	created_by_user_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	CHECK (topology IN ('server_expose', 'client_to_client')),
	CHECK (ingress_location IN ('server', 'client')),
	CHECK (target_location IN ('client')),
	CHECK (
		(topology = 'server_expose' AND ingress_location = 'server' AND ingress_client_id = '' AND target_location = 'client' AND target_client_id <> '')
		OR
		(topology = 'client_to_client' AND ingress_location = 'client' AND ingress_client_id <> '' AND target_location = 'client' AND target_client_id <> '')
	),
	CHECK (ingress_type IN ('tcp_listen', 'udp_listen', 'http_host')),
	CHECK (target_type IN ('tcp_service', 'udp_service')),
	CHECK (transport_policy IN ('server_relay_only', 'direct_preferred', 'direct_only')),
	CHECK (actual_transport IN ('unknown', 'server_relay', 'peer_direct', 'turn_relay')),
	CHECK (p2p_state IN ('idle', 'gathering', 'checking', 'connected', 'failed', 'fallback', 'closed')),
	CHECK (desired_state IN ('running', 'stopped')),
	CHECK (runtime_state IN ('pending', 'active', 'offline', 'idle', 'error')),
	UNIQUE(client_id, name),
	UNIQUE(owner_client_id, name)
);

INSERT INTO tunnels (
	id, name, client_id, type, local_ip, local_port, remote_port, domain, hostname, binding,
	revision, topology, owner_client_id,
	ingress_location, ingress_client_id, ingress_type, ingress_config, ingress_bind_ip, ingress_port, ingress_domain, ingress_path,
	target_location, target_client_id, target_type, target_config, target_host, target_port, target_path, target_resource_key,
	transport_policy, actual_transport, p2p_state, p2p_error, p2p_session_id,
	ingress_bps, egress_bps, desired_state, runtime_state, error, created_by_user_id, created_at, updated_at
)
SELECT
	CASE WHEN id <> '' THEN id ELSE lower(hex(randomblob(16))) END,
	name,
	client_id,
	CASE WHEN type IN ('tcp', 'udp', 'http') THEN type ELSE 'tcp' END,
	local_ip,
	local_port,
	remote_port,
	domain,
	hostname,
	'client_id',
	1,
	'server_expose',
	client_id,
	'server',
	'',
	CASE WHEN type = 'udp' THEN 'udp_listen' WHEN type = 'http' THEN 'http_host' ELSE 'tcp_listen' END,
	'{}',
	CASE WHEN type = 'http' THEN '' ELSE '0.0.0.0' END,
	CASE WHEN type = 'http' THEN 0 ELSE remote_port END,
	CASE WHEN type = 'http' THEN domain ELSE '' END,
	'',
	'client',
	client_id,
	CASE WHEN type = 'udp' THEN 'udp_service' ELSE 'tcp_service' END,
	'{}',
	local_ip,
	local_port,
	'',
	CASE WHEN type = 'udp' THEN 'target:client:' || client_id || ':udp_service:' || local_ip || ':' || local_port ELSE 'target:client:' || client_id || ':tcp_service:' || local_ip || ':' || local_port END,
	'server_relay_only',
	CASE WHEN runtime_state = 'exposed' THEN 'server_relay' ELSE 'unknown' END,
	'idle',
	'',
	'',
	ingress_bps,
	egress_bps,
	CASE WHEN desired_state = 'paused' THEN 'stopped' ELSE desired_state END,
	CASE WHEN runtime_state = 'exposed' THEN 'active' ELSE runtime_state END,
	error,
	'',
	created_at,
	created_at
FROM tunnels_legacy_unified_migration;

DROP TABLE tunnels_legacy_unified_migration;

CREATE INDEX idx_tunnels_hostname ON tunnels(hostname);
CREATE INDEX idx_tunnels_owner ON tunnels(owner_client_id, created_at);
CREATE INDEX idx_tunnels_ingress_client ON tunnels(ingress_client_id);
CREATE INDEX idx_tunnels_target_client ON tunnels(target_client_id);
CREATE INDEX idx_tunnels_topology ON tunnels(topology);
CREATE INDEX idx_tunnels_runtime_state ON tunnels(runtime_state);
CREATE INDEX idx_tunnels_ingress_port ON tunnels(ingress_location, ingress_client_id, ingress_type, ingress_bind_ip, ingress_port);
CREATE INDEX idx_tunnels_ingress_domain ON tunnels(ingress_domain);
CREATE INDEX idx_tunnels_target_resource ON tunnels(target_location, target_client_id, target_type, target_resource_key);

CREATE TABLE tunnel_resource_locks (
	resource_key TEXT PRIMARY KEY,
	tunnel_id TEXT NOT NULL,
	resource_kind TEXT NOT NULL,
	client_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

INSERT INTO tunnel_resource_locks (resource_key, tunnel_id, resource_kind, client_id, created_at)
SELECT
	CASE
		WHEN ingress_type = 'tcp_listen' THEN 'ingress:server:tcp:' || ingress_bind_ip || ':' || ingress_port
		WHEN ingress_type = 'udp_listen' THEN 'ingress:server:udp:' || ingress_bind_ip || ':' || ingress_port
		WHEN ingress_type = 'http_host' THEN 'ingress:server:http_host:' || lower(ingress_domain)
	END,
	id,
	CASE
		WHEN ingress_type = 'tcp_listen' THEN 'server_tcp_port'
		WHEN ingress_type = 'udp_listen' THEN 'server_udp_port'
		WHEN ingress_type = 'http_host' THEN 'server_http_host'
	END,
	'',
	created_at
FROM tunnels;

CREATE INDEX idx_tunnel_resource_locks_tunnel ON tunnel_resource_locks(tunnel_id);
CREATE INDEX idx_tunnel_resource_locks_client ON tunnel_resource_locks(client_id);

DROP INDEX IF EXISTS idx_traffic_query;
ALTER TABLE traffic_buckets RENAME TO traffic_buckets_legacy_unified_migration;

CREATE TABLE traffic_buckets (
	tunnel_id TEXT NOT NULL,
	owner_client_id TEXT NOT NULL,
	ingress_client_id TEXT NOT NULL DEFAULT '',
	target_client_id TEXT NOT NULL DEFAULT '',
	topology TEXT NOT NULL,
	transport TEXT NOT NULL,
	client_id TEXT NOT NULL DEFAULT '',
	tunnel_name TEXT NOT NULL DEFAULT '',
	tunnel_type TEXT NOT NULL DEFAULT '',
	resolution TEXT NOT NULL,
	bucket_start INTEGER NOT NULL,
	ingress_bytes INTEGER NOT NULL DEFAULT 0,
	egress_bytes INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (tunnel_id, transport, resolution, bucket_start)
);

INSERT INTO traffic_buckets (
	tunnel_id, owner_client_id, ingress_client_id, target_client_id, topology, transport,
	client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes
)
SELECT
	t.id,
	t.owner_client_id,
	t.ingress_client_id,
	t.target_client_id,
	t.topology,
	'server_relay',
	b.client_id,
	b.tunnel_name,
	b.tunnel_type,
	b.resolution,
	b.bucket_start,
	b.ingress_bytes,
	b.egress_bytes
FROM traffic_buckets_legacy_unified_migration b
JOIN tunnels t ON t.client_id = b.client_id AND t.name = b.tunnel_name;

DROP TABLE traffic_buckets_legacy_unified_migration;

CREATE INDEX idx_traffic_owner_query ON traffic_buckets(owner_client_id, resolution, bucket_start);
CREATE INDEX idx_traffic_ingress_query ON traffic_buckets(ingress_client_id, resolution, bucket_start);
CREATE INDEX idx_traffic_target_query ON traffic_buckets(target_client_id, resolution, bucket_start);
CREATE INDEX idx_traffic_compat_query ON traffic_buckets(client_id, tunnel_name, resolution, bucket_start);

-- Down:
