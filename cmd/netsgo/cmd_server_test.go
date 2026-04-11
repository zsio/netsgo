package main

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestBuildInitParamsFromViper(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set("init-admin-username", "admin")
	viper.Set("init-admin-password", "Password123")
	viper.Set("init-server-addr", "https://panel.example.com")
	viper.Set("init-allowed-ports", "10000-10010")

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
	if params.AllowedPorts != "10000-10010" {
		t.Fatalf("expected AllowedPorts %q, got %q", "10000-10010", params.AllowedPorts)
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
			wantMsg: "--init-admin-username, --init-admin-password, --init-server-addr, --init-allowed-ports",
		},
		{
			name:        "uninitialized with full init flags passes",
			initialized: false,
			params: initFlagValues{
				AdminUsername: "admin",
				AdminPassword: "Password123",
				ServerAddr:    "https://panel.example.com",
				AllowedPorts:  "10000-10010",
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
