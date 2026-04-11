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
		t.Fatalf("ApplyInit() failed: %v", err)
	}

	got, err := LoadRecoverableInitParams(dataDir)
	if err != nil {
		t.Fatalf("LoadRecoverableInitParams() failed: %v", err)
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
		t.Fatal("uninitialized historical data should return error")
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
		t.Fatalf("ApplyInit() failed: %v", err)
	}
	got, err := LoadRecoverableInitParams(dataDir)
	if err != nil {
		t.Fatalf("LoadRecoverableInitParams() failed: %v", err)
	}
	if got.ServerAddr != "https://old.example.com" {
		t.Fatalf("historical recovery should use old ServerAddr, got %q", got.ServerAddr)
	}
}
