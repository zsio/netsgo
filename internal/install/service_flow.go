package install

import (
	"fmt"
	"strings"

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

func printInstalledSummary(ui uiProvider, title string, role svcmgr.Role) {
	rows := [][2]string{
		{"状态", "已安装"},
		{"服务", svcmgr.UnitName(role)},
		{"下一步", "运行 netsgo manage 管理已安装服务"},
	}
	ui.PrintSummary(title, rows)
}

func printBrokenSummary(ui uiProvider, title string, inspection svcmgr.InstallInspection) {
	ui.PrintSummary(title, degradedRows(inspection, "先清理残留状态，然后重新运行 netsgo install"))
}

func printRecoverableSummary(ui uiProvider, inspection svcmgr.InstallInspection) {
	rows := [][2]string{
		{"状态", "可恢复"},
		{"建议", "继续安装以使用现有 server 数据恢复托管服务"},
	}
	for _, problem := range inspection.Problems {
		rows = append(rows, [2]string{"问题", userFacingInstallProblem(problem)})
	}
	ui.PrintSummary("检测到可恢复的 server 数据", rows)
}

func printInstallCancelled(ui uiProvider) {
	ui.PrintSummary("安装已取消", [][2]string{{"下一步", "再次运行 netsgo install 继续"}})
}

func printOptionalRoleInstallCancelled(ui uiProvider, installedRole svcmgr.Role) {
	ui.PrintSummary("已取消安装，未进行任何修改", [][2]string{
		{"当前角色", string(installedRole)},
		{"状态", "保持当前安装状态"},
		{"下一步", fmt.Sprintf("运行 netsgo manage 管理已安装的 %s 服务", installedRole)},
	})
}

func confirmSummaryRows(role svcmgr.Role, rows ...[2]string) [][2]string {
	base := [][2]string{{"角色", string(role)}}
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		base = append(base, row)
	}
	return base
}

func completionSummaryRows(role svcmgr.Role, endpointLabel, endpoint string) [][2]string {
	rows := [][2]string{
		{"状态", "运行中"},
		{"服务", svcmgr.UnitName(role)},
		{"运行用户", svcmgr.SystemUser},
	}
	if endpointLabel != "" && endpoint != "" {
		rows = append(rows, [2]string{endpointLabel, endpoint})
	}
	rows = append(rows,
		[2]string{"日志", journalctlCommand(role)},
		[2]string{"下一步", "运行 netsgo manage 管理服务"},
	)
	return rows
}

func journalctlCommand(role svcmgr.Role) string {
	return fmt.Sprintf("journalctl -u %s -n 100 -f", svcmgr.UnitName(role))
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
	rows := [][2]string{{"状态", installStateLabel(inspection.State)}, {"建议", advice}}
	for _, problem := range inspection.Problems {
		rows = append(rows, [2]string{"问题", userFacingInstallProblem(problem)})
	}
	return rows
}

func installStateLabel(state svcmgr.InstallState) string {
	switch state {
	case svcmgr.StateNotInstalled:
		return "未安装"
	case svcmgr.StateInstalled:
		return "已安装"
	case svcmgr.StateHistoricalDataOnly:
		return "可恢复"
	case svcmgr.StateBroken:
		return "需要处理"
	default:
		return state.String()
	}
}

func userFacingInstallProblem(problem string) string {
	switch {
	case strings.Contains(problem, "Recoverable server historical data was detected"):
		return "检测到历史 server 数据，但 systemd 服务文件不存在。继续安装会恢复服务。"
	case strings.Contains(problem, "missing unit file"):
		return "systemd 服务文件不存在：" + suffixAfter(problem, ": ")
	case strings.Contains(problem, "missing env file"):
		return "服务环境配置文件不存在：" + suffixAfter(problem, ": ")
	case strings.Contains(problem, "missing runtime data directory"):
		return "运行数据目录不存在：" + suffixAfter(problem, ": ")
	case strings.Contains(problem, "leftover runtime data directory still exists"):
		return "检测到残留运行数据目录：" + suffixAfter(problem, ": ")
	default:
		return problem
	}
}

func suffixAfter(value, sep string) string {
	if idx := strings.Index(value, sep); idx >= 0 {
		return value[idx+len(sep):]
	}
	return value
}

func completeManagedInstall(role svcmgr.Role, deps managedInstallDeps, writeArtifacts func(layout svcmgr.ServiceLayout) error) error {
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
	layout := svcmgr.NewLayout(role)
	if err := writeArtifacts(layout); err != nil {
		return err
	}
	if err := deps.DaemonReload(); err != nil {
		return err
	}
	return deps.EnableAndStart(svcmgr.UnitName(role))
}
