package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update NetsGo binary (not yet implemented)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Auto-update is not yet implemented. Visit https://github.com/zsio/netsgo")
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
