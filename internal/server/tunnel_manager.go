package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"netsgo/pkg/protocol"
)

func tunnelProvisionErrorMessage(err error) string {
	var rejected *tunnelReadyRejectedError
	switch {
	case errors.As(err, &rejected):
		if rejected.message != "" {
			return rejected.message
		}
		return "client 拒绝隧道初始化"
	case errors.Is(err, errTunnelReadyTimeout):
		return "等待 client ready 超时"
	case errors.Is(err, errTunnelReadyCancelled):
		return "逻辑会话已失效"
	default:
		return err.Error()
	}
}

func (s *Server) markPendingTunnelErrorIfCurrent(client *ClientConn, name, message string) {
	if !s.isCurrentGeneration(client.ID, client.generation) {
		return
	}
	config, ok := s.setTunnelState(client, name, protocol.ProxyStatusError, message)
	if !ok {
		return
	}
	_ = s.persistTunnelState(client.ID, name, protocol.ProxyStatusError, message)
	s.emitTunnelChanged(client.ID, config, "error")
	_ = s.notifyClientProxyClose(client, name, "provision_failed")
}

func (s *Server) createManagedTunnel(client *ClientConn, req protocol.ProxyNewRequest, persist bool, action string) (protocol.ProxyConfig, error) {
	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyStatusPending)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	s.emitTunnelChanged(client.ID, tunnel.Config, "pending")

	if _, err := s.waitForTunnelReady(client, tunnel.Config.ToProxyNewRequest()); err != nil {
		if s.isCurrentGeneration(client.ID, client.generation) {
			s.removeTunnelRuntime(client, req.Name)
			_ = s.notifyClientProxyClose(client, req.Name, "provision_failed")
		}
		return protocol.ProxyConfig{}, err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return protocol.ProxyConfig{}, errTunnelReadyCancelled
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		if s.isCurrentGeneration(client.ID, client.generation) {
			s.removeTunnelRuntime(client, req.Name)
			_ = s.notifyClientProxyClose(client, req.Name, "provision_failed")
		}
		return protocol.ProxyConfig{}, err
	}

	updated, err := s.mustGetTunnel(client, req.Name)
	if err != nil {
		s.removeTunnelRuntime(client, req.Name)
		return protocol.ProxyConfig{}, err
	}

	if persist && s.store != nil {
		if err := s.store.AddTunnel(storedTunnelFromRuntime(client, updated)); err != nil {
			s.removeTunnelRuntime(client, req.Name)
			_ = s.notifyClientProxyClose(client, req.Name, "provision_failed")
			return protocol.ProxyConfig{}, err
		}
	}

	s.emitTunnelChanged(client.ID, updated.Config, action)
	return updated.Config, nil
}

func (s *Server) pauseManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	previousStatus := tunnel.Config.Status
	previousError := tunnel.Config.Error
	if err := s.PauseProxy(client, name); err != nil {
		return err
	}

	if err := s.persistTunnelState(client.ID, name, protocol.ProxyStatusPaused, ""); err != nil {
		_ = s.ResumeProxy(client, name)
		return err
	}

	if err := s.notifyClientProxyClose(client, name, "paused"); err != nil {
		_ = s.persistTunnelState(client.ID, name, previousStatus, previousError)
		_, _ = s.setTunnelState(client, name, previousStatus, previousError)
		_ = s.ResumeProxy(client, name)
		return err
	}

	updated, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}
	s.emitTunnelChanged(client.ID, updated.Config, "paused")
	return nil
}

func (s *Server) resumeManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	previousStatus := tunnel.Config.Status
	previousError := tunnel.Config.Error
	pendingConfig, err := s.stageTunnelPending(client, name)
	if err != nil {
		return err
	}
	if err := s.persistTunnelState(client.ID, name, protocol.ProxyStatusPending, ""); err != nil {
		_, _ = s.setTunnelState(client, name, previousStatus, previousError)
		return err
	}
	s.emitTunnelChanged(client.ID, pendingConfig, "pending")

	if _, err := s.waitForTunnelReady(client, pendingConfig.ToProxyNewRequest()); err != nil {
		if errors.Is(err, errTunnelReadyCancelled) {
			return err
		}
		s.markPendingTunnelErrorIfCurrent(client, name, tunnelProvisionErrorMessage(err))
		return err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return errTunnelReadyCancelled
	}

	if err := s.ResumeProxy(client, name); err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousStatus, previousError)
		return err
	}

	if err := s.persistTunnelState(client.ID, name, protocol.ProxyStatusActive, ""); err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousStatus, previousError)
		return err
	}

	updated, err := s.mustGetTunnel(client, name)
	if err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousStatus, previousError)
		return err
	}

	s.emitTunnelChanged(client.ID, updated.Config, "resumed")
	return nil
}

