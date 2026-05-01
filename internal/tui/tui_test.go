package tui

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func capturePrintSummary(title string, rows [][2]string) string {
	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w
	PrintSummary(title, rows)
	_ = w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestParseConfirmAnswerYesNo(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{input: "yes", want: true},
		{input: "Y", want: true},
		{input: " no ", want: false},
		{input: "n", want: false},
	}

	for _, tt := range tests {
		got, err := parseConfirmAnswer(tt.input, ConfirmOptions{})
		if err != nil {
			t.Fatalf("parseConfirmAnswer(%q) unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("parseConfirmAnswer(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFormatSelectOptionIncludesDescription(t *testing.T) {
	got := formatSelectOption(SelectOption{
		Label:       "Status",
		Description: "Show whether the service is installed, running, and enabled.",
	})
	want := "Status - Show whether the service is installed, running, and enabled."
	if got != want {
		t.Fatalf("formatSelectOption() = %q, want %q", got, want)
	}

	got = formatSelectOption(SelectOption{Label: "Back"})
	if got != "Back" {
		t.Fatalf("formatSelectOption without description = %q, want Back", got)
	}
}

func TestParseConfirmAnswerRequiresPhrase(t *testing.T) {
	got, err := parseConfirmAnswer("remove server data", ConfirmOptions{ConfirmText: "remove server data"})
	if err != nil {
		t.Fatalf("parseConfirmAnswer should accept the required phrase: %v", err)
	}
	if !got {
		t.Fatal("required phrase should confirm")
	}

	got, err = parseConfirmAnswer("no", ConfirmOptions{ConfirmText: "remove server data"})
	if err != nil {
		t.Fatalf("parseConfirmAnswer should allow no to cancel: %v", err)
	}
	if got {
		t.Fatal("no should cancel")
	}

	if _, err := parseConfirmAnswer("yes", ConfirmOptions{ConfirmText: "remove server data"}); err == nil {
		t.Fatal("yes should not satisfy a concrete required phrase")
	}
}

func TestConfirmDescriptionCanNameNoAction(t *testing.T) {
	got := confirmDescription(ConfirmOptions{
		ConfirmText:       "remove binary",
		CancelDescription: "保留共享二进制",
	})
	want := "输入 \"remove binary\" 继续，或输入 no 保留共享二进制。"
	if got != want {
		t.Fatalf("confirmDescription() = %q, want %q", got, want)
	}
}

func TestChineseKeyMapLocalizesOrdinaryHelpLabels(t *testing.T) {
	keyMap := chineseKeyMap()

	if got := keyMap.Select.Up.Help().Desc; got != "上移" {
		t.Fatalf("select up help = %q, want 上移", got)
	}
	if got := keyMap.Select.Filter.Help().Desc; got != "筛选" {
		t.Fatalf("select filter help = %q, want 筛选", got)
	}
	if got := keyMap.Select.Submit.Help().Desc; got != "提交" {
		t.Fatalf("select submit help = %q, want 提交", got)
	}
	if got := keyMap.Input.Submit.Help().Desc; got != "提交" {
		t.Fatalf("input submit help = %q, want 提交", got)
	}
}

func TestIsCancelled(t *testing.T) {
	if !IsCancelled(huh.ErrUserAborted) {
		t.Fatal("huh.ErrUserAborted should be treated as cancellation")
	}
	if !IsCancelled(ErrCancelled) {
		t.Fatal("ErrCancelled should be treated as cancellation")
	}
	if IsCancelled(errors.New("other")) {
		t.Fatal("unrelated errors should not be treated as cancellation")
	}
}

func TestPrintSummary(t *testing.T) {
	output := capturePrintSummary("Installation Config", [][2]string{{"Role", "server"}, {"Port", "8080"}})
	if !strings.Contains(output, "Installation Config") {
		t.Fatalf("summary output missing title: %q", output)
	}
	if !strings.Contains(output, "Role") || !strings.Contains(output, "server") {
		t.Fatalf("summary output missing role row: %q", output)
	}
	if !strings.Contains(output, "Port") || !strings.Contains(output, "8080") {
		t.Fatalf("summary output missing port row: %q", output)
	}
}
