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
				log.Printf("⚠️ Client [%s] 连接异常: %v", client.ID, err)
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
	case protocol.MsgTypeProxyClose:
		s.handleProxyCloseMessage(client, msg)
	default:
		log.Printf("⚠️ 未知消息类型 [%s]: %s", client.ID, msg.Type)
	}
}

func (s *Server) handlePingMessage(client *ClientConn) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}

	pong, _ := protocol.NewMessage(protocol.MsgTypePong, nil)
	if err := client.writeJSON(pong); err != nil {
		log.Printf("⚠️ 发送 Pong 失败 [%s]: %v", client.ID, err)
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
		log.Printf("⚠️ 解析探针数据失败 [%s]: %v", client.ID, err)
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
			log.Printf("⚠️ 持久化 Client 最新状态失败 [%s]: %v", client.ID, err)
		}
	}

	log.Printf("📊 [%s] CPU: %.1f%% | 内存: %.1f%% | 磁盘: %.1f%%",
		info.Hostname, stats.CPUUsage, stats.MemUsage, stats.DiskUsage)

	s.events.PublishJSON("stats_update", map[string]any{
		"client_id": client.ID,
		"stats":     stats,
	})
}

func (s *Server) handleProxyCreateMessage(client *ClientConn, msg protocol.Message) {
	var req protocol.ProxyNewRequest
	if err := msg.ParsePayload(&req); err != nil {
		log.Printf("⚠️ 解析代理请求失败 [%s]: %v", client.ID, err)
		return
	}

	if !s.isCurrentLive(client.ID, client.generation) {
		if err := s.waitForCurrentDataReady(client, s.sessions.pendingDataTimeout); err != nil {
			log.Printf("⚠️ 代理创建等待数据通道就绪失败 [%s]: %v", client.ID, err)
			resp, _ := protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
				Name:    req.Name,
				Success: false,
				Message: err.Error(),
			})
			if writeErr := client.writeJSON(resp); writeErr != nil {
				log.Printf("⚠️ 发送代理响应失败 [%s]: %v", client.ID, writeErr)
			}
			return
		}
	}

	err := s.StartProxy(client, req)
	var resp *protocol.Message
	if err != nil {
		log.Printf("❌ 创建代理失败 [%s]: %v", client.ID, err)
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
			Name:       req.Name,
			Success:    true,
			Message:    "代理隧道创建成功",
			RemotePort: actualPort,
		})

		s.emitTunnelChanged(client.ID, config, "created_by_client")
	}

	if err := client.writeJSON(resp); err != nil {
		log.Printf("⚠️ 发送代理响应失败 [%s]: %v", client.ID, err)
	}
}

func (s *Server) handleProxyProvisionAckMessage(client *ClientConn, msg protocol.Message) {
	var ack protocol.ProxyProvisionAck
	if err := msg.ParsePayload(&ack); err != nil {
		log.Printf("⚠️ 解析 provisioning ack 失败 [%s]: %v", client.ID, err)
		return
	}

	resp := provisionAckResult{
		name:     ack.Name,
		accepted: ack.Accepted,
		message:  ack.Message,
	}
	if s.resolveTunnelProvisionAckWaiter(client.ID, client.generation, resp) {
		return
	}
	log.Printf("📩 收到未匹配的 provisioning ack [%s]: name=%s accepted=%v", client.ID, resp.name, resp.accepted)
}

func (s *Server) handleProxyCloseMessage(client *ClientConn, msg protocol.Message) {
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}

	var req protocol.ProxyCloseRequest
	if err := msg.ParsePayload(&req); err != nil {
		log.Printf("⚠️ 解析关闭代理请求失败 [%s]: %v", client.ID, err)
		return
	}

	if err := s.StopProxy(client, req.Name); err != nil {
		log.Printf("⚠️ 关闭代理失败 [%s]: %v", client.ID, err)
		return
	}

	s.emitTunnelChanged(client.ID, protocol.ProxyConfig{
		Name:         req.Name,
		ClientID:     client.ID,
		DesiredState: protocol.ProxyDesiredStateStopped,
		RuntimeState: protocol.ProxyRuntimeStateIdle,
	}, "closed_by_client")
}
