package install

import (
	"time"

	"netsgo/internal/svcmgr"
)

type managedInstallDeps struct {
	EnsureUser        func(string) error
	EnsureDirs        func() error
	CurrentBinaryPath func() (string, error)
	InstallBinary     func(string) error
	DaemonReload      func() error
	EnableAndStart    func(string) error
}

func printInstalledSummary(ui uiProvider, title string) {
	ui.PrintSummary(title, [][2]string{{"状态", "已安装"}, {"下一步", "运行 netsgo manage 管理已安装的服务"}})
}

func printBrokenSummary(ui uiProvider, title string, inspection svcmgr.InstallInspection) {
	ui.PrintSummary(title, degradedRows(inspection, "请先清理残留状态，再重新运行 netsgo install"))
}

func printInstallCancelled(ui uiProvider) {
	ui.PrintSummary("安装已取消", [][2]string{{"下一步", "重新运行 netsgo install 以继续安装"}})
}

func resolveInspection(inspect func(svcmgr.Role) svcmgr.InstallInspection, detect func(svcmgr.Role) svcmgr.InstallState, role svcmgr.Role) svcmgr.InstallInspection {
	if inspect != nil {
		return inspect(role)
	}
	if detect != nil {
		return svcmgr.InstallInspection{Role: role, State: detect(role)}
	}
	return svcmgr.InstallInspection{Role: role, State: svcmgr.StateNotInstalled}
}

func degradedRows(inspection svcmgr.InstallInspection, advice string) [][2]string {
	rows := [][2]string{{"状态", inspection.State.String()}, {"建议", advice}}
	for _, problem := range inspection.Problems {
		rows = append(rows, [2]string{"问题", problem})
	}
	return rows
}

func completeManagedInstall(role svcmgr.Role, deps managedInstallDeps, writeArtifacts func(spec svcmgr.ServiceSpec) error) error {
	if err := deps.EnsureUser(svcmgr.SystemUser); err != nil {
		return err
	}
	if err := deps.EnsureDirs(); err != nil {
		return err
	}
	binaryPath, err := deps.CurrentBinaryPath()
	if err != nil {
		return err
	}
	if err := deps.InstallBinary(binaryPath); err != nil {
		return err
	}
	spec := svcmgr.NewSpec(role)
	spec.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeArtifacts(spec); err != nil {
		return err
	}
	if err := deps.DaemonReload(); err != nil {
		return err
	}
	return deps.EnableAndStart(svcmgr.UnitName(role))
}
