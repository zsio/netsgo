package updater

import (
	"fmt"
	"netsgo/internal/svcmgr"
	"netsgo/pkg/version"
	"os"
	"path/filepath"
)

func checkUpdateNeeded(currentVersion, latestVersion string) (bool, error) {
	current, err := version.ParseSemver(currentVersion)
	if err != nil {
		return false, fmt.Errorf("parse current: %w", err)
	}
	latest, err := version.ParseSemver(latestVersion)
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

func AutoUpdate(channel DownloadChannel, currentVersion string) (*Result, error) {
	result := &Result{OldVersion: currentVersion}

	latest, err := fetchLatestVersion()
	if err != nil {
		return result, fmt.Errorf("check latest: %w", err)
	}
	result.NewVersion = latest

	needed, err := checkUpdateNeeded(currentVersion, latest)
	if err != nil {
		return result, fmt.Errorf("compare: %w", err)
	}
	if !needed {
		return result, nil
	}

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

	tmpDir, err := os.MkdirTemp("", "netsgo-update-*")
	if err != nil {
		return result, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	url := platformAssetURL(channel, latest)
	newBinary := filepath.Join(tmpDir, "netsgo")
	if err := downloadAndExtract(url, newBinary, defaultHTTPClient); err != nil {
		return result, fmt.Errorf("download: %w", err)
	}

	if err := replaceBinary(newBinary, svcmgr.BinaryPath); err != nil {
		return result, fmt.Errorf("replace: %w", err)
	}

	started, err := orch.StartServices(units)
	if err != nil {
		return result, err
	}
	result.Started = started
	return result, nil
}
