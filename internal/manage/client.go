package manage

import (
	"os"
	"syscall"

	"netsgo/internal/svcmgr"
)

type clientDeps struct {
	UI             uiProvider
	Status         func() (string, error)
	Logs           func() error
	ReadClientSpec func() (svcmgr.ServiceSpec, error)
	ReadClientEnv  func() (svcmgr.ClientEnv, error)
	DisableAndStop func() error
	EnableAndStart func() error
	DaemonReload   func() error
	RemovePaths    func(paths ...string) error
	RemoveBinary   func() error
	DetectServer   func() svcmgr.InstallState
}

func ManageClient() error {
	return ManageClientWith(defaultClientDeps())
}

func ManageClientWith(deps clientDeps) error {
	return runServiceMenu(serviceMenuDeps{
		UI:     deps.UI,
		Status: deps.Status,
		Detail: func() error {
			return showClientDetails(deps)
		},
		Logs:  deps.Logs,
		Start: deps.EnableAndStart,
		Stop:  deps.DisableAndStop,
		Uninstall: func() error {
			return uninstallClient(deps)
		},
	})
}

func defaultClientDeps() clientDeps {
	return clientDeps{
		UI: defaultUI{},
		Status: func() (string, error) {
			return svcmgr.Status(svcmgr.UnitName(svcmgr.RoleClient))
		},
		Logs: func() error {
			args := svcmgr.JournalArgs(svcmgr.UnitName(svcmgr.RoleClient), 100)
			return syscall.Exec("/usr/bin/journalctl", args, os.Environ())
		},
		ReadClientSpec: func() (svcmgr.ServiceSpec, error) {
			return svcmgr.ReadClientSpec(svcmgr.SpecPath(svcmgr.RoleClient))
		},
		ReadClientEnv: func() (svcmgr.ClientEnv, error) {
			return svcmgr.ReadClientEnv(svcmgr.NewSpec(svcmgr.RoleClient))
		},
		DisableAndStop: func() error { return svcmgr.DisableAndStop(svcmgr.UnitName(svcmgr.RoleClient)) },
		EnableAndStart: func() error { return svcmgr.EnableAndStart(svcmgr.UnitName(svcmgr.RoleClient)) },
		DaemonReload:   svcmgr.DaemonReload,
		RemovePaths:    removePaths,
		RemoveBinary:   svcmgr.RemoveBinary,
		DetectServer: func() svcmgr.InstallState {
			return svcmgr.Detect(svcmgr.RoleServer)
		},
	}
}

func showClientDetails(deps clientDeps) error {
	spec, err := deps.ReadClientSpec()
	if err != nil {
		return err
	}
	env, err := deps.ReadClientEnv()
	if err != nil {
		return err
	}
	deps.UI.PrintSummary("Client details", [][2]string{{"Service name", spec.ServiceName}, {"Server", env.Server}, {"Skip TLS verification", boolLabel(env.TLSSkipVerify)}, {"TLS fingerprint", env.TLSFingerprint}})
	return nil
}

func uninstallClient(deps clientDeps) error {
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
	spec, err := deps.ReadClientSpec()
	if err != nil {
		return err
	}
	if err := deps.RemovePaths(spec.UnitPath, spec.EnvPath, spec.SpecPath, svcmgr.ManagedDataDir+"/client"); err != nil {
		return err
	}
	if err := deps.DaemonReload(); err != nil {
		return err
	}
	return maybeRemoveSharedBinary(deps.DetectServer, deps.RemoveBinary)
}
