package manage

import (
	"errors"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/term"
	"netsgo/internal/install"
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
	RunInstall   func() error
	UninstallAll func() error
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
		RunInstall: func() error {
			return install.Run()
		},
		UninstallAll: func() error {
			return UninstallAll()
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
	for {
		serverInspection := deps.Inspect(svcmgr.RoleServer)
		clientInspection := deps.Inspect(svcmgr.RoleClient)
		serverState := serverInspection.State
		clientState := clientInspection.State

		switch {
		case serverState == svcmgr.StateInstalled && clientState == svcmgr.StateInstalled:
			if deps.ManageServer == nil || deps.ManageClient == nil {
				return errors.New("manage dependencies are incomplete")
			}
			shouldContinue, err := runDualInstalledMenu(deps)
			if err != nil {
				return err
			}
			if shouldContinue {
				continue
			}
			return nil

		case serverState == svcmgr.StateInstalled:
			if deps.ManageServer == nil {
				return errors.New("manage dependencies are incomplete")
			}
			printDegradedSummary(deps.UI, clientInspection)
			err := deps.ManageServer()
			if errors.Is(err, errReturnToSelection) {
				return nil
			}
			return err

		case clientState == svcmgr.StateInstalled:
			if deps.ManageClient == nil {
				return errors.New("manage dependencies are incomplete")
			}
			printDegradedSummary(deps.UI, serverInspection)
			err := deps.ManageClient()
			if errors.Is(err, errReturnToSelection) {
				return nil
			}
			return err

		case serverState == svcmgr.StateNotInstalled && clientState == svcmgr.StateNotInstalled:
			shouldContinue, err := runNoInstalledMenu(deps)
			if err != nil {
				return err
			}
			if shouldContinue {
				continue
			}
			return nil

		default:
			if deps.ManageServer == nil || deps.ManageClient == nil {
				return errors.New("manage dependencies are incomplete")
			}
			shouldContinue, err := runRecoveryEntryMenu(deps, serverInspection, clientInspection)
			if err != nil {
				return err
			}
			if shouldContinue {
				continue
			}
			return nil
		}
	}
}

func runDualInstalledMenu(deps Deps) (bool, error) {
	role, err := deps.UI.Select("Select a role to manage", []string{
		"Manage server",
		"Manage client",
		"Uninstall all managed services",
		"Exit",
	})
	if err != nil {
		return false, err
	}

	switch role {
	case 0:
		if deps.ManageServer == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		err = deps.ManageServer()
	case 1:
		if deps.ManageClient == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		err = deps.ManageClient()
	case 2:
		if deps.UninstallAll == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		err = deps.UninstallAll()
	case 3:
		return false, nil
	default:
		return false, nil
	}
	if errors.Is(err, errReturnToSelection) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func runNoInstalledMenu(deps Deps) (bool, error) {
	deps.UI.PrintSummary("No services installed", [][2]string{{"Next step", "Select whether to start netsgo install or exit"}})

	action, err := deps.UI.Select("Select an action", []string{"Run netsgo install", "Exit"})
	if err != nil {
		return false, err
	}
	if action == 0 {
		if deps.RunInstall == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		return false, deps.RunInstall()
	}
	return false, nil
}

func runRecoveryEntryMenu(deps Deps, serverInspection, clientInspection svcmgr.InstallInspection) (bool, error) {
	options := make([]string, 0, 4)
	actions := make([]func() error, 0, 4)

	appendRole := func(label string, fn func() error) {
		options = append(options, label)
		actions = append(actions, fn)
	}

	if serverInspection.State != svcmgr.StateNotInstalled {
		appendRole(recoveryRoleLabel(serverInspection), deps.ManageServer)
	}
	if clientInspection.State != svcmgr.StateNotInstalled {
		appendRole(recoveryRoleLabel(clientInspection), deps.ManageClient)
	}
	options = append(options, "Run netsgo install", "Exit")

	choice, err := deps.UI.Select("Select a recovery action", options)
	if err != nil {
		return false, err
	}
	if choice < len(actions) {
		err = actions[choice]()
		if errors.Is(err, errReturnToSelection) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	}
	if choice == len(actions) {
		if deps.RunInstall == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		return false, deps.RunInstall()
	}
	return false, nil
}

func recoveryRoleLabel(inspection svcmgr.InstallInspection) string {
	role := roleLabel(inspection.Role)
	switch inspection.State {
	case svcmgr.StateHistoricalDataOnly:
		return "Inspect recoverable " + role + " state"
	case svcmgr.StateBroken:
		return "Inspect / cleanup broken " + role + " state"
	default:
		return "Manage " + role
	}
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
