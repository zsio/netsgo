package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "更新 NetsGo 二进制 (功能待实现)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("自动更新功能尚未实现，请访问 https://github.com/zsio/netsgo")
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
