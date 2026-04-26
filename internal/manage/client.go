package manage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"netsgo/internal/install"
	"netsgo/internal/svcmgr"
	"netsgo/pkg/version"
)

type clientDeps struct {
	UI             uiProvider
	Inspect        func() svcmgr.InstallInspection
	IsActive       func() (bool, error)
	IsEnabled      func() (bool, error)
	Logs           func() error
	RunInstall     func() error
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
	inspection := deps.Inspect()
	switch inspection.State {
	case svcmgr.StateInstalled:
		return runServiceMenu(serviceMenuDeps{
			UI: deps.UI,
			Status: func() error {
				return showStatusSummary(deps.UI, svcmgr.RoleClient, deps.Inspect(), deps.IsActive, deps.IsEnabled)
			},
			Detail: func() error {
				return showClientDetails(deps)
			},
			Logs:  deps.Logs,
			Start: deps.EnableAndStart,
			Stop:  deps.DisableAndStop,
			Update: func() error {
				return runUpdate(deps.UI, version.Current, nil)
			},
			Uninstall: func() (bool, error) {
				return uninstallClient(deps)
			},
		})
	case svcmgr.StateBroken:
		return runBrokenClientMenu(deps)
	default:
		deps.UI.PrintSummary("Client is not installed", [][2]string{{"Next step", "Run netsgo install to install the client"}})
		return errReturnToSelection
	}
}

