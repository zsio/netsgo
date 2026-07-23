package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	activityPayloadMaxBytes = 16 << 10
	activityIDMaxBytes      = 128
	activityNameMaxBytes    = 255
	activityCodeMaxBytes    = 64
	activityDedupeMaxBytes  = 512
)

type ActivitySeverity string

const (
	ActivitySeverityDebug   ActivitySeverity = "debug"
	ActivitySeverityInfo    ActivitySeverity = "info"
	ActivitySeverityWarning ActivitySeverity = "warning"
	ActivitySeverityError   ActivitySeverity = "error"
)

type ActivityCategory string

const (
	ActivityCategoryClient   ActivityCategory = "client"
	ActivityCategoryTunnel   ActivityCategory = "tunnel"
	ActivityCategoryP2P      ActivityCategory = "p2p"
	ActivityCategoryAdmin    ActivityCategory = "admin"
	ActivityCategorySecurity ActivityCategory = "security"
)

type ActivityScope string

const (
	ActivityScopeGlobal ActivityScope = "global"
	ActivityScopeClient ActivityScope = "client"
	ActivityScopeTunnel ActivityScope = "tunnel"
)

type ActivityDirection string

const (
	ActivityDirectionBefore ActivityDirection = "before"
	ActivityDirectionAfter  ActivityDirection = "after"
)

type ActivityActor struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	IPHash   string `json:"ip_hash,omitempty"`
	IPPrefix string `json:"ip_prefix,omitempty"`
}

type ActivityClientSubject struct {
	ClientID    string `json:"client_id"`
	Relation    string `json:"relation"`
	DisplayName string `json:"display_name,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

type ActivityTunnelSubject struct {
	TunnelID  string `json:"tunnel_id"`
	Relation  string `json:"relation"`
	Name      string `json:"name,omitempty"`
	Type      string `json:"tunnel_type,omitempty"`
	Topology  string `json:"topology,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type ActivitySummaryArgs struct {
	ClientName   string `json:"client_name,omitempty"`
	TunnelName   string `json:"tunnel_name,omitempty"`
	ResourceName string `json:"resource_name,omitempty"`
	Before       string `json:"before,omitempty"`
	After        string `json:"after,omitempty"`
	Value        int64  `json:"value,omitempty"`
	Count        int    `json:"count,omitempty"`
	Transport    string `json:"transport,omitempty"`
	Topology     string `json:"topology,omitempty"`
}

type activityPayload interface {
	activityPayloadDescriptor() (ActivityCategory, string, int)
	activityPayloadJSON() ([]byte, error)
}

type activityPayloadV1 struct {
	category ActivityCategory
	action   string

	SummaryKey  string              `json:"summary_key"`
	SummaryArgs ActivitySummaryArgs `json:"summary_args,omitempty"`
	ReasonCode  string              `json:"reason_code,omitempty"`
	Before      string              `json:"before,omitempty"`
	After       string              `json:"after,omitempty"`
	Revision    uint64              `json:"revision,omitempty"`
	Generation  uint64              `json:"generation,omitempty"`
	SessionID   string              `json:"session_id,omitempty"`
	Sequence    uint64              `json:"sequence,omitempty"`
	Managed     *bool               `json:"managed,omitempty"`
}

func (p activityPayloadV1) activityPayloadDescriptor() (ActivityCategory, string, int) {
	return p.category, p.action, 1
}

func (p activityPayloadV1) activityPayloadJSON() ([]byte, error) {
	return json.Marshal(p)
}

type activityCatalogEntry struct {
	severity   ActivitySeverity
	summaryKey string
}

