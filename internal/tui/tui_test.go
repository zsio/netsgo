package tui

import (
	"io"
	"os"
	"strings"
	"testing"
)

func capturePrintSummary(title string, rows [][2]string) string {
	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w
	PrintSummary(title, rows)
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestPrintSummary(t *testing.T) {
	output := capturePrintSummary("安装配置", [][2]string{{"角色", "server"}, {"端口", "8080"}})
	if !strings.Contains(output, "安装配置") {
		t.Fatalf("summary 输出缺少标题: %q", output)
	}
	if !strings.Contains(output, "角色") || !strings.Contains(output, "server") {
		t.Fatalf("summary 输出缺少角色行: %q", output)
	}
	if !strings.Contains(output, "端口") || !strings.Contains(output, "8080") {
		t.Fatalf("summary 输出缺少端口行: %q", output)
	}
}
