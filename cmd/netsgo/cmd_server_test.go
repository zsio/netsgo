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
		t.Fatalf("AdminUsername 期望 admin，得到 %q", params.AdminUsername)
	}
	if params.AdminPassword != "Password123" {
		t.Fatalf("AdminPassword 期望 Password123，得到 %q", params.AdminPassword)
	}
	if params.ServerAddr != "https://panel.example.com" {
		t.Fatalf("ServerAddr 期望 https://panel.example.com，得到 %q", params.ServerAddr)
	}
	if params.AllowedPorts != "10000-10010" {
		t.Fatalf("AllowedPorts 期望 10000-10010，得到 %q", params.AllowedPorts)
	}
	if !params.IsComplete() {
		t.Fatal("完整初始化参数应被识别为 complete")
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
				t.Fatal("期望返回错误，实际为 nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("期望无错误，实际为 %v", err)
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
		t.Fatalf("交互补全不应报错: %v", err)
	}
	if params.AdminUsername != "admin" {
		t.Fatalf("AdminUsername 期望保留 admin，得到 %q", params.AdminUsername)
	}
	if params.AdminPassword != "Password123" {
		t.Fatalf("AdminPassword 期望 Password123，得到 %q", params.AdminPassword)
	}
	if params.ServerAddr != "https://panel.example.com" {
		t.Fatalf("ServerAddr 期望 https://panel.example.com，得到 %q", params.ServerAddr)
	}
	if params.AllowedPorts != "10000-10010" {
		t.Fatalf("AllowedPorts 期望 10000-10010，得到 %q", params.AllowedPorts)
	}
	if len(prompter.prompts) != 3 {
		t.Fatalf("期望提示 3 次，实际 %d 次", len(prompter.prompts))
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
		t.Fatalf("期望返回 prompt 错误 %v，得到 %v", wantErr, err)
	}
}

func TestShouldWarnInitFlagsIgnored(t *testing.T) {
	if !shouldWarnInitFlagsIgnored(true, initFlagValues{AdminUsername: "admin"}) {
		t.Fatal("已初始化且提供 init 参数时应提示已忽略")
	}
	if shouldWarnInitFlagsIgnored(false, initFlagValues{AdminUsername: "admin"}) {
		t.Fatal("未初始化时不应提示已忽略")
	}
	if shouldWarnInitFlagsIgnored(true, initFlagValues{}) {
		t.Fatal("未提供 init 参数时不应提示已忽略")
	}
}
