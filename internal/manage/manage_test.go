package manage

import (
	"errors"
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
	ui := &fakeUI{}
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
		UI:     &fakeUI{},
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
	})
	if err != nil {
		t.Fatalf("broken state should show guidance instead of failing: %v", err)
	}
	if called {
		t.Fatal("should not enter the normal manage server menu in broken state")
	}
}

func TestRunWithServerHistoricalDataOnlyNoInstalledRole(t *testing.T) {
	ui := &fakeUI{}
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
	})
	if err != nil {
		t.Fatalf("historical-data-only should show recovery guidance instead of failing: %v", err)
	}
	if called {
		t.Fatal("should not enter the normal manage server menu in historical-data-only state")
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Recoverable server data detected" {
		t.Fatalf("should show a historical data notice, got %#v", ui.summaries)
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
