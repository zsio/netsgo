package svcmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadServerEnvRoundTrip(t *testing.T) {
	spec := NewSpec(RoleServer)
	spec.EnvPath = filepath.Join(t.TempDir(), "server.env")

	want := ServerEnv{
		Port:                        8080,
		TLSMode:                     "off",
		TLSCert:                     "/tmp/cert.pem",
		TLSKey:                      "/tmp/key.pem",
		TrustedProxies:              "127.0.0.1/32",
		ServerAddr:                  "https://panel.example.com",
		AllowLoopbackManagementHost: true,
	}

	if err := WriteServerEnv(spec, want); err != nil {
		t.Fatalf("WriteServerEnv() 失败: %v", err)
	}

	got, err := ReadServerEnv(spec)
	if err != nil {
		t.Fatalf("ReadServerEnv() 失败: %v", err)
	}
	if got != want {
		t.Fatalf("server env round trip = %#v, want %#v", got, want)
	}
}

func TestWriteReadClientEnvRoundTrip(t *testing.T) {
	spec := NewSpec(RoleClient)
	spec.EnvPath = filepath.Join(t.TempDir(), "client.env")

	want := ClientEnv{
		Server:         "wss://panel.example.com",
		Key:            "sk-test-key",
		TLSSkipVerify:  true,
		TLSFingerprint: "AA:BB:CC",
	}

	if err := WriteClientEnv(spec, want); err != nil {
		t.Fatalf("WriteClientEnv() 失败: %v", err)
	}

	got, err := ReadClientEnv(spec)
	if err != nil {
		t.Fatalf("ReadClientEnv() 失败: %v", err)
	}
	if got != want {
		t.Fatalf("client env round trip = %#v, want %#v", got, want)
	}
}

func TestWriteServerEnvSparseAndPermissions(t *testing.T) {
	spec := NewSpec(RoleServer)
	spec.EnvPath = filepath.Join(t.TempDir(), "server.env")

	if err := WriteServerEnv(spec, ServerEnv{}); err != nil {
		t.Fatalf("WriteServerEnv() 失败: %v", err)
	}

	content, err := os.ReadFile(spec.EnvPath)
	if err != nil {
		t.Fatalf("读取 env 文件失败: %v", err)
	}
	if len(content) != 0 {
		t.Fatalf("零值 env 不应写出任何内容，得到 %q", string(content))
	}

	info, err := os.Stat(spec.EnvPath)
	if err != nil {
		t.Fatalf("读取 env 文件状态失败: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env 文件权限 = %v, want 0600", info.Mode().Perm())
	}
}

func TestWriteRawEnvRejectsForbiddenKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.env")
	err := writeEnvFile(path, map[string]string{
		"NETSGO_INIT_ADMIN_PASSWORD": "Password123",
	})
	if err == nil {
		t.Fatal("包含 NETSGO_INIT_* 的 env 写入应失败")
	}
}
