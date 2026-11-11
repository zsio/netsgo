package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"netsgo/internal/socks5wire"
	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

type clientRelayRegistry struct {
	mu       sync.RWMutex
	runtimes map[string]clientRelayRuntime
}

const lifecycleControlWriteTimeout = 2 * time.Second

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
	ownerEpoch, releaseOwnerGate, err := s.acquireStoredTunnelLifecycle(stored, 0)
	if err != nil {
		return err
	}
	releaseOwnerGate()
	return s.reconcileClientRelayTunnelAtEpoch(stored, ownerEpoch)
}

func (s *Server) reconcileClientRelayTunnelAtEpoch(stored StoredTunnel, ownerEpoch uint64) error {
	return s.reconcileClientRelayTunnelForTask(stored, ownerEpoch, nil)
}

func (s *Server) reconcileClientRelayTunnelForTask(stored StoredTunnel, ownerEpoch uint64, task *unifiedTunnelReconcileTask) error {
	if stored.Topology != TunnelTopologyClientToClient {
		return nil
	}
	publish := func(fn func() error) error {
		return s.withUnifiedTunnelReconcilePublication(stored, ownerEpoch, task, fn)
	}
	if stored.DesiredState == protocol.ProxyDesiredStateStopped {
		return publish(func() error {
			if err := s.unprovisionClientRelayTunnel(stored, "stopped"); err != nil {
				return err
			}
			s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateIdle, "")
		})
	}
	if stored.Ingress.Type != TunnelIngressTypeTCPListen &&
		stored.Ingress.Type != TunnelIngressTypeUDPListen &&
		stored.Ingress.Type != TunnelIngressTypeSOCKS5Listen {
		return nil
	}
	if !s.isClientOnline(stored.Ingress.ClientID) || !s.isClientOnline(stored.Target.ClientID) {
		return publish(func() error {
			if err := s.unprovisionClientRelayTunnel(stored, "participant_offline"); err != nil {
				log.Printf("⚠️ failed to unprovision client relay tunnel %s after participant offline: %v", stored.ID, err)
			}
			s.unifiedRuntime.clearServerIssues(stored.ID, stored.Revision)
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
		})
	}

	ingressClient, ok := s.loadUnifiedTunnelTaskLiveClient(task, stored.Ingress.ClientID)
	if !ok || !clientHasDataSession(ingressClient) {
		return publish(func() error {
			if err := s.unprovisionClientRelayTunnel(stored, "ingress_data_offline"); err != nil {
				log.Printf("⚠️ failed to unprovision client relay tunnel %s after ingress data offline: %v", stored.ID, err)
			}
			s.unifiedRuntime.clearServerIssues(stored.ID, stored.Revision)
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
		})
	}
	targetClient, ok := s.loadUnifiedTunnelTaskLiveClient(task, stored.Target.ClientID)
	if !ok || !clientHasDataSession(targetClient) {
		return publish(func() error {
			if err := s.unprovisionClientRelayTunnel(stored, "target_data_offline"); err != nil {
				log.Printf("⚠️ failed to unprovision client relay tunnel %s after target data offline: %v", stored.ID, err)
			}
			s.unifiedRuntime.clearServerIssues(stored.ID, stored.Revision)
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
		})
	}
	if issues := s.capabilityIssuesForStoredTunnel(stored); len(issues) > 0 {
		return publish(func() error {
			if err := s.unprovisionClientRelayTunnel(stored, "capability_not_supported"); err != nil {
				log.Printf("⚠️ failed to unprovision client relay tunnel %s after capability loss: %v", stored.ID, err)
			}
			s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, issues[0].Message)
		})
	}

	if active, ok := s.c2c.get(stored.ID); ok && active.Revision == stored.Revision &&
		isActiveRuntimeState(stored.RuntimeState) &&
		!s.unifiedRuntime.hasIssuesForStoredTunnel(stored, true) {
		return publish(func() error {
			if err := s.ensureP2PForTunnel(stored, ingressClient, targetClient); err != nil {
				log.Printf("⚠️ failed to reconcile P2P session for tunnel %s: %v", stored.ID, err)
			}
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateExposed, "")
		})
	}

	if err := publish(func() error {
		s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
		s.c2c.delete(stored.ID)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStatePending, "")
	}); err != nil {
		return err
	}
	if ackErr := s.waitForClientTunnelProvisionAckForTask(targetClient, protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleTarget,
		Spec:     tunnelSpecProtocolForRole(stored, protocol.ProxyRuntimeStatePending, protocol.DataStreamRoleTarget),
	}, task); ackErr != nil {
		if err := publish(func() error {
			if cleanupErr := s.unprovisionClientRelayTunnel(stored, "target_provision_failed"); cleanupErr != nil {
				log.Printf("⚠️ failed to clean up client relay tunnel %s after target provision failure: %v", stored.ID, cleanupErr)
			}
			s.recordClientRelayProvisionIssue(stored, protocol.DataStreamRoleTarget, ackErr)
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, ackErr.Error())
		}); err != nil {
			return err
		}
		return ackErr
	}
	if ackErr := s.waitForClientTunnelProvisionAckForTask(ingressClient, protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Spec:     tunnelSpecProtocolForRole(stored, protocol.ProxyRuntimeStatePending, protocol.DataStreamRoleIngress),
	}, task); ackErr != nil {
		if err := publish(func() error {
			if cleanupErr := s.unprovisionClientRelayTunnel(stored, "ingress_provision_failed"); cleanupErr != nil {
				log.Printf("⚠️ failed to clean up client relay tunnel %s after ingress provision failure: %v", stored.ID, cleanupErr)
			}
			s.recordClientRelayProvisionIssue(stored, protocol.DataStreamRoleIngress, ackErr)
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, ackErr.Error())
		}); err != nil {
			return err
		}
		return ackErr
	}
	return publish(func() error {
		currentIngress, ingressOK := s.loadLiveClient(stored.Ingress.ClientID)
		currentTarget, targetOK := s.loadLiveClient(stored.Target.ClientID)
		if !ingressOK || !targetOK || currentIngress != ingressClient || currentTarget != targetClient ||
			!s.clientLifecycleCurrentLocked(ingressClient) || !s.clientLifecycleCurrentLocked(targetClient) {
			return ErrUserLifecycleEpochChanged
		}
		current, ok, err := s.findStoredTunnelByID(stored.ID)
		if err != nil {
			return err
		}
		if !ok || current.Revision != stored.Revision {
			return errTunnelProvisionAckCancelled
		}
		s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
		s.c2c.set(stored)
		if err := s.ensureP2PForTunnel(stored, ingressClient, targetClient); err != nil {
			log.Printf("⚠️ failed to prepare P2P session for tunnel %s: %v", stored.ID, err)
		}
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateExposed, "")
	})
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

