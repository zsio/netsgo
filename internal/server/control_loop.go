package server

import (
	"log"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

// controlLoop 持续处理控制通道上的消息。
func (s *Server) controlLoop(client *ClientConn) {
	client.mu.Lock()
	conn := client.conn
	client.mu.Unlock()
	if conn == nil {
		return
	}

	for {
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("⚠️ Client [%s] connection closed unexpectedly: %v", client.ID, err)
			}
			return
		}

		s.handleControlMessage(client, msg)
	}
}

func (s *Server) handleControlMessage(client *ClientConn, msg protocol.Message) {
	switch msg.Type {
	case protocol.MsgTypePing:
		s.handlePingMessage(client)
	case protocol.MsgTypeProbeReport:
		s.handleProbeReportMessage(client, msg)
	case protocol.MsgTypeProxyCreate:
		s.handleProxyCreateMessage(client, msg)
	case protocol.MsgTypeProxyProvisionAck:
		s.handleProxyProvisionAckMessage(client, msg)
	case protocol.MsgTypeTunnelRuntimeReport:
		s.handleTunnelRuntimeReportMessage(client, msg)
	case protocol.MsgTypeTunnelPreflightResp:
		s.handleTunnelPreflightResponseMessage(client, msg)
	case protocol.MsgTypeProxyClose:
		s.handleProxyCloseMessage(client, msg)
	default:
		log.Printf("⚠️ Unknown message type [%s]: %s", client.ID, msg.Type)
	}
}

func (s *Server) handlePingMessage(client *ClientConn) {
	// Ping/Pong 是纯心跳消息，不依赖数据通道状态。
	// 只要会话尚未进入 Closing 就应当回复 Pong，
	// 避免数据通道握手完成（DataHandshakeOK 已发出）但 promotePendingToLive
	// 尚未执行的窗口期内丢失 Pong 响应。
	if client.getState() == clientStateClosing {
		return
	}

	pong, _ := protocol.NewMessage(protocol.MsgTypePong, nil)
	if err := client.writeJSON(pong); err != nil {
		log.Printf("⚠️ Failed to send Pong [%s]: %v", client.ID, err)
	}
}

func mergeClientInfoWithStats(info protocol.ClientInfo, stats protocol.SystemStats) protocol.ClientInfo {
	updated := info
	if stats.PublicIPv4 != "" {
		updated.PublicIPv4 = stats.PublicIPv4
	}
	if stats.PublicIPv6 != "" {
		updated.PublicIPv6 = stats.PublicIPv6
	}
	return updated
}

func (s *Server) handleProbeReportMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}

	var stats protocol.SystemStats
	if err := msg.ParsePayload(&stats); err != nil {
		log.Printf("⚠️ Failed to parse probe report [%s]: %v", client.ID, err)
		return
	}

	now := time.Now()
	stats.UpdatedAt = now
	stats.FreshUntil = now.Add(clientStatsFreshnessWindow)
	client.enrichStats(&stats)
	client.SetStats(&stats)

	info := mergeClientInfoWithStats(client.GetInfo(), stats)
	client.SetInfo(info)

	client.statsMu.Lock()
	client.prevStats = cloneSystemStats(&stats)
	client.prevStatsAt = now
	client.statsMu.Unlock()

	if s.auth.adminStore != nil {
		if err := s.auth.adminStore.UpdateClientStats(client.ID, info, stats, client.RemoteAddr); err != nil {
			log.Printf("⚠️ Failed to persist latest client state [%s]: %v", client.ID, err)
		}
	}

	log.Printf("📊 [%s] CPU: %.1f%% | Memory: %.1f%% | Disk: %.1f%%",
		info.Hostname, stats.CPUUsage, stats.MemUsage, stats.DiskUsage)

	s.events.PublishJSON("stats_update", map[string]any{
		"client_id": client.ID,
		"stats":     stats,
	})
}

