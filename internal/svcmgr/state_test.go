package svcmgr

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
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

func TestInspectWithLayout(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		layout := testLayout(t, RoleServer)
		if got := DetectWithLayout(layout); got != StateNotInstalled {
			t.Fatalf("DetectWithLayout() = %v, want %v", got, StateNotInstalled)
		}
	})

	t.Run("server historical data only", func(t *testing.T) {
		layout := testLayout(t, RoleServer)
		if err := os.MkdirAll(layout.RuntimeDir, 0o755); err != nil {
			t.Fatalf("failed to create runtime dir: %v", err)
		}
		writeInitializedServerDB(t, recoverableServerDataPath(layout.RuntimeDir))
		if got := DetectWithLayout(layout); got != StateHistoricalDataOnly {
			t.Fatalf("DetectWithLayout() = %v, want %v", got, StateHistoricalDataOnly)
		}
	})

	t.Run("server uninitialized sqlite data is broken", func(t *testing.T) {
		layout := testLayout(t, RoleServer)
		if err := os.MkdirAll(layout.RuntimeDir, 0o755); err != nil {
			t.Fatalf("failed to create runtime dir: %v", err)
		}
		writeStateTestFile(t, recoverableServerDataPath(layout.RuntimeDir), 0o600)
		if got := DetectWithLayout(layout); got != StateBroken {
			t.Fatalf("DetectWithLayout() = %v, want %v", got, StateBroken)
		}
	})

	t.Run("server installed", func(t *testing.T) {
		layout := writeInstalledState(t, RoleServer)
		if got := DetectWithLayout(layout); got != StateInstalled {
			t.Fatalf("DetectWithLayout() = %v, want %v", got, StateInstalled)
		}
	})

	t.Run("server installed missing sqlite store is broken", func(t *testing.T) {
		layout := writeInstalledState(t, RoleServer)
		if err := os.Remove(recoverableServerDataPath(layout.RuntimeDir)); err != nil {
			t.Fatalf("failed to remove server sqlite store: %v", err)
		}
		if got := DetectWithLayout(layout); got != StateBroken {
			t.Fatalf("DetectWithLayout() = %v, want %v", got, StateBroken)
		}
	})

	t.Run("broken partial pair", func(t *testing.T) {
		layout := writeInstalledState(t, RoleServer)
		if err := os.Remove(layout.EnvPath); err != nil {
			t.Fatalf("failed to remove env file: %v", err)
		}
		if got := DetectWithLayout(layout); got != StateBroken {
			t.Fatalf("DetectWithLayout() = %v, want %v", got, StateBroken)
		}
	})

	t.Run("broken client data only", func(t *testing.T) {
		layout := testLayout(t, RoleClient)
		if err := os.MkdirAll(layout.RuntimeDir, 0o755); err != nil {
			t.Fatalf("failed to create runtime dir: %v", err)
		}
		if got := DetectWithLayout(layout); got != StateBroken {
			t.Fatalf("DetectWithLayout() = %v, want %v", got, StateBroken)
		}
	})
}

func TestInspectWithLayoutReportsBrokenExecStart(t *testing.T) {
	layout := writeInstalledState(t, RoleServer)
	writeStateTestFile(t, layout.UnitPath, 0o644)

	inspection := InspectWithLayout(layout)
	if inspection.State != StateBroken {
		t.Fatalf("InspectWithLayout().State = %v, want %v", inspection.State, StateBroken)
	}
	if len(inspection.Problems) == 0 {
		t.Fatal("broken inspection should return a problem list")
	}
}

func testLayout(t *testing.T, role Role) ServiceLayout {
	t.Helper()
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	layout := NewLayout(role)
	layout.BinaryPath = filepath.Join(root, "bin", "netsgo")
	layout.DataDir = dataRoot
	layout.RuntimeDir = filepath.Join(dataRoot, string(role))
	layout.UnitPath = filepath.Join(root, UnitName(role))
	layout.EnvPath = filepath.Join(root, string(role)+".env")
	return layout
}

func writeInstalledState(t *testing.T, role Role) ServiceLayout {
	t.Helper()
	layout := testLayout(t, role)
	writeStateTestFile(t, layout.BinaryPath, 0o755)
	if err := os.MkdirAll(layout.RuntimeDir, 0o755); err != nil {
		t.Fatalf("failed to create runtime dir: %v", err)
	}

	if role == RoleServer {
		writeInitializedServerDB(t, recoverableServerDataPath(layout.RuntimeDir))
		if err := WriteServerEnv(layout, ServerEnv{Port: 9527, TLSMode: "off", ServerAddr: "https://panel.example.com"}); err != nil {
			t.Fatalf("WriteServerEnv() failed: %v", err)
		}
		if err := WriteServerUnit(layout); err != nil {
			t.Fatalf("WriteServerUnit() failed: %v", err)
		}
	} else {
		if err := WriteClientEnv(layout, ClientEnv{Server: "wss://panel.example.com", Key: "sk-test-key"}); err != nil {
			t.Fatalf("WriteClientEnv() failed: %v", err)
		}
		if err := WriteClientUnit(layout); err != nil {
			t.Fatalf("WriteClientUnit() failed: %v", err)
		}
	}

	return layout
}

func writeStateTestFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), mode); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
}

func writeInitializedServerDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("failed to close test sqlite db: %v", err)
		}
	}()
	if _, err := db.Exec(`CREATE TABLE server_config (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		initialized INTEGER NOT NULL DEFAULT 0 CHECK (initialized IN (0, 1)),
		jwt_secret TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("failed to create test server_config: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO server_config (id, initialized, jwt_secret) VALUES (1, 1, 'test-secret')`); err != nil {
		t.Fatalf("failed to write initialized server config: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("failed to chmod test sqlite db: %v", err)
	}
}
