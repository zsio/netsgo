-- Name: 011_activity_events
-- Description: Persist activity timeline events and per-severity retention policy.
-- CreatedAt: 2026-07-23T00:00:00Z

-- Up:
ALTER TABLE server_config ADD COLUMN activity_debug_retention_days INTEGER NOT NULL DEFAULT 1 CHECK (activity_debug_retention_days BETWEEN 1 AND 3650);
ALTER TABLE server_config ADD COLUMN activity_debug_min_count INTEGER NOT NULL DEFAULT 200 CHECK (activity_debug_min_count BETWEEN 0 AND 100000);
ALTER TABLE server_config ADD COLUMN activity_info_retention_days INTEGER NOT NULL DEFAULT 7 CHECK (activity_info_retention_days BETWEEN 1 AND 3650);
ALTER TABLE server_config ADD COLUMN activity_info_min_count INTEGER NOT NULL DEFAULT 100 CHECK (activity_info_min_count BETWEEN 0 AND 100000);
ALTER TABLE server_config ADD COLUMN activity_warning_retention_days INTEGER NOT NULL DEFAULT 30 CHECK (activity_warning_retention_days BETWEEN 1 AND 3650);
ALTER TABLE server_config ADD COLUMN activity_warning_min_count INTEGER NOT NULL DEFAULT 100 CHECK (activity_warning_min_count BETWEEN 0 AND 100000);
ALTER TABLE server_config ADD COLUMN activity_error_retention_days INTEGER NOT NULL DEFAULT 180 CHECK (activity_error_retention_days BETWEEN 1 AND 3650);
ALTER TABLE server_config ADD COLUMN activity_error_min_count INTEGER NOT NULL DEFAULT 100 CHECK (activity_error_min_count BETWEEN 0 AND 100000);

CREATE TABLE activity_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	occurred_at_ns INTEGER NOT NULL,
	recorded_at_ns INTEGER NOT NULL,
	severity TEXT NOT NULL CHECK (severity IN ('debug', 'info', 'warning', 'error')),
	category TEXT NOT NULL CHECK (category IN ('client', 'tunnel', 'p2p', 'admin', 'security')),
	action TEXT NOT NULL,
	source TEXT NOT NULL,
	actor_type TEXT NOT NULL DEFAULT '',
	actor_id TEXT NOT NULL DEFAULT '',
	actor_name TEXT NOT NULL DEFAULT '',
	actor_ip_hash TEXT NOT NULL DEFAULT '',
	actor_ip_prefix TEXT NOT NULL DEFAULT '',
	dedupe_key TEXT,
	payload_version INTEGER NOT NULL DEFAULT 1 CHECK (payload_version >= 1),
	payload_json TEXT NOT NULL DEFAULT '{}'
);

CREATE UNIQUE INDEX idx_activity_events_dedupe_key
	ON activity_events(dedupe_key)
	WHERE dedupe_key IS NOT NULL;
CREATE INDEX idx_activity_events_occurred
	ON activity_events(occurred_at_ns DESC, id DESC);
CREATE INDEX idx_activity_events_severity_occurred
	ON activity_events(severity, occurred_at_ns DESC, id DESC);
CREATE INDEX idx_activity_events_category_occurred
	ON activity_events(category, occurred_at_ns DESC, id DESC);
CREATE INDEX idx_activity_events_severity_id
	ON activity_events(severity, id DESC);
CREATE INDEX idx_activity_events_category_id
	ON activity_events(category, id DESC);

CREATE TABLE activity_event_clients (
	event_id INTEGER NOT NULL REFERENCES activity_events(id) ON DELETE CASCADE,
	client_id TEXT NOT NULL,
	relation TEXT NOT NULL CHECK (relation IN ('owner', 'ingress', 'target', 'peer', 'subject', 'related')),
	display_name TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	is_truncated INTEGER NOT NULL DEFAULT 0 CHECK (is_truncated IN (0, 1)),
	PRIMARY KEY (event_id, client_id, relation)
);
CREATE INDEX idx_activity_event_clients_client
	ON activity_event_clients(client_id, event_id DESC);

CREATE TABLE activity_event_tunnels (
	event_id INTEGER NOT NULL REFERENCES activity_events(id) ON DELETE CASCADE,
	tunnel_id TEXT NOT NULL,
	relation TEXT NOT NULL CHECK (relation IN ('subject', 'related', 'shared_session')),
	name TEXT NOT NULL DEFAULT '',
	tunnel_type TEXT NOT NULL DEFAULT '',
	topology TEXT NOT NULL DEFAULT '',
	is_truncated INTEGER NOT NULL DEFAULT 0 CHECK (is_truncated IN (0, 1)),
	PRIMARY KEY (event_id, tunnel_id, relation)
);
CREATE INDEX idx_activity_event_tunnels_tunnel
	ON activity_event_tunnels(tunnel_id, event_id DESC);

-- Down:
DROP TABLE activity_event_tunnels;
DROP TABLE activity_event_clients;
DROP TABLE activity_events;
ALTER TABLE server_config DROP COLUMN activity_error_min_count;
ALTER TABLE server_config DROP COLUMN activity_error_retention_days;
ALTER TABLE server_config DROP COLUMN activity_warning_min_count;
ALTER TABLE server_config DROP COLUMN activity_warning_retention_days;
ALTER TABLE server_config DROP COLUMN activity_info_min_count;
ALTER TABLE server_config DROP COLUMN activity_info_retention_days;
ALTER TABLE server_config DROP COLUMN activity_debug_min_count;
ALTER TABLE server_config DROP COLUMN activity_debug_retention_days;