func (s *Server) stopManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	previousStatus := tunnel.Config.Status
	previousError := tunnel.Config.Error
	pausedDuringStop := false
	if tunnel.Config.Status == protocol.ProxyStatusActive {
		if err := s.PauseProxy(client, name); err != nil {
			return err
		}
		pausedDuringStop = true
	}

	client.proxyMu.Lock()
	if current, ok := client.proxies[name]; ok {
		current.Config.Status = protocol.ProxyStatusStopped
	}
	client.proxyMu.Unlock()

	_, _ = s.setTunnelState(client, name, protocol.ProxyStatusStopped, "")
	if err := s.persistTunnelState(client.ID, name, protocol.ProxyStatusStopped, ""); err != nil {
		_, _ = s.setTunnelState(client, name, previousStatus, previousError)
		if pausedDuringStop {
			_ = s.ResumeProxy(client, name)
		}
		return err
	}

	if pausedDuringStop {
		if err := s.notifyClientProxyClose(client, name, "stopped"); err != nil {
			_ = s.persistTunnelState(client.ID, name, previousStatus, previousError)
			_, _ = s.setTunnelState(client, name, previousStatus, previousError)
			_ = s.ResumeProxy(client, name)
			return err
		}
	}

	updated, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}
	s.emitTunnelChanged(client.ID, updated.Config, "stopped")
	return nil
}

func (s *Server) deleteManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	client.proxyMu.Lock()
	delete(client.proxies, name)
	client.proxyMu.Unlock()

	if s.store != nil {
		if err := s.store.RemoveTunnel(client.ID, name); err != nil {
			client.proxyMu.Lock()
			client.proxies[name] = tunnel
			client.proxyMu.Unlock()
			return err
		}
	}

	s.emitTunnelChanged(client.ID, protocol.ProxyConfig{
		Name:     tunnel.Config.Name,
		Type:     tunnel.Config.Type,
		ClientID: client.ID,
		Status:   protocol.ProxyStatusStopped,
	}, "deleted")
	return nil
}

func (s *Server) updateManagedTunnel(client *ClientConn, name string, localIP string, localPort, remotePort int, domain string) (protocol.ProxyConfig, error) {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}

	wasError := tunnel.Config.Status == protocol.ProxyStatusError
	tunnelType := tunnel.Config.Type
	req := protocol.ProxyNewRequest{
		Name:       name,
		Type:       tunnelType,
		LocalIP:    localIP,
		LocalPort:  localPort,
		RemotePort: remotePort,
		Domain:     domain,
	}
	if err := s.validateProxyRequestWithExclusions(client, req, name, client.ID); err != nil {
		return protocol.ProxyConfig{}, err
	}

	// 更新运行时内存中的隧道配置
	client.proxyMu.Lock()
	tunnel.Config.LocalIP = localIP
	tunnel.Config.LocalPort = localPort
	tunnel.Config.RemotePort = remotePort
	tunnel.Config.Domain = domain
	if wasError {
		tunnel.Config.Status = protocol.ProxyStatusPaused
		tunnel.Config.Error = ""
	}
	updated := tunnel.Config
	client.proxyMu.Unlock()

	// 持久化配置变更到存储
	if s.store != nil {
		if err := s.store.UpdateTunnel(client.ID, name, localIP, localPort, remotePort, domain); err != nil {
			return protocol.ProxyConfig{}, err
		}
	}

	// 异常隧道编辑后自动重新启动：删掉旧的占位记录，重新创建隧道
	if wasError {
		client.proxyMu.Lock()
		delete(client.proxies, name)
		client.proxyMu.Unlock()

		config, err := s.createManagedTunnel(client, req, false, "updated")
		if err != nil {
			// 启动失败 → 放回 error 状态的占位记录
			errorConfig := s.upsertTunnelPlaceholder(client, req, protocol.ProxyStatusError, err.Error())
			_ = s.persistTunnelState(client.ID, name, protocol.ProxyStatusError, err.Error())
			s.emitTunnelChanged(client.ID, errorConfig, "updated")
			return errorConfig, nil // 返回 error 状态但不报 API 错误
		}
		// 更新持久化状态为 active
		_ = s.persistTunnelState(client.ID, name, protocol.ProxyStatusActive, "")
		return config, nil
	}

	s.emitTunnelChanged(client.ID, updated, "updated")
	return updated, nil
}

