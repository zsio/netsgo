package main

import (
	"os"

	buildversion "netsgo/pkg/version"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var version = buildversion.Summary()

// rootCmd 是 CLI 的根命令
var rootCmd = &cobra.Command{
	Use:   "netsgo",
	Short: "NetsGo — 新一代内网穿透与边缘管控平台",
	Long: `🚀 NetsGo — 新一代内网穿透与边缘管控平台

轻量级管控中心 (C2) + 高性能网络隧道。
单文件交付，支持服务端与客户端一体化运行。

  文档: https://github.com/netsgo/netsgo
  版本: ` + version,
	Version: version,
}

func init() {
	// 设置环境变量前缀: NETSGO_
	// 例如 --port 对应 NETSGO_PORT
	viper.SetEnvPrefix("NETSGO")
	viper.AutomaticEnv()
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
