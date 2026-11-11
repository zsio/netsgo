package client

import (
	"net"

	"netsgo/internal/connlimit"
)

func (c *Client) admitForwardingConn(conn net.Conn) (net.Conn, bool) {
	admitted, ok := c.dataConnections.Admit(conn, c.MaxConnections)
	if !ok {
		c.reportConnectionLimit()
	}
	return admitted, ok
}

func (c *Client) reportConnectionLimit() {
	if c.dataConnections.ShouldReportRejection() {
		maximum := c.MaxConnections
		if maximum <= 0 {
			maximum = connlimit.Default
		}
		c.logger().Warn("client.connection_limit_reached", "Forwarding connection limit reached; rejected new connection", map[string]any{"active": c.dataConnections.Active(), "limit": maximum})
	}
}
