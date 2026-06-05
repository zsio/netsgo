package manage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"golang.org/x/term"
	"netsgo/internal/install"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

type uiProvider interface {
	Select(prompt string, options []string) (int, error)
	Input(prompt string, opts ...tui.InputOptions) (string, error)
	Password(prompt string, opts ...tui.InputOptions) (string, error)
	Confirm(prompt string) (bool, error)
	ConfirmWithOptions(prompt string, opts tui.ConfirmOptions) (bool, error)
	PrintSummary(title string, rows [][2]string)
}

type defaultUI struct{}

func (defaultUI) Select(prompt string, options []string) (int, error) {
	return tui.Select(prompt, options)
}
func (defaultUI) SelectWithOptions(prompt string, options []tui.SelectOption) (int, error) {
	return tui.SelectWithOptions(prompt, options)
}
func (defaultUI) Input(prompt string, opts ...tui.InputOptions) (string, error) {
	return tui.Input(prompt, opts...)
}
func (defaultUI) Password(prompt string, opts ...tui.InputOptions) (string, error) {
	return tui.Password(prompt, opts...)
}
func (defaultUI) Confirm(prompt string) (bool, error) { return tui.Confirm(prompt) }
func (defaultUI) ConfirmWithOptions(prompt string, opts tui.ConfirmOptions) (bool, error) {
	return tui.ConfirmWithOptions(prompt, opts)
}
func (defaultUI) PrintSummary(title string, rows [][2]string) { tui.PrintSummary(title, rows) }

type Deps struct {
	GOOS         string
	HasTTY       bool
	UID          int
	UI           uiProvider
	Inspect      func(svcmgr.Role) svcmgr.InstallInspection
	Detect       func(svcmgr.Role) svcmgr.InstallState
	ManageServer func() error
	ManageClient func() error
	RunInstall   func() error
	UninstallAll func() error
	LookPath     func(file string) (string, error)
	Exec         func(argv0 string, argv []string, envv []string) error
}

func Run() error {
	return RunWith(Deps{
		GOOS:    runtime.GOOS,
		HasTTY:  term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		UID:     os.Getuid(),
		UI:      defaultUI{},
		Inspect: svcmgr.Inspect,
		Detect:  svcmgr.Detect,
		ManageServer: func() error {
			return ManageServer()
		},
		ManageClient: func() error {
			return ManageClient()
		},
		RunInstall: func() error {
			return install.Run()
		},
		UninstallAll: func() error {
			return UninstallAll()
		},
		LookPath: exec.LookPath,
		Exec:     execAsRoot,
	})
}

func RunWith(deps Deps) error {
	if deps.GOOS != "linux" {
		return errors.New("manage is only supported on Linux")
	}
	if !deps.HasTTY {
		return errors.New("manage must be run in an interactive TTY")
	}
	if deps.UID != 0 {
		if deps.LookPath == nil {
			deps.LookPath = exec.LookPath
		}
		sudoPath, err := deps.LookPath("sudo")
		if err != nil {
			return fmt.Errorf("sudo is required to rerun this command as root, but it was not found in PATH: %w", err)
		}
		return deps.Exec(sudoPath, append([]string{"sudo"}, os.Args...), os.Environ())
	}
	if deps.UI == nil {
		deps.UI = defaultUI{}
	}
	if deps.Inspect == nil {
		if deps.Detect != nil {
			detect := deps.Detect
			deps.Inspect = func(role svcmgr.Role) svcmgr.InstallInspection {
				return svcmgr.InstallInspection{Role: role, State: detect(role)}
			}
		} else {
			deps.Inspect = svcmgr.Inspect
		}
	}
	for {
		serverInspection := deps.Inspect(svcmgr.RoleServer)
		clientInspection := deps.Inspect(svcmgr.RoleClient)
		serverState := serverInspection.State
		clientState := clientInspection.State

		switch {
		case serverState == svcmgr.StateInstalled && clientState == svcmgr.StateInstalled:
			if deps.ManageServer == nil || deps.ManageClient == nil {
				return errors.New("manage dependencies are incomplete")
			}
			shouldContinue, err := runDualInstalledMenu(deps)
			if err != nil {
				return err
			}
			if shouldContinue {
				continue
			}
			return nil

		case serverState == svcmgr.StateInstalled:
			if deps.ManageServer == nil {
				return errors.New("manage dependencies are incomplete")
			}
			printDegradedSummary(deps.UI, clientInspection)
			err := deps.ManageServer()
			if errors.Is(err, errReturnToSelection) {
				return nil
			}
			return err

		case clientState == svcmgr.StateInstalled:
			if deps.ManageClient == nil {
				return errors.New("manage dependencies are incomplete")
			}
			printDegradedSummary(deps.UI, serverInspection)
			err := deps.ManageClient()
			if errors.Is(err, errReturnToSelection) {
				return nil
			}
			return err

		case serverState == svcmgr.StateNotInstalled && clientState == svcmgr.StateNotInstalled:
			shouldContinue, err := runNoInstalledMenu(deps)
			if err != nil {
				return err
			}
			if shouldContinue {
				continue
			}
			return nil

		default:
			if deps.ManageServer == nil || deps.ManageClient == nil {
				return errors.New("manage dependencies are incomplete")
			}
			shouldContinue, err := runRecoveryEntryMenu(deps, serverInspection, clientInspection)
			if err != nil {
				return err
			}
			if shouldContinue {
				continue
			}
			return nil
		}
	}
}

