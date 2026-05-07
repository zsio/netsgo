package install

import (
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"netsgo/internal/clientaddr"
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
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client 已安装" {
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
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client 安装状态异常" {
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
	if len(ui.summaries) != 1 || ui.summaries[0].title != "Client 安装状态异常" {
		t.Fatalf("client historical data should show broken state summary, got %#v", ui.summaries)
	}
	rows := ui.summaries[0].rows
	found建议 := false
	for _, row := range rows {
		if row[0] == "建议" && row[1] == "检测到残留 client 数据；请先清理残留状态后重新安装" {
			found建议 = true
			break
		}
	}
	if !found建议 {
		t.Fatalf("client historical data should give cleanup guidance, got %#v", rows)
	}
	assertSummaryDoesNotContain(t, ui.summaries[0], "恢复")
	assertSummaryDoesNotContain(t, ui.summaries[0], "身份")
}

func TestInstallClientWithFreshInstall(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"https://panel.example.com"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true},
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
	if writtenEnv.TLSSkipVerify || writtenEnv.TLSFingerprint != "" {
		t.Fatalf("client install should not write TLS bypass settings, got %#v", writtenEnv)
	}
	if len(ui.summaries) != 2 {
		t.Fatalf("should show confirmation and completion summaries, got %d", len(ui.summaries))
	}
	if ui.summaries[1].title != "Client 安装完成" {
		t.Fatalf("expected 'Client 安装完成' summary, got %#v", ui.summaries)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "继续安装？")
	assertNoConfirmPrompt(t, ui.confirmCalls, "跳过 TLS 证书校验？")
	assertNoInputPrompt(t, ui.inputCalls, "TLS 证书指纹")
	assertClientServerInputUsesConsoleAddressWording(t, ui.inputCalls)
	assertClientKeyPromptUsesChineseTitle(t, ui.passwordCalls)
	assertSummaryRow(t, ui.summaries[1], "NetsGo 链路", string(ClientLinkEstablished))
	assertSummaryRow(t, ui.summaries[0], "TLS 状态", "启用")
	assertNoSummaryRow(t, ui.summaries[0], "跳过 TLS 校验")
	assertNoSummaryRow(t, ui.summaries[0], "TLS 指纹")
	assertNoSummaryRow(t, ui.summaries[0], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[0], "Data endpoint")
	assertNoSummaryRow(t, ui.summaries[1], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[1], "Data endpoint")
}

func TestInstallClientWithEnsureDirs(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"https://panel.example.com"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true},
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
		inputs:    []string{"https://panel.example.com"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{false},
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
	if len(ui.summaries) != 2 || ui.summaries[1].title != "安装已取消" {
		t.Fatalf("expected '安装已取消' summary after declining, got %#v", ui.summaries)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "继续安装？")
	assertNoConfirmPrompt(t, ui.confirmCalls, "跳过 TLS 证书校验？")
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
		VerifyClientLink: fakeClientLinkWithDetail(ClientLinkNotEstablished, "服务已启动，但 8 秒内未确认连接成功。"),
		CheckServerTLS: func(addr clientaddr.Address, skipVerify bool) error {
			t.Fatal("HTTP install should not probe TLS")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("HTTP client install should not error: %v", err)
	}
	if writtenEnv.Server != "http://netsgo.zsio.dev:9527" {
		t.Fatalf("NETSGO_SERVER should be normalized HTTP base URL, got %#v", writtenEnv)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "继续安装？")
	assertNoConfirmPrompt(t, ui.confirmCalls, "跳过 TLS 证书校验？")
	assertSummaryRow(t, ui.summaries[0], "服务地址", "http://netsgo.zsio.dev:9527")
	assertNoSummaryRow(t, ui.summaries[0], "跳过 TLS 校验")
	assertNoSummaryRow(t, ui.summaries[0], "TLS 指纹")
	assertNoSummaryRow(t, ui.summaries[0], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[0], "Data endpoint")
	assertSummaryRow(t, ui.summaries[1], "NetsGo 链路", string(ClientLinkNotEstablished))
	assertSummaryRow(t, ui.summaries[1], "链路详情", "服务已启动，但 8 秒内未确认连接成功。")
	assertNoSummaryRow(t, ui.summaries[1], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[1], "Data endpoint")
}

