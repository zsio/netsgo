package datadir

import (
	"os"
	"path/filepath"
)

func DefaultDataDir() string {
	if os.Getenv("INVOCATION_ID") != "" {
		return "/var/lib/netsgo"
	}

	home := os.Getenv("HOME")
	if home == "" {
		if detected, err := os.UserHomeDir(); err == nil {
			home = detected
		}
	}

	return filepath.Join(home, ".local", "state", "netsgo")
}
