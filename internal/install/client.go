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
	WriteClientEnv    func(svcmgr.ServiceLayout, svcmgr.ClientEnv) error
	WriteClientUnit   func(svcmgr.ServiceLayout) error
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
		printInstalledSummary(deps.UI, "Client already installed", svcmgr.RoleClient)
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
		Validate:    validateInstallClientServerURL,
	})
	if err != nil {
		return err
	}
	if err := validateInstallClientServerURL(serverURL); err != nil {
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
	usesTLS := strings.HasPrefix(serverURL, "wss://")
	tlsSkipVerify := false
	tlsFingerprint := ""
	if usesTLS {
		tlsSkipVerify, err = deps.UI.Confirm("Skip TLS certificate verification?")
		if err != nil {
			return err
		}
		if !tlsSkipVerify {
			tlsFingerprint, err = deps.UI.Input("TLS certificate fingerprint", tui.InputOptions{
				Placeholder: "AA:BB:CC:...",
				Description: "SHA-256 fingerprint for pinning a self-signed certificate (optional)",
			})
			if err != nil {
				return err
			}
		}
	}

	deps.UI.PrintSummary("Installation summary", confirmSummaryRows(svcmgr.RoleClient,
		[2]string{"Server", serverURL},
		[2]string{"TLS", ternary(usesTLS, "wss", "ws")},
		[2]string{"Skip TLS verify", ternary(usesTLS, boolText(tlsSkipVerify), "N/A")},
		[2]string{"TLS fingerprint", ternary(tlsFingerprint != "", tlsFingerprint, "Not set")},
	))
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
	}, func(layout svcmgr.ServiceLayout) error {
		if err := deps.WriteClientEnv(layout, svcmgr.ClientEnv{Server: serverURL, Key: clientKey, TLSSkipVerify: tlsSkipVerify, TLSFingerprint: tlsFingerprint}); err != nil {
			return err
		}
		return deps.WriteClientUnit(layout)
	}); err != nil {
		return err
	}
	deps.UI.PrintSummary("Client installation complete", completionSummaryRows(svcmgr.RoleClient, "Connected to", serverURL))
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

func ternary(ok bool, a, b string) string {
	if ok {
		return a
	}
	return b
}

func firstProblem(problems []string) string {
	if len(problems) == 0 {
		return "unknown error"
	}
	return problems[0]
}
