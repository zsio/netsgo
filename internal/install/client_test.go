package install

import (
	"errors"
	"strings"
	"testing"
	"time"

	"netsgo/internal/svcmgr"
)

func TestInstallClientWithAlreadyInstalled(t *testing.T) {
	ui := &fakeUI{}
	called := false
	err := InstallClientWith(clientDeps{
		UI:     ui,
		Detect: func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateInstalled },
		InstallBinary: func(src string) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("should not error when already installed: %v", err)
	}
	if called {
		t.Fatal("should not continue install when already installed")
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client already installed" {
		t.Fatalf("should show next-step summary when already installed, got %#v", ui.summaries)
	}
}

func TestInstallClientWithBrokenStateFails(t *testing.T) {
	ui := &fakeUI{}
	err := InstallClientWith(clientDeps{
		UI: ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateBroken, Problems: []string{"missing env file"}}
		},
	})
	if err == nil {
		t.Fatal("broken state should fail")
	}
	if !errors.Is(err, errInstallBrokenState) {
		t.Fatalf("broken state should return errInstallBrokenState, got %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client installation state is broken" {
		t.Fatalf("broken state should show problem summary, got %#v", ui.summaries)
	}
}

func TestInstallClientWithHistoricalDataOnlyFailsWithReauthMessage(t *testing.T) {
	ui := &fakeUI{}
	err := InstallClientWith(clientDeps{
		UI: ui,
		Inspect: func(role svcmgr.Role) svcmgr.InstallInspection {
			return svcmgr.InstallInspection{Role: role, State: svcmgr.StateHistoricalDataOnly, Problems: []string{"residual runtime data directory still exists: /var/lib/netsgo/client"}}
		},
	})
	if err == nil {
		t.Fatal("client historical data residual should refuse install")
	}
	if !errors.Is(err, errInstallBrokenState) {
		t.Fatalf("client historical data residual should return errInstallBrokenState, got %v", err)
	}
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client installation state is broken" {
		t.Fatalf("client historical data should show broken state summary, got %#v", ui.summaries)
	}
	rows := ui.summaries[0].rows
	foundAdvice := false
	for _, row := range rows {
		if row[0] == "Advice" && row[1] == "Client does not support recovering existing data; clear residual data and reinstall" {
			foundAdvice = true
			break
		}
	}
	if !foundAdvice {
		t.Fatalf("client historical data should advise re-authentication, got %#v", rows)
	}
}

func TestInstallClientWithFreshInstall(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"https://panel.example.com", "AA:BB:CC"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true, true},
	}
	writeEnvCalled := false
	var writtenEnv svcmgr.ClientEnv
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientEnv: func(layout svcmgr.ServiceLayout, env svcmgr.ClientEnv) error {
			writeEnvCalled = true
			writtenEnv = env
			return nil
		},
		WriteClientUnit:  func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:     func() error { return nil },
		EnableAndStart:   func(unit string) error { return nil },
		VerifyClientLink: fakeClientLink(ClientLinkEstablished),
	})
	if err != nil {
		t.Fatalf("fresh client install should not error: %v", err)
	}
	if !writeEnvCalled {
		t.Fatal("fresh client install should write env/unit")
	}
	if writtenEnv.Server != "https://panel.example.com" {
		t.Fatalf("client env should write normalized base URL, got %#v", writtenEnv)
	}
	if len(ui.summaries) != 2 {
		t.Fatalf("should show confirmation and completion summaries, got %d", len(ui.summaries))
	}
	if ui.summaries[1].title != "Client installation complete" {
		t.Fatalf("expected 'Client installation complete' summary, got %#v", ui.summaries)
	}
	assertConfirmDefault(t, ui.confirmCalls, "Skip TLS certificate verification?", false)
	assertConfirmDefault(t, ui.confirmCalls, "Proceed with installation?", true)
	assertClientServerInputUsesServiceAddressWording(t, ui.inputCalls)
	assertSummaryRow(t, ui.summaries[1], "NetsGo link", string(ClientLinkEstablished))
	assertNoSummaryRow(t, ui.summaries[0], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[0], "Data endpoint")
	assertNoSummaryRow(t, ui.summaries[1], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[1], "Data endpoint")
}

