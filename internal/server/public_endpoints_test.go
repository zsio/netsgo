package server

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestConfiguredPublicServerAddr(t *testing.T) {
	t.Run("none without env or persisted config", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "")
		if got, ok := configuredPublicServerAddr(&ServerConfig{}); ok {
			t.Fatalf("configuredPublicServerAddr() = %q, true; want none", got)
		}
	})

	t.Run("env overrides persisted config", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "https://Env.Example.com:443")
		got, ok := configuredPublicServerAddr(&ServerConfig{ServerAddr: "http://persisted.example.com:9527"})
		if !ok {
			t.Fatal("expected configured public env addr")
		}
		if got != "https://env.example.com" {
			t.Fatalf("configuredPublicServerAddr() = %q, want https://env.example.com", got)
		}
	})

	t.Run("invalid env does not lock out persisted config", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "ws://invalid.example.com")
		got, ok := configuredPublicServerAddr(&ServerConfig{ServerAddr: "http://persisted.example.com:9527"})
		if !ok {
			t.Fatal("expected configured persisted addr")
		}
		if got != "http://persisted.example.com:9527" {
			t.Fatalf("configuredPublicServerAddr() = %q", got)
		}
	})
}

func TestPublicEndpointURLs(t *testing.T) {
	tests := []struct {
		name string
		base string
		want endpointURLs
	}{
		{
			name: "http",
			base: "http://netsgo.zsio.dev:9527",
			want: endpointURLs{
				Web:     "http://netsgo.zsio.dev:9527",
				Control: "ws://netsgo.zsio.dev:9527/ws/control",
				Data:    "ws://netsgo.zsio.dev:9527/ws/data",
			},
		},
		{
			name: "https",
			base: "https://netsgo.zsio.dev",
			want: endpointURLs{
				Web:     "https://netsgo.zsio.dev",
				Control: "wss://netsgo.zsio.dev/ws/control",
				Data:    "wss://netsgo.zsio.dev/ws/data",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := publicEndpointURLs(tt.base)
			if err != nil {
				t.Fatalf("publicEndpointURLs() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("publicEndpointURLs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestLogServerEndpoints(t *testing.T) {
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})
	log.SetFlags(0)

	t.Run("local only without configured public address", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "")
		var buf bytes.Buffer
		log.SetOutput(&buf)

		logServerEndpoints(9527, false, true, nil)

		output := buf.String()
		if !strings.Contains(output, "Local Web UI: http://localhost:9527") {
			t.Fatalf("local web UI log missing: %s", output)
		}
		if !strings.Contains(output, "Local control channel: ws://localhost:9527/ws/control") {
			t.Fatalf("local control log missing: %s", output)
		}
		if strings.Contains(output, "Configured public") {
			t.Fatalf("configured public logs should not be emitted without explicit config: %s", output)
		}
	})

	t.Run("configured public uses env scheme and label", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "https://Panel.Example.com:443")
		var buf bytes.Buffer
		log.SetOutput(&buf)

		logServerEndpoints(9527, false, true, &ServerConfig{ServerAddr: "http://persisted.example.com:9527"})

		output := buf.String()
		if !strings.Contains(output, "Local control channel: ws://localhost:9527/ws/control") {
			t.Fatalf("local endpoint should still use local TLS mode: %s", output)
		}
		if !strings.Contains(output, "Configured public Web UI: https://panel.example.com") {
			t.Fatalf("configured public web log missing: %s", output)
		}
		if !strings.Contains(output, "Configured public control channel: wss://panel.example.com/ws/control") {
			t.Fatalf("configured public control should derive wss from https env: %s", output)
		}
		if strings.Contains(output, "Configured public control channel: ws://localhost") {
			t.Fatalf("configured public logs must not use listen fallback: %s", output)
		}
	})

	t.Run("dev mode skips web UI rows", func(t *testing.T) {
		t.Setenv("NETSGO_SERVER_ADDR", "http://netsgo.zsio.dev:9527")
		var buf bytes.Buffer
		log.SetOutput(&buf)

		logServerEndpoints(9527, false, false, nil)

		output := buf.String()
		if strings.Contains(output, "Web UI") {
			t.Fatalf("web UI logs should be skipped when web assets are unavailable: %s", output)
		}
		if !strings.Contains(output, "Configured public control channel: ws://netsgo.zsio.dev:9527/ws/control") {
			t.Fatalf("configured public control log missing: %s", output)
		}
	})
}
