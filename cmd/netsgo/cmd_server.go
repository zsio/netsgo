package main

import (
	"context"
	"fmt"
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
)

type initFlagValues struct {
	AdminUsername string
	AdminPassword string
	ServerAddr    string
	AllowedPorts  string
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

func validateInitFlagsForStartup(initialized bool, values initFlagValues) error {
	if initialized {
		return nil
	}
	if !values.anyProvided() {
		return fmt.Errorf("server not yet initialized; provide all --init-* flags (or NETSGO_INIT_* env vars), or use netsgo install for interactive setup")
	}
	if values.AdminUsername == "" || values.AdminPassword == "" || values.ServerAddr == "" || values.AllowedPorts == "" {
		return fmt.Errorf("server not yet initialized; must provide all of: --init-admin-username, --init-admin-password, --init-server-addr, --init-allowed-ports")
	}
	return nil
}

func shouldWarnInitFlagsIgnored(initialized bool, values initFlagValues) bool {
	return initialized && values.anyProvided()
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start NetsGo server",
	Long: `Start NetsGo server, providing Web panel, API, control channel, and data channel.

This command is best suited for direct-run, development/debug, or container scenarios.
For long-running deployments on Linux hosts, prefer using netsgo install and netsgo manage.
If the server is not initialized yet, direct-run startup requires explicit --init-* flags
or NETSGO_INIT_* environment variables. Interactive initialization is only available via
netsgo install.

TLS modes:
  custom  User-provided certificate and key (recommended for production)
  auto    Auto-generate self-signed certificate and persist it (quick deploy)
  off     No TLS; let a reverse proxy handle it (default)

All flags support environment variable configuration with NETSGO_ prefix, e.g.:
  NETSGO_PORT=9090 NETSGO_TLS_MODE=auto netsgo server`,
	Example: `  # Start with default port 9527 (no TLS)
  netsgo server

  # First-time initialization for direct-run startup
  netsgo server \
    --init-admin-username admin \
    --init-admin-password Password123 \
    --init-server-addr https://panel.example.com \
    --init-allowed-ports 10000-11000

  # Start with auto-generated self-signed certificate
  netsgo server --tls-mode auto

  # Start with user-provided certificate
  netsgo server --tls-mode custom --tls-cert /path/to/cert.pem --tls-key /path/to/key.pem

  # Reverse proxy mode (trust specific proxy IPs)
  netsgo server --tls-mode off --trusted-proxies 127.0.0.1/32,10.0.0.0/8`,
	Run: func(cmd *cobra.Command, args []string) {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

		port := viper.GetInt("port")
		log.Printf("🚀 NetsGo Server starting (port: %d)...", port)

		s := server.New(port)
		s.DataDir = viper.GetString("data-dir")
		s.AllowLoopbackManagementHost = viper.GetBool("allow-loopback-management-host")

		adminStore, err := server.NewAdminStore(filepath.Join(s.DataDir, "server", "admin.json"))
		if err != nil {
			log.Fatalf("❌ Failed to read server init state: %v", err)
		}

		initParams := buildInitParamsFromViper()
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
			log.Printf("ℹ️  Server already initialized, --init-* flags will be ignored")
		}

		if !adminStore.IsInitialized() {
			if err := server.ApplyInit(s.DataDir, initParams); err != nil {
				log.Fatalf("❌ Server initialization failed: %v", err)
			}
		}

		unlock, err := flock.TryLock(filepath.Join(s.DataDir, "locks", "server.lock"))
		if err != nil {
			log.Fatalf("❌ Failed to acquire server singleton lock: %v", err)
		}
		defer unlock()

		// Sync server-addr back to env so internal/server's isServerAddrLocked() can read it
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

			// Parse trusted-proxies (comma-separated)
			if proxies := viper.GetString("trusted-proxies"); proxies != "" {
				for _, p := range strings.Split(proxies, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						tlsCfg.TrustedProxies = append(tlsCfg.TrustedProxies, p)
					}
				}
			}

			if err := tlsCfg.Validate(); err != nil {
				log.Fatalf("❌ Invalid TLS configuration: %v", err)
			}
			s.TLS = tlsCfg
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

		go func() {
			sig := <-sigCh
			log.Printf("📩 Received signal %v, starting graceful shutdown...", sig)

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			if err := s.Shutdown(ctx); err != nil {
				log.Printf("⚠️  Graceful shutdown error: %v", err)
				os.Exit(1)
			}
			os.Exit(0)
		}()

		if err := s.Start(); err != nil {
			// http.Server.Shutdown causes Serve to return http.ErrServerClosed, which is expected
			if err.Error() == "http: Server closed" {
				select {} // wait for signal handler goroutine to finish Shutdown and os.Exit
			}
			log.Fatalf("❌ Server startup failed: %v", err)
		}
	},
}

func init() {
	serverCmd.Flags().IntP("port", "p", 9527, "Server listening port")
	serverCmd.Flags().String("data-dir", datadir.DefaultDataDir(), "Data root directory")

	serverCmd.Flags().String("tls-mode", "", "TLS mode: custom / auto / off")
	serverCmd.Flags().String("tls-cert", "", "TLS certificate file path (custom mode)")
	serverCmd.Flags().String("tls-key", "", "TLS private key file path (custom mode)")
	serverCmd.Flags().String("tls-auto-dir", "", "Self-signed cert storage dir (auto mode, default: <data-dir>/server/tls)")
	serverCmd.Flags().String("trusted-proxies", "", "Trusted proxy CIDR list, comma-separated (off mode)")
	serverCmd.Flags().String("init-admin-username", "", "Admin username for first-time initialization")
	serverCmd.Flags().String("init-admin-password", "", "Admin password for first-time initialization")
	serverCmd.Flags().String("init-server-addr", "", "Server external address for first-time initialization")
	serverCmd.Flags().String("init-allowed-ports", "", "Allowed port ranges for first-time initialization")
	serverCmd.Flags().String("server-addr", "", "Force-override server external address or domain")
	serverCmd.Flags().Bool("allow-loopback-management-host", false, "Explicitly allow localhost/127.0.0.1/::1 as fallback management Host")

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
	rootCmd.AddCommand(serverCmd)
}
