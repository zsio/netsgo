package manage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"netsgo/internal/install"
	"netsgo/internal/svcmgr"
)

type serverDeps struct {
	UI             uiProvider
	Inspect        func() svcmgr.InstallInspection
	IsActive       func() (bool, error)
	IsEnabled      func() (bool, error)
	Logs           func() error
	RunInstall     func() error
	ReadServerSpec func() (svcmgr.ServiceSpec, error)
	ReadServerEnv  func() (svcmgr.ServerEnv, error)
	DisableAndStop func() error
	EnableAndStart func() error
	DaemonReload   func() error
	RemovePaths    func(paths ...string) error
	RemoveBinary   func() error
	DetectClient   func() svcmgr.InstallState
}

func ManageServer() error {
	return ManageServerWith(defaultServerDeps())
}

func ManageServerWith(deps serverDeps) error {
	inspection := deps.Inspect()
	switch inspection.State {
	case svcmgr.StateInstalled:
		return runServiceMenu(serviceMenuDeps{
			UI: deps.UI,
			Status: func() error {
				return showStatusSummary(deps.UI, svcmgr.RoleServer, deps.Inspect(), deps.IsActive, deps.IsEnabled)
			},
			Detail: func() error {
				return showServerDetails(deps)
			},
			Logs:  deps.Logs,
			Start: deps.EnableAndStart,
			Stop:  deps.DisableAndStop,
			Uninstall: func() (bool, error) {
				return uninstallServer(deps)
			},
		})
	case svcmgr.StateHistoricalDataOnly:
		return runRecoverableServerMenu(deps)
	case svcmgr.StateBroken:
		return runBrokenServerMenu(deps)
	default:
		deps.UI.PrintSummary("Server is not installed", [][2]string{{"Next step", "Run netsgo install to install the server"}})
		return errReturnToSelection
	}
}

func defaultServerDeps() serverDeps {
	return serverDeps{
		UI: defaultUI{},
		Inspect: func() svcmgr.InstallInspection {
			return svcmgr.Inspect(svcmgr.RoleServer)
		},
		IsActive: func() (bool, error) {
			return svcmgr.IsActive(svcmgr.UnitName(svcmgr.RoleServer))
		},
		IsEnabled: func() (bool, error) {
			return svcmgr.IsEnabled(svcmgr.UnitName(svcmgr.RoleServer))
		},
		Logs: func() error {
			args := svcmgr.JournalArgs(svcmgr.UnitName(svcmgr.RoleServer), 100)
			return syscall.Exec("/usr/bin/journalctl", args, os.Environ())
		},
		RunInstall: func() error {
			return install.Run()
		},
		ReadServerSpec: func() (svcmgr.ServiceSpec, error) {
			return svcmgr.ReadServerSpec(svcmgr.SpecPath(svcmgr.RoleServer))
		},
		ReadServerEnv: func() (svcmgr.ServerEnv, error) {
			return svcmgr.ReadServerEnv(svcmgr.NewSpec(svcmgr.RoleServer))
		},
		DisableAndStop: func() error { return svcmgr.DisableAndStop(svcmgr.UnitName(svcmgr.RoleServer)) },
		EnableAndStart: func() error { return svcmgr.EnableAndStart(svcmgr.UnitName(svcmgr.RoleServer)) },
		DaemonReload:   svcmgr.DaemonReload,
		RemovePaths:    removePaths,
		RemoveBinary:   svcmgr.RemoveBinary,
		DetectClient: func() svcmgr.InstallState {
			return svcmgr.Detect(svcmgr.RoleClient)
		},
	}
}

