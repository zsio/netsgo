package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"netsgo/internal/socks5wire"
	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

type clientRelayRegistry struct {
	mu       sync.RWMutex
	runtimes map[string]clientRelayRuntime
}

type clientRelayRuntime struct {
	stored StoredTunnel
	limits *directionalBandwidthRuntime
}

func newClientRelayRegistry() *clientRelayRegistry {
	return &clientRelayRegistry{runtimes: make(map[string]clientRelayRuntime)}
}

func (r *clientRelayRegistry) set(stored StoredTunnel) {
	if r == nil || stored.ID == "" {
		return
	}
	r.mu.Lock()
	r.runtimes[stored.ID] = clientRelayRuntime{
		stored: stored,
		limits: newDirectionalBandwidthRuntime(stored.BandwidthSettings, realBandwidthClock{}),
	}
	r.mu.Unlock()
}

func (r *clientRelayRegistry) delete(tunnelID string) {
	if r == nil || tunnelID == "" {
		return
	}
	r.mu.Lock()
	delete(r.runtimes, tunnelID)
	r.mu.Unlock()
}

func (r *clientRelayRegistry) get(tunnelID string) (StoredTunnel, bool) {
	if r == nil || tunnelID == "" {
		return StoredTunnel{}, false
	}
	r.mu.RLock()
	runtime, ok := r.runtimes[tunnelID]
	r.mu.RUnlock()
	return runtime.stored, ok
}

func (r *clientRelayRegistry) limits(tunnelID string) *directionalBandwidthRuntime {
	if r == nil || tunnelID == "" {
		return nil
	}
	r.mu.RLock()
	runtime, ok := r.runtimes[tunnelID]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return runtime.limits
}

func (s *Server) reconcileClientRelayTunnel(stored StoredTunnel) error {
	if stored.Topology != TunnelTopologyClientToClient {
		return nil
	}
	if stored.DesiredState == protocol.ProxyDesiredStateStopped {
		if err := s.unprovisionClientRelayTunnel(stored, "stopped"); err != nil {
			return err
		}
		s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateIdle, "")
	}
	if stored.Ingress.Type != TunnelIngressTypeTCPListen &&
		stored.Ingress.Type != TunnelIngressTypeUDPListen &&
		stored.Ingress.Type != TunnelIngressTypeSOCKS5Listen {
		return nil
	}
	if !s.isClientOnline(stored.Ingress.ClientID) || !s.isClientOnline(stored.Target.ClientID) {
		if err := s.unprovisionClientRelayTunnel(stored, "participant_offline"); err != nil {
			log.Printf("⚠️ failed to unprovision client relay tunnel %s after participant offline: %v", stored.ID, err)
		}
		s.unifiedRuntime.clearServerIssues(stored.ID, stored.Revision)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
	}

	ingressClient, ok := s.loadLiveClient(stored.Ingress.ClientID)
	if !ok || !clientHasDataSession(ingressClient) {
		if err := s.unprovisionClientRelayTunnel(stored, "ingress_data_offline"); err != nil {
			log.Printf("⚠️ failed to unprovision client relay tunnel %s after ingress data offline: %v", stored.ID, err)
		}
		s.unifiedRuntime.clearServerIssues(stored.ID, stored.Revision)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
	}
	targetClient, ok := s.loadLiveClient(stored.Target.ClientID)
	if !ok || !clientHasDataSession(targetClient) {
		if err := s.unprovisionClientRelayTunnel(stored, "target_data_offline"); err != nil {
			log.Printf("⚠️ failed to unprovision client relay tunnel %s after target data offline: %v", stored.ID, err)
		}
		s.unifiedRuntime.clearServerIssues(stored.ID, stored.Revision)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
	}
	if issues := s.capabilityIssuesForStoredTunnel(stored); len(issues) > 0 {
		if err := s.unprovisionClientRelayTunnel(stored, "capability_not_supported"); err != nil {
			log.Printf("⚠️ failed to unprovision client relay tunnel %s after capability loss: %v", stored.ID, err)
		}
		s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, issues[0].Message)
	}

	if active, ok := s.c2c.get(stored.ID); ok && active.Revision == stored.Revision &&
		isActiveRuntimeState(stored.RuntimeState) &&
		!s.unifiedRuntime.hasIssuesForStoredTunnel(stored, true) {
		if err := s.ensureP2PForTunnel(stored, ingressClient, targetClient); err != nil {
			log.Printf("⚠️ failed to reconcile P2P session for tunnel %s: %v", stored.ID, err)
		}
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateExposed, "")
	}

	s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
	s.c2c.delete(stored.ID)
	if err := s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStatePending, ""); err != nil {
		return err
	}
	if err := s.waitForClientTunnelProvisionAck(targetClient, protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleTarget,
		Spec:     tunnelSpecProtocolForRole(stored, protocol.ProxyRuntimeStatePending, protocol.DataStreamRoleTarget),
	}); err != nil {
		if cleanupErr := s.unprovisionClientRelayTunnel(stored, "target_provision_failed"); cleanupErr != nil {
			log.Printf("⚠️ failed to clean up client relay tunnel %s after target provision failure: %v", stored.ID, cleanupErr)
		}
		s.recordClientRelayProvisionIssue(stored, protocol.DataStreamRoleTarget, err)
		_ = s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, err.Error())
		return err
	}
	if err := s.waitForClientTunnelProvisionAck(ingressClient, protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Spec:     tunnelSpecProtocolForRole(stored, protocol.ProxyRuntimeStatePending, protocol.DataStreamRoleIngress),
	}); err != nil {
		if cleanupErr := s.unprovisionClientRelayTunnel(stored, "ingress_provision_failed"); cleanupErr != nil {
			log.Printf("⚠️ failed to clean up client relay tunnel %s after ingress provision failure: %v", stored.ID, cleanupErr)
		}
		s.recordClientRelayProvisionIssue(stored, protocol.DataStreamRoleIngress, err)
		_ = s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, err.Error())
		return err
	}
	s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
	s.c2c.set(stored)
	if err := s.ensureP2PForTunnel(stored, ingressClient, targetClient); err != nil {
		log.Printf("⚠️ failed to prepare P2P session for tunnel %s: %v", stored.ID, err)
	}
	return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateExposed, "")
}

