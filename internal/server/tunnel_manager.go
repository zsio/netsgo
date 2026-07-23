package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"netsgo/pkg/protocol"
)

var (
	errStoredTunnelClientNotFound = errors.New("tunnel client not found")
	errStoredTunnelNotFound       = errors.New("tunnel not found")
)

const maxPortPolicyRevisionAttempts = 4

func tunnelProvisionErrorMessage(err error) string {
	var rejected *tunnelProvisionRejectedError
	switch {
	case errors.As(err, &rejected):
		if rejected.message != "" {
			return rejected.message
		}
		return "client rejected tunnel provisioning"
	case errors.Is(err, errTunnelProvisionAckTimeout):
		return "timed out waiting for client provisioning ack"
	case errors.Is(err, errTunnelProvisionAckCancelled):
		return "logical session is no longer valid"
	default:
		return err.Error()
	}
}

func findTunnelBySelector(client *ClientConn, selector string) (string, *ProxyTunnel, bool) {
	client.proxyMu.RLock()
	defer client.proxyMu.RUnlock()

	if tunnel, ok := client.proxies[selector]; ok {
		return selector, tunnel, true
	}
	for name, tunnel := range client.proxies {
		if tunnel.Config.ID == selector {
			return name, tunnel, true
		}
	}
	return "", nil, false
}

func (s *Server) persistTunnelStates(clientID, name, desiredState, runtimeState, errMsg string) error {
	if s.store == nil {
		return nil
	}
	stored, ok := s.store.GetTunnel(clientID, name)
	if !ok {
		return nil
	}
	_, err := s.updateStoredTunnelRuntimeObserved(stored, runtimeState, errMsg)
	return err
}

func (s *Server) updateProxyConfigRuntimeIfCurrent(clientID string, config protocol.ProxyConfig, runtimeState, message string) (bool, error) {
	stored, ok, err := s.findStoredTunnelByID(config.ID)
	if err != nil || !ok {
		return false, err
	}
	if config.Revision > 0 && stored.Revision != config.Revision {
		return false, nil
	}
	return s.updateStoredTunnelRuntimeObserved(stored, runtimeState, message)
}

func (s *Server) upsertTunnelPlaceholderWithRevision(client *ClientConn, req protocol.ProxyNewRequest, revision int64, desiredState, runtimeState, errMsg string, createdAt time.Time) protocol.ProxyConfig {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if revision <= 0 {
		revision = 1
	}
	config := protocol.ProxyConfig{
		ID:                req.ID,
		Name:              req.Name,
		Revision:          revision,
		Type:              req.Type,
		LocalIP:           req.LocalIP,
		LocalPort:         req.LocalPort,
		RemotePort:        req.RemotePort,
		BindIP:            normalizeServerBindIP(req.BindIP),
		Domain:            req.Domain,
		ClientID:          client.ID,
		BandwidthSettings: req.BandwidthSettings,
		CreatedAt:         createdAt.UTC(),
	}
	setProxyConfigStates(&config, desiredState, runtimeState, errMsg)
	client.proxyMu.Lock()
	if client.proxies == nil {
		client.proxies = make(map[string]*ProxyTunnel)
	}
	tunnel := &ProxyTunnel{
		Config: config,
		limits: newDirectionalBandwidthRuntime(req.BandwidthSettings, realBandwidthClock{}),
		done:   make(chan struct{}),
	}
	initializeTunnelRuntimeFromState(tunnel, client.ID, time.Now())
	client.proxies[req.Name] = tunnel
	client.proxyMu.Unlock()
	return config
}