func showServerDetails(deps serverDeps) error {
	inspection := deps.Inspect()
	spec, specErr := loadServerSpec(deps)
	env, envErr := loadServerEnv(deps)

	rows := [][2]string{
		{"Service name", spec.ServiceName},
		{"Role", string(svcmgr.RoleServer)},
		{"State", inspection.State.String()},
		{"Installed", boolLabel(inspection.State == svcmgr.StateInstalled)},
		{"Running", boolStateLabel(inspection.State == svcmgr.StateInstalled, deps.IsActive)},
		{"Enabled", boolStateLabel(inspection.State == svcmgr.StateInstalled, deps.IsEnabled)},
		{"Binary path", spec.BinaryPath},
		{"Data dir", spec.DataDir},
		{"Data path", serverDataPath(spec)},
		{"Lock path", lockPath(spec.DataDir)},
		{"Log target", "journald"},
		{"Unit path", spec.UnitPath},
		{"Env path", spec.EnvPath},
		{"Spec path", spec.SpecPath},
		{"Run as user", spec.RunAsUser},
		{"Listen port", intOrUnavailable(env.Port, envErr)},
		{"TLS mode", stringOrUnavailable(env.TLSMode, envErr)},
		{"Server address", stringOrUnavailable(firstNonEmpty(env.ServerAddr, spec.ServerURL), envErr)},
	}
	if specErr != nil {
		rows = append(rows, [2]string{"Spec status", fmt.Sprintf("Unavailable (%v)", specErr)})
	}
	if envErr != nil {
		rows = append(rows, [2]string{"Env status", fmt.Sprintf("Unavailable (%v)", envErr)})
	}
	rows = appendProblemRows(rows, inspection.Problems)
	deps.UI.PrintSummary("Server inspect", rows)
	return nil
}

func uninstallServer(deps serverDeps) (bool, error) {
	mode, err := deps.UI.Select("Uninstall mode", []string{"Remove service only, keep data", "Remove service and delete data"})
	if err != nil {
		return false, err
	}
	spec, err := loadServerSpec(deps)
	if err != nil {
		return false, err
	}
	deleteData := mode == 1
	rows := [][2]string{{"Mode", uninstallModeLabel(deleteData)}}
	rows = appendRemovalRows(rows, "Remove", spec.UnitPath, spec.EnvPath, spec.SpecPath)
	if deleteData {
		rows = appendRemovalRows(rows, "Remove", serverDataPath(spec))
	} else {
		rows = append(rows, [2]string{"Keep", serverDataPath(spec)})
	}
	rows = append(rows, sharedBinaryPlanRow(deps.DetectClient))
	deps.UI.PrintSummary("Server uninstall plan", rows)

	ok, err := deps.UI.Confirm("Proceed with server uninstall?")
	if err != nil {
		return false, err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return false, nil
	}
	if err := deps.DisableAndStop(); err != nil {
		return false, err
	}
	paths := []string{spec.UnitPath, spec.EnvPath, spec.SpecPath}
	if deleteData {
		paths = append(paths, serverDataPath(spec))
	}
	if err := deps.RemovePaths(paths...); err != nil {
		return false, err
	}
	if err := deps.DaemonReload(); err != nil {
		return false, err
	}
	if err := maybeRemoveSharedBinary(deps.UI, deps.DetectClient, deps.RemoveBinary); err != nil {
		return false, err
	}
	deps.UI.PrintSummary("Server uninstalled", [][2]string{{"State", "Removed"}, {"Next step", "Run netsgo manage to continue managing services"}})
	return true, nil
}

func runRecoverableServerMenu(deps serverDeps) error {
	for {
		action, err := deps.UI.Select("Select a recovery action", []string{"Inspect recoverable server state", "Run netsgo install", "Back"})
		if err != nil {
			return err
		}
		switch action {
		case 0:
			if err := showServerDetails(deps); err != nil {
				return err
			}
		case 1:
			if deps.RunInstall == nil {
				return errors.New("manage dependencies are incomplete")
			}
			return deps.RunInstall()
		case 2:
			return errReturnToSelection
		default:
			return errReturnToSelection
		}
	}
}

