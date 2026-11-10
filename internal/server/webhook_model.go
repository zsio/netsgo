package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	webhookMaxPerUser                     = 50
	webhookMaxHeaders                     = 32
	webhookMaxTargets                     = 500
	webhookTemplateMaxBytes               = 64 << 10
	webhookRenderedRequestMax             = 256 << 10
	webhookResponseBodyMaxBytes           = 64 << 10
	webhookJSONRequestLimitBytes          = 128 << 10
	webhookDeliveryHistoryLimit           = 1000
	webhookMaxNotificationIntervalSeconds = 86400
)

type WebhookMethod string

const (
	WebhookMethodGET  WebhookMethod = "GET"
	WebhookMethodPOST WebhookMethod = "POST"
)

type WebhookTargetKind string

const (
	WebhookTargetClient WebhookTargetKind = "client"
	WebhookTargetTunnel WebhookTargetKind = "tunnel"
)

type WebhookTargetMode string

const (
	WebhookTargetAll      WebhookTargetMode = "all"
	WebhookTargetSelected WebhookTargetMode = "selected"
)

type WebhookDeliveryStatus string

const (
	WebhookDeliveryQueued   WebhookDeliveryStatus = "queued"
	WebhookDeliveryRetrying WebhookDeliveryStatus = "retrying"
	WebhookDeliverySuccess  WebhookDeliveryStatus = "success"
	WebhookDeliveryFailed   WebhookDeliveryStatus = "failed"
	WebhookDeliveryCanceled WebhookDeliveryStatus = "canceled"
)

type WebhookDeliveryOrigin string

const (
	WebhookOriginEvent  WebhookDeliveryOrigin = "event"
	WebhookOriginTest   WebhookDeliveryOrigin = "test"
	WebhookOriginReplay WebhookDeliveryOrigin = "replay"
)

type WebhookHeader struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ActivityWebhook struct {
	ID                  string            `json:"id"`
	Revision            int64             `json:"revision"`
	Name                string            `json:"name"`
	Enabled             bool              `json:"enabled"`
	TargetKind          WebhookTargetKind `json:"target_kind"`
	TargetMode          WebhookTargetMode `json:"target_mode"`
	TargetIDs           []string          `json:"target_ids"`
	Method              WebhookMethod     `json:"method"`
	URL                 string            `json:"url"`
	Headers             []WebhookHeader   `json:"headers"`
	Body                string            `json:"body"`
	Events              []string          `json:"events"`
	MinIntervalSeconds  int               `json:"min_interval_seconds"`
	Calls24h            int64             `json:"calls_24h"`
	LastStatus          string            `json:"last_status"`
	ConsecutiveFailures int64             `json:"consecutive_failures"`
	LastCalledAt        *string           `json:"last_called_at"`
	CreatedAt           string            `json:"created_at"`
	UpdatedAt           string            `json:"updated_at"`
}

type WebhookConfigInput struct {
	ID                 string            `json:"id"`
	ExpectedRevision   int64             `json:"expected_revision,omitempty"`
	Name               string            `json:"name"`
	Enabled            bool              `json:"enabled"`
	TargetKind         WebhookTargetKind `json:"target_kind"`
	TargetMode         WebhookTargetMode `json:"target_mode"`
	TargetIDs          []string          `json:"target_ids"`
	Method             WebhookMethod     `json:"method"`
	URL                string            `json:"url"`
	Headers            []WebhookHeader   `json:"headers"`
	Body               string            `json:"body"`
	Events             []string          `json:"events"`
	MinIntervalSeconds int               `json:"min_interval_seconds"`
}

type webhookConfigSnapshot struct {
	ID                 string            `json:"id"`
	Revision           int64             `json:"revision"`
	Name               string            `json:"name"`
	TargetKind         WebhookTargetKind `json:"target_kind"`
	TargetMode         WebhookTargetMode `json:"target_mode"`
	TargetIDs          []string          `json:"target_ids"`
	Method             WebhookMethod     `json:"method"`
	URL                string            `json:"url"`
	Headers            []WebhookHeader   `json:"headers"`
	Body               string            `json:"body"`
	Events             []string          `json:"events"`
	MinIntervalSeconds int               `json:"min_interval_seconds"`
}

func (w ActivityWebhook) snapshot() webhookConfigSnapshot {
	return webhookConfigSnapshot{
		ID: w.ID, Revision: w.Revision, Name: w.Name,
		TargetKind: w.TargetKind, TargetMode: w.TargetMode,
		TargetIDs: slices.Clone(w.TargetIDs), Method: w.Method, URL: w.URL,
		Headers: slices.Clone(w.Headers), Body: w.Body, Events: slices.Clone(w.Events),
		MinIntervalSeconds: w.MinIntervalSeconds,
	}
}

