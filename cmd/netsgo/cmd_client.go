package main

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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
		logFormat := viper.GetString("log-format")
		logger := client.NewEventLogger(logFormat, os.Stderr)
		if logger.Format() == client.LogFormatJSON {
			log.SetOutput(io.Discard)
		}

		logger.Info("client.starting", "NetsGo client starting", map[string]any{
			"server":  serverAddr,
			"has_key": key != "",
		})
		if key == "" {
			logger.Warn("client.key_missing", "No --key flag provided; client will likely fail authentication", nil)
		}

		c := client.New(serverAddr, key)
		c.Logger = logger
		dataDirFlag := cmd.Flag("data-dir")
		c.DataDir = resolveClientDataDir(dataDirFlag.Value.String(), dataDirFlag.Changed)

		unlock, err := flock.TryLock(filepath.Join(c.DataDir, "locks", "client.lock"))
		if err != nil {
			fatalClient(logger, fmt.Errorf("failed to acquire client singleton lock: %w", err))
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
			logger.Info("client.signal_received", "Received signal, starting graceful shutdown", map[string]any{"signal": sig.String()})
			c.Shutdown()
			os.Exit(0)
		}()

		if err := c.Start(); err != nil {
			fatalClient(logger, fmt.Errorf("client startup failed: %w", err))
		}
	},
}

func fatalClient(logger *client.EventLogger, err error) {
	if logger != nil {
		logger.Error("client.fatal", "Client exited with a fatal error", map[string]any{"error": err.Error()})
	} else {
		log.Printf("❌ %v", err)
	}
	os.Exit(1)
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
	clientCmd.Flags().String("log-format", client.LogFormatText, "Client log format: text or json")

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
	if err := viper.BindPFlag("log-format", clientCmd.Flags().Lookup("log-format")); err != nil {
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
