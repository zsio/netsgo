package server

import (
	"sync/atomic"
	"time"

	"netsgo/pkg/protocol"
)

const (
	tunnelParticipantRoleIngress = "ingress"
	tunnelParticipantRoleTarget  = "target"

	tunnelParticipantStatePendingProvision = "provision_pending"
	tunnelParticipantStateTargetReady      = "target_ready"
	tunnelParticipantStateIngressReady     = "ingress_ready"
	tunnelParticipantStateOffline          = "offline"
	tunnelParticipantStateIdle             = "idle"
	tunnelParticipantStateError            = "error"

	tunnelTransportStatePending     = "pending"
	tunnelTransportStateServerRelay = "server_relay"
	tunnelTransportStateOffline     = "offline"
	tunnelTransportStateIdle        = "idle"
	tunnelTransportStateError       = "error"
)

var nextTunnelRuntimeRevision atomic.Uint64

type participantRuntimeSnapshot struct {
	Role      string    `json:"role"`
	ClientID  string    `json:"client_id,omitempty"`
	State     string    `json:"state"`
	Revision  uint64    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
	Message   string    `json:"message,omitempty"`
}

type transportRuntimeSnapshot struct {
	State     string    `json:"state"`
	Revision  uint64    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
	Message   string    `json:"message,omitempty"`
}

type tunnelRuntimeSnapshot struct {
	Revision        uint64                     `json:"revision"`
	RuntimeState    string                     `json:"runtime_state"`
	Ingress         participantRuntimeSnapshot `json:"ingress"`
	Target          participantRuntimeSnapshot `json:"target"`
	Transport       transportRuntimeSnapshot   `json:"transport"`
	LastProvisioned time.Time                  `json:"last_provisioned,omitempty"`
	UpdatedAt       time.Time                  `json:"updated_at"`
}

func newTunnelRuntimeRevision() uint64 {
	return nextTunnelRuntimeRevision.Add(1)
}

func ensureTunnelRuntimeRevision(tunnel *ProxyTunnel) uint64 {
	if tunnel == nil {
		return 0
	}
	if tunnel.runtime.Revision == 0 {
		tunnel.runtime.Revision = newTunnelRuntimeRevision()
	}
	return tunnel.runtime.Revision
}

func initializeTunnelRuntimeFromState(tunnel *ProxyTunnel, clientID string, now time.Time) {
	if tunnel == nil {
		return
	}
	revision := ensureTunnelRuntimeRevision(tunnel)
	tunnel.runtime.UpdatedAt = now
	tunnel.runtime.RuntimeState = tunnel.Config.RuntimeState
	tunnel.runtime.Ingress = participantRuntimeSnapshot{
		Role:      tunnelParticipantRoleIngress,
		State:     participantStateForRuntime(tunnel.Config.RuntimeState, tunnelParticipantRoleIngress),
		Revision:  revision,
		UpdatedAt: now,
	}
	tunnel.runtime.Target = participantRuntimeSnapshot{
		Role:      tunnelParticipantRoleTarget,
		ClientID:  clientID,
		State:     participantStateForRuntime(tunnel.Config.RuntimeState, tunnelParticipantRoleTarget),
		Revision:  revision,
		UpdatedAt: now,
	}
	tunnel.runtime.Transport = transportRuntimeSnapshot{
		State:     transportStateForRuntime(tunnel.Config.RuntimeState),
		Revision:  revision,
		UpdatedAt: now,
	}
}

func participantStateForRuntime(runtimeState, role string) string {
	switch runtimeState {
	case protocol.ProxyRuntimeStatePending:
		return tunnelParticipantStatePendingProvision
	case protocol.ProxyRuntimeStateExposed:
		if role == tunnelParticipantRoleIngress {
			return tunnelParticipantStateIngressReady
		}
		return tunnelParticipantStateTargetReady
	case protocol.ProxyRuntimeStateOffline:
		return tunnelParticipantStateOffline
	case protocol.ProxyRuntimeStateIdle:
		return tunnelParticipantStateIdle
	case protocol.ProxyRuntimeStateError:
		return tunnelParticipantStateError
	default:
		return tunnelParticipantStateError
	}
}

