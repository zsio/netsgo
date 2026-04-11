package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"netsgo/internal/client"
	"netsgo/internal/server"
	"netsgo/pkg/protocol"

	"github.com/spf13/cobra"
)

var benchmarkCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "Run a full TCP end-to-end performance benchmark",
	Long: `NetsGo full TCP end-to-end performance benchmark.

Full data path:
  Benchmark client → TCP → Server ProxyPort → yamux OpenStream →
  Client AcceptStream → Client Dial Backend → io.Copy → Backend consume`,
	Example: `  # Default: 50 concurrent × 1 MB
  netsgo benchmark

  # 100 concurrent connections
  netsgo benchmark -c 100

  # 20 concurrent × 10 MB each
  netsgo benchmark -c 20 --size 10`,
	Run: func(cmd *cobra.Command, args []string) {
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		dataSize, _ := cmd.Flags().GetInt("size")
		runBenchmark(concurrency, dataSize)
	},
}

func init() {
	benchmarkCmd.Flags().IntP("concurrency", "c", 50, "Number of concurrent connections")
	benchmarkCmd.Flags().Int("size", 1, "Data size per connection (MB)")

	rootCmd.AddCommand(benchmarkCmd)
}

func runBenchmark(concurrency, dataSize int) {
	bytesPerConn := dataSize * 1024 * 1024

	numCPU := runtime.NumCPU()
	halfCPU := numCPU / 2
	if halfCPU < 1 {
		halfCPU = 1
	}
	runtime.GOMAXPROCS(halfCPU)

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║         NetsGo Full TCP End-to-End Benchmark         ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  CPU cores   : %d / %d (50%%)\n", halfCPU, numCPU)
	fmt.Printf("║  Concurrent  : %d\n", concurrency)
	fmt.Printf("║  Per conn    : %d MB\n", dataSize)
	fmt.Printf("║  Total (est) : %d MB\n", concurrency*dataSize)
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	var backendReceived int64
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		benchExitf("Failed to start backend: %v", err)
	}
	defer backendLn.Close()
	backendPort := backendLn.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := backendLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				n, _ := io.Copy(io.Discard, c)
				atomic.AddInt64(&backendReceived, n)
			}(conn)
		}
	}()
	benchPrintStep("Backend consumer started (:%d)", backendPort)

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		benchExitf("Failed to pre-allocate server port: %v", err)
	}
	serverPort := srvLn.Addr().(*net.TCPAddr).Port
	srvLn.Close()

	srv := server.New(serverPort)
	go srv.Start()
	time.Sleep(500 * time.Millisecond)
	benchPrintStep("NetsGo Server started (:%d)", serverPort)

	wsAddr := fmt.Sprintf("ws://127.0.0.1:%d", serverPort)
	c := client.New(wsAddr, "bench-key")
	c.ProxyConfigs = []protocol.ProxyNewRequest{
		{
			Name:       "bench-tunnel",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  backendPort,
			RemotePort: 0,
		},
	}

	go c.Start()

	fmt.Print("⏳ Waiting for tunnel to establish...")
	var proxyPort int
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		proxyPort = benchFindProxyPort(srv)
		if proxyPort != 0 {
			break
		}
	}
	if proxyPort == 0 {
		benchExitf("\n❌ Proxy tunnel not established (5s timeout)")
	}
	fmt.Printf(" done! Proxy port :%d\n", proxyPort)

	tc, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), 2*time.Second)
	if err != nil {
		benchExitf("❌ Proxy port unreachable: %v", err)
	}
	tc.Write([]byte("hi"))
	tc.Close()
	time.Sleep(50 * time.Millisecond)
	benchPrintStep("Tunnel connectivity verified")

	fmt.Printf("\n🚀 Starting benchmark: %d concurrent × %d MB/conn...\n\n", concurrency, dataSize)

	payload := make([]byte, 64*1024)
	rand.Read(payload)

	var (
		wg          sync.WaitGroup
		totalSent   int64
		successConn int64
		failedConn  int64
		latencies   []time.Duration
		latMu       sync.Mutex
	)

	start := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			connStart := time.Now()

			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), 5*time.Second)
			if err != nil {
				atomic.AddInt64(&failedConn, 1)
				return
			}
			defer conn.Close()

			sent := 0
			for sent < bytesPerConn {
				toWrite := bytesPerConn - sent
				if toWrite > len(payload) {
					toWrite = len(payload)
				}
				n, err := conn.Write(payload[:toWrite])
				if err != nil {
					break
				}
				sent += n
			}

			atomic.AddInt64(&totalSent, int64(sent))
			atomic.AddInt64(&successConn, 1)

			latMu.Lock()
			latencies = append(latencies, time.Since(connStart))
			latMu.Unlock()
		}()
	}

	wg.Wait()
	duration := time.Since(start)

	time.Sleep(200 * time.Millisecond)

	sentMB := float64(totalSent) / (1024 * 1024)
	recvMB := float64(atomic.LoadInt64(&backendReceived)) / (1024 * 1024)
	throughput := sentMB / duration.Seconds()

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║                  📊 Benchmark Results               ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Total time      : %.2f s\n", duration.Seconds())
	fmt.Printf("║  Success / Total : %d / %d\n", successConn, concurrency)
	if failedConn > 0 {
		fmt.Printf("║  Failed conns    : %d\n", failedConn)
	}
	fmt.Printf("║  Bytes sent      : %.2f MB\n", sentMB)
	fmt.Printf("║  Backend recv    : %.2f MB\n", recvMB)
	fmt.Printf("║  Throughput      : %.2f MB/s\n", throughput)

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p50 := latencies[len(latencies)*50/100]
		p90 := latencies[len(latencies)*90/100]
		p99idx := len(latencies) * 99 / 100
		if p99idx >= len(latencies) {
			p99idx = len(latencies) - 1
		}
		p99 := latencies[p99idx]
		fmt.Printf("║  Latency P50     : %v\n", p50.Round(time.Millisecond))
		fmt.Printf("║  Latency P90     : %v\n", p90.Round(time.Millisecond))
		fmt.Printf("║  Latency P99     : %v\n", p99.Round(time.Millisecond))
	}

	lossRate := 0.0
	if sentMB > 0 {
		lossRate = (1 - recvMB/sentMB) * 100
	}
	fmt.Printf("║  Data loss rate  : %.2f%%\n", lossRate)
	fmt.Println("╚══════════════════════════════════════════════════════╝")
}

func benchFindProxyPort(srv *server.Server) int {
	var port int
	srv.RangeClients(func(id string, client *server.ClientConn) bool {
		client.RangeProxies(func(name string, tunnel *server.ProxyTunnel) bool {
			port = tunnel.Config.RemotePort
			return false
		})
		return port == 0
	})
	return port
}

func benchPrintStep(format string, args ...any) {
	fmt.Printf("✅ "+format+"\n", args...)
}

func benchExitf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
	panic("benchmark aborted")
}
