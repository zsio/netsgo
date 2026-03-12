package client

import (
	"fmt"
	"log"
	"net"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// handleUDPStream 处理 UDP 类型的 yamux stream：
// 1. Dial 本地 UDP 服务
// 2. 使用 UDPRelay 在 stream（帧化）和 localConn（原始 UDP）之间转发
//
// 每个 stream 代表一个 UDP 虚拟会话（由外部 srcAddr 标识），
// Server 保证同一 srcAddr 的报文走同一个 stream。
func (c *Client) handleUDPStream(stream *yamux.Stream, cfg protocol.ProxyNewRequest) {
	defer stream.Close()

	localAddr := net.JoinHostPort(cfg.LocalIP, fmt.Sprintf("%d", cfg.LocalPort))
	localConn, err := net.Dial("udp", localAddr)
	if err != nil {
		log.Printf("⚠️ UDP 连接本地服务失败 [%s → %s]: %v", cfg.Name, localAddr, err)
		return
	}
	defer localConn.Close()

	// 双向转发：stream（帧化）↔ localConn（原始 UDP）
	mux.UDPRelay(stream, localConn)
}