func (s *Server) failRestoredTunnelAfterReady(client *ClientConn, stored StoredTunnel, message string) {
	s.removeTunnelRuntime(client, stored.Name)
	_ = s.notifyClientProxyClose(client, stored.Name, "provision_failed")
	req := stored.ProxyNewRequest
	req.BindIP = tunnelIngressBindIP(stored)
	config := s.upsertTunnelPlaceholderWithRevision(client, req, stored.Revision, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message, stored.CreatedAt)
	_ = s.persistTunnelStates(client.ID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	s.emitTunnelChangedIfStored(client.ID, config, "error")
}

func storedTunnelToProxyConfig(stored StoredTunnel) protocol.ProxyConfig {
	config := protocol.ProxyConfig{
		ID:                stored.ID,
		Name:              stored.Name,
		Revision:          stored.Revision,
		Type:              stored.Type,
		LocalIP:           stored.LocalIP,
		LocalPort:         stored.LocalPort,
		RemotePort:        stored.RemotePort,
		BindIP:            tunnelIngressBindIP(stored),
		Domain:            stored.Domain,
		ClientID:          stored.ClientID,
		Topology:          stored.Topology,
		OwnerClientID:     stored.OwnerClientID,
		BandwidthSettings: stored.BandwidthSettings,
		CreatedAt:         stored.CreatedAt,
	}
	if stored.TransportPolicy != "" {
		config.TransportPolicy = stored.TransportPolicy
	}
	if stored.ActualTransport != "" {
		config.ActualTransport = stored.ActualTransport
	}
	if stored.Ingress.Location != "" || stored.Ingress.Type != "" {
		ingress := endpointSpecProtocolFromStored(stored.Ingress)
		config.Ingress = &ingress
	}
	if stored.Target.Location != "" || stored.Target.Type != "" || stored.Target.ClientID != "" {
		target := endpointSpecProtocolFromStored(stored.Target)
		config.Target = &target
	}
	if stored.P2P.State != "" || stored.P2P.Error != "" || stored.P2P.SessionID != "" {
		config.P2P = &protocol.P2PState{
			State:     stored.P2P.State,
			Error:     stored.P2P.Error,
			SessionID: stored.P2P.SessionID,
		}
	}
	setProxyConfigStates(&config, stored.DesiredState, stored.RuntimeState, stored.Error)
	return config
}

func (s *Server) loadOfflineTunnelBySelector(clientID, selector string) (StoredTunnel, error) {
	if s.auth.adminStore == nil {
		return StoredTunnel{}, errStoredTunnelClientNotFound
	}
	if _, ok := s.auth.adminStore.GetRegisteredClient(clientID); !ok {
		return StoredTunnel{}, errStoredTunnelClientNotFound
	}
	if s.store == nil {
		return StoredTunnel{}, errStoredTunnelNotFound
	}

	stored, err := s.store.GetTunnelE(clientID, selector)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, ErrTunnelNotFound) {
		return StoredTunnel{}, err
	}

	stored, err = s.store.GetTunnelByIDE(clientID, selector)
	if errors.Is(err, ErrTunnelNotFound) {
		return StoredTunnel{}, errStoredTunnelNotFound
	}
	if err != nil {
		return StoredTunnel{}, err
	}
	return stored, nil
}

func (s *Server) stopOfflineTunnel(clientID, name string) (protocol.ProxyConfig, error) {
	stored, err := s.loadOfflineTunnelBySelector(clientID, name)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	name = stored.Name
	if err := s.store.UpdateStates(clientID, name, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		return protocol.ProxyConfig{}, err
	}

	updated, err := s.store.GetTunnelE(clientID, name)
	if errors.Is(err, ErrTunnelNotFound) {
		return protocol.ProxyConfig{}, fmt.Errorf("tunnel %q not found", name)
	}
	if err != nil {
		return protocol.ProxyConfig{}, fmt.Errorf("failed to reload tunnel %q: %w", name, err)
	}

	config := storedTunnelToProxyConfig(updated)
	s.emitTunnelChangedIfStored(clientID, config, "stopped")
	return config, nil
}
func (s *Server) stopOfflineTunnelWithActivity(clientID, id string, actor ActivityActor) (protocol.ProxyConfig, int64, error) {
	stored, err := s.loadOfflineTunnelBySelector(clientID, id)
	if err != nil {
		return protocol.ProxyConfig{}, 0, err
	}
	updated, activityID, err := s.store.UpdateTunnelStatesWithActivity(
		clientID,
		stored.ID,
		protocol.ProxyDesiredStateStopped,
		protocol.ProxyRuntimeStateIdle,
		"",
		"stopped",
		actor,
	)
	if err != nil {
		return protocol.ProxyConfig{}, 0, err
	}
	config := storedTunnelToProxyConfig(updated)
	s.emitTunnelChangedIfStored(clientID, config, "stopped")
	return config, activityID, nil
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
		return fmt.Errorf("client %s is not in the live session", client.ID)
	}

	if err := client.writeJSON(message); err != nil {
		return fmt.Errorf("failed to write control message: %w", err)
	}
	return nil
}

