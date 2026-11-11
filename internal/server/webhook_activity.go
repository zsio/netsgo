package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
)

func enqueueActivityWebhookDeliveriesTx(tx *sql.Tx, activityID int64, prepared preparedActivitySpec) error {
	if tx == nil || activityID <= 0 || prepared.scopeUserID == "" {
		return nil
	}
	eventType := string(prepared.category) + "." + prepared.action
	targetKind, supported := webhookEventTargetKinds[eventType]
	if !supported {
		return nil
	}
	rows, err := tx.Query(`SELECT w.id, w.revision, w.name, w.target_kind, w.target_mode,
		w.method, w.url_template, w.headers_json, w.body_template, w.min_interval_seconds
		FROM activity_webhooks w
		JOIN activity_webhook_events e
			ON e.owner_user_id = w.owner_user_id AND e.webhook_id = w.id
		WHERE w.owner_user_id = ? AND w.enabled = 1 AND e.event_type = ?
		ORDER BY w.created_at_ns, w.id`, prepared.scopeUserID, eventType)
	if err != nil {
		return fmt.Errorf("find matching activity Webhooks: %w", err)
	}
	type candidate struct {
		config webhookConfigSnapshot
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		var headersJSON string
		if err := rows.Scan(&item.config.ID, &item.config.Revision, &item.config.Name, &item.config.TargetKind,
			&item.config.TargetMode, &item.config.Method, &item.config.URL, &headersJSON, &item.config.Body, &item.config.MinIntervalSeconds); err != nil {
			_ = rows.Close()
			return err
		}
		if item.config.TargetKind != targetKind {
			continue
		}
		if err := json.Unmarshal([]byte(headersJSON), &item.config.Headers); err != nil {
			_ = rows.Close()
			return fmt.Errorf("decode activity Webhook headers: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range candidates {
		events, err := queryStringColumn(tx, `SELECT event_type FROM activity_webhook_events WHERE owner_user_id = ? AND webhook_id = ? ORDER BY event_type`, prepared.scopeUserID, candidates[index].config.ID)
		if err != nil {
			return err
		}
		targets, err := queryStringColumn(tx, `SELECT target_id FROM activity_webhook_targets WHERE owner_user_id = ? AND webhook_id = ? ORDER BY target_id`, prepared.scopeUserID, candidates[index].config.ID)
		if err != nil {
			return err
		}
		candidates[index].config.Events = events
		candidates[index].config.TargetIDs = targets
	}
	for _, item := range candidates {
		baseSnapshot, err := webhookEventSnapshotFromPrepared(activityID, prepared, nil)
		if err != nil {
			return err
		}
		matched := matchedWebhookTargetIDs(item.config.TargetKind, item.config.TargetMode, item.config.TargetIDs, baseSnapshot)
		matched, err = webhookNotificationTargetsTx(tx, prepared.scopeUserID, item.config, eventType, matched, prepared.recordedAt)
		if err != nil {
			return err
		}
		if len(matched) == 0 {
			continue
		}
		baseSnapshot.MatchedTargetIDs = matched
		deliveryID := "dlv_" + generateUUID()
		values := baseSnapshot.values(deliveryID, item.config.ID, item.config.Name)
		if err := insertWebhookDeliveryTx(tx, prepared.scopeUserID, item.config, baseSnapshot, values,
			WebhookOriginEvent, sql.NullInt64{Int64: activityID, Valid: true}, 3, prepared.recordedAt); err != nil {
			var validation *webhookValidationError
			if errors.As(err, &validation) || errors.Is(err, ErrWebhookPendingFull) {
				// Runtime variables (for example a client display name) can
				// render an otherwise valid template invalid, and a saturated
				// per-user pending budget must not drop the activity itself.
				// Skip only this Webhook delivery and keep recording the event.
				log.Printf("⚠️ Skipping Webhook delivery [webhook_id=%s activity_id=%d]: %v", item.config.ID, activityID, err)
				continue
			}
			return err
		}
		if err := recordWebhookNotificationTx(tx, prepared.scopeUserID, item.config, eventType, matched, prepared.recordedAt); err != nil {
			return err
		}
	}
	return nil
}
