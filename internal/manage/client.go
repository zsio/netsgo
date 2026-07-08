package manage

import (
	"errors"
	"path/filepath"

	clientstate "netsgo/internal/client"
	"netsgo/internal/install"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

type clientDeps struct {
	UI                        uiProvider
	Inspect                   func() svcmgr.InstallInspection
	IsActive                  func() (bool, error)
	IsEnabled                 func() (bool, error)
	Logs                      func() error
	RunInstall                func() error
	ReadClientEnv             func() (svcmgr.ClientEnv, error)
	DisableAndStop            func() error
	UpdateClientKey           func(string) error
	PreflightClientTokenClear func() error
	ClearClientToken          func() (clientstate.ClientIdentity, bool, error)
	EnableAndStart            func() error
	DaemonReload              func() error
	RemovePaths               func(paths ...string) error
	RemoveBinary              func() error
	DetectServer              func() svcmgr.InstallState
}

func ManageClient() error {
	return ManageClientWith(defaultClientDeps())
}

func ManageClientWith(deps clientDeps) error {
	inspection := deps.Inspect()
	switch inspection.State {
	case svcmgr.StateInstalled:
		return runServiceMenu(serviceMenuDeps{
			UI:   deps.UI,
			Role: svcmgr.RoleClient,
			Status: func() error {
				return showStatusSummary(deps.UI, svcmgr.RoleClient, deps.Inspect(), deps.IsActive, deps.IsEnabled)
			},
			Detail: func() error {
				return showClientDetails(deps)
			},
			Logs:  deps.Logs,
			Start: deps.EnableAndStart,
			Stop:  deps.DisableAndStop,
			Uninstall: func() (bool, error) {
				return uninstallClient(deps)
			},
			Extra: clientServiceMenuActions(deps),
		})
	case svcmgr.StateBroken:
		return runBrokenClientMenu(deps)
	default:
		deps.UI.PrintSummary("Client 未安装", [][2]string{{"下一步", "运行 netsgo install 安装 client"}})
		return errReturnToSelection
	}
}

func clientServiceMenuActions(deps clientDeps) []serviceMenuAction {
	return []serviceMenuAction{
		{
			Option: tui.SelectOption{
				Label:       "重新认证",
				Description: "输入新的 client key，清空本地 token 并重启 client 服务。",
			},
			Run: func() error {
				return reauthenticateClient(deps)
			},
		},
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
			return execJournal(svcmgr.UnitName(svcmgr.RoleClient))
		},
		RunInstall: func() error {
			return install.Run()
		},
		ReadClientEnv: func() (svcmgr.ClientEnv, error) {
			return svcmgr.ReadClientEnv(svcmgr.NewLayout(svcmgr.RoleClient))
		},
		DisableAndStop: func() error { return svcmgr.DisableAndStop(svcmgr.UnitName(svcmgr.RoleClient)) },
		UpdateClientKey: func(key string) error {
			return svcmgr.UpdateClientKey(svcmgr.NewLayout(svcmgr.RoleClient), key)
		},
		PreflightClientTokenClear: func() error {
			layout := svcmgr.NewLayout(svcmgr.RoleClient)
			if err := svcmgr.CheckClientRuntimeState(layout); err != nil {
				return err
			}
			return clientstate.CheckClientTokenClear(filepath.Join(clientDataPath(layout), clientstate.ClientDBFileName))
		},
		ClearClientToken: func() (clientstate.ClientIdentity, bool, error) {
			layout := svcmgr.NewLayout(svcmgr.RoleClient)
			if err := svcmgr.RepairClientRuntimeOwnership(layout); err != nil {
				return clientstate.ClientIdentity{}, false, err
			}
			identity, ok, err := clientstate.ClearClientToken(filepath.Join(clientDataPath(layout), clientstate.ClientDBFileName))
			if err != nil {
				return clientstate.ClientIdentity{}, false, err
			}
			if err := svcmgr.RepairClientRuntimeOwnership(layout); err != nil {
				return clientstate.ClientIdentity{}, false, err
			}
			return identity, ok, nil
		},
		EnableAndStart: func() error { return svcmgr.EnableAndStart(svcmgr.UnitName(svcmgr.RoleClient)) },
		DaemonReload:   svcmgr.DaemonReload,
		RemovePaths:    removePaths,
		RemoveBinary:   svcmgr.RemoveBinary,
		DetectServer: func() svcmgr.InstallState {
			return svcmgr.Detect(svcmgr.RoleServer)
		},
	}
}