func transportStateForRuntime(runtimeState string) string {
	switch runtimeState {
	case protocol.ProxyRuntimeStatePending:
		return tunnelTransportStatePending
	case protocol.ProxyRuntimeStateExposed:
		return tunnelTransportStateServerRelay
	case protocol.ProxyRuntimeStateOffline:
		return tunnelTransportStateOffline
	case protocol.ProxyRuntimeStateIdle:
		return tunnelTransportStateIdle
	case protocol.ProxyRuntimeStateError:
		return tunnelTransportStateError
	default:
		return tunnelTransportStateError
	}
}

func markTunnelProvisionPending(tunnel *ProxyTunnel, clientID string, revision uint64, now time.Time) {
	if tunnel == nil {
		return
	}
	if revision == 0 {
		revision = ensureTunnelRuntimeRevision(tunnel)
	}
	tunnel.runtime.Revision = revision
	tunnel.runtime.RuntimeState = protocol.ProxyRuntimeStatePending
	tunnel.runtime.Ingress = participantRuntimeSnapshot{Role: tunnelParticipantRoleIngress, State: tunnelParticipantStatePendingProvision, Revision: revision, UpdatedAt: now}
	tunnel.runtime.Target = participantRuntimeSnapshot{Role: tunnelParticipantRoleTarget, ClientID: clientID, State: tunnelParticipantStatePendingProvision, Revision: revision, UpdatedAt: now}
	tunnel.runtime.Transport = transportRuntimeSnapshot{State: tunnelTransportStatePending, Revision: revision, UpdatedAt: now}
	tunnel.runtime.LastProvisioned = now
	tunnel.runtime.UpdatedAt = now
}

func markTunnelServerRelayActive(tunnel *ProxyTunnel, clientID string, now time.Time) {
	if tunnel == nil {
		return
	}
	revision := ensureTunnelRuntimeRevision(tunnel)
	tunnel.runtime.RuntimeState = protocol.ProxyRuntimeStateExposed
	tunnel.runtime.Ingress = participantRuntimeSnapshot{Role: tunnelParticipantRoleIngress, State: tunnelParticipantStateIngressReady, Revision: revision, UpdatedAt: now}
	tunnel.runtime.Target = participantRuntimeSnapshot{Role: tunnelParticipantRoleTarget, ClientID: clientID, State: tunnelParticipantStateTargetReady, Revision: revision, UpdatedAt: now}
	tunnel.runtime.Transport = transportRuntimeSnapshot{State: tunnelTransportStateServerRelay, Revision: revision, UpdatedAt: now}
	tunnel.runtime.UpdatedAt = now
}

func markTunnelRuntimeOffline(tunnel *ProxyTunnel, clientID string, now time.Time) {
	if tunnel == nil {
		return
	}
	revision := ensureTunnelRuntimeRevision(tunnel)
	tunnel.runtime.RuntimeState = protocol.ProxyRuntimeStateOffline
	tunnel.runtime.Ingress = participantRuntimeSnapshot{Role: tunnelParticipantRoleIngress, State: tunnelParticipantStateOffline, Revision: revision, UpdatedAt: now}
	tunnel.runtime.Target = participantRuntimeSnapshot{Role: tunnelParticipantRoleTarget, ClientID: clientID, State: tunnelParticipantStateOffline, Revision: revision, UpdatedAt: now}
	tunnel.runtime.Transport = transportRuntimeSnapshot{State: tunnelTransportStateOffline, Revision: revision, UpdatedAt: now}
	tunnel.runtime.UpdatedAt = now
}

