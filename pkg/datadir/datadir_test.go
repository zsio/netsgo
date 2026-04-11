package datadir

import (
	"path/filepath"
	"testing"
)

func TestDefaultDataDir_DirectRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("INVOCATION_ID", "")

	want := filepath.Join(home, ".local", "state", "netsgo")
	if got := DefaultDataDir(); got != want {
		t.Fatalf("DefaultDataDir() = %q, want %q", got, want)
	}
}

func TestDefaultDataDir_Systemd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("INVOCATION_ID", "systemd-invocation-id")

	if got := DefaultDataDir(); got != "/var/lib/netsgo" {
		t.Fatalf("DefaultDataDir() = %q, want %q", got, "/var/lib/netsgo")
	}
}
