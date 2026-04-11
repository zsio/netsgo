package client

import (
	"fmt"
	"log"
	"net"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// handleUDPStream handles a UDP yamux stream:
// 1. Dial the local UDP service
// 2. Use UDPRelay to forward between the stream (framed) and localConn (raw UDP)
//
// Each stream represents a virtual UDP session identified by the external srcAddr.
// The server guarantees that packets from the same srcAddr use the same stream.
func (c *Client) handleUDPStream(stream *yamux.Stream, cfg protocol.ProxyNewRequest) {
	defer stream.Close()

	localAddr := net.JoinHostPort(cfg.LocalIP, fmt.Sprintf("%d", cfg.LocalPort))
	localConn, err := net.Dial("udp", localAddr)
	if err != nil {
		log.Printf("⚠️ Failed to connect UDP local service [%s → %s]: %v", cfg.Name, localAddr, err)
		return
	}
	defer localConn.Close()

	// Relay traffic in both directions: stream (framed) ↔ localConn (raw UDP)
	mux.UDPRelay(stream, localConn)
}
