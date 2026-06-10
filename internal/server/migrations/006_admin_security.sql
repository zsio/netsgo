-- Name: 006_admin_security
-- Description: Add administrator MFA and passkey security storage.
-- CreatedAt: 2026-06-08T00:00:00Z

-- Up:
CREATE TABLE IF NOT EXISTS admin_users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	last_login TEXT
);
ALTER TABLE admin_users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0 CHECK (totp_enabled IN (0, 1));
ALTER TABLE admin_users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';

CREATE TABLE admin_totp_recovery_codes (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	code_hash TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	used_at TEXT
);
CREATE INDEX idx_admin_totp_recovery_codes_user_unused ON admin_totp_recovery_codes(user_id, used_at);

CREATE TABLE admin_passkeys (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	name TEXT NOT NULL,
	credential_id TEXT NOT NULL UNIQUE,
	credential_json TEXT NOT NULL,
	rp_id TEXT NOT NULL,
	origin TEXT NOT NULL,
	created_at TEXT NOT NULL,
	last_used_at TEXT
);
CREATE INDEX idx_admin_passkeys_user ON admin_passkeys(user_id);
CREATE INDEX idx_admin_passkeys_rp ON admin_passkeys(rp_id, origin);

CREATE TABLE admin_auth_challenges (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	session_json TEXT NOT NULL,
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);
CREATE INDEX idx_admin_auth_challenges_user_kind ON admin_auth_challenges(user_id, kind);
CREATE INDEX idx_admin_auth_challenges_expires ON admin_auth_challenges(expires_at);

-- Down:
