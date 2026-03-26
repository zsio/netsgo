package server

import (
	"errors"
	"fmt"
	"time"

	"netsgo/pkg/protocol"
)

var (
	errTunnelProvisionAckTimeout   = errors.New("tunnel provision ack timeout")
	errTunnelProvisionAckCancelled = errors.New("tunnel provision ack cancelled")
)

type tunnelProvisionRejectedError struct {
	name    string
	message string
}

func (e *tunnelProvisionRejectedError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("client rejected tunnel %s", e.name)
	}
	return fmt.Sprintf("client rejected tunnel %s: %s", e.name, e.message)
}

type pendingTunnelProvisionAckKey struct {
	clientID   string
	generation uint64
	name       string
}

func (s *Server) registerTunnelProvisionAckWaiter(client *ClientConn, name string) (<-chan protocol.ProxyNewResponse, error) {
	key := pendingTunnelProvisionAckKey{
		clientID:   client.ID,
		generation: client.generation,
		name:       name,
	}

	s.pendingReadyMu.Lock()
	defer s.pendingReadyMu.Unlock()

	if _, exists := s.pendingReady[key]; exists {
		return nil, fmt.Errorf("隧道 %q 已存在未完成的 provisioning ack 等待", name)
	}

	ch := make(chan protocol.ProxyNewResponse, 1)
	s.pendingReady[key] = ch
	return ch, nil
}

func (s *Server) unregisterTunnelProvisionAckWaiter(client *ClientConn, name string) {
	key := pendingTunnelProvisionAckKey{
		clientID:   client.ID,
		generation: client.generation,
		name:       name,
	}

	s.pendingReadyMu.Lock()
	delete(s.pendingReady, key)
	s.pendingReadyMu.Unlock()
}

func (s *Server) resolveTunnelProvisionAckWaiter(clientID string, generation uint64, resp protocol.ProxyNewResponse) bool {
	if resp.Name == "" {
		return false
	}

	key := pendingTunnelProvisionAckKey{
		clientID:   clientID,
		generation: generation,
		name:       resp.Name,
	}

	s.pendingReadyMu.Lock()
	ch, ok := s.pendingReady[key]
	if ok {
		delete(s.pendingReady, key)
	}
	s.pendingReadyMu.Unlock()
	if !ok {
		return false
	}

	ch <- resp
	close(ch)
	return true
}

func (s *Server) cancelTunnelProvisionAckWaiters(clientID string, generation uint64) {
	s.pendingReadyMu.Lock()
	defer s.pendingReadyMu.Unlock()

	for key, ch := range s.pendingReady {
		if key.clientID == clientID && key.generation == generation {
			delete(s.pendingReady, key)
			close(ch)
		}
	}
}

func (s *Server) waitForTunnelProvisionAck(client *ClientConn, req protocol.ProxyNewRequest) (protocol.ProxyNewResponse, error) {
	ch, err := s.registerTunnelProvisionAckWaiter(client, req.Name)
	if err != nil {
		return protocol.ProxyNewResponse{}, err
	}

	if err := s.notifyClientProxyNew(client, req); err != nil {
		s.unregisterTunnelProvisionAckWaiter(client, req.Name)
		return protocol.ProxyNewResponse{}, err
	}

	timeout := s.tunnelReadyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp, ok := <-ch:
		if !ok {
			return protocol.ProxyNewResponse{}, errTunnelProvisionAckCancelled
		}
		if !resp.Success {
			return resp, &tunnelProvisionRejectedError{name: req.Name, message: resp.Message}
		}
		return resp, nil
	case <-timer.C:
		s.unregisterTunnelProvisionAckWaiter(client, req.Name)
		return protocol.ProxyNewResponse{}, errTunnelProvisionAckTimeout
	}
}
