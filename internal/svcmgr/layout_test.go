package svcmgr

import (
	"path/filepath"
	"testing"
)

func TestNewLayout(t *testing.T) {
	server := NewLayout(RoleServer)
	if server.ServiceName != "netsgo-server" {
		t.Fatalf("server.ServiceName = %q", server.ServiceName)
	}
	if server.UnitPath != filepath.Join(SystemdDir, "netsgo-server.service") {
		t.Fatalf("server.UnitPath = %q", server.UnitPath)
	}
	if server.EnvPath != filepath.Join(ServicesDir, "server.env") {
		t.Fatalf("server.EnvPath = %q", server.EnvPath)
	}
	if server.RuntimeDir != filepath.Join(ManagedDataDir, "server") {
		t.Fatalf("server.RuntimeDir = %q", server.RuntimeDir)
	}

	client := NewLayout(RoleClient)
	if client.ServiceName != "netsgo-client" {
		t.Fatalf("client.ServiceName = %q", client.ServiceName)
	}
	if client.RuntimeDir != filepath.Join(ManagedDataDir, "client") {
		t.Fatalf("client.RuntimeDir = %q", client.RuntimeDir)
	}
}
