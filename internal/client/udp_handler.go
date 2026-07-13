package client

import (
	"fmt"
	"log"
	"net"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// handleUDPStream handles a framed UDP logical stream:
// 1. Dial the local UDP service
// 2. Use UDPRelay to forward between the stream (framed) and localConn (raw UDP)
//
// Each stream represents a virtual UDP session identified by the external srcAddr.
// The server guarantees that packets from the same srcAddr use the same stream.
func (c *Client) handleUDPStream(stream net.Conn, cfg protocol.ProxyNewRequest, observe func(uint64, uint64)) {
	defer func() { _ = stream.Close() }()

	localAddr := net.JoinHostPort(cfg.LocalIP, fmt.Sprintf("%d", cfg.LocalPort))
	localConn, err := net.Dial("udp", localAddr)
	if err != nil {
		log.Printf("⚠️ Failed to connect UDP local service [%s → %s]: %v", cfg.Name, localAddr, err)
		return
	}
	defer func() { _ = localConn.Close() }()

	// Relay traffic in both directions: stream (framed) ↔ localConn (raw UDP)
	mux.UDPRelayWithTraffic(stream, localConn, observe)
}
