package install

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

type fakeUI struct {
	selects           []int
	selectCalls       []selectCall
	selectOptionCalls []selectOptionCall
	inputs            []string
	passwords         []string
	inputCalls        []inputCall
	passwordCalls     []inputCall
	confirms          []bool
	confirmCalls      []confirmCall
	summaries         []summaryCall
}

type inputCall struct {
	prompt string
	opts   tui.InputOptions
}

type selectCall struct {
	prompt  string
	options []string
}

type selectOptionCall struct {
	prompt  string
	options []tui.SelectOption
}

type confirmCall struct {
	prompt      string
	confirmText string
}

type summaryCall struct {
	title string
	rows  [][2]string
}

func (f *fakeUI) Select(prompt string, options []string) (int, error) {
	f.selectCalls = append(f.selectCalls, selectCall{prompt: prompt, options: append([]string(nil), options...)})
	if len(f.selects) == 0 {
		return 0, errors.New("no select value")
	}
	v := f.selects[0]
	f.selects = f.selects[1:]
	return v, nil
}

func (f *fakeUI) SelectWithOptions(prompt string, options []tui.SelectOption) (int, error) {
	copied := append([]tui.SelectOption(nil), options...)
	f.selectOptionCalls = append(f.selectOptionCalls, selectOptionCall{prompt: prompt, options: copied})
	labels := make([]string, len(options))
	for i, option := range options {
		labels[i] = option.Label
	}
	return f.Select(prompt, labels)
}

func (f *fakeUI) Input(prompt string, opts ...tui.InputOptions) (string, error) {
	call := inputCall{prompt: prompt}
	if len(opts) > 0 {
		call.opts = opts[0]
	}
	f.inputCalls = append(f.inputCalls, call)
	if len(f.inputs) == 0 {
		return "", errors.New("no input value")
	}
	v := f.inputs[0]
	f.inputs = f.inputs[1:]
	return v, nil
}

func (f *fakeUI) Password(prompt string, opts ...tui.InputOptions) (string, error) {
	call := inputCall{prompt: prompt}
	if len(opts) > 0 {
		call.opts = opts[0]
	}
	f.passwordCalls = append(f.passwordCalls, call)
	if len(f.passwords) == 0 {
		return "", errors.New("no password value")
	}
	v := f.passwords[0]
	f.passwords = f.passwords[1:]
	return v, nil
}

func (f *fakeUI) Confirm(prompt string) (bool, error) {
	return f.ConfirmWithOptions(prompt, tui.ConfirmOptions{})
}

func (f *fakeUI) ConfirmWithOptions(prompt string, opts tui.ConfirmOptions) (bool, error) {
	f.confirmCalls = append(f.confirmCalls, confirmCall{prompt: prompt, confirmText: opts.ConfirmText})
	if len(f.confirms) == 0 {
		return false, errors.New("no confirm value")
	}
	v := f.confirms[0]
	f.confirms = f.confirms[1:]
	return v, nil
}

func (f *fakeUI) PrintSummary(title string, rows [][2]string) {
	f.summaries = append(f.summaries, summaryCall{title: title, rows: rows})
}

func assertSelectOptionsDescribed(t *testing.T, calls []selectOptionCall, prompt string) {
	t.Helper()
	for _, call := range calls {
		if call.prompt != prompt {
			continue
		}
		if len(call.options) == 0 {
			t.Fatalf("select %q has no options", prompt)
		}
		for _, option := range call.options {
			if strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Description) == "" {
				t.Fatalf("select %q has undescribed option %#v", prompt, option)
			}
		}
		return
	}
	t.Fatalf("select prompt %q not found in %#v", prompt, calls)
}

func TestRunWithPlatformCheck(t *testing.T) {
	err := RunWith(Deps{
		GOOS:          "darwin",
		HasTTY:        true,
		UID:           0,
		HasSystemd:    true,
		UI:            &fakeUI{},
		InstallServer: func() error { return nil },
		InstallClient: func() error { return nil },
	})
	if err == nil {
		t.Fatal("non-Linux platform should fail")
	}
}

func TestRunWithTTYCheck(t *testing.T) {
	err := RunWith(Deps{
		GOOS:          "linux",
		HasTTY:        false,
		UID:           0,
		HasSystemd:    true,
		UI:            &fakeUI{},
		InstallServer: func() error { return nil },
		InstallClient: func() error { return nil },
	})
	if err == nil {
		t.Fatal("non-TTY should fail")
	}
}

func TestRunWithRoleDispatch(t *testing.T) {
	ui := &fakeUI{selects: []int{0}}
	called := ""
	err := RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        0,
		HasSystemd: true,
		UI:         ui,
		InstallServer: func() error {
			called = "server"
			return nil
		},
		InstallClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunWith() should not fail: %v", err)
	}
	if called != "server" {
		t.Fatalf("selecting server role should dispatch to server, got %q", called)
	}
	assertSelectOptionsDescribed(t, ui.selectOptionCalls, "选择安装角色")
	assertSelectOptionLabels(t, ui.selectOptionCalls, "选择安装角色", []string{"安装 server", "安装 client"})
	called = ""
	ui = &fakeUI{selects: []int{1}}
	err = RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        0,
		HasSystemd: true,
		UI:         ui,
		InstallServer: func() error {
			called = "server"
			return nil
		},
		InstallClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunWith() should not fail: %v", err)
	}
	if called != "client" {
		t.Fatalf("selecting client role should dispatch to client, got %q", called)
	}
}

