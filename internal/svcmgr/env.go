package svcmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"netsgo/pkg/fileutil"
)

type ServerEnv struct {
	Port                               int
	TLSMode                            string
	TLSCert                            string
	TLSKey                             string
	TrustedProxies                     string
	ServerAddr                         string
	AllowLoopbackManagementHost        bool
	AllowLoopbackManagementHostDefined bool
}

type ClientEnv struct {
	Server         string
	Key            string
	TLSSkipVerify  bool
	TLSFingerprint string
}

func WriteServerEnv(layout ServiceLayout, env ServerEnv) error {
	values := map[string]string{}
	if env.Port != 0 {
		values["NETSGO_PORT"] = strconv.Itoa(env.Port)
	}
	if env.TLSMode != "" {
		values["NETSGO_TLS_MODE"] = env.TLSMode
	}
	if env.TLSCert != "" {
		values["NETSGO_TLS_CERT"] = env.TLSCert
	}
	if env.TLSKey != "" {
		values["NETSGO_TLS_KEY"] = env.TLSKey
	}
	if env.TrustedProxies != "" {
		values["NETSGO_TRUSTED_PROXIES"] = env.TrustedProxies
	}
	if env.ServerAddr != "" {
		values["NETSGO_SERVER_ADDR"] = env.ServerAddr
	}
	if env.AllowLoopbackManagementHostDefined {
		values["NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST"] = strconv.FormatBool(env.AllowLoopbackManagementHost)
	} else if env.AllowLoopbackManagementHost {
		values["NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST"] = "true"
	}
	return writeEnvFile(layout.EnvPath, values)
}

func WriteClientEnv(layout ServiceLayout, env ClientEnv) error {
	values := map[string]string{}
	if env.Server != "" {
		values["NETSGO_SERVER"] = env.Server
	}
	if env.Key != "" {
		values["NETSGO_KEY"] = env.Key
	}
	if env.TLSSkipVerify {
		values["NETSGO_TLS_SKIP_VERIFY"] = "true"
	}
	if env.TLSFingerprint != "" {
		values["NETSGO_TLS_FINGERPRINT"] = env.TLSFingerprint
	}
	return writeEnvFile(layout.EnvPath, values)
}

func ReadServerEnv(layout ServiceLayout) (ServerEnv, error) {
	values, err := readEnvFile(layout.EnvPath)
	if err != nil {
		return ServerEnv{}, err
	}

	var env ServerEnv
	if raw := values["NETSGO_PORT"]; raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return ServerEnv{}, err
		}
		env.Port = port
	}
	env.TLSMode = values["NETSGO_TLS_MODE"]
	env.TLSCert = values["NETSGO_TLS_CERT"]
	env.TLSKey = values["NETSGO_TLS_KEY"]
	env.TrustedProxies = values["NETSGO_TRUSTED_PROXIES"]
	env.ServerAddr = values["NETSGO_SERVER_ADDR"]
	if raw, ok := values["NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST"]; ok {
		env.AllowLoopbackManagementHost = raw == "true"
		env.AllowLoopbackManagementHostDefined = true
	} else {
		env.AllowLoopbackManagementHost = true
	}
	return env, nil
}

func ReadClientEnv(layout ServiceLayout) (ClientEnv, error) {
	values, err := readEnvFile(layout.EnvPath)
	if err != nil {
		return ClientEnv{}, err
	}
	return ClientEnv{
		Server:         values["NETSGO_SERVER"],
		Key:            values["NETSGO_KEY"],
		TLSSkipVerify:  values["NETSGO_TLS_SKIP_VERIFY"] == "true",
		TLSFingerprint: values["NETSGO_TLS_FINGERPRINT"],
	}, nil
}

func writeEnvFile(path string, values map[string]string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}

	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value == "" {
			continue
		}
		if err := validateEnvEntry(key, value); err != nil {
			return err
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(values[key])
		builder.WriteByte('\n')
	}

	if err := fileutil.AtomicWriteFile(path, []byte(builder.String()), 0o640); err != nil {
		return err
	}
	return repairEnvFileOwnership(path)
}

func RepairEnvFileOwnership(layout ServiceLayout) error {
	return repairEnvFileOwnership(layout.EnvPath)
}

func EnableServerLoopbackManagementHost(layout ServiceLayout) error {
	if err := setEnvFileValue(layout.EnvPath, "NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST", "true"); err != nil {
		return err
	}
	return repairEnvFileOwnership(layout.EnvPath)
}

func setEnvFileValue(path, key, value string) error {
	if err := validateEnvEntry(key, value); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	updated := replaceOrAppendEnvValue(string(data), key, value)
	if updated == string(data) {
		return nil
	}
	return fileutil.AtomicWriteFile(path, []byte(updated), 0o640)
}

func replaceOrAppendEnvValue(content, key, value string) string {
	lines := strings.Split(content, "\n")
	hasTrailingNewline := strings.HasSuffix(content, "\n")
	limit := len(lines)
	if hasTrailingNewline {
		limit--
	}

	found := false
	for i := 0; i < limit; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		keyPart, _, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(keyPart) != key {
			continue
		}
		lines[i] = key + "=" + value
		found = true
	}
	if found {
		return strings.Join(lines, "\n")
	}

	if content != "" && !hasTrailingNewline {
		content += "\n"
	}
	return content + key + "=" + value + "\n"
}

func validateEnvEntry(key, value string) error {
	if strings.HasPrefix(key, "NETSGO_INIT_") {
		return fmt.Errorf("forbidden env key: %s", key)
	}
	if strings.TrimSpace(key) != key || key == "" {
		return fmt.Errorf("invalid env key: %q", key)
	}
	for _, r := range key {
		if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("invalid env key: %q", key)
		}
	}
	for _, r := range value {
		if r == '\n' || r == '\r' || r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("invalid env value for %s: control characters are not allowed", key)
		}
	}
	return nil
}

func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid env line: %q", line)
		}
		values[key] = value
	}
	return values, nil
}
