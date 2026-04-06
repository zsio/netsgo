package server

import "testing"

func TestLoadRecoverableInitParams(t *testing.T) {
	dataDir := t.TempDir()
	params := InitParams{
		AdminUsername: "admin",
		AdminPassword: "Password123",
		ServerAddr:    "https://panel.example.com",
		AllowedPorts:  "80,443,10000-20000",
	}
	if err := ApplyInit(dataDir, params); err != nil {
		t.Fatalf("ApplyInit() 失败: %v", err)
	}

	got, err := LoadRecoverableInitParams(dataDir)
	if err != nil {
		t.Fatalf("LoadRecoverableInitParams() 失败: %v", err)
	}
	if got.ServerAddr != params.ServerAddr {
		t.Fatalf("ServerAddr = %q, want %q", got.ServerAddr, params.ServerAddr)
	}
	if got.AllowedPorts != params.AllowedPorts {
		t.Fatalf("AllowedPorts = %q, want %q", got.AllowedPorts, params.AllowedPorts)
	}
}

func TestLoadRecoverableInitParamsRequiresInitializedData(t *testing.T) {
	if _, err := LoadRecoverableInitParams(t.TempDir()); err == nil {
		t.Fatal("未初始化的历史数据应返回错误")
	}
}

func TestLoadRecoverableInitParamsKeepsHistoricalServerAddr(t *testing.T) {
	dataDir := t.TempDir()
	params := InitParams{
		AdminUsername: "admin",
		AdminPassword: "Password123",
		ServerAddr:    "https://old.example.com",
		AllowedPorts:  "10000-10010",
	}
	if err := ApplyInit(dataDir, params); err != nil {
		t.Fatalf("ApplyInit() 失败: %v", err)
	}
	got, err := LoadRecoverableInitParams(dataDir)
	if err != nil {
		t.Fatalf("LoadRecoverableInitParams() 失败: %v", err)
	}
	if got.ServerAddr != "https://old.example.com" {
		t.Fatalf("历史恢复应沿用旧 ServerAddr，得到 %q", got.ServerAddr)
	}
}