var activityCatalog = map[ActivityCategory]map[string]activityCatalogEntry{
	ActivityCategoryClient: {
		"registered":           {ActivitySeverityInfo, "activity.client.registered"},
		"online":               {ActivitySeverityInfo, "activity.client.online"},
		"offline":              {ActivitySeverityInfo, "activity.client.offline"},
		"deleted":              {ActivitySeverityInfo, "activity.client.deleted"},
		"display_name_changed": {ActivitySeverityInfo, "activity.client.display_name_changed"},
		"bandwidth_changed":    {ActivitySeverityInfo, "activity.client.bandwidth_changed"},
	},
	ActivityCategoryTunnel: {
		"created":           {ActivitySeverityInfo, "activity.tunnel.created"},
		"updated":           {ActivitySeverityInfo, "activity.tunnel.updated"},
		"deleted":           {ActivitySeverityInfo, "activity.tunnel.deleted"},
		"stopped":           {ActivitySeverityInfo, "activity.tunnel.stopped"},
		"resumed":           {ActivitySeverityInfo, "activity.tunnel.resumed"},
		"migrated":          {ActivitySeverityInfo, "activity.tunnel.migrated"},
		"runtime_changed":   {ActivitySeverityDebug, "activity.tunnel.runtime_changed"},
		"runtime_error":     {ActivitySeverityError, "activity.tunnel.runtime_error"},
		"runtime_recovered": {ActivitySeverityInfo, "activity.tunnel.runtime_recovered"},
	},
	ActivityCategoryP2P: {
		"session_started": {ActivitySeverityDebug, "activity.p2p.session_started"},
		"tunnel_attached": {ActivitySeverityDebug, "activity.p2p.tunnel_attached"},
		"tunnel_detached": {ActivitySeverityInfo, "activity.p2p.tunnel_detached"},
		"checking":        {ActivitySeverityDebug, "activity.p2p.checking"},
		"connected":       {ActivitySeverityInfo, "activity.p2p.connected"},
		"failed":          {ActivitySeverityError, "activity.p2p.failed"},
		"fallback":        {ActivitySeverityWarning, "activity.p2p.fallback"},
		"session_closed":  {ActivitySeverityInfo, "activity.p2p.session_closed"},
	},
	ActivityCategoryAdmin: {
		"server_config_changed":                {ActivitySeverityInfo, "activity.admin.server_config_changed"},
		"activity_retention_changed":           {ActivitySeverityInfo, "activity.admin.activity_retention_changed"},
		"client_auth_rate_limit_changed":       {ActivitySeverityInfo, "activity.admin.client_auth_rate_limit_changed"},
		"client_auth_rate_limit_entry_cleared": {ActivitySeverityInfo, "activity.admin.client_auth_rate_limit_entry_cleared"},
		"api_key_created":                      {ActivitySeverityInfo, "activity.admin.api_key_created"},
		"api_key_enabled":                      {ActivitySeverityInfo, "activity.admin.api_key_enabled"},
		"api_key_disabled":                     {ActivitySeverityInfo, "activity.admin.api_key_disabled"},
		"api_key_deleted":                      {ActivitySeverityInfo, "activity.admin.api_key_deleted"},
		"admin_login_succeeded":                {ActivitySeverityInfo, "activity.admin.admin_login_succeeded"},
		"admin_logout":                         {ActivitySeverityInfo, "activity.admin.admin_logout"},
		"username_changed":                     {ActivitySeverityInfo, "activity.admin.username_changed"},
		"password_changed":                     {ActivitySeverityInfo, "activity.admin.password_changed"},
		"totp_enabled":                         {ActivitySeverityInfo, "activity.admin.totp_enabled"},
		"totp_disabled":                        {ActivitySeverityInfo, "activity.admin.totp_disabled"},
		"recovery_codes_regenerated":           {ActivitySeverityInfo, "activity.admin.recovery_codes_regenerated"},
		"passkey_added":                        {ActivitySeverityInfo, "activity.admin.passkey_added"},
		"passkey_renamed":                      {ActivitySeverityInfo, "activity.admin.passkey_renamed"},
		"passkey_deleted":                      {ActivitySeverityInfo, "activity.admin.passkey_deleted"},
	},
	ActivityCategorySecurity: {
		"admin_login_failed":           {ActivitySeverityWarning, "activity.security.admin_login_failed"},
		"admin_login_rate_limited":     {ActivitySeverityWarning, "activity.security.admin_login_rate_limited"},
		"mfa_failed":                   {ActivitySeverityWarning, "activity.security.mfa_failed"},
		"mfa_rate_limited":             {ActivitySeverityWarning, "activity.security.mfa_rate_limited"},
		"passkey_login_failed":         {ActivitySeverityWarning, "activity.security.passkey_login_failed"},
		"session_environment_mismatch": {ActivitySeverityWarning, "activity.security.session_environment_mismatch"},
		"client_auth_failed":           {ActivitySeverityWarning, "activity.security.client_auth_failed"},
		"client_auth_rate_limited":     {ActivitySeverityWarning, "activity.security.client_auth_rate_limited"},
	},
}

var activityReasonAllowlist = map[string]map[string]struct{}{
	"offline":                      makeStringSet("normal_closure", "server_shutdown", "transport_error", "timeout", "data_channel_closed", "replaced", "unknown"),
	"runtime_error":                makeStringSet("start_failed", "restore_failed", "reconcile_failed", "unknown"),
	"failed":                       makeStringSet("negotiation_failed", "direct_only_failed", "lease_unhealthy", "lease_expired", "unknown"),
	"fallback":                     makeStringSet("negotiation_failed", "lease_unhealthy", "lease_expired", "unknown"),
	"session_closed":               makeStringSet("participant_offline", "lease_unhealthy", "lease_expired", "tunnel_stopped", "tunnel_deleted", "revision_replaced", "unknown"),
	"admin_login_failed":           makeStringSet("bad_credentials"),
	"admin_login_rate_limited":     makeStringSet("rate_limited"),
	"mfa_failed":                   makeStringSet("invalid_token", "invalid_code", "challenge_consumed"),
	"mfa_rate_limited":             makeStringSet("rate_limited"),
	"passkey_login_failed":         makeStringSet("invalid_challenge", "origin_mismatch", "invalid_response", "assertion_failed"),
	"session_environment_mismatch": makeStringSet("environment_mismatch"),
	"client_auth_failed":           makeStringSet("invalid_token", "revoked_token", "expired_token", "install_mismatch", "invalid_key", "disabled_key", "expired_key", "max_uses_exceeded", "concurrent_session"),
	"client_auth_rate_limited":     makeStringSet("rate_limited"),
}

func makeStringSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func newActivityPayload(category ActivityCategory, action string, args ActivitySummaryArgs) activityPayloadV1 {
	entry := activityCatalog[category][action]
	return activityPayloadV1{
		category:    category,
		action:      action,
		SummaryKey:  entry.summaryKey,
		SummaryArgs: args,
	}
}

func newActivityTransitionPayload(category ActivityCategory, action string, args ActivitySummaryArgs, before, after string) activityPayloadV1 {
	payload := newActivityPayload(category, action, args)
	payload.Before = before
	payload.After = after
	return payload
}

func newActivitySecurityPayload(action, reason string) activityPayloadV1 {
	payload := newActivityPayload(ActivityCategorySecurity, action, ActivitySummaryArgs{})
	payload.ReasonCode = normalizeActivityReason(action, reason)
	return payload
}

func newActivityClientLifecyclePayload(action, reason string, generation uint64, managed bool, args ActivitySummaryArgs) activityPayloadV1 {
	payload := newActivityPayload(ActivityCategoryClient, action, args)
	payload.ReasonCode = normalizeActivityReason(action, reason)
	payload.Generation = generation
	payload.Managed = &managed
	return payload
}

func newActivityP2PPayload(action, reason, sessionID string, sequence uint64, args ActivitySummaryArgs) activityPayloadV1 {
	payload := newActivityPayload(ActivityCategoryP2P, action, args)
	payload.ReasonCode = normalizeActivityReason(action, reason)
	payload.SessionID, _ = truncateActivityString(sessionID, activityIDMaxBytes)
	payload.Sequence = sequence
	return payload
}

func normalizeActivityReason(action, reason string) string {
	allowed, constrained := activityReasonAllowlist[action]
	if !constrained {
		return ""
	}
	if _, ok := allowed[reason]; ok {
		return reason
	}
	return "unknown"
}

type ActivityEventSpec struct {
	OccurredAt time.Time
	Severity   ActivitySeverity
	Category   ActivityCategory
	Action     string
	Source     string
	Actor      ActivityActor
	DedupeKey  string
	Payload    activityPayload
	Clients    []ActivityClientSubject
	Tunnels    []ActivityTunnelSubject
}

type ActivityItem struct {
	ID             int64                   `json:"id"`
	OccurredAt     time.Time               `json:"occurred_at"`
	RecordedAt     time.Time               `json:"recorded_at"`
	Severity       ActivitySeverity        `json:"severity"`
	Category       ActivityCategory        `json:"category"`
	Action         string                  `json:"action"`
	Source         string                  `json:"source"`
	Actor          ActivityActor           `json:"actor"`
	PayloadVersion int                     `json:"payload_version"`
	Payload        json.RawMessage         `json:"payload"`
	Clients        []ActivityClientSubject `json:"clients"`
	Tunnels        []ActivityTunnelSubject `json:"tunnels"`
}

type ActivityQuery struct {
	Scope      ActivityScope
	ScopeID    string
	BeforeID   int64
	AfterID    int64
	Limit      int
	Severities []ActivitySeverity
	Categories []ActivityCategory
	From       *time.Time
	To         *time.Time
}

