package svcmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"netsgo/pkg/fileutil"
)

func WriteServerUnit(layout ServiceLayout) error {
	content, err := renderUnit(layout)
	if err != nil {
		return err
	}
	return writeUnitFile(layout.UnitPath, content)
}

func WriteClientUnit(layout ServiceLayout) error {
	content, err := renderUnit(layout)
	if err != nil {
		return err
	}
	return writeUnitFile(layout.UnitPath, content)
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
			info.EnvironmentFile = unquoteSystemdArg(strings.TrimPrefix(line, "EnvironmentFile="))
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
	return fmt.Sprintf("%s %s --data-dir %s", systemdQuoteArg(layout.BinaryPath), string(layout.Role), systemdQuoteArg(layout.DataDir))
}

func renderUnit(layout ServiceLayout) (string, error) {
	description := "NetsGo Server"
	if layout.Role == RoleClient {
		description = "NetsGo Client"
	}

	for name, value := range map[string]string{
		"binary path":      layout.BinaryPath,
		"data dir":         layout.DataDir,
		"env path":         layout.EnvPath,
		"run-as user":      layout.RunAsUser,
		"run-as group":     layout.RunAsGroup,
		"service role":     string(layout.Role),
		"unit description": description,
	} {
		if err := rejectSystemdControlChars(name, value); err != nil {
			return "", err
		}
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
`, description, layout.RunAsUser, layout.RunAsGroup, systemdQuoteArg(layout.EnvPath), expectedExecStart(layout)), nil
}

func rejectSystemdControlChars(name, value string) error {
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("invalid systemd %s: control characters are not allowed", name)
		}
	}
	return nil
}

func systemdQuoteArg(value string) string {
	if value == "" {
		return `""`
	}
	needsQuote := false
	for _, r := range value {
		if r == ' ' || r == '\t' || r == '"' || r == '\'' || r == '\\' || r == ';' || r == '#' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return value
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`)
	return `"` + replacer.Replace(value) + `"`
}

func unquoteSystemdArg(value string) string {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}
	inner := value[1 : len(value)-1]
	var builder strings.Builder
	escaped := false
	for _, r := range inner {
		if escaped {
			builder.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		builder.WriteRune(r)
	}
	if escaped {
		builder.WriteByte('\\')
	}
	return builder.String()
}
