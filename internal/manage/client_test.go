package manage

import (
	"strings"
	"testing"

	"netsgo/internal/svcmgr"
)

func newInstalledClientDeps(t *testing.T, ui *fakeUI) (clientDeps, svcmgr.ServiceLayout) {
	t.Helper()

	layout := svcmgr.NewLayout(svcmgr.RoleClient)

	return clientDeps{
		UI: ui,
		Inspect: func() svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: svcmgr.RoleClient, State: svcmgr.StateInstalled}
		},
		IsActive:  func() (bool, error) { return true, nil },
		IsEnabled: func() (bool, error) { return true, nil },
		Logs:      func() error { return nil },
		RunInstall: func() error {
			return nil
		},
		ReadClientEnv: func() (svcmgr.ClientEnv, error) {
			return svcmgr.ClientEnv{
				Server:         "wss://panel.example.com",
				Key:            "sk-secret",
				TLSSkipVerify:  true,
				TLSFingerprint: "sha256:example",
			}, nil
		},
		DisableAndStop: func() error { return nil },
		EnableAndStart: func() error { return nil },
		DaemonReload:   func() error { return nil },
		RemovePaths:    func(paths ...string) error { return nil },
		RemoveBinary:   func() error { return nil },
		DetectServer: func() svcmgr.InstallState {
			return svcmgr.StateInstalled
		},
	}, layout
}

func TestManageClientInspectRedactsKey(t *testing.T) {
	ui := &fakeUI{selects: []int{1, 7}}
	deps, _ := newInstalledClientDeps(t, ui)

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) == 0 {
		t.Fatal("inspect should output a summary")
	}
	assertSelectOptionsDescribed(t, ui.selectOptionCalls, "选择 client 操作")
	assertSummaryCallRow(t, ui.summaries[0], "Client 本地状态", "本地状态文件未发现")
	assertSummaryCallDoesNotContain(t, ui.summaries[0], "身份")
	for _, row := range ui.summaries[0].rows {
		if strings.Contains(strings.ToLower(row[0]), "key") || strings.Contains(strings.ToLower(row[1]), "sk-") {
			t.Fatalf("inspect should not expose the client key: %#v", ui.summaries[0].rows)
		}
	}
}

func TestManageClientUninstallRemovesData(t *testing.T) {
	ui := &fakeUI{selects: []int{6}, confirms: []bool{true}}
	removed := []string{}
	deps, spec := newInstalledClientDeps(t, ui)
	deps.RemovePaths = func(paths ...string) error {
		removed = append(removed, paths...)
		return nil
	}

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	found := false
	for _, path := range removed {
		if path == clientDataPath(spec) {
			found = true
		}
	}
	if !found {
		t.Fatalf("client uninstall should remove the client data dir: %v", removed)
	}
	assertSummaryCallDoesNotContain(t, ui.summaries[0], "身份")
	assertSummaryCallDoesNotContain(t, ui.summaries[len(ui.summaries)-1], "身份")
	assertSummaryCallRow(t, ui.summaries[len(ui.summaries)-1], "下一步", "需要时运行 netsgo install 重新安装 client")
	assertConfirmPhrase(t, ui.confirmCalls, "继续卸载 client？", "uninstall client")
}

func TestManageClientRestart(t *testing.T) {
	ui := &fakeUI{selects: []int{5, 7}}
	stopped := false
	started := false
	deps, _ := newInstalledClientDeps(t, ui)
	deps.DisableAndStop = func() error { stopped = true; return nil }
	deps.EnableAndStart = func() error { started = true; return nil }

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if !stopped || !started {
		t.Fatalf("restart should stop before start, stopped=%v started=%v", stopped, started)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "操作成功" {
		t.Fatalf("restart should show a success message, got %#v", ui.summaries)
	}
}

func TestManageClientLogs(t *testing.T) {
	ui := &fakeUI{selects: []int{2}}
	called := false
	deps, _ := newInstalledClientDeps(t, ui)
	deps.Logs = func() error {
		called = true
		return nil
	}

	err := ManageClientWith(deps)
	if err != nil {
		t.Fatalf("logs should not fail: %v", err)
	}
	if !called {
		t.Fatal("logs should delegate to the journald execution function")
	}
}

func TestManageClientStartPrintsSuccess(t *testing.T) {
	ui := &fakeUI{selects: []int{3, 7}}
	deps, _ := newInstalledClientDeps(t, ui)

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) != 1 || ui.summaries[0].title != "操作成功" {
		t.Fatalf("start should show a success message, got %#v", ui.summaries)
	}
}

func TestManageClientUninstallCancelPrintsCancelled(t *testing.T) {
	ui := &fakeUI{selects: []int{6, 7}, confirms: []bool{false}}
	deps, _ := newInstalledClientDeps(t, ui)

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if len(ui.summaries) != 2 || ui.summaries[1].title != "已取消" {
		t.Fatalf("canceling uninstall should show a canceled message, got %#v", ui.summaries)
	}
	assertSummaryCallRow(t, ui.summaries[1], "下一步", "选择其他操作，或选择返回")
}

func TestManageClientUninstallLastRoleCanKeepSharedBinary(t *testing.T) {
	ui := &fakeUI{selects: []int{6}, confirms: []bool{true, false}}
	binaryRemoved := false
	deps, _ := newInstalledClientDeps(t, ui)
	deps.DetectServer = func() svcmgr.InstallState { return svcmgr.StateNotInstalled }
	deps.RemoveBinary = func() error {
		binaryRemoved = true
		return nil
	}

	err := ManageClientWith(deps)
	assertSelectionExit(t, err)

	if binaryRemoved {
		t.Fatal("expected the final shared-binary confirmation to allow keeping the binary")
	}
	assertConfirmPhrase(t, ui.confirmCalls, "继续卸载 client？", "uninstall client")
	assertConfirmPhrase(t, ui.confirmCalls, "未检测到其他托管角色。是否同时移除共享二进制 /usr/local/bin/netsgo？", "remove binary")
}

func TestCleanupBrokenClientRequiresCleanupPhrase(t *testing.T) {
	ui := &fakeUI{confirms: []bool{true}}
	deps, _ := newInstalledClientDeps(t, ui)

	cleaned, err := cleanupBrokenClient(deps)
	if err != nil {
		t.Fatalf("cleanupBrokenClient should not fail: %v", err)
	}
	if !cleaned {
		t.Fatal("cleanupBrokenClient should report cleanup complete")
	}
	assertSummaryCallDoesNotContain(t, ui.summaries[0], "身份")
	assertConfirmPhrase(t, ui.confirmCalls, "继续清理异常 client？", "cleanup client")
}

func assertSummaryCallDoesNotContain(t *testing.T, summary summaryCall, notWant string) {
	t.Helper()
	for _, row := range summary.rows {
		if strings.Contains(row[0], notWant) || strings.Contains(row[1], notWant) {
			t.Fatalf("summary should not contain %q: %#v", notWant, summary.rows)
		}
	}
}
