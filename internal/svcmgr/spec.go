package svcmgr

import (
	"encoding/json"
	"os"
	"path/filepath"

	"netsgo/pkg/fileutil"
)

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

type ServiceSpec struct {
	Role        Role   `json:"role"`
	ServiceName string `json:"service_name"`
	BinaryPath  string `json:"binary_path"`
	DataDir     string `json:"data_dir"`
	UnitPath    string `json:"unit_path"`
	EnvPath     string `json:"env_path"`
	SpecPath    string `json:"spec_path"`
	RunAsUser   string `json:"run_as_user"`
	InstalledAt string `json:"installed_at"`
	ListenPort  int    `json:"listen_port,omitempty"`
	TLSMode     string `json:"tls_mode,omitempty"`
	ServerURL   string `json:"server_url,omitempty"`
}

func SpecPath(role Role) string {
	return filepath.Join(ServicesDir, string(role)+".json")
}

func UnitName(role Role) string {
	return "netsgo-" + string(role) + ".service"
}

func NewSpec(role Role) ServiceSpec {
	return ServiceSpec{
		Role:        role,
		ServiceName: "netsgo-" + string(role),
		BinaryPath:  BinaryPath,
		DataDir:     ManagedDataDir,
		UnitPath:    filepath.Join(SystemdDir, UnitName(role)),
		EnvPath:     filepath.Join(ServicesDir, string(role)+".env"),
		SpecPath:    SpecPath(role),
		RunAsUser:   SystemUser,
	}
}

func WriteServerSpec(spec ServiceSpec) error {
	return writeSpec(spec.SpecPath, spec)
}

func ReadServerSpec(path string) (ServiceSpec, error) {
	return readSpec(path)
}

func WriteClientSpec(spec ServiceSpec) error {
	return writeSpec(spec.SpecPath, spec)
}

func ReadClientSpec(path string) (ServiceSpec, error) {
	return readSpec(path)
}

func writeSpec(path string, spec ServiceSpec) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fileutil.AtomicWriteFile(path, data, 0o600)
}

func readSpec(path string) (ServiceSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ServiceSpec{}, err
	}
	var spec ServiceSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return ServiceSpec{}, err
	}
	return spec, nil
}
