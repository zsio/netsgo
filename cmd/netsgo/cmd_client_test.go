package main

import (
	"os"
	"strings"
	"testing"

	"netsgo/pkg/datadir"
)

func TestResolveClientDataDir(t *testing.T) {
	t.Setenv("NETSGO_DATA_DIR", "/env/data")

	if got := resolveClientDataDir("/flag/data", true); got != "/flag/data" {
		t.Fatalf("changed flag should win, got %q", got)
	}
	if got := resolveClientDataDir("/default/data", false); got != "/env/data" {
		t.Fatalf("env data dir should win when flag is unchanged, got %q", got)
	}

	t.Setenv("NETSGO_DATA_DIR", "")
	if got := resolveClientDataDir("/default/data", false); got != "/default/data" {
		t.Fatalf("default data dir should be used without env override, got %q", got)
	}
}

func TestClientCommandLogFormatFlag(t *testing.T) {
	flag := clientCmd.Flags().Lookup("log-format")
	if flag == nil {
		t.Fatal("client command should define --log-format")
	}
	if flag.DefValue != "text" {
		t.Fatalf("default log format = %q, want text", flag.DefValue)
	}
}

func TestClientCommandHelpIncludesImportantFlags(t *testing.T) {
	var output strings.Builder
	clientCmd.SetOut(&output)
	clientCmd.SetErr(&output)
	t.Cleanup(func() {
		clientCmd.SetOut(os.Stdout)
		clientCmd.SetErr(os.Stderr)
	})

	if err := clientCmd.Help(); err != nil {
		t.Fatalf("client help: %v", err)
	}

	help := output.String()
	for _, want := range []string{
		"--server",
		"--key",
		"--data-dir",
		"--log-format",
		"--tls-skip-verify",
		"--tls-fingerprint",
		datadir.DefaultDataDir(),
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("client help missing %q:\n%s", want, help)
		}
	}
}