func (s *Server) restoreManagedTunnel(client *ClientConn, stored StoredTunnel) error {
	tunnel, err := s.prepareProxyTunnel(client, stored.ProxyNewRequest, protocol.ProxyStatusPending)
	if err != nil {
		return err
	}
	if err := s.persistTunnelState(client.ID, stored.Name, protocol.ProxyStatusPending, ""); err != nil {
		s.removeTunnelRuntime(client, stored.Name)
		return err
	}
	s.emitTunnelChanged(client.ID, tunnel.Config, "pending")

	if _, err := s.waitForTunnelReady(client, tunnel.Config.ToProxyNewRequest()); err != nil {
		if errors.Is(err, errTunnelReadyCancelled) {
			return err
		}
		s.markPendingTunnelErrorIfCurrent(client, stored.Name, tunnelProvisionErrorMessage(err))
		return err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return errTunnelReadyCancelled
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		s.failRestoredTunnelAfterReady(client, stored, err.Error())
		return err
	}
	if err := s.persistTunnelState(client.ID, stored.Name, protocol.ProxyStatusActive, ""); err != nil {
		s.failRestoredTunnelAfterReady(client, stored, err.Error())
		return err
	}

	updated, err := s.mustGetTunnel(client, stored.Name)
	if err != nil {
		s.failRestoredTunnelAfterReady(client, stored, err.Error())
		return err
	}
	s.emitTunnelChanged(client.ID, updated.Config, "restored")
	return nil
}

func (s *Server) mustGetTunnel(client *ClientConn, name string) (*ProxyTunnel, error) {
	client.proxyMu.RLock()
	defer client.proxyMu.RUnlock()

	tunnel, ok := client.proxies[name]
	if !ok {
		return nil, fmt.Errorf("隧道 %q 不存在", name)
	}
	return tunnel, nil
}

func (s *Server) setTunnelState(client *ClientConn, name, status, errMsg string) (protocol.ProxyConfig, bool) {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()
	tunnel, ok := client.proxies[name]
	if !ok {
		return protocol.ProxyConfig{}, false
	}
	tunnel.Config.Status = status
	tunnel.Config.Error = storedTunnelErrorForStatus(status, errMsg)
	return tunnel.Config, true
}

func (s *Server) persistTunnelState(clientID, name, status, errMsg string) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpdateState(clientID, name, status, errMsg)
}

func tunnelChangedActionForStatus(status string) string {
	switch status {
	case protocol.ProxyStatusPaused:
		return "paused"
	case protocol.ProxyStatusStopped:
		return "stopped"
	case protocol.ProxyStatusError:
		return "error"
	case protocol.ProxyStatusPending:
		return "pending"
	case protocol.ProxyStatusActive:
		return "active"
	default:
		return "updated"
	}
}

func (s *Server) rollbackResumedTunnelAfterReady(client *ClientConn, name, previousStatus, previousError string) {
	tunnel, err := s.mustGetTunnel(client, name)
	if err == nil && tunnel.Config.Status == protocol.ProxyStatusActive {
		_ = s.PauseProxy(client, name)
	}
	config, ok := s.setTunnelState(client, name, previousStatus, previousError)
	if !ok {
		return
	}
	_ = s.persistTunnelState(client.ID, name, previousStatus, previousError)
	_ = s.notifyClientProxyClose(client, name, "provision_failed")
	s.emitTunnelChanged(client.ID, config, tunnelChangedActionForStatus(previousStatus))
}

func (s *Server) upsertTunnelPlaceholder(client *ClientConn, req protocol.ProxyNewRequest, status, errMsg string) protocol.ProxyConfig {
	config := protocol.ProxyConfig{
		Name:       req.Name,
		Type:       req.Type,
		LocalIP:    req.LocalIP,
		LocalPort:  req.LocalPort,
		RemotePort: req.RemotePort,
		Domain:     req.Domain,
		ClientID:   client.ID,
		Status:     status,
		Error:      storedTunnelErrorForStatus(status, errMsg),
	}
	client.proxyMu.Lock()
	if client.proxies == nil {
		client.proxies = make(map[string]*ProxyTunnel)
	}
	client.proxies[req.Name] = &ProxyTunnel{
		Config: config,
		done:   make(chan struct{}),
	}
	client.proxyMu.Unlock()
	return config
}