func (s *Server) recordClientRelayProvisionIssue(stored StoredTunnel, role string, err error) {
	code := protocol.TunnelIssueCodeProvisionAckRejected
	if errors.Is(err, errTunnelProvisionAckTimeout) {
		code = protocol.TunnelIssueCodeProvisionAckTimeout
	} else if errors.Is(err, errTunnelProvisionAckCancelled) {
		code = protocol.TunnelIssueCodeProvisionAckCancelled
	}
	clientID := stored.Target.ClientID
	scope := "target_client"
	if role == protocol.DataStreamRoleIngress {
		clientID = stored.Ingress.ClientID
		scope = "ingress_client"
	}
	s.unifiedRuntime.recordServerIssue(stored.ID, stored.Revision, protocol.TunnelIssue{
		Code:       code,
		Scope:      scope,
		ClientID:   clientID,
		Severity:   "error",
		Message:    tunnelProvisionErrorMessage(err),
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
	})
}

func (s *Server) unprovisionClientRelayTunnel(stored StoredTunnel, reason string) error {
	if stored.Topology != TunnelTopologyClientToClient {
		return nil
	}
	s.c2c.delete(stored.ID)
	if s.p2p != nil {
		s.sendP2PLifecycleResult(s.p2p.revokeTunnel(stored.ID, stored.Revision, reason))
	}
	var errs []error
	if ingressClient, ok := s.loadLiveClient(stored.Ingress.ClientID); ok {
		if err := s.notifyClientTunnelUnprovision(ingressClient, stored.ID, stored.Revision, protocol.DataStreamRoleIngress, reason); err != nil {
			errs = append(errs, fmt.Errorf("notify ingress client %s: %w", stored.Ingress.ClientID, err))
		}
	}
	if targetClient, ok := s.loadLiveClient(stored.Target.ClientID); ok {
		if err := s.notifyClientTunnelUnprovision(targetClient, stored.ID, stored.Revision, protocol.DataStreamRoleTarget, reason); err != nil {
			errs = append(errs, fmt.Errorf("notify target client %s: %w", stored.Target.ClientID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) updateStoredTunnelRuntime(stored StoredTunnel, runtimeState, message string) error {
	_, err := s.updateStoredTunnelRuntimeIfCurrent(stored, runtimeState, message)
	return err
}

func (s *Server) updateStoredTunnelRuntimeIfCurrent(stored StoredTunnel, runtimeState, message string) (bool, error) {
	return s.updateStoredTunnelRuntimeObserved(stored, runtimeState, message)
}

func (s *Server) transitionStoredTunnelRuntimeIfCurrent(stored StoredTunnel, expectedRuntimeState, runtimeState, message string) (bool, error) {
	return s.transitionStoredTunnelRuntimeObserved(stored, expectedRuntimeState, runtimeState, message)
}

func (s *Server) notifyClientTunnelProvision(client *ClientConn, req protocol.TunnelProvisionRequest) error {
	msg, err := protocol.NewMessage(protocol.MsgTypeTunnelProvision, req)
	if err != nil {
		return err
	}
	return s.writeControlMessage(client, msg)
}

func (s *Server) waitForClientTunnelProvisionAck(client *ClientConn, req protocol.TunnelProvisionRequest) error {
	if req.TunnelID == "" {
		return fmt.Errorf("tunnel provision request missing tunnel id")
	}
	if req.Revision <= 0 {
		return fmt.Errorf("tunnel %q missing revision", req.TunnelID)
	}
	ch, err := s.tunnels.registerProvisionAckWaiter(client, req.TunnelID, uint64(req.Revision), req.Role)
	if err != nil {
		return err
	}
	if err := s.notifyClientTunnelProvision(client, req); err != nil {
		s.tunnels.unregisterProvisionAckWaiter(client, req.TunnelID, uint64(req.Revision), req.Role)
		return err
	}

	timeout := s.tunnels.tunnelReadyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp, ok := <-ch:
		if !ok {
			return errTunnelProvisionAckCancelled
		}
		if !resp.accepted {
			return &tunnelProvisionRejectedError{name: req.TunnelID, message: resp.message}
		}
		return nil
	case <-s.done:
		s.tunnels.unregisterProvisionAckWaiter(client, req.TunnelID, uint64(req.Revision), req.Role)
		return errTunnelProvisionAckCancelled
	case <-timer.C:
		s.tunnels.unregisterProvisionAckWaiter(client, req.TunnelID, uint64(req.Revision), req.Role)
		return errTunnelProvisionAckTimeout
	}
}

func (s *Server) notifyClientTunnelUnprovision(client *ClientConn, tunnelID string, revision int64, role, reason string) error {
	msg, err := protocol.NewMessage(protocol.MsgTypeTunnelUnprovision, protocol.TunnelUnprovisionRequest{
		TunnelID: tunnelID,
		Revision: revision,
		Role:     role,
		Reason:   reason,
	})
	if err != nil {
		return err
	}
	return s.writeControlMessage(client, msg)
}

func tunnelSpecProtocolForRole(stored StoredTunnel, runtimeState, role string) protocol.TunnelSpec {
	actual := stored.ActualTransport
	if actual == "" {
		actual = protocol.ActualTransportUnknown
	}
	if isActiveRuntimeState(runtimeState) {
		actual = protocol.ActualTransportServerRelay
	}
	if runtimeState == protocol.ProxyRuntimeStateExposed {
		runtimeState = protocol.TunnelRuntimeStateActive
	}
	return protocol.TunnelSpec{
		ID:              stored.ID,
		Name:            stored.Name,
		Revision:        stored.Revision,
		Topology:        stored.Topology,
		OwnerClientID:   stored.OwnerClientID,
		Ingress:         endpointSpecProtocolFromStoredForRole(stored.Ingress, role),
		Target:          endpointSpecProtocolFromStoredForRole(stored.Target, role),
		TransportPolicy: stored.TransportPolicy,
		ActualTransport: actual,
		P2P:             protocol.P2PState{State: stored.P2P.State, Error: stored.P2P.Error, SessionID: stored.P2P.SessionID},
		DesiredState:    stored.DesiredState,
		RuntimeState:    runtimeState,
		BandwidthSettings: protocol.BandwidthSettings{
			IngressBPS: stored.IngressBPS,
			EgressBPS:  stored.EgressBPS,
			TotalBPS:   stored.TotalBPS,
		},
		CreatedAt: stored.CreatedAt,
		UpdatedAt: stored.UpdatedAt,
	}
}

func endpointSpecProtocolFromStored(endpoint EndpointSpec) protocol.EndpointSpec {
	return endpointSpecProtocolFromStoredForRole(endpoint, "")
}

func endpointSpecProtocolFromStoredForRole(endpoint EndpointSpec, role string) protocol.EndpointSpec {
	config := endpoint.Config
	if role == protocol.DataStreamRoleTarget {
		switch endpoint.Type {
		case protocol.IngressTypeSOCKS5Listen:
			config = redactSOCKS5ListenConfig(endpoint.Config)
		case protocol.IngressTypeHTTPHost:
			config = redactHTTPHostConfig(endpoint.Config)
		}
	}
	return protocol.EndpointSpec{
		Location: endpoint.Location,
		ClientID: endpoint.ClientID,
		Type:     endpoint.Type,
		Config:   config,
	}
}

func (s *Server) handleClientOpenedDataStream(openClient *ClientConn, openStream net.Conn, header protocol.DataStreamHeader) {
	defer func() { _ = openStream.Close() }()

	stored, ok := s.c2c.get(header.TunnelID)
	if !ok {
		log.Printf("⚠️ client relay stream for unknown tunnel %s", header.TunnelID)
		return
	}
	if err := validateClientRelayHeader(stored, openClient.ID, header); err != nil {
		log.Printf("⚠️ client relay stream rejected: %v", err)
		return
	}
	targetClient, ok := s.loadLiveClient(stored.Target.ClientID)
	if !ok {
		log.Printf("⚠️ client relay target offline: %s", stored.Target.ClientID)
		_ = s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
		if stored.Ingress.Type == TunnelIngressTypeSOCKS5Listen {
			_ = socks5wire.WriteDialResult(openStream, protocol.SOCKS5DialResult{
				Status:  protocol.SOCKS5DialStatusNetworkUnreachable,
				Message: "target client offline",
			})
		}
		return
	}

	targetStream, err := s.openRelayStreamToTarget(targetClient, stored, header)
	if err != nil {
		log.Printf("⚠️ client relay open target stream failed: %v", err)
		s.unifiedRuntime.recordServerIssue(stored.ID, stored.Revision, protocol.TunnelIssue{
			Code:       protocol.TunnelIssueCodeTargetStreamOpenFailed,
			Scope:      "transport",
			ClientID:   stored.Target.ClientID,
			Severity:   "error",
			Message:    fmt.Sprintf("failed to open target client data stream: %v", err),
			Retryable:  true,
			ObservedAt: time.Now().UTC(),
		})
		_ = s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, err.Error())
		if stored.Ingress.Type == TunnelIngressTypeSOCKS5Listen {
			_ = socks5wire.WriteDialResult(openStream, protocol.SOCKS5DialResult{
				Status:  protocol.SOCKS5DialStatusGeneralFailure,
				Message: err.Error(),
			})
		}
		return
	}
	defer func() { _ = targetStream.Close() }()

	if stored.Ingress.Type == TunnelIngressTypeUDPListen {
		s.relayClientUDPFrames(stored, targetStream, openStream, targetClient.BandwidthRuntime(), s.c2c.limits(stored.ID))
		return
	}
	if stored.Ingress.Type == TunnelIngressTypeSOCKS5Listen {
		if err := relaySOCKS5DialResultFrame(openStream, targetStream); err != nil {
			log.Printf("⚠️ client relay SOCKS5 dial result failed [%s]: %v", stored.ID, err)
			return
		}
	}

	_, _ = relayTunnelPayload(targetStream, openStream, targetClient.BandwidthRuntime(), s.c2c.limits(stored.ID), func(ingressBytes, egressBytes uint64) {
		s.recordStoredTunnelTrafficAt(time.Now(), stored, ingressBytes, egressBytes)
	})
}

func relaySOCKS5DialResultFrame(ingressStream, targetStream net.Conn) error {
	result, err := socks5wire.ReadDialResult(targetStream)
	if err != nil {
		return err
	}
	return socks5wire.WriteDialResult(ingressStream, result)
}

func (s *Server) relayClientUDPFrames(stored StoredTunnel, targetStream, ingressStream net.Conn, clientRuntime, tunnelRuntime *directionalBandwidthRuntime) {
	ingressSlots := payloadBudgetSlots(payloadDirectionIngress, clientRuntime, tunnelRuntime)
	egressSlots := payloadBudgetSlots(payloadDirectionEgress, clientRuntime, tunnelRuntime)

	var once sync.Once
	closeAll := func() {
		_ = targetStream.Close()
		_ = ingressStream.Close()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			payload, err := mux.ReadUDPFrame(ingressStream)
			if err != nil {
				if err != io.EOF {
					log.Printf("⚠️ client relay UDP read ingress frame failed [%s]: %v", stored.ID, err)
				}
				once.Do(closeAll)
				return
			}
			reserveFullPayloadBandwidth(len(payload), ingressSlots...)
			tunnelRuntime.reserveShared(payloadDirectionIngress, len(payload))
			if err := mux.WriteUDPFrame(targetStream, payload); err != nil {
				log.Printf("⚠️ client relay UDP write target frame failed [%s]: %v", stored.ID, err)
				once.Do(closeAll)
				return
			}
			s.recordStoredTunnelTrafficAt(time.Now(), stored, uint64(len(payload)), 0)
		}
	}()
	go func() {
		defer wg.Done()
		for {
			payload, err := mux.ReadUDPFrame(targetStream)
			if err != nil {
				if err != io.EOF {
					log.Printf("⚠️ client relay UDP read target frame failed [%s]: %v", stored.ID, err)
				}
				once.Do(closeAll)
				return
			}
			reserveFullPayloadBandwidth(len(payload), egressSlots...)
			tunnelRuntime.reserveShared(payloadDirectionEgress, len(payload))
			if err := mux.WriteUDPFrame(ingressStream, payload); err != nil {
				log.Printf("⚠️ client relay UDP write ingress frame failed [%s]: %v", stored.ID, err)
				once.Do(closeAll)
				return
			}
			s.recordStoredTunnelTrafficAt(time.Now(), stored, 0, uint64(len(payload)))
		}
	}()

	wg.Wait()
}

func validateClientRelayHeader(stored StoredTunnel, openClientID string, header protocol.DataStreamHeader) error {
	if header.Revision != stored.Revision {
		return fmt.Errorf("stale revision %d for tunnel %s current=%d", header.Revision, stored.ID, stored.Revision)
	}
	if header.OpenClientID != openClientID || header.OpenClientID != stored.Ingress.ClientID {
		return fmt.Errorf("open client %s is not ingress client %s", header.OpenClientID, stored.Ingress.ClientID)
	}
	if header.SourceRole != protocol.DataStreamRoleIngress || header.TargetRole != protocol.DataStreamRoleTarget {
		return fmt.Errorf("invalid relay roles source=%s target=%s", header.SourceRole, header.TargetRole)
	}
	if header.Direction != protocol.DataStreamDirectionIngressToTarget {
		return fmt.Errorf("invalid relay direction %s", header.Direction)
	}
	if header.Transport != protocol.ActualTransportServerRelay {
		return fmt.Errorf("invalid relay transport %s", header.Transport)
	}
	if stored.Ingress.Type == TunnelIngressTypeSOCKS5Listen {
		if header.TargetHost == "" || header.TargetPort < 1 || header.TargetPort > 65535 {
			return fmt.Errorf("missing SOCKS5 dynamic target for tunnel %s", stored.ID)
		}
		if header.TargetAddrType != protocol.SOCKS5AddrTypeIPv4 &&
			header.TargetAddrType != protocol.SOCKS5AddrTypeIPv6 &&
			header.TargetAddrType != protocol.SOCKS5AddrTypeDomain {
			return fmt.Errorf("invalid SOCKS5 target address type %q", header.TargetAddrType)
		}
	}
	return nil
}

func (s *Server) openRelayStreamToTarget(client *ClientConn, stored StoredTunnel, ingressHeader protocol.DataStreamHeader) (net.Conn, error) {
	if client.generation != 0 && !s.isCurrentLive(client.ID, client.generation) {
		return nil, fmt.Errorf("client [%s] is not online", client.ID)
	}
	client.dataMu.RLock()
	session := client.dataSession
	client.dataMu.RUnlock()
	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("client [%s] data channel not established", client.ID)
	}
	stream, err := session.Open()
	if err != nil {
		return nil, fmt.Errorf("OpenStream failed: %w", err)
	}
	streamID, err := protocol.NewDataStreamID()
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	header := protocol.DataStreamHeader{
		Kind:             protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:         stored.ID,
		Revision:         stored.Revision,
		StreamID:         streamID,
		OpenClientID:     client.ID,
		SourceRole:       protocol.DataStreamRoleServer,
		TargetRole:       protocol.DataStreamRoleTarget,
		Direction:        protocol.DataStreamDirectionIngressToTarget,
		Transport:        protocol.ActualTransportServerRelay,
		ServerAuthorized: true,
		TargetHost:       ingressHeader.TargetHost,
		TargetPort:       ingressHeader.TargetPort,
		TargetAddrType:   ingressHeader.TargetAddrType,
		OriginalHost:     ingressHeader.OriginalHost,
	}
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("write DataStreamHeader failed: %w", err)
	}
	return stream, nil
}
