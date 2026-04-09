package manage

import (
	"errors"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/term"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

type uiProvider interface {
	Select(prompt string, options []string) (int, error)
	Confirm(prompt string) (bool, error)
	PrintSummary(title string, rows [][2]string)
}

type defaultUI struct{}

func (defaultUI) Select(prompt string, options []string) (int, error) {
	return tui.Select(prompt, options)
}
func (defaultUI) Confirm(prompt string) (bool, error)         { return tui.Confirm(prompt) }
func (defaultUI) PrintSummary(title string, rows [][2]string) { tui.PrintSummary(title, rows) }

type Deps struct {
	GOOS         string
	HasTTY       bool
	UID          int
	UI           uiProvider
	Inspect      func(svcmgr.Role) svcmgr.InstallInspection
	Detect       func(svcmgr.Role) svcmgr.InstallState
	ManageServer func() error
	ManageClient func() error
	Exec         func(argv0 string, argv []string, envv []string) error
}

func Run() error {
	return RunWith(Deps{
		GOOS:    runtime.GOOS,
		HasTTY:  term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		UID:     os.Getuid(),
		UI:      defaultUI{},
		Inspect: svcmgr.Inspect,
		Detect:  svcmgr.Detect,
		ManageServer: func() error {
			return ManageServer()
		},
		ManageClient: func() error {
			return ManageClient()
		},
		Exec: syscall.Exec,
	})
}

func RunWith(deps Deps) error {
	if deps.GOOS != "linux" {
		return errors.New("manage is only supported on Linux")
	}
	if !deps.HasTTY {
		return errors.New("manage must be run in an interactive TTY")
	}
	if deps.UID != 0 {
		return deps.Exec("/usr/bin/sudo", append([]string{"sudo"}, os.Args...), os.Environ())
	}
	if deps.UI == nil {
		deps.UI = defaultUI{}
	}
	if deps.Inspect == nil {
		if deps.Detect != nil {
			detect := deps.Detect
			deps.Inspect = func(role svcmgr.Role) svcmgr.InstallInspection {
				return svcmgr.InstallInspection{Role: role, State: detect(role)}
			}
		} else {
			deps.Inspect = svcmgr.Inspect
		}
	}
	serverInspection := deps.Inspect(svcmgr.RoleServer)
	clientInspection := deps.Inspect(svcmgr.RoleClient)
	serverState := serverInspection.State
	clientState := clientInspection.State
	if serverState == svcmgr.StateNotInstalled && clientState == svcmgr.StateNotInstalled {
		deps.UI.PrintSummary("No services installed", [][2]string{{"Next step", "Run netsgo install first"}})
		return nil
	}
	if serverState == svcmgr.StateInstalled && clientState == svcmgr.StateInstalled {
		if deps.ManageServer == nil || deps.ManageClient == nil {
			return errors.New("manage dependencies are incomplete")
		}

		role, err := deps.UI.Select("Select a role to manage", []string{"Server (server)", "Client (client)"})
		if err != nil {
			return err
		}
		if role == 0 {
			return deps.ManageServer()
		}
		return deps.ManageClient()
	}

	if serverState == svcmgr.StateInstalled {
		if deps.ManageServer == nil {
			return errors.New("manage dependencies are incomplete")
		}
		printDegradedSummary(deps.UI, clientInspection)
		return deps.ManageServer()
	}
	if clientState == svcmgr.StateInstalled {
		if deps.ManageClient == nil {
			return errors.New("manage dependencies are incomplete")
		}
		printDegradedSummary(deps.UI, serverInspection)
		return deps.ManageClient()
	}

	printDegradedSummary(deps.UI, serverInspection)
	printDegradedSummary(deps.UI, clientInspection)
	return nil
}

func printDegradedSummary(ui uiProvider, inspection svcmgr.InstallInspection) {
	if inspection.State == svcmgr.StateInstalled || inspection.State == svcmgr.StateNotInstalled {
		return
	}

	rows := [][2]string{{"Role", roleLabel(inspection.Role)}, {"State", inspection.State.String()}, {"Advice", degradedAdvice(inspection.Role, inspection.State)}}
	for _, problem := range inspection.Problems {
		rows = append(rows, [2]string{"Problem", problem})
	}
	ui.PrintSummary(degradedTitle(inspection.Role, inspection.State), rows)
}

func degradedTitle(role svcmgr.Role, state svcmgr.InstallState) string {
	if role == svcmgr.RoleServer && state == svcmgr.StateHistoricalDataOnly {
		return "Recoverable server data detected"
	}
	return roleLabel(role) + " installation is in an abnormal state"
}

func degradedAdvice(role svcmgr.Role, state svcmgr.InstallState) string {
	if role == svcmgr.RoleServer && state == svcmgr.StateHistoricalDataOnly {
		return "Run netsgo install to restore the installation (existing configuration will be preserved)"
	}
	return "Run netsgo install to repair the installation, or manually clean up leftover files before reinstalling"
}

func roleLabel(role svcmgr.Role) string {
	if role == svcmgr.RoleServer {
		return "Server"
	}
	return "Client"
}
