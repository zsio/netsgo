package svcmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentBinaryPath(t *testing.T) {
	path, err := CurrentBinaryPath()
	if err != nil {
		t.Fatalf("CurrentBinaryPath() 不应报错: %v", err)
	}
	if path == "" {
		t.Fatal("CurrentBinaryPath() 不应为空")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("CurrentBinaryPath() 返回路径应存在: %v", err)
	}
}

func TestIsBinaryInstalledAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo")
	if isBinaryInstalledAt(path) {
		t.Fatal("不存在的路径不应视为已安装")
	}

	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("写入测试二进制失败: %v", err)
	}
	if !isBinaryInstalledAt(path) {
		t.Fatal("存在且可执行的文件应视为已安装")
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("修改测试权限失败: %v", err)
	}
	if isBinaryInstalledAt(path) {
		t.Fatal("不可执行文件不应视为已安装")
	}
}
