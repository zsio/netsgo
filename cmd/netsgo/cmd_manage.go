package main

import (
	"netsgo/internal/manage"

	"github.com/spf13/cobra"
)

var manageCmd = &cobra.Command{
	Use:   "manage",
	Short: "管理已安装的 NetsGo systemd 服务 (仅 Linux)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return manage.Run()
	},
}

func init() {
	rootCmd.AddCommand(manageCmd)
}
