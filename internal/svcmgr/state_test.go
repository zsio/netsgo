package svcmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallStateString(t *testing.T) {
	tests := []struct {
		state InstallState
		want  string
	}{
		{state: StateNotInstalled, want: "not-installed"},
		{state: StateInstalled, want: "installed"},
		{state: StateHistoricalDataOnly, want: "historical-data-only"},
		{state: StateBroken, want: "broken"},
		{state: InstallState(99), want: "InstallState(99)"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Fatalf("InstallState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestDetectWithPaths(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		root := t.TempDir()
		unitPath := filepath.Join(root, "netsgo.service")
		specPath := filepath.Join(root, "service.json")
		envPath := filepath.Join(root, "service.env")
		dataDir := filepath.Join(root, "data")
		if got := DetectWithPaths(unitPath, specPath, envPath, dataDir, true); got != StateNotInstalled {
			t.Fatalf("DetectWithPaths() = %v, want %v", got, StateNotInstalled)
		}
	})

	t.Run("server historical data only", func(t *testing.T) {
		root := t.TempDir()
		unitPath := filepath.Join(root, "netsgo.service")
		specPath := filepath.Join(root, "service.json")
		envPath := filepath.Join(root, "service.env")
		dataDir := filepath.Join(root, "server")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("创建 data dir 失败: %v", err)
		}
		writeStateTestFile(t, filepath.Join(dataDir, "admin.json"), 0o644)
		if got := DetectWithPaths(unitPath, specPath, envPath, dataDir, true); got != StateHistoricalDataOnly {
			t.Fatalf("DetectWithPaths() = %v, want %v", got, StateHistoricalDataOnly)
		}
	})

	t.Run("server installed", func(t *testing.T) {
		unitPath, specPath, envPath, dataDir := writeInstalledState(t, RoleServer)
		if got := DetectWithPaths(unitPath, specPath, envPath, dataDir, true); got != StateInstalled {
			t.Fatalf("DetectWithPaths() = %v, want %v", got, StateInstalled)
		}
	})

	t.Run("server installed missing admin store is broken", func(t *testing.T) {
		unitPath, specPath, envPath, dataDir := writeInstalledState(t, RoleServer)
		if err := os.Remove(filepath.Join(dataDir, "admin.json")); err != nil {
			t.Fatalf("删除 admin.json 失败: %v", err)
		}
		if got := DetectWithPaths(unitPath, specPath, envPath, dataDir, true); got != StateBroken {
			t.Fatalf("DetectWithPaths() = %v, want %v", got, StateBroken)
		}
	})

	t.Run("broken partial trio", func(t *testing.T) {
		unitPath, specPath, envPath, dataDir := writeInstalledState(t, RoleServer)
		if err := os.Remove(envPath); err != nil {
			t.Fatalf("删除 env 文件失败: %v", err)
		}
		if got := DetectWithPaths(unitPath, specPath, envPath, dataDir, true); got != StateBroken {
			t.Fatalf("DetectWithPaths() = %v, want %v", got, StateBroken)
		}
	})

	t.Run("broken client data only", func(t *testing.T) {
		root := t.TempDir()
		unitPath := filepath.Join(root, "netsgo.service")
		specPath := filepath.Join(root, "service.json")
		envPath := filepath.Join(root, "service.env")
		dataDir := filepath.Join(root, "client")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("创建 data dir 失败: %v", err)
		}
		if got := DetectWithPaths(unitPath, specPath, envPath, dataDir, false); got != StateBroken {
			t.Fatalf("DetectWithPaths() = %v, want %v", got, StateBroken)
		}
	})
}

func TestInspectWithPathsReportsBrokenExecStart(t *testing.T) {
	unitPath, specPath, envPath, dataDir := writeInstalledState(t, RoleServer)
	writeStateTestFile(t, unitPath, 0o644)

	inspection := InspectWithPaths(RoleServer, unitPath, specPath, envPath, dataDir)
	if inspection.State != StateBroken {
		t.Fatalf("InspectWithPaths().State = %v, want %v", inspection.State, StateBroken)
	}
	if len(inspection.Problems) == 0 {
		t.Fatal("broken inspection 应返回问题列表")
	}
}

func writeInstalledState(t *testing.T, role Role) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	dataDir := filepath.Join(dataRoot, string(role))
	binaryPath := filepath.Join(root, "bin", "netsgo")
	writeStateTestFile(t, binaryPath, 0o755)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("创建 role data dir 失败: %v", err)
	}

	spec := ServiceSpec{
		Role:        role,
		ServiceName: "netsgo-" + string(role),
		BinaryPath:  binaryPath,
		DataDir:     dataRoot,
		UnitPath:    filepath.Join(root, UnitName(role)),
		EnvPath:     filepath.Join(root, string(role)+".env"),
		SpecPath:    filepath.Join(root, string(role)+".json"),
		RunAsUser:   SystemUser,
	}
	if role == RoleServer {
		spec.ListenPort = 8080
		spec.TLSMode = "off"
		spec.ServerURL = "https://panel.example.com"
		writeStateTestFile(t, filepath.Join(dataDir, "admin.json"), 0o600)
		if err := WriteServerSpec(spec); err != nil {
			t.Fatalf("WriteServerSpec() 失败: %v", err)
		}
		if err := WriteServerEnv(spec, ServerEnv{Port: 8080, TLSMode: "off", ServerAddr: "https://panel.example.com"}); err != nil {
			t.Fatalf("WriteServerEnv() 失败: %v", err)
		}
		if err := WriteServerUnit(spec); err != nil {
			t.Fatalf("WriteServerUnit() 失败: %v", err)
		}
	} else {
		spec.ServerURL = "wss://panel.example.com"
		if err := WriteClientSpec(spec); err != nil {
			t.Fatalf("WriteClientSpec() 失败: %v", err)
		}
		if err := WriteClientEnv(spec, ClientEnv{Server: "wss://panel.example.com", Key: "sk-test-key"}); err != nil {
			t.Fatalf("WriteClientEnv() 失败: %v", err)
		}
		if err := WriteClientUnit(spec); err != nil {
			t.Fatalf("WriteClientUnit() 失败: %v", err)
		}
	}

	return spec.UnitPath, spec.SpecPath, spec.EnvPath, dataDir
}

func writeStateTestFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建测试目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), mode); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
}
