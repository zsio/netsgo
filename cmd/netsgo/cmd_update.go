package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "显示 NetsGo 更新入口",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("托管服务：运行 'netsgo manage'，选择“更新”")
		fmt.Println("已有新版 netsgo 文件：执行新版文件的 'netsgo upgrade'")
		fmt.Println("手动下载：https://github.com/zsio/netsgo/releases")
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
