package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"netsgo/pkg/protocol"
)

func (s *Server) createManagedTunnel(agent *AgentConn, req protocol.ProxyNewRequest, persist bool, action string) (protocol.ProxyConfig, error) {
	if err := s.StartProxy(agent, req); err != nil {
		return protocol.ProxyConfig{}, err
	}

	tunnel, err := s.mustGetTunnel(agent, req.Name)
	if err != nil {
		_ = s.StopProxy(agent, req.Name)
		return protocol.ProxyConfig{}, err
	}

	if persist && s.store != nil {
		if err := s.store.AddTunnel(storedTunnelFromRuntime(agent, tunnel)); err != nil {
			_ = s.StopProxy(agent, req.Name)
			return protocol.ProxyConfig{}, err
		}
	}

	if err := s.notifyAgentProxyNew(agent, tunnel.Config.ToProxyNewRequest()); err != nil {
		if persist && s.store != nil {
			_ = s.store.RemoveTunnel(agent.ID, req.Name)
		}
		_ = s.StopProxy(agent, req.Name)
		return protocol.ProxyConfig{}, err
	}

	s.emitTunnelChanged(agent.ID, tunnel.Config, action)
	return tunnel.Config, nil
}

func (s *Server) pauseManagedTunnel(agent *AgentConn, name string) error {
	tunnel, err := s.mustGetTunnel(agent, name)
	if err != nil {
		return err
	}

	previousStatus := tunnel.Config.Status
	if err := s.PauseProxy(agent, name); err != nil {
		return err
	}

	if s.store != nil {
		if err := s.store.UpdateStatus(agent.ID, name, protocol.ProxyStatusPaused); err != nil {
			_ = s.ResumeProxy(agent, name)
			return err
		}
	}

	if err := s.notifyAgentProxyClose(agent, name, "paused"); err != nil {
		if s.store != nil {
			_ = s.store.UpdateStatus(agent.ID, name, previousStatus)
		}
		_ = s.ResumeProxy(agent, name)
		return err
	}

	updated, err := s.mustGetTunnel(agent, name)
	if err != nil {
		return err
	}
	s.emitTunnelChanged(agent.ID, updated.Config, "paused")
	return nil
}

func (s *Server) resumeManagedTunnel(agent *AgentConn, name string) error {
	tunnel, err := s.mustGetTunnel(agent, name)
	if err != nil {
		return err
	}

	previousStatus := tunnel.Config.Status
	if err := s.ResumeProxy(agent, name); err != nil {
		return err
	}

	if s.store != nil {
		if err := s.store.UpdateStatus(agent.ID, name, protocol.ProxyStatusActive); err != nil {
			_ = s.PauseProxy(agent, name)
			s.setTunnelStatus(agent, name, previousStatus)
			return err
		}
	}

	updated, err := s.mustGetTunnel(agent, name)
	if err != nil {
		return err
	}

	if err := s.notifyAgentProxyNew(agent, updated.Config.ToProxyNewRequest()); err != nil {
		if s.store != nil {
			_ = s.store.UpdateStatus(agent.ID, name, previousStatus)
		}
		_ = s.PauseProxy(agent, name)
		s.setTunnelStatus(agent, name, previousStatus)
		return err
	}

	s.emitTunnelChanged(agent.ID, updated.Config, "resumed")
	return nil
}

func (s *Server) stopManagedTunnel(agent *AgentConn, name string) error {
	tunnel, err := s.mustGetTunnel(agent, name)
	if err != nil {
		return err
	}

	previousStatus := tunnel.Config.Status
	pausedDuringStop := false
	if tunnel.Config.Status == protocol.ProxyStatusActive {
		if err := s.PauseProxy(agent, name); err != nil {
			return err
		}
		pausedDuringStop = true
	}

	agent.proxyMu.Lock()
	if current, ok := agent.proxies[name]; ok {
		current.Config.Status = protocol.ProxyStatusStopped
	}
	agent.proxyMu.Unlock()

	if s.store != nil {
		if err := s.store.UpdateStatus(agent.ID, name, protocol.ProxyStatusStopped); err != nil {
			agent.proxyMu.Lock()
			if current, ok := agent.proxies[name]; ok {
				current.Config.Status = previousStatus
			}
			agent.proxyMu.Unlock()
			if pausedDuringStop {
				_ = s.ResumeProxy(agent, name)
			}
			return err
		}
	}

	if pausedDuringStop {
		if err := s.notifyAgentProxyClose(agent, name, "stopped"); err != nil {
			if s.store != nil {
				_ = s.store.UpdateStatus(agent.ID, name, previousStatus)
			}
			agent.proxyMu.Lock()
			if current, ok := agent.proxies[name]; ok {
				current.Config.Status = previousStatus
			}
			agent.proxyMu.Unlock()
			_ = s.ResumeProxy(agent, name)
			return err
		}
	}

	updated, err := s.mustGetTunnel(agent, name)
	if err != nil {
		return err
	}
	s.emitTunnelChanged(agent.ID, updated.Config, "stopped")
	return nil
}

