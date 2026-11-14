package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"netsgo/internal/client"
)

type sidecarControlCommand struct {
	Cmd string `json:"cmd"`
}

// runSidecarStdinControl reads line-delimited JSON commands from r when
// sidecar control mode is enabled, and invokes onStop when either a
// {"cmd":"shutdown"} request arrives or stdin reaches EOF (parent process
// exited or crashed).
func runSidecarStdinControl(r io.Reader, c *client.Client, logger *client.EventLogger, onStop func(reason string)) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var cmd sidecarControlCommand
		if err := json.Unmarshal([]byte(line), &cmd); err != nil {
			if logger != nil {
				logger.Warn("client.control_invalid", "Ignored malformed sidecar control line", map[string]any{"error": err.Error()})
			}
			continue
		}
		switch cmd.Cmd {
		case "snapshot":
			if logger != nil {
				logger.Info("client.runtime_snapshot", "Client runtime snapshot requested", map[string]any{
					"reason":   "request",
					"snapshot": c.RuntimeSnapshot(),
				})
			}
		case "shutdown":
			if onStop != nil {
				onStop("control_shutdown")
			}
			return
		case "ping":
			if logger != nil {
				logger.Info("client.control_pong", "Sidecar control pong", nil)
			}
		}
	}

	if onStop != nil {
		onStop("parent_stdin_closed")
	}
}
