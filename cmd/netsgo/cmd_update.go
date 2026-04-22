package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update NetsGo binary (use 'manage' or 'upgrade' instead)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("For managed services: run 'netsgo manage' and select 'Update'")
		fmt.Println("To upgrade with current binary: run 'netsgo upgrade'")
		fmt.Println("Manual download: https://github.com/zsio/netsgo/releases")
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
