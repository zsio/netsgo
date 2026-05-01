package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
	"netsgo/pkg/updater"
	buildversion "netsgo/pkg/version"

	"github.com/spf13/cobra"
)

var forceUpgrade bool
var getInstalledVersionFunc = getInstalledVersion

type upgradeCommandDeps struct {
	installedUnits    func() []string
	currentBinaryPath func() (string, error)
	installedVersion  func() (string, error)
	confirm           func(prompt, confirmText string) (bool, error)
	applyUpgrade      func(currentPath, installedVersion, targetVersion string) (*updater.Result, error)
	currentVersion    string
	stdout            io.Writer
	stderr            io.Writer
}

func defaultUpgradeCommandDeps() upgradeCommandDeps {
	return upgradeCommandDeps{
		installedUnits:    installedUnits,
		currentBinaryPath: svcmgr.CurrentBinaryPath,
		installedVersion:  getInstalledVersionFunc,
		confirm:           readUpgradeConfirmation,
		applyUpgrade:      updater.Upgrade,
		currentVersion:    buildversion.Current,
		stdout:            os.Stdout,
		stderr:            os.Stderr,
	}
}

func runUpgradeCommand(force bool, deps upgradeCommandDeps) error {
	units := deps.installedUnits()
	if len(units) == 0 {
		_, _ = fmt.Fprintln(deps.stderr, "未发现已安装的托管服务。")
		_, _ = fmt.Fprintln(deps.stderr, "请先运行 'netsgo install'。")
		_, _ = fmt.Fprintln(deps.stderr, "手动下载 release：https://github.com/zsio/netsgo/releases")
		os.Exit(1)
	}

	currentPath, err := deps.currentBinaryPath()
	if err != nil {
		return fmt.Errorf("get current binary path: %w", err)
	}

	if currentPath == svcmgr.BinaryPath {
		_, _ = fmt.Fprintln(deps.stdout, "当前二进制已经是已安装的二进制。")
		_, _ = fmt.Fprintln(deps.stdout, "无需替换。")
		return nil
	}

	installedVersion, installedVersionErr := deps.installedVersion()
	currentVersion := deps.currentVersion
	normalizedCurrentVersion, currentErr := buildversion.NormalizeVersionString(currentVersion)

	if installedVersionErr == nil && currentErr == nil && installedVersion == normalizedCurrentVersion {
		_, _ = fmt.Fprintf(deps.stdout, "当前版本 %s 与已安装版本相同。\n", normalizedCurrentVersion)
		_, _ = fmt.Fprintln(deps.stdout, "无需替换。")
		return nil
	}

	risks := upgradeRiskRows(currentVersion, currentErr, installedVersionErr)
	if installedVersionErr == nil {
		cmp, err1 := buildversion.ParseSemver(normalizedCurrentVersion)
		inst, err2 := buildversion.ParseSemver(installedVersion)
		if err1 == nil && err2 == nil && cmp.Compare(inst) < 0 {
			risks = append(risks, fmt.Sprintf("目标版本 %s 低于已安装版本 %s", normalizedCurrentVersion, installedVersion))
		}
	}

	targetVersion := currentVersion
	if currentErr == nil {
		targetVersion = normalizedCurrentVersion
	}
	fromVersion := installedVersion
	if installedVersionErr != nil {
		fromVersion = "未知"
	}
	printUpgradePlan(deps.stdout, upgradePlan{
		SourceBinary:  currentPath,
		TargetBinary:  svcmgr.BinaryPath,
		FromVersion:   fromVersion,
		ToVersion:     targetVersion,
		RestartUnits:  units,
		RiskSummaries: risks,
	})

	if !force {
		confirmed, err := deps.confirm("用本次运行的 netsgo 文件替换已安装版本？", "upgrade binary")
		if err != nil {
			if tui.IsCancelled(err) {
				_, _ = fmt.Fprintln(deps.stdout, "替换已取消，未进行任何修改。")
				return nil
			}
			return err
		}
		if !confirmed {
			_, _ = fmt.Fprintln(deps.stdout, "替换已取消，未进行任何修改。")
			return nil
		}
	} else {
		_, _ = fmt.Fprintln(deps.stdout, "已通过 --force 跳过输入确认。")
	}

	result, err := deps.applyUpgrade(currentPath, installedVersion, targetVersion)
	if err != nil {
		return fmt.Errorf("upgrade failed: %w", err)
	}

	_, _ = fmt.Fprintln(deps.stdout, "替换完成。")
	_, _ = fmt.Fprintf(deps.stdout, "已停止: %s\n", formatRestartUnits(result.Stopped))
	_, _ = fmt.Fprintf(deps.stdout, "已启动: %s\n", formatRestartUnits(result.Started))
	return nil
}

