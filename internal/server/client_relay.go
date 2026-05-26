package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

type clientRelayRegistry struct {
	mu       sync.RWMutex
	runtimes map[string]StoredTunnel
}

func newClientRelayRegistry() *clientRelayRegistry {
	return &clientRelayRegistry{runtimes: make(map[string]StoredTunnel)}
}

func (r *clientRelayRegistry) set(stored StoredTunnel) {
	if r == nil || stored.ID == "" {
		return
	}
	r.mu.Lock()
	r.runtimes[stored.ID] = stored
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
	stored, ok := r.runtimes[tunnelID]
	r.mu.RUnlock()
	return stored, ok
}

func (s *Server) reconcileClientRelayTunnel(stored StoredTunnel) error {
	if stored.Topology != TunnelTopologyClientToClient {
		return nil
	}
	if stored.DesiredState == protocol.ProxyDesiredStateStopped {
		s.unprovisionClientRelayTunnel(stored, "stopped")
		s.unifiedRuntime.clearTunnelIssues(stored.ID)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateIdle, "")
	}
	if stored.Ingress.Type != TunnelIngressTypeTCPListen && stored.Ingress.Type != TunnelIngressTypeUDPListen {
		return nil
	}
	if !s.isClientOnline(stored.Ingress.ClientID) || !s.isClientOnline(stored.Target.ClientID) {
		s.unprovisionClientRelayTunnel(stored, "participant_offline")
		s.unifiedRuntime.clearServerIssues(stored.ID)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
	}

	ingressClient, ok := s.loadLiveClient(stored.Ingress.ClientID)
	if !ok || !clientHasDataSession(ingressClient) {
		s.unprovisionClientRelayTunnel(stored, "ingress_data_offline")
		s.unifiedRuntime.clearServerIssues(stored.ID)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
	}
	targetClient, ok := s.loadLiveClient(stored.Target.ClientID)
	if !ok || !clientHasDataSession(targetClient) {
		s.unprovisionClientRelayTunnel(stored, "target_data_offline")
		s.unifiedRuntime.clearServerIssues(stored.ID)
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
	}

	if active, ok := s.c2c.get(stored.ID); ok && active.Revision == stored.Revision &&
		(stored.RuntimeState == protocol.ProxyRuntimeStateExposed || stored.RuntimeState == protocol.TunnelRuntimeStateActive) &&
		!s.unifiedRuntime.hasIssuesForStoredTunnel(stored, true) {
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateExposed, "")
	}

	s.unifiedRuntime.clearTunnelIssues(stored.ID)
	s.c2c.set(stored)
	if err := s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStatePending, ""); err != nil {
		return err
	}
	if err := s.waitForClientTunnelProvisionAck(targetClient, protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleTarget,
		Spec:     tunnelSpecProtocolFromStored(stored, protocol.ProxyRuntimeStatePending),
	}); err != nil {
		s.unprovisionClientRelayTunnel(stored, "target_provision_failed")
		s.recordClientRelayProvisionIssue(stored, protocol.DataStreamRoleTarget, err)
		_ = s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, err.Error())
		return err
	}
	if err := s.waitForClientTunnelProvisionAck(ingressClient, protocol.TunnelProvisionRequest{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Spec:     tunnelSpecProtocolFromStored(stored, protocol.ProxyRuntimeStatePending),
	}); err != nil {
		s.unprovisionClientRelayTunnel(stored, "ingress_provision_failed")
		s.recordClientRelayProvisionIssue(stored, protocol.DataStreamRoleIngress, err)
		_ = s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, err.Error())
		return err
	}
	s.unifiedRuntime.clearTunnelIssues(stored.ID)
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
	s.unifiedRuntime.recordServerIssue(stored.ID, protocol.TunnelIssue{
		Code:       code,
		Scope:      scope,
		ClientID:   clientID,
		Severity:   "error",
		Message:    tunnelProvisionErrorMessage(err),
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
	})
}

func (s *Server) unprovisionClientRelayTunnel(stored StoredTunnel, reason string) {
	if stored.Topology != TunnelTopologyClientToClient {
		return
	}
	s.c2c.delete(stored.ID)
	if ingressClient, ok := s.loadLiveClient(stored.Ingress.ClientID); ok {
		_ = s.notifyClientTunnelUnprovision(ingressClient, stored.ID, stored.Revision, protocol.DataStreamRoleIngress, reason)
	}
	if targetClient, ok := s.loadLiveClient(stored.Target.ClientID); ok {
		_ = s.notifyClientTunnelUnprovision(targetClient, stored.ID, stored.Revision, protocol.DataStreamRoleTarget, reason)
	}
}

func (s *Server) updateStoredTunnelRuntime(stored StoredTunnel, runtimeState, message string) error {
	if s.store == nil {
		return nil
	}
	desired := stored.DesiredState
	if desired == "" {
		desired = protocol.ProxyDesiredStateRunning
	}
	return s.store.UpdateStates(stored.OwnerClientID, stored.Name, desired, runtimeState, message)
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

func tunnelSpecProtocolFromStored(stored StoredTunnel, runtimeState string) protocol.TunnelSpec {
	actual := stored.ActualTransport
	if actual == "" {
		actual = protocol.ActualTransportUnknown
	}
	if runtimeState == protocol.ProxyRuntimeStateExposed || runtimeState == protocol.TunnelRuntimeStateActive {
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
		Ingress:         endpointSpecProtocolFromStored(stored.Ingress),
		Target:          endpointSpecProtocolFromStored(stored.Target),
		TransportPolicy: stored.TransportPolicy,
		ActualTransport: actual,
		P2P:             protocol.P2PState{State: stored.P2P.State, Error: stored.P2P.Error, SessionID: stored.P2P.SessionID},
		DesiredState:    stored.DesiredState,
		RuntimeState:    runtimeState,
		BandwidthSettings: protocol.BandwidthSettings{
			IngressBPS: stored.IngressBPS,
			EgressBPS:  stored.EgressBPS,
		},
		CreatedAt: stored.CreatedAt,
		UpdatedAt: stored.UpdatedAt,
	}
}

func endpointSpecProtocolFromStored(endpoint EndpointSpec) protocol.EndpointSpec {
	return protocol.EndpointSpec{
		Location: endpoint.Location,
		ClientID: endpoint.ClientID,
		Type:     endpoint.Type,
		Config:   endpoint.Config,
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
		return
	}

	targetStream, err := s.openRelayStreamToTarget(targetClient, stored)
	if err != nil {
		log.Printf("⚠️ client relay open target stream failed: %v", err)
		s.unifiedRuntime.recordServerIssue(stored.ID, protocol.TunnelIssue{
			Code:       protocol.TunnelIssueCodeTargetStreamOpenFailed,
			Scope:      "transport",
			ClientID:   stored.Target.ClientID,
			Severity:   "error",
			Message:    fmt.Sprintf("failed to open target client data stream: %v", err),
			Retryable:  true,
			ObservedAt: time.Now().UTC(),
		})
		_ = s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, err.Error())
		return
	}
	defer func() { _ = targetStream.Close() }()

	if stored.Ingress.Type == TunnelIngressTypeUDPListen {
		s.relayClientUDPFrames(stored, targetStream, openStream, targetClient.BandwidthRuntime(), nil)
		return
	}

	_, _ = relayTunnelPayload(targetStream, openStream, targetClient.BandwidthRuntime(), nil, func(ingressBytes, egressBytes uint64) {
		s.recordTrafficObservationAt(time.Now(), stored.ID, stored.OwnerClientID, stored.Name, stored.Type, ingressBytes, egressBytes)
	})
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
			if err := mux.WriteUDPFrame(targetStream, payload); err != nil {
				log.Printf("⚠️ client relay UDP write target frame failed [%s]: %v", stored.ID, err)
				once.Do(closeAll)
				return
			}
			s.recordTrafficObservationAt(time.Now(), stored.ID, stored.OwnerClientID, stored.Name, stored.Type, uint64(len(payload)), 0)
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
			if err := mux.WriteUDPFrame(ingressStream, payload); err != nil {
				log.Printf("⚠️ client relay UDP write ingress frame failed [%s]: %v", stored.ID, err)
				once.Do(closeAll)
				return
			}
			s.recordTrafficObservationAt(time.Now(), stored.ID, stored.OwnerClientID, stored.Name, stored.Type, 0, uint64(len(payload)))
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
	return nil
}

func (s *Server) openRelayStreamToTarget(client *ClientConn, stored StoredTunnel) (net.Conn, error) {
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
	header := protocol.DataStreamHeader{
		Kind:             protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:         stored.ID,
		Revision:         stored.Revision,
		StreamID:         protocol.NewDataStreamID(),
		OpenClientID:     client.ID,
		SourceRole:       protocol.DataStreamRoleServer,
		TargetRole:       protocol.DataStreamRoleTarget,
		Direction:        protocol.DataStreamDirectionIngressToTarget,
		Transport:        protocol.ActualTransportServerRelay,
		ServerAuthorized: true,
	}
	if header.StreamID == "" {
		header.StreamID = generateUUID()
	}
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("write DataStreamHeader failed: %w", err)
	}
	return stream, nil
}
