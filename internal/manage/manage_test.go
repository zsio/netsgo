package manage

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"netsgo/internal/svcmgr"
)

type fakeUI struct {
	selects   []int
	confirms  []bool
	summaries []summaryCall
}

type summaryCall struct {
	title string
	rows  [][2]string
}

func assertSelectionExit(t *testing.T, err error) {
	t.Helper()
	if err != nil && !errors.Is(err, errReturnToSelection) {
		t.Fatalf("manage flow should exit cleanly, got %v", err)
	}
}

func (f *fakeUI) Select(prompt string, options []string) (int, error) {
	if len(f.selects) == 0 {
		return 0, errors.New("no select value")
	}
	v := f.selects[0]
	f.selects = f.selects[1:]
	return v, nil
}

func (f *fakeUI) Confirm(prompt string) (bool, error) {
	if len(f.confirms) == 0 {
		return true, nil
	}
	v := f.confirms[0]
	f.confirms = f.confirms[1:]
	return v, nil
}
func (f *fakeUI) PrintSummary(title string, rows [][2]string) {
	f.summaries = append(f.summaries, summaryCall{title: title, rows: rows})
}

func TestRunWithPlatformCheck(t *testing.T) {
	err := RunWith(Deps{GOOS: "darwin", HasTTY: true, UID: 0, UI: &fakeUI{}})
	if err == nil {
		t.Fatal("non-Linux platforms should fail")
	}
}

func TestRunWithTTYCheck(t *testing.T) {
	err := RunWith(Deps{GOOS: "linux", HasTTY: false, UID: 0, UI: &fakeUI{}})
	if err == nil {
		t.Fatal("non-TTY environments should fail")
	}
}

func TestRunWithNoInstalledRole(t *testing.T) {
	ui := &fakeUI{selects: []int{1}}
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     ui,
		Detect: func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
	})
	if err != nil {
		t.Fatalf("should not error when not installed: %v", err)
	}
	if len(ui.summaries) != 1 {
		t.Fatalf("should emit one summary when not installed, got %d", len(ui.summaries))
	}
}

func TestRunWithNoInstalledRoleCanEnterInstall(t *testing.T) {
	ui := &fakeUI{selects: []int{0}}
	called := false
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     ui,
		Detect: func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		RunInstall: func() error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("should not error when handing off to install: %v", err)
	}
	if !called {
		t.Fatal("should enter netsgo install from the no-installed menu")
	}
}

func TestRunWithRoleDispatch(t *testing.T) {
	called := ""
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     &fakeUI{selects: []int{0}},
		Detect: func(role svcmgr.Role) svcmgr.InstallState {
			if role == svcmgr.RoleServer {
				return svcmgr.StateInstalled
			}
			return svcmgr.StateNotInstalled
		},
		ManageServer: func() error {
			called = "server"
			return nil
		},
		ManageClient: func() error {
			called = "client"
			return nil
		},
		RunInstall:   func() error { return nil },
		UninstallAll: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("manage run should not fail: %v", err)
	}
	if called != "server" {
		t.Fatalf("should dispatch to server, got %q", called)
	}

	called = ""
	err = RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     &fakeUI{selects: []int{1}},
		Detect: func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateInstalled },
		ManageServer: func() error {
			called = "server"
			return nil
		},
		ManageClient: func() error {
			called = "client"
			return nil
		},
		RunInstall:   func() error { return nil },
		UninstallAll: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("manage run should not fail: %v", err)
	}
	if called != "client" {
		t.Fatalf("should dispatch to client, got %q", called)
	}
}

func TestRunWithServerBrokenNoInstalledRole(t *testing.T) {
	called := false
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     &fakeUI{selects: []int{0}},
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			if role == svcmgr.RoleServer {
				return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"missing env file"}}
			}
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateNotInstalled}
		},
		ManageServer: func() error {
			called = true
			return nil
		},
		ManageClient: func() error { return nil },
		RunInstall:   func() error { return nil },
	})
	if err != nil {
		t.Fatalf("broken state should offer an explicit recovery path: %v", err)
	}
	if !called {
		t.Fatal("should allow entering the broken server recovery flow")
	}
}

func TestRunWithServerHistoricalDataOnlyNoInstalledRole(t *testing.T) {
	ui := &fakeUI{selects: []int{1}}
	called := false
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			if role == svcmgr.RoleServer {
				return svcmgr.InstallInspection{Role: role, State: svcmgr.StateHistoricalDataOnly}
			}
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateNotInstalled}
		},
		ManageServer: func() error {
			called = true
			return nil
		},
		RunInstall:   func() error { return nil },
		ManageClient: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("historical-data-only should allow entering install from recovery: %v", err)
	}
	if called {
		t.Fatal("this test chooses install directly, so the recovery manage flow should not be entered")
	}
}

func TestRunWithClientInstalledAndServerBrokenWarnsThenManagesClient(t *testing.T) {
	ui := &fakeUI{}
	called := ""
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			if role == svcmgr.RoleServer {
				return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"missing binary"}}
			}
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateInstalled}
		},
		ManageClient: func() error {
			called = "client"
			return nil
		},
		RunInstall: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("should still allow entering client management when a healthy client exists: %v", err)
	}
	if called != "client" {
		t.Fatalf("should continue into client management, got %q", called)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Server installation is in an abnormal state" {
		t.Fatalf("should first show the server problem notice, got %#v", ui.summaries)
	}
}

func TestRunWithBothInstalledCanDispatchBulkUninstall(t *testing.T) {
	ui := &fakeUI{selects: []int{2, 1}}
	called := false
	serverState := svcmgr.StateInstalled
	clientState := svcmgr.StateInstalled
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     ui,
		Detect: func(role svcmgr.Role) svcmgr.InstallState {
			if role == svcmgr.RoleServer {
				return serverState
			}
			return clientState
		},
		ManageServer: func() error { return nil },
		ManageClient: func() error { return nil },
		RunInstall:   func() error { return nil },
		UninstallAll: func() error {
			called = true
			serverState = svcmgr.StateNotInstalled
			clientState = svcmgr.StateNotInstalled
			return errReturnToSelection
		},
	})
	if err != nil {
		t.Fatalf("bulk uninstall dispatch should not fail: %v", err)
	}
	if !called {
		t.Fatal("expected uninstall-all to be callable from the dual-installed menu")
	}
}

func TestRunWithNonRootReexecsUsingLookedUpSudo(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"netsgo", "manage"}
	t.Cleanup(func() {
		os.Args = origArgs
	})

	execErr := errors.New("exec called")
	var gotPath string
	var gotArgv []string

	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    1000,
		LookPath: func(file string) (string, error) {
			if file != "sudo" {
				t.Fatalf("expected sudo lookup, got %q", file)
			}
			return "/tmp/custom/sudo", nil
		},
		Exec: func(argv0 string, argv []string, envv []string) error {
			gotPath = argv0
			gotArgv = append([]string(nil), argv...)
			return execErr
		},
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

func TestRunWithNonRootMissingSudoFailsClearly(t *testing.T) {
	calledExec := false
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    1000,
		LookPath: func(file string) (string, error) {
			if file != "sudo" {
				t.Fatalf("expected sudo lookup, got %q", file)
			}
			return "", exec.ErrNotFound
		},
		Exec: func(argv0 string, argv []string, envv []string) error {
			calledExec = true
			return nil
		},
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
