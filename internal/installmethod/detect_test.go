package installmethod

import (
	"os"
	"path/filepath"
	"testing"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/updater"
)

func TestDetectDockerWins(t *testing.T) {
	d := Detector{
		GOOS:       "linux",
		CurrentPID: 123,
		IsContainer: func() bool {
			return true
		},
	}
	if got := d.Detect(svcmgr.RoleServer); got != updater.InstallMethodDocker {
		t.Fatalf("Detect = %q, want docker", got)
	}
}

func TestDetectServiceRequiresMainPIDMatch(t *testing.T) {
	d := Detector{
		GOOS:       "linux",
		CurrentPID: 123,
		CurrentBinary: func() (string, error) {
			return svcmgr.BinaryPath, nil
		},
		IsContainer: func() bool { return false },
		SystemdUsable: func() bool {
			return true
		},
		UnitInstalled: func(role svcmgr.Role) bool {
			return role == svcmgr.RoleServer
		},
		UnitMainPID: func(role svcmgr.Role) (int, error) {
			return 123, nil
		},
	}
	if got := d.Detect(svcmgr.RoleServer); got != updater.InstallMethodService {
		t.Fatalf("Detect = %q, want service", got)
	}
}

func TestDetectManualBinaryAtInstalledPathIsBinary(t *testing.T) {
	d := Detector{
		GOOS:       "linux",
		CurrentPID: 123,
		CurrentBinary: func() (string, error) {
			return svcmgr.BinaryPath, nil
		},
		IsContainer:   func() bool { return false },
		SystemdUsable: func() bool { return true },
		UnitInstalled: func(role svcmgr.Role) bool { return true },
		UnitMainPID:   func(role svcmgr.Role) (int, error) { return 456, nil },
	}
	if got := d.Detect(svcmgr.RoleServer); got != updater.InstallMethodBinary {
		t.Fatalf("Detect = %q, want binary", got)
	}
}

func TestDetectServiceAcceptsEquivalentInstalledBinaryPath(t *testing.T) {
	dir := t.TempDir()
	realBinary := filepath.Join(dir, "releases", "netsgo")
	if err := os.MkdirAll(filepath.Dir(realBinary), 0o755); err != nil {
		t.Fatalf("failed to create release dir: %v", err)
	}
	if err := os.WriteFile(realBinary, []byte("netsgo"), 0o755); err != nil {
		t.Fatalf("failed to write binary: %v", err)
	}
	installedPath := filepath.Join(dir, "bin", "netsgo")
	if err := os.MkdirAll(filepath.Dir(installedPath), 0o755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}
	if err := os.Symlink(realBinary, installedPath); err != nil {
		t.Fatalf("failed to symlink installed binary: %v", err)
	}

	d := Detector{
		GOOS:          "linux",
		CurrentPID:    123,
		CurrentBinary: func() (string, error) { return realBinary, nil },
		InstalledPath: installedPath,
		IsContainer:   func() bool { return false },
		SystemdUsable: func() bool { return true },
		UnitInstalled: func(role svcmgr.Role) bool { return role == svcmgr.RoleServer },
		UnitMainPID:   func(role svcmgr.Role) (int, error) { return 123, nil },
	}

	if got := d.Detect(svcmgr.RoleServer); got != updater.InstallMethodService {
		t.Fatalf("Detect = %q, want service", got)
	}
}