type ActivityPage struct {
	Items      []ActivityItem    `json:"items"`
	NextCursor int64             `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
	Direction  ActivityDirection `json:"direction"`
}

type ActivityRetentionRule struct {
	Days     int `json:"days"`
	MinCount int `json:"min_count"`
}

type ActivityRetentionPolicy struct {
	Debug   ActivityRetentionRule `json:"debug"`
	Info    ActivityRetentionRule `json:"info"`
	Warning ActivityRetentionRule `json:"warning"`
	Error   ActivityRetentionRule `json:"error"`
}

func DefaultActivityRetentionPolicy() ActivityRetentionPolicy {
	return ActivityRetentionPolicy{
		Debug:   ActivityRetentionRule{Days: 1, MinCount: 200},
		Info:    ActivityRetentionRule{Days: 7, MinCount: 100},
		Warning: ActivityRetentionRule{Days: 30, MinCount: 100},
		Error:   ActivityRetentionRule{Days: 180, MinCount: 100},
	}
}

func (p ActivityRetentionPolicy) validate() error {
	for severity, rule := range p.rules() {
		if rule.Days < 1 || rule.Days > 3650 {
			return fmt.Errorf("activity %s retention days must be between 1 and 3650", severity)
		}
		if rule.MinCount < 0 || rule.MinCount > 100000 {
			return fmt.Errorf("activity %s minimum count must be between 0 and 100000", severity)
		}
	}
	return nil
}

func (p ActivityRetentionPolicy) rules() map[ActivitySeverity]ActivityRetentionRule {
	return map[ActivitySeverity]ActivityRetentionRule{
		ActivitySeverityDebug:   p.Debug,
		ActivitySeverityInfo:    p.Info,
		ActivitySeverityWarning: p.Warning,
		ActivitySeverityError:   p.Error,
	}
}

type ActivityStore struct {
	path    string
	db      *sql.DB
	closeDB bool
	now     func() time.Time

	failMu          sync.Mutex
	failAppendErr   error
	failAppendCount int
}

func NewActivityStore(path string) (*ActivityStore, error) {
	db, err := openServerDB(path)
	if err != nil {
		return nil, err
	}
	return newActivityStoreWithDB(path, db, true), nil
}

func newActivityStoreWithDB(path string, db *sql.DB, closeDB bool) *ActivityStore {
	return &ActivityStore{path: path, db: db, closeDB: closeDB, now: time.Now}
}

func (s *ActivityStore) Close() error {
	if s == nil || s.db == nil || !s.closeDB {
		return nil
	}
	return s.db.Close()
}

func (s *ActivityStore) failNextAppendsForTest(err error, count int) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	s.failAppendErr = err
	s.failAppendCount = count
}

func (s *ActivityStore) maybeFailAppend() error {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	if s.failAppendErr == nil || s.failAppendCount <= 0 {
		return nil
	}
	err := s.failAppendErr
	s.failAppendCount--
	if s.failAppendCount == 0 {
		s.failAppendErr = nil
	}
	return err
}

func (s *ActivityStore) Append(spec ActivityEventSpec) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("activity store is not initialized")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin activity append: %w", err)
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	id, err := s.appendTx(tx, spec)
	if err != nil {
		return 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, fmt.Errorf("commit activity append: %w", err)
	}
	return id, nil
}

func (s *ActivityStore) appendTx(tx *sql.Tx, spec ActivityEventSpec) (int64, error) {
	if tx == nil {
		return 0, errors.New("activity append transaction must not be nil")
	}
	if err := s.maybeFailAppend(); err != nil {
		return 0, err
	}
	prepared, err := s.prepareSpec(spec)
	if err != nil {
		return 0, err
	}

	result, err := tx.Exec(`INSERT INTO activity_events (
		occurred_at_ns, recorded_at_ns, severity, category, action, source,
		actor_type, actor_id, actor_name, actor_ip_hash, actor_ip_prefix,
		dedupe_key, payload_version, payload_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT DO NOTHING`,
		prepared.occurredAt.UnixNano(), prepared.recordedAt.UnixNano(),
		prepared.severity, prepared.category, prepared.action, prepared.source,
		prepared.actor.Type, prepared.actor.ID, prepared.actor.Name,
		prepared.actor.IPHash, prepared.actor.IPPrefix,
		nullIfEmpty(prepared.dedupeKey), prepared.payloadVersion, string(prepared.payloadJSON),
	)
	if err != nil {
		return 0, fmt.Errorf("insert activity event: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read activity insert result: %w", err)
	}
	if rowsAffected == 0 {
		if prepared.dedupeKey == "" {
			return 0, errors.New("activity event insert was ignored without a dedupe key")
		}
		var existingID int64
		if err := tx.QueryRow(`SELECT id FROM activity_events WHERE dedupe_key = ?`, prepared.dedupeKey).Scan(&existingID); err != nil {
			return 0, fmt.Errorf("load deduplicated activity event: %w", err)
		}
		return existingID, nil
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read activity event id: %w", err)
	}

	for _, subject := range prepared.clients {
		if _, err := tx.Exec(`INSERT INTO activity_event_clients
			(event_id, client_id, relation, display_name, hostname, is_truncated)
			VALUES (?, ?, ?, ?, ?, ?)`, id, subject.ClientID, subject.Relation,
			subject.DisplayName, subject.Hostname, boolToInt(subject.Truncated)); err != nil {
			return 0, fmt.Errorf("insert activity client subject: %w", err)
		}
	}
	for _, subject := range prepared.tunnels {
		if _, err := tx.Exec(`INSERT INTO activity_event_tunnels
			(event_id, tunnel_id, relation, name, tunnel_type, topology, is_truncated)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, id, subject.TunnelID, subject.Relation,
			subject.Name, subject.Type, subject.Topology, boolToInt(subject.Truncated)); err != nil {
			return 0, fmt.Errorf("insert activity tunnel subject: %w", err)
		}
	}
	return id, nil
}

