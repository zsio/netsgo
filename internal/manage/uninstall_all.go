package manage

import (
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

type uninstallAllDeps struct {
	UI     uiProvider
	Server serverDeps
	Client clientDeps
}

func UninstallAll() error {
	return uninstallAllWith(uninstallAllDeps{
		UI:     defaultUI{},
		Server: defaultServerDeps(),
		Client: defaultClientDeps(),
	})
}

func uninstallAllWith(deps uninstallAllDeps) error {
	serverLayout := svcmgr.NewLayout(svcmgr.RoleServer)
	clientLayout := svcmgr.NewLayout(svcmgr.RoleClient)

	serverMode, err := selectWithOptions(deps.UI, "Server 卸载模式", []tui.SelectOption{
		{Label: "仅移除服务，保留数据", Description: "移除 server unit 和 env 文件，同时保留现有 server 数据。"},
		{Label: "移除服务并删除数据", Description: "移除服务文件，并永久删除 server 数据。"},
	})
	if err != nil {
		return err
	}
	deleteServerData := serverMode == 1

	serverRows := [][2]string{{"模式", uninstallModeLabel(deleteServerData)}}
	serverRows = appendRemovalRows(serverRows, "移除", serverLayout.UnitPath, serverLayout.EnvPath)
	if deleteServerData {
		serverRows = appendRemovalRows(serverRows, "移除", serverDataPath(serverLayout))
	} else {
		serverRows = append(serverRows, [2]string{"保留", serverDataPath(serverLayout)})
	}
	serverRows = append(serverRows, [2]string{"保留", svcmgr.BinaryPath})
	deps.UI.PrintSummary("Server 卸载计划", serverRows)
	ok, err := deps.UI.ConfirmWithOptions("在批量移除中包含 server 卸载？", tui.ConfirmOptions{ConfirmText: serverUninstallConfirmText(deleteServerData)})
	if err != nil {
		return err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return errReturnToSelection
	}

	clientRows := [][2]string{
		{"影响", "移除托管 client 服务和本地连接状态"},
		{"结果", "重新安装 client 时请从 Web 控制台获取新的 client key"},
		{"结果", "不会自动清理 server 端历史记录"},
	}
	clientRows = appendRemovalRows(clientRows, "移除", clientLayout.UnitPath, clientLayout.EnvPath, clientDataPath(clientLayout))
	clientRows = append(clientRows, [2]string{"可选", "移除两个角色后，可选择是否移除共享二进制 " + svcmgr.BinaryPath})
	deps.UI.PrintSummary("Client 卸载计划", clientRows)
	ok, err = deps.UI.ConfirmWithOptions("在批量移除中包含 client 卸载？", tui.ConfirmOptions{ConfirmText: "uninstall client"})
	if err != nil {
		return err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return errReturnToSelection
	}

	if err := deps.Server.DisableAndStop(); err != nil {
		return err
	}
	serverPaths := []string{serverLayout.UnitPath, serverLayout.EnvPath}
	if deleteServerData {
		serverPaths = append(serverPaths, serverDataPath(serverLayout))
	}
	if err := deps.Server.RemovePaths(serverPaths...); err != nil {
		return err
	}

	if err := deps.Client.DisableAndStop(); err != nil {
		return err
	}
	if err := deps.Client.RemovePaths(clientLayout.UnitPath, clientLayout.EnvPath, clientDataPath(clientLayout)); err != nil {
		return err
	}

	if err := deps.Server.DaemonReload(); err != nil {
		return err
	}
	ok, err = deps.UI.ConfirmWithOptions("未检测到其他托管角色。是否同时移除共享二进制 "+svcmgr.BinaryPath+"？", tui.ConfirmOptions{ConfirmText: "remove binary", CancelDescription: "保留共享二进制"})
	if err != nil {
		return err
	}
	if ok {
		if err := deps.Server.RemoveBinary(); err != nil {
			return err
		}
	}

	deps.UI.PrintSummary("托管服务已卸载", [][2]string{
		{"server 角色", "已移除"},
		{"client 角色", "已移除"},
		{"下一步", "需要时运行 netsgo install 重新安装托管角色"},
	})
	return errReturnToSelection
}
