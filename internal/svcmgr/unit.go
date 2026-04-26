package svcmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"netsgo/pkg/fileutil"
)

func WriteServerUnit(layout ServiceLayout) error {
	return writeUnitFile(layout.UnitPath, renderUnit(layout))
}

func WriteClientUnit(layout ServiceLayout) error {
	return writeUnitFile(layout.UnitPath, renderUnit(layout))
}

type UnitInfo struct {
	User            string
	Group           string
	EnvironmentFile string
	ExecStart       string
}

func ReadUnitInfo(unitPath string) (UnitInfo, error) {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return UnitInfo{}, err
	}
	var info UnitInfo
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "User="):
			info.User = strings.TrimPrefix(line, "User=")
		case strings.HasPrefix(line, "Group="):
			info.Group = strings.TrimPrefix(line, "Group=")
		case strings.HasPrefix(line, "EnvironmentFile="):
			info.EnvironmentFile = strings.TrimPrefix(line, "EnvironmentFile=")
		case strings.HasPrefix(line, "ExecStart="):
			info.ExecStart = strings.TrimPrefix(line, "ExecStart=")
		}
	}
	if info.ExecStart == "" {
		return UnitInfo{}, fmt.Errorf("ExecStart not found in %s", unitPath)
	}
	return info, nil
}

func ReadUnitExecStart(unitPath string) (string, error) {
	info, err := ReadUnitInfo(unitPath)
	if err != nil {
		return "", err
	}
	return info.ExecStart, nil
}

func writeUnitFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, []byte(content), 0o644)
}

func expectedExecStart(layout ServiceLayout) string {
	return fmt.Sprintf("%s %s --data-dir %s", layout.BinaryPath, string(layout.Role), layout.DataDir)
}

func renderUnit(layout ServiceLayout) string {
	description := "NetsGo Server"
	if layout.Role == RoleClient {
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
`, description, layout.RunAsUser, layout.RunAsGroup, layout.EnvPath, expectedExecStart(layout))
}
