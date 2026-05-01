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
	ClientLinkEstablished    ClientLinkState = "已建立"
	ClientLinkNotEstablished ClientLinkState = "8 秒内未建立"
	ClientLinkNotVerified    ClientLinkState = "未验证"
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
		printInstalledSummary(deps.UI, "Client 已安装", svcmgr.RoleClient)
		return nil
	case svcmgr.StateHistoricalDataOnly:
		deps.UI.PrintSummary("Client 安装状态异常", [][2]string{
			{"状态", "需要清理"},
			{"建议", "检测到残留 client 数据；请先清理残留状态后重新安装"},
			{"问题", userFacingInstallProblem(firstProblem(inspection.Problems))},
		})
		return errInstallBrokenState
	case svcmgr.StateBroken:
		printBrokenSummary(deps.UI, "Client 安装状态异常", inspection)
		return errInstallBrokenState
	}

	serverInput, err := deps.UI.Input("服务地址", tui.InputOptions{
		Placeholder: "e.g. http://netsgo.example.com:9527",
		Description: "粘贴 Web 控制台或 server 安装摘要中的服务地址，通常为 http(s)://；兼容旧的 ws(s):// 输入并会自动规范化。",
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
	clientKey, err := deps.UI.Password("客户端接入密钥", tui.InputOptions{
		Placeholder: "sk-...",
		Description: "从 Web 控制台的 Clients 页面获取 client key。",
		Validate: func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("客户端接入密钥不能为空")
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
		tlsSkipVerify, err = deps.UI.Confirm("跳过 TLS 证书校验？")
		if err != nil {
			return err
		}
		if !tlsSkipVerify {
			tlsFingerprint, err = deps.UI.Input("TLS 证书指纹", tui.InputOptions{
				Placeholder: "AA:BB:CC:...",
				Description: "用于固定自签名证书的 SHA-256 指纹（可选）。",
			})
			if err != nil {
				return err
			}
		}
	}

	deps.UI.PrintSummary("安装摘要", confirmSummaryRows(svcmgr.RoleClient,
		[2]string{"服务地址", serverURL},
		[2]string{"TLS 状态", ternary(usesTLS, "启用", "未启用")},
		[2]string{"跳过 TLS 校验", ternary(usesTLS, boolText(tlsSkipVerify), "不适用")},
		[2]string{"TLS 指纹", ternary(tlsFingerprint != "", tlsFingerprint, "未设置")},
	))
	ok, err := deps.UI.ConfirmWithOptions("继续安装？", tui.ConfirmOptions{})
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
	deps.UI.PrintSummary("Client 安装完成", clientCompletionSummaryRows(serverURL, link))
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

func clientCompletionSummaryRows(serverURL string, link ClientLinkEvidence) [][2]string {
	rows := [][2]string{
		{"状态", "运行中"},
		{"服务", svcmgr.UnitName(svcmgr.RoleClient)},
		{"运行用户", svcmgr.SystemUser},
		[2]string{"服务地址", serverURL},
		[2]string{"NetsGo 链路", string(link.State)},
	}
	if link.Detail != "" {
		rows = append(rows, [2]string{"链路详情", link.Detail})
	}
	rows = append(rows, [2]string{"日志", journalctlCommand(svcmgr.RoleClient)})
	if link.State != ClientLinkEstablished {
		rows = append(rows,
			[2]string{"建议", "检查 DNS/服务地址、客户端接入密钥、TLS 设置、server 服务和 client 日志"},
		)
	}
	rows = append(rows, [2]string{"下一步", "运行 netsgo manage 管理服务"})
	return rows
}

func defaultVerifyClientLink(unit string, since time.Time, timeout time.Duration) ClientLinkEvidence {
	deadline := time.Now().Add(timeout)
	for {
		output, err := clientLinkJournalOutput(unit, since)
		if err != nil {
			return ClientLinkEvidence{
				State:  ClientLinkNotVerified,
				Detail: "无法读取 systemd journal；请手动检查 client 日志。",
			}
		}
		if clientLinkEstablishedFromLogs(string(output)) {
			return ClientLinkEvidence{State: ClientLinkEstablished}
		}
		if time.Now().After(deadline) {
			return ClientLinkEvidence{
				State:  ClientLinkNotEstablished,
				Detail: "服务已启动，但 8 秒内未确认连接成功。",
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
		return "是"
	}
	return "否"
}

func ternary(ok bool, a, b string) string {
	if ok {
		return a
	}
	return b
}

func firstProblem(problems []string) string {
	if len(problems) == 0 {
		return "未知错误"
	}
	return problems[0]
}
