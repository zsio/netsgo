package manage

import (
	"strings"
	"testing"

	"netsgo/internal/tui"
	"netsgo/pkg/updater"
)

type mockUI struct {
	selectIndex       int
	selectErr         error
	selectOptionCalls []selectOptionCall
	confirmVal        bool
	confirmErr        error
	confirmCalls      []confirmCall
	summaries         []summaryRecord
}

type summaryRecord struct {
	title string
	rows  [][2]string
}

func (m *mockUI) Select(prompt string, options []string) (int, error) {
	return m.selectIndex, m.selectErr
}
func (m *mockUI) SelectWithOptions(prompt string, options []tui.SelectOption) (int, error) {
	m.selectOptionCalls = append(m.selectOptionCalls, selectOptionCall{
		prompt:  prompt,
		options: append([]tui.SelectOption(nil), options...),
	})
	return m.Select(prompt, nil)
}
func (m *mockUI) Confirm(prompt string) (bool, error) {
	return m.confirmVal, m.confirmErr
}
func (m *mockUI) ConfirmWithOptions(prompt string, opts tui.ConfirmOptions) (bool, error) {
	m.confirmCalls = append(m.confirmCalls, confirmCall{prompt: prompt, confirmText: opts.ConfirmText})
	return m.Confirm(prompt)
}
func (m *mockUI) PrintSummary(title string, rows [][2]string) {
	m.summaries = append(m.summaries, summaryRecord{title: title, rows: rows})
}

func TestRunUpdate_NoServices(t *testing.T) {
	ui := &mockUI{}
	err := runUpdate(ui, "v1.0.0", func() bool { return false })
	if err == nil {
		t.Fatal("expected error when no services installed")
	}
}

func TestRunUpdate_DevVersion(t *testing.T) {
	ui := &mockUI{}
	err := runUpdate(ui, "dev", func() bool { return true })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "更新" {
		t.Fatalf("expected '更新' summary, got %v", ui.summaries)
	}
	assertSummaryRow(t, ui.summaries[0], "托管服务", "正式 release 可在 netsgo manage 中选择“更新”")
	assertSummaryRow(t, ui.summaries[0], "已有新版 netsgo 文件", "执行新版文件的 netsgo upgrade")
	assertSummaryDoesNotContain(t, ui.summaries[0], "检查、确认、下载、校验")
	assertSummaryDoesNotContain(t, ui.summaries[0], "用该 netsgo 可执行文件")
	assertSummaryDoesNotContain(t, ui.summaries[0], "用该文件运行")
}

func TestRunUpdate_NoUpdateAvailable(t *testing.T) {
	ui := &mockUI{}
	applyCalled := 0
	mockCheck := func(_ updater.DownloadChannel, ver string) (*updater.Result, bool, error) {
		return &updater.Result{OldVersion: ver, NewVersion: ver}, false, nil
	}
	mockApply := func(_ updater.DownloadChannel, currentVersion, targetVersion string) (*updater.Result, error) {
		applyCalled++
		return &updater.Result{OldVersion: currentVersion, NewVersion: targetVersion}, nil
	}
	err := runUpdateWithChecker(ui, "v1.0.0", func() bool { return true }, mockCheck, mockApply)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyCalled != 0 {
		t.Fatalf("expected apply not to run, got %d", applyCalled)
	}
	if ui.confirmVal {
		t.Fatal("unexpected confirm state")
	}
	found := false
	for _, s := range ui.summaries {
		if s.title == "无需更新" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected '无需更新' summary, got %v", ui.summaries)
	}
}

