package manage

import (
	"errors"
	"testing"

	"netsgo/internal/svcmgr"
)

type fakeUI struct {
	selects   []int
	confirms  []bool
	summaries []summaryCall
}

type summaryCall struct {
	title string
	rows  [][2]string
}

func (f *fakeUI) Select(prompt string, options []string) (int, error) {
	if len(f.selects) == 0 {
		return 0, errors.New("no select value")
	}
	v := f.selects[0]
	f.selects = f.selects[1:]
	return v, nil
}

func (f *fakeUI) Confirm(prompt string) (bool, error) {
	if len(f.confirms) == 0 {
		return true, nil
	}
	v := f.confirms[0]
	f.confirms = f.confirms[1:]
	return v, nil
}
func (f *fakeUI) PrintSummary(title string, rows [][2]string) {
	f.summaries = append(f.summaries, summaryCall{title: title, rows: rows})
}

func TestRunWithPlatformCheck(t *testing.T) {
	err := RunWith(Deps{GOOS: "darwin", HasTTY: true, UID: 0, UI: &fakeUI{}})
	if err == nil {
		t.Fatal("非 Linux 平台应失败")
	}
}

func TestRunWithTTYCheck(t *testing.T) {
	err := RunWith(Deps{GOOS: "linux", HasTTY: false, UID: 0, UI: &fakeUI{}})
	if err == nil {
		t.Fatal("非 TTY 应失败")
	}
}

func TestRunWithNoInstalledRole(t *testing.T) {
	ui := &fakeUI{}
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     ui,
		Detect: func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
	})
	if err != nil {
		t.Fatalf("未安装时不应报错: %v", err)
	}
	if len(ui.summaries) != 1 {
		t.Fatalf("未安装时应输出一次 summary，实际 %d 次", len(ui.summaries))
	}
}

func TestRunWithRoleDispatch(t *testing.T) {
	called := ""
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     &fakeUI{selects: []int{0}},
		Detect: func(role svcmgr.Role) svcmgr.InstallState {
			if role == svcmgr.RoleServer {
				return svcmgr.StateInstalled
			}
			return svcmgr.StateNotInstalled
		},
		ManageServer: func() error {
			called = "server"
			return nil
		},
		ManageClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("manage run 不应失败: %v", err)
	}
	if called != "server" {
		t.Fatalf("应分发到 server，得到 %q", called)
	}

	called = ""
	err = RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     &fakeUI{selects: []int{1}},
		Detect: func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateInstalled },
		ManageServer: func() error {
			called = "server"
			return nil
		},
		ManageClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("manage run 不应失败: %v", err)
	}
	if called != "client" {
		t.Fatalf("应分发到 client，得到 %q", called)
	}
}

func TestRunWithServerBrokenNoInstalledRole(t *testing.T) {
	called := false
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     &fakeUI{},
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			if role == svcmgr.RoleServer {
				return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"缺少 env 文件"}}
			}
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateNotInstalled}
		},
		ManageServer: func() error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("broken 状态应输出引导而非报错: %v", err)
	}
	if called {
		t.Fatal("broken 状态下不应进入常规 manage server 菜单")
	}
}

func TestRunWithServerHistoricalDataOnlyNoInstalledRole(t *testing.T) {
	ui := &fakeUI{}
	called := false
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			if role == svcmgr.RoleServer {
				return svcmgr.InstallInspection{Role: role, State: svcmgr.StateHistoricalDataOnly}
			}
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateNotInstalled}
		},
		ManageServer: func() error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("historical-data-only 应输出恢复引导而非报错: %v", err)
	}
	if called {
		t.Fatal("historical-data-only 下不应进入常规 manage server 菜单")
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "检测到服务端历史数据" {
		t.Fatalf("应输出历史数据提示，得到 %#v", ui.summaries)
	}
}

func TestRunWithClientInstalledAndServerBrokenWarnsThenManagesClient(t *testing.T) {
	ui := &fakeUI{}
	called := ""
	err := RunWith(Deps{
		GOOS:   "linux",
		HasTTY: true,
		UID:    0,
		UI:     ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			if role == svcmgr.RoleServer {
				return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"缺少 binary"}}
			}
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateInstalled}
		},
		ManageClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("存在健康客户端时仍应允许进入客户端管理: %v", err)
	}
	if called != "client" {
		t.Fatalf("应继续进入客户端管理，得到 %q", called)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "服务端安装状态异常" {
		t.Fatalf("应先提示服务端异常，得到 %#v", ui.summaries)
	}
}
