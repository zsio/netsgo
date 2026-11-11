package server

import (
	"database/sql"
	"errors"
	"time"
)

// Cooldowns are checked in the activity transaction and advanced only after a
// delivery is queued. Skipped events keep their activity records and do not
// extend the window. Test calls, replays and retries bypass this event filter.
func webhookNotificationTargetsTx(tx *sql.Tx, ownerUserID string, config webhookConfigSnapshot, eventType string, targets []string, now time.Time) ([]string, error) {
	if config.MinIntervalSeconds == 0 {
		return targets, nil
	}
	cutoff := now.Add(-time.Duration(config.MinIntervalSeconds) * time.Second).UnixNano()
	eligible := make([]string, 0, len(targets))
	for _, targetID := range targets {
		var lastEnqueued int64
		err := tx.QueryRow(`SELECT last_enqueued_at_ns FROM activity_webhook_notification_state
			WHERE owner_user_id = ? AND webhook_id = ? AND event_type = ? AND target_id = ?`,
			ownerUserID, config.ID, eventType, targetID).Scan(&lastEnqueued)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if errors.Is(err, sql.ErrNoRows) || lastEnqueued <= cutoff {
			eligible = append(eligible, targetID)
		}
	}
	return eligible, nil
}

func recordWebhookNotificationTx(tx *sql.Tx, ownerUserID string, config webhookConfigSnapshot, eventType string, targets []string, now time.Time) error {
	if config.MinIntervalSeconds == 0 {
		return nil
	}
	for _, targetID := range targets {
		_, err := tx.Exec(`INSERT INTO activity_webhook_notification_state
			(owner_user_id, webhook_id, event_type, target_id, last_enqueued_at_ns) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (owner_user_id, webhook_id, event_type, target_id)
			DO UPDATE SET last_enqueued_at_ns = excluded.last_enqueued_at_ns`,
			ownerUserID, config.ID, eventType, targetID, now.UnixNano())
		if err != nil {
			return err
		}
	}
	return nil
}
