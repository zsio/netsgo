-- Name: 002_rebuild_tunnels_without_registered_client_fk
-- Description: Rebuild tunnels without a registered_clients foreign key.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
CREATE TABLE tunnels_new (
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
	PRIMARY KEY (client_id, name)
);
INSERT INTO tunnels_new (client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, desired_state, runtime_state, error, hostname, binding)
SELECT client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, desired_state, runtime_state, error, hostname, binding
FROM tunnels;
DROP TABLE tunnels;
ALTER TABLE tunnels_new RENAME TO tunnels;
CREATE INDEX idx_tunnels_hostname ON tunnels(hostname);

-- Down:
