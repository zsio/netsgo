package main

import (
	"netsgo/internal/install"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Interactively install NetsGo as a systemd-managed service (Linux only)",
	Long: `Interactively install NetsGo server or client as a systemd-managed service.

Requires Linux with systemd, an interactive TTY, and root privileges.
The installer will auto-elevate via sudo if not already running as root.`,
	Example: `  sudo netsgo install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return install.Run()
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
}
