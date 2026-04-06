package install

import (
	"os"
	"os/user"
	"strconv"

	"netsgo/internal/svcmgr"
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
		printInstalledSummary(deps.UI, "客户端已安装")
		return nil
	case svcmgr.StateHistoricalDataOnly:
		deps.UI.PrintSummary("客户端安装状态异常", [][2]string{{"状态", inspection.State.String()}, {"建议", "客户端不支持恢复旧数据；请清理残留数据后重新安装并重新认证"}, {"问题", firstProblem(inspection.Problems)}})
		return errInstallBrokenState
	case svcmgr.StateBroken:
		printBrokenSummary(deps.UI, "客户端安装状态异常", inspection)
		return errInstallBrokenState
	}

	serverURL, err := deps.UI.Input("服务端地址")
	if err != nil {
		return err
	}
	clientKey, err := deps.UI.Password("客户端 Key")
	if err != nil {
		return err
	}
	tlsSkipVerify, err := deps.UI.Confirm("跳过 TLS 证书校验?")
	if err != nil {
		return err
	}
	tlsFingerprint, err := deps.UI.Input("TLS 证书指纹")
	if err != nil {
		return err
	}

	deps.UI.PrintSummary("安装配置确认", [][2]string{{"角色", "client"}, {"服务端", serverURL}, {"跳过 TLS 校验", boolText(tlsSkipVerify)}, {"TLS 指纹", tlsFingerprint}})
	ok, err := deps.UI.Confirm("确认安装?")
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
	deps.UI.PrintSummary("客户端安装完成", [][2]string{{"状态", "运行中"}, {"连接到", serverURL}, {"下一步", "运行 netsgo manage 管理服务"}})
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
		return "是"
	}
	return "否"
}

func firstProblem(problems []string) string {
	if len(problems) == 0 {
		return "安装状态异常"
	}
	return problems[0]
}
