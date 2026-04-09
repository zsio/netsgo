package manage

import (
	"strings"
	"testing"

	"netsgo/internal/svcmgr"
)

func TestManageServerInspectRedactsSensitiveData(t *testing.T) {
	ui := &fakeUI{selects: []int{1}}
	err := ManageServerWith(serverDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadServerSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.ServiceSpec{}, nil },
		ReadServerEnv: func() (svcmgr.ServerEnv, error) {
			return svcmgr.ServerEnv{Port: 9527, TLSMode: "off", ServerAddr: "https://panel.example.com"}, nil
		},
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectClient:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("inspect should not fail: %v", err)
	}
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
	ui := &fakeUI{selects: []int{6, 0}, confirms: []bool{true}}
	removed := []string{}
	binaryRemoved := false
	err := ManageServerWith(serverDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadServerSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.NewSpec(svcmgr.RoleServer), nil },
		ReadServerEnv:  func() (svcmgr.ServerEnv, error) { return svcmgr.ServerEnv{}, nil },
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths: func(paths ...string) error {
			removed = append(removed, paths...)
			return nil
		},
		RemoveBinary: func() error {
			binaryRemoved = true
			return nil
		},
		DetectClient: func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("uninstall keep-data should not fail: %v", err)
	}
	for _, path := range removed {
		if path == svcmgr.ManagedDataDir+"/server" {
			t.Fatalf("keep-data mode should not remove the server data dir: %v", removed)
		}
	}
	if binaryRemoved {
		t.Fatal("should not remove the shared binary while the client is still installed")
	}
}

func TestManageServerRestart(t *testing.T) {
	ui := &fakeUI{selects: []int{5}}
	stopped := false
	started := false
	err := ManageServerWith(serverDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadServerSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.ServiceSpec{}, nil },
		ReadServerEnv:  func() (svcmgr.ServerEnv, error) { return svcmgr.ServerEnv{}, nil },
		DisableAndStop: func() error { stopped = true; return nil },
		EnableAndStart: func() error { started = true; return nil },
		Logs:           func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectClient:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("restart should not fail: %v", err)
	}
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
	err := ManageServerWith(serverDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		Logs:           func() error { called = true; return nil },
		ReadServerSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.ServiceSpec{}, nil },
		ReadServerEnv:  func() (svcmgr.ServerEnv, error) { return svcmgr.ServerEnv{}, nil },
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectClient:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("logs should not fail: %v", err)
	}
	if !called {
		t.Fatal("logs should delegate to the journald execution function")
	}
}

func TestManageServerStartPrintsSuccess(t *testing.T) {
	ui := &fakeUI{selects: []int{3}}
	err := ManageServerWith(serverDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadServerSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.ServiceSpec{}, nil },
		ReadServerEnv:  func() (svcmgr.ServerEnv, error) { return svcmgr.ServerEnv{}, nil },
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectClient:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("start should not fail: %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Operation successful" {
		t.Fatalf("start should show a success message, got %#v", ui.summaries)
	}
}

func TestManageServerUninstallCancelPrintsCancelled(t *testing.T) {
	ui := &fakeUI{selects: []int{6, 0}, confirms: []bool{false}}
	err := ManageServerWith(serverDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadServerSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.NewSpec(svcmgr.RoleServer), nil },
		ReadServerEnv:  func() (svcmgr.ServerEnv, error) { return svcmgr.ServerEnv{}, nil },
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectClient:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("canceling uninstall should not fail: %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Cancelled" {
		t.Fatalf("canceling uninstall should show a canceled message, got %#v", ui.summaries)
	}
}