func TestInstallClientWithEnsureDirs(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"https://panel.example.com", ""},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{false, true},
	}
	ensureDirsCalled := false
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { ensureDirsCalled = true; return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ClientEnv) error { return nil },
		WriteClientUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
		VerifyClientLink:  fakeClientLink(ClientLinkEstablished),
	})
	if err != nil {
		t.Fatalf("client install should not error: %v", err)
	}
	if !ensureDirsCalled {
		t.Fatal("client install should create required directories")
	}
}

func TestInstallClientWithConfirmNoShowsCancelledSummary(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"https://panel.example.com", ""},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{false, false},
	}
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientEnv:    func(layout svcmgr.ServiceLayout, env svcmgr.ClientEnv) error { return nil },
		WriteClientUnit:   func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:      func() error { return nil },
		EnableAndStart:    func(unit string) error { return nil },
		VerifyClientLink:  fakeClientLink(ClientLinkNotEstablished),
	})
	if err != nil {
		t.Fatalf("cancelling install should not error: %v", err)
	}
	if len(ui.summaries) != 2 || ui.summaries[1].title != "Installation cancelled" {
		t.Fatalf("expected 'Installation cancelled' summary after declining, got %#v", ui.summaries)
	}
	assertConfirmDefault(t, ui.confirmCalls, "Skip TLS certificate verification?", false)
	assertConfirmDefault(t, ui.confirmCalls, "Proceed with installation?", true)
}

func TestInstallClientAcceptsHTTPAndWritesServiceAddress(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"http://netsgo.zsio.dev:9527"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true},
	}
	var writtenEnv svcmgr.ClientEnv
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientEnv: func(layout svcmgr.ServiceLayout, env svcmgr.ClientEnv) error {
			writtenEnv = env
			return nil
		},
		WriteClientUnit:  func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:     func() error { return nil },
		EnableAndStart:   func(unit string) error { return nil },
		VerifyClientLink: fakeClientLink(ClientLinkNotEstablished),
	})
	if err != nil {
		t.Fatalf("HTTP client install should not error: %v", err)
	}
	if writtenEnv.Server != "http://netsgo.zsio.dev:9527" {
		t.Fatalf("NETSGO_SERVER should be normalized HTTP base URL, got %#v", writtenEnv)
	}
	assertConfirmDefault(t, ui.confirmCalls, "Proceed with installation?", true)
	assertSummaryRow(t, ui.summaries[0], "Service address", "http://netsgo.zsio.dev:9527")
	assertNoSummaryRow(t, ui.summaries[0], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[0], "Data endpoint")
	assertSummaryRow(t, ui.summaries[1], "NetsGo link", string(ClientLinkNotEstablished))
	assertNoSummaryRow(t, ui.summaries[1], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[1], "Data endpoint")
}

func TestInstallClientAcceptsLegacyWSSAndWritesHTTPSServiceAddress(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"wss://netsgo.zsio.dev"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true, true},
	}
	var writtenEnv svcmgr.ClientEnv
	err := InstallClientWith(clientDeps{
		UI:                ui,
		Detect:            func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		EnsureUser:        func(name string) error { return nil },
		EnsureDirs:        func() error { return nil },
		CurrentBinaryPath: func() (string, error) { return "/tmp/netsgo", nil },
		InstallBinary:     func(src string) error { return nil },
		WriteClientEnv: func(layout svcmgr.ServiceLayout, env svcmgr.ClientEnv) error {
			writtenEnv = env
			return nil
		},
		WriteClientUnit:  func(layout svcmgr.ServiceLayout) error { return nil },
		DaemonReload:     func() error { return nil },
		EnableAndStart:   func(unit string) error { return nil },
		VerifyClientLink: fakeClientLink(ClientLinkEstablished),
	})
	if err != nil {
		t.Fatalf("legacy WSS client install should not error: %v", err)
	}
	if writtenEnv.Server != "https://netsgo.zsio.dev" {
		t.Fatalf("NETSGO_SERVER should be normalized HTTPS service address, got %#v", writtenEnv)
	}
	assertSummaryRow(t, ui.summaries[0], "Service address", "https://netsgo.zsio.dev")
	assertNoSummaryRow(t, ui.summaries[0], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[0], "Data endpoint")
}

func TestClientLinkEvidenceStates(t *testing.T) {
	if !clientLinkEstablishedFromLogs("✅ Authentication succeeded\n✅ Data channel established") {
		t.Fatal("expected auth + data log evidence to establish link")
	}
	if clientLinkEstablishedFromLogs("✅ Authentication succeeded") {
		t.Fatal("auth without data channel should not establish link")
	}
	rows := clientCompletionSummaryRows("http://server", ClientLinkEvidence{State: ClientLinkNotVerified, Detail: "journal unavailable"})
	for _, row := range rows {
		if row[1] == "sk-test-key" {
			t.Fatal("client completion summary must not leak client key")
		}
	}
}

