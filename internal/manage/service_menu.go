package manage

import (
	"os"
	"strconv"

	"netsgo/internal/svcmgr"
)

var manageActionOptions = []string{"Status", "Details", "Logs", "Start", "Stop", "Restart", "Uninstall"}

type serviceMenuDeps struct {
	UI        uiProvider
	Status    func() (string, error)
	Detail    func() error
	Logs      func() error
	Start     func() error
	Stop      func() error
	Uninstall func() error
}

func runServiceMenu(deps serviceMenuDeps) error {
	action, err := deps.UI.Select("Select an action", manageActionOptions)
	if err != nil {
		return err
	}

	switch action {
	case 0:
		return showStatusSummary(deps.UI, deps.Status)
	case 1:
		return deps.Detail()
	case 2:
		return deps.Logs()
	case 3:
		return runLifecycleAction(deps.UI, "Start", "Started", deps.Start)
	case 4:
		return runLifecycleAction(deps.UI, "Stop", "Stopped", deps.Stop)
	case 5:
		return runRestartAction(deps.UI, deps.Stop, deps.Start)
	case 6:
		return deps.Uninstall()
	default:
		return nil
	}
}

func showStatusSummary(ui uiProvider, statusFn func() (string, error)) error {
	status, err := statusFn()
	if err != nil {
		return err
	}
	ui.PrintSummary("Service status", [][2]string{{"State", status}})
	return nil
}

func runLifecycleAction(ui uiProvider, action, status string, fn func() error) error {
	if err := fn(); err != nil {
		return err
	}
	ui.PrintSummary("Operation successful", [][2]string{{"Action", action}, {"State", status}})
	return nil
}

func runRestartAction(ui uiProvider, stop, start func() error) error {
	if err := stop(); err != nil {
		return err
	}
	if err := start(); err != nil {
		return err
	}
	ui.PrintSummary("Operation successful", [][2]string{{"Action", "Restart"}, {"State", "Restarted"}})
	return nil
}

func printManageCancelled(ui uiProvider) {
	ui.PrintSummary("Cancelled", [][2]string{{"Next step", "Run netsgo manage to continue managing services"}})
}

func maybeRemoveSharedBinary(otherRoleState func() svcmgr.InstallState, removeBinary func() error) error {
	if otherRoleState() != svcmgr.StateNotInstalled {
		return nil
	}
	return removeBinary()
}

func itoa(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func boolLabel(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

func removePaths(paths ...string) error {
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}
