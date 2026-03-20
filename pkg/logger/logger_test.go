package logger

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDefaultDir_UsesHomeNetsgoLogs(t *testing.T) {
	home := t.TempDir()
	setHomeEnv(t, home)

	dir, err := DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir 失败: %v", err)
	}

	want := filepath.Join(home, ".netsgo", "logs")
	if dir != want {
		t.Fatalf("期望 %s，得到 %s", want, dir)
	}
}

func TestInit_CreatesSecureDirAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")

	if err := Init("server", dir); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	t.Cleanup(Close)

	if _, err := os.Stat(filepath.Join(dir, currentLogName("server"))); err != nil {
		t.Fatalf("日志文件未创建: %v", err)
	}

	assertPermissions(t, dir, filepath.Join(dir, currentLogName("server")))
}

func TestInit_TightensExistingDirAndFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	existing := filepath.Join(dir, currentLogName("server"))
	mustWriteFile(t, existing, []byte("old\n"), 0o644)
	mustChmod(t, dir, 0o755)

	if err := Init("server", dir); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	t.Cleanup(Close)

	assertPermissions(t, dir, existing)
}

func TestInit_ReusesExistingFileWhenBelowLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	existing := filepath.Join(dir, currentLogName("server"))
	mustWriteFile(t, existing, []byte("old\n"), 0o600)

	if err := Init("server", dir); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	t.Cleanup(Close)

	log.Print("new")

	assertFileContainsSubstrings(t, existing, "old\n", "new\n")
	assertPathNotExists(t, filepath.Join(dir, nextSeqLogName("server", 1)))
}

func TestInit_CreatesNextSeqWhenLatestFileIsFull(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	existing := filepath.Join(dir, currentLogName("server"))
	mustWriteFile(t, existing, bytes.Repeat([]byte("a"), int(maxFileSize)), 0o600)

	if err := Init("server", dir); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	t.Cleanup(Close)

	assertFileSize(t, existing, maxFileSize)
	assertExistingWritableFile(t, filepath.Join(dir, nextSeqLogName("server", 1)))
}

func currentLogName(role string) string {
	return nextSeqLogName(role, 0)
}

func nextSeqLogName(role string, seq int) string {
	return "netsgo-" + role + "-" + currentDate() + "-" + formatSeq(seq) + ".log"
}

func currentDate() string {
	return time.Now().Format("2006-01-02")
}

func formatSeq(seq int) string {
	return fmt.Sprintf("%03d", seq)
}

func mustWriteFile(t *testing.T, path string, content []byte, perm os.FileMode) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatalf("写测试文件失败: %v", err)
	}
}

func mustChmod(t *testing.T, path string, perm os.FileMode) {
	t.Helper()

	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("设置权限失败: %v", err)
	}
}

func assertFileContainsSubstrings(t *testing.T, path string, wants ...string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}

	content := string(got)
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("期望文件包含 %q，实际内容 %q", want, content)
		}
	}
}

func assertPermissions(t *testing.T, dir string, file string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("目录检查失败: %v", err)
		}
		f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("文件不可写: %v", err)
		}
		_ = f.Close()
		return
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("读取目录权限失败: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("期望目录权限 0700，得到 %#o", got)
	}

	fileInfo, err := os.Stat(file)
	if err != nil {
		t.Fatalf("读取文件权限失败: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("期望文件权限 0600，得到 %#o", got)
	}
}

func assertPathNotExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err == nil {
		t.Fatalf("期望文件不存在: %s", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("检查文件不存在失败: %v", err)
	}
}

func assertExistingWritableFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("期望文件存在: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("期望普通文件，得到目录: %s", path)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("期望文件可写: %v", err)
	}
	_ = f.Close()
}

func assertFileSize(t *testing.T, path string, want int64) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取文件大小失败: %v", err)
	}
	if info.Size() != want {
		t.Fatalf("期望文件大小 %d，得到 %d", want, info.Size())
	}
}

func setHomeEnv(t *testing.T, home string) {
	t.Helper()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
}
