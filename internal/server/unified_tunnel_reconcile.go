package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"netsgo/pkg/protocol"
)

const unifiedTunnelRetryInterval = time.Minute

func (s *Server) reconcileUnifiedTunnel(tunnelID, reason string) error {
	if strings.TrimSpace(tunnelID) == "" {
		return fmt.Errorf("tunnel id is required for unified reconcile")
	}
	stored, ok, err := s.findStoredTunnelByID(tunnelID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrTunnelNotFound
	}
	return s.reconcileStoredUnifiedTunnel(stored, reason)
}

func (s *Server) reconcileStoredUnifiedTunnel(stored StoredTunnel, reason string) error {
	_ = reason // reserved for runtime diagnostics and retry scheduling.
	switch stored.Topology {
	case TunnelTopologyClientToClient:
		return s.reconcileClientRelayTunnel(stored)
	case TunnelTopologyServerExpose, "":
		return s.reconcileServerExposeTunnel(stored)
	default:
		return fmt.Errorf("unsupported tunnel topology %q", stored.Topology)
	}
}

func (s *Server) scheduleUnifiedTunnelReconcile(stored StoredTunnel, reason string) {
	go func() {
		_ = s.reconcileStoredUnifiedTunnel(stored, reason)
	}()
}

func (s *Server) unifiedTunnelReconcileLoop() {
	ticker := time.NewTicker(unifiedTunnelRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.reconcileRunningUnifiedTunnels("retry")
		}
	}
}

func (s *Server) reconcileRunningUnifiedTunnels(reason string) {
	if s == nil || s.store == nil {
		return
	}
	tunnels, err := s.store.GetAllTunnels()
	if err != nil {
		return
	}
	for _, stored := range tunnels {
		if stored.DesiredState != protocol.ProxyDesiredStateRunning {
			continue
		}
		_ = s.reconcileStoredUnifiedTunnel(stored, reason)
	}
}

func (s *Server) findStoredTunnelByID(tunnelID string) (StoredTunnel, bool, error) {
	if s.store == nil {
		return StoredTunnel{}, false, fmt.Errorf("tunnel store not initialized")
	}
	tunnels, err := s.store.GetAllTunnels()
	if err != nil {
		return StoredTunnel{}, false, err
	}
	for _, stored := range tunnels {
		if stored.ID == tunnelID {
			return stored, true, nil
		}
	}
	return StoredTunnel{}, false, nil
}

func (s *Server) reconcileServerExposeTunnel(stored StoredTunnel) error {
	if stored.DesiredState == protocol.ProxyDesiredStateStopped {
		s.unifiedRuntime.clearTunnelIssues(stored.ID)
		if client, ok := s.loadLiveClient(stored.OwnerClientID); ok {
			if name, _, exists := findTunnelBySelector(client, stored.ID); exists {
				_ = s.CloseProxyRuntime(client, name)
				_ = s.notifyClientProxyClose(client, name, "stopped")
			}
		}
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateIdle, "")
	}

	client, ok := s.loadLiveClient(stored.OwnerClientID)
	if !ok || !clientHasDataSession(client) {
		s.unifiedRuntime.clearTunnelIssues(stored.ID)
		if ok {
			if name, _, exists := findTunnelBySelector(client, stored.ID); exists {
				_ = s.CloseProxyRuntime(client, name)
				_ = s.notifyClientProxyClose(client, name, "participant_offline")
			}
		}
		return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
	}

	if name, tunnel, exists := findTunnelBySelector(client, stored.ID); exists {
		if tunnel.Config.DesiredState == protocol.ProxyDesiredStateRunning && serverExposeRuntimeHeld(tunnel) {
			if s.unifiedRuntime.hasIssuesForStoredTunnel(stored, true) {
				s.removeTunnelRuntime(client, name)
				_ = s.notifyClientProxyClose(client, name, "retrying_after_runtime_issue")
			} else {
				s.unifiedRuntime.clearServerIssues(stored.ID)
				return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateExposed, "")
			}
		}
		if tunnel.Config.DesiredState == protocol.ProxyDesiredStateRunning && tunnel.Config.RuntimeState == protocol.ProxyRuntimeStatePending {
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStatePending, "")
		}
		if tunnel.Config.DesiredState == protocol.ProxyDesiredStateStopped {
			s.unifiedRuntime.clearTunnelIssues(stored.ID)
			_ = s.CloseProxyRuntime(client, name)
			_ = s.notifyClientProxyClose(client, name, "stopped")
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateIdle, "")
		}
		if isTunnelExposed(tunnel.Config) && !serverExposeRuntimeHeld(tunnel) {
			s.removeTunnelRuntime(client, name)
		}
	}

	s.unifiedRuntime.clearTunnelIssues(stored.ID)
	if err := s.restoreManagedTunnel(client, stored); err != nil {
		s.recordServerExposeReconcileIssue(stored, err)
		return err
	}
	s.unifiedRuntime.clearServerIssues(stored.ID)
	return nil
}

