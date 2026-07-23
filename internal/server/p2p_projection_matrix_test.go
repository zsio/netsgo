package server

import (
	"testing"

	"netsgo/pkg/protocol"
)

func TestP2PProjectionStateMatrix(t *testing.T) {
	base := StoredTunnel{DesiredState: protocol.ProxyDesiredStateRunning, RuntimeState: protocol.ProxyRuntimeStateExposed, P2P: P2PState{State: protocol.P2PStateGathering, SessionID: "session-1"}}
	tests := []struct {
		name, policy string
		mode         P2PProjectionMode
		wantState    string
		wantActual   string
		wantSession  string
	}{
		{"preferred ready", TunnelTransportDirectPreferred, P2PProjectionReady, protocol.P2PStateConnected, protocol.ActualTransportPeerDirect, "session-1"},
		{"only ready", TunnelTransportDirectOnly, P2PProjectionReady, protocol.P2PStateConnected, protocol.ActualTransportPeerDirect, "session-1"},
		{"preferred failed", TunnelTransportDirectPreferred, P2PProjectionFailed, protocol.P2PStateFallback, protocol.ActualTransportServerRelay, "session-1"},
		{"only failed", TunnelTransportDirectOnly, P2PProjectionFailed, protocol.P2PStateFailed, protocol.ActualTransportUnknown, "session-1"},
		{"preferred closed", TunnelTransportDirectPreferred, P2PProjectionClosed, protocol.P2PStateClosed, protocol.ActualTransportServerRelay, ""},
		{"only closed", TunnelTransportDirectOnly, P2PProjectionClosed, protocol.P2PStateClosed, protocol.ActualTransportUnknown, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := base
			stored.TransportPolicy = tt.policy
			state, message, sessionID, actual := p2pProjectionValues(stored, P2PProjectionTransition{Mode: tt.mode, SessionID: "session-1"})
			if state != tt.wantState || message != "" || sessionID != tt.wantSession || actual != tt.wantActual {
				t.Fatalf("projection = state=%q message=%q session=%q actual=%q", state, message, sessionID, actual)
			}
			if stored.RuntimeState != protocol.ProxyRuntimeStateExposed {
				t.Fatalf("P2P projection mutated runtime state: %q", stored.RuntimeState)
			}
		})
	}
}

func TestP2PClosedProjectionKeepsStoppedTunnelOffRelay(t *testing.T) {
	stored := StoredTunnel{DesiredState: protocol.ProxyDesiredStateStopped, TransportPolicy: TunnelTransportDirectPreferred, ActualTransport: protocol.ActualTransportPeerDirect}
	state, _, _, actual := p2pProjectionValues(stored, P2PProjectionTransition{Mode: P2PProjectionClosed})
	if state != protocol.P2PStateClosed || actual != protocol.ActualTransportUnknown {
		t.Fatalf("stopped close projection state=%q actual=%q", state, actual)
	}
}

func TestFailedStatusCloseKeepsFailureProjection(t *testing.T) {
	closed := closeP2PAfterFailedStatus(p2pLifecycleResult{
		ClosedEdge: true,
		Transition: P2PProjectionTransition{Mode: P2PProjectionClosed},
	})
	if closed.Transition.Mode != "" {
		t.Fatalf("failed status close projection mode = %q, want no second projection", closed.Transition.Mode)
	}
	if !closed.ClosedEdge {
		t.Fatal("failed status close lost lifecycle edge")
	}
}
