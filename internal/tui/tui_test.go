package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintSummary(t *testing.T) {
	var buf bytes.Buffer
	ui := UI{Out: &buf}
	ui.PrintSummary("安装配置", [][2]string{{"角色", "server"}, {"端口", "8080"}})

	output := buf.String()
	if !strings.Contains(output, "安装配置") {
		t.Fatalf("summary 输出缺少标题: %q", output)
	}
	if !strings.Contains(output, "角色: server") {
		t.Fatalf("summary 输出缺少角色: %q", output)
	}
	if !strings.Contains(output, "端口: 8080") {
		t.Fatalf("summary 输出缺少端口: %q", output)
	}
}

func TestInputAndConfirm(t *testing.T) {
	input := bytes.NewBufferString("hello\ny\n")
	var out bytes.Buffer
	ui := UI{In: input, Out: &out}

	value, err := ui.Input("请输入")
	if err != nil {
		t.Fatalf("Input() 失败: %v", err)
	}
	if value != "hello" {
		t.Fatalf("Input() = %q, want hello", value)
	}

	ok, err := ui.Confirm("确认?")
	if err != nil {
		t.Fatalf("Confirm() 失败: %v", err)
	}
	if !ok {
		t.Fatal("Confirm() 应返回 true")
	}
}

func TestSelectRetryOnInvalidInput(t *testing.T) {
	input := bytes.NewBufferString("abc\n5\n2\n")
	var out bytes.Buffer
	ui := UI{In: input, Out: &out}

	index, err := ui.Select("选择角色", []string{"server", "client"})
	if err != nil {
		t.Fatalf("Select() 最终应成功: %v", err)
	}
	if index != 1 {
		t.Fatalf("Select() = %d, want 1", index)
	}
	output := out.String()
	if !strings.Contains(output, "无效输入") {
		t.Fatalf("Select() 应输出无效输入提示: %q", output)
	}
}

func TestSelectFailsAfterMaxRetries(t *testing.T) {
	input := bytes.NewBufferString("abc\n0\n9\n")
	var out bytes.Buffer
	ui := UI{In: input, Out: &out}

	_, err := ui.Select("选择角色", []string{"server", "client"})
	if err == nil {
		t.Fatal("Select() 多次无效输入后应失败")
	}
	if !strings.Contains(err.Error(), "未获得有效输入") {
		t.Fatalf("Select() 错误应说明重试失败，得到 %v", err)
	}
}