func TestInstallClientHTTPSCertificateFailureCanSkipWithoutConsumingKeyDuringProbe(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"https://netsgo.zsio.dev", ""},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true, true},
	}
	var probeSkips []bool
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
		CheckServerTLS: func(addr clientaddr.Address, skipVerify bool) error {
			if len(ui.passwordCalls) != 0 {
				t.Fatal("TLS probe should happen before asking for the client key")
			}
			probeSkips = append(probeSkips, skipVerify)
			if !skipVerify {
				return x509.UnknownAuthorityError{}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("client install with accepted TLS skip should not error: %v", err)
	}
	if len(probeSkips) != 2 || probeSkips[0] || !probeSkips[1] {
		t.Fatalf("TLS probe skip sequence = %#v, want []bool{false, true}", probeSkips)
	}
	if !writtenEnv.TLSSkipVerify {
		t.Fatalf("accepted TLS skip should be written to env, got %#v", writtenEnv)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "HTTPS 证书校验失败，是否跳过 TLS 证书校验？")
	assertConfirmPrompt(t, ui.confirmCalls, "继续安装？")
	assertSummaryRow(t, ui.summaries[0], "跳过 TLS 校验", "是")
	assertSummaryRow(t, ui.summaries[0], "TLS 风险", "连接会加密，但不会验证服务端证书身份")
}

func TestInstallClientHTTPSCertificateFailureCanPinFingerprint(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"https://netsgo.zsio.dev", "AA:BB:CC"},
		passwords: []string{"sk-test-key"},
		confirms:  []bool{true},
	}
	var probeSkips []bool
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
		CheckServerTLS: func(addr clientaddr.Address, skipVerify bool) error {
			probeSkips = append(probeSkips, skipVerify)
			return x509.UnknownAuthorityError{}
		},
	})
	if err != nil {
		t.Fatalf("client install with pinned TLS fingerprint should not error: %v", err)
	}
	if len(probeSkips) != 1 || probeSkips[0] {
		t.Fatalf("TLS probe skip sequence = %#v, want []bool{false}", probeSkips)
	}
	if writtenEnv.TLSSkipVerify {
		t.Fatalf("pinned TLS fingerprint should not enable skip verify, got %#v", writtenEnv)
	}
	if writtenEnv.TLSFingerprint != "AA:BB:CC" {
		t.Fatalf("pinned TLS fingerprint should be written to env, got %#v", writtenEnv)
	}
	assertNoConfirmPrompt(t, ui.confirmCalls, "HTTPS 证书校验失败，是否跳过 TLS 证书校验？")
	assertConfirmPrompt(t, ui.confirmCalls, "继续安装？")
	assertSummaryRow(t, ui.summaries[0], "TLS 指纹", "AA:BB:CC")
	assertNoSummaryRow(t, ui.summaries[0], "跳过 TLS 校验")
}

