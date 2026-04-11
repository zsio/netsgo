package main

import (
	"netsgo/internal/manage"

	"github.com/spf13/cobra"
)

var manageCmd = &cobra.Command{
	Use:   "manage",
	Short: "Manage installed NetsGo systemd services (Linux only)",
	Long: `Manage installed NetsGo server and client systemd services.

Supports status inspection, start/stop/restart, log streaming, and uninstallation.
Requires Linux with systemd, an interactive TTY, and root privileges.
The manager will auto-elevate via sudo if not already running as root.`,
	Example: `  sudo netsgo manage`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return manage.Run()
	},
}

func init() {
	rootCmd.AddCommand(manageCmd)
}
