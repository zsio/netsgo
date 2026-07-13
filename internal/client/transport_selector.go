package client

import (
	"errors"
	"fmt"
	"net"

	"netsgo/pkg/protocol"
)

var (
	errPeerDirectUnavailable = errors.New("peer-direct transport unavailable")
	errPeerDirectOpenFailed  = errors.New("peer-direct stream open failed")
)

// ingressStreamTransport is the endpoint-independent stream contract used by
// TCP, UDP-over-stream, and SOCKS5 ingress runtimes. Implementations must open
// exactly one new logical stream and write its DataStreamHeader before return.
type ingressStreamTransport interface {
	Name() string
	Available() bool
	Open(req protocol.TunnelProvisionRequest, openClientID string, mutate func(*protocol.DataStreamHeader)) (net.Conn, error)
}

type ingressTransportSelector struct {
	relay  ingressStreamTransport
	direct ingressStreamTransport
}

type relayIngressTransport struct {
	runtime *sessionRuntime
}

func (t relayIngressTransport) Name() string { return protocol.ActualTransportServerRelay }

func (t relayIngressTransport) Available() bool {
	if t.runtime == nil {
		return false
	}
	t.runtime.dataMu.RLock()
	session := t.runtime.dataSession
	t.runtime.dataMu.RUnlock()
	return session != nil && !session.IsClosed()
}

func (t relayIngressTransport) Open(req protocol.TunnelProvisionRequest, openClientID string, mutate func(*protocol.DataStreamHeader)) (net.Conn, error) {
	if t.runtime == nil {
		return nil, fmt.Errorf("data session unavailable")
	}
	t.runtime.dataMu.RLock()
	session := t.runtime.dataSession
	t.runtime.dataMu.RUnlock()
	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("data session unavailable")
	}
	stream, err := session.Open()
	if err != nil {
		return nil, err
	}
	header, err := ingressDataStreamHeader(req, openClientID, protocol.ActualTransportServerRelay)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if mutate != nil {
		mutate(&header)
	}
	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

func (c *Client) ingressTransportSelector(rt *sessionRuntime, req protocol.TunnelProvisionRequest) ingressTransportSelector {
	selector := ingressTransportSelector{relay: relayIngressTransport{runtime: rt}}
	if rt != nil && rt.peerManager != nil {
		selector.direct = rt.peerManager.transportFor(req)
	}
	return selector
}

func (s ingressTransportSelector) Open(req protocol.TunnelProvisionRequest, openClientID string, mutate func(*protocol.DataStreamHeader)) (net.Conn, string, error) {
	policy := req.Spec.TransportPolicy
	if policy == "" {
		policy = protocol.TransportPolicyServerRelayOnly
	}
	switch policy {
	case protocol.TransportPolicyServerRelayOnly:
		return openSelectedIngressTransport(s.relay, req, openClientID, mutate)
	case protocol.TransportPolicyDirectPreferred:
		if transportAvailable(s.direct) {
			conn, actual, err := openSelectedIngressTransport(s.direct, req, openClientID, mutate)
			if err == nil {
				return conn, actual, nil
			}
		}
		return openSelectedIngressTransport(s.relay, req, openClientID, mutate)
	case protocol.TransportPolicyDirectOnly:
		if !transportAvailable(s.direct) {
			return nil, "", errPeerDirectUnavailable
		}
		conn, actual, err := openSelectedIngressTransport(s.direct, req, openClientID, mutate)
		if err != nil {
			return nil, actual, fmt.Errorf("%w: %v", errPeerDirectOpenFailed, err)
		}
		return conn, actual, nil
	default:
		return nil, "", fmt.Errorf("unsupported transport policy %q", policy)
	}
}

func transportAvailable(transport ingressStreamTransport) bool {
	return transport != nil && transport.Available()
}

func openSelectedIngressTransport(transport ingressStreamTransport, req protocol.TunnelProvisionRequest, openClientID string, mutate func(*protocol.DataStreamHeader)) (net.Conn, string, error) {
	if transport == nil {
		return nil, "", fmt.Errorf("transport unavailable")
	}
	if !transport.Available() {
		if transport.Name() == protocol.ActualTransportServerRelay {
			return nil, transport.Name(), fmt.Errorf("data session unavailable")
		}
		return nil, transport.Name(), fmt.Errorf("transport unavailable")
	}
	conn, err := transport.Open(req, openClientID, mutate)
	return conn, transport.Name(), err
}
