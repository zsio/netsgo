package server

import (
	"net/url"
	"os"
	"strconv"
	"strings"
)

type endpointURLs struct {
	Web     string
	Control string
	Data    string
}

func localEndpointURLs(port int, tlsEnabled bool) endpointURLs {
	httpScheme := "http"
	wsScheme := "ws"
	if tlsEnabled {
		httpScheme = "https"
		wsScheme = "wss"
	}
	host := "localhost"
	if port != 0 {
		host = host + ":" + strconv.Itoa(port)
	}
	return endpointURLs{
		Web:     httpScheme + "://" + host,
		Control: wsScheme + "://" + host + "/ws/control",
		Data:    wsScheme + "://" + host + "/ws/data",
	}
}

func configuredPublicServerAddr(cfg *ServerConfig) (string, bool) {
	if env := strings.TrimSpace(os.Getenv("NETSGO_SERVER_ADDR")); env != "" {
		if normalized, err := validateServerAddr(env); err == nil {
			return normalized, true
		}
	}
	if cfg == nil || strings.TrimSpace(cfg.ServerAddr) == "" {
		return "", false
	}
	normalized, err := validateServerAddr(cfg.ServerAddr)
	if err != nil {
		return "", false
	}
	return normalized, true
}

func publicEndpointURLs(base string) (endpointURLs, error) {
	normalized, err := validateServerAddr(base)
	if err != nil {
		return endpointURLs{}, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return endpointURLs{}, err
	}
	wsScheme := "ws"
	if parsed.Scheme == "https" {
		wsScheme = "wss"
	}
	return endpointURLs{
		Web:     normalized,
		Control: wsScheme + "://" + parsed.Host + "/ws/control",
		Data:    wsScheme + "://" + parsed.Host + "/ws/data",
	}, nil
}
