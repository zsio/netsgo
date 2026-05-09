package manage

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"netsgo/internal/install"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

type serverDeps struct {
	UI             uiProvider
	Inspect        func() svcmgr.InstallInspection
	IsActive       func() (bool, error)
	IsEnabled      func() (bool, error)
	Logs           func() error
	RunInstall     func() error
	ReadServerEnv  func() (svcmgr.ServerEnv, error)
	DisableAndStop func() error
	EnableAndStart func() error
	DaemonReload   func() error
	RemovePaths    func(paths ...string) error
	RemoveBinary   func() error
	DetectClient   func() svcmgr.InstallState
}

func ManageServer() error {
	return ManageServerWith(defaultServerDeps())
}

func ManageServerWith(deps serverDeps) error {
	inspection := deps.Inspect()
	switch inspection.State {
	case svcmgr.StateInstalled:
		return runServiceMenu(serviceMenuDeps{
			UI:   deps.UI,
			Role: svcmgr.RoleServer,
			Status: func() error {
				return showStatusSummary(deps.UI, svcmgr.RoleServer, deps.Inspect(), deps.IsActive, deps.IsEnabled)
			},
			Detail: func() error {
				return showServerDetails(deps)
			},
			Logs:  deps.Logs,
			Start: deps.EnableAndStart,
			Stop:  deps.DisableAndStop,
			Uninstall: func() (bool, error) {
				return uninstallServer(deps)
			},
		})
	case svcmgr.StateHistoricalDataOnly:
		return runRecoverableServerMenu(deps)
	case svcmgr.StateBroken:
		return runBrokenServerMenu(deps)
	default:
		deps.UI.PrintSummary("Server 未安装", [][2]string{{"下一步", "运行 netsgo install 安装 server"}})
		return errReturnToSelection
	}
}

func defaultServerDeps() serverDeps {
	return serverDeps{
		UI: defaultUI{},
		Inspect: func() svcmgr.InstallInspection {
			return svcmgr.Inspect(svcmgr.RoleServer)
		},
		IsActive: func() (bool, error) {
			return svcmgr.IsActive(svcmgr.UnitName(svcmgr.RoleServer))
		},
		IsEnabled: func() (bool, error) {
			return svcmgr.IsEnabled(svcmgr.UnitName(svcmgr.RoleServer))
		},
		Logs: func() error {
			args := svcmgr.JournalArgs(svcmgr.UnitName(svcmgr.RoleServer), 100)
			return syscall.Exec("/usr/bin/journalctl", args, os.Environ())
		},
		RunInstall: func() error {
			return install.Run()
		},
		ReadServerEnv: func() (svcmgr.ServerEnv, error) {
			return svcmgr.ReadServerEnv(svcmgr.NewLayout(svcmgr.RoleServer))
		},
		DisableAndStop: func() error { return svcmgr.DisableAndStop(svcmgr.UnitName(svcmgr.RoleServer)) },
		EnableAndStart: func() error { return svcmgr.EnableAndStart(svcmgr.UnitName(svcmgr.RoleServer)) },
		DaemonReload:   svcmgr.DaemonReload,
		RemovePaths:    removePaths,
		RemoveBinary:   svcmgr.RemoveBinary,
		DetectClient: func() svcmgr.InstallState {
			return svcmgr.Detect(svcmgr.RoleClient)
		},
	}
}

