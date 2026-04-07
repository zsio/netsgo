package tui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
)

// Select presents an interactive list and returns the 0-based index of the chosen item.
func Select(prompt string, options []string) (int, error) {
	var result int
	huhOptions := make([]huh.Option[int], len(options))
	for i, opt := range options {
		huhOptions[i] = huh.NewOption(opt, i)
	}
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title(prompt).
				Options(huhOptions...).
				Value(&result),
		),
	).Run()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// Input prompts for a single line of text.
func Input(prompt string) (string, error) {
	var result string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(prompt).
				Value(&result),
		),
	).Run()
	return result, err
}

// Password prompts for a secret value with masked echo.
func Password(prompt string) (string, error) {
	var result string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(prompt).
				EchoMode(huh.EchoModePassword).
				Value(&result),
		),
	).Run()
	return result, err
}

// Confirm asks a yes/no question and returns the boolean answer.
func Confirm(prompt string) (bool, error) {
	var result bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(prompt).
				Affirmative("是").
				Negative("否").
				Value(&result),
		),
	).Run()
	return result, err
}

// PrintSummary prints a titled key-value table to stdout.
func PrintSummary(title string, rows [][2]string) {
	fmt.Fprintf(os.Stdout, "\n  %s\n", title)
	for _, row := range rows {
		fmt.Fprintf(os.Stdout, "  %-20s %s\n", row[0]+":", row[1])
	}
	fmt.Fprintln(os.Stdout)
}
