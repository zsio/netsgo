package updater

import (
	"strings"
	"testing"
	"time"
)

func TestComputeCheckResultStableUsesStableOnly(t *testing.T) {
	idx := testIndex("v0.1.1", "v0.2.0-beta.1")
	got := ComputeCheckResult(idx, "v0.1.0", InstallMethodService, "fresh", false, time.Unix(1, 0))

	if !got.UpdateAvailable || got.LatestVersion != "v0.1.1" || got.RecommendedChannel != "stable" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.RecommendedAction != RecommendedActionRunScript || got.Commands == nil {
		t.Fatalf("expected service script command, got %+v", got)
	}
	if want := "--source cnb --channel stable -y"; !strings.Contains(got.Commands.Domestic, want) {
		t.Fatalf("domestic command %q missing %q", got.Commands.Domestic, want)
	}
	if commandArgsContainForce(got.Commands.Domestic) || commandArgsContainForce(got.Commands.Global) {
		t.Fatalf("web upgrade command must not include force flag: %+v", got.Commands)
	}
}

func TestComputeCheckResultBetaChoosesSemverHighest(t *testing.T) {
	idx := testIndex("v0.2.0", "v0.2.1-beta.1")
	got := ComputeCheckResult(idx, "v0.2.0-beta.5", InstallMethodBinary, "cache", false, time.Unix(1, 0))

	if !got.UpdateAvailable || got.LatestVersion != "v0.2.1-beta.1" || got.RecommendedChannel != "beta" {
		t.Fatalf("unexpected beta result: %+v", got)
	}
	if got.RecommendedAction != RecommendedActionGitHubRelease || got.Commands != nil {
		t.Fatalf("binary update must not include commands: %+v", got)
	}
}

func TestComputeCheckResultBetaCanRecommendStable(t *testing.T) {
	idx := testIndex("v0.1.0", "v0.1.0-beta.6")
	got := ComputeCheckResult(idx, "v0.1.0-beta.5", InstallMethodService, "cache", false, time.Unix(1, 0))

	if !got.UpdateAvailable || got.LatestVersion != "v0.1.0" || got.RecommendedChannel != "stable" {
		t.Fatalf("unexpected stable recommendation: %+v", got)
	}
}

func TestComputeCheckResultBetaCanUseStableWhenBetaMissing(t *testing.T) {
	idx := testIndex("v0.2.0", "")
	got := ComputeCheckResult(idx, "v0.1.0-beta.5", InstallMethodService, "cache", false, time.Unix(1, 0))

	if !got.UpdateAvailable || got.LatestVersion != "v0.2.0" || got.RecommendedChannel != "stable" {
		t.Fatalf("beta current should use stable candidate when beta is missing: %+v", got)
	}
}

func TestComputeCheckResultBetaCanUseBetaWhenStableMissing(t *testing.T) {
	idx := testIndex("", "v0.2.0-beta.6")
	got := ComputeCheckResult(idx, "v0.1.0-beta.5", InstallMethodService, "cache", false, time.Unix(1, 0))

	if !got.UpdateAvailable || got.LatestVersion != "v0.2.0-beta.6" || got.RecommendedChannel != "beta" {
		t.Fatalf("beta current should use beta candidate when stable is missing: %+v", got)
	}
}

func TestComputeCheckResultBetaFailsWhenBothChannelsMissing(t *testing.T) {
	idx := testIndex("", "")
	got := ComputeCheckResult(idx, "v0.1.0-beta.5", InstallMethodService, "cache", false, time.Unix(1, 0))

	if !got.CheckFailed || got.Reason != ReasonNoMatchingCandidate || got.UpdateAvailable {
		t.Fatalf("expected no matching candidate for empty beta candidates: %+v", got)
	}
}

func TestComputeCheckResultUncomparableCurrent(t *testing.T) {
	got := ComputeCheckResult(testIndex("v0.1.0", ""), "dev", InstallMethodService, "fresh", false, time.Unix(1, 0))

	if !got.CheckFailed || got.Reason != ReasonCurrentUncomparable || got.UpdateAvailable {
		t.Fatalf("unexpected uncomparable result: %+v", got)
	}
	if got.Commands != nil {
		t.Fatalf("check failure must not include commands: %+v", got.Commands)
	}
}

func TestComputeCheckResultReleaseIndexUnavailable(t *testing.T) {
	got := ComputeCheckResult(nil, "v0.1.0", InstallMethodService, "none", true, time.Unix(1, 0))

	if !got.CheckFailed || !got.RefreshFailed || got.Reason != ReasonReleaseIndexUnavailable {
		t.Fatalf("unexpected failure result: %+v", got)
	}
	if got.RecommendedAction != RecommendedActionGitHubRelease {
		t.Fatalf("check failure should fall back to manual release link, got %q", got.RecommendedAction)
	}
}

func TestComputeCheckResultStaleCacheRefreshFailure(t *testing.T) {
	got := ComputeCheckResult(testIndex("v0.1.0", ""), "v0.1.0", InstallMethodService, "stale_cache", true, time.Unix(1, 0))

	if got.CheckFailed || !got.RefreshFailed || got.CacheSource != "stale_cache" {
		t.Fatalf("unexpected stale cache result: %+v", got)
	}
	if got.UpdateAvailable {
		t.Fatalf("same version should not be reported as update: %+v", got)
	}
}

func testIndex(stable, beta string) *ReleaseIndex {
	channels := map[string]ReleaseChannel{}
	if stable != "" {
		channels["stable"] = ReleaseChannel{Latest: stable}
	}
	if beta != "" {
		channels["beta"] = ReleaseChannel{Latest: beta}
	}
	return &ReleaseIndex{Schema: 1, Project: "netsgo", Channels: channels}
}

func commandArgsContainForce(command string) bool {
	_, args, ok := strings.Cut(command, " sh -s -- ")
	if !ok {
		return false
	}
	for _, field := range strings.Fields(args) {
		if field == "-f" || field == "--force" {
			return true
		}
	}
	return false
}