func uninstallClient(deps clientDeps) (bool, error) {
	layout := svcmgr.NewLayout(svcmgr.RoleClient)

	rows := [][2]string{
		{"影响", "移除托管 client 服务和本地连接状态"},
		{"结果", "重新安装 client 时请从 Web 控制台获取新的 client key"},
		{"结果", "不会自动清理 server 端历史记录"},
	}
	rows = appendRemovalRows(rows, "移除", layout.UnitPath, layout.EnvPath, clientDataPath(layout), roleLockPath(layout))
	rows = append(rows, sharedBinaryPlanRow(deps.DetectServer))
	deps.UI.PrintSummary("Client 卸载计划", rows)

	ok, err := deps.UI.ConfirmWithOptions("继续卸载 client？", tui.ConfirmOptions{ConfirmText: "uninstall client"})
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
	if err := deps.RemovePaths(layout.UnitPath, layout.EnvPath, clientDataPath(layout), roleLockPath(layout)); err != nil {
		return false, err
	}
	if err := deps.DaemonReload(); err != nil {
		return false, err
	}
	if err := maybeRemoveSharedBinary(deps.UI, deps.DetectServer, deps.RemoveBinary); err != nil {
		return false, err
	}
	deps.UI.PrintSummary("Client 已卸载", [][2]string{{"状态", "已移除"}, {"下一步", "需要时运行 netsgo install 重新安装 client"}})
	return true, nil
}

func runBrokenClientMenu(deps clientDeps) error {
	for {
		action, err := selectWithOptions(deps.UI, "选择恢复操作", []tui.SelectOption{
			{Label: "检查异常 client 状态", Description: "选择清理前显示检测到的 client 服务问题。"},
			{Label: "清理异常 client 安装", Description: "通过输入确认短语移除残留的 client 服务文件。"},
			{Label: "运行 netsgo install", Description: "清理或确认当前状态后重新安装 client。"},
			{Label: "返回", Description: "返回上一级服务选择菜单。"},
		})
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
	layout := svcmgr.NewLayout(svcmgr.RoleClient)

	rows := [][2]string{
		{"影响", "移除异常 client 服务文件和本地连接状态"},
		{"结果", "重新安装 client 时请从 Web 控制台获取新的 client key"},
	}
	rows = appendRemovalRows(rows, "移除", layout.UnitPath, layout.EnvPath, clientDataPath(layout), roleLockPath(layout))
	rows = append(rows, sharedBinaryPlanRow(deps.DetectServer))
	deps.UI.PrintSummary("异常 client 清理计划", rows)

	ok, err := deps.UI.ConfirmWithOptions("继续清理异常 client？", tui.ConfirmOptions{ConfirmText: "cleanup client"})
	if err != nil {
		return false, err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return false, nil
	}
	if err := deps.RemovePaths(layout.UnitPath, layout.EnvPath, clientDataPath(layout), roleLockPath(layout)); err != nil {
		return false, err
	}
	if err := deps.DaemonReload(); err != nil {
		return false, err
	}
	if err := maybeRemoveSharedBinary(deps.UI, deps.DetectServer, deps.RemoveBinary); err != nil {
		return false, err
	}
	deps.UI.PrintSummary("异常 client 清理完成", [][2]string{{"状态", "已清理"}, {"下一步", "需要时运行 netsgo install 重新安装 client"}})
	return true, nil
}
