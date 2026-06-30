package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"netsgo/internal/server"
	"netsgo/pkg/flock"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func TestServerAllowLoopbackManagementHostDefaultsTrue(t *testing.T) {
	resetAllowLoopbackManagementHostFlagForTest(t)
	unsetEnvForTest(t, "NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST")

	got := serverCmd.Flags().Lookup("allow-loopback-management-host").DefValue
	if got != "true" {
		t.Fatalf("allow-loopback-management-host default = %q, want true", got)
	}
	if !viper.GetBool("allow-loopback-management-host") {
		t.Fatal("allow-loopback-management-host should default to true through viper")
	}
}

func TestServerAllowLoopbackManagementHostEnvCanDisableDefault(t *testing.T) {
	resetAllowLoopbackManagementHostFlagForTest(t)
	t.Setenv("NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST", "false")

	if viper.GetBool("allow-loopback-management-host") {
		t.Fatal("NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST=false should disable loopback management Host fallback")
	}
}

func TestServerAllowLoopbackManagementHostFlagCanDisableDefault(t *testing.T) {
	flag := resetAllowLoopbackManagementHostFlagForTest(t)

	if err := flag.Value.Set("false"); err != nil {
		t.Fatalf("set flag value: %v", err)
	}
	flag.Changed = true
	if viper.GetBool("allow-loopback-management-host") {
		t.Fatal("--allow-loopback-management-host=false should disable loopback management Host fallback")
	}
}

func resetAllowLoopbackManagementHostFlagForTest(t *testing.T) *pflag.Flag {
	t.Helper()
	flag := serverCmd.Flags().Lookup("allow-loopback-management-host")
	originalValue := flag.Value.String()
	originalChanged := flag.Changed
	if err := flag.Value.Set(flag.DefValue); err != nil {
		t.Fatalf("reset flag value: %v", err)
	}
	flag.Changed = false
	t.Cleanup(func() {
		_ = flag.Value.Set(originalValue)
		flag.Changed = originalChanged
	})
	return flag
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	value, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, value)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func TestBuildInitParamsFromViper(t *testing.T) {
	t.Setenv("NETSGO_INIT_ADMIN_USERNAME", "admin")
	t.Setenv("NETSGO_INIT_ADMIN_PASSWORD", "Password123")
	t.Setenv("NETSGO_INIT_SERVER_ADDR", "https://panel.example.com")

	params := buildInitParamsFromViper()
	if params.AdminUsername != "admin" {
		t.Fatalf("expected AdminUsername %q, got %q", "admin", params.AdminUsername)
	}
	if params.AdminPassword != "Password123" {
		t.Fatalf("expected AdminPassword %q, got %q", "Password123", params.AdminPassword)
	}
	if params.ServerAddr != "https://panel.example.com" {
		t.Fatalf("expected ServerAddr %q, got %q", "https://panel.example.com", params.ServerAddr)
	}
	if !params.IsComplete() {
		t.Fatal("complete init params should be recognized as complete")
	}
}

func TestValidateInitFlagsForStartup(t *testing.T) {
	tests := []struct {
		name        string
		initialized bool
		params      initFlagValues
		wantErr     bool
		wantMsg     string
	}{
		{
			name:        "initialized server ignores init flags",
			initialized: true,
			params: initFlagValues{
				AdminUsername: "admin",
			},
			wantErr: false,
		},
		{
			name:        "uninitialized without any init flags fails",
			initialized: false,
			params:      initFlagValues{},
			wantErr:     true,
			wantMsg:     "or use netsgo install for interactive setup",
		},
		{
			name:        "uninitialized with partial init flags fails",
			initialized: false,
			params: initFlagValues{
				AdminUsername: "admin",
				AdminPassword: "Password123",
			},
			wantErr: true,
			wantMsg: "--init-admin-username, --init-admin-password, --init-server-addr",
		},
		{
			name:        "uninitialized with full init flags passes",
			initialized: false,
			params: initFlagValues{
				AdminUsername: "admin",
				AdminPassword: "Password123",
				ServerAddr:    "https://panel.example.com",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInitFlagsForStartup(tt.initialized, tt.params)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tt.wantMsg)) {
				t.Fatalf("expected error to contain %q, got %v", tt.wantMsg, err)
			}
		})
	}
}

func TestShouldWarnInitFlagsIgnored(t *testing.T) {
	if !shouldWarnInitFlagsIgnored(true, initFlagValues{AdminUsername: "admin"}) {
		t.Fatal("should warn when initialized and init flags provided")
	}
	if shouldWarnInitFlagsIgnored(false, initFlagValues{AdminUsername: "admin"}) {
		t.Fatal("should not warn when not yet initialized")
	}
	if shouldWarnInitFlagsIgnored(true, initFlagValues{}) {
		t.Fatal("should not warn when no init flags provided")
	}
}

func TestPrepareServerStartupRequiresInitWithoutCreatingDB(t *testing.T) {
	dataDir := t.TempDir()

	prepared, err := prepareServerStartup(dataDir, server.InitParams{})
	if err == nil {
		prepared.Unlock()
		t.Fatal("prepareServerStartup should reject an uninitialized server without init params")
	}

	dbPath := filepath.Join(dataDir, "server", server.ServerDBFileName)
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("prepareServerStartup created %s before validation failed; stat error = %v", dbPath, statErr)
	}

	unlock, lockErr := flock.TryLock(filepath.Join(dataDir, "locks", "server.lock"))
	if lockErr != nil {
		t.Fatalf("server lock should be released after validation failure: %v", lockErr)
	}
	unlock()
}
