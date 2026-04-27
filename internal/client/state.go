package client

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"netsgo/pkg/datadir"
)

func (c *Client) statePath() string {
	root := c.DataDir
	if root == "" {
		root = datadir.DefaultDataDir()
	}
	return filepath.Join(root, "client", clientDBFileName)
}

func (c *Client) ensureInstallID() error {
	if c.InstallID != "" {
		return nil
	}

	store, err := newClientStateStore(c.statePath())
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	if state, ok, err := store.Load(); err != nil {
		return err
	} else if ok && state.InstallID != "" {
		c.InstallID = state.InstallID
		if state.Token != "" && c.Token == "" {
			c.Token = state.Token
		}
		if state.TLSFingerprint != "" && c.TLSFingerprint == "" {
			c.TLSFingerprint = state.TLSFingerprint
		}
		return nil
	}

	installID, err := generateInstallID()
	if err != nil {
		return err
	}
	state := persistedState{
		InstallID:      installID,
		Token:          c.Token,
		TLSFingerprint: c.TLSFingerprint,
	}
	if err := store.Save(state); err != nil {
		return err
	}

	c.InstallID = installID
	return nil
}

func (c *Client) saveState(update func(*persistedState)) error {
	store, err := newClientStateStore(c.statePath())
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	state, ok, err := store.Load()
	if err != nil {
		return err
	}
	if !ok {
		state = persistedState{}
	}
	if state.InstallID == "" {
		state.InstallID = c.InstallID
	}
	if state.Token == "" {
		state.Token = c.Token
	}
	if state.TLSFingerprint == "" {
		state.TLSFingerprint = c.TLSFingerprint
	}
	update(&state)
	return store.Save(state)
}

// saveToken persists the token to the client state database.
func (c *Client) saveToken(token string) error {
	return c.saveState(func(state *persistedState) {
		state.Token = token
	})
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

// saveTLSFingerprint persists the TLS fingerprint to the client state database. (P1 TOFU)
func (c *Client) saveTLSFingerprint(fingerprint string) error {
	return c.saveState(func(state *persistedState) {
		state.TLSFingerprint = fingerprint
	})
}