func TestDefaultVerifyClientLink(t *testing.T) {
	originalJournalOutput := clientLinkJournalOutput
	originalSleep := clientLinkSleep
	t.Cleanup(func() {
		clientLinkJournalOutput = originalJournalOutput
		clientLinkSleep = originalSleep
	})
	clientLinkSleep = func(time.Duration) {}

	t.Run("established", func(t *testing.T) {
		clientLinkJournalOutput = func(unit string, since time.Time) (string, error) {
			return "✅ Authentication succeeded\nsecret should stay inside logs\n✅ Data channel established", nil
		}
		got := defaultVerifyClientLink("netsgo-client.service", time.Now(), time.Second)
		if got.State != ClientLinkEstablished {
			t.Fatalf("defaultVerifyClientLink() = %#v, want established", got)
		}
		if strings.Contains(got.Detail, "secret") {
			t.Fatalf("link evidence detail leaked raw journal content: %#v", got)
		}
	})

	t.Run("not established", func(t *testing.T) {
		clientLinkJournalOutput = func(unit string, since time.Time) (string, error) {
			return "✅ Authentication succeeded", nil
		}
		got := defaultVerifyClientLink("netsgo-client.service", time.Now(), 0)
		if got.State != ClientLinkNotEstablished {
			t.Fatalf("defaultVerifyClientLink() = %#v, want not established", got)
		}
	})

	t.Run("not verified", func(t *testing.T) {
		clientLinkJournalOutput = func(unit string, since time.Time) (string, error) {
			return "raw journal with sk-test-key", errors.New("journalctl failed")
		}
		got := defaultVerifyClientLink("netsgo-client.service", time.Now(), time.Second)
		if got.State != ClientLinkNotVerified {
			t.Fatalf("defaultVerifyClientLink() = %#v, want not verified", got)
		}
		if strings.Contains(got.Detail, "sk-test-key") {
			t.Fatalf("not verified detail leaked raw journal content: %#v", got)
		}
	})
}

func fakeClientLink(state ClientLinkState) func(string, time.Time, time.Duration) ClientLinkEvidence {
	return func(unit string, since time.Time, timeout time.Duration) ClientLinkEvidence {
		return ClientLinkEvidence{State: state}
	}
}

func assertConfirmDefault(t *testing.T, calls []confirmCall, prompt string, want bool) {
	t.Helper()
	for _, call := range calls {
		if call.prompt == prompt {
			if call.defaultValue != want {
				t.Fatalf("confirm %q default = %v, want %v", prompt, call.defaultValue, want)
			}
			return
		}
	}
	t.Fatalf("confirm %q not called; calls=%#v", prompt, calls)
}

func assertSummaryRow(t *testing.T, summary summaryCall, key, want string) {
	t.Helper()
	for _, row := range summary.rows {
		if row[0] == key {
			if row[1] != want {
				t.Fatalf("summary row %q = %q, want %q", key, row[1], want)
			}
			return
		}
	}
	t.Fatalf("summary row %q not found in %#v", key, summary.rows)
}

func assertNoSummaryRow(t *testing.T, summary summaryCall, key string) {
	t.Helper()
	for _, row := range summary.rows {
		if row[0] == key {
			t.Fatalf("summary row %q should not be shown in first-use output: %#v", key, summary.rows)
		}
	}
}

func assertClientServerInputUsesServiceAddressWording(t *testing.T, calls []inputCall) {
	t.Helper()
	for _, call := range calls {
		if call.prompt != "Service address" {
			continue
		}
		if call.opts.Placeholder != "e.g. http://netsgo.example.com:9527" {
			t.Fatalf("service address placeholder = %q", call.opts.Placeholder)
		}
		if !strings.Contains(call.opts.Description, "service address") || !strings.Contains(call.opts.Description, "http(s)://") {
			t.Fatalf("service address description should prefer service address/http(s), got %q", call.opts.Description)
		}
		if strings.Contains(call.opts.Description, "control/data") {
			t.Fatalf("service address description should not expose control/data endpoints, got %q", call.opts.Description)
		}
		return
	}
	t.Fatalf("service address input prompt not called; calls=%#v", calls)
}
