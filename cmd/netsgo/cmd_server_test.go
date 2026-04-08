package main

import (
	"errors"
	"testing"

	"netsgo/internal/server"

	"github.com/spf13/viper"
)

type fakeInitPrompter struct {
	interactive      bool
	promptResponses  []string
	passwordResponse string
	promptErr        error
	passwordErr      error
	prompts          []string
}

func (p *fakeInitPrompter) IsInteractive() bool {
	return p.interactive
}

func (p *fakeInitPrompter) Prompt(label string) (string, error) {
	p.prompts = append(p.prompts, label)
	if p.promptErr != nil {
		return "", p.promptErr
	}
	if len(p.promptResponses) == 0 {
		return "", nil
	}
	value := p.promptResponses[0]
	p.promptResponses = p.promptResponses[1:]
	return value, nil
}

func (p *fakeInitPrompter) PromptPassword(label string) (string, error) {
	p.prompts = append(p.prompts, label)
	if p.passwordErr != nil {
		return "", p.passwordErr
	}
	return p.passwordResponse, nil
}

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
		},
		{
			name:        "uninitialized with partial init flags fails",
			initialized: false,
			params: initFlagValues{
				AdminUsername: "admin",
				AdminPassword: "Password123",
			},
			wantErr: true,
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
		})
	}
}

func TestCompleteInitParamsForStartup_PromptsMissingFieldsInInteractiveMode(t *testing.T) {
	prompter := &fakeInitPrompter{
		interactive:      true,
		passwordResponse: "Password123",
		promptResponses:  []string{"https://panel.example.com", "10000-10010"},
	}

	params, err := completeInitParamsForStartup(false, server.InitParams{
		AdminUsername: "admin",
	}, prompter)
	if err != nil {
		t.Fatalf("interactive completion should not fail: %v", err)
	}
	if params.AdminUsername != "admin" {
		t.Fatalf("expected AdminUsername to remain %q, got %q", "admin", params.AdminUsername)
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
	if len(prompter.prompts) != 3 {
		t.Fatalf("expected 3 prompts, got %d", len(prompter.prompts))
	}
}

func TestCompleteInitParamsForStartup_PropagatesPromptError(t *testing.T) {
	wantErr := errors.New("stdin failed")
	prompter := &fakeInitPrompter{
		interactive: true,
		promptErr:   wantErr,
	}

	_, err := completeInitParamsForStartup(false, server.InitParams{}, prompter)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected prompt error %v, got %v", wantErr, err)
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
