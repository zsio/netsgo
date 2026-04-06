package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

type UI struct {
	In  io.Reader
	Out io.Writer
}

var defaultUI = UI{In: os.Stdin, Out: os.Stdout}
var readerCache sync.Map

func Select(prompt string, options []string) (int, error) {
	return defaultUI.Select(prompt, options)
}

func Input(prompt string) (string, error) {
	return defaultUI.Input(prompt)
}

func Password(prompt string) (string, error) {
	return defaultUI.Password(prompt)
}

func Confirm(prompt string) (bool, error) {
	return defaultUI.Confirm(prompt)
}

func PrintSummary(title string, rows [][2]string) {
	defaultUI.PrintSummary(title, rows)
}

func (ui UI) normalized() UI {
	if ui.In == nil {
		ui.In = os.Stdin
	}
	if ui.Out == nil {
		ui.Out = os.Stdout
	}
	return ui
}

func (ui UI) Select(prompt string, options []string) (int, error) {
	ui = ui.normalized()
	if _, err := fmt.Fprintf(ui.Out, "%s\n", prompt); err != nil {
		return 0, err
	}
	for i, option := range options {
		if _, err := fmt.Fprintf(ui.Out, "%d. %s\n", i+1, option); err != nil {
			return 0, err
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		value, err := ui.Input(fmt.Sprintf("请输入序号 (1-%d)", len(options)))
		if err != nil {
			return 0, err
		}
		var selected int
		if _, err := fmt.Sscanf(value, "%d", &selected); err == nil {
			index := selected - 1
			if index >= 0 && index < len(options) {
				return index, nil
			}
		}
		if _, err := fmt.Fprintf(ui.Out, "无效输入，请输入 1-%d 之间的数字\n", len(options)); err != nil {
			return 0, err
		}
	}
	return 0, fmt.Errorf("已重试 3 次，仍未获得有效输入")
}

func (ui UI) Input(prompt string) (string, error) {
	ui = ui.normalized()
	if _, err := fmt.Fprintf(ui.Out, "%s: ", prompt); err != nil {
		return "", err
	}
	reader := getReader(ui.In)
	value, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func (ui UI) Password(prompt string) (string, error) {
	ui = ui.normalized()
	if ui.In == os.Stdin && ui.Out == os.Stdout {
		if _, err := fmt.Fprintf(ui.Out, "%s: ", prompt); err != nil {
			return "", err
		}
		value, err := term.ReadPassword(int(os.Stdin.Fd()))
		if _, printErr := fmt.Fprintln(ui.Out); printErr != nil && err == nil {
			err = printErr
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(value)), nil
	}
	return ui.Input(prompt)
}

func (ui UI) Confirm(prompt string) (bool, error) {
	value, err := ui.Input(prompt + " [y/N]")
	if err != nil {
		return false, err
	}
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "y" || value == "yes", nil
}

func (ui UI) PrintSummary(title string, rows [][2]string) {
	ui = ui.normalized()
	_, _ = fmt.Fprintf(ui.Out, "%s\n", title)
	for _, row := range rows {
		_, _ = fmt.Fprintf(ui.Out, "%s: %s\n", row[0], row[1])
	}
}

func getReader(in io.Reader) *bufio.Reader {
	if reader, ok := in.(*bufio.Reader); ok {
		return reader
	}
	if cached, ok := readerCache.Load(in); ok {
		return cached.(*bufio.Reader)
	}
	reader := bufio.NewReader(in)
	actual, _ := readerCache.LoadOrStore(in, reader)
	return actual.(*bufio.Reader)
}
