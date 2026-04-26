package main

import (
	"bytes"
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"netsgo/pkg/updater"
	"reflect"
	"strings"
	"testing"
)

func TestExtractInstalledVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain version", input: "netsgo version 1.2.3\n", want: "1.2.3"},
		{name: "version with metadata", input: "netsgo version 1.2.3 (abcdef1, 2026-04-25)\n", want: "1.2.3"},
		{name: "prerelease", input: "netsgo version v1.2.3-beta.1\n", want: "1.2.3-beta.1"},
		{name: "build metadata", input: "netsgo version 1.2.3+build.5 (abcdef1, 2026-04-25)\n", want: "1.2.3+build.5"},
		{name: "invalid", input: "netsgo version dev\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractInstalledVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("extractInstalledVersion(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("extractInstalledVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetInstalledVersionReportsErrors(t *testing.T) {
	_, err := getInstalledVersion()
	if err == nil {
		t.Fatal("expected error when installed binary is missing")
	}
	if !strings.Contains(err.Error(), "stat installed binary") {
		t.Fatalf("expected stat context, got %v", err)
	}
}

func TestRerunUpgradeWithSudoIfNeededUsesLookedUpPath(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"netsgo", "upgrade", "--force"}
	t.Cleanup(func() {
		os.Args = origArgs
	})

	execErr := errors.New("exec called")
	var gotPath string
	var gotArgv []string

	err := rerunUpgradeWithSudoIfNeeded(1000, func(file string) (string, error) {
		if file != "sudo" {
			t.Fatalf("expected sudo lookup, got %q", file)
		}
		return "/tmp/custom/sudo", nil
	}, func(argv0 string, argv []string, envv []string) error {
		gotPath = argv0
		gotArgv = append([]string(nil), argv...)
		return execErr
	})

	if !errors.Is(err, execErr) {
		t.Fatalf("expected exec error, got %v", err)
	}
	if gotPath != "/tmp/custom/sudo" {
		t.Fatalf("expected resolved sudo path, got %q", gotPath)
	}
	wantArgv := append([]string{"sudo"}, os.Args...)
	if !reflect.DeepEqual(gotArgv, wantArgv) {
		t.Fatalf("expected argv %v, got %v", wantArgv, gotArgv)
	}
}

func TestRerunUpgradeWithSudoIfNeededMissingSudoFailsClearly(t *testing.T) {
	calledExec := false
	err := rerunUpgradeWithSudoIfNeeded(1000, func(file string) (string, error) {
		if file != "sudo" {
			t.Fatalf("expected sudo lookup, got %q", file)
		}
		return "", exec.ErrNotFound
	}, func(argv0 string, argv []string, envv []string) error {
		calledExec = true
		return nil
	})

	if err == nil {
		t.Fatal("expected missing sudo error")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("expected wrapped exec.ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "sudo") || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("expected actionable sudo PATH error, got %v", err)
	}
	if calledExec {
		t.Fatal("exec should not run when sudo is missing")
	}
}

func TestIsDevVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "plain version", input: "1.2.3", want: false},
		{name: "prerelease", input: "v1.2.3-beta.1", want: false},
		{name: "version summary", input: "netsgo version 1.2.3", want: false},
		{name: "dev", input: "dev", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDevVersion(tt.input); got != tt.want {
				t.Fatalf("isDevVersion(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestReadConfirmation(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "yes with newline", input: "y\n", want: true},
		{name: "no with newline", input: "n\n", want: false},
		{name: "empty eof", input: "", want: false},
		{name: "yes on eof", input: "yes", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(tt.input))
			if got := readConfirmationFrom(reader); got != tt.want {
				t.Fatalf("readConfirmationFrom(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestReadConfirmationReadErrorReturnsFalse(t *testing.T) {
	reader := bufio.NewReader(errReader{})
	if got := readConfirmationFrom(reader); got {
		t.Fatal("expected readConfirmationFrom to return false on read error")
	}
}

func TestRunUpgradeCommand_UnknownInstalledVersionRequiresConfirmation(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	confirmationCalls := 0
	upgradeCalls := 0

	err := runUpgradeCommand(false, upgradeCommandDeps{
		installedUnits: func() []string {
			return []string{"netsgo-server.service"}
		},
		currentBinaryPath: func() (string, error) {
			return "/tmp/current-netsgo", nil
		},
		installedVersion: func() (string, error) {
			return "", errors.New("version lookup failed")
		},
		confirm: func() bool {
			confirmationCalls++
			return false
		},
		applyUpgrade: func(currentPath, installedVersion, targetVersion string) (*updater.Result, error) {
			upgradeCalls++
			return nil, nil
		},
		currentVersion: "0.1.0",
		stdout:         &stdout,
		stderr:         &stderr,
	})
	if err != nil {
		t.Fatalf("runUpgradeCommand returned error: %v", err)
	}
	if confirmationCalls != 1 {
		t.Fatalf("expected confirmation to be requested once, got %d", confirmationCalls)
	}
	if upgradeCalls != 0 {
		t.Fatalf("expected upgrade not to be called, got %d calls", upgradeCalls)
	}
	output := stdout.String()
	if !strings.Contains(output, "Warning: could not determine installed version: version lookup failed") {
		t.Fatalf("expected warning in output, got %q", output)
	}
	if !strings.Contains(output, "Aborted.") {
		t.Fatalf("expected aborted message in output, got %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestRunUpgradeCommand_ForceSkipsUnknownInstalledVersionConfirmation(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	confirmationCalls := 0
	upgradeCalls := 0
	var gotCurrentPath string
	var gotInstalledVersion string
	var gotTargetVersion string

	err := runUpgradeCommand(true, upgradeCommandDeps{
		installedUnits: func() []string {
			return []string{"netsgo-server.service"}
		},
		currentBinaryPath: func() (string, error) {
			return "/tmp/current-netsgo", nil
		},
		installedVersion: func() (string, error) {
			return "", errors.New("version lookup failed")
		},
		confirm: func() bool {
			confirmationCalls++
			return false
		},
		applyUpgrade: func(currentPath, installedVersion, targetVersion string) (*updater.Result, error) {
			upgradeCalls++
			gotCurrentPath = currentPath
			gotInstalledVersion = installedVersion
			gotTargetVersion = targetVersion
			return &updater.Result{
				Stopped: []string{"netsgo-server.service"},
				Started: []string{"netsgo-server.service"},
			}, nil
		},
		currentVersion: "0.1.0",
		stdout:         &stdout,
		stderr:         &stderr,
	})
	if err != nil {
		t.Fatalf("runUpgradeCommand returned error: %v", err)
	}
	if confirmationCalls != 0 {
		t.Fatalf("expected confirmation to be skipped, got %d calls", confirmationCalls)
	}
	if upgradeCalls != 1 {
		t.Fatalf("expected upgrade to be called once, got %d calls", upgradeCalls)
	}
	if gotCurrentPath != "/tmp/current-netsgo" || gotInstalledVersion != "" || gotTargetVersion != "0.1.0" {
		t.Fatalf("unexpected upgrade args: currentPath=%q installedVersion=%q targetVersion=%q", gotCurrentPath, gotInstalledVersion, gotTargetVersion)
	}
	output := stdout.String()
	if !strings.Contains(output, "Upgrading unknown -> 0.1.0") {
		t.Fatalf("expected upgrade banner in output, got %q", output)
	}
	if !strings.Contains(output, "Upgraded successfully.") {
		t.Fatalf("expected success message in output, got %q", output)
	}
	if !strings.Contains(output, "Stopped: [netsgo-server.service]") || !strings.Contains(output, "Started: [netsgo-server.service]") {
		t.Fatalf("expected service summary in output, got %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
