package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
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

TLS 模式:
  custom  用户提供证书和私钥（生产推荐）
  auto    自动生成自签名证书并持久化（快速部署）
  off     不使用 TLS，由反向代理负责（默认）

所有参数均支持环境变量配置，环境变量前缀为 NETSGO_，例如:
  NETSGO_PORT=9090 NETSGO_TLS_MODE=auto netsgo server`,
	Example: `  # 使用默认端口 8080 启动（无 TLS）
  netsgo server

  # 自动生成自签名证书启动
  netsgo server --tls-mode auto

  # 使用用户提供的证书启动
  netsgo server --tls-mode custom --tls-cert /path/to/cert.pem --tls-key /path/to/key.pem

  # 反向代理模式（信任特定代理 IP）
  netsgo server --tls-mode off --trusted-proxies 127.0.0.1/32,10.0.0.0/8`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

		port := viper.GetInt("port")
		log.Printf("🚀 NetsGo Server 启动中 (端口: %d)...", port)

		s := server.New(port)

		tlsMode := viper.GetString("tls-mode")
		if tlsMode != "" {
			tlsCfg := &server.TLSConfig{
				Mode:     tlsMode,
				CertFile: viper.GetString("tls-cert"),
				KeyFile:  viper.GetString("tls-key"),
				AutoDir:  viper.GetString("tls-auto-dir"),
			}

			// 解析 trusted-proxies（逗号分隔）
			if proxies := viper.GetString("trusted-proxies"); proxies != "" {
				for _, p := range strings.Split(proxies, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						tlsCfg.TrustedProxies = append(tlsCfg.TrustedProxies, p)
					}
				}
			}

			if err := tlsCfg.Validate(); err != nil {
				log.Fatalf("❌ TLS 配置无效: %v", err)
			}
			s.TLS = tlsCfg
		}

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

	serverCmd.Flags().String("tls-mode", "", "TLS 模式: custom / auto / off")
	serverCmd.Flags().String("tls-cert", "", "TLS 证书文件路径 (custom 模式)")
	serverCmd.Flags().String("tls-key", "", "TLS 私钥文件路径 (custom 模式)")
	serverCmd.Flags().String("tls-auto-dir", "", "自签证书存储目录 (auto 模式, 默认 ~/.netsgo/tls)")
	serverCmd.Flags().String("trusted-proxies", "", "受信任代理 CIDR 列表, 逗号分隔 (off 模式)")

	// 绑定 viper (支持环境变量)
	viper.BindPFlag("port", serverCmd.Flags().Lookup("port"))
	viper.BindPFlag("tls-mode", serverCmd.Flags().Lookup("tls-mode"))
	viper.BindPFlag("tls-cert", serverCmd.Flags().Lookup("tls-cert"))
	viper.BindPFlag("tls-key", serverCmd.Flags().Lookup("tls-key"))
	viper.BindPFlag("tls-auto-dir", serverCmd.Flags().Lookup("tls-auto-dir"))
	viper.BindPFlag("trusted-proxies", serverCmd.Flags().Lookup("trusted-proxies"))

	// 注册到根命令
	rootCmd.AddCommand(serverCmd)
}
