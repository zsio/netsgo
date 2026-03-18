package server

import (
	"errors"
	"fmt"
	"time"

	"netsgo/pkg/protocol"
)

var (
	errTunnelReadyTimeout   = errors.New("tunnel ready timeout")
	errTunnelReadyCancelled = errors.New("tunnel ready cancelled")
)

type tunnelReadyRejectedError struct {
	name    string
	message string
}

func (e *tunnelReadyRejectedError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("client rejected tunnel %s", e.name)
	}
	return fmt.Sprintf("client rejected tunnel %s: %s", e.name, e.message)
}

type pendingTunnelReadyKey struct {
	clientID   string
	generation uint64
	name       string
}

func (s *Server) registerTunnelReadyWaiter(client *ClientConn, name string) (<-chan protocol.ProxyNewResponse, error) {
	key := pendingTunnelReadyKey{
		clientID:   client.ID,
		generation: client.generation,
		name:       name,
	}

	s.pendingReadyMu.Lock()
	defer s.pendingReadyMu.Unlock()

	if _, exists := s.pendingReady[key]; exists {
		return nil, fmt.Errorf("隧道 %q 已存在未完成的 ready 等待", name)
	}

	ch := make(chan protocol.ProxyNewResponse, 1)
	s.pendingReady[key] = ch
	return ch, nil
}

func (s *Server) unregisterTunnelReadyWaiter(client *ClientConn, name string) {
	key := pendingTunnelReadyKey{
		clientID:   client.ID,
		generation: client.generation,
		name:       name,
	}

	s.pendingReadyMu.Lock()
	delete(s.pendingReady, key)
	s.pendingReadyMu.Unlock()
}

func (s *Server) resolveTunnelReadyWaiter(clientID string, generation uint64, resp protocol.ProxyNewResponse) bool {
	if resp.Name == "" {
		return false
	}

	key := pendingTunnelReadyKey{
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

func (s *Server) cancelTunnelReadyWaiters(clientID string, generation uint64) {
	s.pendingReadyMu.Lock()
	defer s.pendingReadyMu.Unlock()

	for key, ch := range s.pendingReady {
		if key.clientID == clientID && key.generation == generation {
			delete(s.pendingReady, key)
			close(ch)
		}
	}
}

func (s *Server) waitForTunnelReady(client *ClientConn, req protocol.ProxyNewRequest) (protocol.ProxyNewResponse, error) {
	ch, err := s.registerTunnelReadyWaiter(client, req.Name)
	if err != nil {
		return protocol.ProxyNewResponse{}, err
	}

	if err := s.notifyClientProxyNew(client, req); err != nil {
		s.unregisterTunnelReadyWaiter(client, req.Name)
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
			return protocol.ProxyNewResponse{}, errTunnelReadyCancelled
		}
		if !resp.Success {
			return resp, &tunnelReadyRejectedError{name: req.Name, message: resp.Message}
		}
		return resp, nil
	case <-timer.C:
		s.unregisterTunnelReadyWaiter(client, req.Name)
		return protocol.ProxyNewResponse{}, errTunnelReadyTimeout
	}
}
