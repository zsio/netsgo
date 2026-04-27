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

	tunnels, err := s.store.GetTunnelsByClientID(client.ID)
	if err != nil {
		log.Printf("⚠️ failed to load tunnels for client %s: %v", client.ID, err)
		return
	}
	if len(tunnels) == 0 {
		return
	}

	restoredCount := 0
	for _, st := range tunnels {
		if !s.isCurrentLive(client.ID, client.generation) {
			return
		}
		if st.RemotePort != 0 && s.auth.adminStore != nil && s.auth.adminStore.IsInitialized() && !s.auth.adminStore.IsPortAllowed(st.RemotePort) {
			log.Printf("⚠️ tunnel %s port %d is outside the currently allowed range, marking as error", st.Name, st.RemotePort)
			errMsg := fmt.Sprintf("port %d is not within the allowed range", st.RemotePort)
			client.proxyMu.Lock()
			config := storedTunnelToProxyConfig(st)
			setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			client.proxies[st.Name] = &ProxyTunnel{
				Config: config,
				limits: newDirectionalBandwidthRuntime(config.BandwidthSettings, realBandwidthClock{}),
				done:   make(chan struct{}),
			}
			client.proxyMu.Unlock()
			_ = s.persistTunnelStates(client.ID, st.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			eventConfig := storedTunnelToProxyConfig(st)
			eventConfig.LocalIP = ""
			eventConfig.LocalPort = 0
			setProxyConfigStates(&eventConfig, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			s.emitTunnelChanged(client.ID, eventConfig, "port_not_allowed")
			restoredCount++
			continue
		}

		switch {
		case st.DesiredState == protocol.ProxyDesiredStateRunning &&
			(st.RuntimeState == protocol.ProxyRuntimeStateExposed || st.RuntimeState == protocol.ProxyRuntimeStatePending || st.RuntimeState == protocol.ProxyRuntimeStateOffline):
			log.Printf("🔄 restoring tunnel: %s (:%d → %s:%d)", st.Name, st.RemotePort, st.LocalIP, st.LocalPort)
			if err := s.restoreManagedTunnel(client, st); err != nil {
				log.Printf("⚠️ failed to restore tunnel [%s]: %v", st.Name, err)
				continue
			}
			restoredCount++

		default:
			config := storedTunnelToProxyConfig(st)
			setProxyConfigStates(&config, st.DesiredState, st.RuntimeState, st.Error)
			client.proxyMu.Lock()
			client.proxies[st.Name] = &ProxyTunnel{
				Config: config,
				limits: newDirectionalBandwidthRuntime(config.BandwidthSettings, realBandwidthClock{}),
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
