package svcmgr

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWriteReadServerEnvRoundTrip(t *testing.T) {
	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")

	want := ServerEnv{
		Port:                               9527,
		TLSMode:                            "off",
		TLSCert:                            "/tmp/cert.pem",
		TLSKey:                             "/tmp/key.pem",
		TrustedProxies:                     "127.0.0.1/32",
		ServerAddr:                         "https://panel.example.com",
		AllowLoopbackManagementHost:        true,
		AllowLoopbackManagementHostDefined: true,
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

func TestReadServerEnvDefaultsLoopbackManagementHostToTrue(t *testing.T) {
	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")
	if err := os.WriteFile(layout.EnvPath, []byte("NETSGO_PORT=9527\n"), 0o640); err != nil {
		t.Fatalf("write env fixture: %v", err)
	}

	got, err := ReadServerEnv(layout)
	if err != nil {
		t.Fatalf("ReadServerEnv() failed: %v", err)
	}
	if !got.AllowLoopbackManagementHost {
		t.Fatal("missing loopback management Host env should default to true")
	}
	if got.AllowLoopbackManagementHostDefined {
		t.Fatal("missing loopback management Host env should not be marked explicitly defined")
	}
}

func TestReadServerEnvIgnoresComments(t *testing.T) {
	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")
	if err := os.WriteFile(layout.EnvPath, []byte("# comment\nNETSGO_PORT=9527\n"), 0o640); err != nil {
		t.Fatalf("write env fixture: %v", err)
	}

	got, err := ReadServerEnv(layout)
	if err != nil {
		t.Fatalf("ReadServerEnv() failed: %v", err)
	}
	if got.Port != 9527 {
		t.Fatalf("server env port = %d, want 9527", got.Port)
	}
}

func TestWriteReadServerEnvPreservesExplicitFalseValue(t *testing.T) {
	stubLookupSystemUser(t, strconv.Itoa(os.Getgid()))

	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")
	want := ServerEnv{
		Port:                               9527,
		AllowLoopbackManagementHost:        false,
		AllowLoopbackManagementHostDefined: true,
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

func TestWriteServerEnvOverwritesLegacyLoopbackManagementHostFalse(t *testing.T) {
	stubLookupSystemUser(t, strconv.Itoa(os.Getgid()))

	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")
	if err := os.WriteFile(layout.EnvPath, []byte("NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST=false\n"), 0o640); err != nil {
		t.Fatalf("write legacy env fixture: %v", err)
	}

	if err := WriteServerEnv(layout, ServerEnv{
		Port:                               9527,
		AllowLoopbackManagementHost:        true,
		AllowLoopbackManagementHostDefined: true,
	}); err != nil {
		t.Fatalf("WriteServerEnv() failed: %v", err)
	}
	got, err := ReadServerEnv(layout)
	if err != nil {
		t.Fatalf("ReadServerEnv() failed: %v", err)
	}
	if !got.AllowLoopbackManagementHost || !got.AllowLoopbackManagementHostDefined {
		t.Fatalf("server env should overwrite legacy false with explicit true, got %#v", got)
	}
}

func TestEnableServerLoopbackManagementHostOverwritesFalseAndPreservesOtherEntries(t *testing.T) {
	stubLookupSystemUser(t, strconv.Itoa(os.Getgid()))

	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")
	fixture := strings.Join([]string{
		"# keep this comment",
		"NETSGO_PORT=9527",
		"NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST=false",
		"NETSGO_CUSTOM=value",
		"",
	}, "\n")
	if err := os.WriteFile(layout.EnvPath, []byte(fixture), 0o640); err != nil {
		t.Fatalf("write env fixture: %v", err)
	}

	if err := EnableServerLoopbackManagementHost(layout); err != nil {
		t.Fatalf("EnableServerLoopbackManagementHost() failed: %v", err)
	}
	content, err := os.ReadFile(layout.EnvPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"# keep this comment",
		"NETSGO_PORT=9527",
		"NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST=true",
		"NETSGO_CUSTOM=value",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("env file should contain %q, got %q", want, text)
		}
	}
	if strings.Contains(text, "NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST=false") {
		t.Fatalf("env file should not keep legacy false value, got %q", text)
	}
}

func TestEnableServerLoopbackManagementHostAppendsMissingKey(t *testing.T) {
	stubLookupSystemUser(t, strconv.Itoa(os.Getgid()))

	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(t.TempDir(), "server.env")
	if err := os.WriteFile(layout.EnvPath, []byte("NETSGO_PORT=9527\n"), 0o640); err != nil {
		t.Fatalf("write env fixture: %v", err)
	}

	if err := EnableServerLoopbackManagementHost(layout); err != nil {
		t.Fatalf("EnableServerLoopbackManagementHost() failed: %v", err)
	}
	content, err := os.ReadFile(layout.EnvPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if !strings.Contains(string(content), "NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST=true\n") {
		t.Fatalf("env file should append loopback management Host default, got %q", string(content))
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
	stubLookupSystemUser(t, strconv.Itoa(os.Getgid()))

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
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("env file permissions = %v, want 0640", info.Mode().Perm())
	}
	assertEnvFileGroup(t, layout.EnvPath, os.Getgid())
}

func TestWriteServerEnvCreatesTraversableParentDir(t *testing.T) {
	stubLookupSystemUser(t, strconv.Itoa(os.Getgid()))

	root := t.TempDir()
	layout := NewLayout(RoleServer)
	layout.EnvPath = filepath.Join(root, "services", "server.env")

	if err := WriteServerEnv(layout, ServerEnv{Port: 9527}); err != nil {
		t.Fatalf("WriteServerEnv() failed: %v", err)
	}

	info, err := os.Stat(filepath.Dir(layout.EnvPath))
	if err != nil {
		t.Fatalf("stat env parent dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("env parent path is not a directory: %s", filepath.Dir(layout.EnvPath))
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("env parent dir permissions = %v, want 0755", info.Mode().Perm())
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

func TestWriteRawEnvRejectsUnsafeKeysAndValues(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "newline", values: map[string]string{"NETSGO_KEY": "first\nNETSGO_TLS_SKIP_VERIFY=true"}},
		{name: "carriage return", values: map[string]string{"NETSGO_SERVER": "https://panel.example.com\r"}},
		{name: "nul", values: map[string]string{"NETSGO_KEY": "abc\x00def"}},
		{name: "invalid key", values: map[string]string{"NETSGO KEY": "value"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "client.env")
			err := writeEnvFile(path, tt.values)
			if err == nil {
				t.Fatal("writeEnvFile() error = nil, want unsafe env rejection")
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("unsafe env write should not create %s, stat error = %v", path, statErr)
			}
		})
	}
}

func stubLookupSystemUser(t *testing.T, gid string) {
	t.Helper()
	original := lookupSystemUser
	lookupSystemUser = func(string) (*user.User, error) {
		return &user.User{Uid: strconv.Itoa(os.Getuid()), Gid: gid}, nil
	}
	t.Cleanup(func() {
		lookupSystemUser = original
	})
}
