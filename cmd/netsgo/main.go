package main

import (
	"os"
	"strings"

	buildversion "netsgo/pkg/version"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var version = buildversion.Summary()

var rootCmd = &cobra.Command{
	Use:   "netsgo",
	Short: "NetsGo — next-gen intranet tunneling and edge management platform",
	Long: `NetsGo — next-gen intranet tunneling and edge management platform

Lightweight control center (C2) + high-performance network tunnels.
Single-binary delivery, supporting server and client in one executable.

  Docs:    https://github.com/netsgo/netsgo
  Version: ` + version,
	Version: version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	viper.SetEnvPrefix("NETSGO")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
