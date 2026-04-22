package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/updater"
	buildversion "netsgo/pkg/version"

	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade installed NetsGo binary with the currently running one",
	Long: `Upgrade replaces the system-installed NetsGo binary with the currently
running binary, then restarts all managed services.

Requires root privileges (auto-elevates via sudo).
Only works when services are installed via 'netsgo install'.
If the current binary is already the installed one, does nothing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Getuid() != 0 {
			return syscall.Exec("/usr/bin/sudo", append([]string{"sudo"}, os.Args...), os.Environ())
		}

		units := installedUnits()
		if len(units) == 0 {
			fmt.Fprintln(os.Stderr, "No installed services found.")
			fmt.Fprintln(os.Stderr, "Run 'netsgo install' first, or use 'netsgo update' to download.")
			os.Exit(1)
		}

		currentPath, err := svcmgr.CurrentBinaryPath()
		if err != nil {
			return fmt.Errorf("get current binary path: %w", err)
		}

		if currentPath == svcmgr.BinaryPath {
			fmt.Println("Current binary is already the installed binary.")
			fmt.Println("Nothing to upgrade.")
			return nil
		}

		installedVersion := getInstalledVersion()
		currentVersion := buildversion.Current

		if currentVersion == installedVersion && installedVersion != "" {
			fmt.Printf("Current version %s is the same as installed.\n", currentVersion)
			fmt.Println("Nothing to upgrade.")
			return nil
		}

		if isDevVersion(currentVersion) {
			fmt.Printf("Current version is '%s' (development build).\n", currentVersion)
			fmt.Println("Upgrading with a development build is not recommended.")
			return nil
		}

		if installedVersion != "" {
			cmp, _ := buildversion.ParseSemver(currentVersion)
			inst, _ := buildversion.ParseSemver(installedVersion)
			if cmp.Compare(inst) < 0 {
				fmt.Printf("Current %s is older than installed %s.\n", currentVersion, installedVersion)
				fmt.Println("This would downgrade. Aborting.")
				return nil
			}
		}

		fmt.Printf("Upgrading %s -> %s\n", installedVersion, currentVersion)
		fmt.Printf("Services to restart: %v\n", units)
		fmt.Println()

		result, err := updater.Upgrade(currentPath)
		if err != nil {
			return fmt.Errorf("upgrade failed: %w", err)
		}

		fmt.Println("Upgraded successfully.")
		fmt.Printf("Stopped: %v\n", result.Stopped)
		fmt.Printf("Started: %v\n", result.Started)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
}

func installedUnits() []string {
	var units []string
	if svcmgr.Detect(svcmgr.RoleServer) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleServer))
	}
	if svcmgr.Detect(svcmgr.RoleClient) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleClient))
	}
	return units
}

func getInstalledVersion() string {
	if _, err := os.Stat(svcmgr.BinaryPath); err != nil {
		return ""
	}
	out, err := exec.Command(svcmgr.BinaryPath, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func isDevVersion(v string) bool {
	_, err := buildversion.ParseSemver(v)
	return err != nil
}
