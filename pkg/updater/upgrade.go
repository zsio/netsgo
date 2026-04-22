package updater

import (
	"fmt"
	"netsgo/internal/svcmgr"
)

func Upgrade(srcPath string) (*Result, error) {
	result := &Result{}

	units := installedUnits()
	if len(units) == 0 {
		return result, fmt.Errorf("no installed services")
	}

	orch := &Orchestrator{
		DisableAndStop: svcmgr.DisableAndStop,
		EnableAndStart: svcmgr.EnableAndStart,
	}

	stopped, err := orch.StopServices(units)
	if err != nil {
		return result, err
	}
	result.Stopped = stopped

	if err := replaceBinary(srcPath, svcmgr.BinaryPath); err != nil {
		return result, fmt.Errorf("replace: %w", err)
	}

	started, err := orch.StartServices(units)
	if err != nil {
		return result, err
	}
	result.Started = started
	return result, nil
}
