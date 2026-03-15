package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"netsgo/pkg/protocol"
)

func (s *Server) createManagedTunnel(client *ClientConn, req protocol.ProxyNewRequest, persist bool, action string) (protocol.ProxyConfig, error) {
	if err := s.StartProxy(client, req); err != nil {
		return protocol.ProxyConfig{}, err
	}

	tunnel, err := s.mustGetTunnel(client, req.Name)
	if err != nil {
		_ = s.StopProxy(client, req.Name)
		return protocol.ProxyConfig{}, err
	}

	if persist && s.store != nil {
		if err := s.store.AddTunnel(storedTunnelFromRuntime(client, tunnel)); err != nil {
			_ = s.StopProxy(client, req.Name)
			return protocol.ProxyConfig{}, err
		}
	}

	if err := s.notifyClientProxyNew(client, tunnel.Config.ToProxyNewRequest()); err != nil {
		if persist && s.store != nil {
			_ = s.store.RemoveTunnel(client.ID, req.Name)
		}
		_ = s.StopProxy(client, req.Name)
		return protocol.ProxyConfig{}, err
	}

	s.emitTunnelChanged(client.ID, tunnel.Config, action)
	return tunnel.Config, nil
}

func (s *Server) pauseManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	previousStatus := tunnel.Config.Status
	if err := s.PauseProxy(client, name); err != nil {
		return err
	}

	if s.store != nil {
		if err := s.store.UpdateStatus(client.ID, name, protocol.ProxyStatusPaused); err != nil {
			_ = s.ResumeProxy(client, name)
			return err
		}
	}

	if err := s.notifyClientProxyClose(client, name, "paused"); err != nil {
		if s.store != nil {
			_ = s.store.UpdateStatus(client.ID, name, previousStatus)
		}
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
	if err := s.ResumeProxy(client, name); err != nil {
		return err
	}

	if s.store != nil {
		if err := s.store.UpdateStatus(client.ID, name, protocol.ProxyStatusActive); err != nil {
			_ = s.PauseProxy(client, name)
			s.setTunnelStatus(client, name, previousStatus)
			return err
		}
	}

	updated, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	if err := s.notifyClientProxyNew(client, updated.Config.ToProxyNewRequest()); err != nil {
		if s.store != nil {
			_ = s.store.UpdateStatus(client.ID, name, previousStatus)
		}
		_ = s.PauseProxy(client, name)
		s.setTunnelStatus(client, name, previousStatus)
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

	if s.store != nil {
		if err := s.store.UpdateStatus(client.ID, name, protocol.ProxyStatusStopped); err != nil {
			client.proxyMu.Lock()
			if current, ok := client.proxies[name]; ok {
				current.Config.Status = previousStatus
			}
			client.proxyMu.Unlock()
			if pausedDuringStop {
				_ = s.ResumeProxy(client, name)
			}
			return err
		}
	}

	if pausedDuringStop {
		if err := s.notifyClientProxyClose(client, name, "stopped"); err != nil {
			if s.store != nil {
				_ = s.store.UpdateStatus(client.ID, name, previousStatus)
			}
			client.proxyMu.Lock()
			if current, ok := client.proxies[name]; ok {
				current.Config.Status = previousStatus
			}
			client.proxyMu.Unlock()
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

func (s *Server) restoreManagedTunnel(client *ClientConn, stored StoredTunnel) error {
	_, err := s.createManagedTunnel(client, stored.ProxyNewRequest, false, "restored")
	return err
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

func (s *Server) setTunnelStatus(client *ClientConn, name, status string) {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()
	if tunnel, ok := client.proxies[name]; ok {
		tunnel.Config.Status = status
	}
}

func storedTunnelFromRuntime(client *ClientConn, tunnel *ProxyTunnel) StoredTunnel {
	return StoredTunnel{
		ProxyNewRequest: tunnel.Config.ToProxyNewRequest(),
		Status:          tunnel.Config.Status,
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
	value, ok := s.clients.Load(clientID)
	if !ok {
		http.Error(w, `{"error":"client not found"}`, http.StatusNotFound)
		return nil, false
	}
	return value.(*ClientConn), true
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
	client.mu.Lock()
	if client.conn != nil {
		_ = client.conn.Close()
	}
	client.mu.Unlock()

	client.dataMu.Lock()
	if client.dataSession != nil && !client.dataSession.IsClosed() {
		_ = client.dataSession.Close()
	}
	client.dataMu.Unlock()

	s.PauseAllProxies(client)
}

func encodeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("⚠️ JSON 响应编码失败: %v", err)
	}
}
