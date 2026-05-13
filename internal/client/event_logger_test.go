package client

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEventLoggerJSONWritesJSONLine(t *testing.T) {
	var buf bytes.Buffer
	logger := NewEventLogger(LogFormatJSON, &buf)

	logger.Info("client.connected", "Connected to server", map[string]any{"server": "ws://example.test"})

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected a JSON line")
	}
	var event ClientEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("event should be valid JSON: %v\n%s", err, line)
	}
	if event.Level != "info" || event.Event != "client.connected" || event.Message != "Connected to server" {
		t.Fatalf("unexpected event payload: %+v", event)
	}
	if event.Fields["server"] != "ws://example.test" {
		t.Fatalf("unexpected fields: %+v", event.Fields)
	}
}

func TestEventLoggerInvalidFormatDefaultsToText(t *testing.T) {
	logger := NewEventLogger("bad", nil)
	if logger.Format() != LogFormatText {
		t.Fatalf("invalid format should default to text, got %q", logger.Format())
	}
}
