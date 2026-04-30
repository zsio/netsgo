package main

import (
	"strings"
	"testing"
)

func TestMaskKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "empty", key: "", want: "(empty)"},
		{name: "short", key: "abcd", want: "****"},
		{name: "long", key: "sk-test-key", want: "*******-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskKey(tt.key); got != tt.want {
				t.Fatalf("maskKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestClientHelpPrefersHTTPServiceAddress(t *testing.T) {
	serverFlag := clientCmd.Flags().Lookup("server")
	if serverFlag == nil {
		t.Fatal("client --server flag not registered")
	}
	if serverFlag.DefValue != "http://localhost:9527" {
		t.Fatalf("client --server default = %q, want http://localhost:9527", serverFlag.DefValue)
	}
	if !strings.Contains(serverFlag.Usage, "http/https recommended") {
		t.Fatalf("client --server usage should recommend http/https, got %q", serverFlag.Usage)
	}
	if !strings.Contains(clientCmd.Long, "Service address formats:") {
		t.Fatalf("client long help should describe service addresses, got %q", clientCmd.Long)
	}
	if !strings.Contains(clientCmd.Long, "Backward-compatible WebSocket form") {
		t.Fatalf("client long help should keep ws/wss compatibility visible, got %q", clientCmd.Long)
	}
	if strings.Contains(clientCmd.Long, "Plain WebSocket") ||
		strings.Contains(clientCmd.Example, "--server ws://") ||
		strings.Contains(clientCmd.Example, "--server wss://") {
		t.Fatalf("client help should not present ws:// as the first-use path")
	}
}