func defaultClientDeps() clientDeps {
	return clientDeps{
		UI: defaultUI{},
		Inspect: func() svcmgr.InstallInspection {
			return svcmgr.Inspect(svcmgr.RoleClient)
		},
		IsActive: func() (bool, error) {
			return svcmgr.IsActive(svcmgr.UnitName(svcmgr.RoleClient))
		},
		IsEnabled: func() (bool, error) {
			return svcmgr.IsEnabled(svcmgr.UnitName(svcmgr.RoleClient))
		},
		Logs: func() error {
			args := svcmgr.JournalArgs(svcmgr.UnitName(svcmgr.RoleClient), 100)
			return syscall.Exec("/usr/bin/journalctl", args, os.Environ())
		},
		RunInstall: func() error {
			return install.Run()
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
	inspection := deps.Inspect()
	spec, specErr := loadClientSpec(deps)
	env, envErr := loadClientEnv(deps)
	identitySummary, identityErr := clientIdentitySummary(spec)

	rows := [][2]string{
		{"Service name", spec.ServiceName},
		{"Role", string(svcmgr.RoleClient)},
		{"State", inspection.State.String()},
		{"Installed", boolLabel(inspection.State == svcmgr.StateInstalled)},
		{"Running", boolStateLabel(inspection.State == svcmgr.StateInstalled, deps.IsActive)},
		{"Enabled", boolStateLabel(inspection.State == svcmgr.StateInstalled, deps.IsEnabled)},
		{"Binary path", spec.BinaryPath},
		{"Data dir", spec.DataDir},
		{"Data path", clientDataPath(spec)},
		{"Lock path", lockPath(spec.DataDir)},
		{"Log target", "journald"},
		{"Unit path", spec.UnitPath},
		{"Env path", spec.EnvPath},
		{"Spec path", spec.SpecPath},
		{"Run as user", spec.RunAsUser},
		{"Server URL", stringOrUnavailable(firstNonEmpty(env.Server, spec.ServerURL), envErr)},
		{"Skip TLS verification", boolOrUnavailable(env.TLSSkipVerify, envErr)},
		{"TLS fingerprint", stringOrUnavailable(env.TLSFingerprint, envErr)},
		{"Client identity state", stringOrUnavailable(identitySummary, identityErr)},
	}
	if specErr != nil {
		rows = append(rows, [2]string{"Spec status", fmt.Sprintf("Unavailable (%v)", specErr)})
	}
	if envErr != nil {
		rows = append(rows, [2]string{"Env status", fmt.Sprintf("Unavailable (%v)", envErr)})
	}
	rows = appendProblemRows(rows, inspection.Problems)
	deps.UI.PrintSummary("Client inspect", rows)
	return nil
}

func uninstallClient(deps clientDeps) (bool, error) {
	spec, err := loadClientSpec(deps)
	if err != nil {
		return false, err
	}

	rows := [][2]string{
		{"Impact", "Remove the managed client service and local client identity/state"},
		{"Effect", "Reinstalling the client creates a new local identity"},
		{"Effect", "Server-side history is not cleaned automatically"},
	}
	rows = appendRemovalRows(rows, "Remove", spec.UnitPath, spec.EnvPath, spec.SpecPath, clientDataPath(spec))
	rows = append(rows, sharedBinaryPlanRow(deps.DetectServer))
	deps.UI.PrintSummary("Client uninstall plan", rows)

	ok, err := deps.UI.Confirm("Proceed with client uninstall?")
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
	if err := deps.RemovePaths(spec.UnitPath, spec.EnvPath, spec.SpecPath, clientDataPath(spec)); err != nil {
		return false, err
	}
	if err := deps.DaemonReload(); err != nil {
		return false, err
	}
	if err := maybeRemoveSharedBinary(deps.UI, deps.DetectServer, deps.RemoveBinary); err != nil {
		return false, err
	}
	deps.UI.PrintSummary("Client uninstalled", [][2]string{{"State", "Removed"}, {"Next step", "Run netsgo install to create a new client identity if needed"}})
	return true, nil
}

func runBrokenClientMenu(deps clientDeps) error {
	for {
		action, err := deps.UI.Select("Select a recovery action", []string{"Inspect broken client state", "Cleanup broken client installation", "Run netsgo install", "Back"})
		if err != nil {
			return err
		}
		switch action {
		case 0:
			if err := showClientDetails(deps); err != nil {
				return err
			}
		case 1:
			exitMenu, err := cleanupBrokenClient(deps)
			if err != nil {
				return err
			}
			if exitMenu {
				return errReturnToSelection
			}
		case 2:
			if deps.RunInstall == nil {
				return errors.New("manage dependencies are incomplete")
			}
			return deps.RunInstall()
		case 3:
			return errReturnToSelection
		default:
			return errReturnToSelection
		}
	}
}

func cleanupBrokenClient(deps clientDeps) (bool, error) {
	spec := svcmgr.NewSpec(svcmgr.RoleClient)

	rows := [][2]string{
		{"Impact", "Remove broken client service files and local client identity/state"},
		{"Effect", "Reinstalling the client creates a new local identity"},
	}
	rows = appendRemovalRows(rows, "Remove", spec.UnitPath, spec.EnvPath, spec.SpecPath, clientDataPath(spec))
	rows = append(rows, sharedBinaryPlanRow(deps.DetectServer))
	deps.UI.PrintSummary("Broken client cleanup plan", rows)

	ok, err := deps.UI.Confirm("Proceed with broken client cleanup?")
	if err != nil {
		return false, err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return false, nil
	}
	if err := deps.RemovePaths(spec.UnitPath, spec.EnvPath, spec.SpecPath, clientDataPath(spec)); err != nil {
		return false, err
	}
	if err := deps.DaemonReload(); err != nil {
		return false, err
	}
	if err := maybeRemoveSharedBinary(deps.UI, deps.DetectServer, deps.RemoveBinary); err != nil {
		return false, err
	}
	deps.UI.PrintSummary("Broken client cleanup complete", [][2]string{{"State", "Cleaned"}, {"Next step", "Run netsgo install to install the client again if needed"}})
	return true, nil
}

func loadClientSpec(deps clientDeps) (svcmgr.ServiceSpec, error) {
	spec := svcmgr.NewSpec(svcmgr.RoleClient)
	if deps.ReadClientSpec == nil {
		return spec, nil
	}
	readSpec, err := deps.ReadClientSpec()
	if err != nil {
		return spec, err
	}
	return readSpec, nil
}

func loadClientEnv(deps clientDeps) (svcmgr.ClientEnv, error) {
	if deps.ReadClientEnv == nil {
		return svcmgr.ClientEnv{}, nil
	}
	return deps.ReadClientEnv()
}

func clientDataPath(spec svcmgr.ServiceSpec) string {
	return filepath.Join(spec.DataDir, "client")
}

func clientIdentitySummary(spec svcmgr.ServiceSpec) (string, error) {
	path := filepath.Join(clientDataPath(spec), "client.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var state struct {
		InstallID      string `json:"install_id"`
		Token          string `json:"token,omitempty"`
		TLSFingerprint string `json:"tls_fingerprint,omitempty"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return "", err
	}

	parts := []string{}
	if state.InstallID != "" {
		parts = append(parts, "persisted install id")
	}
	if state.Token != "" {
		parts = append(parts, "saved token")
	}
	if state.TLSFingerprint != "" {
		parts = append(parts, "saved TLS fingerprint")
	}
	if len(parts) == 0 {
		return "state file present without usable identity data", nil
	}
	return strings.Join(parts, ", "), nil
}

func boolOrUnavailable(value bool, err error) string {
	if err != nil {
		return fmt.Sprintf("Unavailable (%v)", err)
	}
	return boolLabel(value)
}
