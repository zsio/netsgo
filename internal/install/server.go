package install

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"netsgo/internal/server"
	"netsgo/internal/svcmgr"
)

var errInstallBrokenState = errors.New("install: broken existing state")

type serverDeps struct {
	UI                uiProvider
	Inspect           func(svcmgr.Role) svcmgr.InstallInspection
	Detect            func(svcmgr.Role) svcmgr.InstallState
	SelectTLSMode     func(ui uiProvider) (string, error)
	LoadRecoverable   func() (server.InitParams, error)
	EnsureUser        func(string) error
	EnsureDirs        func() error
	ApplyInit         func(string, server.InitParams) error
	CurrentBinaryPath func() (string, error)
	InstallBinary     func(string) error
	WriteServerSpec   func(svcmgr.ServiceSpec) error
	WriteServerEnv    func(svcmgr.ServiceSpec, svcmgr.ServerEnv) error
	WriteServerUnit   func(svcmgr.ServiceSpec) error
	DaemonReload      func() error
	EnableAndStart    func(string) error
}

func InstallServer() error {
	return InstallServerWith(defaultServerDeps())
}

func InstallServerWith(deps serverDeps) error {
	inspection := resolveInspection(deps.Inspect, deps.Detect, svcmgr.RoleServer)
	state := inspection.State
	switch state {
	case svcmgr.StateInstalled:
		printInstalledSummary(deps.UI, "服务端已安装")
		return nil
	case svcmgr.StateBroken:
		printBrokenSummary(deps.UI, "服务端安装状态异常", inspection)
		return errInstallBrokenState
	}

	portRaw, err := deps.UI.Input("监听端口（留空使用默认值 8080）")
	if err != nil {
		return err
	}
	port := 8080
	if portRaw != "" {
		port, err = strconv.Atoi(portRaw)
		if err != nil {
			return err
		}
	}
	tlsMode, err := deps.SelectTLSMode(deps.UI)
	if err != nil {
		return err
	}
	trustedProxies, err := deps.UI.Input("受信任代理 CIDR（可留空，例：127.0.0.1/8,192.168.0.0/16）")
	if err != nil {
		return err
	}
	tlsCert := ""
	tlsKey := ""
	if tlsMode == "custom" {
		tlsCert, err = deps.UI.Input("TLS 证书路径（例：/etc/ssl/certs/netsgo.pem）")
		if err != nil {
			return err
		}
		tlsKey, err = deps.UI.Input("TLS 私钥路径（例：/etc/ssl/private/netsgo.key）")
		if err != nil {
			return err
		}
	}

	serverAddr := ""
	initParams := server.InitParams{}
	if state == svcmgr.StateHistoricalDataOnly {
		deps.UI.PrintSummary("检测到服务端历史数据", [][2]string{{"状态", "可恢复"}, {"说明", "如果继续，将沿用现有管理员、ServerAddr、AllowedPorts 与其他运行数据"}})
		ok, err := deps.UI.Confirm("是否使用原有数据继续安装?")
		if err != nil {
			return err
		}
		if !ok {
			deps.UI.PrintSummary("安装已取消", [][2]string{{"下一步", "如需重新初始化，请先清理旧的 server 数据后再重新安装"}})
			return nil
		}
		if deps.LoadRecoverable == nil {
			return errors.New("install dependencies are incomplete")
		}
		initParams, err = deps.LoadRecoverable()
		if err != nil {
			return err
		}
		serverAddr = initParams.ServerAddr
	} else {
		serverAddr, err = deps.UI.Input("服务对外访问地址（例：https://netsgo.example.com）")
		if err != nil {
			return err
		}
		initParams.ServerAddr = serverAddr
		initParams.AdminUsername, err = deps.UI.Input("管理员用户名（例：admin）")
		if err != nil {
			return err
		}
		initParams.AdminPassword, err = deps.UI.Password("管理员密码")
		if err != nil {
			return err
		}
		initParams.AllowedPorts, err = deps.UI.Input("允许的端口范围（例：1-65535）")
		if err != nil {
			return err
		}
	}

	deps.UI.PrintSummary("安装配置确认", [][2]string{{"角色", "server"}, {"端口", strconv.Itoa(port)}, {"TLS 模式", tlsMode}, {"服务地址", serverAddr}, {"受信任代理", trustedProxies}})
	ok, err := deps.UI.Confirm("确认安装?")
	if err != nil {
		return err
	}
	if !ok {
		printInstallCancelled(deps.UI)
		return nil
	}

	if state != svcmgr.StateHistoricalDataOnly {
		if err := deps.ApplyInit(svcmgr.ManagedDataDir, initParams); err != nil {
			return err
		}
	}
	if err := completeManagedInstall(svcmgr.RoleServer, managedInstallDeps{
		EnsureUser:        deps.EnsureUser,
		EnsureDirs:        deps.EnsureDirs,
		CurrentBinaryPath: deps.CurrentBinaryPath,
		InstallBinary:     deps.InstallBinary,
		DaemonReload:      deps.DaemonReload,
		EnableAndStart:    deps.EnableAndStart,
	}, func(spec svcmgr.ServiceSpec) error {
		spec.ListenPort = port
		spec.TLSMode = tlsMode
		spec.ServerURL = serverAddr
		if err := deps.WriteServerSpec(spec); err != nil {
			return err
		}
		if err := deps.WriteServerEnv(spec, svcmgr.ServerEnv{Port: port, TLSMode: tlsMode, TLSCert: tlsCert, TLSKey: tlsKey, TrustedProxies: trustedProxies, ServerAddr: serverAddr}); err != nil {
			return err
		}
		return deps.WriteServerUnit(spec)
	}); err != nil {
		return err
	}
	deps.UI.PrintSummary("服务端安装完成", [][2]string{{"状态", "运行中"}, {"面板地址", serverAddr}, {"下一步", "运行 netsgo manage 管理服务"}})
	return nil
}