func assertSummaryRow(t *testing.T, summary summaryRecord, key, want string) {
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

func assertSummaryDoesNotContain(t *testing.T, summary summaryRecord, notWant string) {
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

func TestRunUpdate_NoUpdateAvailable_WhenLatestHasVPrefix(t *testing.T) {
	ui := &mockUI{}
	applyCalled := 0
	mockCheck := func(_ updater.DownloadChannel, ver string) (*updater.Result, bool, error) {
		return &updater.Result{OldVersion: ver, NewVersion: "v" + ver}, false, nil
	}
	mockApply := func(_ updater.DownloadChannel, currentVersion, targetVersion string) (*updater.Result, error) {
		applyCalled++
		return &updater.Result{OldVersion: currentVersion, NewVersion: targetVersion}, nil
	}
	err := runUpdateWithChecker(ui, "1.0.0", func() bool { return true }, mockCheck, mockApply)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyCalled != 0 {
		t.Fatalf("expected apply not to run, got %d", applyCalled)
	}
	if len(ui.summaries) == 0 || ui.summaries[len(ui.summaries)-1].title != "无需更新" {
		t.Fatalf("expected last summary to be 无需更新, got %v", ui.summaries)
	}
}

func TestRunUpdate_NoUpdateAvailable_WhenOnlyBuildMetadataDiffers(t *testing.T) {
	ui := &mockUI{}
	applyCalled := 0
	mockCheck := func(_ updater.DownloadChannel, ver string) (*updater.Result, bool, error) {
		return &updater.Result{OldVersion: ver, NewVersion: "v1.0.0+build.2"}, false, nil
	}
	mockApply := func(_ updater.DownloadChannel, currentVersion, targetVersion string) (*updater.Result, error) {
		applyCalled++
		return &updater.Result{OldVersion: currentVersion, NewVersion: targetVersion}, nil
	}
	err := runUpdateWithChecker(ui, "1.0.0+build.1", func() bool { return true }, mockCheck, mockApply)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyCalled != 0 {
		t.Fatalf("expected apply not to run, got %d", applyCalled)
	}
	if len(ui.summaries) == 0 || ui.summaries[len(ui.summaries)-1].title != "无需更新" {
		t.Fatalf("expected last summary to be 无需更新, got %v", ui.summaries)
	}
}

func TestRunUpdate_ConfirmsBeforeApplyConfirmedUpdate(t *testing.T) {
	ui := &mockUI{confirmVal: true}
	checkCalled := 0
	applyCalled := 0
	mockCheck := func(channel updater.DownloadChannel, ver string) (*updater.Result, bool, error) {
		checkCalled++
		if channel != updater.ChannelGitHub {
			t.Fatalf("unexpected channel: %q", channel)
		}
		return &updater.Result{OldVersion: ver, NewVersion: "v1.1.0"}, true, nil
	}
	mockApply := func(channel updater.DownloadChannel, currentVersion, targetVersion string) (*updater.Result, error) {
		applyCalled++
		if channel != updater.ChannelGitHub {
			t.Fatalf("unexpected channel: %q", channel)
		}
		if currentVersion != "v1.0.0" || targetVersion != "v1.1.0" {
			t.Fatalf("unexpected apply args: current=%q target=%q", currentVersion, targetVersion)
		}
		return &updater.Result{OldVersion: currentVersion, NewVersion: targetVersion, Stopped: []string{"netsgo-server.service"}, Started: []string{"netsgo-server.service"}}, nil
	}

	err := runUpdateWithChecker(ui, "v1.0.0", func() bool { return true }, mockCheck, mockApply)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checkCalled != 1 {
		t.Fatalf("expected one update check, got %d", checkCalled)
	}
	if applyCalled != 1 {
		t.Fatalf("expected one confirmed apply call, got %d", applyCalled)
	}
	if len(ui.summaries) < 3 {
		t.Fatalf("expected checking, available, complete summaries, got %v", ui.summaries)
	}
	if ui.summaries[1].title != "发现可用更新" {
		t.Fatalf("expected second summary to be 发现可用更新, got %q", ui.summaries[1].title)
	}
	assertSelectOptionsDescribed(t, ui.selectOptionCalls, "选择下载通道")
	if ui.summaries[len(ui.summaries)-1].title != "更新完成" {
		t.Fatalf("expected last summary to be 更新完成, got %q", ui.summaries[len(ui.summaries)-1].title)
	}
	assertConfirmPhrase(t, ui.confirmCalls, "应用此更新？", "apply update")
}

func TestRunUpdate_CancelledBeforeApplyConfirmedUpdate(t *testing.T) {
	ui := &mockUI{confirmVal: false}
	applyCalled := 0
	mockCheck := func(_ updater.DownloadChannel, ver string) (*updater.Result, bool, error) {
		return &updater.Result{OldVersion: ver, NewVersion: "v1.1.0"}, true, nil
	}
	mockApply := func(_ updater.DownloadChannel, currentVersion, targetVersion string) (*updater.Result, error) {
		applyCalled++
		return &updater.Result{OldVersion: currentVersion, NewVersion: targetVersion}, nil
	}

	err := runUpdateWithChecker(ui, "v1.0.0", func() bool { return true }, mockCheck, mockApply)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyCalled != 0 {
		t.Fatalf("expected apply not to run, got %d", applyCalled)
	}
	if len(ui.summaries) == 0 || ui.summaries[len(ui.summaries)-1].title != "更新已取消" {
		t.Fatalf("expected last summary to be 更新已取消, got %v", ui.summaries)
	}
}

func TestRunUpdate_DoesNotConfirmWhenCheckerSaysNoUpdateWithDifferentPresentation(t *testing.T) {
	ui := &mockUI{confirmVal: true}
	applyCalled := 0
	mockCheck := func(_ updater.DownloadChannel, ver string) (*updater.Result, bool, error) {
		return &updater.Result{OldVersion: ver, NewVersion: "v9.9.9"}, false, nil
	}
	mockApply := func(_ updater.DownloadChannel, currentVersion, targetVersion string) (*updater.Result, error) {
		applyCalled++
		return &updater.Result{OldVersion: currentVersion, NewVersion: targetVersion}, nil
	}

	err := runUpdateWithChecker(ui, "v1.0.0", func() bool { return true }, mockCheck, mockApply)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyCalled != 0 {
		t.Fatalf("expected apply not to run, got %d", applyCalled)
	}
	for _, s := range ui.summaries {
		if s.title == "发现可用更新" || s.title == "更新已取消" || s.title == "更新完成" {
			t.Fatalf("unexpected summary when no update needed: %v", ui.summaries)
		}
	}
}