func (s *Server) emitTunnelChanged(clientID string, tunnel protocol.ProxyConfig, action string) {
	s.tunnelEventMu.Lock()
	defer s.tunnelEventMu.Unlock()
	s.emitTunnelChangedLocked(clientID, tunnel, action)
}

func (s *Server) emitTunnelChangedLocked(clientID string, tunnel protocol.ProxyConfig, action string) {
	_, clientOnline := s.loadLiveClient(clientID)
	tunnel = proxyConfigForClientView(tunnel, clientOnline)
	log.Printf("🔎 tunnel_changed action=%s client_id=%s tunnel_id=%s name=%s desired=%s runtime=%s online=%v",
		action, clientID, tunnel.ID, tunnel.Name, tunnel.DesiredState, tunnel.RuntimeState, clientOnline)
	payload := map[string]any{
		"client_id": clientID,
		"action":    action,
		"tunnel":    tunnel,
	}
	s.events.PublishJSON("tunnel_changed", payload)
}

func (s *Server) emitTunnelChangedIfStored(clientID string, tunnel protocol.ProxyConfig, action string) {
	s.tunnelEventMu.Lock()
	defer s.tunnelEventMu.Unlock()
	if !s.hasStoredTunnelForEvent(clientID, tunnel) {
		log.Printf("🔎 suppressing runtime-only tunnel_changed action=%s client_id=%s tunnel_id=%s name=%s",
			action, clientID, tunnel.ID, tunnel.Name)
		return
	}
	s.emitTunnelChangedLocked(clientID, tunnel, action)
}

func (s *Server) hasStoredTunnelForEvent(clientID string, tunnel protocol.ProxyConfig) bool {
	if s.store == nil {
		return false
	}
	ownerClientID := tunnel.OwnerClientID
	if ownerClientID == "" {
		ownerClientID = tunnel.ClientID
	}
	if ownerClientID == "" {
		ownerClientID = clientID
	}
	if ownerClientID == "" {
		return false
	}
	if tunnel.ID != "" {
		if stored, err := s.store.GetTunnelByIDE(ownerClientID, tunnel.ID); err == nil {
			return storedTunnelMatchesEvent(stored, tunnel)
		} else if !errors.Is(err, ErrTunnelNotFound) {
			log.Printf("⚠️ failed to check stored tunnel event by id: client_id=%s tunnel_id=%s err=%v", ownerClientID, tunnel.ID, err)
		}
		if tunnel.Revision > 0 {
			return false
		}
	}
	if tunnel.Name == "" {
		return false
	}
	if stored, err := s.store.GetTunnelE(ownerClientID, tunnel.Name); err == nil {
		return storedTunnelMatchesEvent(stored, tunnel)
	} else if !errors.Is(err, ErrTunnelNotFound) {
		log.Printf("⚠️ failed to check stored tunnel event by name: client_id=%s name=%s err=%v", ownerClientID, tunnel.Name, err)
	}
	return false
}

func storedTunnelMatchesEvent(stored StoredTunnel, tunnel protocol.ProxyConfig) bool {
	if tunnel.Revision > 0 && stored.Revision != tunnel.Revision {
		return false
	}
	if tunnel.DesiredState != "" && stored.DesiredState != canonicalDesiredState(tunnel.DesiredState) {
		return false
	}
	if tunnel.RuntimeState != "" {
		runtimeState := protocolRuntimeStateFromStorage(tunnel.RuntimeState)
		if stored.RuntimeState != runtimeState {
			return false
		}
		if runtimeState == protocol.ProxyRuntimeStateError && stored.Error != tunnel.Error {
			return false
		}
	}
	return true
}

func encodeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("⚠️ Failed to encode JSON response: %v", err)
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	encodeJSON(w, status, apiErrorResponse{
		Error:   message,
		Message: message,
		Code:    code,
	})
}

