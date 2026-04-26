package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func Upgrade(srcPath, oldVersion, newVersion string) (*Result, error) {
	result := &Result{OldVersion: oldVersion, NewVersion: newVersion}
	var err error

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
	err = orch.StopServices(units, &stopped)
	if err != nil {
		if rollbackErr := orch.RestartStoppedServices(stopped); rollbackErr != nil {
			return result, fmt.Errorf("%w; %v", err, rollbackErr)
		}
		return result, err
	}
	result.Stopped = stopped

	tmpDir, err := osMkdirTempFunc("", "netsgo-upgrade-*")
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

	if err := replaceBinaryFunc(installedBinaryPath, originalBinary); err != nil {
		if rollbackErr := orch.RestartStoppedServices(stopped); rollbackErr != nil {
			return result, errors.Join(fmt.Errorf("backup: %w", err), rollbackErr)
		}
		return result, fmt.Errorf("backup: %w", err)
	}
	backupAvailable = true

	if err := replaceBinaryFunc(srcPath, installedBinaryPath); err != nil {
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
