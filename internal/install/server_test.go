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
		t.Fatalf("已安装时不应报错: %v", err)
	}
	if called {
		t.Fatal("已安装时不应继续执行安装动作")
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "服务端已安装" {
		t.Fatalf("已安装时应提示下一步，得到 %#v", ui.summaries)
	}
}

func TestInstallServerWithHistoricalDataSkipsInit(t *testing.T) {
	ui := &fakeUI{
		inputs:   []string{"8080", "127.0.0.1/32"},
		confirms: []bool{true, true},
	}
	applyInitCalled := false
	writeSpecCalled := false
	err := InstallServerWith(serverDeps{
		UI:            ui,
		Detect:        func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateHistoricalDataOnly },
		SelectTLSMode: func(ui uiProvider) (string, error) { return "off", nil },
		LoadRecoverable: func() (server.InitParams, error) {
			return server.InitParams{ServerAddr: "https://panel.example.com", AllowedPorts: "1-65535"}, nil
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
		DaemonReload:    func() error { return nil },
		EnableAndStart:  func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("历史数据恢复安装不应报错: %v", err)
	}
	if applyInitCalled {
		t.Fatal("历史数据恢复安装不应再次调用 ApplyInit")
	}
	if !writeSpecCalled {
		t.Fatal("历史数据恢复安装应继续写入 spec/env/unit")
	}
	if len(ui.summaries) != 3 {
		t.Fatalf("应输出历史数据确认、安装确认、完成三次 summary，实际 %d 次", len(ui.summaries))
	}
	if ui.summaries[0].title != "检测到服务端历史数据" {
		t.Fatalf("应先提示历史数据选择，得到 %#v", ui.summaries)
	}
	if ui.summaries[2].title != "服务端安装完成" {
		t.Fatalf("历史恢复安装完成后应输出成功摘要，得到 %#v", ui.summaries)
	}
}

func TestInstallServerWithHistoricalDataDeclineReuseStopsInstall(t *testing.T) {
	ui := &fakeUI{
		inputs:   []string{"8080", "127.0.0.1/32"},
		confirms: []bool{false},
	}
	writeSpecCalled := false
	err := InstallServerWith(serverDeps{
		UI:            ui,
		Detect:        func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateHistoricalDataOnly },
		SelectTLSMode: func(ui uiProvider) (string, error) { return "off", nil },
		LoadRecoverable: func() (server.InitParams, error) {
			return server.InitParams{ServerAddr: "https://panel.example.com", AllowedPorts: "1-65535"}, nil
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
		t.Fatalf("拒绝使用历史数据不应报错: %v", err)
	}
	if writeSpecCalled {
		t.Fatal("拒绝使用历史数据后不应继续安装")
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "安装已取消" {
		t.Fatalf("拒绝使用历史数据后应输出取消摘要，得到 %#v", ui.summaries)
	}
}

func TestInstallServerWithCustomTLSCollectsCertAndKey(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"8080", "127.0.0.1/32", "/tmp/cert.pem", "/tmp/key.pem", "https://panel.example.com", "admin", "1-65535"},
		passwords: []string{"Password123"},
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
		DaemonReload:    func() error { return nil },
		EnableAndStart:  func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("custom TLS 安装不应报错: %v", err)
	}
	if writtenEnv.TLSCert != "/tmp/cert.pem" || writtenEnv.TLSKey != "/tmp/key.pem" {
		t.Fatalf("custom TLS 应写入 cert/key，得到 %#v", writtenEnv)
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "服务端安装完成" {
		t.Fatalf("安装成功后应输出完成摘要，得到 %#v", ui.summaries)
	}
}

func TestInstallServerWithBrokenStateFails(t *testing.T) {
	ui := &fakeUI{}
	err := InstallServerWith(serverDeps{
		UI: ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"缺少 unit 文件"}}
		},
	})
	if err == nil {
		t.Fatal("broken 状态应失败")
	}
	if !errors.Is(err, errInstallBrokenState) {
		t.Fatalf("broken 状态应返回 errInstallBrokenState，得到 %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "服务端安装状态异常" {
		t.Fatalf("broken 状态应先输出问题摘要，得到 %#v", ui.summaries)
	}
}

func TestInstallServerWithConfirmNoPrintsCancelledSummary(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"8080", "127.0.0.1/32", "https://panel.example.com", "admin", "1-65535"},
		passwords: []string{"Password123"},
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
		t.Fatalf("取消安装不应报错: %v", err)
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "安装已取消" {
		t.Fatalf("取消安装后应输出取消摘要，得到 %#v", ui.summaries)
	}
}
