package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"netsgo/pkg/protocol"
)

func (s *Server) restoreUnifiedServerExposeTunnel(client *ClientConn, stored StoredTunnel) error {
	runtimeConfig, err := serverExposeRuntimeProxyRequest(stored)
	if err != nil {
		return err
	}
	tunnel, err := s.prepareProxyTunnelWithExclusions(
		client,
		runtimeConfig,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStatePending,
		stored.Name,
		client.ID,
		stored.CreatedAt,
	)
	if err != nil {
		return err
	}
	config := s.applyStoredServerExposeConfig(client, tunnel, stored, protocol.ProxyRuntimeStatePending, "")
	if config.ID == "" {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		return errTunnelProvisionAckCancelled
	}
	updated, err := s.updateStoredTunnelRuntimeIfCurrent(stored, protocol.ProxyRuntimeStatePending, "")
	if err != nil {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		return err
	}
	if !updated {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		return errTunnelProvisionAckCancelled
	}
	s.emitTunnelChangedIfStored(client.ID, config, "pending")

	req := protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleTarget,
		Spec:     tunnelSpecProtocolForRole(stored, protocol.ProxyRuntimeStatePending, protocol.DataStreamRoleTarget),
	}
	if err := s.waitForClientTunnelProvisionAck(client, req); err != nil {
		if errors.Is(err, errTunnelProvisionAckCancelled) {
			s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
			_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "provision_cancelled")
			return err
		}
		applied, transitionErr := s.failUnifiedServerExposeAfterProvision(client, tunnel, stored, tunnelProvisionErrorMessage(err))
		if transitionErr != nil {
			return errors.Join(err, transitionErr)
		}
		if !applied {
			return errTunnelProvisionAckCancelled
		}
		return err
	}

	current, ok, err := s.findStoredTunnelByID(stored.ID)
	if err != nil {
		applied, transitionErr := s.failUnifiedServerExposeAfterProvision(client, tunnel, stored, err.Error())
		if transitionErr != nil {
			return errors.Join(err, transitionErr)
		}
		if !applied {
			return errTunnelProvisionAckCancelled
		}
		return err
	}
	if !ok || current.Revision != stored.Revision {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "stale_revision")
		return errTunnelProvisionAckCancelled
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "generation_changed")
		return errTunnelProvisionAckCancelled
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		applied, transitionErr := s.failUnifiedServerExposeAfterProvision(client, tunnel, stored, err.Error())
		if transitionErr != nil {
			return errors.Join(err, transitionErr)
		}
		if !applied {
			return errTunnelProvisionAckCancelled
		}
		return err
	}
	if !s.proxyActivationClientCurrent(client) {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "generation_changed")
		return errTunnelProvisionAckCancelled
	}
	if s.serverExposeActivatedHook != nil {
		s.serverExposeActivatedHook(stored, tunnel)
	}
	updated, err = s.transitionStoredTunnelRuntimeIfCurrent(
		stored,
		protocol.ProxyRuntimeStatePending,
		protocol.ProxyRuntimeStateExposed,
		"",
	)
	if err != nil {
		applied, transitionErr := s.failUnifiedServerExposeAfterProvision(client, tunnel, stored, err.Error())
		if transitionErr != nil {
			return errors.Join(err, transitionErr)
		}
		if !applied {
			return errTunnelProvisionAckCancelled
		}
		return err
	}
	if !updated {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "stale_revision")
		return errTunnelProvisionAckCancelled
	}

	config, runtimeHeld, stillExists := serverExposeTunnelSnapshot(client, stored.Name, tunnel)
	if !stillExists ||
		config.ID != stored.ID ||
		config.Revision != stored.Revision ||
		!isTunnelExposed(config) ||
		!runtimeHeld {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "activation_not_current")
		return errTunnelProvisionAckCancelled
	}
	s.emitTunnelChangedIfStored(client.ID, config, "restored")
	return nil
}

