package clientaddr

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Mode controls how tolerant address normalization should be.
type Mode int

const (
	// ModeRuntime preserves client runtime compatibility: bare host inputs are
	// accepted and treated as http://.
	ModeRuntime Mode = iota
	// ModeManagedInstall is strict because it writes persistent service config.
	ModeManagedInstall
)

// Address is a normalized NetsGo server address and its derived endpoints.
type Address struct {
	BaseURL    string
	UseTLS     bool
	ControlURL string
	DataURL    string
}

// Normalize converts http(s)/ws(s) NetsGo server addresses into a stable
// base HTTP URL and derived control/data WebSocket endpoints.
func Normalize(raw string, mode Mode) (Address, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return Address{}, fmt.Errorf("server address cannot be empty")
	}
	if strings.ContainsAny(input, " \t\r\n") {
		return Address{}, fmt.Errorf("server address cannot contain whitespace")
	}
	if !strings.Contains(input, "://") {
		if mode == ModeManagedInstall {
			return Address{}, fmt.Errorf("server address must include a scheme: http://, https://, ws://, or wss://")
		}
		input = "http://" + input
	}

	parsed, err := url.Parse(input)
	if err != nil {
		return Address{}, fmt.Errorf("server address must be a valid URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https", "ws", "wss":
	default:
		return Address{}, fmt.Errorf("server address scheme must be http, https, ws, or wss")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return Address{}, fmt.Errorf("server address must include a host")
	}
	if port := parsed.Port(); port != "" {
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return Address{}, fmt.Errorf("server address port is invalid")
		}
	}
	if parsed.User != nil {
		return Address{}, fmt.Errorf("server address must not include user info")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return Address{}, fmt.Errorf("server address must not include a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return Address{}, fmt.Errorf("server address must not include a query or fragment")
	}

	baseScheme := scheme
	switch scheme {
	case "ws":
		baseScheme = "http"
	case "wss":
		baseScheme = "https"
	}

	host := normalizeHost(parsed)
	baseURL := baseScheme + "://" + host
	wsScheme := "ws"
	if baseScheme == "https" {
		wsScheme = "wss"
	}

	return Address{
		BaseURL:    baseURL,
		UseTLS:     baseScheme == "https",
		ControlURL: wsScheme + "://" + host + "/ws/control",
		DataURL:    wsScheme + "://" + host + "/ws/data",
	}, nil
}

func normalizeHost(parsed *url.URL) string {
	hostname := strings.ToLower(parsed.Hostname())
	if strings.Contains(hostname, ":") {
		hostname = "[" + strings.Trim(hostname, "[]") + "]"
	}
	if port := parsed.Port(); port != "" {
		return net.JoinHostPort(strings.Trim(hostname, "[]"), port)
	}
	return hostname
}