func markTunnelRuntimeError(tunnel *ProxyTunnel, clientID, message string, now time.Time) {
	if tunnel == nil {
		return
	}
	revision := ensureTunnelRuntimeRevision(tunnel)
	tunnel.runtime.RuntimeState = protocol.ProxyRuntimeStateError
	tunnel.runtime.Ingress = participantRuntimeSnapshot{Role: tunnelParticipantRoleIngress, State: tunnelParticipantStateError, Revision: revision, UpdatedAt: now, Message: message}
	tunnel.runtime.Target = participantRuntimeSnapshot{Role: tunnelParticipantRoleTarget, ClientID: clientID, State: tunnelParticipantStateError, Revision: revision, UpdatedAt: now, Message: message}
	tunnel.runtime.Transport = transportRuntimeSnapshot{State: tunnelTransportStateError, Revision: revision, UpdatedAt: now, Message: message}
	tunnel.runtime.UpdatedAt = now
}

func updateTunnelRuntimeFromConfig(tunnel *ProxyTunnel, clientID, message string, now time.Time) {
	if tunnel == nil {
		return
	}
	switch tunnel.Config.RuntimeState {
	case protocol.ProxyRuntimeStatePending:
		markTunnelProvisionPending(tunnel, clientID, tunnel.runtime.Revision, now)
	case protocol.ProxyRuntimeStateExposed:
		markTunnelServerRelayActive(tunnel, clientID, now)
	case protocol.ProxyRuntimeStateOffline:
		markTunnelRuntimeOffline(tunnel, clientID, now)
	case protocol.ProxyRuntimeStateError:
		markTunnelRuntimeError(tunnel, clientID, message, now)
	case protocol.ProxyRuntimeStateIdle:
		revision := ensureTunnelRuntimeRevision(tunnel)
		tunnel.runtime.RuntimeState = protocol.ProxyRuntimeStateIdle
		tunnel.runtime.Ingress = participantRuntimeSnapshot{Role: tunnelParticipantRoleIngress, State: tunnelParticipantStateIdle, Revision: revision, UpdatedAt: now}
		tunnel.runtime.Target = participantRuntimeSnapshot{Role: tunnelParticipantRoleTarget, ClientID: clientID, State: tunnelParticipantStateIdle, Revision: revision, UpdatedAt: now}
		tunnel.runtime.Transport = transportRuntimeSnapshot{State: tunnelTransportStateIdle, Revision: revision, UpdatedAt: now}
		tunnel.runtime.UpdatedAt = now
	default:
		markTunnelRuntimeError(tunnel, clientID, "unknown runtime state", now)
	}
}

func aggregateTunnelRuntimeState(rt tunnelRuntimeSnapshot) string {
	if rt.RuntimeState != "" {
		return rt.RuntimeState
	}
	switch {
	case rt.Ingress.State == tunnelParticipantStateIdle || rt.Target.State == tunnelParticipantStateIdle || rt.Transport.State == tunnelTransportStateIdle:
		return protocol.ProxyRuntimeStateIdle
	case rt.Ingress.State == tunnelParticipantStateError || rt.Target.State == tunnelParticipantStateError || rt.Transport.State == tunnelTransportStateError:
		return protocol.ProxyRuntimeStateError
	case rt.Ingress.State == tunnelParticipantStateOffline || rt.Target.State == tunnelParticipantStateOffline || rt.Transport.State == tunnelTransportStateOffline:
		return protocol.ProxyRuntimeStateOffline
	case rt.Ingress.State == tunnelParticipantStatePendingProvision || rt.Target.State == tunnelParticipantStatePendingProvision || rt.Transport.State == tunnelTransportStatePending:
		return protocol.ProxyRuntimeStatePending
	case rt.Ingress.State == tunnelParticipantStateIngressReady && rt.Transport.State == tunnelTransportStateServerRelay:
		return protocol.ProxyRuntimeStateExposed
	default:
		return protocol.ProxyRuntimeStateError
	}
}