type preparedActivitySpec struct {
	occurredAt     time.Time
	recordedAt     time.Time
	severity       ActivitySeverity
	category       ActivityCategory
	action         string
	source         string
	actor          ActivityActor
	dedupeKey      string
	payloadVersion int
	payloadJSON    []byte
	clients        []ActivityClientSubject
	tunnels        []ActivityTunnelSubject
}

func (s *ActivityStore) prepareSpec(spec ActivityEventSpec) (preparedActivitySpec, error) {
	categoryEntries, ok := activityCatalog[spec.Category]
	if !ok {
		return preparedActivitySpec{}, fmt.Errorf("unsupported activity category %q", spec.Category)
	}
	entry, ok := categoryEntries[spec.Action]
	if !ok {
		return preparedActivitySpec{}, fmt.Errorf("unsupported activity action %q for category %q", spec.Action, spec.Category)
	}
	severity := spec.Severity
	if severity == "" {
		severity = entry.severity
	}
	if !validActivitySeverity(severity) {
		return preparedActivitySpec{}, fmt.Errorf("unsupported activity severity %q", severity)
	}
	if !validActivityCode(spec.Source) {
		return preparedActivitySpec{}, fmt.Errorf("invalid activity source %q", spec.Source)
	}
	if spec.Payload == nil {
		return preparedActivitySpec{}, errors.New("activity payload must not be nil")
	}
	payloadCategory, payloadAction, payloadVersion := spec.Payload.activityPayloadDescriptor()
	if payloadCategory != spec.Category || payloadAction != spec.Action || payloadVersion != 1 {
		return preparedActivitySpec{}, errors.New("activity payload does not match category, action, and version")
	}
	payloadJSON, err := spec.Payload.activityPayloadJSON()
	if err != nil {
		return preparedActivitySpec{}, fmt.Errorf("marshal activity payload: %w", err)
	}
	if len(payloadJSON) > activityPayloadMaxBytes {
		return preparedActivitySpec{}, fmt.Errorf("activity payload exceeds %d bytes", activityPayloadMaxBytes)
	}
	if !json.Valid(payloadJSON) {
		return preparedActivitySpec{}, errors.New("activity payload is not valid JSON")
	}
	if len(spec.DedupeKey) > activityDedupeMaxBytes || !utf8.ValidString(spec.DedupeKey) {
		return preparedActivitySpec{}, errors.New("invalid activity dedupe key")
	}

	actor, err := normalizeActivityActor(spec.Actor)
	if err != nil {
		return preparedActivitySpec{}, err
	}
	clients, err := normalizeActivityClientSubjects(spec.Clients)
	if err != nil {
		return preparedActivitySpec{}, err
	}
	tunnels, err := normalizeActivityTunnelSubjects(spec.Tunnels)
	if err != nil {
		return preparedActivitySpec{}, err
	}

	recordedAt := s.now().UTC()
	occurredAt := spec.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = recordedAt
	}
	return preparedActivitySpec{
		occurredAt: occurredAt, recordedAt: recordedAt, severity: severity,
		category: spec.Category, action: spec.Action, source: spec.Source,
		actor: actor, dedupeKey: spec.DedupeKey, payloadVersion: payloadVersion,
		payloadJSON: payloadJSON, clients: clients, tunnels: tunnels,
	}, nil
}

func validActivitySeverity(severity ActivitySeverity) bool {
	switch severity {
	case ActivitySeverityDebug, ActivitySeverityInfo, ActivitySeverityWarning, ActivitySeverityError:
		return true
	default:
		return false
	}
}

