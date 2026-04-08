package install

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"

	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

type clientDeps struct {
	UI                uiProvider
	Inspect           func(svcmgr.Role) svcmgr.InstallInspection
	Detect            func(svcmgr.Role) svcmgr.InstallState
	EnsureUser        func(string) error
	EnsureDirs        func() error
	CurrentBinaryPath func() (string, error)
	InstallBinary     func(string) error
	WriteClientSpec   func(svcmgr.ServiceSpec) error
	WriteClientEnv    func(svcmgr.ServiceSpec, svcmgr.ClientEnv) error
	WriteClientUnit   func(svcmgr.ServiceSpec) error
	DaemonReload      func() error
	EnableAndStart    func(string) error
}

func InstallClient() error {
	return InstallClientWith(defaultClientDeps())
}

func InstallClientWith(deps clientDeps) error {
	inspection := resolveInspection(deps.Inspect, deps.Detect, svcmgr.RoleClient)
	state := inspection.State
	switch state {
	case svcmgr.StateInstalled:
		printInstalledSummary(deps.UI, "Client already installed")
		return nil
	case svcmgr.StateHistoricalDataOnly:
		deps.UI.PrintSummary("Client installation state is broken", [][2]string{
			{"Status", inspection.State.String()},
			{"Advice", "Client does not support recovering existing data; clear residual data and reinstall"},
			{"Problem", firstProblem(inspection.Problems)},
		})
		return errInstallBrokenState
	case svcmgr.StateBroken:
		printBrokenSummary(deps.UI, "Client installation state is broken", inspection)
		return errInstallBrokenState
	}

	serverURL, err := deps.UI.Input("Server address", tui.InputOptions{
		Placeholder: "e.g. wss://netsgo.example.com",
		Description: "WebSocket URL of the NetsGo server (ws:// or wss://)",
	})
	if err != nil {
		return err
	}
	clientKey, err := deps.UI.Password("Client key", tui.InputOptions{
		Placeholder: "sk-...",
		Description: "Obtain from the Web panel → Clients page",
		Validate: func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("client key cannot be empty")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	tlsSkipVerify, err := deps.UI.Confirm("Skip TLS certificate verification?")
	if err != nil {
		return err
	}
	tlsFingerprint, err := deps.UI.Input("TLS certificate fingerprint", tui.InputOptions{
		Placeholder: "AA:BB:CC:...",
		Description: "SHA-256 fingerprint for pinning a self-signed certificate (optional)",
	})
	if err != nil {
		return err
	}

	deps.UI.PrintSummary("Installation summary", [][2]string{
		{"Role", "client"},
		{"Server", serverURL},
		{"Skip TLS verify", boolText(tlsSkipVerify)},
		{"TLS fingerprint", tlsFingerprint},
	})
	ok, err := deps.UI.Confirm("Proceed with installation?")
	if err != nil {
		return err
	}
	if !ok {
		printInstallCancelled(deps.UI)
		return nil
	}

	if err := completeManagedInstall(svcmgr.RoleClient, managedInstallDeps{
		EnsureUser:        deps.EnsureUser,
		EnsureDirs:        deps.EnsureDirs,
		CurrentBinaryPath: deps.CurrentBinaryPath,
		InstallBinary:     deps.InstallBinary,
		DaemonReload:      deps.DaemonReload,
		EnableAndStart:    deps.EnableAndStart,
	}, func(spec svcmgr.ServiceSpec) error {
		spec.ServerURL = serverURL
		if err := deps.WriteClientSpec(spec); err != nil {
			return err
		}
		if err := deps.WriteClientEnv(spec, svcmgr.ClientEnv{Server: serverURL, Key: clientKey, TLSSkipVerify: tlsSkipVerify, TLSFingerprint: tlsFingerprint}); err != nil {
			return err
		}
		return deps.WriteClientUnit(spec)
	}); err != nil {
		return err
	}
	deps.UI.PrintSummary("Client installation complete", [][2]string{
		{"Status", "Running"},
		{"Connected to", serverURL},
		{"Next step", "Run netsgo manage to manage the service"},
	})
	return nil
}

func defaultClientDeps() clientDeps {
	return clientDeps{
		UI:                defaultUI{},
		Inspect:           svcmgr.Inspect,
		Detect:            svcmgr.Detect,
		EnsureUser:        svcmgr.EnsureUser,
		EnsureDirs:        ensureManagedClientDirs,
		CurrentBinaryPath: svcmgr.CurrentBinaryPath,
		InstallBinary:     svcmgr.InstallBinary,
		WriteClientSpec:   svcmgr.WriteClientSpec,
		WriteClientEnv:    svcmgr.WriteClientEnv,
		WriteClientUnit:   svcmgr.WriteClientUnit,
		DaemonReload:      svcmgr.DaemonReload,
		EnableAndStart:    svcmgr.EnableAndStart,
	}
}

func ensureManagedClientDirs() error {
	if err := os.MkdirAll(svcmgr.ManagedDataDir+"/client", 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(svcmgr.ManagedDataDir+"/locks", 0o750); err != nil {
		return err
	}
	account, err := user.Lookup(svcmgr.SystemUser)
	if err != nil {
		return nil
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	if err := os.Chown(svcmgr.ManagedDataDir+"/client", uid, gid); err != nil {
		return err
	}
	return os.Chown(svcmgr.ManagedDataDir+"/locks", uid, gid)
}

func boolText(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

func firstProblem(problems []string) string {
	if len(problems) == 0 {
		return "unknown error"
	}
	return problems[0]
}
