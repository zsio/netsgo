package main

import (
	"flag"
	"log"

	"netsgo/internal/server"
)

func main() {
	port := flag.Int("port", 8080, "服务端监听端口")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	s := server.New(*port)
	if err := s.Start(); err != nil {
		log.Fatalf("❌ 服务端启动失败: %v", err)
	}
}
