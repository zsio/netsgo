-- Name: 004_tunnel_created_at
-- Description: Add tunnel creation timestamps.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
ALTER TABLE tunnels ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
UPDATE tunnels SET created_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE created_at = '';

-- Down:
