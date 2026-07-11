package server

import (
	"fmt"
	"log"
	"time"

	"netsgo/pkg/protocol"
)

const (
	restorePlaceholderActionStorageUnavailable = "storage_unavailable"
	restorePlaceholderActionPortNotAllowed     = "port_not_allowed"
	restorePlaceholderActionStopped            = "stopped"
)

func storedTunnelMatchesRestoreSnapshot(current, expected StoredTunnel) bool {
	return current.ID == expected.ID &&
		current.Name == expected.Name &&
		current.Revision == expected.Revision &&
		canonicalDesiredState(current.DesiredState) == canonicalDesiredState(expected.DesiredState) &&
		protocolRuntimeStateFromStorage(current.RuntimeState) == protocolRuntimeStateFromStorage(expected.RuntimeState) &&
		current.Error == expected.Error
}

func (s *Server) restoreTunnelPlaceholderIfCurrent(
	client *ClientConn,
	stored StoredTunnel,
	config protocol.ProxyConfig,
	action string,
) bool {
	if s.store == nil || client == nil || stored.ID == "" || stored.Revision <= 0 {
		return false
	}
	ownerClientID := stored.OwnerClientID
	if ownerClientID == "" {
		ownerClientID = stored.ClientID
	}
	if ownerClientID == "" {
		return false
	}

	releaseRuntimeOperation := s.tunnelRuntimeOps.lock(tunnelRuntimeOperationKey(stored.ID, ownerClientID, stored.Name))
	defer releaseRuntimeOperation()

	current, err := s.store.GetTunnelByIDE(ownerClientID, stored.ID)
	if err != nil || !storedTunnelMatchesRestoreSnapshot(current, stored) {
		return false
	}
	if s.restorePlaceholderBeforeInstallHook != nil {
		s.restorePlaceholderBeforeInstallHook(stored, action)
	}
	current, err = s.store.GetTunnelByIDE(ownerClientID, stored.ID)
	if err != nil || !storedTunnelMatchesRestoreSnapshot(current, stored) {
		return false
	}
	if !s.isCurrentLive(client.ID, client.generation) {
		return false
	}

	expectedStored := stored
	switch action {
	case restorePlaceholderActionStorageUnavailable, restorePlaceholderActionPortNotAllowed:
		updated, updateErr := s.store.UpdateStatesIfCurrent(
			ownerClientID,
			stored.ID,
			stored.Revision,
			protocol.ProxyDesiredStateRunning,
			protocol.ProxyRuntimeStateError,
			config.Error,
		)
		if updateErr != nil {
			log.Printf("⚠️ failed to persist restored tunnel placeholder state for %s/%s revision %d: %v", ownerClientID, stored.ID, stored.Revision, updateErr)
			return false
		}
		if !updated {
			return false
		}
		setStoredTunnelStates(&expectedStored, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, config.Error)
		setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, config.Error)
		config.ActualTransport = protocol.ActualTransportUnknown
	case restorePlaceholderActionStopped:
		// A stopped placeholder reflects the existing stored state and does not
		// perform a storage transition.
	default:
		return false
	}

	placeholder := &ProxyTunnel{
		Config: config,
		limits: newDirectionalBandwidthRuntime(config.BandwidthSettings, realBandwidthClock{}),
		done:   make(chan struct{}),
	}
	initializeTunnelRuntimeFromState(placeholder, client.ID, time.Now())

	client.proxyMu.Lock()
	if client.proxies == nil {
		client.proxies = make(map[string]*ProxyTunnel)
	}
	if existing, exists := client.proxies[stored.Name]; exists {
		if existing.Config.ID != stored.ID || existing.Config.Revision != stored.Revision {
			client.proxyMu.Unlock()
			return false
		}
		closeTunnelRuntimeResources(existing)
	}
	client.proxies[stored.Name] = placeholder
	client.proxyMu.Unlock()

	current, err = s.store.GetTunnelByIDE(ownerClientID, stored.ID)
	if err != nil ||
		!storedTunnelMatchesRestoreSnapshot(current, expectedStored) ||
		!s.isCurrentLive(client.ID, client.generation) {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, placeholder, stored.ID, stored.Revision)
		return false
	}

	if action != restorePlaceholderActionStopped {
		eventConfig := config
		eventConfig.LocalIP = ""
		eventConfig.LocalPort = 0
		s.emitTunnelChangedIfStored(ownerClientID, eventConfig, action)
	}
	return true
}

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
		s.reconcileNonOwnerTunnelsForClient(client.ID, "restore_related")
		return
	}

	restoredCount := 0
	for _, st := range tunnels {
		if !s.isCurrentLive(client.ID, client.generation) {
			return
		}
		if st.DesiredState == protocol.ProxyDesiredStateRunning && st.RemotePort != 0 && s.auth.adminStore != nil {
			initialized, err := s.auth.adminStore.IsInitializedE()
			if err != nil {
				log.Printf("⚠️ failed to read initialization state while restoring tunnel %s, marking as error: %v", st.Name, err)
				errMsg := fmt.Sprintf("storage unavailable while checking allowed ports: %v", err)
				config := storedTunnelToProxyConfig(st)
				setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
				if s.restoreTunnelPlaceholderIfCurrent(client, st, config, restorePlaceholderActionStorageUnavailable) {
					restoredCount++
				}
				continue
			}
			if initialized && !s.auth.adminStore.IsPortAllowed(st.RemotePort) {
				log.Printf("⚠️ tunnel %s port %d is outside the currently allowed range, marking as error", st.Name, st.RemotePort)
				errMsg := fmt.Sprintf("port %d is not within the allowed range", st.RemotePort)
				config := storedTunnelToProxyConfig(st)
				setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
				if s.restoreTunnelPlaceholderIfCurrent(client, st, config, restorePlaceholderActionPortNotAllowed) {
					restoredCount++
				}
				continue
			}
		}

		switch st.DesiredState {
		case protocol.ProxyDesiredStateRunning:
			log.Printf("🔄 restoring tunnel: %s (:%d → %s:%d)", st.Name, st.RemotePort, st.LocalIP, st.LocalPort)
			if err := s.reconcileUnifiedTunnel(st.ID, "restore"); err != nil {
				log.Printf("⚠️ failed to restore tunnel [%s]: %v", st.Name, err)
				continue
			}
			restoredCount++

		default:
			config := storedTunnelToProxyConfig(st)
			setProxyConfigStates(&config, st.DesiredState, st.RuntimeState, st.Error)
			if s.restoreTunnelPlaceholderIfCurrent(client, st, config, restorePlaceholderActionStopped) {
				restoredCount++
			}
		}
	}

	if restoredCount > 0 && s.isCurrentLive(client.ID, client.generation) {
		s.events.PublishJSON("tunnel_changed", map[string]any{
			"client_id": client.ID,
			"action":    "restored_batch",
			"count":     restoredCount,
		})
	}
	s.reconcileNonOwnerTunnelsForClient(client.ID, "restore_related")
}