// affectedTunnel describes a tunnel affected by a port allowlist change.
type affectedTunnel struct {
	ClientID     string `json:"client_id"`
	Hostname     string `json:"hostname"`
	DisplayName  string `json:"display_name,omitempty"`
	TunnelName   string `json:"tunnel_name"`
	RemotePort   int    `json:"remote_port"`
	DesiredState string `json:"desired_state"`
	RuntimeState string `json:"runtime_state"`
	Error        string `json:"error,omitempty"`

	TunnelID      string               `json:"-"`
	Revision      int64                `json:"-"`
	OwnerClientID string               `json:"-"`
	Config        protocol.ProxyConfig `json:"-"`
}

func affectedTunnelOwner(config protocol.ProxyConfig, fallback string) string {
	if config.OwnerClientID != "" {
		return config.OwnerClientID
	}
	if config.ClientID != "" {
		return config.ClientID
	}
	return fallback
}

func affectedTunnelKey(id, ownerClientID, name string) string {
	if id != "" {
		return "id:" + id
	}
	return "legacy:" + ownerClientID + ":" + name
}

func affectedTunnelRevisionKey(id string, revision int64, ownerClientID, name string) string {
	if id != "" {
		return fmt.Sprintf("id:%s:revision:%d", id, revision)
	}
	return affectedTunnelKey(id, ownerClientID, name)
}

func affectedTunnelFromStored(stored StoredTunnel) affectedTunnel {
	config := storedTunnelToProxyConfig(stored)
	ownerClientID := affectedTunnelOwner(config, stored.ClientID)
	return affectedTunnel{
		ClientID:      stored.ClientID,
		Hostname:      stored.Hostname,
		TunnelName:    stored.Name,
		RemotePort:    stored.RemotePort,
		DesiredState:  stored.DesiredState,
		RuntimeState:  stored.RuntimeState,
		Error:         stored.Error,
		TunnelID:      stored.ID,
		Revision:      stored.Revision,
		OwnerClientID: ownerClientID,
		Config:        config,
	}
}

// isPortInRanges checks whether a port is within the given allowlist ranges.
func isPortInRanges(port int, ranges []PortRange) bool {
	for _, pr := range ranges {
		if port >= pr.Start && port <= pr.End {
			return true
		}
	}
	return false
}

// findTunnelsAffectedByPortChange finds all tunnels affected by the new port allowlist rules.
// It scans both runtime tunnels and persisted tunnels for offline clients.
func (s *Server) findTunnelsAffectedByPortChange(newPorts []PortRange) ([]affectedTunnel, error) {
	// An empty allowlist means no port restriction, so nothing is affected.
	if len(newPorts) == 0 {
		return []affectedTunnel{}, nil
	}

	affected := []affectedTunnel{}
	seen := map[string]bool{}

	// 1) Scan runtime tunnels for online clients.
	s.clients.Range(func(_, value any) bool {
		client := value.(*ClientConn)
		client.RangeProxies(func(name string, tunnel *ProxyTunnel) bool {
			config := tunnel.Config
			if canonicalDesiredState(config.DesiredState) != protocol.ProxyDesiredStateRunning {
				return true
			}
			if config.RemotePort != 0 && !isPortInRanges(config.RemotePort, newPorts) {
				// Do not report tunnels already in error state again.
				if config.RuntimeState == protocol.ProxyRuntimeStateError {
					return true
				}
				ownerClientID := affectedTunnelOwner(config, client.ID)
				key := affectedTunnelKey(config.ID, ownerClientID, name)
				if seen[key] {
					return true
				}
				seen[key] = true
				// Try to load display_name.
				displayName := ""
				if s.auth.adminStore != nil {
					if reg, ok := s.auth.adminStore.GetRegisteredClient(client.ID); ok {
						displayName = reg.DisplayName
					}
				}
				affected = append(affected, affectedTunnel{
					ClientID:      client.ID,
					Hostname:      client.GetInfo().Hostname,
					DisplayName:   displayName,
					TunnelName:    name,
					RemotePort:    config.RemotePort,
					DesiredState:  config.DesiredState,
					RuntimeState:  config.RuntimeState,
					Error:         config.Error,
					TunnelID:      config.ID,
					Revision:      config.Revision,
					OwnerClientID: ownerClientID,
					Config:        config,
				})
			}
			return true
		})
		return true
	})

	// 2) Scan persisted tunnels, including tunnels for offline clients.
	if s.store != nil {
		allStored, err := s.store.GetAllTunnels()
		if err != nil {
			return nil, fmt.Errorf("load persisted tunnels for port allocation: %w", err)
		} else {
			for _, st := range allStored {
				if canonicalDesiredState(st.DesiredState) != protocol.ProxyDesiredStateRunning {
					continue
				}
				if st.RemotePort == 0 {
					continue
				}
				if st.RuntimeState == protocol.ProxyRuntimeStateError {
					continue
				}
				config := storedTunnelToProxyConfig(st)
				ownerClientID := affectedTunnelOwner(config, st.ClientID)
				key := affectedTunnelKey(st.ID, ownerClientID, st.Name)
				if seen[key] {
					continue // Already counted from runtime state.
				}
				if !isPortInRanges(st.RemotePort, newPorts) {
					hostname := st.Hostname
					displayName := ""
					// Try to get a more detailed hostname and display name from adminStore.
					if s.auth.adminStore != nil && st.ClientID != "" {
						if reg, ok := s.auth.adminStore.GetRegisteredClient(st.ClientID); ok {
							hostname = reg.Info.Hostname
							displayName = reg.DisplayName
						}
					}
					storedAffected := affectedTunnelFromStored(st)
					storedAffected.Hostname = hostname
					storedAffected.DisplayName = displayName
					affected = append(affected, storedAffected)
				}
			}
		}
	}

	return affected, nil
}

