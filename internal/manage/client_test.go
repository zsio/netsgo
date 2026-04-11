package manage

import (
	"path/filepath"
	"strings"
	"testing"

	"netsgo/internal/svcmgr"
)

func newInstalledClientDeps(t *testing.T, ui *fakeUI) (clientDeps, svcmgr.ServiceSpec) {
	t.Helper()

	spec := svcmgr.NewSpec(svcmgr.RoleClient)
	spec.DataDir = t.TempDir()
	spec.UnitPath = filepath.Join(spec.DataDir, "netsgo-client.service")
	spec.EnvPath = filepath.Join(spec.DataDir, "client.env")
	spec.SpecPath = filepath.Join(spec.DataDir, "client.json")
	spec.ServerURL = "wss://panel.example.com"

	return clientDeps{
		UI: ui,
		Inspect: func() svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: svcmgr.RoleClient, State: svcmgr.StateInstalled}
		},
		IsActive:  func() (bool, error) { return true, nil },
		IsEnabled: func() (bool, error) { return true, nil },
		Logs:      func() error { return nil },
		RunInstall: func() error {
			return nil
		},
		ReadClientSpec: func() (svcmgr.ServiceSpec, error) { return spec, nil },
		ReadClientEnv: func() (svcmgr.ClientEnv, error) {
			return svcmgr.ClientEnv{
				Server:         spec.ServerURL,
				Key:            "sk-secret",
				TLSSkipVerify:  true,
				TLSFingerprint: "sha256:example",
			}, nil
		},
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectServer: func() svcmgr.InstallState {
			return svcmgr.StateInstalled
		},
	}, spec
}

func TestManageClientInspectRedactsKey(t *testing.T) {
	ui := &fakeUI{selects: []int{1, 7}}
	deps, _ := newInstalledClientDeps(t, ui)

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) == 0 {
		t.Fatal("inspect should output a summary")
	}
	for _, row := range ui.summaries[0].rows {
		if strings.Contains(strings.ToLower(row[0]), "key") || strings.Contains(strings.ToLower(row[1]), "sk-") {
			t.Fatalf("inspect should not expose the client key: %#v", ui.summaries[0].rows)
		}
	}
}

func TestManageClientUninstallRemovesData(t *testing.T) {
	ui := &fakeUI{selects: []int{6}, confirms: []bool{true}}
	removed := []string{}
	deps, spec := newInstalledClientDeps(t, ui)
	deps.RemovePaths = func(paths ...string) error {
		removed = append(removed, paths...)
		return nil
	}

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	found := false
	for _, path := range removed {
		if path == clientDataPath(spec) {
			found = true
		}
	}
	if !found {
		t.Fatalf("client uninstall should remove the client data dir: %v", removed)
	}
}

func TestManageClientRestart(t *testing.T) {
	ui := &fakeUI{selects: []int{5, 7}}
	stopped := false
	started := false
	deps, _ := newInstalledClientDeps(t, ui)
	deps.DisableAndStop = func() error { stopped = true; return nil }
	deps.EnableAndStart = func() error { started = true; return nil }

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if !stopped || !started {
		t.Fatalf("restart should stop before start, stopped=%v started=%v", stopped, started)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Operation successful" {
		t.Fatalf("restart should show a success message, got %#v", ui.summaries)
	}
}

func TestManageClientLogs(t *testing.T) {
	ui := &fakeUI{selects: []int{2}}
	called := false
	deps, _ := newInstalledClientDeps(t, ui)
	deps.Logs = func() error {
		called = true
		return nil
	}

	err := ManageClientWith(deps)
	if err != nil {
		t.Fatalf("logs should not fail: %v", err)
	}
	if !called {
		t.Fatal("logs should delegate to the journald execution function")
	}
}

func TestManageClientStartPrintsSuccess(t *testing.T) {
	ui := &fakeUI{selects: []int{3, 7}}
	deps, _ := newInstalledClientDeps(t, ui)

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) != 1 || ui.summaries[0].title != "Operation successful" {
		t.Fatalf("start should show a success message, got %#v", ui.summaries)
	}
}

func TestManageClientUninstallCancelPrintsCancelled(t *testing.T) {
	ui := &fakeUI{selects: []int{6, 7}, confirms: []bool{false}}
	deps, _ := newInstalledClientDeps(t, ui)

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) != 2 || ui.summaries[1].title != "Cancelled" {
		t.Fatalf("canceling uninstall should show a canceled message, got %#v", ui.summaries)
	}
}

func TestManageClientUninstallLastRoleCanKeepSharedBinary(t *testing.T) {
	ui := &fakeUI{selects: []int{6}, confirms: []bool{true, false}}
	binaryRemoved := false
	deps, _ := newInstalledClientDeps(t, ui)
	deps.DetectServer = func() svcmgr.InstallState { return svcmgr.StateNotInstalled }
	deps.RemoveBinary = func() error {
		binaryRemoved = true
		return nil
	}

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if binaryRemoved {
		t.Fatal("expected the final shared-binary confirmation to allow keeping the binary")
	}
}
