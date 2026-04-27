package manage

import "netsgo/internal/svcmgr"

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

	serverMode, err := deps.UI.Select("Server uninstall mode", []string{"Remove service only, keep data", "Remove service and delete data"})
	if err != nil {
		return err
	}
	deleteServerData := serverMode == 1

	serverRows := [][2]string{{"Mode", uninstallModeLabel(deleteServerData)}}
	serverRows = appendRemovalRows(serverRows, "Remove", serverLayout.UnitPath, serverLayout.EnvPath)
	if deleteServerData {
		serverRows = appendRemovalRows(serverRows, "Remove", serverDataPath(serverLayout))
	} else {
		serverRows = append(serverRows, [2]string{"Keep", serverDataPath(serverLayout)})
	}
	serverRows = append(serverRows, [2]string{"Keep", svcmgr.BinaryPath})
	deps.UI.PrintSummary("Server uninstall plan", serverRows)
	ok, err := deps.UI.Confirm("Include server uninstall in the bulk removal?")
	if err != nil {
		return err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return errReturnToSelection
	}

	clientRows := [][2]string{
		{"Impact", "Remove the managed client service and local client identity/state"},
		{"Effect", "Reinstalling the client creates a new local identity"},
		{"Effect", "Server-side history is not cleaned automatically"},
	}
	clientRows = appendRemovalRows(clientRows, "Remove", clientLayout.UnitPath, clientLayout.EnvPath, clientDataPath(clientLayout))
	clientRows = append(clientRows, [2]string{"Optional", "After removing both roles, you can choose whether to remove the shared binary " + svcmgr.BinaryPath})
	deps.UI.PrintSummary("Client uninstall plan", clientRows)
	ok, err = deps.UI.Confirm("Include client uninstall in the bulk removal?")
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
	ok, err = deps.UI.Confirm("No other managed roles detected. Remove shared binary " + svcmgr.BinaryPath + " as well?")
	if err != nil {
		return err
	}
	if ok {
		if err := deps.Server.RemoveBinary(); err != nil {
			return err
		}
	}

	deps.UI.PrintSummary("Managed services uninstalled", [][2]string{
		{"Server", "Removed"},
		{"Client", "Removed"},
		{"Next step", "Run netsgo install to install a managed role again if needed"},
	})
	return errReturnToSelection
}
