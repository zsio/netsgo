package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleGetClientTraffic(w http.ResponseWriter, r *http.Request) {
	scope, scopeOK := requireResourceScope(w, r)
	if !scopeOK {
		return
	}
	clientID := r.PathValue("id")
	if clientID == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_client_id", "missing client id")
		return
	}
	if s.auth == nil || s.auth.adminStore == nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_store_unavailable", "admin store unavailable")
		return
	}
	if _, ok := s.auth.adminStore.GetRegisteredClientForUser(scope.OwnerUserID, clientID); !ok {
		writeAPIError(w, http.StatusNotFound, "client_not_found", "client not found")
		return
	}

	if s.trafficStore == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "traffic_store_unavailable", "traffic store unavailable")
		return
	}

	q := r.URL.Query()

	now := time.Now()
	from, to, err := parseTrafficTimeRange(q.Get("from"), q.Get("to"), now)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_traffic_time_range", err.Error())
		return
	}

	tunnelName := q.Get("tunnel")
	resolution, err := parseTrafficResolution(q.Get("resolution"), from, to)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_traffic_resolution", err.Error())
		return
	}
	if resolution == TrafficResolutionSecond {
		if err := validateRealtimeTrafficTimeRange(from, to); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_traffic_time_range", err.Error())
			return
		}
	}

	s.flushTrafficObservations()

	var result TrafficQueryResult
	knownTunnels := s.knownTrafficTunnelsForUser(scope.OwnerUserID, clientID, tunnelName)
	if resolution == TrafficResolutionSecond {
		result, err = s.buildRealtimeTrafficResultForUser(scope.OwnerUserID, clientID, tunnelName, from, to, knownTunnels)
	} else {
		result, err = s.trafficStore.QueryWithResolutionForUser(scope.OwnerUserID, clientID, tunnelName, from, to, resolution)
		if err == nil && tunnelName == "" {
			result = filterTrafficResultByKnownTunnels(result, knownTunnels)
		}
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "traffic_query_failed", "failed to query traffic")
		return
	}

	encodeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetRealtimeTraffic(w http.ResponseWriter, r *http.Request) {
	scope, scopeOK := requireResourceScope(w, r)
	if !scopeOK {
		return
	}
	if s.trafficStore == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "traffic_store_unavailable", "traffic store unavailable")
		return
	}

	event, err := s.collectRealtimeTrafficForUser(scope.OwnerUserID, time.Now())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "traffic_query_failed", "failed to query realtime traffic")
		return
	}

	encodeJSON(w, http.StatusOK, event)
}

func parseTrafficResolution(resolutionStr string, from, to time.Time) (TrafficResolution, error) {
	if resolutionStr == "" {
		return autoTrafficResolution(from, to), nil
	}

	switch TrafficResolution(resolutionStr) {
	case TrafficResolutionSecond, TrafficResolutionMinute, TrafficResolutionHour:
		return TrafficResolution(resolutionStr), nil
	default:
		return "", errors.New("resolution must be 'second', 'minute', or 'hour'")
	}
}

func parseTrafficTimeRange(fromStr, toStr string, now time.Time) (time.Time, time.Time, error) {
	to := now
	from := now.Add(-24 * time.Hour)

	if toStr != "" {
		t, err := parseUnixOrRFC3339(toStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = t
	}

	if fromStr != "" {
		t, err := parseUnixOrRFC3339(fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = t
	}

	from = from.UTC()
	to = to.UTC()
	if from.After(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	if to.Sub(from) > trafficMaxRange {
		return time.Time{}, time.Time{}, errors.New("time range must be within 7 days")
	}

	return from, to, nil
}

func parseUnixOrRFC3339(s string) (time.Time, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(n, 0).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
