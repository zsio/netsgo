package manage

import (
	"testing"

	"netsgo/pkg/updater"
)

type mockUI struct {
	selectIndex int
	selectErr   error
	confirmVal  bool
	confirmErr  error
	summaries   []summaryRecord
}

type summaryRecord struct {
	title string
	rows  [][2]string
}

func (m *mockUI) Select(prompt string, options []string) (int, error) {
	return m.selectIndex, m.selectErr
}
func (m *mockUI) Confirm(prompt string) (bool, error) {
	return m.confirmVal, m.confirmErr
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
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Update" {
		t.Fatalf("expected 'Update' summary, got %v", ui.summaries)
	}
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
		if s.title == "No update" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'No update' summary, got %v", ui.summaries)
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
	if len(ui.summaries) == 0 || ui.summaries[len(ui.summaries)-1].title != "No update" {
		t.Fatalf("expected last summary to be No update, got %v", ui.summaries)
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
	if len(ui.summaries) == 0 || ui.summaries[len(ui.summaries)-1].title != "No update" {
		t.Fatalf("expected last summary to be No update, got %v", ui.summaries)
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
	if ui.summaries[1].title != "Update available" {
		t.Fatalf("expected second summary to be Update available, got %q", ui.summaries[1].title)
	}
	if ui.summaries[len(ui.summaries)-1].title != "Update complete" {
		t.Fatalf("expected last summary to be Update complete, got %q", ui.summaries[len(ui.summaries)-1].title)
	}
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
	if len(ui.summaries) == 0 || ui.summaries[len(ui.summaries)-1].title != "Update cancelled" {
		t.Fatalf("expected last summary to be Update cancelled, got %v", ui.summaries)
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
		if s.title == "Update available" || s.title == "Update cancelled" || s.title == "Update complete" {
			t.Fatalf("unexpected summary when no update needed: %v", ui.summaries)
		}
	}
}
