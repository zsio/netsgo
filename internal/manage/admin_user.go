package manage

import (
	"fmt"
	"path/filepath"
	"strings"

	"netsgo/internal/server"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
	"netsgo/pkg/flock"
)

func ResetAdminUser(dataDir, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("admin username is required")
	}
	if password == "" {
		return fmt.Errorf("admin password is required")
	}

	lockPath := filepath.Join(dataDir, "locks", "server.lock")
	unlock, err := flock.TryLock(lockPath)
	if err != nil {
		return fmt.Errorf("server appears to be running or another server operation holds %s; stop the server before resetting the admin user: %w", lockPath, err)
	}
	defer unlock()

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

	return store.ResetAdminUser(username, password)
}

func resetAdminUserInteractive(deps serverDeps) error {
	if deps.ResetAdminUser == nil {
		return fmt.Errorf("manage dependencies are incomplete")
	}

	username, err := deps.UI.Input("新的管理员用户名", tui.InputOptions{
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
	if err := deps.ResetAdminUser(username, password); err != nil {
		return err
	}
	deps.UI.PrintSummary("管理员用户已重置", [][2]string{
		{"新管理员", username},
		{"会话", "现有 Web 登录会话已全部失效"},
		{"数据目录", resetAdminUserDataDir(deps)},
	})
	return nil
}

func resetAdminUserDataDir(deps serverDeps) string {
	if deps.ResetAdminUserDataDir != "" {
		return deps.ResetAdminUserDataDir
	}
	return svcmgr.ManagedDataDir
}
