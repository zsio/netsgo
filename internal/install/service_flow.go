package install

import (
	"fmt"

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
		{"Status", "Installed"},
		{"Service", svcmgr.UnitName(role)},
		{"Next step", "Run netsgo manage to manage the installed service"},
	}
	ui.PrintSummary(title, rows)
}

func printBrokenSummary(ui uiProvider, title string, inspection svcmgr.InstallInspection) {
	ui.PrintSummary(title, degradedRows(inspection, "Clean up residual state first, then re-run netsgo install"))
}

func printRecoverableSummary(ui uiProvider, inspection svcmgr.InstallInspection) {
	rows := [][2]string{
		{"Status", "Recoverable"},
		{"Advice", "Continue installation to restore the managed service using existing server data"},
	}
	for _, problem := range inspection.Problems {
		rows = append(rows, [2]string{"Problem", problem})
	}
	ui.PrintSummary("Recoverable server data detected", rows)
}

func printInstallCancelled(ui uiProvider) {
	ui.PrintSummary("Installation cancelled", [][2]string{{"Next step", "Run netsgo install again to continue"}})
}

func confirmSummaryRows(role svcmgr.Role, rows ...[2]string) [][2]string {
	base := [][2]string{{"Role", string(role)}}
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
		{"Status", "Running"},
		{"Service", svcmgr.UnitName(role)},
		{"Run as", svcmgr.SystemUser},
	}
	if endpointLabel != "" && endpoint != "" {
		rows = append(rows, [2]string{endpointLabel, endpoint})
	}
	rows = append(rows,
		[2]string{"Logs", journalctlCommand(role)},
		[2]string{"Next step", "Run netsgo manage to manage the service"},
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
	rows := [][2]string{{"Status", inspection.State.String()}, {"Advice", advice}}
	for _, problem := range inspection.Problems {
		rows = append(rows, [2]string{"Problem", problem})
	}
	return rows
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
