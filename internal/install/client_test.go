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
		t.Fatalf("已安装时不应报错: %v", err)
	}
	if called {
		t.Fatal("已安装时不应继续执行安装动作")
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "客户端已安装" {
		t.Fatalf("已安装时应提示下一步，得到 %#v", ui.summaries)
	}
}

func TestInstallClientWithBrokenStateFails(t *testing.T) {
	ui := &fakeUI{}
	err := InstallClientWith(clientDeps{
		UI: ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"缺少 env 文件"}}
		},
	})
	if err == nil {
		t.Fatal("broken 状态应失败")
	}
	if !errors.Is(err, errInstallBrokenState) {
		t.Fatalf("broken 状态应返回 errInstallBrokenState，得到 %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "客户端安装状态异常" {
		t.Fatalf("broken 状态应先输出问题摘要，得到 %#v", ui.summaries)
	}
}

func TestInstallClientWithHistoricalDataOnlyFailsWithReauthMessage(t *testing.T) {
	ui := &fakeUI{}
	err := InstallClientWith(clientDeps{
		UI: ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateHistoricalDataOnly, Problems: []string{"残留运行数据目录仍存在: /var/lib/netsgo/client"}}
		},
	})
	if err == nil {
		t.Fatal("client 历史数据残留应拒绝安装")
	}
	if !errors.Is(err, errInstallBrokenState) {
		t.Fatalf("client 历史数据残留应返回 errInstallBrokenState，得到 %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "客户端安装状态异常" {
		t.Fatalf("client 历史数据残留应输出异常摘要，得到 %#v", ui.summaries)
	}
	rows := ui.summaries[0].rows
	foundAdvice := false
	for _, row := range rows {
		if row[0] == "建议" && row[1] == "客户端不支持恢复旧数据；请清理残留数据后重新安装并重新认证" {
			foundAdvice = true
			break
		}
	}
	if !foundAdvice {
		t.Fatalf("client 历史数据残留应明确提示重新认证，得到 %#v", rows)
	}
}

func TestInstallClientWithFreshInstall(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"wss://panel.example.com", "AA:BB:CC"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true, true},
	}
	writeSpecCalled := false
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientSpec: func(spec svcmgr.ServiceSpec) error {
			writeSpecCalled = true
			return nil
		},
		WriteClientEnv:  func(spec svcmgr.ServiceSpec, env svcmgr.ClientEnv) error { return nil },
		WriteClientUnit: func(spec svcmgr.ServiceSpec) error { return nil },
		DaemonReload:    func() error { return nil },
		EnableAndStart:  func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("新装 client 不应报错: %v", err)
	}
	if !writeSpecCalled {
		t.Fatal("新装 client 应写入 spec")
	}
	if len(ui.summaries) != 2 {
		t.Fatalf("应输出确认与完成两次 summary，实际 %d 次", len(ui.summaries))
	}
	if ui.summaries[1].title != "客户端安装完成" {
		t.Fatalf("安装成功后应输出完成摘要，得到 %#v", ui.summaries)
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
		WriteClientSpec:   func(spec svcmgr.ServiceSpec) error { return nil },
		WriteClientEnv:    func(spec svcmgr.ServiceSpec, env svcmgr.ClientEnv) error { return nil },
		WriteClientUnit:   func(spec svcmgr.ServiceSpec) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
	})
	if err != nil {
		t.Fatalf("client install 不应报错: %v", err)
	}
	if !ensureDirsCalled {
		t.Fatal("client install 应创建固定目录")
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
		WriteClientSpec:   func(spec svcmgr.ServiceSpec) error { return nil },
		WriteClientEnv:    func(spec svcmgr.ServiceSpec, env svcmgr.ClientEnv) error { return nil },
		WriteClientUnit:   func(spec svcmgr.ServiceSpec) error { return nil },
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
