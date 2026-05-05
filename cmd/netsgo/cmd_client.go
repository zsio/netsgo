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
	Short: "Start NetsGo client (proxy agent)",
	Long: `Start NetsGo client, connect to the server, and wait for server-dispatched instructions.

This command is best suited for direct-run, development/debug, or container scenarios.
For long-running deployments on Linux hosts, prefer using netsgo install and netsgo manage.

On startup, the client automatically:
  1. Connects to the server and authenticates
  2. Establishes the data channel (yamux)
  3. Periodically reports system probe data (CPU/memory/disk/network)
  4. Listens for proxy tunnel instructions dispatched by the server

Tunnel creation, management, and deletion are all controlled from the server Web panel.

Service address formats:
  http://host:port     Plain HTTP service address
  https://host:port    HTTPS service address
  ws://host:port       Backward-compatible WebSocket form; normalized to http://
  wss://host:port      Backward-compatible WebSocket form; normalized to https://

All flags support environment variable configuration with NETSGO_ prefix, e.g.:
  NETSGO_SERVER=https://1.2.3.4:9527 NETSGO_KEY=mykey netsgo client`,
	Example: `  # Connect to local server (plain text)
  netsgo client

  # Connect to remote TLS server
  netsgo client --server https://1.2.3.4:9527 --key mykey

  # Skip TLS certificate verification (for testing only)
  netsgo client --server https://1.2.3.4:9527 --key mykey --tls-skip-verify`,
	Run: func(cmd *cobra.Command, args []string) {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

		serverAddr := viper.GetString("server")
		key := viper.GetString("key")

		log.Printf("🔗 NetsGo Client connecting to %s (key: %s)...", serverAddr, maskKey(key))
		if key == "" {
			log.Printf("⚠️  No --key flag provided; client will likely fail authentication")
		}

		c := client.New(serverAddr, key)
		dataDirFlag := cmd.Flag("data-dir")
		c.DataDir = resolveClientDataDir(dataDirFlag.Value.String(), dataDirFlag.Changed)

		unlock, err := flock.TryLock(filepath.Join(c.DataDir, "locks", "client.lock"))
		if err != nil {
			log.Fatalf("❌ Failed to acquire client singleton lock: %v", err)
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
			log.Printf("📩 Received signal %v, starting graceful shutdown...", sig)
			c.Shutdown()
			os.Exit(0)
		}()

		if err := c.Start(); err != nil {
			log.Fatalf("❌ Client startup failed: %v", err)
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

func resolveClientDataDir(flagValue string, flagChanged bool) string {
	if flagChanged {
		return flagValue
	}
	if envDataDir := os.Getenv("NETSGO_DATA_DIR"); envDataDir != "" {
		return envDataDir
	}
	return flagValue
}

func init() {
	clientCmd.Flags().StringP("server", "s", "http://localhost:9527", "Service address (http/https recommended; ws/wss accepted)")
	clientCmd.Flags().StringP("key", "k", "", "Authentication key")
	clientCmd.Flags().String("data-dir", datadir.DefaultDataDir(), "Data root directory")

	clientCmd.Flags().Bool("tls-skip-verify", false, "Skip TLS certificate verification (dev/test only)")
	clientCmd.Flags().String("tls-fingerprint", "", "Pin server certificate SHA-256 fingerprint (AA:BB:CC:... format)")

	if err := viper.BindPFlag("server", clientCmd.Flags().Lookup("server")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("key", clientCmd.Flags().Lookup("key")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("data-dir", clientCmd.Flags().Lookup("data-dir")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("tls-skip-verify", clientCmd.Flags().Lookup("tls-skip-verify")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("tls-fingerprint", clientCmd.Flags().Lookup("tls-fingerprint")); err != nil {
		panic(err)
	}

	rootCmd.AddCommand(clientCmd)
}
