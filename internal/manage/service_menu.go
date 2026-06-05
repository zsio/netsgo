package manage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

var (
	errReturnToSelection = errors.New("manage: return to selection")
)

type serviceMenuDeps struct {
	UI        uiProvider
	Role      svcmgr.Role
	Status    func() error
	Detail    func() error
	Logs      func() error
	Start     func() error
	Stop      func() error
	Uninstall func() (bool, error)
	Extra     []serviceMenuAction
}

type serviceMenuAction struct {
	Option tui.SelectOption
	Run    func() error
}

func runServiceMenu(deps serviceMenuDeps) error {
	for {
		options := serviceActionOptions(deps.Extra)
		action, err := selectWithOptions(deps.UI, serviceActionPrompt(deps.Role), options)
		if err != nil {
			return err
		}

		switch action {
		case 0:
			if err := deps.Status(); err != nil {
				return err
			}
		case 1:
			if err := deps.Detail(); err != nil {
				return err
			}
		case 2:
			return deps.Logs()
		case 3:
			if err := runLifecycleAction(deps.UI, "启动", "已启动", deps.Start); err != nil {
				return err
			}
		case 4:
			if err := runLifecycleAction(deps.UI, "停止", "已停止", deps.Stop); err != nil {
				return err
			}
		case 5:
			if err := runRestartAction(deps.UI, deps.Stop, deps.Start); err != nil {
				return err
			}
		case 6:
			exitMenu, err := deps.Uninstall()
			if err != nil {
				return err
			}
			if exitMenu {
				return errReturnToSelection
			}
		case len(manageActionOptions()) - 1 + len(deps.Extra):
			return errReturnToSelection
		default:
			extraIndex := action - (len(manageActionOptions()) - 1)
			if extraIndex < 0 || extraIndex >= len(deps.Extra) {
				return errReturnToSelection
			}
			if err := deps.Extra[extraIndex].Run(); err != nil {
				return err
			}
		}
	}
}

func serviceActionOptions(extra []serviceMenuAction) []tui.SelectOption {
	base := manageActionOptions()
	if len(extra) == 0 {
		return base
	}
	options := make([]tui.SelectOption, 0, len(base)+len(extra))
	options = append(options, base[:len(base)-1]...)
	options = append(options, extraActionOptions(extra)...)
	options = append(options, base[len(base)-1])
	return options
}

func extraActionOptions(actions []serviceMenuAction) []tui.SelectOption {
	options := make([]tui.SelectOption, len(actions))
	for i, action := range actions {
		options[i] = action.Option
	}
	return options
}

func serviceActionPrompt(role svcmgr.Role) string {
	switch role {
	case svcmgr.RoleServer:
		return "选择 server 操作"
	case svcmgr.RoleClient:
		return "选择 client 操作"
	default:
		return "选择操作"
	}
}

func manageActionOptions() []tui.SelectOption {
	return []tui.SelectOption{
		{Label: "状态", Description: "显示服务是否已安装、正在运行并已启用。"},
		{Label: "检查", Description: "显示服务文件、数据路径、运行设置和检测到的问题。"},
		{Label: "日志", Description: "打开此服务最近的 journald 日志。"},
		{Label: "启动", Description: "启用并启动 systemd 服务。"},
		{Label: "停止", Description: "停止并禁用 systemd 服务。"},
		{Label: "重启", Description: "停止后重新启动服务，以重新加载当前配置。"},
		{Label: "卸载", Description: "通过输入确认短语移除此托管服务。"},
		{Label: "返回", Description: "返回上一级角色或恢复菜单。"},
	}
}

func showStatusSummary(ui uiProvider, role svcmgr.Role, inspection svcmgr.InstallInspection, isActive func() (bool, error), isEnabled func() (bool, error)) error {
	rows := [][2]string{
		{"服务", svcmgr.UnitName(role)},
		{"角色", string(role)},
		{"状态", lifecycleStateLabel(inspection.State)},
		{"已安装", boolLabel(inspection.State == svcmgr.StateInstalled)},
		{"运行中", boolStateLabel(inspection.State == svcmgr.StateInstalled, isActive)},
		{"已启用", boolStateLabel(inspection.State == svcmgr.StateInstalled, isEnabled)},
	}
	rows = appendProblemRows(rows, inspection.Problems)
	ui.PrintSummary("服务状态", rows)
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
	ui.PrintSummary("已取消", [][2]string{
		{"状态", "未进行任何修改"},
		{"下一步", "选择其他操作，或选择返回"},
	})
}

func maybeRemoveSharedBinary(ui uiProvider, otherRoleState func() svcmgr.InstallState, removeBinary func() error) error {
	if otherRoleState == nil || removeBinary == nil {
		return nil
	}
	if otherRoleState() != svcmgr.StateNotInstalled {
		return nil
	}
	ok, err := ui.ConfirmWithOptions(
		fmt.Sprintf("未检测到其他托管角色。是否同时移除共享二进制 %s？", svcmgr.BinaryPath),
		tui.ConfirmOptions{ConfirmText: "remove binary", CancelDescription: "保留共享二进制"},
	)
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

func appendProblemRows(rows [][2]string, problems []string) [][2]string {
	for _, problem := range problems {
		rows = append(rows, [2]string{"问题", lifecycleProblem(problem)})
	}
	return rows
}

func boolStateLabel(available bool, fn func() (bool, error)) string {
	if !available || fn == nil {
		return "否"
	}
	value, err := fn()
	if err != nil {
		return fmt.Sprintf("未知（%v）", err)
	}
	return boolLabel(value)
}

func appendRemovalRows(rows [][2]string, label string, paths ...string) [][2]string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		rows = append(rows, [2]string{label, path})
	}
	return rows
}

func lifecycleStateLabel(state svcmgr.InstallState) string {
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

func lifecycleProblem(problem string) string {
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

func lockPath(dataDir string) string {
	return filepath.Join(dataDir, "locks")
}