func showServerDetails(deps serverDeps) error {
	inspection := deps.Inspect()
	layout := svcmgr.NewLayout(svcmgr.RoleServer)
	env, envErr := loadServerEnv(deps)

	rows := [][2]string{
		{"服务", layout.ServiceName},
		{"角色", string(svcmgr.RoleServer)},
		{"状态", lifecycleStateLabel(inspection.State)},
		{"已安装", boolLabel(inspection.State == svcmgr.StateInstalled)},
		{"运行中", boolStateLabel(inspection.State == svcmgr.StateInstalled, deps.IsActive)},
		{"已启用", boolStateLabel(inspection.State == svcmgr.StateInstalled, deps.IsEnabled)},
		{"二进制路径", layout.BinaryPath},
		{"数据目录", layout.DataDir},
		{"数据路径", serverDataPath(layout)},
		{"锁路径", lockPath(layout.DataDir)},
		{"日志目标", "journald"},
		{"Unit 路径", layout.UnitPath},
		{"Env 路径", layout.EnvPath},
		{"运行用户", layout.RunAsUser},
		{"监听端口", intOrUnavailable(env.Port, envErr)},
		{"TLS 模式", stringOrUnavailable(env.TLSMode, envErr)},
		{"Server 地址", stringOrUnavailable(env.ServerAddr, envErr)},
	}
	if envErr != nil {
		rows = append(rows, [2]string{"Env 状态", fmt.Sprintf("不可用（%v）", envErr)})
	}
	rows = appendProblemRows(rows, inspection.Problems)
	deps.UI.PrintSummary("Server 检查", rows)
	return nil
}

func uninstallServer(deps serverDeps) (bool, error) {
	mode, err := selectWithOptions(deps.UI, "Server 卸载模式", []tui.SelectOption{
		{Label: "仅移除服务，保留数据", Description: "移除 server unit 和 env 文件，同时保留现有 server 数据。"},
		{Label: "移除服务并删除数据", Description: "移除服务文件，并永久删除 server 数据。"},
	})
	if err != nil {
		return false, err
	}
	layout := svcmgr.NewLayout(svcmgr.RoleServer)
	deleteData := mode == 1
	rows := [][2]string{{"模式", uninstallModeLabel(deleteData)}}
	rows = appendRemovalRows(rows, "移除", layout.UnitPath, layout.EnvPath)
	if deleteData {
		rows = appendRemovalRows(rows, "移除", serverDataPath(layout))
	} else {
		rows = append(rows, [2]string{"保留", serverDataPath(layout)})
	}
	rows = append(rows, sharedBinaryPlanRow(deps.DetectClient))
	deps.UI.PrintSummary("Server 卸载计划", rows)

	ok, err := deps.UI.ConfirmWithOptions("继续卸载 server？", tui.ConfirmOptions{ConfirmText: serverUninstallConfirmText(deleteData)})
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
	paths := []string{layout.UnitPath, layout.EnvPath}
	if deleteData {
		paths = append(paths, serverDataPath(layout))
	}
	if err := deps.RemovePaths(paths...); err != nil {
		return false, err
	}
	if err := deps.DaemonReload(); err != nil {
		return false, err
	}
	if err := maybeRemoveSharedBinary(deps.UI, deps.DetectClient, deps.RemoveBinary); err != nil {
		return false, err
	}
	deps.UI.PrintSummary("Server 已卸载", [][2]string{{"状态", "已移除"}, {"下一步", "选择其他操作，或选择返回"}})
	return true, nil
}

func runRecoverableServerMenu(deps serverDeps) error {
	for {
		action, err := selectWithOptions(deps.UI, "选择恢复操作", []tui.SelectOption{
			{Label: "检查可恢复的 server 状态", Description: "显示可用于恢复的已保存 server 数据和路径。"},
			{Label: "运行 netsgo install", Description: "使用现有配置和数据恢复 server。"},
			{Label: "返回", Description: "返回上一级服务选择菜单。"},
		})
		if err != nil {
			return err
		}
		switch action {
		case 0:
			if err := showServerDetails(deps); err != nil {
				return err
			}
		case 1:
			if deps.RunInstall == nil {
				return errors.New("manage dependencies are incomplete")
			}
			return deps.RunInstall()
		case 2:
			return errReturnToSelection
		default:
			return errReturnToSelection
		}
	}
}

