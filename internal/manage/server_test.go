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
		t.Fatalf("inspect 不应报错: %v", err)
	}
	if len(ui.summaries) == 0 {
		t.Fatal("inspect 应输出 summary")
	}
	for _, row := range ui.summaries[0].rows {
		if strings.Contains(strings.ToLower(row[0]), "password") || strings.Contains(strings.ToLower(row[1]), "password") {
			t.Fatalf("inspect 不应暴露敏感字段: %#v", ui.summaries[0].rows)
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
		t.Fatalf("uninstall keep-data 不应报错: %v", err)
	}
	for _, path := range removed {
		if path == svcmgr.ManagedDataDir+"/server" {
			t.Fatalf("keep-data 模式不应删除 server data dir: %v", removed)
		}
	}
	if binaryRemoved {
		t.Fatal("client 仍安装时不应删除共享二进制")
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
		t.Fatalf("restart 不应报错: %v", err)
	}
	if !stopped || !started {
		t.Fatalf("restart 应先 stop 再 start, stopped=%v started=%v", stopped, started)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "操作成功" {
		t.Fatalf("restart 成功后应输出成功提示，得到 %#v", ui.summaries)
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
		t.Fatalf("logs 不应报错: %v", err)
	}
	if !called {
		t.Fatal("logs 应转交给 journald 执行函数")
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
		t.Fatalf("start 不应报错: %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "操作成功" {
		t.Fatalf("start 成功后应输出成功提示，得到 %#v", ui.summaries)
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
		t.Fatalf("取消卸载不应报错: %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "已取消" {
		t.Fatalf("取消卸载后应输出取消提示，得到 %#v", ui.summaries)
	}
}
