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
	Short: "运行真实 TCP 全链路性能压测",
	Long: `NetsGo 真实 TCP 全链路性能压测工具。

完整数据路径:
  压测客户端 → TCP → Server ProxyPort → yamux OpenStream →
  Client AcceptStream → Client Dial Backend → io.Copy → Backend 消费`,
	Example: `  # 默认: 50 并发 × 1MB
  netsgo benchmark

  # 100 并发
  netsgo benchmark -c 100

  # 20 并发 × 10MB
  netsgo benchmark -c 20 --size 10`,
	Run: func(cmd *cobra.Command, args []string) {
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		dataSize, _ := cmd.Flags().GetInt("size")
		runBenchmark(concurrency, dataSize)
	},
}

func init() {
	benchmarkCmd.Flags().IntP("concurrency", "c", 50, "并发连接数")
	benchmarkCmd.Flags().Int("size", 1, "每个连接传输的数据大小 (MB)")

	rootCmd.AddCommand(benchmarkCmd)
}

func runBenchmark(concurrency, dataSize int) {
	bytesPerConn := dataSize * 1024 * 1024

	// 限制 CPU 到 50%
	numCPU := runtime.NumCPU()
	halfCPU := numCPU / 2
	if halfCPU < 1 {
		halfCPU = 1
	}
	runtime.GOMAXPROCS(halfCPU)

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║         NetsGo 真实 TCP 全链路性能压测              ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  CPU 核心    : %d / %d (50%%)\n", halfCPU, numCPU)
	fmt.Printf("║  并发连接    : %d\n", concurrency)
	fmt.Printf("║  每连接传输  : %d MB\n", dataSize)
	fmt.Printf("║  预期总传输  : %d MB\n", concurrency*dataSize)
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	// ============================================================
	// 1. 启动 Backend (接收并消费数据，统计收到的字节数)
	// ============================================================
	var backendReceived int64
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		benchExitf("启动 backend 失败: %v", err)
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
	benchPrintStep("Backend 消费服务已启动 (:%d)", backendPort)

	// ============================================================
	// 2. 启动 NetsGo Server
	// ============================================================
	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		benchExitf("Server 预分配端口失败: %v", err)
	}
	serverPort := srvLn.Addr().(*net.TCPAddr).Port
	srvLn.Close()

	srv := server.New(serverPort)
	go srv.Start()
	time.Sleep(500 * time.Millisecond)
	benchPrintStep("NetsGo Server 已启动 (:%d)", serverPort)

	// ============================================================
	// 3. 启动 Client + 创建代理隧道
	// ============================================================
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

	fmt.Print("⏳ 等待隧道建立...")
	var proxyPort int
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		proxyPort = benchFindProxyPort(srv)
		if proxyPort != 0 {
			break
		}
	}
	if proxyPort == 0 {
		benchExitf("\n❌ 代理隧道未建立 (5秒超时)")
	}
	fmt.Printf(" 完成! 代理端口 :%d\n", proxyPort)

	// 验证隧道
	tc, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), 2*time.Second)
	if err != nil {
		benchExitf("❌ 代理端口不可达: %v", err)
	}
	tc.Write([]byte("hi"))
	tc.Close()
	time.Sleep(50 * time.Millisecond)
	benchPrintStep("隧道可通行性验证通过")

	// ============================================================
	// 4. 并发压测
	// ============================================================
	fmt.Printf("\n🚀 开始压测: %d 并发 × %d MB/连接...\n\n", concurrency, dataSize)

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

	// ============================================================
	// 5. 结果报告
	// ============================================================
	sentMB := float64(totalSent) / (1024 * 1024)
	recvMB := float64(atomic.LoadInt64(&backendReceived)) / (1024 * 1024)
	throughput := sentMB / duration.Seconds()

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║                 📊 压测结果报告                     ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  总耗时          : %.2f 秒\n", duration.Seconds())
	fmt.Printf("║  成功 / 总连接   : %d / %d\n", successConn, concurrency)
	if failedConn > 0 {
		fmt.Printf("║  失败连接        : %d\n", failedConn)
	}
	fmt.Printf("║  发送总量        : %.2f MB\n", sentMB)
	fmt.Printf("║  Backend 收到    : %.2f MB\n", recvMB)
	fmt.Printf("║  发送吞吐量      : %.2f MB/s\n", throughput)

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p50 := latencies[len(latencies)*50/100]
		p90 := latencies[len(latencies)*90/100]
		p99idx := len(latencies) * 99 / 100
		if p99idx >= len(latencies) {
			p99idx = len(latencies) - 1
		}
		p99 := latencies[p99idx]
		fmt.Printf("║  延迟 P50        : %v\n", p50.Round(time.Millisecond))
		fmt.Printf("║  延迟 P90        : %v\n", p90.Round(time.Millisecond))
		fmt.Printf("║  延迟 P99        : %v\n", p99.Round(time.Millisecond))
	}

	lossRate := 0.0
	if sentMB > 0 {
		lossRate = (1 - recvMB/sentMB) * 100
	}
	fmt.Printf("║  数据丢失率      : %.2f%%\n", lossRate)
	fmt.Println("╚══════════════════════════════════════════════════════╝")
}

func benchFindProxyPort(srv *server.Server) int {
	var port int
	srv.RangeAgents(func(id string, agent *server.AgentConn) bool {
		agent.RangeProxies(func(name string, tunnel *server.ProxyTunnel) bool {
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
