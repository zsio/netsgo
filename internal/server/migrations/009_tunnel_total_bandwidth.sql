-- Name: 009_tunnel_total_bandwidth
-- Description: Add an explicit shared bidirectional tunnel bandwidth limit.
-- CreatedAt: 2026-07-11T00:00:00Z

-- Up:
ALTER TABLE tunnels ADD COLUMN total_bps INTEGER NOT NULL DEFAULT 0 CHECK (total_bps >= 0);

-- Down:
ALTER TABLE tunnels DROP COLUMN total_bps;
