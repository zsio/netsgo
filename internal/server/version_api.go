package server

import (
	"net/http"
	"strings"
	"time"

	"netsgo/pkg/updater"
)

type versionCheckResponse struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version"`
	UpdateAvailable bool      `json:"update_available"`
	CheckedAt       time.Time `json:"checked_at"`
}

var checkVersionUpdate = func(currentVersion string) (*updater.Result, bool, error) {
	result, needed, err := updater.CheckForUpdate(updater.ChannelCNB, currentVersion)
	if err == nil {
		return result, needed, nil
	}
	return updater.CheckForUpdate(updater.ChannelGitHub, currentVersion)
}

func (s *Server) handleAPIVersionCheck(w http.ResponseWriter, r *http.Request) {
	currentVersion := strings.TrimSpace(r.URL.Query().Get("version"))
	if currentVersion == "" {
		http.Error(w, `{"error":"version is required"}`, http.StatusBadRequest)
		return
	}

	result, needed, err := checkVersionUpdate(currentVersion)
	if err != nil {
		http.Error(w, `{"error":"failed to check version"}`, http.StatusBadGateway)
		return
	}

	encodeJSON(w, http.StatusOK, versionCheckResponse{
		CurrentVersion:  currentVersion,
		LatestVersion:   result.NewVersion,
		UpdateAvailable: needed,
		CheckedAt:       time.Now(),
	})
}
