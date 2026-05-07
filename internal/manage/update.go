package manage

import (
	"fmt"
	"strings"

	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
	"netsgo/pkg/updater"
	"netsgo/pkg/version"
)

type updateChecker func(updater.DownloadChannel, string) (*updater.Result, bool, error)
type confirmedUpdateApplier func(updater.DownloadChannel, string, string) (*updater.Result, error)

const updateManualFallbackMessage = "网络连接失败，请您访问 https://github.com/zsio/netsgo/releases 下载最新版本的二进制文件，然后使用 ./netsgo upgrade 进行更新替换。"

func runUpdate(ui uiProvider, currentVersion string, hasInstalled func() bool) error {
	return runUpdateWithChecker(ui, currentVersion, hasInstalled, updater.CheckForUpdate, updater.ApplyConfirmedUpdate)
}

func runUpdateWithChecker(ui uiProvider, currentVersion string, hasInstalled func() bool, checkForUpdate updateChecker, applyConfirmedUpdate confirmedUpdateApplier) error {
	if hasInstalled == nil {
		hasInstalled = func() bool {
			return svcmgr.Detect(svcmgr.RoleServer) == svcmgr.StateInstalled ||
				svcmgr.Detect(svcmgr.RoleClient) == svcmgr.StateInstalled
		}
	}

	if !hasInstalled() {
		return fmt.Errorf("未发现已安装的托管服务")
	}

	channelIdx, err := selectWithOptions(ui, "选择下载通道", []tui.SelectOption{
		{Label: "GitHub (默认)", Description: "直接从 GitHub 下载官方 release。"},
		{Label: "ghproxy (镜像)", Description: "直连 GitHub 不稳定时使用 ghproxy 镜像。"},
	})
	if err != nil {
		return err
	}

	channel := updater.ChannelGitHub
	if channelIdx == 1 {
		channel = updater.ChannelGhproxy
	}

	ui.PrintSummary("更新", [][2]string{
		{"当前版本", currentVersion},
		{"通道", string(channel)},
		{"状态", "检查中..."},
	})

	if checkForUpdate == nil {
		checkForUpdate = updater.CheckForUpdate
	}
	if applyConfirmedUpdate == nil {
		applyConfirmedUpdate = updater.ApplyConfirmedUpdate
	}

	result, needsUpdate, err := checkForUpdate(channel, currentVersion)
	if err != nil {
		ui.PrintSummary("更新失败", updateFailureRows(err))
		return nil
	}

	if !needsUpdate {
		currentNormalized, normalizeErr := version.NormalizeVersionString(currentVersion)
		if normalizeErr != nil {
			currentNormalized = currentVersion
		}
		ui.PrintSummary("无需更新", [][2]string{
			{"当前版本", currentNormalized},
			{"状态", "已是最新"},
		})
		return nil
	}

	confirmRows := [][2]string{
		{"当前版本", result.OldVersion},
		{"最新版本", result.NewVersion},
		{"通道", string(channel)},
		{"结果", "托管服务将切换到最新 release"},
	}
	ui.PrintSummary("发现可用更新", confirmRows)

	confirmed, err := ui.ConfirmWithOptions("应用此更新？", tui.ConfirmOptions{ConfirmText: "apply update"})
	if err != nil {
		return err
	}
	if !confirmed {
		ui.PrintSummary("更新已取消", [][2]string{{"状态", "未进行任何修改"}})
		return nil
	}

	result, err = applyConfirmedUpdate(channel, currentVersion, result.NewVersion)
	if err != nil {
		ui.PrintSummary("更新失败", updateFailureRows(err))
		return nil
	}

	rows := [][2]string{
		{"从", result.OldVersion},
		{"到", result.NewVersion},
	}
	if len(result.Stopped) > 0 {
		rows = append(rows, [2]string{"已停止", formatServiceList(result.Stopped)})
	}
	if len(result.Started) > 0 {
		rows = append(rows, [2]string{"已启动", formatServiceList(result.Started)})
	}
	ui.PrintSummary("更新完成", rows)
	return nil
}

func updateFailureRows(err error) [][2]string {
	return [][2]string{
		{"错误", err.Error()},
		{"提示", updateManualFallbackMessage},
	}
}

func formatServiceList(services []string) string {
	if len(services) == 0 {
		return "无"
	}
	return strings.Join(services, ", ")
}
