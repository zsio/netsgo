package clientaddr

import "testing"

func TestNormalizeRuntimeMode(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Address
	}{
		{
			name: "bare host keeps runtime compatibility",
			raw:  "localhost:9527",
			want: Address{
				BaseURL:    "http://localhost:9527",
				UseTLS:     false,
				ControlURL: "ws://localhost:9527/ws/control",
				DataURL:    "ws://localhost:9527/ws/data",
			},
		},
		{
			name: "wss normalizes to https",
			raw:  "wss://NetsGo.Example.com/",
			want: Address{
				BaseURL:    "https://netsgo.example.com",
				UseTLS:     true,
				ControlURL: "wss://netsgo.example.com/ws/control",
				DataURL:    "wss://netsgo.example.com/ws/data",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.raw, ModeRuntime)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Normalize() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestNormalizeManagedInstallMode(t *testing.T) {
	tests := []struct {
		raw     string
		base    string
		useTLS  bool
		control string
		data    string
	}{
		{"http://netsgo.zsio.dev:9527", "http://netsgo.zsio.dev:9527", false, "ws://netsgo.zsio.dev:9527/ws/control", "ws://netsgo.zsio.dev:9527/ws/data"},
		{"https://netsgo.zsio.dev", "https://netsgo.zsio.dev", true, "wss://netsgo.zsio.dev/ws/control", "wss://netsgo.zsio.dev/ws/data"},
		{"ws://netsgo.zsio.dev:9527", "http://netsgo.zsio.dev:9527", false, "ws://netsgo.zsio.dev:9527/ws/control", "ws://netsgo.zsio.dev:9527/ws/data"},
		{"wss://netsgo.zsio.dev", "https://netsgo.zsio.dev", true, "wss://netsgo.zsio.dev/ws/control", "wss://netsgo.zsio.dev/ws/data"},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := Normalize(tt.raw, ModeManagedInstall)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if got.BaseURL != tt.base || got.UseTLS != tt.useTLS || got.ControlURL != tt.control || got.DataURL != tt.data {
				t.Fatalf("Normalize() = %#v", got)
			}
		})
	}
}

func TestNormalizeManagedInstallRejectsUnsafeForms(t *testing.T) {
	tests := []string{
		"netsgo.zsio.dev:9527",
		"http://netsgo.zsio.dev/path",
		"http://user@netsgo.zsio.dev",
		"http://netsgo.zsio.dev?x=1",
		"http://netsgo.zsio.dev#frag",
		"http://netsgo.zsio.dev:bad",
		"http://netsgo.zsio.dev:70000",
		"ftp://netsgo.zsio.dev",
		"http://netsgo.zsio.dev/a b",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if got, err := Normalize(raw, ModeManagedInstall); err == nil {
				t.Fatalf("Normalize(%q) = %#v, want error", raw, got)
			}
		})
	}
}