type upgradePlan struct {
	SourceBinary  string
	TargetBinary  string
	FromVersion   string
	ToVersion     string
	RestartUnits  []string
	RiskSummaries []string
}

func printUpgradePlan(w io.Writer, plan upgradePlan) {
	_, _ = fmt.Fprintln(w, "替换计划")
	_, _ = fmt.Fprintf(w, "  源二进制:       %s\n", plan.SourceBinary)
	_, _ = fmt.Fprintf(w, "  目标二进制:     %s\n", plan.TargetBinary)
	_, _ = fmt.Fprintf(w, "  版本变化:       %s -> %s\n", plan.FromVersion, plan.ToVersion)
	_, _ = fmt.Fprintf(w, "  将重启服务:     %s\n", formatRestartUnits(plan.RestartUnits))
	for _, risk := range plan.RiskSummaries {
		_, _ = fmt.Fprintf(w, "  风险:           %s\n", risk)
	}
	_, _ = fmt.Fprintln(w)
}

func formatRestartUnits(units []string) string {
	if len(units) == 0 {
		return "无"
	}
	return strings.Join(units, ", ")
}

func upgradeRiskRows(currentVersion string, currentErr error, installedVersionErr error) []string {
	risks := []string{}
	if currentErr != nil {
		risks = append(risks, fmt.Sprintf("目标二进制是开发构建（%s）", currentVersion))
	}
	if installedVersionErr != nil {
		risks = append(risks, "无法确定已安装版本；无法完成版本安全检查")
	}
	return risks
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "用本次运行的 netsgo 文件替换已安装版本",
	Long: `upgrade 会用本次运行的 netsgo 文件替换系统已安装的托管服务二进制，
然后重启托管服务。

需要 root 权限（会在需要时通过 sudo 重新执行）。
仅适用于通过 'netsgo install' 安装的托管服务。
如果当前二进制已经是已安装版本，将不做修改。

	使用 --force 可跳过输入确认。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := rerunUpgradeWithSudoIfNeeded(os.Getuid(), exec.LookPath, syscall.Exec); err != nil {
			return err
		}

		return runUpgradeCommand(forceUpgrade, defaultUpgradeCommandDeps())
	},
}

func rerunUpgradeWithSudoIfNeeded(uid int, lookPath func(file string) (string, error), execFn func(argv0 string, argv []string, envv []string) error) error {
	if uid == 0 {
		return nil
	}

	sudoPath, err := lookPath("sudo")
	if err != nil {
		return fmt.Errorf("sudo is required to rerun this command as root, but it was not found in PATH: %w", err)
	}

	return execFn(sudoPath, append([]string{"sudo"}, os.Args...), os.Environ())
}

func init() {
	upgradeCmd.Flags().BoolVar(&forceUpgrade, "force", false, "跳过输入确认")
	rootCmd.AddCommand(upgradeCmd)
}

func readUpgradeConfirmation(prompt, confirmText string) (bool, error) {
	return tui.ConfirmWithOptions(prompt, tui.ConfirmOptions{ConfirmText: confirmText})
}

func readConfirmationFrom(reader *bufio.Reader, confirmText string) bool {
	input, err := reader.ReadString('\n')
	if err != nil && len(input) == 0 {
		return false
	}
	input = strings.TrimSpace(input)
	return strings.EqualFold(input, confirmText)
}

func installedUnits() []string {
	var units []string
	if svcmgr.Detect(svcmgr.RoleServer) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleServer))
	}
	if svcmgr.Detect(svcmgr.RoleClient) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleClient))
	}
	return units
}

func getInstalledVersion() (string, error) {
	if _, err := os.Stat(svcmgr.BinaryPath); err != nil {
		return "", fmt.Errorf("stat installed binary: %w", err)
	}
	out, err := exec.Command(svcmgr.BinaryPath, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("run installed binary --version: %w", err)
	}
	version, err := extractInstalledVersion(string(out))
	if err != nil {
		return "", fmt.Errorf("parse installed version: %w", err)
	}
	return version, nil
}

func isDevVersion(v string) bool {
	_, err := buildversion.NormalizeVersionString(v)
	return err != nil
}

func extractInstalledVersion(output string) (string, error) {
	return buildversion.NormalizeVersionString(strings.TrimSpace(output))
}