func (s *Server) applyStoredServerExposeConfig(client *ClientConn, tunnel *ProxyTunnel, stored StoredTunnel, runtimeState, message string) protocol.ProxyConfig {
	if tunnel == nil {
		return protocol.ProxyConfig{}
	}
	config := storedTunnelToProxyConfig(stored)
	setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, runtimeState, message)

	client.proxyMu.Lock()
	current, exists := client.proxies[stored.Name]
	if !exists || current != tunnel {
		client.proxyMu.Unlock()
		return protocol.ProxyConfig{}
	}
	current.Config = config
	if current.limits == nil {
		current.limits = newDirectionalBandwidthRuntime(config.BandwidthSettings, realBandwidthClock{})
	} else {
		current.limits.Update(config.BandwidthSettings)
	}
	current.runtime.Revision = uint64(stored.Revision)
	updateTunnelRuntimeFromConfig(current, client.ID, message, time.Now())
	client.proxyMu.Unlock()
	return config
}

func (s *Server) failUnifiedServerExposeAfterProvision(client *ClientConn, tunnel *ProxyTunnel, stored StoredTunnel, message string) (bool, error) {
	config := storedTunnelToProxyConfig(stored)
	setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	client.proxyMu.Lock()
	current := client.proxies[stored.Name]
	if current == tunnel && current.Config.ID == stored.ID && current.Config.Revision == stored.Revision {
		closeTunnelRuntimeResources(current)
		current.Config = config
		current.runtime.Revision = uint64(stored.Revision)
		updateTunnelRuntimeFromConfig(current, client.ID, message, time.Now())
	}
	client.proxyMu.Unlock()
	if current != tunnel {
		closeTunnelRuntimeResources(tunnel)
	}
	_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "provision_failed")
	updated, err := s.updateStoredTunnelRuntimeIfCurrent(stored, protocol.ProxyRuntimeStateError, message)
	if err != nil {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		return false, err
	}
	if !updated {
		s.discardTunnelRuntimeIfCurrent(client, stored.Name, tunnel, stored.ID, stored.Revision)
		return false, nil
	}
	s.emitTunnelChangedIfStored(client.ID, config, "error")
	return true, nil
}

func serverExposeRuntimeProxyRequest(stored StoredTunnel) (protocol.ProxyNewRequest, error) {
	config := protocol.ProxyNewRequest{
		ID:                stored.ID,
		Name:              stored.Name,
		BandwidthSettings: stored.BandwidthSettings,
	}

	switch stored.Ingress.Type {
	case protocol.IngressTypeHTTPHost:
		var ingress httpHostConfigAPI
		if err := json.Unmarshal(stored.Ingress.Config, &ingress); err != nil {
			return protocol.ProxyNewRequest{}, fmt.Errorf("decode http_host ingress config: %w", err)
		}
		config.Type = protocol.ProxyTypeHTTP
		config.RemotePort = 0
		config.BindIP = ""
		config.Domain = ingress.Domain
	case protocol.IngressTypeUDPListen:
		var ingress tcpListenConfigAPI
		if err := json.Unmarshal(stored.Ingress.Config, &ingress); err != nil {
			return protocol.ProxyNewRequest{}, fmt.Errorf("decode udp_listen ingress config: %w", err)
		}
		config.Type = protocol.ProxyTypeUDP
		config.RemotePort = ingress.Port
		config.BindIP = normalizeServerBindIP(ingress.BindIP)
	case protocol.IngressTypeTCPListen:
		var ingress tcpListenConfigAPI
		if err := json.Unmarshal(stored.Ingress.Config, &ingress); err != nil {
			return protocol.ProxyNewRequest{}, fmt.Errorf("decode tcp_listen ingress config: %w", err)
		}
		config.Type = protocol.ProxyTypeTCP
		config.RemotePort = ingress.Port
		config.BindIP = normalizeServerBindIP(ingress.BindIP)
	case protocol.IngressTypeSOCKS5Listen:
		listenCfg, err := normalizeSOCKS5ListenConfig(stored.Ingress.Config, false)
		if err != nil {
			return protocol.ProxyNewRequest{}, fmt.Errorf("decode socks5_listen ingress config: %w", err)
		}
		config.Type = protocol.ProxyTypeTCP
		config.RemotePort = listenCfg.Port
		config.BindIP = normalizeServerBindIP(listenCfg.BindIP)
	default:
		return protocol.ProxyNewRequest{}, fmt.Errorf("unsupported server ingress type %q", stored.Ingress.Type)
	}

	switch stored.Target.Type {
	case protocol.TargetTypeTCPService, protocol.TargetTypeUDPService:
		var target serviceConfigAPI
		if err := json.Unmarshal(stored.Target.Config, &target); err != nil {
			return protocol.ProxyNewRequest{}, fmt.Errorf("decode service target config: %w", err)
		}
		host := target.Host
		if host == "" {
			host = target.IP
		}
		config.LocalIP = host
		config.LocalPort = target.Port
	case protocol.TargetTypeSOCKS5ConnectHandler:
		config.LocalIP = ""
		config.LocalPort = 0
	default:
		return protocol.ProxyNewRequest{}, fmt.Errorf("unsupported target type %q", stored.Target.Type)
	}
	return config, nil
}

