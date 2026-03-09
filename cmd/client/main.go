package main

import (
	"flag"
	"log"

	"netsgo/internal/client"
)

func main() {
	serverAddr := flag.String("server", "ws://localhost:8080", "Server 的 WebSocket 地址")
	token := flag.String("token", "", "认证令牌")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	c := client.New(*serverAddr, *token)
	if err := c.Start(); err != nil {
		log.Fatalf("❌ 客户端启动失败: %v", err)
	}
}