func runDualInstalledMenu(deps Deps) (bool, error) {
	role, err := selectWithOptions(deps.UI, "选择要管理的角色", []tui.SelectOption{
		{Label: "管理 server", Description: "检查、重启或卸载 server 服务。"},
		{Label: "管理 client", Description: "检查、重启或卸载 client 服务。"},
		{Label: "卸载全部托管服务", Description: "通过一个引导流程移除两个托管角色。"},
		{Label: "退出", Description: "离开服务管理，不做任何修改。"},
	})
	if err != nil {
		return false, err
	}

	switch role {
	case 0:
		if deps.ManageServer == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		err = deps.ManageServer()
	case 1:
		if deps.ManageClient == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		err = deps.ManageClient()
	case 2:
		if deps.UninstallAll == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		err = deps.UninstallAll()
	case 3:
		return false, nil
	default:
		return false, nil
	}
	if errors.Is(err, errReturnToSelection) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func runNoInstalledMenu(deps Deps) (bool, error) {
	deps.UI.PrintSummary("未安装托管服务", [][2]string{{"下一步", "选择启动 netsgo install 或退出"}})

	action, err := selectWithOptions(deps.UI, "选择操作", []tui.SelectOption{
		{Label: "运行 netsgo install", Description: "启动 server 或 client 角色的引导安装。"},
		{Label: "退出", Description: "不安装托管服务并离开。"},
	})
	if err != nil {
		return false, err
	}
	if action == 0 {
		if deps.RunInstall == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		return false, deps.RunInstall()
	}
	return false, nil
}

func runRecoveryEntryMenu(deps Deps, serverInspection, clientInspection svcmgr.InstallInspection) (bool, error) {
	options := make([]string, 0, 4)
	actions := make([]func() error, 0, 4)

	appendRole := func(label string, fn func() error) {
		options = append(options, label)
		actions = append(actions, fn)
	}

	if serverInspection.State != svcmgr.StateNotInstalled {
		appendRole(recoveryRoleLabel(serverInspection), deps.ManageServer)
	}
	if clientInspection.State != svcmgr.StateNotInstalled {
		appendRole(recoveryRoleLabel(clientInspection), deps.ManageClient)
	}
	options = append(options, "运行 netsgo install", "退出")

	choice, err := selectWithOptions(deps.UI, "选择恢复操作", recoveryEntryOptions(options))
	if err != nil {
		return false, err
	}
	if choice < len(actions) {
		err = actions[choice]()
		if errors.Is(err, errReturnToSelection) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	}
	if choice == len(actions) {
		if deps.RunInstall == nil {
			return false, errors.New("manage dependencies are incomplete")
		}
		return false, deps.RunInstall()
	}
	return false, nil
}

type selectOptionsUI interface {
	SelectWithOptions(prompt string, options []tui.SelectOption) (int, error)
}

func selectWithOptions(ui uiProvider, prompt string, options []tui.SelectOption) (int, error) {
	if described, ok := ui.(selectOptionsUI); ok {
		return described.SelectWithOptions(prompt, options)
	}
	labels := make([]string, len(options))
	for i, option := range options {
		labels[i] = option.Label
	}
	return ui.Select(prompt, labels)
}

func recoveryEntryOptions(labels []string) []tui.SelectOption {
	options := make([]tui.SelectOption, len(labels))
	for i, label := range labels {
		options[i] = tui.SelectOption{Label: label, Description: recoveryEntryDescription(label)}
	}
	return options
}

func recoveryEntryDescription(label string) string {
	switch {
	case strings.HasPrefix(label, "检查可恢复"):
		return "查看可通过安装器恢复的已保存数据。"
	case strings.HasPrefix(label, "检查/清理异常"):
		return "检查残留服务文件，并可选择移除异常状态。"
	case label == "运行 netsgo install":
		return "启动引导安装器以恢复或新增托管服务。"
	case label == "退出":
		return "离开服务管理，不做任何修改。"
	default:
		return "打开对应角色的服务管理操作。"
	}
}

func recoveryRoleLabel(inspection svcmgr.InstallInspection) string {
	role := roleLabel(inspection.Role)
	switch inspection.State {
	case svcmgr.StateHistoricalDataOnly:
		return "检查可恢复的 " + role + " 状态"
	case svcmgr.StateBroken:
		return "检查/清理异常的 " + role + " 状态"
	default:
		return "管理 " + role
	}
}

func printDegradedSummary(ui uiProvider, inspection svcmgr.InstallInspection) {
	if inspection.State == svcmgr.StateInstalled || inspection.State == svcmgr.StateNotInstalled {
		return
	}

	rows := [][2]string{{"角色", roleLabel(inspection.Role)}, {"状态", lifecycleStateLabel(inspection.State)}, {"建议", degradedAdvice(inspection.Role, inspection.State)}}
	for _, problem := range inspection.Problems {
		rows = append(rows, [2]string{"问题", lifecycleProblem(problem)})
	}
	ui.PrintSummary(degradedTitle(inspection.Role, inspection.State), rows)
}

func degradedTitle(role svcmgr.Role, state svcmgr.InstallState) string {
	if role == svcmgr.RoleServer && state == svcmgr.StateHistoricalDataOnly {
		return "检测到可恢复的 server 数据"
	}
	return roleLabel(role) + " 安装状态异常"
}

func degradedAdvice(role svcmgr.Role, state svcmgr.InstallState) string {
	if role == svcmgr.RoleServer && state == svcmgr.StateHistoricalDataOnly {
		return "运行 netsgo install 恢复安装（将保留现有配置）"
	}
	return "运行 netsgo install 修复安装，或在重新安装前手动清理残留文件"
}

func roleLabel(role svcmgr.Role) string {
	if role == svcmgr.RoleServer {
		return "server"
	}
	return "client"
}
