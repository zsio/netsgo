package main

import (
	"log"

	"netsgo/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "启动 NetsGo 客户端 (代理端)",
	Long: `启动 NetsGo 客户端，连接到服务端并等待服务端下发指令。

客户端启动后会自动完成:
  1. 连接到服务端并完成认证
  2. 建立数据通道 (yamux)
  3. 定时上报系统探针数据 (CPU/内存/磁盘/网络)
  4. 监听服务端下发的代理隧道指令

代理隧道的创建、管理和销毁均由服务端 Web 面板统一控制。

所有参数均支持环境变量配置，环境变量前缀为 NETSGO_，例如:
  NETSGO_SERVER=ws://1.2.3.4:8080 NETSGO_KEY=mykey netsgo client`,
	Example: `  # 连接到本地服务端
  netsgo client

  # 连接到远程服务端
  netsgo client --server ws://1.2.3.4:8080

  # 带认证密钥
  netsgo client --server ws://1.2.3.4:8080 --key mykey

  # 通过环境变量配置
  NETSGO_SERVER=ws://1.2.3.4:8080 NETSGO_KEY=mykey netsgo client`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

		serverAddr := viper.GetString("server")
		key := viper.GetString("key")

		log.Printf("🔗 NetsGo Client 连接到 %s ...", serverAddr)

		c := client.New(serverAddr, key)

		if err := c.Start(); err != nil {
			log.Fatalf("❌ 客户端启动失败: %v", err)
		}
	},
}

func init() {
	// 定义 flags
	clientCmd.Flags().StringP("server", "s", "ws://localhost:8080", "Server 的 WebSocket 地址")
	clientCmd.Flags().StringP("key", "k", "", "认证密钥")

	// 绑定 viper (支持环境变量)
	viper.BindPFlag("server", clientCmd.Flags().Lookup("server"))
	viper.BindPFlag("key", clientCmd.Flags().Lookup("key"))

	// 注册到根命令
	rootCmd.AddCommand(clientCmd)
}
