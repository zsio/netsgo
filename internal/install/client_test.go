package install

import (
	"errors"
	"testing"

	"netsgo/internal/svcmgr"
)

func TestInstallClientWithAlreadyInstalled(t *testing.T) {
	ui := &fakeUI{}
	called := false
	err := InstallClientWith(clientDeps{
		UI:     ui,
		Detect: func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateInstalled },
		InstallBinary: func(src string) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("should not error when already installed: %v", err)
	}
	if called {
		t.Fatal("should not continue install when already installed")
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client already installed" {
		t.Fatalf("should show next-step summary when already installed, got %#v", ui.summaries)
	}
}

func TestInstallClientWithBrokenStateFails(t *testing.T) {
	ui := &fakeUI{}
	err := InstallClientWith(clientDeps{
		UI: ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"missing env file"}}
		},
	})
	if err == nil {
		t.Fatal("broken state should fail")
	}
	if !errors.Is(err, errInstallBrokenState) {
		t.Fatalf("broken state should return errInstallBrokenState, got %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client installation state is broken" {
		t.Fatalf("broken state should show problem summary, got %#v", ui.summaries)
	}
}

func TestInstallClientWithHistoricalDataOnlyFailsWithReauthMessage(t *testing.T) {
	ui := &fakeUI{}
	err := InstallClientWith(clientDeps{
		UI: ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateHistoricalDataOnly, Problems: []string{"residual runtime data directory still exists: /var/lib/netsgo/client"}}
		},
	})
	if err == nil {
		t.Fatal("client historical data residual should refuse install")
	}
	if !errors.Is(err, errInstallBrokenState) {
		t.Fatalf("client historical data residual should return errInstallBrokenState, got %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client installation state is broken" {
		t.Fatalf("client historical data should show broken state summary, got %#v", ui.summaries)
	}
	rows := ui.summaries[0].rows
	foundAdvice := false
	for _, row := range rows {
		if row[0] == "Advice" && row[1] == "Client does not support recovering existing data; clear residual data and reinstall" {
			foundAdvice = true
			break
		}
	}
	if !foundAdvice {
		t.Fatalf("client historical data should advise re-authentication, got %#v", rows)
	}
}

func TestInstallClientWithFreshInstall(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"wss://panel.example.com", "AA:BB:CC"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true, true},
	}
	writeEnvCalled := false
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ClientEnv) error { writeEnvCalled = true; return nil },
		WriteClientUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("fresh client install should not error: %v", err)
	}
	if !writeEnvCalled {
		t.Fatal("fresh client install should write env/unit")
	}
	if len(ui.summaries) != 2 {
		t.Fatalf("should show confirmation and completion summaries, got %d", len(ui.summaries))
	}
	if ui.summaries[1].title != "Client installation complete" {
		t.Fatalf("expected 'Client installation complete' summary, got %#v", ui.summaries)
	}
}

func TestInstallClientWithEnsureDirs(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"wss://panel.example.com", ""},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{false, true},
	}
	ensureDirsCalled := false
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { ensureDirsCalled = true; return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ClientEnv) error { return nil },
		WriteClientUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("client install should not error: %v", err)
	}
	if !ensureDirsCalled {
		t.Fatal("client install should create required directories")
	}
}

func TestInstallClientWithConfirmNoShowsCancelledSummary(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"wss://panel.example.com", ""},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{false, false},
	}
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ClientEnv) error { return nil },
		WriteClientUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("cancelling install should not error: %v", err)
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "Installation cancelled" {
		t.Fatalf("expected 'Installation cancelled' summary after declining, got %#v", ui.summaries)
	}
}
