package svcmgr

import "testing"

func TestSpecPath(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want string
	}{
		{name: "server", role: RoleServer, want: "/etc/netsgo/services/server.json"},
		{name: "client", role: RoleClient, want: "/etc/netsgo/services/client.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SpecPath(tt.role); got != tt.want {
				t.Fatalf("SpecPath(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestUnitName(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want string
	}{
		{name: "server", role: RoleServer, want: "netsgo-server.service"},
		{name: "client", role: RoleClient, want: "netsgo-client.service"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnitName(tt.role); got != tt.want {
				t.Fatalf("UnitName(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestNewSpec(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want ServiceSpec
	}{
		{
			name: "server",
			role: RoleServer,
			want: ServiceSpec{
				Role:        RoleServer,
				ServiceName: "netsgo-server",
				BinaryPath:  "/usr/local/bin/netsgo",
				DataDir:     "/var/lib/netsgo",
				UnitPath:    "/etc/systemd/system/netsgo-server.service",
				EnvPath:     "/etc/netsgo/services/server.env",
				SpecPath:    "/etc/netsgo/services/server.json",
				RunAsUser:   "netsgo",
			},
		},
		{
			name: "client",
			role: RoleClient,
			want: ServiceSpec{
				Role:        RoleClient,
				ServiceName: "netsgo-client",
				BinaryPath:  "/usr/local/bin/netsgo",
				DataDir:     "/var/lib/netsgo",
				UnitPath:    "/etc/systemd/system/netsgo-client.service",
				EnvPath:     "/etc/netsgo/services/client.env",
				SpecPath:    "/etc/netsgo/services/client.json",
				RunAsUser:   "netsgo",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewSpec(tt.role)
			if got != tt.want {
				t.Fatalf("NewSpec(%q) = %#v, want %#v", tt.role, got, tt.want)
			}
		})
	}
}