func runBrokenServerMenu(deps serverDeps) error {
	for {
		action, err := selectWithOptions(deps.UI, "选择恢复操作", []tui.SelectOption{
			{Label: "检查异常 server 状态", Description: "选择清理前显示检测到的 server 服务问题。"},
			{Label: "清理异常 server 安装", Description: "通过输入确认短语移除残留的 server 服务文件。"},
			{Label: "返回", Description: "返回上一级服务选择菜单。"},
		})
		if err != nil {
			return err
		}
		switch action {
		case 0:
			if err := showServerDetails(deps); err != nil {
				return err
			}
		case 1:
			exitMenu, err := cleanupBrokenServer(deps)
			if err != nil {
				return err
			}
			if exitMenu {
				return errReturnToSelection
			}
		case 2:
			return errReturnToSelection
		default:
			return errReturnToSelection
		}
	}
}

func cleanupBrokenServer(deps serverDeps) (bool, error) {
	mode, err := selectWithOptions(deps.UI, "Server 清理模式", []tui.SelectOption{
		{Label: "移除异常服务文件，保留数据", Description: "移除残留 server unit/env 文件，同时保留 server 数据。"},
		{Label: "移除异常服务文件并删除数据", Description: "移除残留服务文件，并永久删除 server 数据。"},
	})
	if err != nil {
		return false, err
	}
	layout := svcmgr.NewLayout(svcmgr.RoleServer)
	deleteData := mode == 1

	rows := [][2]string{{"模式", uninstallModeLabel(deleteData)}}
	rows = appendRemovalRows(rows, "移除", layout.UnitPath, layout.EnvPath)
	if deleteData {
		rows = appendRemovalRows(rows, "移除", serverDataPath(layout))
	} else {
		rows = append(rows, [2]string{"保留", serverDataPath(layout)})
	}
	rows = append(rows, sharedBinaryPlanRow(deps.DetectClient))
	deps.UI.PrintSummary("异常 server 清理计划", rows)

	ok, err := deps.UI.ConfirmWithOptions("继续清理异常 server？", tui.ConfirmOptions{ConfirmText: serverCleanupConfirmText(deleteData)})
	if err != nil {
		return false, err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return false, nil
	}
	paths := []string{layout.UnitPath, layout.EnvPath}
	if deleteData {
		paths = append(paths, serverDataPath(layout))
	}
	if err := deps.RemovePaths(paths...); err != nil {
		return false, err
	}
	if err := deps.DaemonReload(); err != nil {
		return false, err
	}
	if err := maybeRemoveSharedBinary(deps.UI, deps.DetectClient, deps.RemoveBinary); err != nil {
		return false, err
	}
	deps.UI.PrintSummary("异常 server 清理完成", [][2]string{{"状态", "已清理"}, {"下一步", "需要时运行 netsgo install 恢复 server"}})
	return true, nil
}

func loadServerEnv(deps serverDeps) (svcmgr.ServerEnv, error) {
	if deps.ReadServerEnv == nil {
		return svcmgr.ServerEnv{}, nil
	}
	return deps.ReadServerEnv()
}

func serverDataPath(layout svcmgr.ServiceLayout) string {
	return layout.RuntimeDir
}

func uninstallModeLabel(deleteData bool) string {
	if deleteData {
		return "移除服务并删除数据"
	}
	return "仅移除服务，保留数据"
}

func serverUninstallConfirmText(deleteData bool) string {
	if deleteData {
		return "remove server data"
	}
	return "uninstall server"
}

func serverCleanupConfirmText(deleteData bool) string {
	if deleteData {
		return "remove server data"
	}
	return "cleanup server"
}

func sharedBinaryPlanRow(otherRoleState func() svcmgr.InstallState) [2]string {
	if otherRoleState != nil && otherRoleState() == svcmgr.StateNotInstalled {
		return [2]string{"可选", "可选择是否移除共享二进制 " + svcmgr.BinaryPath}
	}
	return [2]string{"保留", svcmgr.BinaryPath}
}

func stringOrUnavailable(value string, err error) string {
	if err != nil {
		return fmt.Sprintf("不可用（%v）", err)
	}
	if value == "" {
		return "（无）"
	}
	return value
}

func intOrUnavailable(value int, err error) string {
	if err != nil {
		return fmt.Sprintf("不可用（%v）", err)
	}
	if value == 0 {
		return "（无）"
	}
	return itoa(value)
}
