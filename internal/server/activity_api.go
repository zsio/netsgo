package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleAPIActivity(w http.ResponseWriter, r *http.Request) {
	s.ensureSharedStoreReferences()
	if s.activityStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "activity_store_unavailable", "activity store not initialized")
		return
	}
	query, err := parseActivityQuery(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_activity_query", err.Error())
		return
	}
	page, err := s.activityStore.Query(query)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "activity_query_failed", "failed to query activity")
		return
	}
	encodeJSON(w, http.StatusOK, page)
}

func parseActivityQuery(r *http.Request) (ActivityQuery, error) {
	values := r.URL.Query()
	query := ActivityQuery{Scope: ActivityScope(values.Get("scope")), Limit: 50}
	if query.Scope == "" {
		query.Scope = ActivityScopeGlobal
	}
	switch query.Scope {
	case ActivityScopeGlobal:
		if values.Get("client_id") != "" || values.Get("tunnel_id") != "" {
			return ActivityQuery{}, fmt.Errorf("global scope must not include client_id or tunnel_id")
		}
	case ActivityScopeClient:
		query.ScopeID = values.Get("client_id")
		if query.ScopeID == "" || values.Get("tunnel_id") != "" {
			return ActivityQuery{}, fmt.Errorf("client scope requires only client_id")
		}
	case ActivityScopeTunnel:
		query.ScopeID = values.Get("tunnel_id")
		if query.ScopeID == "" || values.Get("client_id") != "" {
			return ActivityQuery{}, fmt.Errorf("tunnel scope requires only tunnel_id")
		}
	default:
		return ActivityQuery{}, fmt.Errorf("unsupported activity scope %q", query.Scope)
	}
	var err error
	if raw := values.Get("before"); raw != "" {
		query.BeforeID, err = parsePositiveActivityInt(raw, "before")
		if err != nil {
			return ActivityQuery{}, err
		}
	}
	if raw := values.Get("after"); raw != "" {
		query.AfterID, err = parsePositiveActivityInt(raw, "after")
		if err != nil {
			return ActivityQuery{}, err
		}
	}
	if query.BeforeID > 0 && query.AfterID > 0 {
		return ActivityQuery{}, fmt.Errorf("before and after are mutually exclusive")
	}
	if raw := values.Get("limit"); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit < 1 || limit > 200 {
			return ActivityQuery{}, fmt.Errorf("limit must be between 1 and 200")
		}
		query.Limit = limit
	}
	severities := values["severity"]
	if len(severities) == 0 {
		query.Severities = []ActivitySeverity{ActivitySeverityInfo, ActivitySeverityWarning, ActivitySeverityError}
	} else {
		for _, severity := range severities {
			query.Severities = append(query.Severities, ActivitySeverity(strings.TrimSpace(severity)))
		}
	}
	for _, category := range values["category"] {
		query.Categories = append(query.Categories, ActivityCategory(strings.TrimSpace(category)))
	}
	if query.From, err = parseOptionalActivityTime(values.Get("from"), "from"); err != nil {
		return ActivityQuery{}, err
	}
	if query.To, err = parseOptionalActivityTime(values.Get("to"), "to"); err != nil {
		return ActivityQuery{}, err
	}
	if query.From != nil && query.To != nil && !query.From.Before(*query.To) {
		return ActivityQuery{}, fmt.Errorf("from must be before to")
	}
	return query, nil
}

func parsePositiveActivityInt(raw, name string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func parseOptionalActivityTime(raw, name string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be RFC3339", name)
	}
	value = value.UTC()
	return &value, nil
}
