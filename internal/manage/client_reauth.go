package manage

import (
	"errors"
	"fmt"
	"strings"

	"netsgo/internal/tui"
)

func reauthenticateClient(deps clientDeps) error {
	if deps.ReadClientEnv == nil || deps.UpdateClientKey == nil || deps.PreflightClientTokenClear == nil || deps.ClearClientToken == nil || deps.DisableAndStop == nil || deps.EnableAndStart == nil {
		return errors.New("manage dependencies are incomplete")
	}

	key, err := deps.UI.Password("新的 client key", tui.InputOptions{
		Description: "将写入托管 client env；不会显示或打印 key。",
		Validate:    validateClientReauthenticationKey,
	})
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if err := validateClientReauthenticationKey(key); err != nil {
		return err
	}

	deps.UI.PrintSummary("Client 重新认证计划", [][2]string{
		{"影响", "更新托管 client key"},
		{"影响", "清空本地保存的 client token"},
		{"保留", "server URL、TLS 配置、install_id 和 TLS 指纹"},
		{"服务", "停止后重新启动 netsgo-client.service"},
	})
	ok, err := deps.UI.ConfirmWithOptions("继续重新认证 client？", tui.ConfirmOptions{
		ConfirmText:       "reauth client",
		CancelDescription: "不修改 client",
	})
	if err != nil {
		return err
	}
	if !ok {
		printManageCancelled(deps.UI)
		return nil
	}

	currentEnv, err := deps.ReadClientEnv()
	if err != nil {
		return fmt.Errorf("read current client env: %w", err)
	}
	if err := deps.PreflightClientTokenClear(); err != nil {
		return fmt.Errorf("preflight clear client token: %w", err)
	}
	if err := deps.DisableAndStop(); err != nil {
		return fmt.Errorf("stop client service: %w", err)
	}
	if err := deps.UpdateClientKey(key); err != nil {
		return recoverClientReauthenticationFailure(deps, currentEnv.Key, fmt.Errorf("update client key: %w", err))
	}
	_, foundState, err := deps.ClearClientToken()
	if err != nil {
		return recoverClientReauthenticationFailure(deps, currentEnv.Key, fmt.Errorf("clear client token: %w", err))
	}
	if err := deps.EnableAndStart(); err != nil {
		return fmt.Errorf("start client service: %w", err)
	}

	tokenStatus := "本地状态文件未发现，已跳过"
	if foundState {
		tokenStatus = "已清空"
	}
	deps.UI.PrintSummary("Client 重新认证完成", [][2]string{
		{"状态", "重新认证成功"},
		{"本地 token", tokenStatus},
		{"保留", "server URL、TLS 配置、install_id 和 TLS 指纹"},
		{"服务", "已重启"},
	})
	return nil
}

func recoverClientReauthenticationFailure(deps clientDeps, oldKey string, cause error) error {
	var recoveryErrs []error
	if strings.TrimSpace(oldKey) != "" {
		if err := deps.UpdateClientKey(oldKey); err != nil {
			recoveryErrs = append(recoveryErrs, fmt.Errorf("restore client key: %w", err))
		}
	}
	if err := deps.EnableAndStart(); err != nil {
		recoveryErrs = append(recoveryErrs, fmt.Errorf("restart client service: %w", err))
	}
	if len(recoveryErrs) > 0 {
		return fmt.Errorf("%w; recovery failed: %w", cause, errors.Join(recoveryErrs...))
	}
	return cause
}

func validateClientReauthenticationKey(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("client key 不能为空")
	}
	return nil
}
