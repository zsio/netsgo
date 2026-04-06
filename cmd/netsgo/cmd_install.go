package main

import (
	"netsgo/internal/install"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "交互式安装 NetsGo 为 systemd 受管服务 (仅 Linux)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return install.Run()
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
}