func validActivityCode(value string) bool {
	if value == "" || len(value) > activityCodeMaxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func normalizeActivityActor(actor ActivityActor) (ActivityActor, error) {
	switch actor.Type {
	case "admin", "client", "system", "security", "unknown":
	default:
		return ActivityActor{}, fmt.Errorf("unsupported activity actor type %q", actor.Type)
	}
	actor.ID, _ = truncateActivityString(actor.ID, activityIDMaxBytes)
	actor.Name, _ = truncateActivityString(actor.Name, activityNameMaxBytes)
	if len(actor.IPHash) > sha256.Size*2 || !validOptionalHex(actor.IPHash) {
		return ActivityActor{}, errors.New("invalid activity actor IP hash")
	}
	if len(actor.IPPrefix) > activityCodeMaxBytes || !utf8.ValidString(actor.IPPrefix) {
		return ActivityActor{}, errors.New("invalid activity actor IP prefix")
	}
	return actor, nil
}

func validOptionalHex(value string) bool {
	if value == "" {
		return true
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func NewActivityActor(actorType, id, name, rawIP, jwtSecret string) ActivityActor {
	actor := ActivityActor{Type: actorType, ID: id, Name: name}
	ip := net.ParseIP(strings.TrimSpace(rawIP))
	if ip == nil || jwtSecret == "" {
		return actor
	}
	canonical := ip.String()
	mac := hmac.New(sha256.New, []byte(jwtSecret))
	_, _ = mac.Write([]byte("netsgo/activity-actor-ip/v1\x00"))
	_, _ = mac.Write([]byte(canonical))
	actor.IPHash = hex.EncodeToString(mac.Sum(nil))
	actor.IPPrefix = activityIPPrefix(ip)
	return actor
}

func activityIPPrefix(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return (&net.IPNet{IP: v4.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}).String()
	}
	v6 := ip.To16()
	if v6 == nil {
		return ""
	}
	return (&net.IPNet{IP: v6.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
}

func normalizeActivityClientSubjects(subjects []ActivityClientSubject) ([]ActivityClientSubject, error) {
	result := make([]ActivityClientSubject, 0, len(subjects))
	seen := make(map[string]struct{}, len(subjects))
	for _, subject := range subjects {
		subject.ClientID, subject.Truncated = truncateActivityString(subject.ClientID, activityIDMaxBytes)
		if subject.ClientID == "" {
			return nil, errors.New("activity client subject ID must not be empty")
		}
		switch subject.Relation {
		case "owner", "ingress", "target", "peer", "subject", "related":
		default:
			return nil, fmt.Errorf("unsupported activity client relation %q", subject.Relation)
		}
		var truncated bool
		subject.DisplayName, truncated = truncateActivityString(subject.DisplayName, activityNameMaxBytes)
		subject.Truncated = subject.Truncated || truncated
		subject.Hostname, truncated = truncateActivityString(subject.Hostname, activityNameMaxBytes)
		subject.Truncated = subject.Truncated || truncated
		key := subject.ClientID + "\x00" + subject.Relation
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, subject)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ClientID == result[j].ClientID {
			return result[i].Relation < result[j].Relation
		}
		return result[i].ClientID < result[j].ClientID
	})
	return result, nil
}

func normalizeActivityTunnelSubjects(subjects []ActivityTunnelSubject) ([]ActivityTunnelSubject, error) {
	result := make([]ActivityTunnelSubject, 0, len(subjects))
	seen := make(map[string]struct{}, len(subjects))
	for _, subject := range subjects {
		subject.TunnelID, subject.Truncated = truncateActivityString(subject.TunnelID, activityIDMaxBytes)
		if subject.TunnelID == "" {
			return nil, errors.New("activity tunnel subject ID must not be empty")
		}
		switch subject.Relation {
		case "subject", "related", "shared_session":
		default:
			return nil, fmt.Errorf("unsupported activity tunnel relation %q", subject.Relation)
		}
		var truncated bool
		subject.Name, truncated = truncateActivityString(subject.Name, activityNameMaxBytes)
		subject.Truncated = subject.Truncated || truncated
		subject.Type, truncated = truncateActivityString(subject.Type, activityNameMaxBytes)
		subject.Truncated = subject.Truncated || truncated
		subject.Topology, truncated = truncateActivityString(subject.Topology, activityNameMaxBytes)
		subject.Truncated = subject.Truncated || truncated
		key := subject.TunnelID + "\x00" + subject.Relation
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, subject)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TunnelID == result[j].TunnelID {
			return result[i].Relation < result[j].Relation
		}
		return result[i].TunnelID < result[j].TunnelID
	})
	return result, nil
}

func truncateActivityString(value string, maxBytes int) (string, bool) {
	valid := strings.ToValidUTF8(value, "�")
	truncated := valid != value
	if len(valid) <= maxBytes {
		return valid, truncated
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(valid[cut]) {
		cut--
	}
	return valid[:cut], true
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *ActivityStore) GetByID(id int64) (ActivityItem, error) {
	if id <= 0 {
		return ActivityItem{}, sql.ErrNoRows
	}
	items, err := s.queryRows(`e.id = ?`, []any{id}, "e.id DESC", 1)
	if err != nil {
		return ActivityItem{}, err
	}
	if len(items) == 0 {
		return ActivityItem{}, sql.ErrNoRows
	}
	return items[0], nil
}

func (s *ActivityStore) MaxID() (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("activity store is not initialized")
	}
	var id int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM activity_events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("read activity maximum id: %w", err)
	}
	return id, nil
}

