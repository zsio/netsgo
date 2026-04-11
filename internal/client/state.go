package client

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"netsgo/pkg/datadir"
	"netsgo/pkg/fileutil"
)

type persistedState struct {
	InstallID      string `json:"install_id"`
	Token          string `json:"token,omitempty"` // Connection token exchanged from the key
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
}

func (c *Client) statePath() string {
	root := c.DataDir
	if root == "" {
		root = datadir.DefaultDataDir()
	}
	return filepath.Join(root, "client", "client.json")
}

func (c *Client) ensureInstallID() error {
	if c.InstallID != "" {
		return nil
	}

	path := c.statePath()

	if data, err := os.ReadFile(path); err == nil {
		var state persistedState
		if err := json.Unmarshal(data, &state); err == nil && state.InstallID != "" {
			c.InstallID = state.InstallID
			// Also load the token if present.
			if state.Token != "" && c.Token == "" {
				c.Token = state.Token
			}

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
		return fmt.Errorf("failed to create client state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal client state: %w", err)
	}
	if err := fileutil.AtomicWriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write client state: %w", err)
	}

	c.InstallID = installID
	return nil
}

// saveToken persists the token to the client state file.
func (c *Client) saveToken(token string) error {
	path := c.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create client state directory: %w", err)
	}

	// Read the existing state.
	state := persistedState{InstallID: c.InstallID, Token: token}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal client state: %w", err)
	}
	return fileutil.AtomicWriteFile(path, data, 0o600)
}

// clearToken clears the locally saved token.
func (c *Client) clearToken() error {
	c.Token = ""
	return c.saveToken("")
}

func generateInstallID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("failed to generate install id: %w", err)
	}
	return "client-" + hex.EncodeToString(buf[:]), nil
}

// saveTLSFingerprint persists the TLS fingerprint to the client state file. (P1 TOFU)
func (c *Client) saveTLSFingerprint(fingerprint string) error {
	path := c.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create client state directory: %w", err)
	}

	state := persistedState{
		InstallID:      c.InstallID,
		Token:          c.Token,
		TLSFingerprint: fingerprint,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal client state: %w", err)
	}
	return fileutil.AtomicWriteFile(path, data, 0o600)
}