func defaultServerDeps() serverDeps {
	return serverDeps{
		UI:      defaultUI{},
		Inspect: svcmgr.Inspect,
		Detect:  svcmgr.Detect,
		SelectTLSMode: func(ui uiProvider) (string, error) {
			index, err := ui.Select("TLS 模式", []string{
				"off    — 不使用 TLS（适合放在反代后部署）",
				"auto   — 自动生成自签证书（TOFU 模式）",
				"custom — 使用自定义证书文件",
			})
			if err != nil {
				return "", err
			}
			return []string{"off", "auto", "custom"}[index], nil
		},
		LoadRecoverable: func() (server.InitParams, error) {
			return server.LoadRecoverableInitParams(svcmgr.ManagedDataDir)
		},
		EnsureUser:        svcmgr.EnsureUser,
		EnsureDirs:        ensureManagedServerDirs,
		ApplyInit:         server.ApplyInit,
		CurrentBinaryPath: svcmgr.CurrentBinaryPath,
		InstallBinary:     svcmgr.InstallBinary,
		WriteServerSpec:   svcmgr.WriteServerSpec,
		WriteServerEnv:    svcmgr.WriteServerEnv,
		WriteServerUnit:   svcmgr.WriteServerUnit,
		DaemonReload:      svcmgr.DaemonReload,
		EnableAndStart:    svcmgr.EnableAndStart,
	}
}

func ensureManagedServerDirs() error {
	if err := os.MkdirAll(svcmgr.ManagedDataDir+"/server", 0o750); err != nil {
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
	serverDir := svcmgr.ManagedDataDir + "/server"
	if err := filepath.WalkDir(serverDir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, uid, gid)
	}); err != nil {
		return err
	}
	return os.Chown(svcmgr.ManagedDataDir+"/locks", uid, gid)
}
