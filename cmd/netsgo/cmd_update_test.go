package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestUpdateCommandGuidanceIsConcise(t *testing.T) {
	output := captureStdout(t, func() {
		updateCmd.Run(&cobra.Command{}, nil)
	})

	for _, want := range []string{
		"托管服务：运行 'netsgo manage'，选择“更新”",
		"已有新版 netsgo 文件：执行新版文件的 'netsgo upgrade'",
		"手动下载：https://github.com/zsio/netsgo/releases",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in output, got %q", want, output)
		}
	}
	for _, notWant := range []string{"托管服务更新：", "已下载新版 netsgo 时：", "用该可执行文件", "用该文件运行", "检查、确认、下载、校验", "应用最新 release", "下载、验证、应用、重启"} {
		if strings.Contains(output, notWant) {
			t.Fatalf("standalone update guidance should not expose internal steps %q: %q", notWant, output)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = orig
	})

	fn()
	os.Stdout = orig
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return string(out)
}