func TestInstallClientHTTPSCertificateFailureDeclinedDoesNotAskForKey(t *testing.T) {
	ui := &fakeUI{
		inputs:   []string{"https://netsgo.zsio.dev", ""},
		confirms: []bool{false},
	}
	err := InstallClientWith(clientDeps{
		UI:             ui,
		Detect:         func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		CheckServerTLS: func(addr clientaddr.Address, skipVerify bool) error { return x509.UnknownAuthorityError{} },
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPS 证书校验失败") {
		t.Fatalf("declined TLS skip should return certificate error, got %v", err)
	}
	if len(ui.passwordCalls) != 0 {
		t.Fatalf("client key should not be requested after declined TLS skip: %#v", ui.passwordCalls)
	}
	assertConfirmPrompt(t, ui.confirmCalls, "HTTPS 证书校验失败，是否跳过 TLS 证书校验？")
	assertNoConfirmPrompt(t, ui.confirmCalls, "继续安装？")
}

func TestInstallClientHTTPSNonCertificateFailureDoesNotOfferTLSSkip(t *testing.T) {
	ui := &fakeUI{
		inputs: []string{"https://netsgo.zsio.dev"},
	}
	err := InstallClientWith(clientDeps{
		UI:             ui,
		Detect:         func(role svcmgr.Role) svcmgr.InstallState { return svcmgr.StateNotInstalled },
		CheckServerTLS: func(addr clientaddr.Address, skipVerify bool) error { return errors.New("connection refused") },
	})
	if err == nil || !strings.Contains(err.Error(), "无法连接 HTTPS 服务") {
		t.Fatalf("non-certificate HTTPS failure should return connection error, got %v", err)
	}
	assertNoConfirmPrompt(t, ui.confirmCalls, "HTTPS 证书校验失败，是否跳过 TLS 证书校验？")
	if len(ui.passwordCalls) != 0 {
		t.Fatalf("client key should not be requested after HTTPS connection failure: %#v", ui.passwordCalls)
	}
}

func TestInstallClientAcceptsLegacyWSSAndWritesHTTPSServiceAddress(t *testing.T) {
	ui := &fakeUI{
		inputs:    []string{"wss://netsgo.zsio.dev"},
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
		VerifyClientLink: fakeClientLink(ClientLinkEstablished),
	})
	if err != nil {
		t.Fatalf("legacy WSS client install should not error: %v", err)
	}
	if writtenEnv.Server != "https://netsgo.zsio.dev" {
		t.Fatalf("NETSGO_SERVER should be normalized HTTPS service address, got %#v", writtenEnv)
	}
	if writtenEnv.TLSSkipVerify || writtenEnv.TLSFingerprint != "" {
		t.Fatalf("client install should not write TLS bypass settings, got %#v", writtenEnv)
	}
	assertNoConfirmPrompt(t, ui.confirmCalls, "跳过 TLS 证书校验？")
	assertSummaryRow(t, ui.summaries[0], "服务地址", "https://netsgo.zsio.dev")
	assertSummaryRow(t, ui.summaries[0], "TLS 状态", "启用")
	assertNoSummaryRow(t, ui.summaries[0], "跳过 TLS 校验")
	assertNoSummaryRow(t, ui.summaries[0], "TLS 指纹")
	assertNoSummaryRow(t, ui.summaries[0], "Control endpoint")
	assertNoSummaryRow(t, ui.summaries[0], "Data endpoint")
}

func assertClientKeyPromptUsesChineseTitle(t *testing.T, calls []inputCall) {
	t.Helper()
	for _, call := range calls {
		if call.prompt == "Client key" {
			t.Fatalf("client key prompt should not use the raw English title: %#v", calls)
		}
		if call.prompt == "客户端接入密钥" {
			if !strings.Contains(call.opts.Description, "client key") {
				t.Fatalf("client key prompt should preserve the technical token in description, got %#v", call.opts)
			}
			return
		}
	}
	t.Fatalf("client key prompt not found in %#v", calls)
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

func fakeClientLinkWithDetail(state ClientLinkState, detail string) func(string, time.Time, time.Duration) ClientLinkEvidence {
	return func(unit string, since time.Time, timeout time.Duration) ClientLinkEvidence {
		return ClientLinkEvidence{State: state, Detail: detail}
	}
}

func assertConfirmPrompt(t *testing.T, calls []confirmCall, prompt string) {
	t.Helper()
	for _, call := range calls {
		if call.prompt == prompt {
			if call.confirmText != "" {
				t.Fatalf("confirm %q should use yes/no typed confirmation, got required phrase %q", prompt, call.confirmText)
			}
			return
		}
	}
	t.Fatalf("confirm %q not called; calls=%#v", prompt, calls)
}

func assertNoConfirmPrompt(t *testing.T, calls []confirmCall, prompt string) {
	t.Helper()
	for _, call := range calls {
		if call.prompt == prompt {
			t.Fatalf("confirm %q should not be called; calls=%#v", prompt, calls)
		}
	}
}

func assertNoInputPrompt(t *testing.T, calls []inputCall, prompt string) {
	t.Helper()
	for _, call := range calls {
		if call.prompt == prompt {
			t.Fatalf("input %q should not be called; calls=%#v", prompt, calls)
		}
	}
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

func assertClientServerInputUsesConsoleAddressWording(t *testing.T, calls []inputCall) {
	t.Helper()
	for _, call := range calls {
		if call.prompt != "服务地址" {
			continue
		}
		if call.opts.Placeholder != "https://netsgo.domain.com" {
			t.Fatalf("console address placeholder = %q", call.opts.Placeholder)
		}
		if call.opts.Description != "请输入服务端控制台地址, 通常是http(s)://域名" {
			t.Fatalf("console address description = %q", call.opts.Description)
		}
		if strings.Contains(call.opts.Description, "control/data") {
			t.Fatalf("console address description should not expose control/data endpoints, got %q", call.opts.Description)
		}
		return
	}
	t.Fatalf("console address input prompt not called; calls=%#v", calls)
}
