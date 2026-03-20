package logger

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDefaultDir_UsesHomeNetsgoLogs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir, err := DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir 失败: %v", err)
	}

	want := filepath.Join(os.Getenv("HOME"), ".netsgo", "logs")
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

func TestInit_ReusesExistingFileWhenBelowLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	existing := filepath.Join(dir, currentLogName("server"))
	mustWriteFile(t, existing, []byte("old\n"), 0o600)

	if err := Init("server", dir); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	t.Cleanup(Close)

	if got := globalWriter.file.Name(); got != existing {
		t.Fatalf("期望继续写入现有文件 %s，得到 %s", existing, got)
	}

	if _, err := globalWriter.Write([]byte("new\n")); err != nil {
		t.Fatalf("写日志失败: %v", err)
	}

	assertFileContainsSubstrings(t, existing, "old\n", "new\n")
}

func TestInit_CreatesNextSeqWhenLatestFileIsFull(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	existing := filepath.Join(dir, currentLogName("server"))
	mustWriteFile(t, existing, bytes.Repeat([]byte("a"), int(maxFileSize)), 0o600)

	if err := Init("server", dir); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	t.Cleanup(Close)

	if got := filepath.Base(globalWriter.file.Name()); got != nextSeqLogName("server", 1) {
		t.Fatalf("期望新序号文件，得到 %s", got)
	}
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
