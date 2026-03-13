package client

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type persistedState struct {
	InstallID string `json:"install_id"`
}

func defaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	return filepath.Join(home, ".netsgo", "client.json"), nil
}

func (c *Client) ensureInstallID() error {
	if c.InstallID != "" {
		return nil
	}

	path := c.StatePath
	if path == "" {
		var err error
		path, err = defaultStatePath()
		if err != nil {
			return err
		}
		c.StatePath = path
	}

	if data, err := os.ReadFile(path); err == nil {
		var state persistedState
		if err := json.Unmarshal(data, &state); err == nil && state.InstallID != "" {
			c.InstallID = state.InstallID
			return nil
		}
	}

	installID, err := generateInstallID()
	if err != nil {
		return err
	}
	state := persistedState{InstallID: installID}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建客户端状态目录失败: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化客户端状态失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入客户端状态失败: %w", err)
	}

	c.InstallID = installID
	return nil
}

func generateInstallID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("生成 install id 失败: %w", err)
	}
	return "agent-" + hex.EncodeToString(buf[:]), nil
}
