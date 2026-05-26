package server

import (
	"errors"
	"fmt"
	"time"

	"netsgo/pkg/protocol"
)

func (s *Server) restoreUnifiedServerExposeTunnel(client *ClientConn, stored StoredTunnel) error {
	tunnel, err := s.prepareProxyTunnelWithExclusions(
		client,
		stored.ProxyNewRequest,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStatePending,
		stored.Name,
		client.ID,
		stored.CreatedAt,
	)
	if err != nil {
		return err
	}
	s.applyStoredServerExposeConfig(client, tunnel, stored, protocol.ProxyRuntimeStatePending, "")
	if err := s.persistTunnelStates(client.ID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, ""); err != nil {
		s.removeTunnelRuntime(client, stored.Name)
		return err
	}
	s.emitTunnelChanged(client.ID, tunnel.Config, "pending")

	req := protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleTarget,
		Spec:     tunnelSpecProtocolFromStored(stored, protocol.ProxyRuntimeStatePending),
	}
	if err := s.waitForClientTunnelProvisionAck(client, req); err != nil {
		if errors.Is(err, errTunnelProvisionAckCancelled) {
			return err
		}
		s.failUnifiedServerExposeAfterProvision(client, stored, tunnelProvisionErrorMessage(err))
		return err
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
	s.applyStoredServerExposeConfig(client, updated, stored, protocol.ProxyRuntimeStateExposed, "")
	s.emitTunnelChanged(client.ID, updated.Config, "restored")
	return nil
}

func (s *Server) applyStoredServerExposeConfig(client *ClientConn, tunnel *ProxyTunnel, stored StoredTunnel, runtimeState, message string) {
	if tunnel == nil {
		return
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
}

func (s *Server) failUnifiedServerExposeAfterProvision(client *ClientConn, stored StoredTunnel, message string) {
	s.removeTunnelRuntime(client, stored.Name)
	_ = s.notifyServerExposeTargetUnprovision(client, storedTunnelToProxyConfig(stored), "provision_failed")
	config := s.upsertTunnelPlaceholderWithRevision(client, stored.ProxyNewRequest, stored.Revision, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message, stored.CreatedAt)
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
