-- Name: 010_client_auth_control
-- Description: Persist optional client authentication rate limiting and renew surviving client tokens for safe upgrade reconnects.
-- CreatedAt: 2026-07-21T00:00:00Z

-- Up:
ALTER TABLE server_config ADD COLUMN client_auth_rate_limit_enabled INTEGER NOT NULL DEFAULT 0 CHECK (client_auth_rate_limit_enabled IN (0, 1));
ALTER TABLE server_config ADD COLUMN client_auth_rate_limit_per_minute INTEGER NOT NULL DEFAULT 20 CHECK (client_auth_rate_limit_per_minute BETWEEN 1 AND 1000);
UPDATE client_tokens
SET last_active_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE is_revoked = 0;

-- Down:
ALTER TABLE server_config DROP COLUMN client_auth_rate_limit_per_minute;
ALTER TABLE server_config DROP COLUMN client_auth_rate_limit_enabled;
