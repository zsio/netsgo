package main

import "testing"

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
