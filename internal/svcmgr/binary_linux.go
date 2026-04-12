//go:build linux

package svcmgr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func InstallBinary(srcPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	if err := os.MkdirAll(filepath.Dir(BinaryPath), 0o755); err != nil {
		return err
	}

	tmpPath := BinaryPath + ".tmp"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, BinaryPath); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	return nil
}

func RemoveBinary() error {
	err := os.Remove(BinaryPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