func assertSelectOptionLabels(t *testing.T, calls []selectOptionCall, prompt string, want []string) {
	t.Helper()
	for _, call := range calls {
		if call.prompt != prompt {
			continue
		}
		got := make([]string, len(call.options))
		for i, option := range call.options {
			got[i] = option.Label
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("select %q labels = %#v, want %#v", prompt, got, want)
		}
		return
	}
	t.Fatalf("select prompt %q not found in %#v", prompt, calls)
}

func TestRunWithBothRolesInstalledSkipsRoleSelection(t *testing.T) {
	ui := &fakeUI{}
	called := ""
	err := RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        0,
		HasSystemd: true,
		UI:         ui,
		Detect: func(role svcmgr.Role) svcmgr.InstallState {
			return svcmgr.StateInstalled
		},
		InstallServer: func() error {
			called = "server"
			return nil
		},
		InstallClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunWith() should not fail: %v", err)
	}
	if called != "" {
		t.Fatalf("install should not dispatch an already-installed role, got %q", called)
	}
	if len(ui.selectCalls) != 0 {
		t.Fatalf("install should not ask for a role when both roles are installed, got %#v", ui.selectCalls)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "托管服务已安装" {
		t.Fatalf("expected installed-state summary, got %#v", ui.summaries)
	}
	assertSummaryRow(t, ui.summaries[0], "server 角色", "已安装")
	assertSummaryRow(t, ui.summaries[0], "client 角色", "已安装")
}

func TestRunWithOneRoleInstalledRequiresConfirmationBeforeInstallingOtherRole(t *testing.T) {
	tests := []struct {
		name       string
		installed  svcmgr.Role
		wantCalled string
		wantTitle  string
		wantNext   string
		wantPrompt string
	}{
		{
			name:       "server installed",
			installed:  svcmgr.RoleServer,
			wantCalled: "client",
			wantTitle:  "本机已安装 server",
			wantNext:   "需要时继续安装 client；否则保持当前状态",
			wantPrompt: "是否也在本机安装 client？",
		},
		{
			name:       "client installed",
			installed:  svcmgr.RoleClient,
			wantCalled: "server",
			wantTitle:  "本机已安装 client",
			wantNext:   "需要时继续安装 server；否则保持当前状态",
			wantPrompt: "是否也在本机安装 server？",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := &fakeUI{confirms: []bool{true}}
			called := ""
			err := RunWith(Deps{
				GOOS:       "linux",
				HasTTY:     true,
				UID:        0,
				HasSystemd: true,
				UI:         ui,
				Detect: func(role svcmgr.Role) svcmgr.InstallState {
					if role == tt.installed {
						return svcmgr.StateInstalled
					}
					return svcmgr.StateNotInstalled
				},
				InstallServer: func() error {
					called = "server"
					return nil
				},
				InstallClient: func() error {
					called = "client"
					return nil
				},
			})
			if err != nil {
				t.Fatalf("RunWith() should not fail: %v", err)
			}
			if called != tt.wantCalled {
				t.Fatalf("install should dispatch optional role %q after confirmation, got %q", tt.wantCalled, called)
			}
			if len(ui.selectCalls) != 0 {
				t.Fatalf("install should not ask users to choose an installed role, got %#v", ui.selectCalls)
			}
			if len(ui.summaries) != 1 || ui.summaries[0].title != tt.wantTitle {
				t.Fatalf("install should summarize the intended role, got %#v", ui.summaries)
			}
			assertSummaryRow(t, ui.summaries[0], "当前状态", string(tt.installed)+" 托管服务已安装")
			assertSummaryRow(t, ui.summaries[0], "下一步", tt.wantNext)
			if tt.installed == svcmgr.RoleServer {
				assertSummaryRow(t, ui.summaries[0], "说明", "本机已安装 server。如果这台机器还需要作为 client 连接到另一个 NetsGo server，可以继续安装 client。")
			} else {
				assertSummaryRow(t, ui.summaries[0], "说明", "本机已安装 client。如果这台机器还需要作为 server 提供 Web 控制台和公网隧道入口，可以继续安装 server。")
			}
			assertConfirmPrompt(t, ui.confirmCalls, tt.wantPrompt)
			assertSummaryDoesNotContain(t, ui.summaries[0], "缺失")
			assertSummaryDoesNotContain(t, ui.summaries[0], "未安装")
			assertSummaryDoesNotContain(t, ui.summaries[0], "有效配置")
			assertSummaryDoesNotContain(t, ui.summaries[0], "同机运行")
		})
	}
}

