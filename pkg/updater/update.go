package updater

import (
	"errors"
	"fmt"
	"os"

	"netsgo/internal/svcmgr"
)

func installedUnits() []string {
	var units []string
	if serviceFilesExist(svcmgr.NewLayout(svcmgr.RoleServer)) {
		units = append(units, svcmgr.UnitName(svcmgr.RoleServer))
	}
	if serviceFilesExist(svcmgr.NewLayout(svcmgr.RoleClient)) {
		units = append(units, svcmgr.UnitName(svcmgr.RoleClient))
	}
	return units
}

func serviceFilesExist(layout svcmgr.ServiceLayout) bool {
	if _, err := os.Stat(layout.UnitPath); err != nil {
		return false
	}
	if _, err := os.Stat(layout.EnvPath); err != nil {
		return false
	}
	return true
}

type Result struct {
	OldVersion string
	NewVersion string
	Stopped    []string
	Started    []string
}

func rollbackUpdateOrUpgrade(orch *Orchestrator, started, stopped []string, backupPath string, restoreBinary bool, envSnapshots []serviceEnvSnapshot) error {
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
	if len(envSnapshots) > 0 {
		if err := restoreServiceEnvSnapshots(envSnapshots); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
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

func recoverUpdateOrUpgradeOnPanic(orch *Orchestrator, started, stopped *[]string, backupPath *string, restoreBinary *bool, envSnapshots *[]serviceEnvSnapshot) {
	if r := recover(); r != nil {
		_ = rollbackUpdateOrUpgrade(orch, *started, *stopped, *backupPath, *restoreBinary, *envSnapshots)
		panic(r)
	}
}
