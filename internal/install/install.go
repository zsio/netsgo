package install

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/term"
	"netsgo/internal/tui"
)

type uiProvider interface {
	Select(prompt string, options []string) (int, error)
	Input(prompt string, opts ...tui.InputOptions) (string, error)
	Password(prompt string, opts ...tui.InputOptions) (string, error)
	Confirm(prompt string) (bool, error)
	PrintSummary(title string, rows [][2]string)
}

type defaultUI struct{}

func (defaultUI) Select(prompt string, options []string) (int, error) {
	return tui.Select(prompt, options)
}
func (defaultUI) Input(prompt string, opts ...tui.InputOptions) (string, error) {
	return tui.Input(prompt, opts...)
}
func (defaultUI) Password(prompt string, opts ...tui.InputOptions) (string, error) {
	return tui.Password(prompt, opts...)
}
func (defaultUI) Confirm(prompt string) (bool, error)         { return tui.Confirm(prompt) }
func (defaultUI) PrintSummary(title string, rows [][2]string) { tui.PrintSummary(title, rows) }

type Deps struct {
	GOOS          string
	HasTTY        bool
	UID           int
	HasSystemd    bool
	UI            uiProvider
	InstallServer func() error
	InstallClient func() error
	Exec          func(argv0 string, argv []string, envv []string) error
}

func Run() error {
	return RunWith(Deps{
		GOOS:       runtime.GOOS,
		HasTTY:     term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		UID:        os.Getuid(),
		HasSystemd: hasSystemd(),
		UI:         defaultUI{},
		InstallServer: func() error {
			return InstallServer()
		},
		InstallClient: func() error {
			return InstallClient()
		},
		Exec: syscall.Exec,
	})
}

func RunWith(deps Deps) error {
	if deps.GOOS != "linux" {
		return errors.New("install is only supported on Linux")
	}
	if !deps.HasSystemd {
		return errors.New("install requires systemd")
	}
	if !deps.HasTTY {
		return errors.New("install must be run in an interactive TTY")
	}
	if deps.UID != 0 {
		return deps.Exec("/usr/bin/sudo", append([]string{"sudo"}, os.Args...), os.Environ())
	}
	if deps.UI == nil {
		deps.UI = defaultUI{}
	}
	if deps.InstallServer == nil || deps.InstallClient == nil {
		return errors.New("install dependencies are incomplete")
	}

	role, err := deps.UI.Select("Select installation role", []string{"Server", "Client"})
	if err != nil {
		return err
	}
	if role == 0 {
		return deps.InstallServer()
	}
	return deps.InstallClient()
}

func hasSystemd() bool {
	if _, err := os.Stat("/run/systemd/private"); err == nil {
		return true
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}
