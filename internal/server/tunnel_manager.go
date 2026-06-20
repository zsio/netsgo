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
	errManagedTunnelClientNotFound = errors.New("managed tunnel client not found")
	errManagedTunnelNotFound       = errors.New("managed tunnel not found")
)

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

func (s *Server) markPendingTunnelErrorIfCurrent(client *ClientConn, name, message string) {
	if !s.isCurrentGeneration(client.ID, client.generation) {
		return
	}
	config, ok := s.setTunnelStates(client, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	if !ok {
		return
	}
	_ = s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	s.emitTunnelChanged(client.ID, config, "error")
	_ = s.notifyClientProxyClose(client, name, "provision_failed")
}

func (s *Server) emitTunnelFailure(client *ClientConn, config protocol.ProxyConfig, message string) {
	if !s.isCurrentGeneration(client.ID, client.generation) {
		return
	}
	setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	s.emitTunnelChanged(client.ID, config, "error")
}

func (s *Server) createManagedTunnel(client *ClientConn, req protocol.ProxyNewRequest, persist bool, action string) (protocol.ProxyConfig, error) {
	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	s.emitTunnelChanged(client.ID, tunnel.Config, "pending")

	if _, err := s.waitForTunnelProvisionAck(client, s.prepareTunnelProvisionRequest(client, tunnel)); err != nil {
		if s.isCurrentGeneration(client.ID, client.generation) {
			if !errors.Is(err, errTunnelProvisionAckCancelled) {
				s.emitTunnelFailure(client, tunnel.Config, tunnelProvisionErrorMessage(err))
			}
			s.removeTunnelRuntime(client, req.Name)
			_ = s.notifyClientProxyClose(client, req.Name, "provision_failed")
		}
		return protocol.ProxyConfig{}, err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return protocol.ProxyConfig{}, errTunnelProvisionAckCancelled
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		if s.isCurrentGeneration(client.ID, client.generation) {
			s.emitTunnelFailure(client, tunnel.Config, err.Error())
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
			s.emitTunnelFailure(client, updated.Config, err.Error())
			s.removeTunnelRuntime(client, req.Name)
			_ = s.notifyClientProxyClose(client, req.Name, "provision_failed")
			return protocol.ProxyConfig{}, err
		}
	}

	s.emitTunnelChanged(client.ID, updated.Config, action)
	return updated.Config, nil
}

func (s *Server) createOfflineManagedTunnel(clientID string, req protocol.ProxyNewRequest) (protocol.ProxyConfig, error) {
	if s.auth.adminStore == nil {
		return protocol.ProxyConfig{}, errManagedTunnelClientNotFound
	}
	record, ok := s.auth.adminStore.GetRegisteredClient(clientID)
	if !ok {
		return protocol.ProxyConfig{}, errManagedTunnelClientNotFound
	}
	if s.store == nil {
		return protocol.ProxyConfig{}, fmt.Errorf("tunnel store not initialized")
	}

	if req.Type == protocol.ProxyTypeHTTP {
		req.RemotePort = 0
	}
	// Tunnel IDs are server-owned stable identifiers.
	id, err := generateUUIDE()
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	req.ID = id
	if err := s.validateProxyRequestWithExclusions(nil, req, "", clientID); err != nil {
		return protocol.ProxyConfig{}, err
	}

	stored := StoredTunnel{
		ProxyNewRequest: req,
		ClientID:        clientID,
		Hostname:        record.Info.Hostname,
		Binding:         TunnelBindingClientID,
		Revision:        1,
		CreatedAt:       time.Now().UTC(),
	}
	setStoredTunnelStates(&stored, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, "")
	if err := s.store.AddTunnel(stored); err != nil {
		return protocol.ProxyConfig{}, err
	}

	config := storedTunnelToProxyConfig(stored)
	s.emitTunnelChanged(clientID, config, "created")
	return config, nil
}

func (s *Server) resumeManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	previousDesired := tunnel.Config.DesiredState
	previousRuntime := tunnel.Config.RuntimeState
	previousError := tunnel.Config.Error
	pendingConfig, err := s.stageTunnelPending(client, name)
	if err != nil {
		return err
	}
	if err := s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, ""); err != nil {
		_, _ = s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
		return err
	}
	s.emitTunnelChanged(client.ID, pendingConfig, "pending")

	if _, err := s.waitForTunnelProvisionAck(client, s.prepareTunnelProvisionRequest(client, tunnel)); err != nil {
		if errors.Is(err, errTunnelProvisionAckCancelled) {
			return err
		}
		s.markPendingTunnelErrorIfCurrent(client, name, tunnelProvisionErrorMessage(err))
		return err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return errTunnelProvisionAckCancelled
	}

	if err := s.ReopenProxyRuntime(client, name); err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousDesired, previousRuntime, previousError)
		return err
	}

	if err := s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousDesired, previousRuntime, previousError)
		return err
	}
	if _, ok := s.setTunnelStates(client, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); !ok {
		s.rollbackResumedTunnelAfterReady(client, name, previousDesired, previousRuntime, previousError)
		return fmt.Errorf("tunnel %q not found", name)
	}

	updated, err := s.mustGetTunnel(client, name)
	if err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousDesired, previousRuntime, previousError)
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

	previousDesired := tunnel.Config.DesiredState
	previousRuntime := tunnel.Config.RuntimeState
	previousError := tunnel.Config.Error
	runtimeClosedDuringStop := false
	if isTunnelExposed(tunnel.Config) {
		if err := s.CloseProxyRuntime(client, name); err != nil {
			return err
		}
		runtimeClosedDuringStop = true
	}
	if _, ok := s.setTunnelStates(client, name, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); !ok {
		return fmt.Errorf("tunnel %q not found", name)
	}
	if err := s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		_, _ = s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
		if runtimeClosedDuringStop {
			_ = s.ReopenProxyRuntime(client, name)
		}
		return err
	}

	if runtimeClosedDuringStop {
		if err := s.notifyClientProxyClose(client, name, "stopped"); err != nil {
			_ = s.persistTunnelStates(client.ID, name, previousDesired, previousRuntime, previousError)
			_, _ = s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
			_ = s.ReopenProxyRuntime(client, name)
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
	deletedConfig := tunnel.Config

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

	setProxyConfigStates(&deletedConfig, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, "")
	s.emitTunnelChanged(client.ID, deletedConfig, "deleted")
	return nil
}

func (s *Server) updateManagedTunnel(client *ClientConn, selector, newName string, localIP string, localPort, remotePort int, domain string, ingressBPS, egressBPS int64) (protocol.ProxyConfig, error) {
	return s.updateManagedTunnelWithRevision(client, selector, 0, newName, localIP, localPort, remotePort, domain, ingressBPS, egressBPS)
}

func (s *Server) updateManagedTunnelWithRevision(client *ClientConn, selector string, expectedRevision int64, newName string, localIP string, localPort, remotePort int, domain string, ingressBPS, egressBPS int64) (protocol.ProxyConfig, error) {
	name, tunnel, ok := findTunnelBySelector(client, selector)
	if !ok {
		return protocol.ProxyConfig{}, fmt.Errorf("tunnel %q not found", selector)
	}
	if err := s.ensureManagedTunnelIdentity(client, name, tunnel, selector); err != nil {
		return protocol.ProxyConfig{}, err
	}

	client.proxyMu.RLock()
	current, exists := client.proxies[name]
	if !exists || current != tunnel {
		client.proxyMu.RUnlock()
		return protocol.ProxyConfig{}, fmt.Errorf("tunnel %q not found", selector)
	}
	currentConfig := tunnel.Config
	client.proxyMu.RUnlock()

	if newName == "" {
		newName = currentConfig.Name
	}

	previousRuntime := currentConfig.RuntimeState
	tunnelWasProvisioned := currentConfig.DesiredState == protocol.ProxyDesiredStateRunning &&
		(currentConfig.RuntimeState == protocol.ProxyRuntimeStateExposed || currentConfig.RuntimeState == protocol.ProxyRuntimeStatePending || currentConfig.RuntimeState == protocol.ProxyRuntimeStateError)
	tunnelWasExposed := isTunnelExposed(currentConfig)
	tunnelType := currentConfig.Type
	req := protocol.ProxyNewRequest{
		ID:                currentConfig.ID,
		Name:              newName,
		Type:              tunnelType,
		BindIP:            currentConfig.BindIP,
		LocalIP:           localIP,
		LocalPort:         localPort,
		RemotePort:        remotePort,
		Domain:            domain,
		BandwidthSettings: protocol.BandwidthSettings{IngressBPS: ingressBPS, EgressBPS: egressBPS},
	}
	if req.Type == protocol.ProxyTypeHTTP {
		req.RemotePort = 0
	}
	if err := s.validateProxyRequestWithExclusions(client, req, name, client.ID); err != nil {
		return protocol.ProxyConfig{}, err
	}
	if req.Name != name {
		client.proxyMu.RLock()
		existing := client.proxies[req.Name]
		existingID := ""
		if existing != nil {
			existingID = existing.Config.ID
		}
		client.proxyMu.RUnlock()
		if existing != nil && existingID != req.ID {
			return protocol.ProxyConfig{}, newProxyRequestValidationError(fmt.Errorf("tunnel name %q already exists", req.Name), protocol.TunnelMutationFieldName, "", http.StatusConflict)
		}
	}

	var updatedStored StoredTunnel
	if s.store != nil {
		if expectedRevision > 0 {
			var err error
			updatedStored, err = s.store.UpdateTunnelByIDWithRevision(client.ID, req.ID, expectedRevision, req.Name, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain, req.IngressBPS, req.EgressBPS)
			if err != nil {
				return protocol.ProxyConfig{}, err
			}
		} else {
			if err := s.store.UpdateTunnelByID(client.ID, req.ID, req.Name, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain, req.IngressBPS, req.EgressBPS); err != nil {
				return protocol.ProxyConfig{}, err
			}
			if stored, err := s.store.GetTunnelByIDE(client.ID, req.ID); err == nil {
				updatedStored = stored
			} else if !errors.Is(err, ErrTunnelNotFound) {
				return protocol.ProxyConfig{}, err
			}
		}
	}
	if req.Name != name && s.trafficStore != nil {
		if err := s.trafficStore.RenameTunnel(client.ID, name, req.Name); err != nil {
			log.Printf("⚠️ failed to rename traffic buckets for tunnel %s/%s -> %s: %v", client.ID, name, req.Name, err)
		}
	}

	// Update the tunnel configuration in runtime memory.
	client.proxyMu.Lock()
	runtimeTunnel, exists := client.proxies[name]
	if !exists || runtimeTunnel != tunnel {
		client.proxyMu.Unlock()
		return protocol.ProxyConfig{}, fmt.Errorf("tunnel %q not found", selector)
	}
	if tunnelWasExposed {
		closeTunnelRuntimeResources(tunnel)
	}
	if name != req.Name {
		delete(client.proxies, name)
		client.proxies[req.Name] = tunnel
	}
	tunnel.Config.Name = req.Name
	if updatedStored.Revision > 0 {
		tunnel.Config.Revision = updatedStored.Revision
	} else if tunnel.Config.Revision == 0 {
		tunnel.Config.Revision = 1
	} else {
		tunnel.Config.Revision++
	}
	tunnel.Config.LocalIP = req.LocalIP
	tunnel.Config.LocalPort = req.LocalPort
	tunnel.Config.RemotePort = req.RemotePort
	tunnel.Config.Domain = req.Domain
	tunnel.Config.IngressBPS = req.IngressBPS
	tunnel.Config.EgressBPS = req.EgressBPS
	if tunnel.limits == nil {
		tunnel.limits = newDirectionalBandwidthRuntime(tunnel.Config.BandwidthSettings, realBandwidthClock{})
	} else {
		tunnel.limits.Update(tunnel.Config.BandwidthSettings)
	}
	if tunnelWasProvisioned {
		setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, "")
	}
	updated := tunnel.Config
	client.proxyMu.Unlock()

	if !tunnelWasProvisioned {
		s.emitTunnelChanged(client.ID, updated, "updated")
		return updated, nil
	}

	if err := s.persistTunnelStates(client.ID, req.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, ""); err != nil {
		return protocol.ProxyConfig{}, err
	}
	s.emitTunnelChanged(client.ID, updated, "pending")

	if err := s.notifyClientProxyClose(client, name, "updated"); err != nil && previousRuntime == protocol.ProxyRuntimeStateExposed {
		s.markPendingTunnelErrorIfCurrent(client, req.Name, err.Error())
		return protocol.ProxyConfig{}, err
	}

	if _, err := s.waitForTunnelProvisionAck(client, s.prepareTunnelProvisionRequest(client, tunnel)); err != nil {
		if errors.Is(err, errTunnelProvisionAckCancelled) {
			return protocol.ProxyConfig{}, err
		}
		s.markPendingTunnelErrorIfCurrent(client, req.Name, tunnelProvisionErrorMessage(err))
		return protocol.ProxyConfig{}, err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return protocol.ProxyConfig{}, errTunnelProvisionAckCancelled
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		s.markPendingTunnelErrorIfCurrent(client, req.Name, err.Error())
		return protocol.ProxyConfig{}, err
	}
	if err := s.persistTunnelStates(client.ID, req.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); err != nil {
		s.markPendingTunnelErrorIfCurrent(client, req.Name, err.Error())
		return protocol.ProxyConfig{}, err
	}
	config, ok := s.setTunnelStates(client, req.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
	if !ok {
		return protocol.ProxyConfig{}, fmt.Errorf("tunnel %q not found", req.Name)
	}
	s.emitTunnelChanged(client.ID, config, "updated")
	return config, nil
}

func (s *Server) ensureManagedTunnelIdentity(client *ClientConn, name string, tunnel *ProxyTunnel, selector string) error {
	client.proxyMu.RLock()
	current, exists := client.proxies[name]
	if !exists || current != tunnel {
		client.proxyMu.RUnlock()
		return fmt.Errorf("tunnel %q not found", selector)
	}
	needsID := current.Config.ID == ""
	needsCreatedAt := current.Config.CreatedAt.IsZero()
	client.proxyMu.RUnlock()
	if !needsID && !needsCreatedAt {
		return nil
	}

	id := ""
	if needsID {
		generatedID, err := generateUUIDE()
		if err != nil {
			return err
		}
		id = generatedID
	}
	var createdAt time.Time
	if needsID && s.store != nil {
		if stored, err := s.store.GetTunnelE(client.ID, name); err == nil {
			if stored.ID != "" {
				id = stored.ID
			}
			createdAt = stored.CreatedAt
		} else if !errors.Is(err, ErrTunnelNotFound) {
			return err
		}
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()
	current, exists = client.proxies[name]
	if !exists || current != tunnel {
		return fmt.Errorf("tunnel %q not found", selector)
	}
	if current.Config.ID == "" {
		current.Config.ID = id
	}
	if current.Config.CreatedAt.IsZero() {
		current.Config.CreatedAt = createdAt
	}
	return nil
}

func (s *Server) mustGetTunnel(client *ClientConn, name string) (*ProxyTunnel, error) {
	client.proxyMu.RLock()
	defer client.proxyMu.RUnlock()

	tunnel, ok := client.proxies[name]
	if !ok {
		return nil, fmt.Errorf("tunnel %q not found", name)
	}
	return tunnel, nil
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

func (s *Server) setTunnelStates(client *ClientConn, name, desiredState, runtimeState, errMsg string) (protocol.ProxyConfig, bool) {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()
	tunnel, ok := client.proxies[name]
	if !ok {
		return protocol.ProxyConfig{}, false
	}
	setProxyConfigStates(&tunnel.Config, desiredState, runtimeState, errMsg)
	updateTunnelRuntimeFromConfig(tunnel, client.ID, errMsg, time.Now())
	return tunnel.Config, true
}

func (s *Server) persistTunnelStates(clientID, name, desiredState, runtimeState, errMsg string) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpdateStates(clientID, name, desiredState, runtimeState, errMsg)
}

func tunnelChangedActionForStates(desiredState, runtimeState string) string {
	switch {
	case desiredState == protocol.ProxyDesiredStateStopped && runtimeState == protocol.ProxyRuntimeStateIdle:
		return "stopped"
	case desiredState == protocol.ProxyDesiredStateRunning && runtimeState == protocol.ProxyRuntimeStateError:
		return "error"
	case desiredState == protocol.ProxyDesiredStateRunning && runtimeState == protocol.ProxyRuntimeStatePending:
		return "pending"
	case desiredState == protocol.ProxyDesiredStateRunning && runtimeState == protocol.ProxyRuntimeStateExposed:
		return "active"
	default:
		return "updated"
	}
}

func (s *Server) rollbackResumedTunnelAfterReady(client *ClientConn, name, previousDesired, previousRuntime, previousError string) {
	tunnel, err := s.mustGetTunnel(client, name)
	if err == nil && isTunnelExposed(tunnel.Config) {
		_ = s.CloseProxyRuntime(client, name)
	}
	config, ok := s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
	if !ok {
		return
	}
	_ = s.persistTunnelStates(client.ID, name, previousDesired, previousRuntime, previousError)
	_ = s.notifyClientProxyClose(client, name, "provision_failed")
	s.emitTunnelChanged(client.ID, config, tunnelChangedActionForStates(previousDesired, previousRuntime))
}

func (s *Server) upsertTunnelPlaceholder(client *ClientConn, req protocol.ProxyNewRequest, desiredState, runtimeState, errMsg string, createdAt time.Time) protocol.ProxyConfig {
	return s.upsertTunnelPlaceholderWithRevision(client, req, 1, desiredState, runtimeState, errMsg, createdAt)
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
	s.emitTunnelChanged(client.ID, config, "error")
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

func storedTunnelFromRuntime(client *ClientConn, tunnel *ProxyTunnel) StoredTunnel {
	stored := StoredTunnel{
		ProxyNewRequest: tunnel.Config.ToProxyNewRequest(),
		ClientID:        client.ID,
		Hostname:        client.GetInfo().Hostname,
		Binding:         TunnelBindingClientID,
		Revision:        tunnel.Config.Revision,
		CreatedAt:       tunnel.Config.CreatedAt,
	}
	stored.DesiredState = tunnel.Config.DesiredState
	stored.RuntimeState = tunnel.Config.RuntimeState
	stored.Error = tunnel.Config.Error
	_ = stored.normalize()
	return stored
}

func (s *Server) loadOfflineManagedTunnelBySelector(clientID, selector string) (StoredTunnel, error) {
	if s.auth.adminStore == nil {
		return StoredTunnel{}, errManagedTunnelClientNotFound
	}
	if _, ok := s.auth.adminStore.GetRegisteredClient(clientID); !ok {
		return StoredTunnel{}, errManagedTunnelClientNotFound
	}
	if s.store == nil {
		return StoredTunnel{}, errManagedTunnelNotFound
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
		return StoredTunnel{}, errManagedTunnelNotFound
	}
	if err != nil {
		return StoredTunnel{}, err
	}
	return stored, nil
}

func offlineManagedStateAfterUpdate(stored StoredTunnel) (string, string, string) {
	switch canonicalDesiredState(stored.DesiredState) {
	case protocol.ProxyDesiredStateStopped:
		return protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""
	default:
		return protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, ""
	}
}

func (s *Server) updateOfflineManagedTunnel(clientID, selector, newName, localIP string, localPort, remotePort int, domain string, ingressBPS, egressBPS int64) (protocol.ProxyConfig, error) {
	return s.updateOfflineManagedTunnelWithRevision(clientID, selector, 0, newName, localIP, localPort, remotePort, domain, ingressBPS, egressBPS)
}

func (s *Server) updateOfflineManagedTunnelWithRevision(clientID, selector string, expectedRevision int64, newName, localIP string, localPort, remotePort int, domain string, ingressBPS, egressBPS int64) (protocol.ProxyConfig, error) {
	stored, err := s.loadOfflineManagedTunnelBySelector(clientID, selector)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	if newName == "" {
		newName = stored.Name
	}

	req := protocol.ProxyNewRequest{
		ID:                stored.ID,
		Name:              newName,
		Type:              stored.Type,
		BindIP:            stored.BindIP,
		LocalIP:           localIP,
		LocalPort:         localPort,
		RemotePort:        remotePort,
		Domain:            domain,
		BandwidthSettings: protocol.BandwidthSettings{IngressBPS: ingressBPS, EgressBPS: egressBPS},
	}
	if req.Type == protocol.ProxyTypeHTTP {
		req.RemotePort = 0
	}
	if err := s.validateProxyRequestWithExclusions(nil, req, stored.Name, clientID); err != nil {
		return protocol.ProxyConfig{}, err
	}
	if req.Name != stored.Name {
		existing, err := s.store.GetTunnelE(clientID, req.Name)
		if err == nil && existing.ID != stored.ID {
			return protocol.ProxyConfig{}, newProxyRequestValidationError(fmt.Errorf("tunnel name %q already exists", req.Name), protocol.TunnelMutationFieldName, "", http.StatusConflict)
		}
		if err != nil && !errors.Is(err, ErrTunnelNotFound) {
			return protocol.ProxyConfig{}, err
		}
	}
	if expectedRevision > 0 {
		if _, err := s.store.UpdateTunnelByIDWithRevision(clientID, stored.ID, expectedRevision, req.Name, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain, req.IngressBPS, req.EgressBPS); err != nil {
			return protocol.ProxyConfig{}, err
		}
	} else {
		if err := s.store.UpdateTunnelByID(clientID, stored.ID, req.Name, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain, req.IngressBPS, req.EgressBPS); err != nil {
			return protocol.ProxyConfig{}, err
		}
	}
	if req.Name != stored.Name && s.trafficStore != nil {
		if err := s.trafficStore.RenameTunnel(clientID, stored.Name, req.Name); err != nil {
			log.Printf("⚠️ failed to rename traffic buckets for tunnel %s/%s -> %s: %v", clientID, stored.Name, req.Name, err)
		}
	}
	desiredState, runtimeState, errMsg := offlineManagedStateAfterUpdate(stored)
	if err := s.store.UpdateStates(clientID, req.Name, desiredState, runtimeState, errMsg); err != nil {
		return protocol.ProxyConfig{}, err
	}

	updated, err := s.store.GetTunnelByIDE(clientID, stored.ID)
	if errors.Is(err, ErrTunnelNotFound) {
		return protocol.ProxyConfig{}, fmt.Errorf("tunnel id %q not found", stored.ID)
	}
	if err != nil {
		return protocol.ProxyConfig{}, fmt.Errorf("failed to reload tunnel id %q: %w", stored.ID, err)
	}

	config := storedTunnelToProxyConfig(updated)
	s.emitTunnelChanged(clientID, config, "updated")
	return config, nil
}

func (s *Server) resumeOfflineManagedTunnel(clientID, name string) (protocol.ProxyConfig, error) {
	stored, err := s.loadOfflineManagedTunnelBySelector(clientID, name)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	name = stored.Name
	if !canResumeTunnel(storedTunnelToProxyConfig(stored)) {
		return protocol.ProxyConfig{}, fmt.Errorf("only stopped or error tunnels can be resumed")
	}
	if err := s.store.UpdateStates(clientID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, ""); err != nil {
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
	s.emitTunnelChanged(clientID, config, "resumed")
	return config, nil
}

func (s *Server) stopOfflineManagedTunnel(clientID, name string) (protocol.ProxyConfig, error) {
	stored, err := s.loadOfflineManagedTunnelBySelector(clientID, name)
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
	s.emitTunnelChanged(clientID, config, "stopped")
	return config, nil
}

func (s *Server) deleteOfflineManagedTunnel(clientID, name string) error {
	stored, err := s.loadOfflineManagedTunnelBySelector(clientID, name)
	if err != nil {
		return err
	}
	deletedConfig := storedTunnelToProxyConfig(stored)
	if err := s.store.RemoveTunnelByID(clientID, stored.ID); err != nil {
		return err
	}

	setProxyConfigStates(&deletedConfig, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, "")
	s.emitTunnelChanged(clientID, deletedConfig, "deleted")
	return nil
}

func (s *Server) notifyClientProxyProvision(client *ClientConn, req protocol.ProxyNewRequest) error {
	message, err := protocol.NewMessage(protocol.MsgTypeProxyProvision, protocol.ProxyProvisionRequest(req))
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
		return fmt.Errorf("client %s is not in the live session", client.ID)
	}

	if err := client.writeJSON(message); err != nil {
		return fmt.Errorf("failed to write control message: %w", err)
	}
	return nil
}

func (s *Server) emitTunnelChanged(clientID string, tunnel protocol.ProxyConfig, action string) {
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
	seen := map[string]bool{} // key: "clientID:tunnelName"

	// 1) Scan runtime tunnels for online clients.
	s.clients.Range(func(_, value any) bool {
		client := value.(*ClientConn)
		client.RangeProxies(func(name string, tunnel *ProxyTunnel) bool {
			if tunnel.Config.RemotePort != 0 && !isPortInRanges(tunnel.Config.RemotePort, newPorts) {
				// Do not report tunnels already in error state again.
				if tunnel.Config.RuntimeState == protocol.ProxyRuntimeStateError {
					return true
				}
				key := client.ID + ":" + name
				seen[key] = true
				// Try to load display_name.
				displayName := ""
				if s.auth.adminStore != nil {
					if reg, ok := s.auth.adminStore.GetRegisteredClient(client.ID); ok {
						displayName = reg.DisplayName
					}
				}
				affected = append(affected, affectedTunnel{
					ClientID:     client.ID,
					Hostname:     client.GetInfo().Hostname,
					DisplayName:  displayName,
					TunnelName:   name,
					RemotePort:   tunnel.Config.RemotePort,
					DesiredState: tunnel.Config.DesiredState,
					RuntimeState: tunnel.Config.RuntimeState,
					Error:        tunnel.Config.Error,
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
				if st.RemotePort == 0 {
					continue
				}
				if st.RuntimeState == protocol.ProxyRuntimeStateError {
					continue
				}
				key := st.ClientID + ":" + st.Name
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
					affected = append(affected, affectedTunnel{
						ClientID:     st.ClientID,
						Hostname:     hostname,
						DisplayName:  displayName,
						TunnelName:   st.Name,
						RemotePort:   st.RemotePort,
						DesiredState: st.DesiredState,
						RuntimeState: st.RuntimeState,
						Error:        st.Error,
					})
				}
			}
		}
	}

	return affected, nil
}

// markTunnelsPortNotAllowed marks tunnels affected by a port allowlist change as error.
func (s *Server) markTunnelsPortNotAllowed(affected []affectedTunnel) {
	for _, a := range affected {
		errMsg := fmt.Sprintf("port %d is not allowed", a.RemotePort)
		hasEventConfig := false
		eventConfig := protocol.ProxyConfig{
			Name:         a.TunnelName,
			RemotePort:   a.RemotePort,
			ClientID:     a.ClientID,
			DesiredState: protocol.ProxyDesiredStateRunning,
			RuntimeState: protocol.ProxyRuntimeStateError,
			Error:        errMsg,
		}

		// Update runtime state for online clients.
		if value, ok := s.clients.Load(a.ClientID); ok {
			client := value.(*ClientConn)
			client.proxyMu.Lock()
			if tunnel, exists := client.proxies[a.TunnelName]; exists {
				if isTunnelExposed(tunnel.Config) {
					closeTunnelRuntimeResources(tunnel)
					go func(c *ClientConn, name string) {
						_ = s.notifyClientProxyClose(c, name, "port_not_allowed")
					}(client, a.TunnelName)
				}
				setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
				eventConfig = tunnel.Config
				hasEventConfig = true
			}
			client.proxyMu.Unlock()
		}

		if !hasEventConfig && s.store != nil {
			stored, err := s.store.GetTunnelE(a.ClientID, a.TunnelName)
			if err == nil {
				eventConfig = storedTunnelToProxyConfig(stored)
				setProxyConfigStates(&eventConfig, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
				hasEventConfig = true
			} else if !errors.Is(err, ErrTunnelNotFound) {
				log.Printf("⚠️ failed to load persisted tunnel %s/%s for event fallback: %v", a.ClientID, a.TunnelName, err)
			}
		}

		s.emitTunnelChanged(a.ClientID, eventConfig, "port_not_allowed")

		_ = s.persistTunnelStates(a.ClientID, a.TunnelName, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)

		log.Printf("⚠️ Tunnel %s (port %d, client %s) was marked as error due to a port allowlist change",
			a.TunnelName, a.RemotePort, a.ClientID)
	}
}
