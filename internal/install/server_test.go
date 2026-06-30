package install

import (
	"errors"
	"strings"
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
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Server 已安装" {
		t.Fatalf("expected 'Server 已安装' summary, got %#v", ui.summaries)
	}
}

func TestInstallServerWithHistoricalDataSkipsInit(t *testing.T) {
	ui := &fakeUI{
		inputs:   []string{"9527", "127.0.0.1/32"},
		confirms: []bool{true, true},
	}
	applyInitCalled := false
	writeEnvCalled := false
	err := InstallServerWith(serverDeps{
		UI:            ui,
		Detect:        func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateHistoricalDataOnly },
		SelectTLSMode: func(ui uiProvider) (string, error) { return "off", nil },
		LoadRecoverable: func() (server.InitParams, error) {
			return server.InitParams{ServerAddr: "https://panel.example.com"}, nil
		},
		EnsureUser: func(name string) error { return nil },
		EnsureDirs: func() error { return nil },
		ApplyInit: func(dataDir string, params server.InitParams) error {
			applyInitCalled = true
			return nil
		},
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteServerEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ServerEnv) error { writeEnvCalled = true; return nil },
		WriteServerUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
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
	if !writeEnvCalled {
		t.Fatal("historical data recovery install should still write env/unit")
	}
	if len(ui.summaries) != 3 {
		t.Fatalf("expected 3 summaries (historical data, install confirm, complete), got %d", len(ui.summaries))
	}
	if ui.summaries[0].title != "检测到可恢复的 server 数据" {
		t.Fatalf("expected first summary to be '检测到可恢复的 server 数据', got %#v", ui.summaries)
	}
	if ui.summaries[2].title != "Server 安装完成" {
		t.Fatalf("expected last summary to be 'Server 安装完成', got %#v", ui.summaries)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "使用现有数据继续安装？")
	assertConfirmPrompt(t, ui.confirmCalls, "继续安装？")
}

func TestInstallServerWithHistoricalDataDeclineReuseStopsInstall(t *testing.T) {
	ui := &fakeUI{
		inputs:   []string{"9527", "127.0.0.1/32"},
		confirms: []bool{false},
	}
	writeEnvCalled := false
	err := InstallServerWith(serverDeps{
		UI:            ui,
		Detect:        func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateHistoricalDataOnly },
		SelectTLSMode: func(ui uiProvider) (string, error) { return "off", nil },
		LoadRecoverable: func() (server.InitParams, error) {
			return server.InitParams{ServerAddr: "https://panel.example.com"}, nil
		},
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		ApplyInit:         func(dataDir string, params server.InitParams) error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteServerEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ServerEnv) error { writeEnvCalled = true; return nil },
		WriteServerUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("declining historical data reuse should not error: %v", err)
	}
	if writeEnvCalled {
		t.Fatal("should not continue install after declining historical data")
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "安装已取消" {
		t.Fatalf("should show cancellation summary after declining, got %#v", ui.summaries)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "使用现有数据继续安装？")
}

func TestInstallServerWithCustomTLSCollectsCertAndKey(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"9527", "127.0.0.1/8", "/tmp/cert.pem", "/tmp/key.pem", "https://panel.example.com", "admin", "1024-65535"},
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
		WriteServerEnv: func(layout svcmgr.ServiceLayout, env svcmgr.ServerEnv) error {
			writtenEnv = env
			return nil
		},
		WriteServerUnit: func(layout svcmgr.ServiceLayout) error { return nil },
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
	if !writtenEnv.AllowLoopbackManagementHost {
		t.Fatal("server install should allow loopback management Host fallback by default")
	}
	if !writtenEnv.AllowLoopbackManagementHostDefined {
		t.Fatal("server install should explicitly define loopback management Host fallback")
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "Server 安装完成" {
		t.Fatalf("should show completion summary after successful install, got %#v", ui.summaries)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "继续安装？")
}

func TestInstallServerFreshInstallPreparesDirsBeforeApplyInit(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"9527", "127.0.0.1/8", "https://panel.example.com", "admin"},
		passwords: []string{"Password123", "Password123"},
		confirms:  []bool{true},
	}
	calls := make([]string, 0, 8)
	record := func(name string) {
		calls = append(calls, name)
	}
	var gotInitParams server.InitParams

	err := InstallServerWith(serverDeps{
		UI:            ui,
		Detect:        func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		SelectTLSMode: func(ui uiProvider) (string, error) { return "off", nil },
		EnsureUser: func(name string) error {
			record("ensure-user")
			return nil
		},
		EnsureDirs: func() error {
			record("ensure-dirs")
			return nil
		},
		ApplyInit: func(dataDir string, params server.InitParams) error {
			record("apply-init")
			gotInitParams = params
			return nil
		},
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteServerEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ServerEnv) error { return nil },
		WriteServerUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("fresh server install should not error: %v", err)
	}
	assertCallBefore(t, calls, "ensure-user", "apply-init")
	assertCallBefore(t, calls, "ensure-dirs", "apply-init")
	if gotInitParams.ServerAddr != "https://panel.example.com" {
		t.Fatalf("fresh server init server addr = %q, want %q", gotInitParams.ServerAddr, "https://panel.example.com")
	}
	assertInputPromptDefault(t, ui.inputCalls, "可信代理 CIDR", "127.0.0.1/8")
	assertInputPromptDescriptionContains(t, ui.inputCalls, "可信代理 CIDR", "0.0.0.0/0")
	assertInputPromptDescriptionContains(t, ui.inputCalls, "可信代理 CIDR", "反向代理")
}

func assertCallBefore(t *testing.T, calls []string, first, second string) {
	t.Helper()
	firstIndex := -1
	secondIndex := -1
	for i, call := range calls {
		if call == first && firstIndex == -1 {
			firstIndex = i
		}
		if call == second && secondIndex == -1 {
			secondIndex = i
		}
	}
	if firstIndex == -1 || secondIndex == -1 {
		t.Fatalf("calls = %#v, want both %q and %q", calls, first, second)
	}
	if firstIndex > secondIndex {
		t.Fatalf("calls = %#v, want %q before %q", calls, first, second)
	}
}

func assertInputPromptDefault(t *testing.T, calls []inputCall, prompt, want string) {
	t.Helper()
	for _, call := range calls {
		if call.prompt == prompt {
			if call.opts.Default != want {
				t.Fatalf("input prompt %q default = %q, want %q", prompt, call.opts.Default, want)
			}
			return
		}
	}
	t.Fatalf("input prompt %q not found in %#v", prompt, calls)
}

func assertInputPromptDescriptionContains(t *testing.T, calls []inputCall, prompt, want string) {
	t.Helper()
	for _, call := range calls {
		if call.prompt == prompt {
			if !strings.Contains(call.opts.Description, want) {
				t.Fatalf("input prompt %q description = %q, want to contain %q", prompt, call.opts.Description, want)
			}
			return
		}
	}
	t.Fatalf("input prompt %q not found in %#v", prompt, calls)
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
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Server 安装状态异常" {
		t.Fatalf("broken state should show problem summary, got %#v", ui.summaries)
	}
}

func TestInstallServerWithConfirmNoPrintsCancelledSummary(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"9527", "127.0.0.1/8", "https://panel.example.com", "admin"},
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
		WriteServerEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ServerEnv) error { return nil },
		WriteServerUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("cancelling install should not error: %v", err)
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "安装已取消" {
		t.Fatalf("should show cancellation summary after declining, got %#v", ui.summaries)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "继续安装？")
}