func (s *Server) failRestoredTunnelAfterReady(client *ClientConn, stored StoredTunnel, message string) {
	s.removeTunnelRuntime(client, stored.Name)
	_ = s.notifyClientProxyClose(client, stored.Name, "provision_failed")
	config := s.upsertTunnelPlaceholder(client, stored.ProxyNewRequest, protocol.ProxyStatusError, message)
	_ = s.persistTunnelState(client.ID, stored.Name, protocol.ProxyStatusError, message)
	s.emitTunnelChanged(client.ID, config, "error")
}

func storedTunnelFromRuntime(client *ClientConn, tunnel *ProxyTunnel) StoredTunnel {
	return StoredTunnel{
		ProxyNewRequest: tunnel.Config.ToProxyNewRequest(),
		Status:          tunnel.Config.Status,
		Error:           tunnel.Config.Error,
		ClientID:        client.ID,
		Hostname:        client.Info.Hostname,
		Binding:         TunnelBindingClientID,
	}
}

func (s *Server) notifyClientProxyNew(client *ClientConn, req protocol.ProxyNewRequest) error {
	message, err := protocol.NewMessage(protocol.MsgTypeProxyNew, req)
	if err != nil {
		return err
	}
	return s.writeControlMessage(client, message)
}

func (s *Server) notifyClientProxyClose(client *ClientConn, name, reason string) error {
	message, err := protocol.NewMessage(protocol.MsgTypeProxyClose, protocol.ProxyCloseRequest{
		Name:   name,
		Reason: reason,
	})
	if err != nil {
		return err
	}
	return s.writeControlMessage(client, message)
}

func (s *Server) writeControlMessage(client *ClientConn, message *protocol.Message) error {
	if client.generation != 0 && !s.isCurrentLive(client.ID, client.generation) {
		return fmt.Errorf("client %s 当前不处于 live 会话", client.ID)
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	if client.conn == nil {
		return fmt.Errorf("client %s 控制通道不可用", client.ID)
	}
	if err := client.conn.WriteJSON(message); err != nil {
		return fmt.Errorf("写入控制消息失败: %w", err)
	}
	return nil
}

func (s *Server) emitTunnelChanged(clientID string, tunnel protocol.ProxyConfig, action string) {
	payload := map[string]any{
		"client_id": clientID,
		"action":    action,
		"tunnel":    tunnel,
	}
	s.events.PublishJSON("tunnel_changed", payload)
}

func (s *Server) readClientFromPath(w http.ResponseWriter, r *http.Request) (*ClientConn, bool) {
	clientID := r.PathValue("id")
	client, ok := s.loadLiveClient(clientID)
	if !ok {
		http.Error(w, `{"error":"client not found"}`, http.StatusNotFound)
		return nil, false
	}
	return client, true
}

func (s *Server) migrateLegacyTunnels(client *ClientConn) (int, error) {
	if s.store == nil || s.adminStore == nil {
		return 0, nil
	}
	if s.adminStore.CountClientsByHostname(client.Info.Hostname) != 1 {
		pending := s.store.GetLegacyTunnelsByHostname(client.Info.Hostname)
		if len(pending) > 0 {
			log.Printf("⚠️ 主机名 %s 存在 %d 个注册 Client，跳过 legacy 隧道自动迁移", client.Info.Hostname, len(pending))
		}
		return 0, nil
	}
	return s.store.MigrateLegacyTunnels(client.Info.Hostname, client.ID)
}

func (s *Server) forceDisconnectClient(client *ClientConn) {
	_ = s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "force_disconnect")
}

func encodeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("⚠️ JSON 响应编码失败: %v", err)
	}
}

// affectedTunnel 描述一条受端口白名单变更影响的隧道
type affectedTunnel struct {
	ClientID    string `json:"client_id"`
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name,omitempty"`
	TunnelName  string `json:"tunnel_name"`
	RemotePort  int    `json:"remote_port"`
	Status      string `json:"status"`
}

// isPortInRanges 检查端口是否在给定的白名单范围内
func isPortInRanges(port int, ranges []PortRange) bool {
	for _, pr := range ranges {
		if port >= pr.Start && port <= pr.End {
			return true
		}
	}
	return false
}

