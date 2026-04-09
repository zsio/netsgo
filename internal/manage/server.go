package manage

import (
	"os"
	"syscall"

	"netsgo/internal/svcmgr"
)

type serverDeps struct {
	UI             uiProvider
	Status         func() (string, error)
	Logs           func() error
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
	return runServiceMenu(serviceMenuDeps{
		UI:     deps.UI,
		Status: deps.Status,
		Detail: func() error {
			return showServerDetails(deps)
		},
		Logs:  deps.Logs,
		Start: deps.EnableAndStart,
		Stop:  deps.DisableAndStop,
		Uninstall: func() error {
			return uninstallServer(deps)
		},
	})
}

func defaultServerDeps() serverDeps {
	return serverDeps{
		UI: defaultUI{},
		Status: func() (string, error) {
			return svcmgr.Status(svcmgr.UnitName(svcmgr.RoleServer))
		},
		Logs: func() error {
			args := svcmgr.JournalArgs(svcmgr.UnitName(svcmgr.RoleServer), 100)
			return syscall.Exec("/usr/bin/journalctl", args, os.Environ())
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
	spec, err := deps.ReadServerSpec()
	if err != nil {
		return err
	}
	env, err := deps.ReadServerEnv()
	if err != nil {
		return err
	}
	deps.UI.PrintSummary("Server details", [][2]string{{"Service name", spec.ServiceName}, {"Listen port", itoa(env.Port)}, {"TLS mode", env.TLSMode}, {"Server address", env.ServerAddr}})
	return nil
}

func uninstallServer(deps serverDeps) error {
	mode, err := deps.UI.Select("Uninstall mode", []string{"Remove service only, keep data", "Remove service and delete data"})
	if err != nil {
		return err
	}
	ok, err := deps.UI.Confirm("Confirm uninstall?")
	if err != nil {
		return err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return nil
	}
	if err := deps.DisableAndStop(); err != nil {
		return err
	}
	spec, err := deps.ReadServerSpec()
	if err != nil {
		return err
	}
	paths := []string{spec.UnitPath, spec.EnvPath, spec.SpecPath}
	if mode == 1 {
		paths = append(paths, svcmgr.ManagedDataDir+"/server")
	}
	if err := deps.RemovePaths(paths...); err != nil {
		return err
	}
	if err := deps.DaemonReload(); err != nil {
		return err
	}
	return maybeRemoveSharedBinary(deps.DetectClient, deps.RemoveBinary)
}
