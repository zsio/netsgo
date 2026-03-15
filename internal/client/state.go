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
	InstallID      string `json:"install_id"`
	Token          string `json:"token,omitempty"`          // 由 Key 兑换的连接密钥
	TLSFingerprint string `json:"tls_fingerprint,omitempty"` // P1: TOFU 证书指纹
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
			// 同时加载 Token（如果有）
			if state.Token != "" && c.Token == "" {
				c.Token = state.Token
			}
			// P1: 加载 TLS 指纹（如果有）
			if state.TLSFingerprint != "" && c.TLSFingerprint == "" {
				c.TLSFingerprint = state.TLSFingerprint
			}
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

// saveToken 将 Token 持久化到客户端状态文件
func (c *Client) saveToken(token string) error {
	path := c.StatePath
	if path == "" {
		return nil
	}

	// 读取已有状态
	state := persistedState{InstallID: c.InstallID, Token: token}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化客户端状态失败: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

// clearToken 清除本地保存的 Token
func (c *Client) clearToken() error {
	c.Token = ""
	return c.saveToken("")
}

func generateInstallID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("生成 install id 失败: %w", err)
	}
	return "client-" + hex.EncodeToString(buf[:]), nil
}

// saveTLSFingerprint 将 TLS 指纹持久化到客户端状态文件 (P1 TOFU)
func (c *Client) saveTLSFingerprint(fingerprint string) error {
	path := c.StatePath
	if path == "" {
		return nil
	}

	state := persistedState{
		InstallID:      c.InstallID,
		Token:          c.Token,
		TLSFingerprint: fingerprint,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化客户端状态失败: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}
