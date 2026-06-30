package install

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"netsgo/internal/server"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
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
	WriteServerEnv    func(svcmgr.ServiceLayout, svcmgr.ServerEnv) error
	WriteServerUnit   func(svcmgr.ServiceLayout) error
	ValidateCustomTLS func(certPath, keyPath string) error
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
		printInstalledSummary(deps.UI, "Server 已安装", svcmgr.RoleServer)
		return nil
	case svcmgr.StateBroken:
		printBrokenSummary(deps.UI, "Server 安装状态异常", inspection)
		return errInstallBrokenState
	}

	serverAddr := ""
	initParams := server.InitParams{}
	installMode := "fresh"
	if state == svcmgr.StateHistoricalDataOnly {
		printRecoverableSummary(deps.UI, inspection)
		ok, err := deps.UI.Confirm("使用现有数据继续安装？")
		if err != nil {
			return err
		}
		if !ok {
			printInstallCancelled(deps.UI)
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
		installMode = "恢复现有数据"
	}

	portRaw, err := deps.UI.Input("监听端口", tui.InputOptions{
		Placeholder: "e.g. 9527",
		Description: "server 监听的 TCP 端口（1024-65535）。",
		Default:     "9527",
		Validate: func(s string) error {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1024 || n > 65535 {
				return fmt.Errorf("端口必须是 1024 到 65535 之间的数字")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	port := 9527
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
	trustedProxies, err := deps.UI.Input("可信代理 CIDR", tui.InputOptions{
		Placeholder: "e.g. 127.0.0.1/8,10.0.0.0/8 or 0.0.0.0/0",
		Description: "逗号分隔的可信代理 CIDR；默认仅信任本机回环地址。若 NetsGo 位于反向代理后方，可添加代理所在网段；若需信任所有来源可设为 0.0.0.0/0，但需注意此配置允许客户端伪造 X-Forwarded-For。",
		Default:     "127.0.0.1/8",
	})
	if err != nil {
		return err
	}
	if tlsMode != "off" {
		trustedProxies = ""
	}
	tlsCert := ""
	tlsKey := ""
	if tlsMode == "custom" {
		tlsCert, err = deps.UI.Input("TLS 证书路径", tui.InputOptions{
			Placeholder: "e.g. /etc/ssl/certs/netsgo.pem",
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("TLS 证书路径不能为空")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
		tlsKey, err = deps.UI.Input("TLS 私钥路径", tui.InputOptions{
			Placeholder: "e.g. /etc/ssl/private/netsgo.key",
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("TLS 私钥路径不能为空")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
	}
	if state != svcmgr.StateHistoricalDataOnly {
		serverAddr, err = deps.UI.Input("Server 外部访问地址", tui.InputOptions{
			Placeholder: "e.g. https://netsgo.example.com",
			Description: "client 访问此 server 的公网 URL（http:// 或 https://）。",
			Validate:    validateInstallServerAddr,
		})
		if err != nil {
			return err
		}
		initParams.ServerAddr = serverAddr
		initParams.AdminUsername, err = deps.UI.Input("管理员用户名", tui.InputOptions{
			Placeholder: "e.g. admin",
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("管理员用户名不能为空")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
		initParams.AdminPassword, err = deps.UI.Password("管理员密码", tui.InputOptions{
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("管理员密码不能为空")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
		confirmPassword, err := deps.UI.Password("确认管理员密码", tui.InputOptions{
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("管理员密码确认不能为空")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
		if initParams.AdminPassword != confirmPassword {
			return fmt.Errorf("两次输入的管理员密码不一致")
		}
	}

	deps.UI.PrintSummary("安装摘要", confirmSummaryRows(svcmgr.RoleServer,
		[2]string{"安装模式", installMode},
		[2]string{"端口", strconv.Itoa(port)},
		[2]string{"TLS 模式", tlsMode},
		[2]string{"服务地址", serverAddr},
		[2]string{"可信代理", trustedProxies},
	))
	ok, err := deps.UI.ConfirmWithOptions("继续安装？", tui.ConfirmOptions{})
	if err != nil {
		return err
	}
	if !ok {
		printInstallCancelled(deps.UI)
		return nil
	}

	if tlsMode == "custom" {
		if deps.ValidateCustomTLS == nil {
			return errors.New("install dependencies are incomplete")
		}
		if err := deps.EnsureUser(svcmgr.SystemUser); err != nil {
			return err
		}
		if err := deps.ValidateCustomTLS(tlsCert, tlsKey); err != nil {
			return err
		}
	}
	if state != svcmgr.StateHistoricalDataOnly {
		if err := deps.EnsureUser(svcmgr.SystemUser); err != nil {
			return err
		}
		if err := deps.EnsureDirs(); err != nil {
			return err
		}
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
	}, func(layout svcmgr.ServiceLayout) error {
		if err := deps.WriteServerEnv(layout, svcmgr.ServerEnv{Port: port, TLSMode: tlsMode, TLSCert: tlsCert, TLSKey: tlsKey, TrustedProxies: trustedProxies, ServerAddr: serverAddr, AllowLoopbackManagementHost: true, AllowLoopbackManagementHostDefined: true}); err != nil {
			return err
		}
		return deps.WriteServerUnit(layout)
	}); err != nil {
		return err
	}
	deps.UI.PrintSummary("Server 安装完成", completionSummaryRows(svcmgr.RoleServer, "Web 控制台", serverAddr))
	return nil
}

func defaultServerDeps() serverDeps {
	return serverDeps{
		UI:      defaultUI{},
		Inspect: svcmgr.Inspect,
		Detect:  svcmgr.Detect,
		SelectTLSMode: func(ui uiProvider) (string, error) {
			index, err := selectWithOptions(ui, "TLS 模式", []tui.SelectOption{
				{Label: "off", Description: "不启用 TLS；适合由反向代理终止 HTTPS 的部署。"},
				{Label: "auto", Description: "生成自签名证书，供 client 首次信任使用。"},
				{Label: "custom", Description: "使用已有证书和私钥文件。"},
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
		WriteServerEnv:    svcmgr.WriteServerEnv,
		WriteServerUnit:   svcmgr.WriteServerUnit,
		ValidateCustomTLS: func(certPath, keyPath string) error {
			return validateReadableCustomTLSFiles(certPath, keyPath, svcmgr.SystemUser)
		},
		DaemonReload:   svcmgr.DaemonReload,
		EnableAndStart: svcmgr.EnableAndStart,
	}
}
