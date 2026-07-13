package client

import (
	"errors"
	"net"
	"testing"

	"netsgo/pkg/protocol"
)

type fakeIngressTransport struct {
	name      string
	available bool
	err       error
	opens     int
}

func (f *fakeIngressTransport) Name() string    { return f.name }
func (f *fakeIngressTransport) Available() bool { return f.available }
func (f *fakeIngressTransport) Open(protocol.TunnelProvisionRequest, string, func(*protocol.DataStreamHeader)) (net.Conn, error) {
	f.opens++
	if f.err != nil {
		return nil, f.err
	}
	a, b := net.Pipe()
	_ = b.Close()
	return a, nil
}

func TestIngressTransportSelectorPolicies(t *testing.T) {
	tests := []struct {
		name        string
		policy      string
		directReady bool
		directErr   error
		wantName    string
		wantErr     error
		wantDirect  int
		wantRelay   int
	}{
		{name: "relay only ignores ready direct", policy: protocol.TransportPolicyServerRelayOnly, directReady: true, wantName: protocol.ActualTransportServerRelay, wantRelay: 1},
		{name: "preferred uses ready direct", policy: protocol.TransportPolicyDirectPreferred, directReady: true, wantName: protocol.ActualTransportPeerDirect, wantDirect: 1},
		{name: "preferred falls back when direct unavailable", policy: protocol.TransportPolicyDirectPreferred, wantName: protocol.ActualTransportServerRelay, wantRelay: 1},
		{name: "preferred falls back when direct open fails", policy: protocol.TransportPolicyDirectPreferred, directReady: true, directErr: errors.New("direct failed"), wantName: protocol.ActualTransportServerRelay, wantDirect: 1, wantRelay: 1},
		{name: "direct only rejects unavailable direct", policy: protocol.TransportPolicyDirectOnly, wantErr: errPeerDirectUnavailable},
		{name: "direct only does not fall back after open failure", policy: protocol.TransportPolicyDirectOnly, directReady: true, directErr: errors.New("direct failed"), wantName: protocol.ActualTransportPeerDirect, wantErr: errPeerDirectOpenFailed, wantDirect: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			direct := &fakeIngressTransport{name: protocol.ActualTransportPeerDirect, available: tt.directReady, err: tt.directErr}
			relay := &fakeIngressTransport{name: protocol.ActualTransportServerRelay, available: true}
			selector := ingressTransportSelector{relay: relay, direct: direct}
			conn, actual, err := selector.Open(protocol.TunnelProvisionRequest{Spec: protocol.TunnelSpec{TransportPolicy: tt.policy}}, "ingress", nil)
			if conn != nil {
				_ = conn.Close()
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error: want %v, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if actual != tt.wantName {
				t.Fatalf("actual transport: want %q, got %q", tt.wantName, actual)
			}
			if direct.opens != tt.wantDirect || relay.opens != tt.wantRelay {
				t.Fatalf("open counts: direct want=%d got=%d relay want=%d got=%d", tt.wantDirect, direct.opens, tt.wantRelay, relay.opens)
			}
		})
	}
}

func TestIngressTransportSelectorNeverOpensBothAfterDirectSuccess(t *testing.T) {
	direct := &fakeIngressTransport{name: protocol.ActualTransportPeerDirect, available: true}
	relay := &fakeIngressTransport{name: protocol.ActualTransportServerRelay, available: true}
	selector := ingressTransportSelector{relay: relay, direct: direct}

	conn, actual, err := selector.Open(protocol.TunnelProvisionRequest{Spec: protocol.TunnelSpec{TransportPolicy: protocol.TransportPolicyDirectPreferred}}, "ingress", nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if actual != protocol.ActualTransportPeerDirect || direct.opens != 1 || relay.opens != 0 {
		t.Fatalf("selector duplicated open: actual=%s direct=%d relay=%d", actual, direct.opens, relay.opens)
	}
}
