package server

import (
	"errors"
	"log"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

const (
	clientTokenTouchInterval = time.Hour
	clientTokenTouchRetry    = time.Minute
)

// controlLoop continuously processes control-channel messages and returns a
// stable, redacted disconnect cause for lifecycle activity.
func (s *Server) controlLoop(client *ClientConn) clientDisconnectCause {
	client.mu.Lock()
	conn := client.conn
	client.mu.Unlock()
	if conn == nil {
		return clientDisconnectCause{ReasonCode: "transport_error"}
	}

	for {
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			cause := clientDisconnectCauseFromError(err)
			if !cause.Expected {
				log.Printf("⚠️ Client [%s] connection closed unexpectedly: %v", client.ID, err)
			}
			return cause
		}

		s.handleControlMessage(client, msg)
	}
}
func clientDisconnectCauseFromError(err error) clientDisconnectCause {
	cause := clientDisconnectCause{ReasonCode: "transport_error"}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		cause.CloseCode = closeErr.Code
		if closeErr.Code == websocket.CloseNormalClosure {
			cause.ReasonCode = "normal_closure"
			cause.Expected = true
		}
	}
	return cause
}

func (s *Server) handleControlMessage(client *ClientConn, msg protocol.Message) {
	s.touchClientTokenIfDue(client, time.Now())
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
	case protocol.MsgTypeP2PSignal:
		s.withClientRuntimePublication(client, func() { s.handleP2PSignalMessage(client, msg) })
	case protocol.MsgTypeP2PSessionReady, protocol.MsgTypeP2PFailed, protocol.MsgTypeP2PClosed:
		s.withClientRuntimePublication(client, func() { s.handleP2PStatusMessage(client, msg) })
	case protocol.MsgTypeP2PStatsReport:
		s.withClientFinalAccountingPublication(client, func() { s.handleP2PStatsMessage(client, msg) })
	case protocol.MsgTypeP2PCreditDemand:
		s.withClientRuntimePublication(client, func() { s.handleP2PCreditDemandMessage(client, msg) })
	case protocol.MsgTypeP2PCreditGrant:
		s.withClientRuntimePublication(client, func() { s.handleP2PCreditGrantMessage(client, msg) })
	default:
		log.Printf("⚠️ Unknown message type [%s]: %s", client.ID, msg.Type)
	}
}

func (s *Server) touchClientTokenIfDue(client *ClientConn, now time.Time) {
	if s.auth == nil || s.auth.adminStore == nil || client.clientTokenID == "" {
		return
	}

	client.tokenTouchMu.Lock()
	defer client.tokenTouchMu.Unlock()
	if now.Before(client.nextTokenTouch) {
		return
	}
	if err := s.auth.adminStore.TouchToken(client.clientTokenID, client.RemoteAddr); err != nil {
		client.nextTokenTouch = now.Add(clientTokenTouchRetry)
		log.Printf("⚠️ Failed to refresh client token activity [%s]: %v", client.ID, err)
		return
	}
	client.nextTokenTouch = now.Add(clientTokenTouchInterval)
}

func (s *Server) handlePingMessage(client *ClientConn) {
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
	_, releaseOwnerGate, gateErr := s.acquireUserLifecycleRead(client.OwnerUserID, client.OwnerEpoch, true)
	if gateErr != nil {
		return
	}
	s.clientTunnelMutationMu.Lock()
	current, currentOK := s.clients.Load(client.ID)
	if !currentOK || current != client || !client.isLive() || !s.clientLifecycleCurrentLocked(client) {
		s.clientTunnelMutationMu.Unlock()
		releaseOwnerGate()
		return
	}
	defer s.clientTunnelMutationMu.Unlock()
	defer releaseOwnerGate()

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

	s.events.PublishScopedJSON("stats_update", client.OwnerUserID, map[string]any{
		"client_id": client.ID,
		"stats":     stats,
	})
}

