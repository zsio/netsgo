package manage

import (
	"strings"
	"testing"

	"netsgo/internal/svcmgr"
)

func TestManageClientInspectRedactsKey(t *testing.T) {
	ui := &fakeUI{selects: []int{1}}
	err := ManageClientWith(clientDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadClientSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.ServiceSpec{}, nil },
		ReadClientEnv: func() (svcmgr.ClientEnv, error) {
			return svcmgr.ClientEnv{Server: "wss://panel.example.com", Key: "sk-secret", TLSSkipVerify: true}, nil
		},
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectServer:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("inspect should not fail: %v", err)
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
	err := ManageClientWith(clientDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadClientSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.NewSpec(svcmgr.RoleClient), nil },
		ReadClientEnv:  func() (svcmgr.ClientEnv, error) { return svcmgr.ClientEnv{}, nil },
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths: func(paths ...string) error {
			removed = append(removed, paths...)
			return nil
		},
		RemoveBinary: func() error { return nil },
		DetectServer: func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("client uninstall should not fail: %v", err)
	}
	found := false
	for _, path := range removed {
		if path == svcmgr.ManagedDataDir+"/client" {
			found = true
		}
	}
	if !found {
		t.Fatalf("client uninstall should remove the client data dir: %v", removed)
	}
}

func TestManageClientRestart(t *testing.T) {
	ui := &fakeUI{selects: []int{5}}
	stopped := false
	started := false
	err := ManageClientWith(clientDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadClientSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.ServiceSpec{}, nil },
		ReadClientEnv:  func() (svcmgr.ClientEnv, error) { return svcmgr.ClientEnv{}, nil },
		DisableAndStop: func() error { stopped = true; return nil },
		EnableAndStart: func() error { started = true; return nil },
		Logs:           func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectServer:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
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

func TestManageClientLogs(t *testing.T) {
	ui := &fakeUI{selects: []int{2}}
	called := false
	err := ManageClientWith(clientDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		Logs:           func() error { called = true; return nil },
		ReadClientSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.ServiceSpec{}, nil },
		ReadClientEnv:  func() (svcmgr.ClientEnv, error) { return svcmgr.ClientEnv{}, nil },
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectServer:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("logs should not fail: %v", err)
	}
	if !called {
		t.Fatal("logs should delegate to the journald execution function")
	}
}

func TestManageClientStartPrintsSuccess(t *testing.T) {
	ui := &fakeUI{selects: []int{3}}
	err := ManageClientWith(clientDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadClientSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.ServiceSpec{}, nil },
		ReadClientEnv:  func() (svcmgr.ClientEnv, error) { return svcmgr.ClientEnv{}, nil },
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectServer:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("start should not fail: %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Operation successful" {
		t.Fatalf("start should show a success message, got %#v", ui.summaries)
	}
}

func TestManageClientUninstallCancelPrintsCancelled(t *testing.T) {
	ui := &fakeUI{selects: []int{6}, confirms: []bool{false}}
	err := ManageClientWith(clientDeps{
		UI:             ui,
		Status:         func() (string, error) { return "", nil },
		ReadClientSpec: func() (svcmgr.ServiceSpec, error) { return svcmgr.NewSpec(svcmgr.RoleClient), nil },
		ReadClientEnv:  func() (svcmgr.ClientEnv, error) { return svcmgr.ClientEnv{}, nil },
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectServer:   func() svcmgr.InstallState { return svcmgr.StateInstalled },
	})
	if err != nil {
		t.Fatalf("canceling uninstall should not fail: %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Cancelled" {
		t.Fatalf("canceling uninstall should show a canceled message, got %#v", ui.summaries)
	}
}
