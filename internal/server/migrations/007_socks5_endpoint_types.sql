-- Name: 007_socks5_endpoint_types
-- Description: Allow SOCKS5 endpoint types in unified tunnel storage.
-- CreatedAt: 2026-06-20T00:00:00Z

-- Up:
ALTER TABLE tunnels RENAME TO tunnels_socks5_endpoint_types_migration;

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
	CHECK (ingress_type IN ('tcp_listen', 'udp_listen', 'http_host', 'socks5_listen')),
	CHECK (target_type IN ('tcp_service', 'udp_service', 'socks5_connect_handler')),
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
	id, name, client_id, type, local_ip, local_port, remote_port, domain, hostname, binding,
	revision, topology, owner_client_id,
	ingress_location, ingress_client_id, ingress_type, ingress_config, ingress_bind_ip, ingress_port, ingress_domain, ingress_path,
	target_location, target_client_id, target_type, target_config, target_host, target_port, target_path, target_resource_key,
	transport_policy, actual_transport, p2p_state, p2p_error, p2p_session_id,
	ingress_bps, egress_bps, desired_state, runtime_state, error, created_by_user_id, created_at, updated_at
FROM tunnels_socks5_endpoint_types_migration;

DROP TABLE tunnels_socks5_endpoint_types_migration;

CREATE INDEX idx_tunnels_hostname ON tunnels(hostname);
CREATE INDEX idx_tunnels_owner ON tunnels(owner_client_id, created_at);
CREATE INDEX idx_tunnels_ingress_client ON tunnels(ingress_client_id);
CREATE INDEX idx_tunnels_target_client ON tunnels(target_client_id);
CREATE INDEX idx_tunnels_topology ON tunnels(topology);
CREATE INDEX idx_tunnels_runtime_state ON tunnels(runtime_state);
CREATE INDEX idx_tunnels_ingress_port ON tunnels(ingress_location, ingress_client_id, ingress_type, ingress_bind_ip, ingress_port);
CREATE INDEX idx_tunnels_ingress_domain ON tunnels(ingress_domain);
CREATE INDEX idx_tunnels_target_resource ON tunnels(target_location, target_client_id, target_type, target_resource_key);

-- Down:
