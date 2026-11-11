package server

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// webhookMaxPendingPerUser caps queued+retrying deliveries per user across all
// origins. Event traffic beyond this budget is dropped (activity events are
// still recorded); test/replay calls surface an API error.
const webhookMaxPendingPerUser = 1000

var (
	ErrWebhookNotFound          = errors.New("webhook not found")
	ErrWebhookRevisionConflict  = errors.New("webhook revision conflict")
	ErrWebhookLimitReached      = errors.New("webhook limit reached")
	ErrWebhookDeliveryNotFound  = errors.New("webhook delivery not found")
	ErrWebhookReplayUnavailable = errors.New("webhook delivery cannot be replayed")
	ErrWebhookDailyCapReached   = errors.New("webhook daily delivery cap reached")
	ErrWebhookPendingFull       = errors.New("webhook pending delivery limit reached")
)

type WebhookStore struct {
	db       *sql.DB
	now      func() time.Time
	settings func() WebhookSettings
}

// currentSettings resolves the effective outbound policy. A nil or
// non-positive loader falls back to the safe defaults.
func (s *WebhookStore) currentSettings() WebhookSettings {
	if s.settings != nil {
		if settings := s.settings(); settings.DailyDeliveryCap > 0 {
			return settings
		}
	}
	return defaultWebhookSettings()
}

// ensureDailyCapTx rejects user-initiated (test/replay) deliveries once the
// owner has enqueued cap deliveries within the last 24 hours. The cap must be
// resolved before the transaction opens: the store shares a single pooled
// connection, so reading settings inside the transaction would self-deadlock.
func ensureDailyCapTx(tx *sql.Tx, ownerUserID string, now time.Time, cap int) error {
	if cap <= 0 {
		return nil
	}
	var recent int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM activity_webhook_deliveries
		WHERE owner_user_id = ? AND origin IN ('test', 'replay') AND created_at_ns >= ?`,
		ownerUserID, now.Add(-24*time.Hour).UnixNano()).Scan(&recent); err != nil {
		return err
	}
	if recent >= cap {
		return fmt.Errorf("%w: limit is %d per day", ErrWebhookDailyCapReached, cap)
	}
	return nil
}

// ensurePendingCapTx bounds how many queued/retrying deliveries a single user
// may accumulate across ALL origins (events included). Without it a user
// flapping their own tunnels could enqueue durable rows faster than the
// dispatcher drains them (one in-flight request per user every 2s) and grow
// the SQLite file without limit.
func ensurePendingCapTx(tx *sql.Tx, ownerUserID string) error {
	var pending int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM activity_webhook_deliveries
		WHERE owner_user_id = ? AND status IN ('queued', 'retrying')`, ownerUserID).Scan(&pending); err != nil {
		return err
	}
	if pending >= webhookMaxPendingPerUser {
		return fmt.Errorf("%w: limit is %d", ErrWebhookPendingFull, webhookMaxPendingPerUser)
	}
	return nil
}
func newWebhookStoreWithDB(db *sql.DB) *WebhookStore {
	return &WebhookStore{db: db, now: time.Now}
}

type WebhookPreview struct {
	Event   string            `json:"event"`
	Values  map[string]any    `json:"values"`
	Method  WebhookMethod     `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
}

type WebhookDeliveryAttempt struct {
	Number     int    `json:"number"`
	OccurredAt string `json:"occurred_at"`
	Status     string `json:"status"`
	StatusCode *int   `json:"status_code"`
	DurationMS *int64 `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type WebhookDelivery struct {
	ID              string                   `json:"id"`
	WebhookID       string                   `json:"webhook_id"`
	WebhookName     string                   `json:"webhook_name"`
	EventID         string                   `json:"event_id"`
	Event           string                   `json:"event"`
	OccurredAt      string                   `json:"occurred_at"`
	Status          WebhookDeliveryStatus    `json:"status"`
	Origin          WebhookDeliveryOrigin    `json:"origin"`
	StatusCode      *int                     `json:"status_code"`
	DurationMS      *int64                   `json:"duration_ms"`
	Attempts        []WebhookDeliveryAttempt `json:"attempts"`
	RequestMethod   WebhookMethod            `json:"request_method"`
	RequestURL      string                   `json:"request_url"`
	RequestHeaders  map[string]string        `json:"request_headers"`
	RequestBody     *string                  `json:"request_body"`
	ResponseHeaders map[string]string        `json:"response_headers"`
	ResponseBody    string                   `json:"response_body"`
	Error           string                   `json:"error,omitempty"`
	NextAttemptAt   *string                  `json:"next_attempt_at,omitempty"`
	CreatedAt       string                   `json:"created_at"`
}

type WebhookDeliveryPage struct {
	Items      []WebhookDelivery `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

type webhookDeliveryCursor struct {
	CreatedAtNS int64  `json:"created_at_ns"`
	ID          string `json:"id"`
}

type webhookStoredDelivery struct {
	WebhookDelivery
	OwnerUserID     string
	SourceEventID   sql.NullInt64
	AttemptCount    int
	MaxAttempts     int
	NextAttemptAtNS int64
	LeaseUntilNS    sql.NullInt64
	ConfigRevision  int64
	ConfigSnapshot  webhookConfigSnapshot
	EventSnapshot   webhookEventSnapshot
	ValuesSnapshot  map[string]any
	CreatedAtNS     int64
	StartedAtNS     sql.NullInt64
	CompletedAtNS   sql.NullInt64
	UpdatedAtNS     int64
}

func (s *WebhookStore) List(ownerUserID string) ([]ActivityWebhook, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("webhook store is not initialized")
	}
	rows, err := s.db.Query(webhookListQuery+` WHERE w.owner_user_id = ? ORDER BY w.updated_at_ns DESC, w.id DESC`, s.listQueryCutoff(), ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list Webhooks: %w", err)
	}
	items, err := scanActivityWebhookRows(rows)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if err := s.loadWebhookRelations(s.db, ownerUserID, &items[index]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *WebhookStore) Get(ownerUserID, id string) (ActivityWebhook, error) {
	if s == nil || s.db == nil {
		return ActivityWebhook{}, errors.New("webhook store is not initialized")
	}
	item, err := scanActivityWebhook(s.listRow(ownerUserID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ActivityWebhook{}, ErrWebhookNotFound
	}
	if err != nil {
		return ActivityWebhook{}, fmt.Errorf("get Webhook: %w", err)
	}
	if err := s.loadWebhookRelations(s.db, ownerUserID, &item); err != nil {
		return ActivityWebhook{}, err
	}
	return item, nil
}

const webhookListQuery = `SELECT w.id, w.revision, w.name, w.enabled, w.target_kind, w.target_mode,
	w.method, w.url_template, w.headers_json, w.body_template, w.min_interval_seconds,
	w.last_status, w.consecutive_failures, w.last_called_at_ns, w.created_at_ns, w.updated_at_ns,
	(SELECT COUNT(*) FROM activity_webhook_deliveries d
	 WHERE d.owner_user_id = w.owner_user_id AND d.webhook_id = w.id
	 AND d.origin <> 'test' AND d.created_at_ns >= ?) AS calls_24h
	FROM activity_webhooks w`

func (s *WebhookStore) listQueryCutoff() int64 {
	return s.now().UTC().Add(-24 * time.Hour).UnixNano()
}

// The query contains a moving cutoff before its WHERE arguments. Keep the
// public helpers explicit so ownership arguments cannot accidentally occupy it.
func (s *WebhookStore) listRow(ownerUserID, id string) *sql.Row {
	return s.db.QueryRow(webhookListQuery+` WHERE w.owner_user_id = ? AND w.id = ?`, s.listQueryCutoff(), ownerUserID, id)
}

func (s *WebhookStore) Create(ownerUserID string, raw WebhookConfigInput) (ActivityWebhook, error) {
	input := normalizeWebhookInput(raw)
	catalog := activityWebhookCatalog()
	if err := validateWebhookInput(input, catalog.Fixtures, catalog.Variables, s.currentSettings().AllowPrivateTargets); err != nil {
		return ActivityWebhook{}, err
	}
	now := s.now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return ActivityWebhook{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM activity_webhooks WHERE owner_user_id = ?`, ownerUserID).Scan(&count); err != nil {
		return ActivityWebhook{}, err
	}
	if count >= webhookMaxPerUser {
		return ActivityWebhook{}, ErrWebhookLimitReached
	}
	if err := validateWebhookTargetsTx(tx, ownerUserID, input.TargetKind, input.TargetMode, input.TargetIDs); err != nil {
		return ActivityWebhook{}, err
	}
	headersJSON, err := json.Marshal(input.Headers)
	if err != nil {
		return ActivityWebhook{}, err
	}
	_, err = tx.Exec(`INSERT INTO activity_webhooks (
		owner_user_id, id, revision, name, enabled, target_kind, target_mode, method,
		url_template, headers_json, body_template, min_interval_seconds, created_at_ns, updated_at_ns
	) VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, ownerUserID, input.ID, input.Name,
		boolToInt(input.Enabled), input.TargetKind, input.TargetMode, input.Method, input.URL,
		string(headersJSON), input.Body, input.MinIntervalSeconds, now.UnixNano(), now.UnixNano())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ActivityWebhook{}, ErrWebhookRevisionConflict
		}
		return ActivityWebhook{}, fmt.Errorf("insert Webhook: %w", err)
	}
	if err := replaceWebhookRelationsTx(tx, ownerUserID, input.ID, input.Events, input.TargetIDs); err != nil {
		return ActivityWebhook{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return ActivityWebhook{}, err
	}
	return s.Get(ownerUserID, input.ID)
}

func (s *WebhookStore) Update(ownerUserID, id string, raw WebhookConfigInput) (ActivityWebhook, error) {
	input := normalizeWebhookInput(raw)
	input.ID = id
	if input.ExpectedRevision < 1 {
		return ActivityWebhook{}, ErrWebhookRevisionConflict
	}
	catalog := activityWebhookCatalog()
	if err := validateWebhookInput(input, catalog.Fixtures, catalog.Variables, s.currentSettings().AllowPrivateTargets); err != nil {
		return ActivityWebhook{}, err
	}
	now := s.now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return ActivityWebhook{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	var wasEnabled int
	var revision int64
	if err := tx.QueryRow(`SELECT enabled, revision FROM activity_webhooks WHERE owner_user_id = ? AND id = ?`, ownerUserID, id).Scan(&wasEnabled, &revision); errors.Is(err, sql.ErrNoRows) {
		return ActivityWebhook{}, ErrWebhookNotFound
	} else if err != nil {
		return ActivityWebhook{}, err
	}
	if revision != input.ExpectedRevision {
		return ActivityWebhook{}, ErrWebhookRevisionConflict
	}
	if err := validateWebhookTargetsTx(tx, ownerUserID, input.TargetKind, input.TargetMode, input.TargetIDs); err != nil {
		return ActivityWebhook{}, err
	}
	headersJSON, err := json.Marshal(input.Headers)
	if err != nil {
		return ActivityWebhook{}, err
	}
	result, err := tx.Exec(`UPDATE activity_webhooks SET revision = revision + 1, name = ?, enabled = ?,
		target_kind = ?, target_mode = ?, method = ?, url_template = ?, headers_json = ?, body_template = ?, min_interval_seconds = ?, updated_at_ns = ?
		WHERE owner_user_id = ? AND id = ? AND revision = ?`, input.Name, boolToInt(input.Enabled), input.TargetKind,
		input.TargetMode, input.Method, input.URL, string(headersJSON), input.Body, input.MinIntervalSeconds, now.UnixNano(), ownerUserID, id, input.ExpectedRevision)
	if err != nil {
		return ActivityWebhook{}, fmt.Errorf("update Webhook: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ActivityWebhook{}, ErrWebhookRevisionConflict
	}
	if err := replaceWebhookRelationsTx(tx, ownerUserID, id, input.Events, input.TargetIDs); err != nil {
		return ActivityWebhook{}, err
	}
	if wasEnabled != 0 && !input.Enabled {
		if _, err := cancelWaitingWebhookDeliveriesTx(tx, ownerUserID, id, now); err != nil {
			return ActivityWebhook{}, err
		}
		if err := pruneWebhookHistoryTx(tx, ownerUserID, id, now); err != nil {
			return ActivityWebhook{}, err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return ActivityWebhook{}, err
	}
	return s.Get(ownerUserID, id)
}

// SetEnabled toggles a stored Webhook without requiring a full configuration
// round-trip. Enabling re-runs the complete validation (including the private
// target policy) against the stored configuration, so a policy-violating URL
// cannot be turned on; disabling always succeeds and cancels waiting
// deliveries like a full update would.
func (s *WebhookStore) SetEnabled(ownerUserID, id string, enabled bool) (ActivityWebhook, error) {
	current, err := s.Get(ownerUserID, id)
	if err != nil {
		return ActivityWebhook{}, err
	}
	if current.Enabled == enabled {
		return current, nil
	}
	if enabled {
		catalog := activityWebhookCatalog()
		if err := validateWebhookInput(current.toConfigInput(true), catalog.Fixtures, catalog.Variables, s.currentSettings().AllowPrivateTargets); err != nil {
			return ActivityWebhook{}, err
		}
	}
	now := s.now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return ActivityWebhook{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	result, err := tx.Exec(`UPDATE activity_webhooks SET revision = revision + 1, enabled = ?, updated_at_ns = ?
		WHERE owner_user_id = ? AND id = ? AND revision = ?`, boolToInt(enabled), now.UnixNano(), ownerUserID, id, current.Revision)
	if err != nil {
		return ActivityWebhook{}, fmt.Errorf("toggle Webhook enabled: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ActivityWebhook{}, ErrWebhookRevisionConflict
	}
	if current.Enabled && !enabled {
		if _, err := cancelWaitingWebhookDeliveriesTx(tx, ownerUserID, id, now); err != nil {
			return ActivityWebhook{}, err
		}
		if err := pruneWebhookHistoryTx(tx, ownerUserID, id, now); err != nil {
			return ActivityWebhook{}, err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return ActivityWebhook{}, err
	}
	return s.Get(ownerUserID, id)
}

// DisableWebhooksViolatingPrivatePolicy turns off every enabled Webhook whose
// stored URL violates the private-target policy. It runs after an
// administrator disables private targets, so previously allowed intranet
// Webhooks stop delivering immediately instead of failing per attempt. Only
// url_target_not_allowed violations disable a Webhook; unrelated validation
// errors leave it untouched. Returns the number of disabled Webhooks.
func (s *WebhookStore) DisableWebhooksViolatingPrivatePolicy() (int, error) {
	rows, err := s.db.Query(`SELECT owner_user_id, id FROM activity_webhooks WHERE enabled = 1`)
	if err != nil {
		return 0, err
	}
	type webhookRef struct{ ownerUserID, id string }
	refs := []webhookRef{}
	for rows.Next() {
		var ref webhookRef
		if err := rows.Scan(&ref.ownerUserID, &ref.id); err != nil {
			return 0, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	_ = rows.Close()

	catalog := activityWebhookCatalog()
	violating := []webhookRef{}
	for _, ref := range refs {
		current, err := s.Get(ref.ownerUserID, ref.id)
		if err != nil {
			return 0, err
		}
		err = validateWebhookInput(current.toConfigInput(true), catalog.Fixtures, catalog.Variables, false)
		var validation *webhookValidationError
		if errors.As(err, &validation) && validation.Code == "url_target_not_allowed" {
			violating = append(violating, ref)
		}
	}
	if len(violating) == 0 {
		return 0, nil
	}
	now := s.now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, ref := range violating {
		result, err := tx.Exec(`UPDATE activity_webhooks SET revision = revision + 1, enabled = 0, updated_at_ns = ?
			WHERE owner_user_id = ? AND id = ? AND enabled = 1`, now.UnixNano(), ref.ownerUserID, ref.id)
		if err != nil {
			return 0, fmt.Errorf("disable policy-violating Webhook: %w", err)
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			continue
		}
		if _, err := cancelWaitingWebhookDeliveriesTx(tx, ref.ownerUserID, ref.id, now); err != nil {
			return 0, err
		}
		if err := pruneWebhookHistoryTx(tx, ref.ownerUserID, ref.id, now); err != nil {
			return 0, err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return len(violating), nil
}

func (s *WebhookStore) Delete(ownerUserID, id string) error {
	now := s.now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if _, err := cancelWaitingWebhookDeliveriesTx(tx, ownerUserID, id, now); err != nil {
		return err
	}
	if err := pruneWebhookHistoryTx(tx, ownerUserID, id, now); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM activity_webhooks WHERE owner_user_id = ? AND id = ?`, ownerUserID, id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrWebhookNotFound
	}
	return commitTx(tx, &committed)
}