func runBrokenServerMenu(deps serverDeps) error {
	for {
		action, err := deps.UI.Select("Select a recovery action", []string{"Inspect broken server state", "Cleanup broken server installation", "Back"})
		if err != nil {
			return err
		}
		switch action {
		case 0:
			if err := showServerDetails(deps); err != nil {
				return err
			}
		case 1:
			exitMenu, err := cleanupBrokenServer(deps)
			if err != nil {
				return err
			}
			if exitMenu {
				return errReturnToSelection
			}
		case 2:
			return errReturnToSelection
		default:
			return errReturnToSelection
		}
	}
}

func cleanupBrokenServer(deps serverDeps) (bool, error) {
	mode, err := deps.UI.Select("Cleanup mode", []string{"Remove broken service files, keep data", "Remove broken service files and delete data"})
	if err != nil {
		return false, err
	}
	spec := svcmgr.NewSpec(svcmgr.RoleServer)
	deleteData := mode == 1

	rows := [][2]string{{"Mode", uninstallModeLabel(deleteData)}}
	rows = appendRemovalRows(rows, "Remove", spec.UnitPath, spec.EnvPath, spec.SpecPath)
	if deleteData {
		rows = appendRemovalRows(rows, "Remove", serverDataPath(spec))
	} else {
		rows = append(rows, [2]string{"Keep", serverDataPath(spec)})
	}
	rows = append(rows, sharedBinaryPlanRow(deps.DetectClient))
	deps.UI.PrintSummary("Broken server cleanup plan", rows)

	ok, err := deps.UI.Confirm("Proceed with broken server cleanup?")
	if err != nil {
		return false, err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return false, nil
	}
	paths := []string{spec.UnitPath, spec.EnvPath, spec.SpecPath}
	if deleteData {
		paths = append(paths, serverDataPath(spec))
	}
	if err := deps.RemovePaths(paths...); err != nil {
		return false, err
	}
	if err := deps.DaemonReload(); err != nil {
		return false, err
	}
	if err := maybeRemoveSharedBinary(deps.UI, deps.DetectClient, deps.RemoveBinary); err != nil {
		return false, err
	}
	deps.UI.PrintSummary("Broken server cleanup complete", [][2]string{{"State", "Cleaned"}, {"Next step", "Run netsgo install to restore the server if needed"}})
	return true, nil
}

func loadServerSpec(deps serverDeps) (svcmgr.ServiceSpec, error) {
	spec := svcmgr.NewSpec(svcmgr.RoleServer)
	if deps.ReadServerSpec == nil {
		return spec, nil
	}
	readSpec, err := deps.ReadServerSpec()
	if err != nil {
		return spec, err
	}
	return readSpec, nil
}

func loadServerEnv(deps serverDeps) (svcmgr.ServerEnv, error) {
	if deps.ReadServerEnv == nil {
		return svcmgr.ServerEnv{}, nil
	}
	return deps.ReadServerEnv()
}

func serverDataPath(spec svcmgr.ServiceSpec) string {
	return filepath.Join(spec.DataDir, "server")
}

func uninstallModeLabel(deleteData bool) string {
	if deleteData {
		return "Remove service and delete data"
	}
	return "Remove service only, keep data"
}

func sharedBinaryPlanRow(otherRoleState func() svcmgr.InstallState) [2]string {
	if otherRoleState != nil && otherRoleState() == svcmgr.StateNotInstalled {
		return [2]string{"Optional", "You can choose whether to remove the shared binary " + svcmgr.BinaryPath}
	}
	return [2]string{"Keep", svcmgr.BinaryPath}
}

func stringOrUnavailable(value string, err error) string {
	if err != nil {
		return fmt.Sprintf("Unavailable (%v)", err)
	}
	if value == "" {
		return "(none)"
	}
	return value
}

func intOrUnavailable(value int, err error) string {
	if err != nil {
		return fmt.Sprintf("Unavailable (%v)", err)
	}
	if value == 0 {
		return "(none)"
	}
	return itoa(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