func (s *Server) waitForClientTunnelProvisionAckForTask(client *ClientConn, req protocol.TunnelProvisionRequest, task *unifiedTunnelReconcileTask) error {
	if req.TunnelID == "" {
		return fmt.Errorf("tunnel provision request missing tunnel id")
	}
	if req.Revision <= 0 {
		return fmt.Errorf("tunnel %q missing revision", req.TunnelID)
	}
	ch, err := s.beginClientTunnelProvisionAckWaitForTask(client, req, task)
	if err != nil {
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

func (s *Server) beginClientTunnelProvisionAckWait(client *ClientConn, req protocol.TunnelProvisionRequest) (<-chan provisionAckResult, error) {
	return s.beginClientTunnelProvisionAckWaitForTask(client, req, nil)
}

func (s *Server) beginClientTunnelProvisionAckWaitForTask(client *ClientConn, req protocol.TunnelProvisionRequest, task *unifiedTunnelReconcileTask) (<-chan provisionAckResult, error) {
	if client == nil {
		return nil, errTunnelProvisionAckCancelled
	}
	releaseOwnerGate := func() {}
	if client.OwnerUserID != "" {
		_, release, err := s.acquireUserLifecycleRead(client.OwnerUserID, client.OwnerEpoch, true)
		if err != nil {
			return nil, err
		}
		releaseOwnerGate = release
	}
	defer releaseOwnerGate()

	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
	if task != nil {
		taskCurrent, err := s.unifiedTunnelReconcileTaskCurrentLocked(*task)
		if err != nil {
			return nil, err
		}
		if !taskCurrent {
			return nil, ErrUserLifecycleEpochChanged
		}
	}
	current, ok := s.clients.Load(client.ID)
	if !ok || current != client || !client.isLive() || !s.clientLifecycleCurrentLocked(client) {
		return nil, errTunnelProvisionAckCancelled
	}
	ch, err := s.tunnels.registerProvisionAckWaiter(client, req.TunnelID, uint64(req.Revision), req.Role)
	if err != nil {
		return nil, err
	}

	req.IngressNetworkID = s.ingressNetworkIdentity(req.Spec.Ingress)
	msg, err := protocol.NewMessage(protocol.MsgTypeTunnelProvision, req)
	if err == nil {
		err = s.writeControlMessageBefore(client, msg, time.Now().Add(lifecycleControlWriteTimeout))
	}
	if err != nil {
		s.tunnels.unregisterProvisionAckWaiter(client, req.TunnelID, uint64(req.Revision), req.Role)
		return nil, err
	}
	return ch, nil
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

type clientRelayStreamGrant struct {
	stored        StoredTunnel
	sourceClient  *ClientConn
	sourceSession *yamux.Session
	targetClient  *ClientConn
	targetSession *yamux.Session
	ownerEpoch    uint64
}

func (s *Server) handleClientOpenedDataStream(openClient *ClientConn, openStream net.Conn, header protocol.DataStreamHeader) {
	s.handleClientOpenedDataStreamInternal(openClient, nil, openStream, header, false)
}

func (s *Server) handleClientOpenedDataStreamForSession(openClient *ClientConn, sourceSession *yamux.Session, openStream net.Conn, header protocol.DataStreamHeader) {
	s.handleClientOpenedDataStreamInternal(openClient, sourceSession, openStream, header, true)
}

func (s *Server) handleClientOpenedDataStreamInternal(openClient *ClientConn, sourceSession *yamux.Session, openStream net.Conn, header protocol.DataStreamHeader, strictSession bool) {
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

	var grant clientRelayStreamGrant
	var targetClient *ClientConn
	if strictSession {
		var err error
		grant, err = s.authorizeClientRelayStream(openClient, sourceSession, header)
		if err != nil {
			log.Printf("⚠️ client relay stream authorization failed: %v", err)
			if stored.Ingress.Type == TunnelIngressTypeSOCKS5Listen {
				_ = socks5wire.WriteDialResult(openStream, protocol.SOCKS5DialResult{
					Status:  protocol.SOCKS5DialStatusNetworkUnreachable,
					Message: "target client offline",
				})
			}
			return
		}
		targetClient = grant.targetClient
	} else {
		targetClient, _ = s.loadLiveClient(stored.Target.ClientID)
	}
	if targetClient == nil {
		log.Printf("⚠️ client relay target offline: %s", stored.Target.ClientID)
		if stored.Ingress.Type == TunnelIngressTypeSOCKS5Listen {
			_ = socks5wire.WriteDialResult(openStream, protocol.SOCKS5DialResult{
				Status:  protocol.SOCKS5DialStatusNetworkUnreachable,
				Message: "target client offline",
			})
		}
		return
	}

	var targetStream net.Conn
	var err error
	if strictSession {
		targetStream, err = s.openRelayStreamForGrant(grant, header)
	} else {
		targetStream, err = s.openRelayStreamToTarget(targetClient, stored, header)
	}
	if err != nil {
		log.Printf("⚠️ client relay open target stream failed: %v", err)
		if strictSession {
			s.publishClientRelayStreamOpenFailure(grant, err)
		} else {
			s.recordClientRelayStreamOpenFailure(stored, err)
		}
		if stored.Ingress.Type == TunnelIngressTypeSOCKS5Listen {
			_ = socks5wire.WriteDialResult(openStream, protocol.SOCKS5DialResult{
				Status:  protocol.SOCKS5DialStatusGeneralFailure,
				Message: err.Error(),
			})
		}
		return
	}
	defer func() { _ = targetStream.Close() }()

	ownerEpoch := uint64(0)
	if strictSession {
		ownerEpoch = grant.ownerEpoch
	}
	if stored.Ingress.Type == TunnelIngressTypeUDPListen {
		s.relayClientUDPFrames(stored, ownerEpoch, targetStream, openStream, targetClient.BandwidthRuntime(), s.c2c.limits(stored.ID))
		return
	}
	if stored.Ingress.Type == TunnelIngressTypeSOCKS5Listen {
		if err := relaySOCKS5DialResultFrame(openStream, targetStream); err != nil {
			log.Printf("⚠️ client relay SOCKS5 dial result failed [%s]: %v", stored.ID, err)
			return
		}
	}

	_, _ = relayTunnelPayload(targetStream, openStream, targetClient.BandwidthRuntime(), s.c2c.limits(stored.ID), func(ingressBytes, egressBytes uint64) {
		s.recordStoredTunnelTrafficAtEpoch(time.Now(), stored, ownerEpoch, ingressBytes, egressBytes)
	})
}

func (s *Server) authorizeClientRelayStream(openClient *ClientConn, sourceSession *yamux.Session, header protocol.DataStreamHeader) (clientRelayStreamGrant, error) {
	if openClient == nil || sourceSession == nil || openClient.OwnerUserID == "" {
		return clientRelayStreamGrant{}, ErrUserLifecycleEpochChanged
	}
	ownerEpoch := openClient.OwnerEpoch
	_, releaseOwnerGate, err := s.acquireUserLifecycleRead(openClient.OwnerUserID, ownerEpoch, true)
	if err != nil {
		return clientRelayStreamGrant{}, err
	}
	defer releaseOwnerGate()

	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
	stored, ok := s.c2c.get(header.TunnelID)
	if !ok || validateClientRelayHeader(stored, openClient.ID, header) != nil || stored.OwnerUserID != openClient.OwnerUserID {
		return clientRelayStreamGrant{}, ErrUserLifecycleEpochChanged
	}
	targetClient, ok := s.loadLiveClient(stored.Target.ClientID)
	if !ok {
		return clientRelayStreamGrant{}, fmt.Errorf("target client %s is offline", stored.Target.ClientID)
	}
	targetClient.dataMu.RLock()
	targetSession := targetClient.dataSession
	targetClient.dataMu.RUnlock()
	grant := clientRelayStreamGrant{
		stored:        stored,
		sourceClient:  openClient,
		sourceSession: sourceSession,
		targetClient:  targetClient,
		targetSession: targetSession,
		ownerEpoch:    ownerEpoch,
	}
	if !s.clientRelayStreamGrantCurrentLocked(grant, true) {
		return clientRelayStreamGrant{}, ErrUserLifecycleEpochChanged
	}
	return grant, nil
}

func (s *Server) clientRelayStreamGrantCurrentLocked(grant clientRelayStreamGrant, requireOpenSessions bool) bool {
	if grant.sourceClient == nil || grant.sourceSession == nil || grant.targetClient == nil || grant.targetSession == nil {
		return false
	}
	source, sourceOK := s.clients.Load(grant.sourceClient.ID)
	target, targetOK := s.clients.Load(grant.targetClient.ID)
	if !sourceOK || source != grant.sourceClient || !targetOK || target != grant.targetClient ||
		!grant.sourceClient.isLive() || !grant.targetClient.isLive() ||
		grant.sourceClient.OwnerUserID != grant.stored.OwnerUserID || grant.targetClient.OwnerUserID != grant.stored.OwnerUserID ||
		grant.sourceClient.OwnerEpoch != grant.ownerEpoch || grant.targetClient.OwnerEpoch != grant.ownerEpoch ||
		!s.clientLifecycleCurrentLocked(grant.sourceClient) || !s.clientLifecycleCurrentLocked(grant.targetClient) {
		return false
	}
	grant.sourceClient.dataMu.RLock()
	sourceSessionCurrent := grant.sourceClient.dataSession == grant.sourceSession
	grant.sourceClient.dataMu.RUnlock()
	grant.targetClient.dataMu.RLock()
	targetSessionCurrent := grant.targetClient.dataSession == grant.targetSession
	grant.targetClient.dataMu.RUnlock()
	if !sourceSessionCurrent || !targetSessionCurrent {
		return false
	}
	current, ok := s.c2c.get(grant.stored.ID)
	if !ok || current.Revision != grant.stored.Revision || current.OwnerUserID != grant.stored.OwnerUserID {
		return false
	}
	return !requireOpenSessions || (!grant.sourceSession.IsClosed() && !grant.targetSession.IsClosed())
}

func (s *Server) openRelayStreamForGrant(grant clientRelayStreamGrant, ingressHeader protocol.DataStreamHeader) (net.Conn, error) {
	stream, err := grant.targetSession.Open()
	if err != nil {
		return nil, fmt.Errorf("OpenStream failed: %w", err)
	}
	if err := s.writeClientRelayTargetHeaderAtEpoch(grant, stream, ingressHeader); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

func (s *Server) writeClientRelayTargetHeaderAtEpoch(grant clientRelayStreamGrant, stream net.Conn, ingressHeader protocol.DataStreamHeader) error {
	_, releaseOwnerGate, err := s.acquireUserLifecycleRead(grant.stored.OwnerUserID, grant.ownerEpoch, true)
	if err != nil {
		return err
	}
	defer releaseOwnerGate()
	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
	if !s.clientRelayStreamGrantCurrentLocked(grant, true) {
		return ErrUserLifecycleEpochChanged
	}
	return writeClientRelayTargetHeader(stream, grant.targetClient.ID, grant.stored, ingressHeader)
}

func (s *Server) publishClientRelayStreamOpenFailure(grant clientRelayStreamGrant, openErr error) {
	if errors.Is(openErr, ErrUserLifecycleEpochChanged) || errors.Is(openErr, ErrUserDisabled) || errors.Is(openErr, ErrUserNotFound) {
		return
	}
	_ = s.withStoredTunnelPublication(grant.stored, grant.ownerEpoch, func() error {
		if !s.clientRelayStreamGrantCurrentLocked(grant, false) {
			return ErrUserLifecycleEpochChanged
		}
		s.recordClientRelayStreamOpenFailure(grant.stored, openErr)
		return nil
	})
}

func (s *Server) recordClientRelayStreamOpenFailure(stored StoredTunnel, openErr error) {
	s.unifiedRuntime.recordServerIssue(stored.ID, stored.Revision, protocol.TunnelIssue{
		Code:       protocol.TunnelIssueCodeTargetStreamOpenFailed,
		Scope:      "transport",
		ClientID:   stored.Target.ClientID,
		Severity:   "error",
		Message:    fmt.Sprintf("failed to open target client data stream: %v", openErr),
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
	})
	_ = s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, openErr.Error())
}

func relaySOCKS5DialResultFrame(ingressStream, targetStream net.Conn) error {
	result, err := socks5wire.ReadDialResult(targetStream)
	if err != nil {
		return err
	}
	return socks5wire.WriteDialResult(ingressStream, result)
}

func (s *Server) relayClientUDPFrames(stored StoredTunnel, ownerEpoch uint64, targetStream, ingressStream net.Conn, clientRuntime, tunnelRuntime *directionalBandwidthRuntime) {
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
			s.recordStoredTunnelTrafficAtEpoch(time.Now(), stored, ownerEpoch, uint64(len(payload)), 0)
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
			s.recordStoredTunnelTrafficAtEpoch(time.Now(), stored, ownerEpoch, 0, uint64(len(payload)))
		}
	}()

	wg.Wait()
}

func (s *Server) recordStoredTunnelTrafficAtEpoch(now time.Time, stored StoredTunnel, ownerEpoch, ingressBytes, egressBytes uint64) {
	if ownerEpoch == 0 || stored.OwnerUserID == "" {
		s.recordStoredTunnelTrafficAt(now, stored, ingressBytes, egressBytes)
		return
	}
	_, releaseOwnerGate, err := s.acquireUserLifecycleRead(stored.OwnerUserID, ownerEpoch, false)
	if err != nil {
		return
	}
	defer releaseOwnerGate()
	s.recordStoredTunnelTrafficAt(now, stored, ingressBytes, egressBytes)
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
	if err := writeClientRelayTargetHeader(stream, client.ID, stored, ingressHeader); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

func writeClientRelayTargetHeader(stream net.Conn, targetClientID string, stored StoredTunnel, ingressHeader protocol.DataStreamHeader) error {
	streamID, err := protocol.NewDataStreamID()
	if err != nil {
		return err
	}
	header := protocol.DataStreamHeader{
		Kind:             protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:         stored.ID,
		Revision:         stored.Revision,
		StreamID:         streamID,
		OpenClientID:     targetClientID,
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
	if err := stream.SetWriteDeadline(time.Now().Add(lifecycleControlWriteTimeout)); err != nil {
		return fmt.Errorf("set DataStreamHeader deadline failed: %w", err)
	}
	defer func() { _ = stream.SetWriteDeadline(time.Time{}) }()
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		return fmt.Errorf("write DataStreamHeader failed: %w", err)
	}
	return nil
}
