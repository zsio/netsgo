package updater

import (
	"errors"
	"fmt"
	"netsgo/internal/svcmgr"
	"netsgo/pkg/version"
	"strings"
)

func checkUpdateNeeded(currentVersion, latestVersion string) (bool, error) {
	latestNormalized, err := version.NormalizeVersionString(latestVersion)
	if err != nil {
		return false, fmt.Errorf("parse latest: %w", err)
	}
	currentNormalized, err := version.NormalizeVersionString(currentVersion)
	if err != nil {
		return true, nil
	}
	if isDevelopmentSnapshotVersion(currentVersion) || isDevelopmentSnapshotVersion(currentNormalized) {
		return currentNormalized != latestNormalized, nil
	}
	current, err := version.ParseSemver(currentNormalized)
	if err != nil {
		return false, fmt.Errorf("parse current: %w", err)
	}
	latest, err := version.ParseSemver(latestNormalized)
	if err != nil {
		return false, fmt.Errorf("parse latest: %w", err)
	}
	return latest.Compare(current) > 0, nil
}

func releaseTrackForCurrentVersion(currentVersion string) releaseTrack {
	currentNormalized, err := version.NormalizeVersionString(currentVersion)
	if err != nil {
		return releaseTrackAny
	}
	current, err := version.ParseSemver(currentNormalized)
	if err != nil {
		return releaseTrackAny
	}
	if current.Prerelease == "" {
		return releaseTrackStable
	}
	if strings.HasPrefix(current.Prerelease, "beta.") {
		return releaseTrackBeta
	}
	return releaseTrackAny
}

func isDevelopmentSnapshotVersion(v string) bool {
	lower := strings.ToLower(v)
	return strings.Contains(lower, "snapshot") || strings.Contains(lower, "dirty") || strings.Contains(lower, "dev")
}

func normalizedVersionOrOriginal(v string) string {
	normalized, err := version.NormalizeVersionString(v)
	if err != nil {
		return v
	}
	return normalized
}

func installedUnits() []string {
	var units []string
	if svcmgr.Detect(svcmgr.RoleServer) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleServer))
	}
	if svcmgr.Detect(svcmgr.RoleClient) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleClient))
	}
	return units
}

type Result struct {
	OldVersion string
	NewVersion string
	Stopped    []string
	Started    []string
}

func CheckForUpdate(channel DownloadChannel, currentVersion string) (*Result, bool, error) {
	result := &Result{OldVersion: currentVersion}

	track := releaseTrackForCurrentVersion(currentVersion)
	latest, err := fetchLatestVersionFunc(channel, track)
	if err != nil {
		if errors.Is(err, errNoCompatibleRelease) && track == releaseTrackStable {
			result.NewVersion = normalizedVersionOrOriginal(currentVersion)
			return result, false, nil
		}
		return result, false, fmt.Errorf("check latest: %w", err)
	}
	result.NewVersion = latest

	needed, err := checkUpdateNeeded(currentVersion, latest)
	if err != nil {
		return result, false, fmt.Errorf("compare: %w", err)
	}

	return result, needed, nil
}

func rollbackUpdateOrUpgrade(orch *Orchestrator, started, stopped []string, backupPath string, restoreBinary bool) error {
	var rollbackErr error
	if len(started) > 0 {
		if err := orch.StopStartedServices(started); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if restoreBinary {
		if err := restoreBinaryFunc(backupPath, installedBinaryPath); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore binary: %w", err))
		}
	}
	if err := orch.RestartStoppedServices(stopped); err != nil {
		rollbackErr = errors.Join(rollbackErr, err)
	}
	return rollbackErr
}

func recoverStoppedServicesOnPanic(orch *Orchestrator, stopped *[]string, armed *bool) {
	if r := recover(); r != nil {
		if *armed {
			_ = orch.RestartStoppedServices(*stopped)
		}
		panic(r)
	}
}

func recoverUpdateOrUpgradeOnPanic(orch *Orchestrator, started, stopped *[]string, backupPath *string, restoreBinary *bool) {
	if r := recover(); r != nil {
		_ = rollbackUpdateOrUpgrade(orch, *started, *stopped, *backupPath, *restoreBinary)
		panic(r)
	}
}
