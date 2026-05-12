package server

import (
	"net/http"
	"strings"
	"time"

	"netsgo/internal/installmethod"
	"netsgo/pkg/protocol"
	"netsgo/pkg/updater"
	buildversion "netsgo/pkg/version"
)

type versionCheckResponse struct {
	Target             string            `json:"target"`
	TargetID           string            `json:"target_id"`
	CurrentVersion     string            `json:"current_version"`
	LatestVersion      string            `json:"latest_version"`
	UpdateAvailable    bool              `json:"update_available"`
	CheckedAt          time.Time         `json:"checked_at"`
	InstallMethod      string            `json:"install_method"`
	RecommendedChannel string            `json:"recommended_channel"`
	RecommendedAction  string            `json:"recommended_action"`
	Commands           *updater.Commands `json:"commands"`
	ReleaseURL         string            `json:"release_url"`
	CheckFailed        bool              `json:"check_failed"`
	RefreshFailed      bool              `json:"refresh_failed"`
	CacheSource        string            `json:"cache_source"`
	Reason             string            `json:"reason"`
}

func fetchDefaultReleaseIndex() (*updater.ReleaseIndex, error) {
	idx, _, err := updater.FetchReleaseIndex(nil, updater.LatestIndexURLCNB, updater.LatestIndexURLGitHub)
	return idx, err
}

func (s *Server) handleAPIVersionCheck(w http.ResponseWriter, r *http.Request) {
	force := parseForce(r)
	capability := s.serverUpdateCapability(time.Now(), force)
	result := s.computeVersionCheck(force, "server", "server", buildversion.Current, capability.InstallMethod)
	encodeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAPIClientVersionCheck(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	value, ok := s.clients.Load(clientID)
	if !ok {
		result := versionCheckFromUpdater("client", clientID, updater.CheckResult{
			CurrentVersion:    "",
			CheckedAt:         time.Now(),
			InstallMethod:     updater.InstallMethodBinary,
			RecommendedAction: updater.RecommendedActionGitHubRelease,
			ReleaseURL:        updater.ReleaseURL,
			CheckFailed:       true,
			CacheSource:       "none",
			Reason:            updater.ReasonClientOffline,
		})
		encodeJSON(w, http.StatusOK, result)
		return
	}
	client := value.(*ClientConn)
	if !client.isLive() {
		result := versionCheckFromUpdater("client", clientID, updater.CheckResult{
			CurrentVersion:    client.GetInfo().Version,
			CheckedAt:         time.Now(),
			InstallMethod:     updater.InstallMethodBinary,
			RecommendedAction: updater.RecommendedActionGitHubRelease,
			ReleaseURL:        updater.ReleaseURL,
			CheckFailed:       true,
			CacheSource:       "none",
			Reason:            updater.ReasonClientOffline,
		})
		encodeJSON(w, http.StatusOK, result)
		return
	}

	info := client.GetInfo()
	result := s.computeVersionCheck(parseForce(r), "client", clientID, info.Version, installMethodFromClientInfo(info))
	encodeJSON(w, http.StatusOK, result)
}

func (s *Server) serverUpdateCapability(now time.Time, force ...bool) *protocol.UpdateCapability {
	cache := s.updateCapabilityCache
	if cache == nil {
		cache = newUpdateCapabilityCache(installmethod.Detect)
	}
	if len(force) > 0 && force[0] {
		return cache.Refresh(now)
	}
	return cache.Get(now)
}

func (s *Server) computeVersionCheck(force bool, target, targetID, currentVersion, installMethod string) versionCheckResponse {
	now := time.Now()
	snap := s.releaseIndexCache.Get(force, now)
	result := updater.ComputeCheckResult(snap.index, currentVersion, installMethod, snap.cacheSource, snap.refreshFailed, now)
	return versionCheckFromUpdater(target, targetID, result)
}

func versionCheckFromUpdater(target, targetID string, result updater.CheckResult) versionCheckResponse {
	return versionCheckResponse{
		Target:             target,
		TargetID:           targetID,
		CurrentVersion:     result.CurrentVersion,
		LatestVersion:      result.LatestVersion,
		UpdateAvailable:    result.UpdateAvailable,
		CheckedAt:          result.CheckedAt,
		InstallMethod:      result.InstallMethod,
		RecommendedChannel: result.RecommendedChannel,
		RecommendedAction:  result.RecommendedAction,
		Commands:           result.Commands,
		ReleaseURL:         result.ReleaseURL,
		CheckFailed:        result.CheckFailed,
		RefreshFailed:      result.RefreshFailed,
		CacheSource:        result.CacheSource,
		Reason:             result.Reason,
	}
}

func installMethodFromClientInfo(info protocol.ClientInfo) string {
	if info.UpdateCapability == nil {
		return updater.InstallMethodBinary
	}
	return normalizeInstallMethod(strings.TrimSpace(info.UpdateCapability.InstallMethod))
}

func normalizeInstallMethod(method string) string {
	switch method {
	case updater.InstallMethodService, updater.InstallMethodDocker, updater.InstallMethodBinary:
		return method
	default:
		return updater.InstallMethodBinary
	}
}

func parseForce(r *http.Request) bool {
	return strings.EqualFold(r.URL.Query().Get("force"), "true") || r.URL.Query().Get("force") == "1"
}
