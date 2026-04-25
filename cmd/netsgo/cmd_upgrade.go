package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/updater"
	buildversion "netsgo/pkg/version"

	"github.com/spf13/cobra"
)

var forceUpgrade bool
var getInstalledVersionFunc = getInstalledVersion

type upgradeCommandDeps struct {
	installedUnits   func() []string
	currentBinaryPath func() (string, error)
	installedVersion func() (string, error)
	confirm          func() bool
	applyUpgrade     func(currentPath, installedVersion, targetVersion string) (*updater.Result, error)
	currentVersion   string
	stdout           io.Writer
	stderr           io.Writer
}

func defaultUpgradeCommandDeps() upgradeCommandDeps {
	return upgradeCommandDeps{
		installedUnits:   installedUnits,
		currentBinaryPath: svcmgr.CurrentBinaryPath,
		installedVersion: getInstalledVersionFunc,
		confirm:          readConfirmation,
		applyUpgrade:     updater.Upgrade,
		currentVersion:   buildversion.Current,
		stdout:           os.Stdout,
		stderr:           os.Stderr,
	}
}

func runUpgradeCommand(force bool, deps upgradeCommandDeps) error {
	units := deps.installedUnits()
	if len(units) == 0 {
		fmt.Fprintln(deps.stderr, "No installed services found.")
		fmt.Fprintln(deps.stderr, "Run 'netsgo install' first.")
		fmt.Fprintln(deps.stderr, "To download a release binary manually, visit: https://github.com/zsio/netsgo/releases")
		os.Exit(1)
	}

	currentPath, err := deps.currentBinaryPath()
	if err != nil {
		return fmt.Errorf("get current binary path: %w", err)
	}

	if currentPath == svcmgr.BinaryPath {
		fmt.Fprintln(deps.stdout, "Current binary is already the installed binary.")
		fmt.Fprintln(deps.stdout, "Nothing to upgrade.")
		return nil
	}

	installedVersion, installedVersionErr := deps.installedVersion()
	currentVersion := deps.currentVersion
	normalizedCurrentVersion, currentErr := buildversion.NormalizeVersionString(currentVersion)

	if installedVersionErr == nil && currentErr == nil && installedVersion == normalizedCurrentVersion {
		fmt.Fprintf(deps.stdout, "Current version %s is the same as installed.\n", normalizedCurrentVersion)
		fmt.Fprintln(deps.stdout, "Nothing to upgrade.")
		return nil
	}

	if isDevVersion(currentVersion) && !force {
		fmt.Fprintf(deps.stdout, "Current version is '%s' (development build).\n", currentVersion)
		fmt.Fprint(deps.stdout, "Upgrading with a development build is not recommended. Continue? [y/N]: ")
		if !deps.confirm() {
			fmt.Fprintln(deps.stdout, "Aborted.")
			return nil
		}
	}

	if installedVersionErr != nil && !force {
		fmt.Fprintf(deps.stdout, "Warning: could not determine installed version: %v\n", installedVersionErr)
		fmt.Fprint(deps.stdout, "Version safety checks cannot be completed. Continue anyway? [y/N]: ")
		if !deps.confirm() {
			fmt.Fprintln(deps.stdout, "Aborted.")
			return nil
		}
	}

	if installedVersionErr == nil {
		cmp, err1 := buildversion.ParseSemver(normalizedCurrentVersion)
		inst, err2 := buildversion.ParseSemver(installedVersion)
		if err1 == nil && err2 == nil && cmp.Compare(inst) < 0 {
			if !force {
				fmt.Fprintf(deps.stdout, "Current %s is older than installed %s.\n", normalizedCurrentVersion, installedVersion)
				fmt.Fprint(deps.stdout, "This would downgrade. Continue? [y/N]: ")
				if !deps.confirm() {
					fmt.Fprintln(deps.stdout, "Aborted.")
					return nil
				}
			}
		}
	}

	targetVersion := currentVersion
	if currentErr == nil {
		targetVersion = normalizedCurrentVersion
	}
	fromVersion := installedVersion
	if installedVersionErr != nil {
		fromVersion = "unknown"
	}
	fmt.Fprintf(deps.stdout, "Upgrading %s -> %s\n", fromVersion, targetVersion)
	fmt.Fprintf(deps.stdout, "Services to restart: %v\n", units)
	fmt.Fprintln(deps.stdout)

	result, err := deps.applyUpgrade(currentPath, installedVersion, targetVersion)
	if err != nil {
		return fmt.Errorf("upgrade failed: %w", err)
	}

	fmt.Fprintln(deps.stdout, "Upgraded successfully.")
	fmt.Fprintf(deps.stdout, "Stopped: %v\n", result.Stopped)
	fmt.Fprintf(deps.stdout, "Started: %v\n", result.Started)
	return nil
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Replace the installed NetsGo binary with the current one",
	Long: `Upgrade replaces the system-installed NetsGo binary with the currently
running binary, then restarts all managed services.

Requires root privileges (auto-elevates via sudo).
Only works when services are installed via 'netsgo install'.
If the current binary is already the installed one, does nothing.

	Use --force to skip confirmation for development builds, unknown installed
	versions, or downgrades.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := rerunUpgradeWithSudoIfNeeded(os.Getuid(), exec.LookPath, syscall.Exec); err != nil {
			return err
		}

		return runUpgradeCommand(forceUpgrade, defaultUpgradeCommandDeps())
	},
}

func rerunUpgradeWithSudoIfNeeded(uid int, lookPath func(file string) (string, error), execFn func(argv0 string, argv []string, envv []string) error) error {
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
	upgradeCmd.Flags().BoolVar(&forceUpgrade, "force", false, "Skip confirmation for development builds, unknown installed versions, or downgrades")
	rootCmd.AddCommand(upgradeCmd)
}

func readConfirmation() bool {
	reader := bufio.NewReader(os.Stdin)
	return readConfirmationFrom(reader)
}

func readConfirmationFrom(reader *bufio.Reader) bool {
	input, err := reader.ReadString('\n')
	if err != nil && len(input) == 0 {
		return false
	}
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
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

func getInstalledVersion() (string, error) {
	if _, err := os.Stat(svcmgr.BinaryPath); err != nil {
		return "", fmt.Errorf("stat installed binary: %w", err)
	}
	out, err := exec.Command(svcmgr.BinaryPath, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("run installed binary --version: %w", err)
	}
	version, err := extractInstalledVersion(string(out))
	if err != nil {
		return "", fmt.Errorf("parse installed version: %w", err)
	}
	return version, nil
}

func isDevVersion(v string) bool {
	_, err := buildversion.NormalizeVersionString(v)
	return err != nil
}

func extractInstalledVersion(output string) (string, error) {
	return buildversion.NormalizeVersionString(strings.TrimSpace(output))
}
