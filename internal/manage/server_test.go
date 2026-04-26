package manage

import (
	"strings"
	"testing"

	"netsgo/internal/svcmgr"
)

func newInstalledServerDeps(t *testing.T, ui *fakeUI) (serverDeps, svcmgr.ServiceLayout) {
	t.Helper()

	layout := svcmgr.NewLayout(svcmgr.RoleServer)

	return serverDeps{
		UI: ui,
		Inspect: func() svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: svcmgr.RoleServer, State: svcmgr.StateInstalled}
		},
		IsActive:  func() (bool, error) { return true, nil },
		IsEnabled: func() (bool, error) { return true, nil },
		Logs:      func() error { return nil },
		RunInstall: func() error {
			return nil
		},
		ReadServerEnv: func() (svcmgr.ServerEnv, error) {
			return svcmgr.ServerEnv{Port: 9527, TLSMode: "off", ServerAddr: "https://panel.example.com"}, nil
		},
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectClient: func() svcmgr.InstallState {
			return svcmgr.StateInstalled
		},
	}, layout
}

func TestManageServerInspectRedactsSensitiveData(t *testing.T) {
	ui := &fakeUI{selects: []int{1, 8}}
	deps, _ := newInstalledServerDeps(t, ui)

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) == 0 {
		t.Fatal("inspect should output a summary")
	}
	for _, row := range ui.summaries[0].rows {
		if strings.Contains(strings.ToLower(row[0]), "password") || strings.Contains(strings.ToLower(row[1]), "password") {
			t.Fatalf("inspect should not expose sensitive fields: %#v", ui.summaries[0].rows)
		}
	}
}

func TestManageServerUninstallKeepData(t *testing.T) {
	ui := &fakeUI{selects: []int{7, 0}, confirms: []bool{true}}
	removed := []string{}
	binaryRemoved := false
	deps, spec := newInstalledServerDeps(t, ui)
	deps.RemovePaths = func(paths ...string) error {
		removed = append(removed, paths...)
		return nil
	}
	deps.RemoveBinary = func() error {
		binaryRemoved = true
		return nil
	}

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	for _, path := range removed {
		if path == serverDataPath(spec) {
			t.Fatalf("keep-data mode should not remove the server data dir: %v", removed)
		}
	}
	if binaryRemoved {
		t.Fatal("should not remove the shared binary while the client is still installed")
	}
}

func TestManageServerRestart(t *testing.T) {
	ui := &fakeUI{selects: []int{5, 8}}
	stopped := false
	started := false
	deps, _ := newInstalledServerDeps(t, ui)
	deps.DisableAndStop = func() error { stopped = true; return nil }
	deps.EnableAndStart = func() error { started = true; return nil }

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if !stopped || !started {
		t.Fatalf("restart should stop before start, stopped=%v started=%v", stopped, started)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Operation successful" {
		t.Fatalf("restart should show a success message, got %#v", ui.summaries)
	}
}

func TestManageServerLogs(t *testing.T) {
	ui := &fakeUI{selects: []int{2}}
	called := false
	deps, _ := newInstalledServerDeps(t, ui)
	deps.Logs = func() error {
		called = true
		return nil
	}

	err := ManageServerWith(deps)
	if err != nil {
		t.Fatalf("logs should not fail: %v", err)
	}
	if !called {
		t.Fatal("logs should delegate to the journald execution function")
	}
}

func TestManageServerStartPrintsSuccess(t *testing.T) {
	ui := &fakeUI{selects: []int{3, 8}}
	deps, _ := newInstalledServerDeps(t, ui)

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) != 1 || ui.summaries[0].title != "Operation successful" {
		t.Fatalf("start should show a success message, got %#v", ui.summaries)
	}
}

func TestManageServerUninstallCancelPrintsCancelled(t *testing.T) {
	ui := &fakeUI{selects: []int{7, 0, 8}, confirms: []bool{false}}
	deps, _ := newInstalledServerDeps(t, ui)

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) != 2 || ui.summaries[1].title != "Cancelled" {
		t.Fatalf("canceling uninstall should show a canceled message, got %#v", ui.summaries)
	}
}

func TestManageServerUninstallLastRoleCanRemoveSharedBinary(t *testing.T) {
	ui := &fakeUI{selects: []int{7, 0}, confirms: []bool{true, true}}
	binaryRemoved := false
	deps, _ := newInstalledServerDeps(t, ui)
	deps.DetectClient = func() svcmgr.InstallState { return svcmgr.StateNotInstalled }
	deps.RemoveBinary = func() error {
		binaryRemoved = true
		return nil
	}

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if !binaryRemoved {
		t.Fatal("expected the last-role uninstall flow to allow removing the shared binary")
	}
}
