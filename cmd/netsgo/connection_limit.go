package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Read the executing command's flag directly: server and client have distinct
// flag sets, while Viper's bindings are global.
func connectionLimitForCommand(cmd *cobra.Command) (int, error) {
	limit, err := cmd.Flags().GetInt("max-connections")
	if err != nil {
		return 0, err
	}
	if !cmd.Flags().Changed("max-connections") && viper.IsSet("max-connections") {
		limit = viper.GetInt("max-connections")
	}
	if limit <= 0 {
		return 0, fmt.Errorf("max-connections must be a positive integer")
	}
	return limit, nil
}