// markTunnelsPortNotAllowed marks tunnels affected by a port allowlist change as error.
// A revision may advance after discovery, so stale CAS misses are re-evaluated
// against the current persisted revision while the tunnel operation is held.
func (s *Server) markTunnelsPortNotAllowed(affected []affectedTunnel, allowedPorts []PortRange) {
	processed := make(map[string]bool, len(affected))
	for _, tunnel := range affected {
		s.markTunnelPortNotAllowed(tunnel, allowedPorts, processed)
	}
}

func (s *Server) markTunnelPortNotAllowed(initial affectedTunnel, allowedPorts []PortRange, processed map[string]bool) {
	config, ownerClientID := normalizedAffectedTunnel(initial)
	if canonicalDesiredState(config.DesiredState) != protocol.ProxyDesiredStateRunning {
		return
	}

	releaseRuntimeOperation := s.tunnelRuntimeOps.lock(tunnelRuntimeOperationKey(config.ID, ownerClientID, config.Name))
	defer releaseRuntimeOperation()

	current := initial
	for attempt := 0; attempt < maxPortPolicyRevisionAttempts; attempt++ {
		config, ownerClientID = normalizedAffectedTunnel(current)
		key := affectedTunnelRevisionKey(config.ID, config.Revision, ownerClientID, config.Name)
		if processed[key] {
			return
		}
		processed[key] = true

		updated, retryCurrent := s.markTunnelPortNotAllowedRevision(current)
		if updated || !retryCurrent {
			return
		}

		latest, ok := s.currentTunnelAffectedByPortPolicy(config.ID, allowedPorts)
		if !ok {
			return
		}
		current = affectedTunnelFromStored(latest)
	}
	config, ownerClientID = normalizedAffectedTunnel(current)
	log.Printf("⚠️ stopped retrying port policy error for tunnel %s/%s after %d attempts",
		ownerClientID, config.ID, maxPortPolicyRevisionAttempts)
}

func normalizedAffectedTunnel(affected affectedTunnel) (protocol.ProxyConfig, string) {
	config := affected.Config
	if config.ID == "" {
		config.ID = affected.TunnelID
	}
	if config.Name == "" {
		config.Name = affected.TunnelName
	}
	if config.Revision == 0 {
		config.Revision = affected.Revision
	}
	if config.RemotePort == 0 {
		config.RemotePort = affected.RemotePort
	}
	if config.ClientID == "" {
		config.ClientID = affected.ClientID
	}
	if config.DesiredState == "" {
		config.DesiredState = affected.DesiredState
	}
	if config.RuntimeState == "" {
		config.RuntimeState = affected.RuntimeState
	}
	if config.Error == "" {
		config.Error = affected.Error
	}
	ownerClientID := affected.OwnerClientID
	if ownerClientID == "" {
		ownerClientID = affectedTunnelOwner(config, affected.ClientID)
	}
	if config.OwnerClientID == "" {
		config.OwnerClientID = ownerClientID
	}
	return config, ownerClientID
}

