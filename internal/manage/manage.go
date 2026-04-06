package manage

import (
	"errors"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/term"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

type uiProvider interface {
	Select(prompt string, options []string) (int, error)
	Confirm(prompt string) (bool, error)
	PrintSummary(title string, rows [][2]string)
}

type defaultUI struct{}

func (defaultUI) Select(prompt string, options []string) (int, error) {
	return tui.Select(prompt, options)
}
func (defaultUI) Confirm(prompt string) (bool, error)         { return tui.Confirm(prompt) }
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
		Exec: syscall.Exec,
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
		return deps.Exec("/usr/bin/sudo", append([]string{"sudo"}, os.Args...), os.Environ())
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
	serverInspection := deps.Inspect(svcmgr.RoleServer)
	clientInspection := deps.Inspect(svcmgr.RoleClient)
	serverState := serverInspection.State
	clientState := clientInspection.State
	if serverState == svcmgr.StateNotInstalled && clientState == svcmgr.StateNotInstalled {
		deps.UI.PrintSummary("尚未安装任何服务", [][2]string{{"下一步", "请先运行 netsgo install"}})
		return nil
	}
	if serverState == svcmgr.StateInstalled && clientState == svcmgr.StateInstalled {
		if deps.ManageServer == nil || deps.ManageClient == nil {
			return errors.New("manage dependencies are incomplete")
		}

		role, err := deps.UI.Select("选择管理角色", []string{"服务端 (server)", "客户端 (client)"})
		if err != nil {
			return err
		}
		if role == 0 {
			return deps.ManageServer()
		}
		return deps.ManageClient()
	}

	if serverState == svcmgr.StateInstalled {
		if deps.ManageServer == nil {
			return errors.New("manage dependencies are incomplete")
		}
		printDegradedSummary(deps.UI, clientInspection)
		return deps.ManageServer()
	}
	if clientState == svcmgr.StateInstalled {
		if deps.ManageClient == nil {
			return errors.New("manage dependencies are incomplete")
		}
		printDegradedSummary(deps.UI, serverInspection)
		return deps.ManageClient()
	}

	printDegradedSummary(deps.UI, serverInspection)
	printDegradedSummary(deps.UI, clientInspection)
	return nil
}

func printDegradedSummary(ui uiProvider, inspection svcmgr.InstallInspection) {
	if inspection.State == svcmgr.StateInstalled || inspection.State == svcmgr.StateNotInstalled {
		return
	}

	rows := [][2]string{{"角色", roleLabel(inspection.Role)}, {"状态", inspection.State.String()}, {"建议", degradedAdvice(inspection.Role, inspection.State)}}
	for _, problem := range inspection.Problems {
		rows = append(rows, [2]string{"问题", problem})
	}
	ui.PrintSummary(degradedTitle(inspection.Role, inspection.State), rows)
}

func degradedTitle(role svcmgr.Role, state svcmgr.InstallState) string {
	if role == svcmgr.RoleServer && state == svcmgr.StateHistoricalDataOnly {
		return "检测到服务端历史数据"
	}
	return roleLabel(role) + "安装状态异常"
}

func degradedAdvice(role svcmgr.Role, state svcmgr.InstallState) string {
	if role == svcmgr.RoleServer && state == svcmgr.StateHistoricalDataOnly {
		return "请运行 netsgo install 以恢复安装（会保留历史配置）"
	}
	return "请先运行 netsgo install 修复安装，或手动清理残留文件后重新安装"
}

func roleLabel(role svcmgr.Role) string {
	if role == svcmgr.RoleServer {
		return "服务端"
	}
	return "客户端"
}
