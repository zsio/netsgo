package server

import (
	"encoding/json"
	"errors"
	"fmt"
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
	if err := s.persistTunnelStates(client.ID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, ""); err != nil {
		s.removeTunnelRuntime(client, stored.Name)
		return err
	}
	s.emitTunnelChanged(client.ID, config, "pending")

	req := protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleTarget,
		Spec:     tunnelSpecProtocolForRole(stored, protocol.ProxyRuntimeStatePending, protocol.DataStreamRoleTarget),
	}
	if err := s.waitForClientTunnelProvisionAck(client, req); err != nil {
		if errors.Is(err, errTunnelProvisionAckCancelled) {
			s.removeTunnelRuntime(client, stored.Name)
			return err
		}
		s.recordServerExposeReconcileIssue(stored, err)
		s.failUnifiedServerExposeAfterProvision(client, stored, tunnelProvisionErrorMessage(err))
		return err
	}

	current, ok, err := s.findStoredTunnelByID(stored.ID)
	if err != nil {
		s.failUnifiedServerExposeAfterProvision(client, stored, err.Error())
		return err
	}
	if !ok || current.Revision != stored.Revision {
		s.removeTunnelRuntime(client, stored.Name)
		_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "stale_revision")
		err := errTunnelProvisionAckCancelled
		if ok {
			s.recordServerExposeReconcileIssue(current, err)
			_ = s.updateStoredTunnelRuntime(current, protocol.ProxyRuntimeStateError, tunnelProvisionErrorMessage(err))
		}
		return errTunnelProvisionAckCancelled
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return errTunnelProvisionAckCancelled
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		s.failUnifiedServerExposeAfterProvision(client, stored, err.Error())
		return err
	}
	if err := s.persistTunnelStates(client.ID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); err != nil {
		s.failUnifiedServerExposeAfterProvision(client, stored, err.Error())
		return err
	}

	updated, err := s.mustGetTunnel(client, stored.Name)
	if err != nil {
		s.failUnifiedServerExposeAfterProvision(client, stored, err.Error())
		return err
	}
	config = s.applyStoredServerExposeConfig(client, updated, stored, protocol.ProxyRuntimeStateExposed, "")
	s.emitTunnelChanged(client.ID, config, "restored")
	return nil
}

func (s *Server) applyStoredServerExposeConfig(client *ClientConn, tunnel *ProxyTunnel, stored StoredTunnel, runtimeState, message string) protocol.ProxyConfig {
	if tunnel == nil {
		return protocol.ProxyConfig{}
	}
	config := storedTunnelToProxyConfig(stored)
	setProxyConfigStates(&config, protocol.ProxyDesiredStateRunning, runtimeState, message)

	client.proxyMu.Lock()
	tunnel.Config = config
	if tunnel.limits == nil {
		tunnel.limits = newDirectionalBandwidthRuntime(config.BandwidthSettings, realBandwidthClock{})
	} else {
		tunnel.limits.Update(config.BandwidthSettings)
	}
	tunnel.runtime.Revision = uint64(stored.Revision)
	updateTunnelRuntimeFromConfig(tunnel, client.ID, message, time.Now())
	client.proxyMu.Unlock()
	return config
}

func (s *Server) failUnifiedServerExposeAfterProvision(client *ClientConn, stored StoredTunnel, message string) {
	s.removeTunnelRuntime(client, stored.Name)
	_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "provision_failed")
	runtimeConfig, err := serverExposeRuntimeProxyRequest(stored)
	if err != nil {
		runtimeConfig = protocol.ProxyNewRequest{ID: stored.ID, Name: stored.Name}
	}
	config := s.upsertTunnelPlaceholderWithRevision(client, runtimeConfig, stored.Revision, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message, stored.CreatedAt)
	mergeStoredMetadataIntoProxyConfig(&config, stored)
	client.proxyMu.Lock()
	if tunnel := client.proxies[stored.Name]; tunnel != nil {
		mergeStoredMetadataIntoProxyConfig(&tunnel.Config, stored)
		tunnel.runtime.Revision = uint64(stored.Revision)
		updateTunnelRuntimeFromConfig(tunnel, client.ID, message, time.Now())
	}
	client.proxyMu.Unlock()
	_ = s.persistTunnelStates(client.ID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	s.emitTunnelChanged(client.ID, config, "error")
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

func mergeStoredMetadataIntoProxyConfig(config *protocol.ProxyConfig, stored StoredTunnel) {
	if config == nil {
		return
	}
	config.Topology = stored.Topology
	config.OwnerClientID = stored.OwnerClientID
	if stored.Ingress.Location != "" || stored.Ingress.Type != "" {
		ingress := endpointSpecProtocolFromStored(stored.Ingress)
		config.Ingress = &ingress
	}
	if stored.Target.Location != "" || stored.Target.Type != "" || stored.Target.ClientID != "" {
		target := endpointSpecProtocolFromStored(stored.Target)
		config.Target = &target
	}
	config.TransportPolicy = stored.TransportPolicy
	config.ActualTransport = stored.ActualTransport
	if stored.P2P.State != "" || stored.P2P.Error != "" || stored.P2P.SessionID != "" {
		config.P2P = &protocol.P2PState{
			State:     stored.P2P.State,
			Error:     stored.P2P.Error,
			SessionID: stored.P2P.SessionID,
		}
	}
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
	if name, _, exists := findTunnelBySelector(client, stored.ID); exists {
		if removeRuntime {
			s.removeTunnelRuntime(client, name)
		} else if err := s.CloseProxyRuntime(client, name); err != nil {
			errs = append(errs, fmt.Errorf("close server runtime: %w", err))
		}
	}
	if err := s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), reason); err != nil {
		errs = append(errs, fmt.Errorf("notify target client %s: %w", clientID, err))
	}
	return errors.Join(errs...)
}
