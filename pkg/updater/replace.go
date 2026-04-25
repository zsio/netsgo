package updater

import (
	"fmt"
	"io"
	"os"
)

type Orchestrator struct {
	DisableAndStop func(unitName string) error
	EnableAndStart func(unitName string) error
}

func (o *Orchestrator) StopServices(units []string) ([]string, error) {
	stopped := make([]string, 0, len(units))
	for _, unit := range units {
		if err := o.DisableAndStop(unit); err != nil {
			return stopped, fmt.Errorf("stop %s: %w", unit, err)
		}
		stopped = append(stopped, unit)
	}
	return stopped, nil
}

func (o *Orchestrator) StartServices(units []string) ([]string, error) {
	started := make([]string, 0, len(units))
	for _, unit := range units {
		if err := o.EnableAndStart(unit); err != nil {
			return started, fmt.Errorf("start %s: %w", unit, err)
		}
		started = append(started, unit)
	}
	return started, nil
}

func replaceBinary(srcPath, dstPath string) error {
	tmpPath := dstPath + ".tmp"

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
	return nil
}
