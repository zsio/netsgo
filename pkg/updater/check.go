package updater

import (
	"fmt"
	"time"

	buildversion "netsgo/pkg/version"
)

const (
	InstallMethodService = "service"
	InstallMethodDocker  = "docker"
	InstallMethodBinary  = "binary"

	RecommendedActionNone          = "none"
	RecommendedActionRunScript     = "run_script"
	RecommendedActionGitHubRelease = "github_release"
	RecommendedActionDockerDocs    = "docker_docs"

	ReasonReleaseIndexUnavailable = "release_index_unavailable"
	ReasonChannelUnavailable      = "channel_unavailable"
	ReasonCurrentUncomparable     = "current_version_uncomparable"
	ReasonNoMatchingCandidate     = "no_matching_candidate"
	ReasonClientOffline           = "client_offline"
)

type Commands struct {
	Domestic string `json:"domestic"`
	Global   string `json:"global"`
}

type CheckResult struct {
	CurrentVersion     string
	LatestVersion      string
	UpdateAvailable    bool
	CheckedAt          time.Time
	InstallMethod      string
	RecommendedChannel string
	RecommendedAction  string
	Commands           *Commands
	ReleaseURL         string
	CheckFailed        bool
	RefreshFailed      bool
	CacheSource        string
	Reason             string
}

func ComputeCheckResult(idx *ReleaseIndex, currentVersion, installMethod, cacheSource string, refreshFailed bool, checkedAt time.Time) CheckResult {
	if checkedAt.IsZero() {
		checkedAt = time.Now()
	}
	installMethod = normalizeInstallMethod(installMethod)
	result := CheckResult{
		CurrentVersion:    currentVersion,
		CheckedAt:         checkedAt,
		InstallMethod:     installMethod,
		RecommendedAction: RecommendedActionNone,
		ReleaseURL:        ReleaseURL,
		RefreshFailed:     refreshFailed,
		CacheSource:       cacheSource,
	}

	if idx == nil {
		result.CheckFailed = true
		result.RecommendedAction = RecommendedActionGitHubRelease
		result.Reason = ReasonReleaseIndexUnavailable
		return result
	}

	base, comparable := buildversion.ComparableBase(currentVersion)
	if !comparable {
		result.CheckFailed = true
		result.Reason = ReasonCurrentUncomparable
		return result
	}

	candidates := candidatesForCurrent(idx, base)
	if len(candidates) == 0 {
		result.CheckFailed = true
		result.Reason = ReasonNoMatchingCandidate
		return result
	}

	best, ok := selectBestCandidate(candidates)
	if !ok {
		result.CheckFailed = true
		result.Reason = ReasonChannelUnavailable
		return result
	}

	result.LatestVersion = best.Version
	result.RecommendedChannel = best.Channel
	result.UpdateAvailable = best.CompareToCurrent > 0
	result.RecommendedAction = recommendedActionForInstallMethod(installMethod, result.UpdateAvailable)
	if result.UpdateAvailable && installMethod == InstallMethodService {
		result.Commands = BuildUpgradeCommands(best.Channel)
	}
	return result
}

func BuildUpgradeCommands(channel string) *Commands {
	return &Commands{
		Domestic: fmt.Sprintf("curl -fsSL https://cnb.cool/zsio/netsgo/-/raw/main/scripts/upgrade.sh | sh -s -- --source cnb --channel %s -y", channel),
		Global:   fmt.Sprintf("curl -fsSL https://raw.githubusercontent.com/zsio/netsgo/main/scripts/upgrade.sh | sh -s -- --source github --channel %s -y", channel),
	}
}

type versionCandidate struct {
	Channel          string
	Version          string
	CompareToCurrent int
}

func candidatesForCurrent(idx *ReleaseIndex, currentBase string) []versionCandidate {
	currentChannel, _ := buildversion.ChannelForReleaseTag(currentBase)
	channels := []string{buildversion.ChannelStable}
	if currentChannel == buildversion.ChannelBeta {
		channels = []string{buildversion.ChannelStable, buildversion.ChannelBeta}
	}

	var candidates []versionCandidate
	for _, channel := range channels {
		entry, ok := idx.Channels[channel]
		if !ok || entry.Latest == "" {
			continue
		}
		if !buildversion.IsReleaseTag(entry.Latest) {
			continue
		}
		cmp, err := buildversion.Compare(entry.Latest, currentBase)
		if err != nil {
			continue
		}
		candidates = append(candidates, versionCandidate{
			Channel:          channel,
			Version:          entry.Latest,
			CompareToCurrent: cmp,
		})
	}
	return candidates
}

func selectBestCandidate(candidates []versionCandidate) (versionCandidate, bool) {
	if len(candidates) == 0 {
		return versionCandidate{}, false
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		cmp, err := buildversion.Compare(candidate.Version, best.Version)
		if err == nil && cmp > 0 {
			best = candidate
		}
	}
	return best, true
}

func normalizeInstallMethod(method string) string {
	switch method {
	case InstallMethodService, InstallMethodDocker, InstallMethodBinary:
		return method
	default:
		return InstallMethodBinary
	}
}

func recommendedActionForInstallMethod(method string, updateAvailable bool) string {
	if !updateAvailable {
		return RecommendedActionNone
	}
	switch method {
	case InstallMethodService:
		return RecommendedActionRunScript
	case InstallMethodDocker:
		return RecommendedActionDockerDocs
	default:
		return RecommendedActionGitHubRelease
	}
}
