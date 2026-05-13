package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"golang.org/x/term"
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
	GOOS          string
	HasTTY        bool
	UID           int
	HasSystemd    bool
	UI            uiProvider
	Inspect       func(svcmgr.Role) svcmgr.InstallInspection
	Detect        func(svcmgr.Role) svcmgr.InstallState
	InstallServer func() error
	InstallClient func() error
	LookPath      func(file string) (string, error)
	Exec          func(argv0 string, argv []string, envv []string) error
}

func Run() error {
	return RunWith(Deps{
		GOOS:       runtime.GOOS,
		HasTTY:     term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		UID:        os.Getuid(),
		HasSystemd: hasSystemd(),
		UI:         defaultUI{},
		Inspect:    svcmgr.Inspect,
		Detect:     svcmgr.Detect,
		InstallServer: func() error {
			return InstallServer()
		},
		InstallClient: func() error {
			return InstallClient()
		},
		LookPath: exec.LookPath,
		Exec:     execAsRoot,
	})
}

func RunWith(deps Deps) error {
	if deps.GOOS != "linux" {
		return errors.New("install is only supported on Linux")
	}
	if !deps.HasSystemd {
		return errors.New("install requires systemd")
	}
	if !deps.HasTTY {
		return errors.New("install must be run in an interactive TTY")
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
	if deps.InstallServer == nil || deps.InstallClient == nil {
		return errors.New("install dependencies are incomplete")
	}

	serverInspection := resolveInspection(deps.Inspect, deps.Detect, svcmgr.RoleServer)
	clientInspection := resolveInspection(deps.Inspect, deps.Detect, svcmgr.RoleClient)
	if handled, err := runInstallPreflight(deps, serverInspection, clientInspection); handled {
		return err
	}

	role, err := selectWithOptions(deps.UI, "选择安装角色", []tui.SelectOption{
		{Label: "安装 server", Description: "在本机安装 Web 控制台和公网隧道入口。"},
		{Label: "安装 client", Description: "连接到现有 NetsGo server，并在本机作为托管服务运行。"},
	})
	if err != nil {
		return err
	}
	if role == 0 {
		return deps.InstallServer()
	}
	if role == 1 {
		return deps.InstallClient()
	}
	printInstallCancelled(deps.UI)
	return nil
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

func runInstallPreflight(deps Deps, serverInspection, clientInspection svcmgr.InstallInspection) (bool, error) {
	serverInstalled := serverInspection.State == svcmgr.StateInstalled
	clientInstalled := clientInspection.State == svcmgr.StateInstalled
	serverAvailableForInstall := serverInspection.State == svcmgr.StateNotInstalled
	clientAvailableForInstall := clientInspection.State == svcmgr.StateNotInstalled

	switch {
	case serverInstalled && clientInstalled:
		deps.UI.PrintSummary("托管服务已安装", installStateRows(
			serverInspection,
			clientInspection,
			"运行 netsgo manage 管理已安装服务",
		))
		return true, nil
	case serverInstalled && clientAvailableForInstall:
		deps.UI.PrintSummary("本机已安装 server", optionalRoleInstallRows(
			svcmgr.RoleServer,
			svcmgr.RoleClient,
		))
		ok, err := deps.UI.ConfirmWithOptions("是否也在本机安装 client？", tui.ConfirmOptions{})
		if err != nil {
			return true, err
		}
		if !ok {
			printOptionalRoleInstallCancelled(deps.UI, svcmgr.RoleServer)
			return true, nil
		}
		return true, deps.InstallClient()
	case clientInstalled && serverAvailableForInstall:
		deps.UI.PrintSummary("本机已安装 client", optionalRoleInstallRows(
			svcmgr.RoleClient,
			svcmgr.RoleServer,
		))
		ok, err := deps.UI.ConfirmWithOptions("是否也在本机安装 server？", tui.ConfirmOptions{})
		if err != nil {
			return true, err
		}
		if !ok {
			printOptionalRoleInstallCancelled(deps.UI, svcmgr.RoleClient)
			return true, nil
		}
		return true, deps.InstallServer()
	case serverInstalled || clientInstalled:
		deps.UI.PrintSummary("托管服务状态需要处理", installStateRows(
			serverInspection,
			clientInspection,
			"运行 netsgo manage 检查或清理异常服务状态",
		))
		return true, nil
	default:
		return false, nil
	}
}

func installStateRows(serverInspection, clientInspection svcmgr.InstallInspection, nextStep string) [][2]string {
	return [][2]string{
		{"server 角色", installStateLabel(serverInspection.State)},
		{"client 角色", installStateLabel(clientInspection.State)},
		{"下一步", nextStep},
	}
}

func optionalRoleInstallRows(installedRole, optionalRole svcmgr.Role) [][2]string {
	explanation := fmt.Sprintf("本机已安装 %s。", installedRole)
	switch installedRole {
	case svcmgr.RoleServer:
		explanation += "如果这台机器还需要作为 client 连接到另一个 NetsGo server，可以继续安装 client。"
	case svcmgr.RoleClient:
		explanation += "如果这台机器还需要作为 server 提供 Web 控制台和公网隧道入口，可以继续安装 server。"
	default:
		explanation += fmt.Sprintf("如果这台机器还需要运行 %s，可以继续安装。", optionalRole)
	}

	return [][2]string{
		{"当前状态", fmt.Sprintf("%s 托管服务已安装", installedRole)},
		{"说明", explanation},
		{"下一步", fmt.Sprintf("需要时继续安装 %s；否则保持当前状态", optionalRole)},
	}
}

func hasSystemd() bool {
	if _, err := os.Stat("/run/systemd/private"); err == nil {
		return true
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}
