package main

import (
	"fmt"
	"os"
	"os/exec"

	"netsgo/internal/install"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:          "install",
	Short:        "Interactively install NetsGo as a systemd-managed service (Linux only)",
	SilenceUsage: true,
	Long: `Interactively install NetsGo server or client as a systemd-managed service.

Requires Linux with systemd, an interactive TTY, and root privileges.
The installer will auto-elevate via sudo if not already running as root.`,
	Example: `  sudo netsgo install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := rerunInstallWithSudoIfNeeded(os.Getuid(), exec.LookPath, execAsRoot); err != nil {
			return err
		}

		nonInteractiveClient, err := cmd.Flags().GetBool("client")
		if err != nil {
			return err
		}
		if nonInteractiveClient {
			server, err := cmd.Flags().GetString("server")
			if err != nil {
				return err
			}
			key, err := cmd.Flags().GetString("key")
			if err != nil {
				return err
			}
			return runInteractiveCommand(func() error {
				return install.InstallClientNonInteractive(server, key)
			})
		}
		return runInteractiveCommand(install.Run)
	},
}

func rerunInstallWithSudoIfNeeded(uid int, lookPath func(file string) (string, error), execFn func(argv0 string, argv []string, envv []string) error) error {
	if uid == 0 {
		return nil
	}

	sudoPath, err := lookPath("sudo")
	if err != nil {
		return fmt.Errorf("sudo is required to rerun this command as root, but it was not found in PATH: %w", err)
	}

	return execFn(sudoPath, append([]string{"sudo"}, os.Args...), os.Environ())
}

func init() {
	installCmd.Flags().Bool("client", false, "Install NetsGo client non-interactively")
	installCmd.Flags().String("server", "", "Client service address for non-interactive install")
	installCmd.Flags().String("key", "", "Client connection key for non-interactive install")
	rootCmd.AddCommand(installCmd)
}
