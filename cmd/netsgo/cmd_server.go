package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"netsgo/internal/server"
	"netsgo/pkg/datadir"
	"netsgo/pkg/flock"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

type initFlagValues struct {
	AdminUsername string
	AdminPassword string
	ServerAddr    string
	AllowedPorts  string
}

type initPrompter interface {
	IsInteractive() bool
	Prompt(label string) (string, error)
	PromptPassword(label string) (string, error)
}

type terminalInitPrompter struct{}

func (terminalInitPrompter) IsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func (terminalInitPrompter) Prompt(label string) (string, error) {
	if _, err := fmt.Fprintf(os.Stdout, "%s: ", label); err != nil {
		return "", err
	}
	reader := bufio.NewReader(os.Stdin)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func (terminalInitPrompter) PromptPassword(label string) (string, error) {
	if _, err := fmt.Fprintf(os.Stdout, "%s: ", label); err != nil {
		return "", err
	}
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	if _, printErr := fmt.Fprintln(os.Stdout); printErr != nil && err == nil {
		err = printErr
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(value)), nil
}

func (v initFlagValues) anyProvided() bool {
	return v.AdminUsername != "" ||
		v.AdminPassword != "" ||
		v.ServerAddr != "" ||
		v.AllowedPorts != ""
}

func buildInitParamsFromViper() server.InitParams {
	return server.InitParams{
		AdminUsername: viper.GetString("init-admin-username"),
		AdminPassword: viper.GetString("init-admin-password"),
		ServerAddr:    viper.GetString("init-server-addr"),
		AllowedPorts:  viper.GetString("init-allowed-ports"),
	}
}

func completeInitParamsForStartup(initialized bool, params server.InitParams, prompter initPrompter) (server.InitParams, error) {
	if initialized || params.IsComplete() || !prompter.IsInteractive() {
		return params, nil
	}

	var err error
	if params.AdminUsername == "" {
		params.AdminUsername, err = prompter.Prompt("Init admin username")
		if err != nil {
			return params, err
		}
	}
	if params.AdminPassword == "" {
		params.AdminPassword, err = prompter.PromptPassword("Init admin password")
		if err != nil {
			return params, err
		}
	}
	if params.ServerAddr == "" {
		params.ServerAddr, err = prompter.Prompt("Init server addr")
		if err != nil {
			return params, err
		}
	}
	if params.AllowedPorts == "" {
		params.AllowedPorts, err = prompter.Prompt("Init allowed ports")
		if err != nil {
			return params, err
		}
	}

	return params, nil
}

func validateInitFlagsForStartup(initialized bool, values initFlagValues) error {
	if initialized {
		return nil
	}
	if !values.anyProvided() {
		return fmt.Errorf("服务尚未初始化，请通过 init 参数完成一次性初始化")
	}
	if values.AdminUsername == "" || values.AdminPassword == "" || values.ServerAddr == "" || values.AllowedPorts == "" {
		return fmt.Errorf("服务尚未初始化，必须完整提供 --init-admin-username、--init-admin-password、--init-server-addr、--init-allowed-ports")
	}
	return nil
}

func shouldWarnInitFlagsIgnored(initialized bool, values initFlagValues) bool {
	return initialized && values.anyProvided()
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "启动 NetsGo 服务端",
	Long: `启动 NetsGo 服务端，提供 Web 面板、API、控制通道和数据通道。

该命令更适合 direct-run、开发调试或容器场景。
如果你是在 Linux 主机上长期运行，请优先使用 netsgo install 与 netsgo manage 管理受管服务。

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
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

		port := viper.GetInt("port")
		log.Printf("🚀 NetsGo Server 启动中 (端口: %d)...", port)

		s := server.New(port)
		s.DataDir = viper.GetString("data-dir")
		s.AllowLoopbackManagementHost = viper.GetBool("allow-loopback-management-host")

		adminStore, err := server.NewAdminStore(filepath.Join(s.DataDir, "server", "admin.json"))
		if err != nil {
			log.Fatalf("❌ 读取服务初始化状态失败: %v", err)
		}

		initParams := buildInitParamsFromViper()
		initParams, err = completeInitParamsForStartup(adminStore.IsInitialized(), initParams, terminalInitPrompter{})
		if err != nil {
			log.Fatalf("❌ 读取初始化输入失败: %v", err)
		}
		if err := validateInitFlagsForStartup(adminStore.IsInitialized(), initFlagValues{
			AdminUsername: initParams.AdminUsername,
			AdminPassword: initParams.AdminPassword,
			ServerAddr:    initParams.ServerAddr,
			AllowedPorts:  initParams.AllowedPorts,
		}); err != nil {
			log.Fatalf("❌ %v", err)
		}
		if shouldWarnInitFlagsIgnored(adminStore.IsInitialized(), initFlagValues{
			AdminUsername: initParams.AdminUsername,
			AdminPassword: initParams.AdminPassword,
			ServerAddr:    initParams.ServerAddr,
			AllowedPorts:  initParams.AllowedPorts,
		}) {
			log.Printf("ℹ️ 服务已初始化，--init-* 参数将被忽略")
		}

		if !adminStore.IsInitialized() {
			if err := server.ApplyInit(s.DataDir, initParams); err != nil {
				log.Fatalf("❌ 服务初始化失败: %v", err)
			}
		}

		unlock, err := flock.TryLock(filepath.Join(s.DataDir, "locks", "server.lock"))
		if err != nil {
			log.Fatalf("❌ 获取 server 单实例锁失败: %v", err)
		}
		defer unlock()

		// 将 server-addr 同步回环境变量，以便 internal/server 包中的 isServerAddrLocked() 等函数读取
		if addr := viper.GetString("server-addr"); addr != "" {
			os.Setenv("NETSGO_SERVER_ADDR", addr)
		}

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
	serverCmd.Flags().String("data-dir", datadir.DefaultDataDir(), "运行数据根目录")

	serverCmd.Flags().String("tls-mode", "", "TLS 模式: custom / auto / off")
	serverCmd.Flags().String("tls-cert", "", "TLS 证书文件路径 (custom 模式)")
	serverCmd.Flags().String("tls-key", "", "TLS 私钥文件路径 (custom 模式)")
	serverCmd.Flags().String("tls-auto-dir", "", "自签证书存储目录 (auto 模式, 默认 <data-dir>/server/tls)")
	serverCmd.Flags().String("trusted-proxies", "", "受信任代理 CIDR 列表, 逗号分隔 (off 模式)")
	serverCmd.Flags().String("init-admin-username", "", "首次初始化管理员用户名")
	serverCmd.Flags().String("init-admin-password", "", "首次初始化管理员密码")
	serverCmd.Flags().String("init-server-addr", "", "首次初始化服务对外地址")
	serverCmd.Flags().String("init-allowed-ports", "", "首次初始化允许端口范围")
	serverCmd.Flags().String("server-addr", "", "强制配置服务端的外网访问地址或域名")
	serverCmd.Flags().Bool("allow-loopback-management-host", false, "显式允许 localhost / 127.0.0.1 / ::1 作为管理面兜底 Host")

	// 绑定 viper (支持环境变量)
	viper.BindPFlag("port", serverCmd.Flags().Lookup("port"))
	viper.BindPFlag("data-dir", serverCmd.Flags().Lookup("data-dir"))
	viper.BindPFlag("tls-mode", serverCmd.Flags().Lookup("tls-mode"))
	viper.BindPFlag("tls-cert", serverCmd.Flags().Lookup("tls-cert"))
	viper.BindPFlag("tls-key", serverCmd.Flags().Lookup("tls-key"))
	viper.BindPFlag("tls-auto-dir", serverCmd.Flags().Lookup("tls-auto-dir"))
	viper.BindPFlag("trusted-proxies", serverCmd.Flags().Lookup("trusted-proxies"))
	viper.BindPFlag("init-admin-username", serverCmd.Flags().Lookup("init-admin-username"))
	viper.BindPFlag("init-admin-password", serverCmd.Flags().Lookup("init-admin-password"))
	viper.BindPFlag("init-server-addr", serverCmd.Flags().Lookup("init-server-addr"))
	viper.BindPFlag("init-allowed-ports", serverCmd.Flags().Lookup("init-allowed-ports"))
	viper.BindPFlag("server-addr", serverCmd.Flags().Lookup("server-addr"))
	viper.BindPFlag("allow-loopback-management-host", serverCmd.Flags().Lookup("allow-loopback-management-host"))
	// 注册到根命令
	rootCmd.AddCommand(serverCmd)
}
