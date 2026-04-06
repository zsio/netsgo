package main

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"netsgo/internal/client"
	"netsgo/pkg/datadir"
	"netsgo/pkg/flock"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "启动 NetsGo 客户端 (代理端)",
	Long: `启动 NetsGo 客户端，连接到服务端并等待服务端下发指令。

该命令更适合 direct-run、开发调试或容器场景。
如果你是在 Linux 主机上长期运行，请优先使用 netsgo install 与 netsgo manage 管理受管服务。

客户端启动后会自动完成:
  1. 连接到服务端并完成认证
  2. 建立数据通道 (yamux)
  3. 定时上报系统探针数据 (CPU/内存/磁盘/网络)
  4. 监听服务端下发的代理隧道指令

代理隧道的创建、管理和销毁均由服务端 Web 面板统一控制。

服务端地址支持以下格式:
  ws://host:port       明文 WebSocket
  wss://host:port      加密 WebSocket
  http://host:port     明文 HTTP（自动推导为 ws://）
  https://host:port    加密 HTTPS（自动推导为 wss://）

所有参数均支持环境变量配置，环境变量前缀为 NETSGO_，例如:
  NETSGO_SERVER=https://1.2.3.4:8080 NETSGO_KEY=mykey netsgo client`,
	Example: `  # 连接到本地服务端（明文）
  netsgo client

  # 连接到远程 TLS 服务端
  netsgo client --server https://1.2.3.4:8080 --key mykey

  # 跳过 TLS 证书校验（仅测试用）
  netsgo client --server wss://1.2.3.4:8080 --key mykey --tls-skip-verify

  # 使用 ws:// 格式连接（向后兼容）
  netsgo client --server ws://1.2.3.4:8080 --key mykey`,
	Run: func(cmd *cobra.Command, args []string) {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

		serverAddr := viper.GetString("server")
		key := viper.GetString("key")

		log.Printf("🔗 NetsGo Client 连接到 %s (key: %s) ...", serverAddr, maskKey(key))
		if key == "" {
			log.Printf("⚠️ 未提供 --key 参数，客户端大概率会在认证阶段失败")
		}

		c := client.New(serverAddr, key)
		c.DataDir = viper.GetString("data-dir")

		unlock, err := flock.TryLock(filepath.Join(c.DataDir, "locks", "client.lock"))
		if err != nil {
			log.Fatalf("❌ 获取 client 单实例锁失败: %v", err)
		}
		defer unlock()

		c.TLSSkipVerify = viper.GetBool("tls-skip-verify")
		if fp := viper.GetString("tls-fingerprint"); fp != "" {
			c.TLSFingerprint = fp
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

		go func() {
			sig := <-sigCh
			log.Printf("📩 收到信号 %v，开始优雅关闭...", sig)
			c.Shutdown()
			os.Exit(0)
		}()

		if err := c.Start(); err != nil {
			log.Fatalf("❌ 客户端启动失败: %v", err)
		}
	},
}

func maskKey(key string) string {
	if key == "" {
		return "(empty)"
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}

func init() {
	// 定义 flags
	clientCmd.Flags().StringP("server", "s", "ws://localhost:8080", "服务端地址 (支持 ws/wss/http/https)")
	clientCmd.Flags().StringP("key", "k", "", "认证密钥")
	clientCmd.Flags().String("data-dir", datadir.DefaultDataDir(), "运行数据根目录")

	clientCmd.Flags().Bool("tls-skip-verify", false, "跳过 TLS 证书校验（仅开发/测试用）")
	clientCmd.Flags().String("tls-fingerprint", "", "指定服务器证书 SHA-256 指纹 (AA:BB:CC:... 格式)")

	// 绑定 viper (支持环境变量)
	viper.BindPFlag("server", clientCmd.Flags().Lookup("server"))
	viper.BindPFlag("key", clientCmd.Flags().Lookup("key"))
	viper.BindPFlag("data-dir", clientCmd.Flags().Lookup("data-dir"))
	viper.BindPFlag("tls-skip-verify", clientCmd.Flags().Lookup("tls-skip-verify"))
	viper.BindPFlag("tls-fingerprint", clientCmd.Flags().Lookup("tls-fingerprint"))

	// 注册到根命令
	rootCmd.AddCommand(clientCmd)
}
