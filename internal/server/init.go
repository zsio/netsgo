package server

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type InitParams struct {
	AdminUsername string
	AdminPassword string
	ServerAddr    string
	AllowedPorts  string
}

func (p InitParams) IsComplete() bool {
	return p.AdminUsername != "" &&
		p.AdminPassword != "" &&
		p.ServerAddr != "" &&
		p.AllowedPorts != ""
}

func ApplyInit(dataDir string, params InitParams) error {
	adminStore, err := NewAdminStore(filepath.Join(dataDir, "server", serverDBFileName))
	if err != nil {
		return err
	}
	defer func() { _ = adminStore.Close() }()
	if adminStore.IsInitialized() {
		return nil
	}

	serverAddr, err := validateServerAddr(params.ServerAddr)
	if err != nil {
		return err
	}

	allowedPorts, err := parseAllowedPorts(params.AllowedPorts)
	if err != nil {
		return err
	}

	return adminStore.Initialize(params.AdminUsername, params.AdminPassword, serverAddr, allowedPorts)
}

func LoadRecoverableInitParams(dataDir string) (InitParams, error) {
	adminStore, err := NewAdminStore(filepath.Join(dataDir, "server", serverDBFileName))
	if err != nil {
		return InitParams{}, err
	}
	defer func() { _ = adminStore.Close() }()
	if !adminStore.IsInitialized() {
		return InitParams{}, fmt.Errorf("server historical data has not been initialized")
	}

	config := adminStore.GetServerConfig()
	allowedPorts := formatAllowedPorts(config.AllowedPorts)
	if strings.TrimSpace(config.ServerAddr) == "" || allowedPorts == "" {
		return InitParams{}, fmt.Errorf("server historical data is incomplete")
	}

	return InitParams{
		ServerAddr:   config.ServerAddr,
		AllowedPorts: allowedPorts,
	}, nil
}

func parseAllowedPorts(raw string) ([]PortRange, error) {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	ranges := make([]PortRange, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid allowed port %q", part)
			}
			end, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid allowed port %q", part)
			}
			if start < 1 || end < 1 || start > 65535 || end > 65535 || start > end {
				return nil, fmt.Errorf("invalid allowed port %q", part)
			}
			ranges = append(ranges, PortRange{Start: start, End: end})
			continue
		}

		port, err := strconv.Atoi(part)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid allowed port %q", part)
		}
		ranges = append(ranges, PortRange{Start: port, End: port})
	}

	if len(ranges) == 0 {
		return nil, fmt.Errorf("allowed ports cannot be empty")
	}

	return ranges, nil
}

func formatAllowedPorts(ranges []PortRange) string {
	parts := make([]string, 0, len(ranges))
	for _, pr := range ranges {
		if pr.Start == pr.End {
			parts = append(parts, strconv.Itoa(pr.Start))
			continue
		}
		parts = append(parts, strconv.Itoa(pr.Start)+"-"+strconv.Itoa(pr.End))
	}
	return strings.Join(parts, ",")
}
