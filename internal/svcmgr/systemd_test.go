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
		t.Skip("linux 平台下由真实实现覆盖")
	}

	if err := DaemonReload(); err != ErrUnsupportedPlatform {
		t.Fatalf("DaemonReload() 应返回 ErrUnsupportedPlatform，得到 %v", err)
	}
	if err := EnableAndStart("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("EnableAndStart() 应返回 ErrUnsupportedPlatform，得到 %v", err)
	}
	if err := DisableAndStop("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("DisableAndStop() 应返回 ErrUnsupportedPlatform，得到 %v", err)
	}
	if _, err := IsActive("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("IsActive() 应返回 ErrUnsupportedPlatform，得到 %v", err)
	}
	if _, err := IsEnabled("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("IsEnabled() 应返回 ErrUnsupportedPlatform，得到 %v", err)
	}
	if _, err := Status("netsgo-server.service"); err != ErrUnsupportedPlatform {
		t.Fatalf("Status() 应返回 ErrUnsupportedPlatform，得到 %v", err)
	}
}
