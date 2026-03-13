package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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

		// P15: 监听系统信号，优雅关闭
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

		go func() {
			sig := <-sigCh
			log.Printf("📩 收到信号 %v，开始优雅关闭...", sig)

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			if err := s.Shutdown(ctx); err != nil {
				log.Printf("⚠️ 优雅关闭出错: %v", err)
				os.Exit(1)
			}
			os.Exit(0)
		}()

		if err := s.Start(); err != nil {
			// http.Server.Shutdown 会导致 Serve 返回 http.ErrServerClosed，这是正常行为
			// 此时信号处理 goroutine 的 Shutdown() 可能仍在执行（断开 Agent、关闭 EventBus），
			// 需要阻塞等待信号处理 goroutine 调用 os.Exit(0) 完成退出
			if err.Error() == "http: Server closed" {
				select {} // 等待信号处理 goroutine 完成 Shutdown 并 os.Exit
			}
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