func serverExposeRuntimeHeld(tunnel *ProxyTunnel) bool {
	if tunnel == nil || !isTunnelExposed(tunnel.Config) {
		return false
	}
	switch tunnel.Config.Type {
	case protocol.ProxyTypeHTTP:
		return true
	case protocol.ProxyTypeUDP:
		return tunnel.UDPState != nil
	default:
		return tunnel.Listener != nil
	}
}

func (s *Server) recordServerExposeReconcileIssue(stored StoredTunnel, err error) {
	if err == nil {
		return
	}
	var rejected *tunnelProvisionRejectedError
	switch {
	case errors.Is(err, errTunnelProvisionAckTimeout):
		s.recordServerExposeProvisionIssue(stored, protocol.TunnelIssueCodeProvisionAckTimeout, err)
	case errors.Is(err, errTunnelProvisionAckCancelled):
		s.recordServerExposeProvisionIssue(stored, protocol.TunnelIssueCodeProvisionAckCancelled, err)
	case errors.As(err, &rejected):
		s.recordServerExposeProvisionIssue(stored, protocol.TunnelIssueCodeProvisionAckRejected, err)
	default:
		s.recordServerExposeIngressIssue(stored.ID, stored.Type, err.Error())
	}
}

func (s *Server) recordServerExposeProvisionIssue(stored StoredTunnel, code string, err error) {
	s.unifiedRuntime.recordServerIssue(stored.ID, protocol.TunnelIssue{
		Code:       code,
		Scope:      "target_client",
		ClientID:   stored.Target.ClientID,
		Severity:   "error",
		Message:    tunnelProvisionErrorMessage(err),
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
	})
}

func (s *Server) recordServerExposeIngressIssue(tunnelID, tunnelType, message string) {
	message = strings.TrimSpace(message)
	if tunnelID == "" || message == "" {
		return
	}
	s.unifiedRuntime.recordServerIssue(tunnelID, protocol.TunnelIssue{
		Code:       serverExposeIngressIssueCode(tunnelType, message),
		Scope:      "server",
		Severity:   "error",
		Message:    message,
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
	})
}

func serverExposeIngressIssueCode(tunnelType, message string) string {
	if tunnelType == protocol.ProxyTypeHTTP {
		return protocol.TunnelIssueCodeIngressRouteFailed
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "address already in use") || strings.Contains(lower, "only one usage of each socket address") {
		return protocol.TunnelIssueCodeIngressPortInUse
	}
	return protocol.TunnelIssueCodeIngressListenFailed
}

func (s *Server) reconcileTunnelsForClient(clientID, reason string) {
	if s == nil || s.store == nil || strings.TrimSpace(clientID) == "" {
		return
	}
	tunnels, err := s.store.GetAllTunnels()
	if err != nil {
		return
	}
	for _, stored := range tunnels {
		if stored.OwnerClientID == clientID || stored.Target.ClientID == clientID || stored.Ingress.ClientID == clientID {
			_ = s.reconcileStoredUnifiedTunnel(stored, reason)
		}
	}
}

func (s *Server) releaseUnifiedRuntimeForClient(clientID string) {
	if s == nil || s.store == nil || strings.TrimSpace(clientID) == "" {
		return
	}
	tunnels, err := s.store.GetAllTunnels()
	if err != nil {
		return
	}
	for _, stored := range tunnels {
		if stored.OwnerClientID == clientID || stored.Target.ClientID == clientID || stored.Ingress.ClientID == clientID {
			s.unprovisionClientRelayTunnel(stored, "participant_session_released")
		}
	}
}