func replaceWebhookRelationsTx(tx *sql.Tx, ownerUserID, webhookID string, events, targetIDs []string) error {
	if _, err := tx.Exec(`DELETE FROM activity_webhook_events WHERE owner_user_id = ? AND webhook_id = ?`, ownerUserID, webhookID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM activity_webhook_targets WHERE owner_user_id = ? AND webhook_id = ?`, ownerUserID, webhookID); err != nil {
		return err
	}
	for _, event := range events {
		if _, err := tx.Exec(`INSERT INTO activity_webhook_events (owner_user_id, webhook_id, event_type) VALUES (?, ?, ?)`, ownerUserID, webhookID, event); err != nil {
			return err
		}
	}
	for _, targetID := range targetIDs {
		if _, err := tx.Exec(`INSERT INTO activity_webhook_targets (owner_user_id, webhook_id, target_id) VALUES (?, ?, ?)`, ownerUserID, webhookID, targetID); err != nil {
			return err
		}
	}
	return nil
}

func validateWebhookTargetsTx(tx *sql.Tx, ownerUserID string, kind WebhookTargetKind, mode WebhookTargetMode, targetIDs []string) error {
	if mode == WebhookTargetAll {
		return nil
	}
	table := "registered_clients"
	if kind == WebhookTargetTunnel {
		table = "tunnels"
	}
	for _, targetID := range targetIDs {
		var exists int
		err := tx.QueryRow(`SELECT 1 FROM `+table+` WHERE owner_user_id = ? AND id = ?`, ownerUserID, targetID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return invalidWebhook("targets", "target_not_found", fmt.Sprintf("Webhook target %q was not found", targetID))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func cancelWaitingWebhookDeliveriesTx(tx *sql.Tx, ownerUserID, webhookID string, now time.Time) (int64, error) {
	result, err := tx.Exec(`UPDATE activity_webhook_deliveries SET status = 'canceled', error = 'Webhook was disabled or deleted',
		completed_at_ns = ?, updated_at_ns = ?, lease_until_ns = NULL
		WHERE owner_user_id = ? AND webhook_id = ? AND status IN ('queued', 'retrying')
		AND lease_until_ns IS NULL`, now.UnixNano(), now.UnixNano(), ownerUserID, webhookID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *WebhookStore) loadWebhookRelations(queryer dbQuerier, ownerUserID string, item *ActivityWebhook) error {
	events, err := queryStringColumn(queryer, `SELECT event_type FROM activity_webhook_events WHERE owner_user_id = ? AND webhook_id = ? ORDER BY event_type`, ownerUserID, item.ID)
	if err != nil {
		return err
	}
	targets, err := queryStringColumn(queryer, `SELECT target_id FROM activity_webhook_targets WHERE owner_user_id = ? AND webhook_id = ? ORDER BY target_id`, ownerUserID, item.ID)
	if err != nil {
		return err
	}
	item.Events, item.TargetIDs = events, targets
	return nil
}

func queryStringColumn(queryer dbQuerier, query string, args ...any) ([]string, error) {
	rows, err := queryer.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func scanActivityWebhook(scanner dbScanner) (ActivityWebhook, error) {
	var item ActivityWebhook
	var enabled int
	var headersJSON string
	var lastCalledAt sql.NullInt64
	var createdAtNS, updatedAtNS int64
	err := scanner.Scan(&item.ID, &item.Revision, &item.Name, &enabled, &item.TargetKind, &item.TargetMode,
		&item.Method, &item.URL, &headersJSON, &item.Body, &item.MinIntervalSeconds, &item.LastStatus, &item.ConsecutiveFailures,
		&lastCalledAt, &createdAtNS, &updatedAtNS, &item.Calls24h)
	if err != nil {
		return ActivityWebhook{}, err
	}
	item.Enabled = intToBool(enabled)
	if err := json.Unmarshal([]byte(headersJSON), &item.Headers); err != nil {
		return ActivityWebhook{}, fmt.Errorf("decode Webhook headers: %w", err)
	}
	if item.Headers == nil {
		item.Headers = []WebhookHeader{}
	}
	item.CreatedAt, item.UpdatedAt = formatUnixNano(createdAtNS), formatUnixNano(updatedAtNS)
	if lastCalledAt.Valid {
		value := formatUnixNano(lastCalledAt.Int64)
		item.LastCalledAt = &value
	}
	return item, nil
}

func scanActivityWebhookRows(rows *sql.Rows) ([]ActivityWebhook, error) {
	defer func() { _ = rows.Close() }()
	items := []ActivityWebhook{}
	for rows.Next() {
		item, err := scanActivityWebhook(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func formatUnixNano(value int64) string {
	return time.Unix(0, value).UTC().Format(time.RFC3339Nano)
}

func nullableFormattedUnixNano(value sql.NullInt64) *string {
	if !value.Valid {
		return nil
	}
	formatted := formatUnixNano(value.Int64)
	return &formatted
}

func (s *WebhookStore) Preview(raw WebhookConfigInput, event string) (WebhookPreview, error) {
	input := normalizeWebhookInput(raw)
	catalog := activityWebhookCatalog()
	if err := validateWebhookInput(input, catalog.Fixtures, catalog.Variables, s.currentSettings().AllowPrivateTargets); err != nil {
		return WebhookPreview{}, err
	}
	if !slices.Contains(input.Events, event) {
		return WebhookPreview{}, invalidWebhook("event", "event_not_selected", "sample event is not selected")
	}
	values := cloneWebhookValues(catalog.Fixtures[event])
	values["webhook.id"], values["webhook.name"] = input.ID, input.Name
	rendered, err := renderWebhookRequest(input.snapshot(max(input.ExpectedRevision, 1)), values, 1)
	if err != nil {
		return WebhookPreview{}, err
	}
	return WebhookPreview{Event: event, Values: values, Method: rendered.Method, URL: rendered.URL, Headers: rendered.Headers, Body: rendered.Body}, nil
}

func (s *WebhookStore) EnqueueTest(ownerUserID string, raw WebhookConfigInput, event string) (WebhookDelivery, error) {
	input := normalizeWebhookInput(raw)
	catalog := activityWebhookCatalog()
	if err := validateWebhookInput(input, catalog.Fixtures, catalog.Variables, s.currentSettings().AllowPrivateTargets); err != nil {
		return WebhookDelivery{}, err
	}
	if !slices.Contains(input.Events, event) {
		return WebhookDelivery{}, invalidWebhook("event", "event_not_selected", "test event is not selected")
	}
	createdAt := s.now().UTC()
	settings := s.currentSettings()
	tx, err := s.db.Begin()
	if err != nil {
		return WebhookDelivery{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err := validateWebhookTargetsTx(tx, ownerUserID, input.TargetKind, input.TargetMode, input.TargetIDs); err != nil {
		return WebhookDelivery{}, err
	}
	if err := ensureDailyCapTx(tx, ownerUserID, createdAt, settings.DailyDeliveryCap); err != nil {
		return WebhookDelivery{}, err
	}
	snapshot := sampleWebhookEvent(event)
	if input.TargetMode == WebhookTargetSelected {
		snapshot.MatchedTargetIDs = slices.Clone(input.TargetIDs)
	}
	deliveryID := "dlv_" + generateUUID()
	values := snapshot.values(deliveryID, input.ID, input.Name)
	if err := insertWebhookDeliveryTx(tx, ownerUserID, input.snapshot(max(input.ExpectedRevision, 0)), snapshot, values, WebhookOriginTest, sql.NullInt64{}, 1, createdAt); err != nil {
		return WebhookDelivery{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return WebhookDelivery{}, err
	}
	return s.GetDelivery(ownerUserID, deliveryID)
}

func (s *WebhookStore) Replay(ownerUserID, deliveryID string) (WebhookDelivery, error) {
	original, err := s.getStoredDelivery(ownerUserID, deliveryID)
	if err != nil {
		return WebhookDelivery{}, err
	}
	if original.Origin == WebhookOriginTest {
		return WebhookDelivery{}, ErrWebhookReplayUnavailable
	}
	current, err := s.Get(ownerUserID, original.WebhookID)
	if err != nil {
		return WebhookDelivery{}, ErrWebhookReplayUnavailable
	}
	if !slices.Contains(current.Events, original.Event) {
		return WebhookDelivery{}, ErrWebhookReplayUnavailable
	}
	snapshot := original.EventSnapshot
	snapshot.MatchedTargetIDs = matchedWebhookTargetIDs(current.TargetKind, current.TargetMode, current.TargetIDs, snapshot)
	if len(snapshot.MatchedTargetIDs) == 0 {
		return WebhookDelivery{}, ErrWebhookReplayUnavailable
	}
	newID := "dlv_" + generateUUID()
	values := snapshot.values(newID, current.ID, current.Name)
	now := s.now().UTC()
	settings := s.currentSettings()
	tx, err := s.db.Begin()
	if err != nil {
		return WebhookDelivery{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err := ensureDailyCapTx(tx, ownerUserID, now, settings.DailyDeliveryCap); err != nil {
		return WebhookDelivery{}, err
	}
	if err := insertWebhookDeliveryTx(tx, ownerUserID, current.snapshot(), snapshot, values, WebhookOriginReplay, original.SourceEventID, 3, now); err != nil {
		return WebhookDelivery{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return WebhookDelivery{}, err
	}
	return s.GetDelivery(ownerUserID, newID)
}

func insertWebhookDeliveryTx(tx *sql.Tx, ownerUserID string, config webhookConfigSnapshot, event webhookEventSnapshot, values map[string]any, origin WebhookDeliveryOrigin, sourceEventID sql.NullInt64, maxAttempts int, now time.Time) error {
	if err := ensurePendingCapTx(tx, ownerUserID); err != nil {
		return err
	}
	deliveryID := webhookValueText(values["delivery.id"])
	rendered, err := renderWebhookRequest(config, values, 1)
	if err != nil {
		return err
	}
	for key, value := range rendered.Headers {
		if !validWebhookHeaderValue(value) {
			return invalidWebhook("headers", "invalid_header_value", "Webhook header "+key+" renders to an invalid value")
		}
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}
	valuesJSON, err := json.Marshal(values)
	if err != nil {
		return err
	}
	headersJSON, err := json.Marshal(rendered.Headers)
	if err != nil {
		return err
	}
	var eventID any
	if sourceEventID.Valid {
		eventID = sourceEventID.Int64
	}
	_, err = tx.Exec(`INSERT INTO activity_webhook_deliveries (
		id, owner_user_id, webhook_id, webhook_name, origin, source_event_id, event_type,
		event_occurred_at_ns, status, max_attempts, next_attempt_at_ns, config_revision,
		config_snapshot_json, event_snapshot_json, values_snapshot_json,
		request_method, request_url, request_headers_json, request_body,
		created_at_ns, updated_at_ns
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		deliveryID, ownerUserID, config.ID, config.Name, origin, eventID, event.Type,
		parseWebhookOccurredAt(event.OccurredAt, now).UnixNano(), maxAttempts, now.UnixNano(), config.Revision,
		string(configJSON), string(eventJSON), string(valuesJSON), rendered.Method, rendered.URL,
		string(headersJSON), rendered.Body, now.UnixNano(), now.UnixNano())
	if err != nil {
		return fmt.Errorf("insert Webhook delivery: %w", err)
	}
	return nil
}

func parseWebhookOccurredAt(raw string, fallback time.Time) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func matchedWebhookTargetIDs(kind WebhookTargetKind, mode WebhookTargetMode, selected []string, snapshot webhookEventSnapshot) []string {
	available := make([]string, 0)
	if kind == WebhookTargetClient {
		for _, subject := range snapshot.Clients {
			available = append(available, subject.ID)
		}
	} else {
		for _, subject := range snapshot.Tunnels {
			available = append(available, subject.ID)
		}
	}
	if mode == WebhookTargetAll {
		return uniqueSortedStrings(available)
	}
	wanted := make(map[string]struct{}, len(selected))
	for _, id := range selected {
		wanted[id] = struct{}{}
	}
	matched := make([]string, 0)
	for _, id := range available {
		if _, ok := wanted[id]; ok {
			matched = append(matched, id)
		}
	}
	return uniqueSortedStrings(matched)
}

func uniqueSortedStrings(values []string) []string {
	values = uniqueNonEmptyStrings(values)
	sort.Strings(values)
	return values
}

func (s *WebhookStore) ListDeliveries(ownerUserID, webhookID, cursor string, limit int, status WebhookDeliveryStatus) (WebhookDeliveryPage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	decoded, err := decodeWebhookDeliveryCursor(cursor)
	if err != nil {
		return WebhookDeliveryPage{}, invalidWebhook("cursor", "invalid_cursor", "delivery cursor is invalid")
	}
	query := webhookDeliverySelectQuery + ` WHERE owner_user_id = ? AND webhook_id = ?`
	args := []any{ownerUserID, webhookID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if decoded.ID != "" {
		query += ` AND (created_at_ns < ? OR (created_at_ns = ? AND id < ?))`
		args = append(args, decoded.CreatedAtNS, decoded.CreatedAtNS, decoded.ID)
	}
	query += ` ORDER BY created_at_ns DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return WebhookDeliveryPage{}, err
	}
	stored, err := scanWebhookDeliveryRows(rows)
	if err != nil {
		return WebhookDeliveryPage{}, err
	}
	page := WebhookDeliveryPage{Items: []WebhookDelivery{}}
	if len(stored) > limit {
		page.HasMore = true
		stored = stored[:limit]
	}
	for index := range stored {
		if err := s.loadDeliveryAttempts(&stored[index]); err != nil {
			return WebhookDeliveryPage{}, err
		}
		page.Items = append(page.Items, stored[index].WebhookDelivery)
	}
	if page.HasMore && len(stored) > 0 {
		page.NextCursor, err = encodeWebhookDeliveryCursor(webhookDeliveryCursor{CreatedAtNS: stored[len(stored)-1].CreatedAtNS, ID: stored[len(stored)-1].ID})
	}
	return page, err
}

func (s *WebhookStore) GetDelivery(ownerUserID, deliveryID string) (WebhookDelivery, error) {
	stored, err := s.getStoredDelivery(ownerUserID, deliveryID)
	if err != nil {
		return WebhookDelivery{}, err
	}
	if err := s.loadDeliveryAttempts(&stored); err != nil {
		return WebhookDelivery{}, err
	}
	return stored.WebhookDelivery, nil
}

func (s *WebhookStore) getStoredDelivery(ownerUserID, deliveryID string) (webhookStoredDelivery, error) {
	stored, err := scanWebhookDelivery(s.db.QueryRow(webhookDeliverySelectQuery+` WHERE owner_user_id = ? AND id = ?`, ownerUserID, deliveryID))
	if errors.Is(err, sql.ErrNoRows) {
		return webhookStoredDelivery{}, ErrWebhookDeliveryNotFound
	}
	if err != nil {
		return webhookStoredDelivery{}, err
	}
	return stored, nil
}

const webhookDeliverySelectQuery = `SELECT id, owner_user_id, webhook_id, webhook_name, origin, source_event_id,
	event_type, event_occurred_at_ns, status, attempt_count, max_attempts, next_attempt_at_ns, lease_until_ns,
	config_revision, config_snapshot_json, event_snapshot_json, values_snapshot_json,
	request_method, request_url, request_headers_json, request_body,
	response_status, response_headers_json, response_body, error, duration_ms,
	created_at_ns, started_at_ns, completed_at_ns, updated_at_ns
	FROM activity_webhook_deliveries`

func scanWebhookDelivery(scanner dbScanner) (webhookStoredDelivery, error) {
	var stored webhookStoredDelivery
	var eventOccurredAtNS int64
	var configJSON, eventJSON, valuesJSON, requestHeadersJSON, responseHeadersJSON string
	var requestBody sql.NullString
	var responseStatus sql.NullInt64
	var durationMS sql.NullInt64
	err := scanner.Scan(&stored.ID, &stored.OwnerUserID, &stored.WebhookID, &stored.WebhookName, &stored.Origin,
		&stored.SourceEventID, &stored.Event, &eventOccurredAtNS, &stored.Status, &stored.AttemptCount,
		&stored.MaxAttempts, &stored.NextAttemptAtNS, &stored.LeaseUntilNS, &stored.ConfigRevision,
		&configJSON, &eventJSON, &valuesJSON, &stored.RequestMethod, &stored.RequestURL,
		&requestHeadersJSON, &requestBody, &responseStatus, &responseHeadersJSON, &stored.ResponseBody,
		&stored.Error, &durationMS, &stored.CreatedAtNS, &stored.StartedAtNS, &stored.CompletedAtNS, &stored.UpdatedAtNS)
	if err != nil {
		return webhookStoredDelivery{}, err
	}
	if err := json.Unmarshal([]byte(configJSON), &stored.ConfigSnapshot); err != nil {
		return webhookStoredDelivery{}, err
	}
	if err := json.Unmarshal([]byte(eventJSON), &stored.EventSnapshot); err != nil {
		return webhookStoredDelivery{}, err
	}
	if err := json.Unmarshal([]byte(valuesJSON), &stored.ValuesSnapshot); err != nil {
		return webhookStoredDelivery{}, err
	}
	if err := json.Unmarshal([]byte(requestHeadersJSON), &stored.RequestHeaders); err != nil {
		return webhookStoredDelivery{}, err
	}
	if err := json.Unmarshal([]byte(responseHeadersJSON), &stored.ResponseHeaders); err != nil {
		return webhookStoredDelivery{}, err
	}
	if stored.RequestHeaders == nil {
		stored.RequestHeaders = map[string]string{}
	}
	if stored.ResponseHeaders == nil {
		stored.ResponseHeaders = map[string]string{}
	}
	if requestBody.Valid {
		stored.RequestBody = &requestBody.String
	}
	if responseStatus.Valid {
		value := int(responseStatus.Int64)
		stored.StatusCode = &value
	}
	if durationMS.Valid {
		stored.DurationMS = &durationMS.Int64
	}
	stored.EventID = stored.EventSnapshot.ID
	stored.OccurredAt = formatUnixNano(eventOccurredAtNS)
	stored.CreatedAt = formatUnixNano(stored.CreatedAtNS)
	if stored.Status == WebhookDeliveryQueued || stored.Status == WebhookDeliveryRetrying {
		stored.NextAttemptAt = nullableFormattedUnixNano(sql.NullInt64{Int64: stored.NextAttemptAtNS, Valid: true})
	}
	stored.Attempts = []WebhookDeliveryAttempt{}
	return stored, nil
}

func scanWebhookDeliveryRows(rows *sql.Rows) ([]webhookStoredDelivery, error) {
	defer func() { _ = rows.Close() }()
	items := []webhookStoredDelivery{}
	for rows.Next() {
		item, err := scanWebhookDelivery(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *WebhookStore) loadDeliveryAttempts(stored *webhookStoredDelivery) error {
	rows, err := s.db.Query(`SELECT attempt_number, status, started_at_ns, response_status, duration_ms, error
		FROM activity_webhook_delivery_attempts WHERE delivery_id = ? ORDER BY attempt_number`, stored.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	stored.Attempts = []WebhookDeliveryAttempt{}
	for rows.Next() {
		var attempt WebhookDeliveryAttempt
		var startedAtNS int64
		var statusCode, durationMS sql.NullInt64
		if err := rows.Scan(&attempt.Number, &attempt.Status, &startedAtNS, &statusCode, &durationMS, &attempt.Error); err != nil {
			return err
		}
		attempt.OccurredAt = formatUnixNano(startedAtNS)
		if statusCode.Valid {
			value := int(statusCode.Int64)
			attempt.StatusCode = &value
		}
		if durationMS.Valid {
			attempt.DurationMS = &durationMS.Int64
		}
		stored.Attempts = append(stored.Attempts, attempt)
	}
	return rows.Err()
}

func encodeWebhookDeliveryCursor(cursor webhookDeliveryCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeWebhookDeliveryCursor(raw string) (webhookDeliveryCursor, error) {
	if raw == "" {
		return webhookDeliveryCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return webhookDeliveryCursor{}, err
	}
	var cursor webhookDeliveryCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return webhookDeliveryCursor{}, err
	}
	if cursor.CreatedAtNS <= 0 || cursor.ID == "" {
		return webhookDeliveryCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func (s *WebhookStore) Prune(now time.Time) (int64, error) {
	if _, err := s.db.Exec(`DELETE FROM activity_webhook_notification_state WHERE last_enqueued_at_ns <= ?`,
		now.UTC().Add(-time.Duration(webhookMaxNotificationIntervalSeconds)*time.Second).UnixNano()); err != nil {
		return 0, fmt.Errorf("prune Webhook notification state: %w", err)
	}
	result, err := s.db.Exec(`DELETE FROM activity_webhook_deliveries
		WHERE status IN ('success', 'failed', 'canceled') AND (
			completed_at_ns < ? OR id IN (
				SELECT id FROM (
					SELECT id, ROW_NUMBER() OVER (
						PARTITION BY owner_user_id, webhook_id
						ORDER BY completed_at_ns DESC, id DESC
					) AS history_rank
					FROM activity_webhook_deliveries
					WHERE status IN ('success', 'failed', 'canceled')
				) ranked WHERE history_rank > ?
			)
		)`, now.UTC().Add(-30*24*time.Hour).UnixNano(), webhookDeliveryHistoryLimit)
	if err != nil {
		return 0, fmt.Errorf("prune Webhook deliveries: %w", err)
	}
	return result.RowsAffected()
}

func pruneWebhookHistoryTx(tx *sql.Tx, ownerUserID, webhookID string, now time.Time) error {
	_, err := tx.Exec(`DELETE FROM activity_webhook_deliveries
		WHERE owner_user_id = ? AND webhook_id = ?
		AND status IN ('success', 'failed', 'canceled') AND (
			completed_at_ns < ? OR id NOT IN (
				SELECT id FROM activity_webhook_deliveries
				WHERE owner_user_id = ? AND webhook_id = ?
				AND status IN ('success', 'failed', 'canceled')
				ORDER BY completed_at_ns DESC, id DESC LIMIT ?
			)
		)`, ownerUserID, webhookID, now.UTC().Add(-30*24*time.Hour).UnixNano(), ownerUserID, webhookID, webhookDeliveryHistoryLimit)
	return err
}
