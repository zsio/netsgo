package updater

import (
	"errors"
	"fmt"
	"netsgo/internal/svcmgr"
)

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
