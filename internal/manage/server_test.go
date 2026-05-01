package manage

import (
	"strings"
	"testing"

	"netsgo/internal/svcmgr"
)

func newInstalledServerDeps(t *testing.T, ui *fakeUI) (serverDeps, svcmgr.ServiceLayout) {
	t.Helper()

	layout := svcmgr.NewLayout(svcmgr.RoleServer)

	return serverDeps{
		UI: ui,
		Inspect: func() svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: svcmgr.RoleServer, State: svcmgr.StateInstalled}
		},
		IsActive:  func() (bool, error) { return true, nil },
		IsEnabled: func() (bool, error) { return true, nil },
		Logs:      func() error { return nil },
		RunInstall: func() error {
			return nil
		},
		ReadServerEnv: func() (svcmgr.ServerEnv, error) {
			return svcmgr.ServerEnv{Port: 9527, TLSMode: "off", ServerAddr: "https://panel.example.com"}, nil
		},
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectClient: func() svcmgr.InstallState {
			return svcmgr.StateInstalled
		},
	}, layout
}

func TestManageServerInspectRedactsSensitiveData(t *testing.T) {
	ui := &fakeUI{selects: []int{1, 8}}
	deps, _ := newInstalledServerDeps(t, ui)

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) == 0 {
		t.Fatal("inspect should output a summary")
	}
	assertSelectOptionsDescribed(t, ui.selectOptionCalls, "选择 server 操作")
	for _, row := range ui.summaries[0].rows {
		if strings.Contains(strings.ToLower(row[0]), "password") || strings.Contains(strings.ToLower(row[1]), "password") {
			t.Fatalf("inspect should not expose sensitive fields: %#v", ui.summaries[0].rows)
		}
	}
}

func TestManageServerUninstallKeepData(t *testing.T) {
	ui := &fakeUI{selects: []int{7, 0}, confirms: []bool{true}}
	removed := []string{}
	binaryRemoved := false
	deps, spec := newInstalledServerDeps(t, ui)
	deps.RemovePaths = func(paths ...string) error {
		removed = append(removed, paths...)
		return nil
	}
	deps.RemoveBinary = func() error {
		binaryRemoved = true
		return nil
	}

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	for _, path := range removed {
		if path == serverDataPath(spec) {
			t.Fatalf("keep-data mode should not remove the server data dir: %v", removed)
		}
	}
	if binaryRemoved {
		t.Fatal("should not remove the shared binary while the client is still installed")
	}
	if !hasSelectPrompt(ui.selectCalls, "Server 卸载模式") {
		t.Fatalf("server uninstall should identify the role in the mode prompt, got %#v", ui.selectCalls)
	}
	assertSelectOptionsDescribed(t, ui.selectOptionCalls, "Server 卸载模式")
	assertConfirmPhrase(t, ui.confirmCalls, "继续卸载 server？", "uninstall server")
}

func TestManageServerRestart(t *testing.T) {
	ui := &fakeUI{selects: []int{5, 8}}
	stopped := false
	started := false
	deps, _ := newInstalledServerDeps(t, ui)
	deps.DisableAndStop = func() error { stopped = true; return nil }
	deps.EnableAndStart = func() error { started = true; return nil }

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if !stopped || !started {
		t.Fatalf("restart should stop before start, stopped=%v started=%v", stopped, started)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "操作成功" {
		t.Fatalf("restart should show a success message, got %#v", ui.summaries)
	}
}

func TestManageServerLogs(t *testing.T) {
	ui := &fakeUI{selects: []int{2}}
	called := false
	deps, _ := newInstalledServerDeps(t, ui)
	deps.Logs = func() error {
		called = true
		return nil
	}

	err := ManageServerWith(deps)
	if err != nil {
		t.Fatalf("logs should not fail: %v", err)
	}
	if !called {
		t.Fatal("logs should delegate to the journald execution function")
	}
}

func TestManageServerStartPrintsSuccess(t *testing.T) {
	ui := &fakeUI{selects: []int{3, 8}}
	deps, _ := newInstalledServerDeps(t, ui)

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) != 1 || ui.summaries[0].title != "操作成功" {
		t.Fatalf("start should show a success message, got %#v", ui.summaries)
	}
}