func (s *Server) notifyServerExposeTargetUnprovision(client *ClientConn, config protocol.ProxyConfig, reason string) error {
	if config.Topology == TunnelTopologyServerExpose && config.ID != "" && config.Revision > 0 {
		return s.notifyClientTunnelUnprovision(client, config.ID, config.Revision, protocol.DataStreamRoleTarget, reason)
	}
	if config.Name == "" {
		return fmt.Errorf("server-expose target unprovision missing tunnel identity")
	}
	return s.notifyClientProxyClose(client, config.Name, reason)
}

func (s *Server) notifyRuntimeErrorUnprovision(client *ClientConn, tunnel *ProxyTunnel, config protocol.ProxyConfig) error {
	if config.Topology == TunnelTopologyServerExpose && config.ID != "" && config.Revision > 0 {
		return s.notifyServerExposeTargetUnprovision(client, config, "runtime_error")
	}

	// Legacy cleanup is name-scoped. Avoid sending a late ProxyClose after the
	// map entry has already been replaced by a newer same-name runtime.
	client.proxyMu.RLock()
	current := client.proxies[config.Name]
	stillCurrent := current == tunnel && current != nil && current.Config.ID == config.ID
	client.proxyMu.RUnlock()
	if !stillCurrent {
		return nil
	}
	return s.notifyServerExposeTargetUnprovision(client, config, "runtime_error")
}

func (s *Server) unprovisionServerExposeTunnel(stored StoredTunnel, reason string, removeRuntime bool) error {
	if stored.Topology != TunnelTopologyServerExpose && stored.Topology != "" {
		return nil
	}
	clientID := stored.OwnerClientID
	if clientID == "" {
		clientID = stored.ClientID
	}
	client, ok := s.loadLiveClient(clientID)
	if !ok {
		return nil
	}

	var errs []error
	closedRuntimeName := ""
	client.proxyMu.Lock()
	for name, tunnel := range client.proxies {
		if tunnel.Config.ID != stored.ID || tunnel.Config.Revision != stored.Revision {
			continue
		}
		closeTunnelRuntimeResources(tunnel)
		if removeRuntime {
			delete(client.proxies, name)
		}
		closedRuntimeName = name
		break
	}
	client.proxyMu.Unlock()
	if closedRuntimeName != "" {
		log.Printf("🛑 proxy tunnel runtime closed: %s", closedRuntimeName)
	}
	if err := s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), reason); err != nil {
		errs = append(errs, fmt.Errorf("notify target client %s: %w", clientID, err))
	}
	return errors.Join(errs...)
}
