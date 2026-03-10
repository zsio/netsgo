package main

import (
	"log"

	"netsgo/internal/server"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "启动 NetsGo 服务端",
	Long: `启动 NetsGo 服务端，提供 Web 面板、API、控制通道和数据通道。

所有参数均支持环境变量配置，环境变量前缀为 NETSGO_，例如:
  NETSGO_PORT=9090 netsgo server`,
	Example: `  # 使用默认端口 8080 启动
  netsgo server

  # 指定端口
  netsgo server --port 9090

  # 通过环境变量配置
  NETSGO_PORT=9090 netsgo server`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

		port := viper.GetInt("port")
		log.Printf("🚀 NetsGo Server 启动中 (端口: %d)...", port)

		s := server.New(port)
		if err := s.Start(); err != nil {
			log.Fatalf("❌ 服务端启动失败: %v", err)
		}
	},
}

func init() {
	// 定义 flags
	serverCmd.Flags().IntP("port", "p", 8080, "服务端监听端口")

	// 绑定 viper (支持环境变量)
	viper.BindPFlag("port", serverCmd.Flags().Lookup("port"))

	// 注册到根命令
	rootCmd.AddCommand(serverCmd)
}