func TestManageServerUninstallCancelPrintsCancelled(t *testing.T) {
	ui := &fakeUI{selects: []int{7, 0, 8}, confirms: []bool{false}}
	deps, _ := newInstalledServerDeps(t, ui)

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) != 2 || ui.summaries[1].title != "已取消" {
		t.Fatalf("canceling uninstall should show a canceled message, got %#v", ui.summaries)
	}
	assertSummaryCallRow(t, ui.summaries[1], "下一步", "选择其他操作，或选择返回")
}

func TestManageServerUninstallLastRoleCanRemoveSharedBinary(t *testing.T) {
	ui := &fakeUI{selects: []int{7, 0}, confirms: []bool{true, true}}
	binaryRemoved := false
	deps, _ := newInstalledServerDeps(t, ui)
	deps.DetectClient = func() svcmgr.InstallState { return svcmgr.StateNotInstalled }
	deps.RemoveBinary = func() error {
		binaryRemoved = true
		return nil
	}

	err := ManageServerWith(deps)
	assertSelectionExit(t, err)

	if !binaryRemoved {
		t.Fatal("expected the last-role uninstall flow to allow removing the shared binary")
	}
	assertConfirmPhrase(t, ui.confirmCalls, "继续卸载 server？", "uninstall server")
	assertConfirmPhrase(t, ui.confirmCalls, "未检测到其他托管角色。是否同时移除共享二进制 /usr/local/bin/netsgo？", "remove binary")
}

func TestCleanupBrokenServerUsesCleanupPhraseWhenKeepingData(t *testing.T) {
	ui := &fakeUI{selects: []int{0}, confirms: []bool{true}}
	deps, _ := newInstalledServerDeps(t, ui)

	cleaned, err := cleanupBrokenServer(deps)
	if err != nil {
		t.Fatalf("cleanupBrokenServer should not fail: %v", err)
	}
	if !cleaned {
		t.Fatal("cleanupBrokenServer should report cleanup complete")
	}
	if len(ui.selectCalls) == 0 || ui.selectCalls[0].prompt != "Server 清理模式" {
		t.Fatalf("server cleanup should identify the role in the mode prompt, got %#v", ui.selectCalls)
	}
	assertSelectOptionsDescribed(t, ui.selectOptionCalls, "Server 清理模式")
	assertConfirmPhrase(t, ui.confirmCalls, "继续清理异常 server？", "cleanup server")
}

func TestCleanupBrokenServerDeleteDataRequiresRemovePhrase(t *testing.T) {
	ui := &fakeUI{selects: []int{1}, confirms: []bool{true}}
	deps, _ := newInstalledServerDeps(t, ui)

	cleaned, err := cleanupBrokenServer(deps)
	if err != nil {
		t.Fatalf("cleanupBrokenServer should not fail: %v", err)
	}
	if !cleaned {
		t.Fatal("cleanupBrokenServer should report cleanup complete")
	}
	assertConfirmPhrase(t, ui.confirmCalls, "继续清理异常 server？", "remove server data")
}

func assertSummaryCallRow(t *testing.T, summary summaryCall, key, want string) {
	t.Helper()
	for _, row := range summary.rows {
		if row[0] == key {
			if row[1] != want {
				t.Fatalf("summary row %q = %q, want %q", key, row[1], want)
			}
			return
		}
	}
	t.Fatalf("summary row %q not found in %#v", key, summary.rows)
}

func hasSelectPrompt(calls []selectCall, prompt string) bool {
	for _, call := range calls {
		if call.prompt == prompt {
			return true
		}
	}
	return false
}
