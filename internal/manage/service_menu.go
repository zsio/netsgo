package manage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"netsgo/internal/svcmgr"
)

var (
	manageActionOptions  = []string{"Status", "Inspect", "Logs", "Start", "Stop", "Restart", "Uninstall", "Back"}
	errReturnToSelection = errors.New("manage: return to selection")
)

type serviceMenuDeps struct {
	UI        uiProvider
	Status    func() error
	Detail    func() error
	Logs      func() error
	Start     func() error
	Stop      func() error
	Uninstall func() (bool, error)
}

func runServiceMenu(deps serviceMenuDeps) error {
	for {
		action, err := deps.UI.Select("Select an action", manageActionOptions)
		if err != nil {
			return err
		}

		switch action {
		case 0:
			if err := deps.Status(); err != nil {
				return err
			}
		case 1:
			if err := deps.Detail(); err != nil {
				return err
			}
		case 2:
			return deps.Logs()
		case 3:
			if err := runLifecycleAction(deps.UI, "Start", "Started", deps.Start); err != nil {
				return err
			}
		case 4:
			if err := runLifecycleAction(deps.UI, "Stop", "Stopped", deps.Stop); err != nil {
				return err
			}
		case 5:
			if err := runRestartAction(deps.UI, deps.Stop, deps.Start); err != nil {
				return err
			}
		case 6:
			exitMenu, err := deps.Uninstall()
			if err != nil {
				return err
			}
			if exitMenu {
				return errReturnToSelection
			}
		case 7:
			return errReturnToSelection
		default:
			return errReturnToSelection
		}
	}
}

func showStatusSummary(ui uiProvider, role svcmgr.Role, inspection svcmgr.InstallInspection, isActive func() (bool, error), isEnabled func() (bool, error)) error {
	rows := [][2]string{
		{"Service", svcmgr.UnitName(role)},
		{"Role", string(role)},
		{"State", inspection.State.String()},
		{"Installed", boolLabel(inspection.State == svcmgr.StateInstalled)},
		{"Running", boolStateLabel(inspection.State == svcmgr.StateInstalled, isActive)},
		{"Enabled", boolStateLabel(inspection.State == svcmgr.StateInstalled, isEnabled)},
	}
	rows = appendProblemRows(rows, inspection.Problems)
	ui.PrintSummary("Service status", rows)
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

func maybeRemoveSharedBinary(ui uiProvider, otherRoleState func() svcmgr.InstallState, removeBinary func() error) error {
	if otherRoleState == nil || removeBinary == nil {
		return nil
	}
	if otherRoleState() != svcmgr.StateNotInstalled {
		return nil
	}
	ok, err := ui.Confirm(fmt.Sprintf("No other managed roles detected. Remove shared binary %s as well?", svcmgr.BinaryPath))
	if err != nil {
		return err
	}
	if !ok {
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

func appendProblemRows(rows [][2]string, problems []string) [][2]string {
	for _, problem := range problems {
		rows = append(rows, [2]string{"Problem", problem})
	}
	return rows
}

func boolStateLabel(available bool, fn func() (bool, error)) string {
	if !available || fn == nil {
		return "No"
	}
	value, err := fn()
	if err != nil {
		return fmt.Sprintf("Unknown (%v)", err)
	}
	return boolLabel(value)
}

func appendRemovalRows(rows [][2]string, label string, paths ...string) [][2]string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		rows = append(rows, [2]string{label, path})
	}
	return rows
}

func lockPath(dataDir string) string {
	return filepath.Join(dataDir, "locks")
}
