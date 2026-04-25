package updater

import (
	"fmt"
	"io"
	"netsgo/internal/svcmgr"
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
var installedBinaryPath = svcmgr.BinaryPath

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