// toConfigInput rebuilds the stored configuration as an update input pinned to
// the current revision, so policy checks run against exactly what is stored.
func (w ActivityWebhook) toConfigInput(enabled bool) WebhookConfigInput {
	return WebhookConfigInput{
		ID: w.ID, ExpectedRevision: w.Revision, Name: w.Name, Enabled: enabled,
		TargetKind: w.TargetKind, TargetMode: w.TargetMode,
		TargetIDs: slices.Clone(w.TargetIDs), Method: w.Method, URL: w.URL,
		Headers: slices.Clone(w.Headers), Body: w.Body, Events: slices.Clone(w.Events),
		MinIntervalSeconds: w.MinIntervalSeconds,
	}
}

func (input WebhookConfigInput) snapshot(revision int64) webhookConfigSnapshot {
	return webhookConfigSnapshot{
		ID: input.ID, Revision: revision, Name: input.Name,
		TargetKind: input.TargetKind, TargetMode: input.TargetMode,
		TargetIDs: slices.Clone(input.TargetIDs), Method: input.Method, URL: input.URL,
		Headers: slices.Clone(input.Headers), Body: input.Body, Events: slices.Clone(input.Events),
		MinIntervalSeconds: input.MinIntervalSeconds,
	}
}

type webhookValidationError struct {
	Field string
	Code  string
	Err   error
}

func (e *webhookValidationError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return e.Err.Error()
}

func (e *webhookValidationError) Unwrap() error { return e.Err }

func invalidWebhook(field, code, message string) error {
	return &webhookValidationError{Field: field, Code: code, Err: errors.New(message)}
}

var (
	webhookIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	webhookHeaderPattern = regexp.MustCompile(`^[!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+$`)
)

var webhookRestrictedHeaders = map[string]struct{}{
	"connection": {}, "content-length": {}, "host": {}, "transfer-encoding": {},
	"user-agent": {}, "x-netsgo-delivery": {}, "x-netsgo-event": {}, "x-netsgo-attempt": {},
}

var webhookEventTargetKinds = map[string]WebhookTargetKind{
	"client.online": WebhookTargetClient, "client.offline": WebhookTargetClient,
	"tunnel.stopped": WebhookTargetTunnel, "tunnel.resumed": WebhookTargetTunnel,
	"tunnel.runtime_changed": WebhookTargetTunnel, "tunnel.runtime_error": WebhookTargetTunnel,
	"tunnel.runtime_recovered": WebhookTargetTunnel,
	"p2p.checking":             WebhookTargetTunnel, "p2p.connected": WebhookTargetTunnel,
	"p2p.failed": WebhookTargetTunnel, "p2p.fallback": WebhookTargetTunnel,
	"p2p.session_closed": WebhookTargetTunnel,
}

