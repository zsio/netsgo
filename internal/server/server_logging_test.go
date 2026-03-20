package server

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"netsgo/pkg/logger"
)

func TestEmitSetupTokenBanner_WritesToStderrOnly(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")
	if err := logger.Init("server", logDir); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	t.Cleanup(logger.Close)

	s := New(0)
	s.setupToken = "setup-secret-123"

	stderr := captureStderr(t, func() {
		s.emitSetupTokenBanner(os.Stderr)
	})

	if !strings.Contains(stderr, "setup-secret-123") {
		t.Fatalf("stderr 未输出 setup token")
	}

	latest, err := os.ReadFile(findLatestLogFile(t, logDir))
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	if strings.Contains(string(latest), "setup-secret-123") {
		t.Fatalf("setup token 不应进入日志文件")
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建 stderr pipe 失败: %v", err)
	}

	os.Stderr = w
	defer func() {
		os.Stderr = original
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("关闭 stderr writer 失败: %v", err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("读取 stderr 失败: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("关闭 stderr reader 失败: %v", err)
	}

	return string(data)
}

func findLatestLogFile(t *testing.T, logDir string) string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(logDir, "netsgo-server-*.log"))
	if err != nil {
		t.Fatalf("匹配日志文件失败: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("未找到日志文件")
	}

	sort.Strings(matches)
	return matches[len(matches)-1]
}
