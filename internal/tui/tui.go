package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
)

// ErrCancelled marks an intentional interactive cancellation.
var ErrCancelled = errors.New("cancelled")

// IsCancelled reports whether err represents a user-initiated interactive abort.
func IsCancelled(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrCancelled) ||
		errors.Is(err, huh.ErrUserAborted) ||
		err.Error() == "user aborted"
}

func normalizeFormError(err error) error {
	if IsCancelled(err) {
		return ErrCancelled
	}
	return err
}

// SelectOption is a single select-menu option with optional explanatory copy.
type SelectOption struct {
	Label       string
	Description string
}

// Select presents an interactive list and returns the 0-based index of the chosen item.
func Select(prompt string, options []string) (int, error) {
	selectOptions := make([]SelectOption, len(options))
	for i, option := range options {
		selectOptions[i] = SelectOption{Label: option}
	}
	return SelectWithOptions(prompt, selectOptions)
}

// SelectWithOptions presents an interactive list with optional per-choice descriptions.
func SelectWithOptions(prompt string, options []SelectOption) (int, error) {
	var result int
	huhOptions := make([]huh.Option[int], len(options))
	for i, opt := range options {
		huhOptions[i] = huh.NewOption(formatSelectOption(opt), i)
	}
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title(prompt).
				Options(huhOptions...).
				Value(&result),
		).WithKeyMap(chineseKeyMap()),
	).Run()
	if err != nil {
		return 0, normalizeFormError(err)
	}
	return result, nil
}

func formatSelectOption(option SelectOption) string {
	label := strings.TrimSpace(option.Label)
	description := strings.TrimSpace(option.Description)
	if description == "" {
		return label
	}
	return label + " - " + description
}

// InputOptions holds optional configuration for Input and Password prompts.
type InputOptions struct {
	Placeholder string
	Description string
	Default     string
	Validate    func(string) error
}

// Input prompts for a single line of text.
func Input(prompt string, opts ...InputOptions) (string, error) {
	var result string
	if len(opts) > 0 && opts[0].Default != "" {
		result = opts[0].Default
	}
	field := huh.NewInput().
		Title(prompt).
		Value(&result)
	if len(opts) > 0 {
		if opts[0].Placeholder != "" {
			field = field.Placeholder(opts[0].Placeholder)
		}
		if opts[0].Description != "" {
			field = field.Description(opts[0].Description)
		}
		if opts[0].Validate != nil {
			field = field.Validate(opts[0].Validate)
		}
	}
	err := huh.NewForm(huh.NewGroup(field).WithKeyMap(chineseKeyMap())).Run()
	return result, normalizeFormError(err)
}

// Password prompts for a secret value with masked echo.
func Password(prompt string, opts ...InputOptions) (string, error) {
	var result string
	if len(opts) > 0 && opts[0].Default != "" {
		result = opts[0].Default
	}
	field := huh.NewInput().
		Title(prompt).
		EchoMode(huh.EchoModePassword).
		Value(&result)
	if len(opts) > 0 {
		if opts[0].Placeholder != "" {
			field = field.Placeholder(opts[0].Placeholder)
		}
		if opts[0].Description != "" {
			field = field.Description(opts[0].Description)
		}
		if opts[0].Validate != nil {
			field = field.Validate(opts[0].Validate)
		}
	}
	err := huh.NewForm(huh.NewGroup(field).WithKeyMap(chineseKeyMap())).Run()
	return result, normalizeFormError(err)
}

// ConfirmOptions holds optional configuration for Confirm prompts.
type ConfirmOptions struct {
	ConfirmText       string
	CancelDescription string
}

// Confirm asks a yes/no question and returns the boolean answer.
func Confirm(prompt string) (bool, error) {
	return ConfirmWithOptions(prompt, ConfirmOptions{})
}

// ConfirmWithOptions asks a yes/no question with explicit prompt options.
func ConfirmWithOptions(prompt string, opts ConfirmOptions) (bool, error) {
	var answer string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(prompt).
				Description(confirmDescription(opts)).
				Value(&answer).
				Validate(func(value string) error {
					_, err := parseConfirmAnswer(value, opts)
					return err
				}),
		).WithKeyMap(chineseKeyMap()),
	).Run()
	if err != nil {
		return false, normalizeFormError(err)
	}
	return parseConfirmAnswer(answer, opts)
}

func confirmDescription(opts ConfirmOptions) string {
	cancelDescription := strings.TrimSpace(opts.CancelDescription)
	if cancelDescription == "" {
		cancelDescription = "取消"
	}
	if text := strings.TrimSpace(opts.ConfirmText); text != "" {
		return fmt.Sprintf("输入 %q 继续，或输入 no %s。", text, cancelDescription)
	}
	return fmt.Sprintf("输入 yes 继续，或输入 no %s。", cancelDescription)
}