func normalizeWebhookInput(input WebhookConfigInput) WebhookConfigInput {
	input.ID = strings.TrimSpace(input.ID)
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	if input.TargetMode == WebhookTargetAll {
		input.TargetIDs = []string{}
	}
	input.TargetIDs = uniqueNonEmptyStrings(input.TargetIDs)
	input.Events = uniqueNonEmptyStrings(input.Events)
	if input.Headers == nil {
		input.Headers = []WebhookHeader{}
	}
	for index := range input.Headers {
		input.Headers[index].ID = strings.TrimSpace(input.Headers[index].ID)
		input.Headers[index].Key = strings.TrimSpace(input.Headers[index].Key)
	}
	return input
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func validateWebhookInput(input WebhookConfigInput, fixtures map[string]map[string]any, variables []WebhookVariable, allowPrivateTargets bool) error {
	if input.MinIntervalSeconds < 0 || input.MinIntervalSeconds > webhookMaxNotificationIntervalSeconds {
		return invalidWebhook("min_interval_seconds", "invalid_interval", "minimum notification interval must be between 0 and 86400 seconds")
	}
	if !webhookIDPattern.MatchString(input.ID) {
		return invalidWebhook("id", "invalid_id", "Webhook id is invalid")
	}
	if input.Name == "" {
		return invalidWebhook("name", "required", "Webhook name is required")
	}
	if utf8.RuneCountInString(input.Name) > 120 {
		return invalidWebhook("name", "too_long", "Webhook name is too long")
	}
	if !validWebhookHeaderValue(input.Name) {
		return invalidWebhook("name", "invalid_name", "Webhook name must not contain control characters")
	}
	if input.TargetKind != WebhookTargetClient && input.TargetKind != WebhookTargetTunnel {
		return invalidWebhook("target_kind", "invalid_target_kind", "Webhook target kind is invalid")
	}
	if input.TargetMode != WebhookTargetAll && input.TargetMode != WebhookTargetSelected {
		return invalidWebhook("target_mode", "invalid_target_mode", "Webhook target mode is invalid")
	}
	if input.TargetMode == WebhookTargetSelected && len(input.TargetIDs) == 0 {
		return invalidWebhook("targets", "required", "at least one target is required")
	}
	if len(input.TargetIDs) > webhookMaxTargets {
		return invalidWebhook("targets", "too_many", "too many Webhook targets")
	}
	for _, id := range input.TargetIDs {
		if len(id) > activityIDMaxBytes || !utf8.ValidString(id) {
			return invalidWebhook("targets", "invalid_target", "Webhook target id is invalid")
		}
	}
	if len(input.Events) == 0 {
		return invalidWebhook("events", "required", "at least one event is required")
	}
	for _, event := range input.Events {
		kind, ok := webhookEventTargetKinds[event]
		if !ok {
			return invalidWebhook("events", "unknown_event", fmt.Sprintf("unsupported Webhook event %q", event))
		}
		if kind != input.TargetKind {
			return invalidWebhook("events", "event_target_mismatch", "Webhook event does not match target kind")
		}
	}
	if input.Method != WebhookMethodGET && input.Method != WebhookMethodPOST {
		return invalidWebhook("method", "invalid_method", "Webhook method must be GET or POST")
	}
	if input.URL == "" {
		return invalidWebhook("url", "required", "Webhook URL is required")
	}
	if len(input.URL) > 8<<10 {
		return invalidWebhook("url", "too_long", "Webhook URL is too long")
	}
	if issues := webhookTemplateIssues(input.URL, input.Events, "url", variables); len(issues) > 0 {
		return issues[0]
	}
	if len(input.Headers) > webhookMaxHeaders {
		return invalidWebhook("headers", "too_many", "too many Webhook headers")
	}
	seenHeaders := make(map[string]struct{}, len(input.Headers))
	for _, header := range input.Headers {
		if header.Key == "" && strings.TrimSpace(header.Value) == "" {
			continue
		}
		if !webhookHeaderPattern.MatchString(header.Key) || strings.ContainsAny(header.Value, "\r\n") {
			return invalidWebhook("headers", "invalid_header", "Webhook header is invalid")
		}
		key := strings.ToLower(header.Key)
		if _, ok := seenHeaders[key]; ok {
			return invalidWebhook("headers", "duplicate_header", "Webhook header is duplicated")
		}
		seenHeaders[key] = struct{}{}
		if _, restricted := webhookRestrictedHeaders[key]; restricted {
			return invalidWebhook("headers", "restricted_header", "Webhook header is managed by NetsGo")
		}
		if len(header.Value) > 8<<10 {
			return invalidWebhook("headers", "too_long", "Webhook header value is too long")
		}
		if issues := webhookTemplateIssues(header.Value, input.Events, "header", variables); len(issues) > 0 {
			return issues[0]
		}
	}
	if len(input.Body) > webhookTemplateMaxBytes {
		return invalidWebhook("body", "too_long", "Webhook JSON body is too large")
	}
	if input.Method == WebhookMethodPOST {
		var body any
		if err := json.Unmarshal([]byte(input.Body), &body); err != nil {
			return invalidWebhook("body", "invalid_json", "Webhook body must be valid JSON")
		}
		if body == nil {
			return invalidWebhook("body", "body_must_be_object", "Webhook body must be a JSON object")
		}
		if _, ok := body.(map[string]any); !ok {
			return invalidWebhook("body", "body_must_be_object", "Webhook body must be a JSON object")
		}
		if issues := webhookTemplateIssues(input.Body, input.Events, "body", variables); len(issues) > 0 {
			return issues[0]
		}
	}
	for _, event := range input.Events {
		values := fixtures[event]
		if values == nil {
			return invalidWebhook("events", "missing_fixture", "Webhook event sample is unavailable")
		}
		values = cloneWebhookValues(values)
		values["webhook.id"] = input.ID
		values["webhook.name"] = input.Name
		rendered, err := renderWebhookRequest(input.snapshot(max(input.ExpectedRevision, 1)), values, 1)
		if err != nil {
			return invalidWebhook("body", "invalid_template", err.Error())
		}
		parsed, err := url.ParseRequestURI(rendered.URL)
		if err != nil || parsed.Host == "" {
			return invalidWebhook("url", "invalid_url", "Webhook URL is invalid after rendering")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return invalidWebhook("url", "invalid_url_scheme", "Webhook URL must use HTTP or HTTPS")
		}
		if !allowPrivateTargets && webhookHostBlocked(parsed.Hostname()) {
			return invalidWebhook("url", "url_target_not_allowed", "Webhook URL targets a private address; allow private targets in server settings first")
		}
		for key, value := range rendered.Headers {
			if !validWebhookHeaderValue(value) {
				return invalidWebhook("headers", "invalid_header_value", "Webhook header "+key+" renders to an invalid value")
			}
		}
		if rendered.size() > webhookRenderedRequestMax {
			return invalidWebhook("body", "rendered_request_too_large", "rendered Webhook request is too large")
		}
	}
	return nil
}
