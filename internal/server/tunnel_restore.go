package server

import (
	"fmt"
	"log"

	"netsgo/pkg/protocol"
)

func (s *Server) restoreTunnels(client *ClientConn) {
	if s.store == nil {
		return
	}
	if !s.isCurrentLive(client.ID, client.generation) {
		return
	}

	tunnels := s.store.GetTunnelsByClientID(client.ID)
	if len(tunnels) == 0 {
		return
	}

	restoredCount := 0
	for _, st := range tunnels {
		if !s.isCurrentLive(client.ID, client.generation) {
			return
		}
		if st.RemotePort != 0 && s.auth.adminStore != nil && s.auth.adminStore.IsInitialized() && !s.auth.adminStore.IsPortAllowed(st.RemotePort) {
			log.Printf("⚠️ 隧道 %s 端口 %d 不在当前允许范围内，标记为 error", st.Name, st.RemotePort)
			errMsg := fmt.Sprintf("端口 %d 不在允许范围内", st.RemotePort)
			client.proxyMu.Lock()
			config := protocol.ProxyConfig{
				Name:       st.Name,
				Type:       st.Type,
				LocalIP:    st.LocalIP,
				LocalPort:  st.LocalPort,
				RemotePort: st.RemotePort,
				Domain:     st.Domain,
				ClientID:   client.ID,
			}
			setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			client.proxies[st.Name] = &ProxyTunnel{
				Config: config,
				done:   make(chan struct{}),
			}
			client.proxyMu.Unlock()
			_ = s.persistTunnelStates(client.ID, st.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			eventConfig := protocol.ProxyConfig{
				Name:       st.Name,
				Type:       st.Type,
				RemotePort: st.RemotePort,
				Domain:     st.Domain,
				ClientID:   client.ID,
			}
			setProxyConfigStates(&eventConfig, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			s.emitTunnelChanged(client.ID, eventConfig, "port_not_allowed")
			restoredCount++
			continue
		}

		switch {
		case st.DesiredState == protocol.ProxyDesiredStateRunning &&
			(st.RuntimeState == protocol.ProxyRuntimeStateExposed || st.RuntimeState == protocol.ProxyRuntimeStatePending || st.RuntimeState == protocol.ProxyRuntimeStateOffline):
			log.Printf("🔄 恢复隧道: %s (:%d → %s:%d)", st.Name, st.RemotePort, st.LocalIP, st.LocalPort)
			if err := s.restoreManagedTunnel(client, st); err != nil {
				log.Printf("⚠️ 恢复隧道失败 [%s]: %v", st.Name, err)
				continue
			}
			restoredCount++

		default:
			config := protocol.ProxyConfig{
				Name:       st.Name,
				Type:       st.Type,
				LocalIP:    st.LocalIP,
				LocalPort:  st.LocalPort,
				RemotePort: st.RemotePort,
				Domain:     st.Domain,
				ClientID:   client.ID,
			}
			setProxyConfigStates(&config, st.DesiredState, st.RuntimeState, st.Error)
			client.proxyMu.Lock()
			client.proxies[st.Name] = &ProxyTunnel{
				Config: config,
				done:   make(chan struct{}),
			}
			client.proxyMu.Unlock()
			restoredCount++
		}
	}

	if restoredCount > 0 && s.isCurrentLive(client.ID, client.generation) {
		s.events.PublishJSON("tunnel_changed", map[string]any{
			"client_id": client.ID,
			"action":    "restored_batch",
			"count":     restoredCount,
		})
	}
}
