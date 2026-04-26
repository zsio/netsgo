package updater

import (
	"errors"
	"fmt"
	"netsgo/internal/svcmgr"
	"netsgo/pkg/version"
	"os"
	"path/filepath"
)

var downloadAndExtractFunc = downloadAndExtract
var osMkdirTempFunc = os.MkdirTemp

func checkUpdateNeeded(currentVersion, latestVersion string) (bool, error) {
	currentNormalized, err := version.NormalizeVersionString(currentVersion)
	if err != nil {
		return false, fmt.Errorf("parse current: %w", err)
	}
	latestNormalized, err := version.NormalizeVersionString(latestVersion)
	if err != nil {
		return false, fmt.Errorf("parse latest: %w", err)
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

type updatePlan struct {
	Result  *Result
	Channel DownloadChannel
}

func CheckForUpdate(channel DownloadChannel, currentVersion string) (*Result, bool, error) {
	result := &Result{OldVersion: currentVersion}

	latest, err := fetchLatestVersionFunc(channel)
	if err != nil {
		return result, false, fmt.Errorf("check latest: %w", err)
	}
	result.NewVersion = latest

	needed, err := checkUpdateNeeded(currentVersion, latest)
	if err != nil {
		return result, false, fmt.Errorf("compare: %w", err)
	}

	return result, needed, nil
}

func ApplyConfirmedUpdate(channel DownloadChannel, currentVersion, targetVersion string) (*Result, error) {
	result := &Result{OldVersion: currentVersion, NewVersion: targetVersion}
	return applyUpdate(&updatePlan{Result: result, Channel: channel})
}

func applyUpdate(plan *updatePlan) (*Result, error) {
	result := plan.Result

	units := detectInstalledUnitsFunc()
	if len(units) == 0 {
		return result, fmt.Errorf("no installed services")
	}

	orch := &Orchestrator{
		DisableAndStop: disableAndStopFunc,
		EnableAndStart: enableAndStartFunc,
	}

	stopped := make([]string, 0, len(units))
	stopPhaseArmed := true
	defer recoverStoppedServicesOnPanic(orch, &stopped, &stopPhaseArmed)
	err := orch.StopServices(units, &stopped)
	if err != nil {
		if rollbackErr := orch.RestartStoppedServices(stopped); rollbackErr != nil {
			return result, fmt.Errorf("%w; %v", err, rollbackErr)
		}
		return result, err
	}
	result.Stopped = stopped
	tmpDir, err := osMkdirTempFunc("", "netsgo-update-*")
	if err != nil {
		if rollbackErr := orch.RestartStoppedServices(stopped); rollbackErr != nil {
			return result, errors.Join(fmt.Errorf("temp dir: %w", err), rollbackErr)
		}
		return result, fmt.Errorf("temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	started := make([]string, 0, len(units))
	originalBinary := filepath.Join(tmpDir, filepath.Base(installedBinaryPath)+".backup")
	backupAvailable := false
	defer recoverUpdateOrUpgradeOnPanic(orch, &started, &stopped, &originalBinary, &backupAvailable)
	stopPhaseArmed = false

	url := platformAssetURL(plan.Channel, result.NewVersion)
	newBinary := filepath.Join(tmpDir, "netsgo")
	if err := downloadAndExtractFunc(url, newBinary, downloadHTTPClient); err != nil {
		if rollbackErr := orch.RestartStoppedServices(stopped); rollbackErr != nil {
			return result, errors.Join(fmt.Errorf("download: %w", err), rollbackErr)
		}
		return result, fmt.Errorf("download: %w", err)
	}

	if err := replaceBinaryFunc(installedBinaryPath, originalBinary); err != nil {
		if rollbackErr := orch.RestartStoppedServices(stopped); rollbackErr != nil {
			return result, errors.Join(fmt.Errorf("backup: %w", err), rollbackErr)
		}
		return result, fmt.Errorf("backup: %w", err)
	}
	backupAvailable = true

	if err := replaceBinaryFunc(newBinary, installedBinaryPath); err != nil {
		rollbackErr := rollbackUpdateOrUpgrade(orch, nil, stopped, originalBinary, true)
		if rollbackErr != nil {
			return result, errors.Join(fmt.Errorf("replace: %w", err), rollbackErr)
		}
		return result, fmt.Errorf("replace: %w", err)
	}

	err = orch.StartServices(units, &started)
	if err != nil {
		rollbackErr := rollbackUpdateOrUpgrade(orch, started, stopped, originalBinary, true)
		if rollbackErr != nil {
			return result, errors.Join(err, rollbackErr)
		}
		return result, err
	}
	result.Started = started
	return result, nil
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