func (s *Server) handleProxyCreateMessage(client *ClientConn, msg protocol.Message) {
	// Legacy Client-initiated creation is still a runtime write path. Recheck
	// the resolved owner so a disable racing with an already-open control
	// session cannot create a new tunnel before teardown wins.
	if err := s.ensureClientOwnerOperational(client.OwnerUserID); err != nil {
		log.Printf("⚠️ Rejecting legacy proxy creation for non-operational owner [%s]: %v", client.ID, err)
		s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "owner_not_operational")
		return
	}
	var req protocol.ProxyNewRequest
	if err := msg.ParsePayload(&req); err != nil {
		log.Printf("⚠️ Failed to parse proxy request [%s]: %v", client.ID, err)
		return
	}
	req.ID = ""
	req.IngressBPS = 0
	req.EgressBPS = 0

	if req.Type == protocol.ProxyTypeHTTP || req.Type == protocol.ProxyTypeTLS {
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
				Name: req.Name, Success: false, Message: err.Error(),
			})
			if writeErr := client.writeJSON(resp); writeErr != nil {
				log.Printf("⚠️ Failed to send proxy response [%s]: %v", client.ID, writeErr)
			}
			return
		}
	}

	_, releaseOwnerGate, gateErr := s.acquireUserLifecycleRead(client.OwnerUserID, client.OwnerEpoch, true)
	if gateErr != nil {
		log.Printf("⚠️ Rejecting legacy proxy publication for non-operational owner [%s]: %v", client.ID, gateErr)
		s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "owner_not_operational")
		return
	}
	s.clientTunnelMutationMu.Lock()
	if current, ok := s.clients.Load(client.ID); !ok || current != client || !client.isLive() || !s.clientLifecycleCurrentLocked(client) {
		s.clientTunnelMutationMu.Unlock()
		releaseOwnerGate()
		return
	}

	err := s.StartProxy(client, req)
	var resp *protocol.Message
	if err != nil {
		log.Printf("❌ Failed to create proxy [%s]: %v", client.ID, err)
		resp, _ = protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
			Name: req.Name, Success: false, Message: err.Error(),
		})
	} else {
		client.proxyMu.RLock()
		tunnel := client.proxies[req.Name]
		actualPort := tunnel.Config.RemotePort
		config := tunnel.Config
		client.proxyMu.RUnlock()

		resp, _ = protocol.NewMessage(protocol.MsgTypeProxyCreateResp, protocol.ProxyCreateResponse{
			ID: config.ID, Name: config.Name, Success: true,
			Message: "proxy tunnel created successfully", RemotePort: actualPort,
			TransportPolicy: config.TransportPolicy, ActualTransport: config.ActualTransport,
		})
	}
	s.clientTunnelMutationMu.Unlock()
	releaseOwnerGate()

	if err := client.writeJSON(resp); err != nil {
		log.Printf("⚠️ Failed to send proxy response [%s]: %v", client.ID, err)
	}
}

func (s *Server) handleProxyProvisionAckMessage(client *ClientConn, msg protocol.Message) {
	var unifiedAck protocol.TunnelProvisionAck
	if err := msg.ParsePayload(&unifiedAck); err == nil && unifiedAck.TunnelID != "" {
		resp := provisionAckResult{name: unifiedAck.TunnelID, accepted: unifiedAck.Accepted, message: unifiedAck.Message, revision: uint64(unifiedAck.Revision), role: unifiedAck.Role}
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
	resp := provisionAckResult{name: ack.Name, accepted: ack.Accepted, message: ack.Message, revision: ack.ProvisionRevision}
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

	_, releaseOwnerGate, gateErr := s.acquireUserLifecycleRead(client.OwnerUserID, client.OwnerEpoch, true)
	if gateErr != nil {
		return
	}
	s.clientTunnelMutationMu.Lock()
	currentClient, currentClientOK := s.clients.Load(client.ID)
	currentStored, currentStoredOK, currentStoredErr := s.findStoredTunnelByID(report.TunnelID)
	if !currentClientOK || currentClient != client || !client.isLive() || !s.clientLifecycleCurrentLocked(client) ||
		currentStoredErr != nil || !currentStoredOK || currentStored.Revision != report.Revision ||
		currentStored.OwnerUserID != client.OwnerUserID || !runtimeReportMatchesStoredTunnel(client.ID, currentStored, report) {
		s.clientTunnelMutationMu.Unlock()
		releaseOwnerGate()
		return
	}
	stored = currentStored
	reconcileTask, taskErr := s.newUnifiedTunnelReconcileTaskAtEpoch(stored, "runtime_report", client.OwnerEpoch)
	if taskErr != nil {
		s.clientTunnelMutationMu.Unlock()
		releaseOwnerGate()
		return
	}
	s.unifiedRuntime.recordReport(client.ID, report, time.Now())
	s.emitTunnelChangedIfStored(stored.OwnerClientID, storedTunnelToProxyConfig(stored), "runtime_report")
	s.clientTunnelMutationMu.Unlock()
	releaseOwnerGate()
	s.runUserLifecycleHook("runtime_report_reconcile_captured", client.OwnerUserID)
	s.scheduleCapturedUnifiedTunnelReconcile(stored, reconcileTask)
	log.Printf("📩 Received tunnel runtime report [%s]: tunnel_id=%s role=%s revision=%d message=%q", client.ID, report.TunnelID, report.Role, report.Revision, report.Message)
}

func runtimeReportMatchesStoredTunnel(clientID string, stored StoredTunnel, report protocol.TunnelRuntimeReport) bool {
	switch report.Role {
	case protocol.DataStreamRoleTarget:
		return clientID != "" && clientID == stored.Target.ClientID
	case protocol.DataStreamRoleIngress:
		return stored.Ingress.Location == tunnelEndpointLocationClient && clientID != "" && clientID == stored.Ingress.ClientID
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

	if err := s.StopProxy(client, req.Name); err != nil {
		log.Printf("⚠️ Failed to close proxy [%s]: %v", client.ID, err)
	}
}
