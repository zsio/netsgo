package manage

import (
	"fmt"
	"path/filepath"
	"strings"

	"netsgo/internal/server"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

func ResetAdminPassword(dataDir, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("admin username is required")
	}
	if password == "" {
		return fmt.Errorf("admin password is required")
	}

	initialized, err := server.IsInitialized(dataDir)
	if err != nil {
		return err
	}
	if !initialized {
		return fmt.Errorf("server data is not initialized at %s", filepath.Join(dataDir, "server", server.ServerDBFileName))
	}

	store, err := server.NewAdminStoreWithOptions(
		filepath.Join(dataDir, "server", server.ServerDBFileName),
		server.AdminStoreOptions{SuppressUninitializedWarning: true},
	)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	return store.ResetAdminPassword(username, password)
}

func resetAdminPasswordInteractive(deps serverDeps) error {
	if deps.ResetAdminPassword == nil {
		return fmt.Errorf("manage dependencies are incomplete")
	}

	username, err := deps.UI.Input("管理员用户名", tui.InputOptions{
		Placeholder: "e.g. admin",
		Validate: func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("管理员用户名不能为空")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	password, err := deps.UI.Password("新管理员密码", tui.InputOptions{
		Description: "至少 8 位，并同时包含字母和数字。",
		Validate: func(value string) error {
			if value == "" {
				return fmt.Errorf("管理员密码不能为空")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	confirmPassword, err := deps.UI.Password("确认新管理员密码", tui.InputOptions{
		Validate: func(value string) error {
			if value == "" {
				return fmt.Errorf("管理员密码确认不能为空")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	if password != confirmPassword {
		return fmt.Errorf("两次输入的管理员密码不一致")
	}

	username = strings.TrimSpace(username)
	if err := deps.ResetAdminPassword(username, password); err != nil {
		return err
	}
	deps.UI.PrintSummary("管理员密码已重置", [][2]string{
		{"用户", username},
		{"会话", "该管理员的现有 Web 登录会话已失效"},
		{"数据目录", resetAdminPasswordDataDir(deps)},
	})
	return nil
}

func resetAdminPasswordDataDir(deps serverDeps) string {
	if deps.ResetAdminPasswordDataDir != "" {
		return deps.ResetAdminPasswordDataDir
	}
	return svcmgr.ManagedDataDir
}