// findTunnelsAffectedByPortChange 找出所有会被新端口白名单规则影响的隧道
// 扫描运行时内存中的隧道 + 持久化存储中的隧道（离线客户端的隧道）
func (s *Server) findTunnelsAffectedByPortChange(newPorts []PortRange) []affectedTunnel {
	// 新白名单为空 → 不限制端口，不会有受影响的隧道
	if len(newPorts) == 0 {
		return []affectedTunnel{}
	}

	affected := []affectedTunnel{}
	seen := map[string]bool{} // key: "clientID:tunnelName"

	// 1) 扫描运行时内存中的隧道（在线客户端）
	s.clients.Range(func(_, value any) bool {
		client := value.(*ClientConn)
		client.RangeProxies(func(name string, tunnel *ProxyTunnel) bool {
			if tunnel.Config.RemotePort != 0 && !isPortInRanges(tunnel.Config.RemotePort, newPorts) {
				// 已经是 error 状态的不重复通报（除非端口也变了）
				if tunnel.Config.Status == protocol.ProxyStatusError {
					return true
				}
				key := client.ID + ":" + name
				seen[key] = true
				// 尝试获取 display_name
				displayName := ""
				if s.adminStore != nil {
					if reg, ok := s.adminStore.GetRegisteredClient(client.ID); ok {
						displayName = reg.DisplayName
					}
				}
				affected = append(affected, affectedTunnel{
					ClientID:    client.ID,
					Hostname:    client.Info.Hostname,
					DisplayName: displayName,
					TunnelName:  name,
					RemotePort:  tunnel.Config.RemotePort,
					Status:      tunnel.Config.Status,
				})
			}
			return true
		})
		return true
	})

	// 2) 扫描持久化存储中的隧道（包含离线客户端的隧道）
	if s.store != nil {
		allStored := s.store.GetAllTunnels()
		for _, st := range allStored {
			if st.RemotePort == 0 {
				continue
			}
			if st.Status == protocol.ProxyStatusError {
				continue
			}
			key := st.ClientID + ":" + st.Name
			if seen[key] {
				continue // 已在运行时中统计过
			}
			if !isPortInRanges(st.RemotePort, newPorts) {
				hostname := st.Hostname
				displayName := ""
				// 尝试从 adminStore 获取更详细的主机名和展示名
				if s.adminStore != nil && st.ClientID != "" {
					if reg, ok := s.adminStore.GetRegisteredClient(st.ClientID); ok {
						hostname = reg.Info.Hostname
						displayName = reg.DisplayName
					}
				}
				affected = append(affected, affectedTunnel{
					ClientID:    st.ClientID,
					Hostname:    hostname,
					DisplayName: displayName,
					TunnelName:  st.Name,
					RemotePort:  st.RemotePort,
					Status:      st.Status,
				})
			}
		}
	}

	return affected
}

// markTunnelsPortNotAllowed 将受端口白名单变更影响的隧道标记为 error 状态
func (s *Server) markTunnelsPortNotAllowed(affected []affectedTunnel) {
	for _, a := range affected {
		errMsg := fmt.Sprintf("端口 %d 不在允许范围内", a.RemotePort)

		// 更新运行时状态（在线客户端）
		if value, ok := s.clients.Load(a.ClientID); ok {
			client := value.(*ClientConn)
			client.proxyMu.Lock()
			if tunnel, exists := client.proxies[a.TunnelName]; exists {
				// 如果隧道是 active 状态，先关闭 listener
				if tunnel.Config.Status == protocol.ProxyStatusActive {
					tunnel.once.Do(func() {
						close(tunnel.done)
						if tunnel.UDPState != nil {
							tunnel.UDPState.Close()
						}
						if tunnel.Listener != nil {
							tunnel.Listener.Close()
						}
					})
					// 通知客户端关闭隧道
					go func(c *ClientConn, name string) {
						_ = s.notifyClientProxyClose(c, name, "port_not_allowed")
					}(client, a.TunnelName)
				}
				tunnel.Config.Status = protocol.ProxyStatusError
				tunnel.Config.Error = errMsg
			}
			client.proxyMu.Unlock()

			// 发送 tunnel_changed 事件
			s.emitTunnelChanged(a.ClientID, protocol.ProxyConfig{
				Name:       a.TunnelName,
				RemotePort: a.RemotePort,
				ClientID:   a.ClientID,
				Status:     protocol.ProxyStatusError,
				Error:      errMsg,
			}, "port_not_allowed")
		}

		// 更新持久化状态
		_ = s.persistTunnelState(a.ClientID, a.TunnelName, protocol.ProxyStatusError, errMsg)

		log.Printf("⚠️ 隧道 %s (端口 %d, 客户端 %s) 因端口白名单变更被标记为异常",
			a.TunnelName, a.RemotePort, a.ClientID)
	}
}
