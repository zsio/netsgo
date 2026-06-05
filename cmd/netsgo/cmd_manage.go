package main

import (
	"fmt"
	"strings"

	"netsgo/internal/manage"
	"netsgo/internal/svcmgr"

	"github.com/spf13/cobra"
)

var manageCmd = &cobra.Command{
	Use:          "manage",
	Short:        "Manage installed NetsGo systemd services (Linux only)",
	SilenceUsage: true,
	Long: `Manage installed NetsGo server and client systemd services.

	Supports status inspection, start/stop/restart, installation,
	log streaming, and uninstallation.
	Requires Linux with systemd, an interactive TTY, and root privileges.
	The manager will auto-elevate via sudo if not already running as root.`,
	Example: `  sudo netsgo manage`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runInteractiveCommand(manage.Run)
	},
}

var resetAdminPasswordCmd = &cobra.Command{
	Use:          "reset-admin-password",
	Short:        "Reset a NetsGo server admin password offline",
	SilenceUsage: true,
	Long: `Reset a NetsGo server admin password directly in the server SQLite data.

This command is intended for recovery and container/script usage. It requires an
existing initialized server data directory and an existing admin username.`,
	Example: `  netsgo manage reset-admin-password --username admin --password NewPass123
  netsgo manage reset-admin-password --data-dir /var/lib/netsgo --username admin --password NewPass123`,
	RunE: runResetAdminPasswordCommand,
}

func addResetAdminPasswordFlags(cmd *cobra.Command) {
	cmd.Flags().String("data-dir", svcmgr.ManagedDataDir, "Data root directory containing server/netsgo.db")
	cmd.Flags().String("username", "", "Existing admin username to reset")
	cmd.Flags().String("password", "", "New admin password")
	if err := cmd.MarkFlagRequired("username"); err != nil {
		panic(err)
	}
	if err := cmd.MarkFlagRequired("password"); err != nil {
		panic(err)
	}
}

func runResetAdminPasswordCommand(cmd *cobra.Command, args []string) error {
	dataDir, err := cmd.Flags().GetString("data-dir")
	if err != nil {
		return err
	}
	username, err := cmd.Flags().GetString("username")
	if err != nil {
		return err
	}
	password, err := cmd.Flags().GetString("password")
	if err != nil {
		return err
	}
	username = strings.TrimSpace(username)
	if err := manage.ResetAdminPassword(dataDir, username, password); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "admin password reset for %q\n", username)
	return err
}

func init() {
	addResetAdminPasswordFlags(resetAdminPasswordCmd)
	manageCmd.AddCommand(resetAdminPasswordCmd)
	rootCmd.AddCommand(manageCmd)
}
