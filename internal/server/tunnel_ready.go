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

type provisionAckResult struct {
	name     string
	accepted bool
	message  string
}

func (s *Server) registerTunnelProvisionAckWaiter(client *ClientConn, name string) (<-chan provisionAckResult, error) {
	key := pendingTunnelProvisionAckKey{
		clientID:   client.ID,
		generation: client.generation,
		name:       name,
	}

	s.pendingProvisionAckMu.Lock()
	defer s.pendingProvisionAckMu.Unlock()

	if _, exists := s.pendingProvisionAcks[key]; exists {
		return nil, fmt.Errorf("隧道 %q 已存在未完成的 provisioning ack 等待", name)
	}

	ch := make(chan provisionAckResult, 1)
	s.pendingProvisionAcks[key] = ch
	return ch, nil
}

func (s *Server) unregisterTunnelProvisionAckWaiter(client *ClientConn, name string) {
	key := pendingTunnelProvisionAckKey{
		clientID:   client.ID,
		generation: client.generation,
		name:       name,
	}

	s.pendingProvisionAckMu.Lock()
	delete(s.pendingProvisionAcks, key)
	s.pendingProvisionAckMu.Unlock()
}

func (s *Server) resolveTunnelProvisionAckWaiter(clientID string, generation uint64, resp provisionAckResult) bool {
	if resp.name == "" {
		return false
	}

	key := pendingTunnelProvisionAckKey{
		clientID:   clientID,
		generation: generation,
		name:       resp.name,
	}

	s.pendingProvisionAckMu.Lock()
	ch, ok := s.pendingProvisionAcks[key]
	if ok {
		delete(s.pendingProvisionAcks, key)
	}
	s.pendingProvisionAckMu.Unlock()
	if !ok {
		return false
	}

	ch <- resp
	close(ch)
	return true
}

func (s *Server) cancelTunnelProvisionAckWaiters(clientID string, generation uint64) {
	s.pendingProvisionAckMu.Lock()
	defer s.pendingProvisionAckMu.Unlock()

	for key, ch := range s.pendingProvisionAcks {
		if key.clientID == clientID && key.generation == generation {
			delete(s.pendingProvisionAcks, key)
			close(ch)
		}
	}
}

func (s *Server) waitForTunnelProvisionAck(client *ClientConn, req protocol.ProxyNewRequest) (provisionAckResult, error) {
	ch, err := s.registerTunnelProvisionAckWaiter(client, req.Name)
	if err != nil {
		return provisionAckResult{}, err
	}

	if err := s.notifyClientProxyProvision(client, req); err != nil {
		s.unregisterTunnelProvisionAckWaiter(client, req.Name)
		return provisionAckResult{}, err
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
			return provisionAckResult{}, errTunnelProvisionAckCancelled
		}
		if !resp.accepted {
			return resp, &tunnelProvisionRejectedError{name: req.Name, message: resp.message}
		}
		return resp, nil
	case <-timer.C:
		s.unregisterTunnelProvisionAckWaiter(client, req.Name)
		return provisionAckResult{}, errTunnelProvisionAckTimeout
	}
}
