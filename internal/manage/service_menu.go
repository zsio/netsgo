package manage

import (
	"os"
	"strconv"

	"netsgo/internal/svcmgr"
)

var manageActionOptions = []string{"状态", "详情", "日志", "启动", "停止", "重启", "卸载"}

type serviceMenuDeps struct {
	UI        uiProvider
	Status    func() (string, error)
	Detail    func() error
	Logs      func() error
	Start     func() error
	Stop      func() error
	Uninstall func() error
}

func runServiceMenu(deps serviceMenuDeps) error {
	action, err := deps.UI.Select("选择操作", manageActionOptions)
	if err != nil {
		return err
	}

	switch action {
	case 0:
		return showStatusSummary(deps.UI, deps.Status)
	case 1:
		return deps.Detail()
	case 2:
		return deps.Logs()
	case 3:
		return runLifecycleAction(deps.UI, "启动", "已启动", deps.Start)
	case 4:
		return runLifecycleAction(deps.UI, "停止", "已停止", deps.Stop)
	case 5:
		return runRestartAction(deps.UI, deps.Stop, deps.Start)
	case 6:
		return deps.Uninstall()
	default:
		return nil
	}
}

func showStatusSummary(ui uiProvider, statusFn func() (string, error)) error {
	status, err := statusFn()
	if err != nil {
		return err
	}
	ui.PrintSummary("服务状态", [][2]string{{"状态", status}})
	return nil
}

func runLifecycleAction(ui uiProvider, action, status string, fn func() error) error {
	if err := fn(); err != nil {
		return err
	}
	ui.PrintSummary("操作成功", [][2]string{{"操作", action}, {"状态", status}})
	return nil
}

func runRestartAction(ui uiProvider, stop, start func() error) error {
	if err := stop(); err != nil {
		return err
	}
	if err := start(); err != nil {
		return err
	}
	ui.PrintSummary("操作成功", [][2]string{{"操作", "重启"}, {"状态", "已重启"}})
	return nil
}

func printManageCancelled(ui uiProvider) {
	ui.PrintSummary("已取消", [][2]string{{"下一步", "运行 netsgo manage 继续管理服务"}})
}

func maybeRemoveSharedBinary(ui uiProvider, otherRoleState func() svcmgr.InstallState, removeBinary func() error) error {
	if otherRoleState() != svcmgr.StateNotInstalled {
		return nil
	}
	ok, err := ui.Confirm("是否同时删除 /usr/local/bin/netsgo?")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return removeBinary()
}

func itoa(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func boolLabel(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

func removePaths(paths ...string) error {
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}
