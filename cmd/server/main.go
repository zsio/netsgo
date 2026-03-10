package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"netsgo/internal/server"
)

func main() {
	port := flag.Int("port", 8080, "服务端监听端口")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	s := server.New(*port)

	// 监听系统信号，实现优雅退出
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Printf("收到信号 %v，正在优雅关闭…", sig)
		if err := s.Stop(); err != nil {
			log.Printf("关闭服务端时出错: %v", err)
		}
	}()

	if err := s.Start(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("❌ 服务端启动失败: %v", err)
	}
	log.Println("服务端已关闭")
}
