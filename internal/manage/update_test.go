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
	summaries   []string
}

func (m *mockUI) Select(prompt string, options []string) (int, error) {
	return m.selectIndex, m.selectErr
}
func (m *mockUI) Confirm(prompt string) (bool, error) {
	return m.confirmVal, m.confirmErr
}
func (m *mockUI) PrintSummary(title string, rows [][2]string) {
	m.summaries = append(m.summaries, title)
}

func TestRunUpdate_NoServices(t *testing.T) {
	ui := &mockUI{}
	err := runUpdate(ui, "v1.0.0", func() bool { return false }, nil)
	if err == nil {
		t.Fatal("expected error when no services installed")
	}
}

func TestRunUpdate_DevVersion(t *testing.T) {
	ui := &mockUI{}
	err := runUpdate(ui, "dev", func() bool { return true }, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0] != "Update" {
		t.Fatalf("expected 'Update' summary, got %v", ui.summaries)
	}
}

func TestRunUpdate_NoUpdateAvailable(t *testing.T) {
	ui := &mockUI{}
	mockUpdate := func(_ updater.DownloadChannel, ver string) (*updater.Result, error) {
		return &updater.Result{OldVersion: ver, NewVersion: ver}, nil
	}
	err := runUpdate(ui, "v1.0.0", func() bool { return true }, mockUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, s := range ui.summaries {
		if s == "No update" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'No update' summary, got %v", ui.summaries)
	}
}
