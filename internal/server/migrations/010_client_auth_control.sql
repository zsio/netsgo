-- Name: 010_client_auth_control
-- Description: Persist optional client authentication rate limiting.
-- CreatedAt: 2026-07-21T00:00:00Z

-- Up:
ALTER TABLE server_config ADD COLUMN client_auth_rate_limit_enabled INTEGER NOT NULL DEFAULT 0 CHECK (client_auth_rate_limit_enabled IN (0, 1));
ALTER TABLE server_config ADD COLUMN client_auth_rate_limit_per_minute INTEGER NOT NULL DEFAULT 20 CHECK (client_auth_rate_limit_per_minute BETWEEN 1 AND 1000);

-- Down:
ALTER TABLE server_config DROP COLUMN client_auth_rate_limit_per_minute;
ALTER TABLE server_config DROP COLUMN client_auth_rate_limit_enabled;
