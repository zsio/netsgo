package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleGetClientTraffic(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	if clientID == "" {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing client id"})
		return
	}

	if s.trafficStore == nil {
		encodeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "traffic store unavailable"})
		return
	}

	q := r.URL.Query()

	now := time.Now()
	from, to, err := parseTrafficTimeRange(q.Get("from"), q.Get("to"), now)
	if err != nil {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	tunnelName := q.Get("tunnel")
	resolutionStr := q.Get("resolution")
	resolution := autoTrafficResolution(from, to)

	if resolutionStr != "" && resolutionStr != "minute" && resolutionStr != "hour" {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "resolution must be 'minute' or 'hour'"})
		return
	}

	if resolutionStr != "" {
		resolution = TrafficResolution(resolutionStr)
	}

	result := s.trafficStore.QueryWithResolution(clientID, tunnelName, from, to, resolution)

	encodeJSON(w, http.StatusOK, result)
}

func parseTrafficTimeRange(fromStr, toStr string, now time.Time) (time.Time, time.Time, error) {
	to := now
	from := now.Add(-time.Hour)

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
		return time.Time{}, time.Time{}, errors.New("time range must be within 30 days")
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