func (s *Server) handleProxyCreateMessage(client *ClientConn, msg protocol.Message) {
	var req protocol.ProxyNewRequest
	if err := msg.ParsePayload(&req); err != nil {
		log.Printf("⚠️ Failed to parse proxy request [%s]: %v", client.ID, err)
		return
	}
	req.ID = ""
	req.IngressBPS = 0
	req.EgressBPS = 0

	if req.Type == protocol.ProxyTypeHTTP {
		resp, _ := protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
			Name:    req.Name,
			Success: false,
			Message: "HTTP tunnels can only be created via admin API",
		})
		if writeErr := client.writeJSON(resp); writeErr != nil {
			log.Printf("⚠️ Failed to send proxy response [%s]: %v", client.ID, writeErr)
		}
		return
	}

	if !s.isCurrentLive(client.ID, client.generation) {
		if err := s.waitForCurrentDataReady(client, s.sessions.pendingDataTimeout); err != nil {
			log.Printf("⚠️ Failed while waiting for data channel readiness before proxy creation [%s]: %v", client.ID, err)
			resp, _ := protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
				Name:    req.Name,
				Success: false,
				Message: err.Error(),
			})
			if writeErr := client.writeJSON(resp); writeErr != nil {
				log.Printf("⚠️ Failed to send proxy response [%s]: %v", client.ID, writeErr)
			}
			return
		}
	}

	err := s.StartProxy(client, req)
	var resp *protocol.Message
	if err != nil {
		log.Printf("❌ Failed to create proxy [%s]: %v", client.ID, err)
		resp, _ = protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
			Name:    req.Name,
			Success: false,
			Message: err.Error(),
		})
	} else {
		client.proxyMu.RLock()
		tunnel := client.proxies[req.Name]
		actualPort := tunnel.Config.RemotePort
		config := tunnel.Config
		client.proxyMu.RUnlock()

		resp, _ = protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
			ID:              config.ID,
			Name:            config.Name,
			Success:         true,
			Message:         "proxy tunnel created successfully",
			RemotePort:      actualPort,
			TransportPolicy: config.TransportPolicy,
			ActualTransport: config.ActualTransport,
		})

		s.emitTunnelChanged(client.ID, config, "created_by_client")
	}

	if err := client.writeJSON(resp); err != nil {
		log.Printf("⚠️ Failed to send proxy response [%s]: %v", client.ID, err)
	}
}

func (s *Server) handleProxyProvisionAckMessage(client *ClientConn, msg protocol.Message) {
	var unifiedAck protocol.TunnelProvisionAck
	if err := msg.ParsePayload(&unifiedAck); err == nil && unifiedAck.TunnelID != "" {
		resp := provisionAckResult{
			name:     unifiedAck.TunnelID,
			accepted: unifiedAck.Accepted,
			message:  unifiedAck.Message,
			revision: uint64(unifiedAck.Revision),
			role:     unifiedAck.Role,
		}
		if s.resolveTunnelProvisionAckWaiter(client.ID, client.generation, resp) {
			return
		}
		log.Printf("📩 Received unmatched tunnel provisioning ack [%s]: tunnel_id=%s role=%s accepted=%v", client.ID, unifiedAck.TunnelID, unifiedAck.Role, unifiedAck.Accepted)
		return
	}

	var ack protocol.ProxyProvisionAck
	if err := msg.ParsePayload(&ack); err != nil {
		log.Printf("⚠️ Failed to parse provisioning ack [%s]: %v", client.ID, err)
		return
	}

	resp := provisionAckResult{
		name:     ack.Name,
		accepted: ack.Accepted,
		message:  ack.Message,
		revision: ack.ProvisionRevision,
	}
	if s.resolveTunnelProvisionAckWaiter(client.ID, client.generation, resp) {
		return
	}
	log.Printf("📩 Received unmatched provisioning ack [%s]: name=%s accepted=%v", client.ID, resp.name, resp.accepted)
}

func (s *Server) handleTunnelRuntimeReportMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}

	var report protocol.TunnelRuntimeReport
	if err := msg.ParsePayload(&report); err != nil {
		log.Printf("⚠️ Failed to parse tunnel runtime report [%s]: %v", client.ID, err)
		return
	}
	if report.TunnelID == "" || report.Revision <= 0 || report.Role == "" {
		log.Printf("⚠️ Ignoring incomplete tunnel runtime report [%s]: tunnel_id=%q role=%q revision=%d", client.ID, report.TunnelID, report.Role, report.Revision)
		return
	}

	stored, ok, err := s.findStoredTunnelByID(report.TunnelID)
	if err != nil {
		log.Printf("⚠️ Ignoring tunnel runtime report [%s]: failed to load tunnel %q: %v", client.ID, report.TunnelID, err)
		return
	}
	if !ok {
		log.Printf("⚠️ Ignoring tunnel runtime report [%s]: unknown tunnel_id=%s role=%s revision=%d", client.ID, report.TunnelID, report.Role, report.Revision)
		return
	}
	if stored.Revision != report.Revision {
		log.Printf("⚠️ Ignoring stale tunnel runtime report [%s]: tunnel_id=%s role=%s report_revision=%d current_revision=%d", client.ID, report.TunnelID, report.Role, report.Revision, stored.Revision)
		return
	}
	if !runtimeReportMatchesStoredTunnel(client.ID, stored, report) {
		log.Printf("⚠️ Ignoring tunnel runtime report with unexpected role/client [%s]: tunnel_id=%s role=%s revision=%d", client.ID, report.TunnelID, report.Role, report.Revision)
		return
	}

	s.unifiedRuntime.recordReport(client.ID, report, time.Now())
	s.emitTunnelChanged(stored.OwnerClientID, storedTunnelToProxyConfig(stored), "runtime_report")
	s.scheduleUnifiedTunnelReconcile(stored, "runtime_report")
	log.Printf("📩 Received tunnel runtime report [%s]: tunnel_id=%s role=%s revision=%d message=%q", client.ID, report.TunnelID, report.Role, report.Revision, report.Message)
}

func runtimeReportMatchesStoredTunnel(clientID string, stored StoredTunnel, report protocol.TunnelRuntimeReport) bool {
	switch report.Role {
	case protocol.DataStreamRoleTarget:
		return clientID != "" && clientID == stored.Target.ClientID
	case protocol.DataStreamRoleIngress:
		return stored.Ingress.Location == tunnelEndpointLocationClient &&
			clientID != "" &&
			clientID == stored.Ingress.ClientID
	default:
		return false
	}
}

func (s *Server) handleTunnelPreflightResponseMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}

	var resp protocol.TunnelPreflightResponse
	if err := msg.ParsePayload(&resp); err != nil {
		log.Printf("⚠️ Failed to parse tunnel preflight response [%s]: %v", client.ID, err)
		return
	}
	if resp.RequestID == "" {
		log.Printf("⚠️ Ignoring tunnel preflight response without request_id [%s]: tunnel_id=%s role=%s", client.ID, resp.TunnelID, resp.Role)
		return
	}
	if s.tunnels.resolvePreflightWaiter(client.ID, client.generation, resp) {
		return
	}

	log.Printf("📩 Received unmatched tunnel preflight response [%s]: request_id=%s tunnel_id=%s role=%s accepted=%v code=%s", client.ID, resp.RequestID, resp.TunnelID, resp.Role, resp.Accepted, resp.Code)
}

func (s *Server) handleProxyCloseMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}

	var req protocol.ProxyCloseRequest
	if err := msg.ParsePayload(&req); err != nil {
		log.Printf("⚠️ Failed to parse proxy close request [%s]: %v", client.ID, err)
		return
	}

	config := protocol.ProxyConfig{
		Name:     req.Name,
		ClientID: client.ID,
	}
	client.proxyMu.RLock()
	if tunnel, exists := client.proxies[req.Name]; exists {
		config = tunnel.Config
	}
	client.proxyMu.RUnlock()

	if err := s.StopProxy(client, req.Name); err != nil {
		log.Printf("⚠️ Failed to close proxy [%s]: %v", client.ID, err)
		return
	}

	setProxyConfigStates(&config, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, "")
	s.emitTunnelChanged(client.ID, config, "closed_by_client")
}
