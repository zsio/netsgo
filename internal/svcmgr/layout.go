package svcmgr

import "path/filepath"

type Role string

const (
	RoleServer Role = "server"
	RoleClient Role = "client"
)

const (
	ServicesDir    = "/etc/netsgo/services"
	SystemdDir     = "/etc/systemd/system"
	BinaryPath     = "/usr/local/bin/netsgo"
	ManagedDataDir = "/var/lib/netsgo"
	SystemUser     = "netsgo"
	SystemGroup    = "netsgo"
)

type ServiceLayout struct {
	Role        Role
	ServiceName string
	BinaryPath  string
	DataDir     string
	RuntimeDir  string
	UnitPath    string
	EnvPath     string
	RunAsUser   string
	RunAsGroup  string
}

func NewLayout(role Role) ServiceLayout {
	return ServiceLayout{
		Role:        role,
		ServiceName: "netsgo-" + string(role),
		BinaryPath:  BinaryPath,
		DataDir:     ManagedDataDir,
		RuntimeDir:  filepath.Join(ManagedDataDir, string(role)),
		UnitPath:    filepath.Join(SystemdDir, UnitName(role)),
		EnvPath:     filepath.Join(ServicesDir, string(role)+".env"),
		RunAsUser:   SystemUser,
		RunAsGroup:  SystemGroup,
	}
}

func UnitName(role Role) string {
	return "netsgo-" + string(role) + ".service"
}
