-- Name: 016_webhook_notification_interval
-- Description: Persist per-target, per-event Webhook notification intervals.
-- CreatedAt: 2026-11-10T00:00:00Z

-- Up:
ALTER TABLE activity_webhooks ADD COLUMN min_interval_seconds INTEGER NOT NULL DEFAULT 0 CHECK (min_interval_seconds BETWEEN 0 AND 86400);

CREATE TABLE activity_webhook_notification_state (
	owner_user_id TEXT NOT NULL,
	webhook_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	target_id TEXT NOT NULL,
	last_enqueued_at_ns INTEGER NOT NULL,
	PRIMARY KEY (owner_user_id, webhook_id, event_type, target_id),
	FOREIGN KEY (owner_user_id, webhook_id)
		REFERENCES activity_webhooks(owner_user_id, id) ON DELETE CASCADE
);

-- Down:
DROP TABLE activity_webhook_notification_state;
ALTER TABLE activity_webhooks DROP COLUMN min_interval_seconds;
