package server

import (
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

func (c *ClientConn) GetInfo() protocol.ClientInfo {
	c.infoMu.RLock()
	defer c.infoMu.RUnlock()
	return c.Info
}

func (c *ClientConn) SetInfo(info protocol.ClientInfo) {
	c.infoMu.Lock()
	c.Info = info
	c.infoMu.Unlock()
}

func (c *ClientConn) writeJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("client %s control channel unavailable", c.ID)
	}
	return c.conn.WriteJSON(v)
}

func (c *ClientConn) detachControlConn() *websocket.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn := c.conn
	c.conn = nil
	return conn
}

func (a *ClientConn) SetStats(s *protocol.SystemStats) {
	a.statsMu.Lock()
	a.stats = s
	a.statsMu.Unlock()
}

func (a *ClientConn) GetStats() *protocol.SystemStats {
	a.statsMu.RLock()
	defer a.statsMu.RUnlock()
	return a.stats
}

func (a *ClientConn) enrichStats(stats *protocol.SystemStats) {
	a.statsMu.RLock()
	prev := a.prevStats
	prevAt := a.prevStatsAt
	a.statsMu.RUnlock()

	if prev != nil {
		elapsed := time.Since(prevAt).Seconds()
		if elapsed > 0.5 {
			if stats.NetSent >= prev.NetSent {
				stats.NetSentSpeed = float64(stats.NetSent-prev.NetSent) / elapsed
			}
			if stats.NetRecv >= prev.NetRecv {
				stats.NetRecvSpeed = float64(stats.NetRecv-prev.NetRecv) / elapsed
			}
		}
	}
}

func (a *ClientConn) RangeProxies(fn func(name string, tunnel *ProxyTunnel) bool) {
	a.proxyMu.RLock()
	defer a.proxyMu.RUnlock()
	for name, tunnel := range a.proxies {
		if !fn(name, tunnel) {
			return
		}
	}
}