func TestRunWithOneRoleInstalledNoKeepsCurrentState(t *testing.T) {
	tests := []struct {
		name       string
		installed  svcmgr.Role
		cancelNext string
	}{
		{
			name:       "server installed",
			installed:  svcmgr.RoleServer,
			cancelNext: "运行 netsgo manage 管理已安装的 server 服务",
		},
		{
			name:       "client installed",
			installed:  svcmgr.RoleClient,
			cancelNext: "运行 netsgo manage 管理已安装的 client 服务",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := &fakeUI{confirms: []bool{false}}
			called := ""
			err := RunWith(Deps{
				GOOS:       "linux",
				HasTTY:     true,
				UID:        0,
				HasSystemd: true,
				UI:         ui,
				Detect: func(role svcmgr.Role) svcmgr.InstallState {
					if role == tt.installed {
						return svcmgr.StateInstalled
					}
					return svcmgr.StateNotInstalled
				},
				InstallServer: func() error {
					called = "server"
					return nil
				},
				InstallClient: func() error {
					called = "client"
					return nil
				},
			})
			if err != nil {
				t.Fatalf("RunWith() should not fail: %v", err)
			}
			if called != "" {
				t.Fatalf("install should not dispatch after no confirmation, got %q", called)
			}
			if len(ui.summaries) != 2 || ui.summaries[1].title != "已取消安装，未进行任何修改" {
				t.Fatalf("install should print a no-changes cancellation summary, got %#v", ui.summaries)
			}
			assertSummaryRow(t, ui.summaries[1], "状态", "保持当前安装状态")
			assertSummaryRow(t, ui.summaries[1], "下一步", tt.cancelNext)
			assertSummaryDoesNotContain(t, ui.summaries[0], "缺失")
			assertSummaryDoesNotContain(t, ui.summaries[1], "缺失")
		})
	}
}

func TestRunWithInstalledAndAbnormalRoleRoutesToManage(t *testing.T) {
	ui := &fakeUI{}
	called := ""
	err := RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        0,
		HasSystemd: true,
		UI:         ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			if role == svcmgr.RoleServer {
				return svcmgr.InstallInspection{Role: role, State: svcmgr.StateInstalled}
			}
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"missing env file"}}
		},
		InstallServer: func() error {
			called = "server"
			return nil
		},
		InstallClient: func() error {
			called = "client"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunWith() should not fail: %v", err)
	}
	if called != "" {
		t.Fatalf("install should not dispatch when the other role is abnormal, got %q", called)
	}
	if len(ui.selectCalls) != 0 {
		t.Fatalf("install should not ask users to choose an installed role, got %#v", ui.selectCalls)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "托管服务状态需要处理" {
		t.Fatalf("expected abnormal-state summary, got %#v", ui.summaries)
	}
	assertSummaryRow(t, ui.summaries[0], "下一步", "运行 netsgo manage 检查或清理异常服务状态")
	assertSummaryRow(t, ui.summaries[0], "client 角色", "需要处理")
}

func assertSummaryDoesNotContain(t *testing.T, summary summaryCall, notWant string) {
	t.Helper()
	if strings.Contains(summary.title, notWant) {
		t.Fatalf("summary title should not contain %q: %#v", notWant, summary)
	}
	for _, row := range summary.rows {
		if strings.Contains(row[0], notWant) || strings.Contains(row[1], notWant) {
			t.Fatalf("summary should not contain %q: %#v", notWant, summary.rows)
		}
	}
}

func TestRunWithNonRootReexecsUsingLookedUpSudo(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"netsgo", "install"}
	t.Cleanup(func() {
		os.Args = origArgs
	})

	execErr := errors.New("exec called")
	var gotPath string
	var gotArgv []string

	err := RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        1000,
		HasSystemd: true,
		LookPath: func(file string) (string, error) {
			if file != "sudo" {
				t.Fatalf("expected sudo lookup, got %q", file)
			}
			return "/tmp/custom/sudo", nil
		},
		Exec: func(argv0 string, argv []string, envv []string) error {
			gotPath = argv0
			gotArgv = append([]string(nil), argv...)
			return execErr
		},
	})

	if !errors.Is(err, execErr) {
		t.Fatalf("expected exec error, got %v", err)
	}
	if gotPath != "/tmp/custom/sudo" {
		t.Fatalf("expected resolved sudo path, got %q", gotPath)
	}
	wantArgv := append([]string{"sudo"}, os.Args...)
	if !reflect.DeepEqual(gotArgv, wantArgv) {
		t.Fatalf("expected argv %v, got %v", wantArgv, gotArgv)
	}
}

func TestRunWithNonRootMissingSudoFailsClearly(t *testing.T) {
	calledExec := false
	err := RunWith(Deps{
		GOOS:       "linux",
		HasTTY:     true,
		UID:        1000,
		HasSystemd: true,
		LookPath: func(file string) (string, error) {
			if file != "sudo" {
				t.Fatalf("expected sudo lookup, got %q", file)
			}
			return "", exec.ErrNotFound
		},
		Exec: func(argv0 string, argv []string, envv []string) error {
			calledExec = true
			return nil
		},
	})

	if err == nil {
		t.Fatal("expected missing sudo error")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("expected wrapped exec.ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "sudo") || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("expected actionable sudo PATH error, got %v", err)
	}
	if calledExec {
		t.Fatal("exec should not run when sudo is missing")
	}
}
