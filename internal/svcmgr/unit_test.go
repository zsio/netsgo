package svcmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteServerUnit(t *testing.T) {
	layout := NewLayout(RoleServer)
	layout.UnitPath = filepath.Join(t.TempDir(), "netsgo-server.service")

	if err := WriteServerUnit(layout); err != nil {
		t.Fatalf("WriteServerUnit() failed: %v", err)
	}

	content, err := os.ReadFile(layout.UnitPath)
	if err != nil {
		t.Fatalf("failed to read server unit: %v", err)
	}
	text := string(content)

	required := []string{
		"Wants=network-online.target",
		"After=network-online.target",
		"User=netsgo",
		"Group=netsgo",
		"EnvironmentFile=/etc/netsgo/services/server.env",
		"ExecStart=/usr/local/bin/netsgo server --data-dir /var/lib/netsgo",
		"Restart=on-failure",
		"RestartSec=5s",
		"NoNewPrivileges=true",
	}
	for _, fragment := range required {
		if !strings.Contains(text, fragment) {
			t.Fatalf("server unit is missing %q\n%s", fragment, text)
		}
	}

	info, err := os.Stat(layout.UnitPath)
	if err != nil {
		t.Fatalf("failed to stat server unit: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("server unit permissions = %v, want 0644", info.Mode().Perm())
	}
}

func TestWriteClientUnit(t *testing.T) {
	layout := NewLayout(RoleClient)
	layout.UnitPath = filepath.Join(t.TempDir(), "netsgo-client.service")

	if err := WriteClientUnit(layout); err != nil {
		t.Fatalf("WriteClientUnit() failed: %v", err)
	}

	content, err := os.ReadFile(layout.UnitPath)
	if err != nil {
		t.Fatalf("failed to read client unit: %v", err)
	}
	text := string(content)

	required := []string{
		"Wants=network-online.target",
		"After=network-online.target",
		"EnvironmentFile=/etc/netsgo/services/client.env",
		"ExecStart=/usr/local/bin/netsgo client --data-dir /var/lib/netsgo",
		"User=netsgo",
		"Group=netsgo",
	}
	for _, fragment := range required {
		if !strings.Contains(text, fragment) {
			t.Fatalf("client unit is missing %q\n%s", fragment, text)
		}
	}
}

func TestReadUnitExecStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo.service")
	content := "[Service]\nExecStart=/usr/local/bin/netsgo server --data-dir /var/lib/netsgo\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test unit: %v", err)
	}

	got, err := ReadUnitExecStart(path)
	if err != nil {
		t.Fatalf("ReadUnitExecStart() failed: %v", err)
	}
	want := "/usr/local/bin/netsgo server --data-dir /var/lib/netsgo"
	if got != want {
		t.Fatalf("ReadUnitExecStart() = %q, want %q", got, want)
	}
}

func TestWriteUnitQuotesPathsWithSpaces(t *testing.T) {
	layout := NewLayout(RoleServer)
	root := t.TempDir()
	layout.UnitPath = filepath.Join(root, "netsgo-server.service")
	layout.BinaryPath = filepath.Join(root, "bin dir", "netsgo")
	layout.DataDir = filepath.Join(root, "data dir")
	layout.EnvPath = filepath.Join(root, "service env", "server.env")

	if err := WriteServerUnit(layout); err != nil {
		t.Fatalf("WriteServerUnit() failed: %v", err)
	}
	content, err := os.ReadFile(layout.UnitPath)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, `EnvironmentFile="`+layout.EnvPath+`"`) {
		t.Fatalf("unit should quote EnvironmentFile with spaces:\n%s", text)
	}
	wantExec := `ExecStart="` + layout.BinaryPath + `" server --data-dir "` + layout.DataDir + `"`
	if !strings.Contains(text, wantExec) {
		t.Fatalf("unit ExecStart missing quoted path %q:\n%s", wantExec, text)
	}

	info, err := ReadUnitInfo(layout.UnitPath)
	if err != nil {
		t.Fatalf("ReadUnitInfo() failed: %v", err)
	}
	if info.ExecStart != expectedExecStart(layout) {
		t.Fatalf("ReadUnitInfo ExecStart = %q, want %q", info.ExecStart, expectedExecStart(layout))
	}
}

func TestWriteUnitRejectsControlCharacters(t *testing.T) {
	layout := NewLayout(RoleClient)
	layout.UnitPath = filepath.Join(t.TempDir(), "netsgo-client.service")
	layout.DataDir = "/var/lib/netsgo\nExecStart=/bin/sh"

	if err := WriteClientUnit(layout); err == nil {
		t.Fatal("WriteClientUnit() error = nil, want control-character rejection")
	}
}
