-- Name: 003_tunnel_stable_id
-- Description: Add stable tunnel IDs.
-- CreatedAt: 2026-05-15T00:00:00Z

-- Up:
ALTER TABLE tunnels ADD COLUMN id TEXT NOT NULL DEFAULT '';
UPDATE tunnels SET id = lower(hex(randomblob(16))) WHERE id = '';
CREATE UNIQUE INDEX idx_tunnels_id ON tunnels(id);

-- Down:
DROP INDEX IF EXISTS idx_tunnels_id;