func (s *Server) deleteManagedTunnel(agent *AgentConn, name string) error {
	tunnel, err := s.mustGetTunnel(agent, name)
	if err != nil {
		return err
	}

	agent.proxyMu.Lock()
	delete(agent.proxies, name)
	agent.proxyMu.Unlock()

	if s.store != nil {
		if err := s.store.RemoveTunnel(agent.ID, name); err != nil {
			agent.proxyMu.Lock()
			agent.proxies[name] = tunnel
			agent.proxyMu.Unlock()
			return err
		}
	}

	s.emitTunnelChanged(agent.ID, protocol.ProxyConfig{
		Name:    tunnel.Config.Name,
		Type:    tunnel.Config.Type,
		AgentID: agent.ID,
		Status:  protocol.ProxyStatusStopped,
	}, "deleted")
	return nil
}

func (s *Server) restoreManagedTunnel(agent *AgentConn, stored StoredTunnel) error {
	_, err := s.createManagedTunnel(agent, stored.ProxyNewRequest, false, "restored")
	return err
}

func (s *Server) mustGetTunnel(agent *AgentConn, name string) (*ProxyTunnel, error) {
	agent.proxyMu.RLock()
	defer agent.proxyMu.RUnlock()

	tunnel, ok := agent.proxies[name]
	if !ok {
		return nil, fmt.Errorf("隧道 %q 不存在", name)
	}
	return tunnel, nil
}

func (s *Server) setTunnelStatus(agent *AgentConn, name, status string) {
	agent.proxyMu.Lock()
	defer agent.proxyMu.Unlock()
	if tunnel, ok := agent.proxies[name]; ok {
		tunnel.Config.Status = status
	}
}

func storedTunnelFromRuntime(agent *AgentConn, tunnel *ProxyTunnel) StoredTunnel {
	return StoredTunnel{
		ProxyNewRequest: tunnel.Config.ToProxyNewRequest(),
		Status:          tunnel.Config.Status,
		AgentID:         agent.ID,
		Hostname:        agent.Info.Hostname,
		Binding:         TunnelBindingAgentID,
	}
}

func (s *Server) notifyAgentProxyNew(agent *AgentConn, req protocol.ProxyNewRequest) error {
	message, err := protocol.NewMessage(protocol.MsgTypeProxyNew, req)
	if err != nil {
		return err
	}
	return s.writeControlMessage(agent, message)
}

func (s *Server) notifyAgentProxyClose(agent *AgentConn, name, reason string) error {
	message, err := protocol.NewMessage(protocol.MsgTypeProxyClose, protocol.ProxyCloseRequest{
		Name:   name,
		Reason: reason,
	})
	if err != nil {
		return err
	}
	return s.writeControlMessage(agent, message)
}

func (s *Server) writeControlMessage(agent *AgentConn, message *protocol.Message) error {
	agent.mu.Lock()
	defer agent.mu.Unlock()

	if agent.conn == nil {
		return fmt.Errorf("agent %s 控制通道不可用", agent.ID)
	}
	if err := agent.conn.WriteJSON(message); err != nil {
		return fmt.Errorf("写入控制消息失败: %w", err)
	}
	return nil
}

func (s *Server) emitTunnelChanged(agentID string, tunnel protocol.ProxyConfig, action string) {
	payload := map[string]any{
		"agent_id": agentID,
		"action":   action,
		"tunnel":   tunnel,
	}
	s.events.PublishJSON("tunnel_changed", payload)
}

func (s *Server) readAgentFromPath(w http.ResponseWriter, r *http.Request) (*AgentConn, bool) {
	agentID := r.PathValue("id")
	value, ok := s.agents.Load(agentID)
	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return nil, false
	}
	return value.(*AgentConn), true
}

func (s *Server) migrateLegacyTunnels(agent *AgentConn) (int, error) {
	if s.store == nil || s.adminStore == nil {
		return 0, nil
	}
	if s.adminStore.CountAgentsByHostname(agent.Info.Hostname) != 1 {
		pending := s.store.GetLegacyTunnelsByHostname(agent.Info.Hostname)
		if len(pending) > 0 {
			log.Printf("⚠️ 主机名 %s 存在 %d 个注册 Agent，跳过 legacy 隧道自动迁移", agent.Info.Hostname, len(pending))
		}
		return 0, nil
	}
	return s.store.MigrateLegacyTunnels(agent.Info.Hostname, agent.ID)
}

func (s *Server) forceDisconnectAgent(agent *AgentConn) {
	agent.mu.Lock()
	if agent.conn != nil {
		_ = agent.conn.Close()
	}
	agent.mu.Unlock()

	agent.dataMu.Lock()
	if agent.dataSession != nil && !agent.dataSession.IsClosed() {
		_ = agent.dataSession.Close()
	}
	agent.dataMu.Unlock()

	s.PauseAllProxies(agent)
}

func encodeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("⚠️ JSON 响应编码失败: %v", err)
	}
}
