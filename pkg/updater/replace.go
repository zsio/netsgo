package updater

import (
	"errors"
	"fmt"
	"io"
	"netsgo/internal/svcmgr"
	"netsgo/pkg/fileutil"
	"os"
)

type Orchestrator struct {
	DisableAndStop func(unitName string) error
	EnableAndStart func(unitName string) error
}

var disableAndStopFunc = svcmgr.DisableAndStop
var enableAndStartFunc = svcmgr.EnableAndStart
var detectInstalledUnitsFunc = installedUnits
var replaceBinaryFunc = replaceBinary
var restoreBinaryFunc = restoreBinary
var repairServiceEnvFilesFunc = repairServiceEnvFiles
var newServiceLayoutFunc = svcmgr.NewLayout
var installedBinaryPath = svcmgr.BinaryPath

type serviceEnvSnapshot struct {
	unit   string
	layout svcmgr.ServiceLayout
	data   []byte
	perm   os.FileMode
}

func repairServiceEnvFiles(units []string) error {
	for _, unit := range units {
		layout, ok := serviceLayoutForUnit(unit)
		if !ok {
			continue
		}
		switch layout.Role {
		case svcmgr.RoleServer:
			if err := svcmgr.EnableServerLoopbackManagementHost(layout); err != nil {
				return fmt.Errorf("repair %s env: %w", unit, err)
			}
		case svcmgr.RoleClient:
			if err := svcmgr.RepairEnvFileOwnership(layout); err != nil {
				return fmt.Errorf("repair %s env: %w", unit, err)
			}
		}
	}
	return nil
}

func snapshotServiceEnvFiles(units []string) ([]serviceEnvSnapshot, error) {
	snapshots := make([]serviceEnvSnapshot, 0, len(units))
	for _, unit := range units {
		layout, ok := serviceLayoutForUnit(unit)
		if !ok {
			continue
		}
		data, err := os.ReadFile(layout.EnvPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("snapshot %s env: %w", unit, err)
		}
		info, err := os.Stat(layout.EnvPath)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s env metadata: %w", unit, err)
		}
		snapshots = append(snapshots, serviceEnvSnapshot{
			unit:   unit,
			layout: layout,
			data:   data,
			perm:   info.Mode().Perm(),
		})
	}
	return snapshots, nil
}

func restoreServiceEnvSnapshots(snapshots []serviceEnvSnapshot) error {
	var restoreErr error
	for _, snapshot := range snapshots {
		if err := fileutil.AtomicWriteFile(snapshot.layout.EnvPath, snapshot.data, snapshot.perm); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore %s env: %w", snapshot.unit, err))
			continue
		}
		if err := os.Chmod(snapshot.layout.EnvPath, snapshot.perm); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore %s env mode: %w", snapshot.unit, err))
		}
	}
	return restoreErr
}

func serviceLayoutForUnit(unit string) (svcmgr.ServiceLayout, bool) {
	switch unit {
	case svcmgr.UnitName(svcmgr.RoleServer):
		return newServiceLayoutFunc(svcmgr.RoleServer), true
	case svcmgr.UnitName(svcmgr.RoleClient):
		return newServiceLayoutFunc(svcmgr.RoleClient), true
	default:
		return svcmgr.ServiceLayout{}, false
	}
}

func (o *Orchestrator) StopServices(units []string, stopped *[]string) error {
	*stopped = (*stopped)[:0]
	for _, unit := range units {
		if err := o.DisableAndStop(unit); err != nil {
			return fmt.Errorf("stop %s: %w", unit, err)
		}
		*stopped = append(*stopped, unit)
	}
	return nil
}

func (o *Orchestrator) RestartStoppedServices(stopped []string) error {
	if len(stopped) == 0 {
		return nil
	}
	var restarted []string
	err := o.StartServices(stopped, &restarted)
	if err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	return nil
}

func (o *Orchestrator) StopStartedServices(started []string) error {
	for i := len(started) - 1; i >= 0; i-- {
		if err := o.DisableAndStop(started[i]); err != nil {
			return fmt.Errorf("rollback stop %s: %w", started[i], err)
		}
	}
	return nil
}

func (o *Orchestrator) StartServices(units []string, started *[]string) error {
	*started = (*started)[:0]
	for _, unit := range units {
		if err := o.EnableAndStart(unit); err != nil {
			return fmt.Errorf("start %s: %w", unit, err)
		}
		*started = append(*started, unit)
	}
	return nil
}

func replaceBinary(srcPath, dstPath string) error {
	tmpPath := dstPath + ".tmp"
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copy: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	cleanupTmp = false
	return nil
}

func restoreBinary(srcPath, dstPath string) error {
	return replaceBinary(srcPath, dstPath)
}
