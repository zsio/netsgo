package install

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"netsgo/internal/clientaddr"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

const clientLinkEvidenceTimeout = 8 * time.Second

var clientLinkJournalOutput = func(unit string, since time.Time) (string, error) {
	args := svcmgr.JournalSinceArgs(unit, since)
	output, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	return string(output), err
}

var clientLinkSleep = time.Sleep

type ClientLinkState string

const (
	ClientLinkEstablished    ClientLinkState = "Established"
	ClientLinkNotEstablished ClientLinkState = "Not established within 8s"
	ClientLinkNotVerified    ClientLinkState = "Not verified"
)

type ClientLinkEvidence struct {
	State  ClientLinkState
	Detail string
}

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
	VerifyClientLink  func(unit string, since time.Time, timeout time.Duration) ClientLinkEvidence
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

	serverInput, err := deps.UI.Input("Server address", tui.InputOptions{
		Placeholder: "e.g. http://netsgo.example.com:9527",
		Description: "Paste the server address from the Web panel or CLI command. http(s):// and ws(s):// are accepted; NetsGo will derive the control/data WebSocket endpoints.",
		Validate:    validateInstallClientServerURL,
	})
	if err != nil {
		return err
	}
	serverAddr, err := clientaddr.Normalize(serverInput, clientaddr.ModeManagedInstall)
	if err != nil {
		return err
	}
	serverURL := serverAddr.BaseURL
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
	usesTLS := serverAddr.UseTLS
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
		[2]string{"Control endpoint", serverAddr.ControlURL},
		[2]string{"Data endpoint", serverAddr.DataURL},
		[2]string{"TLS", ternary(usesTLS, "Enabled", "Disabled")},
		[2]string{"Skip TLS verify", ternary(usesTLS, boolText(tlsSkipVerify), "N/A")},
		[2]string{"TLS fingerprint", ternary(tlsFingerprint != "", tlsFingerprint, "Not set")},
	))
	ok, err := deps.UI.ConfirmWithOptions("Proceed with installation?", tui.ConfirmOptions{Default: true})
	if err != nil {
		return err
	}
	if !ok {
		printInstallCancelled(deps.UI)
		return nil
	}

	evidenceSince := time.Now().Add(-1 * time.Second)
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
	verifyClientLink := deps.VerifyClientLink
	if verifyClientLink == nil {
		verifyClientLink = defaultVerifyClientLink
	}
	link := verifyClientLink(svcmgr.UnitName(svcmgr.RoleClient), evidenceSince, clientLinkEvidenceTimeout)
	deps.UI.PrintSummary("Client installation complete", clientCompletionSummaryRows(serverURL, serverAddr.ControlURL, serverAddr.DataURL, link))
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
		VerifyClientLink:  defaultVerifyClientLink,
	}
}

func clientCompletionSummaryRows(serverURL, controlURL, dataURL string, link ClientLinkEvidence) [][2]string {
	rows := [][2]string{
		{"Status", "Running"},
		{"Service", svcmgr.UnitName(svcmgr.RoleClient)},
		{"Run as", svcmgr.SystemUser},
		[2]string{"Server", serverURL},
		[2]string{"Control endpoint", controlURL},
		[2]string{"Data endpoint", dataURL},
		[2]string{"NetsGo link", string(link.State)},
	}
	if link.Detail != "" {
		rows = append(rows, [2]string{"Link detail", link.Detail})
	}
	rows = append(rows, [2]string{"Logs", journalctlCommand(svcmgr.RoleClient)})
	if link.State != ClientLinkEstablished {
		rows = append(rows,
			[2]string{"Advice", "Check DNS/server address, client key, TLS settings, server service, and client logs"},
		)
	}
	rows = append(rows, [2]string{"Next step", "Run netsgo manage to manage the service"})
	return rows
}

func defaultVerifyClientLink(unit string, since time.Time, timeout time.Duration) ClientLinkEvidence {
	deadline := time.Now().Add(timeout)
	for {
		output, err := clientLinkJournalOutput(unit, since)
		if err != nil {
			return ClientLinkEvidence{
				State:  ClientLinkNotVerified,
				Detail: "Could not read systemd journal; inspect client logs manually.",
			}
		}
		if clientLinkEstablishedFromLogs(string(output)) {
			return ClientLinkEvidence{State: ClientLinkEstablished}
		}
		if time.Now().After(deadline) {
			return ClientLinkEvidence{
				State:  ClientLinkNotEstablished,
				Detail: "Service started, but NetsGo control/data channels were not both observed in the verification window.",
			}
		}
		clientLinkSleep(500 * time.Millisecond)
	}
}

func clientLinkEstablishedFromLogs(logs string) bool {
	return strings.Contains(logs, "Authentication succeeded") && strings.Contains(logs, "Data channel established")
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