func parseConfirmAnswer(raw string, opts ConfirmOptions) (bool, error) {
	answer := strings.TrimSpace(raw)
	normalized := strings.ToLower(answer)
	if text := strings.TrimSpace(opts.ConfirmText); text != "" {
		if strings.EqualFold(answer, text) {
			return true, nil
		}
		if isNegativeAnswer(normalized) {
			return false, nil
		}
		cancelDescription := strings.TrimSpace(opts.CancelDescription)
		if cancelDescription == "" {
			cancelDescription = "取消"
		}
		return false, fmt.Errorf("请输入 %q 继续，或输入 no %s", text, cancelDescription)
	}

	switch normalized {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("请输入 yes 或 no")
	}
}

func isNegativeAnswer(value string) bool {
	switch value {
	case "n", "no", "cancel":
		return true
	default:
		return false
	}
}

func chineseKeyMap() *huh.KeyMap {
	k := huh.NewDefaultKeyMap()
	localizeCommonInputHelp(&k.Input.Prev, &k.Input.Next, &k.Input.Submit)
	localizeCommonInputHelp(&k.Note.Prev, &k.Note.Next, &k.Note.Submit)
	localizeCommonInputHelp(&k.Confirm.Prev, &k.Confirm.Next, &k.Confirm.Submit)

	k.Input.AcceptSuggestion.SetHelp("ctrl+e", "补全")

	localizeSelectHelp(&k.Select.Up, &k.Select.Down, &k.Select.Filter, &k.Select.Submit)
	k.Select.Next.SetHelp("enter/tab", "选择")
	k.Select.Left.SetHelp("←", "左移")
	k.Select.Right.SetHelp("→", "右移")
	k.Select.SetFilter.SetHelp("esc", "应用筛选")
	k.Select.ClearFilter.SetHelp("esc", "清除筛选")
	k.Select.HalfPageUp.SetHelp("ctrl+u", "上翻半页")
	k.Select.HalfPageDown.SetHelp("ctrl+d", "下翻半页")
	k.Select.GotoTop.SetHelp("g/home", "到开头")
	k.Select.GotoBottom.SetHelp("G/end", "到结尾")

	localizeSelectHelp(&k.MultiSelect.Up, &k.MultiSelect.Down, &k.MultiSelect.Filter, &k.MultiSelect.Submit)
	k.MultiSelect.Next.SetHelp("enter/tab", "确认")
	k.MultiSelect.Toggle.SetHelp("x", "切换")
	k.MultiSelect.SetFilter.SetHelp("esc", "应用筛选")
	k.MultiSelect.ClearFilter.SetHelp("esc", "清除筛选")
	k.MultiSelect.HalfPageUp.SetHelp("ctrl+u", "上翻半页")
	k.MultiSelect.HalfPageDown.SetHelp("ctrl+d", "下翻半页")
	k.MultiSelect.GotoTop.SetHelp("g/home", "到开头")
	k.MultiSelect.GotoBottom.SetHelp("G/end", "到结尾")
	k.MultiSelect.SelectAll.SetHelp("ctrl+a", "全选")
	k.MultiSelect.SelectNone.SetHelp("ctrl+a", "全不选")

	k.Text.Prev.SetHelp("shift+tab", "返回")
	k.Text.Next.SetHelp("enter/tab", "下一项")
	k.Text.Submit.SetHelp("enter", "提交")
	k.Text.NewLine.SetHelp("alt+enter / ctrl+j", "换行")
	k.Text.Editor.SetHelp("ctrl+e", "打开编辑器")

	return k
}

func localizeCommonInputHelp(prev, next, submit *key.Binding) {
	prev.SetHelp("shift+tab", "返回")
	next.SetHelp("enter/tab", "下一项")
	submit.SetHelp("enter", "提交")
}

func localizeSelectHelp(up, down, filter, submit *key.Binding) {
	up.SetHelp("↑", "上移")
	down.SetHelp("↓", "下移")
	filter.SetHelp("/", "筛选")
	submit.SetHelp("enter", "提交")
}

// PrintSummary prints a titled key-value table to stdout.
func PrintSummary(title string, rows [][2]string) {
	_, _ = fmt.Fprintf(os.Stdout, "\n  %s\n", title)
	for _, row := range rows {
		_, _ = fmt.Fprintf(os.Stdout, "  %-20s %s\n", row[0]+":", row[1])
	}
	_, _ = fmt.Fprintln(os.Stdout)
}
