package svcmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"netsgo/pkg/fileutil"
)

type ServerEnv struct {
	Port                        int
	TLSMode                     string
	TLSCert                     string
	TLSKey                      string
	TrustedProxies              string
	ServerAddr                  string
	AllowLoopbackManagementHost bool
}

type ClientEnv struct {
	Server         string
	Key            string
	TLSSkipVerify  bool
	TLSFingerprint string
}

func WriteServerEnv(spec ServiceSpec, env ServerEnv) error {
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
	if env.AllowLoopbackManagementHost {
		values["NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST"] = "true"
	}
	return writeEnvFile(spec.EnvPath, values)
}

func WriteClientEnv(spec ServiceSpec, env ClientEnv) error {
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
	return writeEnvFile(spec.EnvPath, values)
}

func ReadServerEnv(spec ServiceSpec) (ServerEnv, error) {
	values, err := readEnvFile(spec.EnvPath)
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
	env.AllowLoopbackManagementHost = values["NETSGO_ALLOW_LOOPBACK_MANAGEMENT_HOST"] == "true"
	return env, nil
}

func ReadClientEnv(spec ServiceSpec) (ClientEnv, error) {
	values, err := readEnvFile(spec.EnvPath)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value == "" {
			continue
		}
		if strings.HasPrefix(key, "NETSGO_INIT_") {
			return fmt.Errorf("forbidden env key: %s", key)
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

	return fileutil.AtomicWriteFile(path, []byte(builder.String()), 0o600)
}

func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
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