func (s *Server) markTunnelPortNotAllowedRevision(affected affectedTunnel) (bool, bool) {
	config, ownerClientID := normalizedAffectedTunnel(affected)
	if canonicalDesiredState(config.DesiredState) != protocol.ProxyDesiredStateRunning {
		return false, false
	}
	errMsg := fmt.Sprintf("port %d is not allowed", config.RemotePort)

	client, clientOnline := s.loadLiveClient(ownerClientID)
	matchedRuntime := false
	if clientOnline {
		client.proxyMu.Lock()
		if tunnel, exists := client.proxies[config.Name]; exists &&
			tunnel.Config.ID == config.ID &&
			tunnel.Config.Revision == config.Revision {
			matchedRuntime = true
			closeTunnelRuntimeResources(tunnel)
			setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			tunnel.Config.ActualTransport = protocol.ActualTransportUnknown
			markTunnelRuntimeError(tunnel, client.ID, errMsg, time.Now())
			config = tunnel.Config
		}
		client.proxyMu.Unlock()

		stableServerExpose := config.Topology == TunnelTopologyServerExpose && config.ID != "" && config.Revision > 0
		if matchedRuntime || stableServerExpose {
			if notifyErr := s.notifyServerExposeTargetUnprovision(client, config, "port_not_allowed"); notifyErr != nil {
				log.Printf("⚠️ failed to unprovision tunnel %s/%s after port policy change: %v", ownerClientID, config.ID, notifyErr)
			}
		}
	}

	if s.portPolicyAfterRuntimeCleanupHook != nil {
		s.portPolicyAfterRuntimeCleanupHook(affected)
	}

	if s.store == nil || config.ID == "" {
		log.Printf("⚠️ skipped persisting port policy error for tunnel %s: stable identity is incomplete", config.Name)
		return false, false
	}
	if ownerClientID == "" || config.Revision <= 0 {
		log.Printf("⚠️ retrying port policy error for tunnel %s from its persisted identity", config.Name)
		return false, true
	}

	updated, err := s.store.UpdateStatesIfCurrent(
		ownerClientID,
		config.ID,
		config.Revision,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStateError,
		errMsg,
	)
	if err != nil {
		log.Printf("⚠️ failed to persist port policy error for tunnel %s/%s revision %d: %v", ownerClientID, config.ID, config.Revision, err)
		return false, false
	}
	if updated {
		setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
		config.ActualTransport = protocol.ActualTransportUnknown
		s.emitTunnelChangedIfStored(ownerClientID, config, "port_not_allowed")
	}

	log.Printf("⚠️ Tunnel %s (port %d, client %s) was marked as error due to a port allowlist change",
		config.Name, config.RemotePort, ownerClientID)
	return updated, !updated
}

func (s *Server) currentTunnelAffectedByPortPolicy(tunnelID string, allowedPorts []PortRange) (StoredTunnel, bool) {
	if s.store == nil || tunnelID == "" || len(allowedPorts) == 0 {
		return StoredTunnel{}, false
	}
	stored, err := s.store.GetTunnelByID(tunnelID)
	if errors.Is(err, ErrTunnelNotFound) {
		return StoredTunnel{}, false
	}
	if err != nil {
		log.Printf("⚠️ failed to reload tunnel %s after stale port policy update: %v", tunnelID, err)
		return StoredTunnel{}, false
	}
	if canonicalDesiredState(stored.DesiredState) != protocol.ProxyDesiredStateRunning ||
		stored.RemotePort == 0 ||
		stored.RuntimeState == protocol.ProxyRuntimeStateError ||
		isPortInRanges(stored.RemotePort, allowedPorts) {
		return StoredTunnel{}, false
	}
	return stored, true
}
