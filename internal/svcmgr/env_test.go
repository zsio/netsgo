package svcmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadServerEnvRoundTrip(t *testing.T) {
	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")

	want := ServerEnv{
		Port:                        9527,
		TLSMode:                     "off",
		TLSCert:                     "/tmp/cert.pem",
		TLSKey:                      "/tmp/key.pem",
		TrustedProxies:              "127.0.0.1/32",
		ServerAddr:                  "https://panel.example.com",
		AllowLoopbackManagementHost: true,
	}

	if err := WriteServerEnv(layout, want); err != nil {
		t.Fatalf("WriteServerEnv() failed: %v", err)
	}

	got, err := ReadServerEnv(layout)
	if err != nil {
		t.Fatalf("ReadServerEnv() failed: %v", err)
	}
	if got != want {
		t.Fatalf("server env round trip = %#v, want %#v", got, want)
	}
}

func TestWriteReadClientEnvRoundTrip(t *testing.T) {
	layout := NewLayout(RoleClient)
	layout.EnvPath = filepath.Join(t.TempDir(), "client.env")

	want := ClientEnv{
		Server:         "wss://panel.example.com",
		Key:            "sk-test-key",
		TLSSkipVerify:  true,
		TLSFingerprint: "AA:BB:CC",
	}

	if err := WriteClientEnv(layout, want); err != nil {
		t.Fatalf("WriteClientEnv() failed: %v", err)
	}

	got, err := ReadClientEnv(layout)
	if err != nil {
		t.Fatalf("ReadClientEnv() failed: %v", err)
	}
	if got != want {
		t.Fatalf("client env round trip = %#v, want %#v", got, want)
	}
}

func TestWriteServerEnvSparseAndPermissions(t *testing.T) {
	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")

	if err := WriteServerEnv(layout, ServerEnv{}); err != nil {
		t.Fatalf("WriteServerEnv() failed: %v", err)
	}

	content, err := os.ReadFile(layout.EnvPath)
	if err != nil {
		t.Fatalf("failed to read env file: %v", err)
	}
	if len(content) != 0 {
		t.Fatalf("zero-value env should not write any content, got %q", string(content))
	}

	info, err := os.Stat(layout.EnvPath)
	if err != nil {
		t.Fatalf("failed to stat env file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env file permissions = %v, want 0600", info.Mode().Perm())
	}
}

func TestWriteRawEnvRejectsForbiddenKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.env")
	err := writeEnvFile(path, map[string]string{
		"NETSGO_INIT_ADMIN_PASSWORD": "Password123",
	})
	if err == nil {
		t.Fatal("writing env with NETSGO_INIT_* entries should fail")
	}
}
