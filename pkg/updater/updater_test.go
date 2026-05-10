package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceBinary(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "netsgo")
	dstPath := filepath.Join(dstDir, "netsgo")

	_ = os.WriteFile(srcPath, []byte("new binary"), 0o755)
	_ = os.WriteFile(dstPath, []byte("old binary"), 0o755)

	err := replaceBinary(srcPath, dstPath)
	if err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	data, _ := os.ReadFile(dstPath)
	if string(data) != "new binary" {
		t.Fatalf("binary not replaced")
	}
}

func TestReplaceBinaryCleansTempFileOnRenameError(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "netsgo")
	dstPath := filepath.Join(dstDir, "existing-dir")

	if err := os.WriteFile(srcPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dstPath, 0o755); err != nil {
		t.Fatal(err)
	}

	err := replaceBinary(srcPath, dstPath)
	if err == nil {
		t.Fatal("expected rename error")
	}

	if _, err := os.Stat(dstPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected temp file to be cleaned up, stat err = %v", err)
	}
}

func TestUpgradeRestartsStoppedServicesWhenReplaceFails(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origReplaceBinary := replaceBinaryFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		replaceBinaryFunc = origReplaceBinary
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string {
		return []string{"netsgo-server.service"}
	}
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}
	replaceBinaryFunc = func(srcPath, dstPath string) error {
		return fmt.Errorf("replace failed")
	}

	_, err := Upgrade("/tmp/netsgo", "1.0.0", "1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(restarted) != 1 || restarted[0] != "netsgo-server.service" {
		t.Fatalf("expected service restart rollback, got %v", restarted)
	}
}

func TestUpgradeReturnsProvidedVersionFields(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	newPath := filepath.Join(tmpDir, "new-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error { return nil }

	result, err := Upgrade(newPath, "1.0.0", "1.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OldVersion != "1.0.0" || result.NewVersion != "1.1.0" {
		t.Fatalf("unexpected versions: old=%q new=%q", result.OldVersion, result.NewVersion)
	}
	if len(result.Stopped) != 1 || result.Stopped[0] != "netsgo-server.service" {
		t.Fatalf("unexpected stopped services: %v", result.Stopped)
	}
	if len(result.Started) != 1 || result.Started[0] != "netsgo-server.service" {
		t.Fatalf("unexpected started services: %v", result.Started)
	}
}

func TestUpgradeRestoresOldBinaryWhenStartFails(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	newPath := filepath.Join(tmpDir, "new-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error { return fmt.Errorf("start failed") }

	_, err := Upgrade(newPath, "1.0.0", "1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}

	data, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old binary" {
		t.Fatalf("expected old binary restored, got %q", string(data))
	}
}

func TestUpgradeStopsAlreadyStartedServicesBeforeRollback(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	newPath := filepath.Join(tmpDir, "new-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	var stoppedAgain []string
	var startCalls []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		stoppedAgain = append(stoppedAgain, unit)
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		startCalls = append(startCalls, unit)
		if unit == "netsgo-client.service" {
			return fmt.Errorf("start failed")
		}
		return nil
	}

	_, err := Upgrade(newPath, "1.0.0", "1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(startCalls) < 2 || startCalls[0] != "netsgo-server.service" || startCalls[1] != "netsgo-client.service" {
		t.Fatalf("unexpected start order: %v", startCalls)
	}
	if len(stoppedAgain) != 3 {
		t.Fatalf("expected original stop plus rollback stop, got %v", stoppedAgain)
	}
	if stoppedAgain[2] != "netsgo-server.service" {
		t.Fatalf("expected already-started service to be stopped during rollback, got %v", stoppedAgain)
	}
}

func TestUpgradeRestartsStoppedServicesWhenPanicOccurs(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origReplaceBinary := replaceBinaryFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		replaceBinaryFunc = origReplaceBinary
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}
	replaceBinaryFunc = func(srcPath, dstPath string) error {
		panic("replace panic")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 2 || restarted[0] != "netsgo-server.service" || restarted[1] != "netsgo-client.service" {
			t.Fatalf("expected stopped services to be restarted in rollback order, got %v", restarted)
		}
	}()

	_, _ = Upgrade("/tmp/netsgo", "1.0.0", "1.1.0")
}

func TestUpgradeRestartsOnlyPartiallyStoppedServicesWhenStopPanics(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		if unit == "netsgo-client.service" {
			panic("stop panic")
		}
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 1 || restarted[0] != "netsgo-server.service" {
			t.Fatalf("expected only fully stopped service to be restarted, got %v", restarted)
		}
	}()

	_, _ = Upgrade("/tmp/netsgo", "1.0.0", "1.1.0")
}

func TestUpgradeRollsBackWhenStartPanics(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	newPath := filepath.Join(tmpDir, "new-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	var stopCalls []string
	var startCalls []string
	var startCallCount int
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		stopCalls = append(stopCalls, unit)
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		startCallCount++
		startCalls = append(startCalls, unit)
		if startCallCount == 2 {
			panic("start panic")
		}
		return nil
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(stopCalls) != 3 || stopCalls[2] != "netsgo-server.service" {
			t.Fatalf("expected rollback to stop already-started service, got %v", stopCalls)
		}
		if len(startCalls) != 4 || startCalls[0] != "netsgo-server.service" || startCalls[1] != "netsgo-client.service" || startCalls[2] != "netsgo-server.service" || startCalls[3] != "netsgo-client.service" {
			t.Fatalf("expected restart sequence after panic rollback, got %v", startCalls)
		}
		data, err := os.ReadFile(installedPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "old binary" {
			t.Fatalf("expected old binary restored after panic, got %q", string(data))
		}
	}()

	_, _ = Upgrade(newPath, "1.0.0", "1.1.0")
}

func TestUpgradeRestartsStoppedServicesWhenPanicOccursInProtectionGap(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origMkdirTemp := osMkdirTempFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		osMkdirTempFunc = origMkdirTemp
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}
	osMkdirTempFunc = func(dir, pattern string) (string, error) {
		panic("gap panic")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 2 || restarted[0] != "netsgo-server.service" || restarted[1] != "netsgo-client.service" {
			t.Fatalf("expected stopped services to be restarted from protection gap, got %v", restarted)
		}
	}()

	_, _ = Upgrade("/tmp/netsgo", "1.0.0", "1.1.0")
}
