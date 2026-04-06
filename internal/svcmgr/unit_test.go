package svcmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteServerUnit(t *testing.T) {
	spec := NewSpec(RoleServer)
	spec.UnitPath = filepath.Join(t.TempDir(), "netsgo-server.service")

	if err := WriteServerUnit(spec); err != nil {
		t.Fatalf("WriteServerUnit() 失败: %v", err)
	}

	content, err := os.ReadFile(spec.UnitPath)
	if err != nil {
		t.Fatalf("读取 server unit 失败: %v", err)
	}
	text := string(content)

	required := []string{
		"Wants=network-online.target",
		"After=network-online.target",
		"User=netsgo",
		"Group=netsgo",
		"EnvironmentFile=/etc/netsgo/services/server.env",
		"ExecStart=/usr/local/bin/netsgo server --data-dir /var/lib/netsgo",
		"Restart=on-failure",
		"RestartSec=5s",
		"NoNewPrivileges=true",
	}
	for _, fragment := range required {
		if !strings.Contains(text, fragment) {
			t.Fatalf("server unit 缺少 %q\n%s", fragment, text)
		}
	}

	info, err := os.Stat(spec.UnitPath)
	if err != nil {
		t.Fatalf("读取 server unit 状态失败: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("server unit 权限 = %v, want 0644", info.Mode().Perm())
	}
}

func TestWriteClientUnit(t *testing.T) {
	spec := NewSpec(RoleClient)
	spec.UnitPath = filepath.Join(t.TempDir(), "netsgo-client.service")

	if err := WriteClientUnit(spec); err != nil {
		t.Fatalf("WriteClientUnit() 失败: %v", err)
	}

	content, err := os.ReadFile(spec.UnitPath)
	if err != nil {
		t.Fatalf("读取 client unit 失败: %v", err)
	}
	text := string(content)

	required := []string{
		"Wants=network-online.target",
		"After=network-online.target",
		"EnvironmentFile=/etc/netsgo/services/client.env",
		"ExecStart=/usr/local/bin/netsgo client --data-dir /var/lib/netsgo",
		"User=netsgo",
		"Group=netsgo",
	}
	for _, fragment := range required {
		if !strings.Contains(text, fragment) {
			t.Fatalf("client unit 缺少 %q\n%s", fragment, text)
		}
	}
}

func TestReadUnitExecStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo.service")
	content := "[Service]\nExecStart=/usr/local/bin/netsgo server --data-dir /var/lib/netsgo\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试 unit 失败: %v", err)
	}

	got, err := ReadUnitExecStart(path)
	if err != nil {
		t.Fatalf("ReadUnitExecStart() 失败: %v", err)
	}
	want := "/usr/local/bin/netsgo server --data-dir /var/lib/netsgo"
	if got != want {
		t.Fatalf("ReadUnitExecStart() = %q, want %q", got, want)
	}
}
