package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestConnectionLimitFlagOverridesEnvWithoutCrossCommandBinding(t *testing.T) {
	t.Setenv("NETSGO_MAX_CONNECTIONS", "20000")
	for _, name := range []string{"server", "client"} {
		cmd := &cobra.Command{Use: name}
		cmd.Flags().Int("max-connections", 10000, "")
		if got, err := connectionLimitForCommand(cmd); err != nil || got != 20000 {
			t.Fatalf("env %s: %d %v", name, got, err)
		}
		_ = cmd.Flags().Set("max-connections", "30000")
		if got, err := connectionLimitForCommand(cmd); err != nil || got != 30000 {
			t.Fatalf("flag %s: %d %v", name, got, err)
		}
		_ = cmd.Flags().Set("max-connections", "0")
		if _, err := connectionLimitForCommand(cmd); err == nil {
			t.Fatal("zero limit accepted")
		}
	}
}
