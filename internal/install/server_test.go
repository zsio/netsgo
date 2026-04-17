package install

import (
	"errors"
	"testing"

	"netsgo/internal/server"
	"netsgo/internal/svcmgr"
)

func TestInstallServerWithAlreadyInstalled(t *testing.T) {
	ui := &fakeUI{}
	called := false
	err := InstallServerWith(serverDeps{
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
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Server already installed" {
		t.Fatalf("expected 'Server already installed' summary, got %#v", ui.summaries)
	}
}

func TestInstallServerWithHistoricalDataSkipsInit(t *testing.T) {
	ui := &fakeUI{
		inputs:   []string{"9527", "127.0.0.1/32"},
		confirms: []bool{true, true},
	}
	applyInitCalled := false
	writeSpecCalled := false
	err := InstallServerWith(serverDeps{
		UI:            ui,
		Detect:        func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateHistoricalDataOnly },
		SelectTLSMode: func(ui uiProvider) (string, error) { return "off", nil },
		LoadRecoverable: func() (server.InitParams, error) {
			return server.InitParams{ServerAddr: "https://panel.example.com", AllowedPorts: "10000-11000"}, nil
		},
		EnsureUser: func(name string) error { return nil },
		EnsureDirs: func() error { return nil },
		ApplyInit: func(dataDir string, params server.InitParams) error {
			applyInitCalled = true
			return nil
		},
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteServerSpec: func(spec svcmgr.ServiceSpec) error {
			writeSpecCalled = true
			return nil
		},
		WriteServerEnv:  func(spec svcmgr.ServiceSpec, env svcmgr.ServerEnv) error { return nil },
		WriteServerUnit: func(spec svcmgr.ServiceSpec) error { return nil },
		ValidateCustomTLS: func(certPath, keyPath string) error {
			return nil
		},
		DaemonReload:   func() error { return nil },
		EnableAndStart: func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("historical data recovery install should not error: %v", err)
	}
	if applyInitCalled {
		t.Fatal("historical data recovery install should not call ApplyInit again")
	}
	if !writeSpecCalled {
		t.Fatal("historical data recovery install should still write spec/env/unit")
	}
	if len(ui.summaries) != 3 {
		t.Fatalf("expected 3 summaries (historical data, install confirm, complete), got %d", len(ui.summaries))
	}
	if ui.summaries[0].title != "Recoverable server data detected" {
		t.Fatalf("expected first summary to be 'Recoverable server data detected', got %#v", ui.summaries)
	}
	if ui.summaries[2].title != "Server installation complete" {
		t.Fatalf("expected last summary to be 'Server installation complete', got %#v", ui.summaries)
	}
}

func TestInstallServerWithHistoricalDataDeclineReuseStopsInstall(t *testing.T) {
	ui := &fakeUI{
		inputs:   []string{"9527", "127.0.0.1/32"},
		confirms: []bool{false},
	}
	writeSpecCalled := false
	err := InstallServerWith(serverDeps{
		UI:            ui,
		Detect:        func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateHistoricalDataOnly },
		SelectTLSMode: func(ui uiProvider) (string, error) { return "off", nil },
		LoadRecoverable: func() (server.InitParams, error) {
			return server.InitParams{ServerAddr: "https://panel.example.com", AllowedPorts: "10000-11000"}, nil
		},
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		ApplyInit:         func(dataDir string, params server.InitParams) error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteServerSpec: func(spec svcmgr.ServiceSpec) error {
			writeSpecCalled = true
			return nil
		},
		WriteServerEnv:  func(spec svcmgr.ServiceSpec, env svcmgr.ServerEnv) error { return nil },
		WriteServerUnit: func(spec svcmgr.ServiceSpec) error { return nil },
		DaemonReload:    func() error { return nil },
		EnableAndStart:  func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("declining historical data reuse should not error: %v", err)
	}
	if writeSpecCalled {
		t.Fatal("should not continue install after declining historical data")
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "Installation cancelled" {
		t.Fatalf("should show cancellation summary after declining, got %#v", ui.summaries)
	}
}

func TestInstallServerWithCustomTLSCollectsCertAndKey(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"9527", "127.0.0.1/32", "/tmp/cert.pem", "/tmp/key.pem", "https://panel.example.com", "admin", "10000-11000"},
		passwords: []string{"Password123", "Password123"},
		confirms:  []bool{true},
	}
	var writtenEnv svcmgr.ServerEnv
	err := InstallServerWith(serverDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		SelectTLSMode:     func(ui uiProvider) (string, error) { return "custom", nil },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		ApplyInit:         func(dataDir string, params server.InitParams) error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteServerSpec:   func(spec svcmgr.ServiceSpec) error { return nil },
		WriteServerEnv: func(spec svcmgr.ServiceSpec, env svcmgr.ServerEnv) error {
			writtenEnv = env
			return nil
		},
		WriteServerUnit: func(spec svcmgr.ServiceSpec) error { return nil },
		ValidateCustomTLS: func(certPath, keyPath string) error {
			return nil
		},
		DaemonReload:   func() error { return nil },
		EnableAndStart: func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("custom TLS install should not error: %v", err)
	}
	if writtenEnv.TLSCert != "/tmp/cert.pem" || writtenEnv.TLSKey != "/tmp/key.pem" {
		t.Fatalf("custom TLS should write cert/key, got %#v", writtenEnv)
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "Server installation complete" {
		t.Fatalf("should show completion summary after successful install, got %#v", ui.summaries)
	}
}

func TestInstallServerWithBrokenStateFails(t *testing.T) {
	ui := &fakeUI{}
	err := InstallServerWith(serverDeps{
		UI: ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"missing unit file"}}
		},
	})
	if err == nil {
		t.Fatal("broken state should fail")
	}
	if !errors.Is(err, errInstallBrokenState) {
		t.Fatalf("broken state should return errInstallBrokenState, got %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Server installation state is broken" {
		t.Fatalf("broken state should show problem summary, got %#v", ui.summaries)
	}
}

func TestInstallServerWithConfirmNoPrintsCancelledSummary(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"9527", "127.0.0.1/32", "https://panel.example.com", "admin", "10000-11000"},
		passwords: []string{"Password123", "Password123"},
		confirms:  []bool{false},
	}
	err := InstallServerWith(serverDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		SelectTLSMode:     func(ui uiProvider) (string, error) { return "off", nil },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		ApplyInit:         func(dataDir string, params server.InitParams) error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteServerSpec:   func(spec svcmgr.ServiceSpec) error { return nil },
		WriteServerEnv:    func(spec svcmgr.ServiceSpec, env svcmgr.ServerEnv) error { return nil },
		WriteServerUnit:   func(spec svcmgr.ServiceSpec) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("cancelling install should not error: %v", err)
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "Installation cancelled" {
		t.Fatalf("should show cancellation summary after declining, got %#v", ui.summaries)
	}
}
