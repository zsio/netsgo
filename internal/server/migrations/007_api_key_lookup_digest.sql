-- Name: 007_api_key_lookup_digest
-- Description: Add API key lookup digest for candidate selection before bcrypt verification.
-- CreatedAt: 2026-06-15T00:00:00Z

-- Up:
CREATE TABLE IF NOT EXISTS api_keys (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	key_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT,
	is_active INTEGER NOT NULL,
	max_uses INTEGER NOT NULL,
	use_count INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS api_key_permissions (
	api_key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
	permission TEXT NOT NULL,
	PRIMARY KEY (api_key_id, permission)
);
ALTER TABLE api_keys ADD COLUMN lookup_digest TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_api_keys_lookup_digest ON api_keys(lookup_digest);

-- Down:
