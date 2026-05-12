package installmethod

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/updater"
)

type Detector struct {
	GOOS          string
	CurrentPID    int
	CurrentBinary func() (string, error)
	InstalledPath string
	BinaryMatches func(currentPath, installedPath string) bool
	IsContainer   func() bool
	SystemdUsable func() bool
	UnitInstalled func(svcmgr.Role) bool
	UnitMainPID   func(svcmgr.Role) (int, error)
}

func DefaultDetector() Detector {
	return Detector{
		GOOS:          runtime.GOOS,
		CurrentPID:    os.Getpid(),
		CurrentBinary: svcmgr.CurrentBinaryPath,
		InstalledPath: svcmgr.BinaryPath,
		BinaryMatches: binaryPathsMatch,
		IsContainer:   isContainerEnvironment,
		SystemdUsable: systemdUsable,
		UnitInstalled: func(role svcmgr.Role) bool {
			return svcmgr.Detect(role) == svcmgr.StateInstalled
		},
		UnitMainPID: systemdMainPID,
	}
}

func Detect(role svcmgr.Role) string {
	return DefaultDetector().Detect(role)
}

func (d Detector) Detect(role svcmgr.Role) string {
	if d.IsContainer != nil && d.IsContainer() {
		return updater.InstallMethodDocker
	}
	if d.GOOS == "" {
		d.GOOS = runtime.GOOS
	}
	if d.CurrentPID == 0 {
		d.CurrentPID = os.Getpid()
	}
	if d.GOOS != "linux" || d.SystemdUsable == nil || !d.SystemdUsable() {
		return updater.InstallMethodBinary
	}
	if d.UnitInstalled == nil || !d.UnitInstalled(role) {
		return updater.InstallMethodBinary
	}
	if d.CurrentBinary == nil {
		return updater.InstallMethodBinary
	}
	path, err := d.CurrentBinary()
	if err != nil {
		return updater.InstallMethodBinary
	}
	installedPath := d.InstalledPath
	if installedPath == "" {
		installedPath = svcmgr.BinaryPath
	}
	binaryMatches := d.BinaryMatches
	if binaryMatches == nil {
		binaryMatches = binaryPathsMatch
	}
	if !binaryMatches(path, installedPath) {
		return updater.InstallMethodBinary
	}
	if d.UnitMainPID == nil {
		return updater.InstallMethodBinary
	}
	mainPID, err := d.UnitMainPID(role)
	if err != nil || mainPID != d.CurrentPID {
		return updater.InstallMethodBinary
	}
	return updater.InstallMethodService
}

func binaryPathsMatch(currentPath, installedPath string) bool {
	if strings.TrimSpace(currentPath) == "" || strings.TrimSpace(installedPath) == "" {
		return false
	}

	currentInfo, currentErr := os.Stat(currentPath)
	installedInfo, installedErr := os.Stat(installedPath)
	if currentErr == nil && installedErr == nil && os.SameFile(currentInfo, installedInfo) {
		return true
	}

	currentResolved, currentErr := filepath.EvalSymlinks(currentPath)
	installedResolved, installedErr := filepath.EvalSymlinks(installedPath)
	if currentErr == nil && installedErr == nil {
		return filepath.Clean(currentResolved) == filepath.Clean(installedResolved)
	}

	return filepath.Clean(currentPath) == filepath.Clean(installedPath)
}

func isContainerEnvironment() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "docker") ||
		strings.Contains(lower, "kubepods") ||
		strings.Contains(lower, "containerd") ||
		strings.Contains(lower, "podman")
}
