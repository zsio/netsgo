package svcmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"netsgo/pkg/fileutil"
)

func WriteServerUnit(spec ServiceSpec) error {
	return writeUnitFile(spec.UnitPath, renderUnit(spec, RoleServer))
}

func WriteClientUnit(spec ServiceSpec) error {
	return writeUnitFile(spec.UnitPath, renderUnit(spec, RoleClient))
}

func ReadUnitExecStart(unitPath string) (string, error) {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "ExecStart="); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("ExecStart not found in %s", unitPath)
}

func writeUnitFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, []byte(content), 0o644)
}

func expectedExecStart(role Role, binaryPath, dataDir string) string {
	return fmt.Sprintf("%s %s --data-dir %s", binaryPath, string(role), dataDir)
}

func renderUnit(spec ServiceSpec, role Role) string {
	description := "NetsGo Server"
	if role == RoleClient {
		description = "NetsGo Client"
	}

	return fmt.Sprintf(`[Unit]
Description=%s
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=%s
Group=%s
EnvironmentFile=%s
ExecStart=%s
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`, description, SystemUser, SystemGroup, spec.EnvPath, expectedExecStart(role, spec.BinaryPath, spec.DataDir))
}
