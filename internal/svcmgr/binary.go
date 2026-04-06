package svcmgr

import (
	"os"
	"path/filepath"
)

func CurrentBinaryPath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func IsBinaryInstalled() bool {
	return isBinaryInstalledAt(BinaryPath)
}

func isBinaryInstalledAt(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}