func (s *ActivityStore) Query(query ActivityQuery) (ActivityPage, error) {
	if s == nil || s.db == nil {
		return ActivityPage{}, errors.New("activity store is not initialized")
	}
	if query.Scope == "" {
		query.Scope = ActivityScopeGlobal
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 200 {
		return ActivityPage{}, errors.New("activity limit must be between 1 and 200")
	}
	if query.BeforeID > 0 && query.AfterID > 0 {
		return ActivityPage{}, errors.New("activity before and after cursors are mutually exclusive")
	}
	if query.BeforeID < 0 || query.AfterID < 0 {
		return ActivityPage{}, errors.New("activity cursor must be positive")
	}
	if query.From != nil && query.To != nil && !query.From.Before(*query.To) {
		return ActivityPage{}, errors.New("activity from must be before to")
	}

	where := make([]string, 0, 8)
	args := make([]any, 0, 16)
	switch query.Scope {
	case ActivityScopeGlobal:
		if query.ScopeID != "" {
			return ActivityPage{}, errors.New("global activity scope must not include an ID")
		}
	case ActivityScopeClient:
		if query.ScopeID == "" || len(query.ScopeID) > activityIDMaxBytes {
			return ActivityPage{}, errors.New("client activity scope requires a valid client ID")
		}
		where = append(where, `EXISTS (SELECT 1 FROM activity_event_clients ac WHERE ac.event_id = e.id AND ac.client_id = ?)`)
		args = append(args, query.ScopeID)
	case ActivityScopeTunnel:
		if query.ScopeID == "" || len(query.ScopeID) > activityIDMaxBytes {
			return ActivityPage{}, errors.New("tunnel activity scope requires a valid tunnel ID")
		}
		where = append(where, `EXISTS (SELECT 1 FROM activity_event_tunnels at WHERE at.event_id = e.id AND at.tunnel_id = ?)`)
		args = append(args, query.ScopeID)
	default:
		return ActivityPage{}, fmt.Errorf("unsupported activity scope %q", query.Scope)
	}

	if query.BeforeID > 0 {
		where = append(where, `e.id < ?`)
		args = append(args, query.BeforeID)
	}
	if query.AfterID > 0 {
		where = append(where, `e.id > ?`)
		args = append(args, query.AfterID)
	}
	var err error
	where, args, err = appendActivitySeverityFilter(where, args, query.Severities)
	if err != nil {
		return ActivityPage{}, err
	}
	where, args, err = appendActivityCategoryFilter(where, args, query.Categories)
	if err != nil {
		return ActivityPage{}, err
	}
	if query.From != nil {
		where = append(where, `e.occurred_at_ns >= ?`)
		args = append(args, query.From.UTC().UnixNano())
	}
	if query.To != nil {
		where = append(where, `e.occurred_at_ns < ?`)
		args = append(args, query.To.UTC().UnixNano())
	}

	direction := ActivityDirectionBefore
	order := "e.id DESC"
	if query.AfterID > 0 {
		direction = ActivityDirectionAfter
		order = "e.id ASC"
	}
	predicate := "1 = 1"
	if len(where) > 0 {
		predicate = strings.Join(where, " AND ")
	}
	items, err := s.queryRows(predicate, args, order, query.Limit+1)
	if err != nil {
		return ActivityPage{}, err
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	if direction == ActivityDirectionAfter {
		slices.Reverse(items)
	}
	page := ActivityPage{Items: items, HasMore: hasMore, Direction: direction}
	if len(items) > 0 {
		if direction == ActivityDirectionBefore {
			page.NextCursor = items[len(items)-1].ID
		} else {
			page.NextCursor = items[0].ID
		}
	}
	return page, nil
}

func appendActivitySeverityFilter(where []string, args []any, values []ActivitySeverity) ([]string, []any, error) {
	values = slices.Compact(slices.Sorted(slices.Values(values)))
	if len(values) == 0 {
		return where, args, nil
	}
	placeholders := make([]string, 0, len(values))
	for _, value := range values {
		if !validActivitySeverity(value) {
			return nil, nil, fmt.Errorf("unsupported activity severity %q", value)
		}
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	where = append(where, `e.severity IN (`+strings.Join(placeholders, ",")+`)`)
	return where, args, nil
}

func appendActivityCategoryFilter(where []string, args []any, values []ActivityCategory) ([]string, []any, error) {
	values = slices.Compact(slices.Sorted(slices.Values(values)))
	if len(values) == 0 {
		return where, args, nil
	}
	placeholders := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := activityCatalog[value]; !ok {
			return nil, nil, fmt.Errorf("unsupported activity category %q", value)
		}
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	where = append(where, `e.category IN (`+strings.Join(placeholders, ",")+`)`)
	return where, args, nil
}

func (s *ActivityStore) queryRows(predicate string, args []any, order string, limit int) ([]ActivityItem, error) {
	query := `SELECT e.id, e.occurred_at_ns, e.recorded_at_ns, e.severity, e.category,
		e.action, e.source, e.actor_type, e.actor_id, e.actor_name,
		e.actor_ip_hash, e.actor_ip_prefix, e.payload_version, e.payload_json
		FROM activity_events e WHERE ` + predicate + ` ORDER BY ` + order + ` LIMIT ?`
	queryArgs := append(slices.Clone(args), limit)
	rows, err := s.db.Query(query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query activity events: %w", err)
	}
	items := make([]ActivityItem, 0, min(limit, 200))
	for rows.Next() {
		var item ActivityItem
		var occurredNS, recordedNS int64
		var payload string
		if err := rows.Scan(&item.ID, &occurredNS, &recordedNS, &item.Severity, &item.Category,
			&item.Action, &item.Source, &item.Actor.Type, &item.Actor.ID, &item.Actor.Name,
			&item.Actor.IPHash, &item.Actor.IPPrefix, &item.PayloadVersion, &payload); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan activity event: %w", err)
		}
		if !json.Valid([]byte(payload)) {
			_ = rows.Close()
			return nil, fmt.Errorf("activity event %d contains invalid payload JSON", item.ID)
		}
		item.OccurredAt = time.Unix(0, occurredNS).UTC()
		item.RecordedAt = time.Unix(0, recordedNS).UTC()
		item.Payload = json.RawMessage(payload)
		item.Clients = []ActivityClientSubject{}
		item.Tunnels = []ActivityTunnelSubject{}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate activity events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close activity event rows: %w", err)
	}
	if err := s.loadActivitySubjects(items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *ActivityStore) loadActivitySubjects(items []ActivityItem) error {
	if len(items) == 0 {
		return nil
	}
	placeholders := make([]string, len(items))
	args := make([]any, len(items))
	itemByID := make(map[int64]*ActivityItem, len(items))
	for index := range items {
		placeholders[index] = "?"
		args[index] = items[index].ID
		itemByID[items[index].ID] = &items[index]
	}
	inClause := strings.Join(placeholders, ",")
	clientRows, err := s.db.Query(`SELECT event_id, client_id, relation, display_name, hostname, is_truncated
		FROM activity_event_clients WHERE event_id IN (`+inClause+`) ORDER BY event_id, client_id, relation`, args...)
	if err != nil {
		return fmt.Errorf("query activity client subjects: %w", err)
	}
	for clientRows.Next() {
		var eventID int64
		var subject ActivityClientSubject
		var truncated int
		if err := clientRows.Scan(&eventID, &subject.ClientID, &subject.Relation, &subject.DisplayName, &subject.Hostname, &truncated); err != nil {
			_ = clientRows.Close()
			return fmt.Errorf("scan activity client subject: %w", err)
		}
		subject.Truncated = truncated != 0
		if item := itemByID[eventID]; item != nil {
			item.Clients = append(item.Clients, subject)
		}
	}
	if err := clientRows.Err(); err != nil {
		_ = clientRows.Close()
		return fmt.Errorf("iterate activity client subjects: %w", err)
	}
	if err := clientRows.Close(); err != nil {
		return fmt.Errorf("close activity client subjects: %w", err)
	}

	tunnelRows, err := s.db.Query(`SELECT event_id, tunnel_id, relation, name, tunnel_type, topology, is_truncated
		FROM activity_event_tunnels WHERE event_id IN (`+inClause+`) ORDER BY event_id, tunnel_id, relation`, args...)
	if err != nil {
		return fmt.Errorf("query activity tunnel subjects: %w", err)
	}
	defer tunnelRows.Close()
	for tunnelRows.Next() {
		var eventID int64
		var subject ActivityTunnelSubject
		var truncated int
		if err := tunnelRows.Scan(&eventID, &subject.TunnelID, &subject.Relation, &subject.Name, &subject.Type, &subject.Topology, &truncated); err != nil {
			return fmt.Errorf("scan activity tunnel subject: %w", err)
		}
		subject.Truncated = truncated != 0
		if item := itemByID[eventID]; item != nil {
			item.Tunnels = append(item.Tunnels, subject)
		}
	}
	if err := tunnelRows.Err(); err != nil {
		return fmt.Errorf("iterate activity tunnel subjects: %w", err)
	}
	return nil
}

func (s *ActivityStore) Prune(now time.Time, policy ActivityRetentionPolicy) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("activity store is not initialized")
	}
	if err := policy.validate(); err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin activity prune: %w", err)
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	var deleted int64
	for _, severity := range []ActivitySeverity{ActivitySeverityDebug, ActivitySeverityInfo, ActivitySeverityWarning, ActivitySeverityError} {
		rule := policy.rules()[severity]
		cutoff := now.UTC().Add(-time.Duration(rule.Days) * 24 * time.Hour).UnixNano()
		var result sql.Result
		if rule.MinCount == 0 {
			result, err = tx.Exec(`DELETE FROM activity_events WHERE severity = ? AND occurred_at_ns < ?`, severity, cutoff)
		} else {
			result, err = tx.Exec(`DELETE FROM activity_events
				WHERE severity = ? AND occurred_at_ns < ?
				AND id NOT IN (
					SELECT id FROM activity_events
					WHERE severity = ?
					ORDER BY occurred_at_ns DESC, id DESC LIMIT ?
				)`, severity, cutoff, severity, rule.MinCount)
		}
		if err != nil {
			return 0, fmt.Errorf("prune %s activity events: %w", severity, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("count pruned %s activity events: %w", severity, err)
		}
		deleted += count
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, fmt.Errorf("commit activity prune: %w", err)
	}
	return deleted, nil
}
