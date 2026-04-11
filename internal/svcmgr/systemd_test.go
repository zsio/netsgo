package svcmgr

import (
	"reflect"
	"runtime"
	"testing"
)

func TestJournalArgs(t *testing.T) {
	got := JournalArgs("netsgo-server.service", 100)
	want := []string{"journalctl", "-u", "netsgo-server.service", "-n", "100", "-f"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JournalArgs() = %#v, want %#v", got, want)
	}
}

func TestSystemdStub(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("covered by the real implementation on Linux")
	}

	if err := DaemonReload(); err != ErrUnsupportedPlatform {
		t.Fatalf("DaemonReload() should return ErrUnsupportedPlatform, got %v", err)
	}
	if err := EnableAndStart("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("EnableAndStart() should return ErrUnsupportedPlatform, got %v", err)
	}
	if err := DisableAndStop("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("DisableAndStop() should return ErrUnsupportedPlatform, got %v", err)
	}
	if _, err := IsActive("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("IsActive() should return ErrUnsupportedPlatform, got %v", err)
	}
	if _, err := IsEnabled("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("IsEnabled() should return ErrUnsupportedPlatform, got %v", err)
	}
	if _, err := Status("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("Status() should return ErrUnsupportedPlatform, got %v", err)
	}
}
