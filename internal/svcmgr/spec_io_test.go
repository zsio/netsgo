package svcmgr

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadServerSpec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	spec := NewSpec(RoleServer)
	spec.SpecPath = path
	spec.ListenPort = 9527
	spec.TLSMode = "off"
	spec.ServerURL = "https://panel.example.com"
	spec.InstalledAt = time.Now().UTC().Format(time.RFC3339)

	if err := WriteServerSpec(spec); err != nil {
		t.Fatalf("WriteServerSpec() 失败: %v", err)
	}

	got, err := ReadServerSpec(path)
	if err != nil {
		t.Fatalf("ReadServerSpec() 失败: %v", err)
	}
	if got != spec {
		t.Fatalf("ReadServerSpec() = %#v, want %#v", got, spec)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取 spec 文件状态失败: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("spec 文件权限 = %v, want 0600", info.Mode().Perm())
	}
}

func TestWriteReadClientSpec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	spec := NewSpec(RoleClient)
	spec.SpecPath = path
	spec.ServerURL = "wss://panel.example.com"
	spec.InstalledAt = time.Now().UTC().Format(time.RFC3339)

	if err := WriteClientSpec(spec); err != nil {
		t.Fatalf("WriteClientSpec() 失败: %v", err)
	}

	got, err := ReadClientSpec(path)
	if err != nil {
		t.Fatalf("ReadClientSpec() 失败: %v", err)
	}
	if got != spec {
		t.Fatalf("ReadClientSpec() = %#v, want %#v", got, spec)
	}
}
