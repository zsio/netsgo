package server

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

var (
	errTunnelProvisionAckTimeout   = errors.New("tunnel provision ack timeout")
	errTunnelProvisionAckCancelled = errors.New("tunnel provision ack cancelled")
	errTunnelPreflightTimeout      = errors.New("tunnel preflight timeout")
	errTunnelPreflightCancelled    = errors.New("tunnel preflight cancelled")
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
	revision   uint64
	role       string
}

type provisionAckResult struct {
	name     string
	accepted bool
	message  string
	revision uint64
	role     string
}

type pendingTunnelPreflightKey struct {
	clientID   string
	generation uint64
	requestID  string
}

// TunnelRegistry holds tunnel provisioning wait state and timeout configuration:
//   - pendingProvisionAcks: the set of channels waiting for client tunnel provision ack responses
//   - tunnelReadyTimeout: maximum timeout duration for waiting on a provision ack
//
// Other files in the same package access it directly via s.tunnels.*; no external interface is exposed.
type TunnelRegistry struct {
	pendingProvisionAckMu sync.Mutex
	pendingProvisionAcks  map[pendingTunnelProvisionAckKey]chan provisionAckResult
	tunnelReadyTimeout    time.Duration
	pendingPreflightMu    sync.Mutex
	pendingPreflights     map[pendingTunnelPreflightKey]chan protocol.TunnelPreflightResponse
	preflightTimeout      time.Duration
}

// newTunnelRegistry creates a TunnelRegistry with default timeout.
func newTunnelRegistry() *TunnelRegistry {
	return &TunnelRegistry{
		pendingProvisionAcks: make(map[pendingTunnelProvisionAckKey]chan provisionAckResult),
		tunnelReadyTimeout:   5 * time.Second,
		pendingPreflights:    make(map[pendingTunnelPreflightKey]chan protocol.TunnelPreflightResponse),
		preflightTimeout:     3 * time.Second,
	}
}

func (tr *TunnelRegistry) registerProvisionAckWaiter(client *ClientConn, name string, revision uint64, role string) (<-chan provisionAckResult, error) {
	key := pendingTunnelProvisionAckKey{
		clientID:   client.ID,
		generation: client.generation,
		name:       name,
		revision:   revision,
		role:       role,
	}

	tr.pendingProvisionAckMu.Lock()
	defer tr.pendingProvisionAckMu.Unlock()

	if _, exists := tr.pendingProvisionAcks[key]; exists {
		return nil, fmt.Errorf("tunnel %q already has a pending provisioning ack waiter", name)
	}

	ch := make(chan provisionAckResult, 1)
	tr.pendingProvisionAcks[key] = ch
	return ch, nil
}

func (tr *TunnelRegistry) unregisterProvisionAckWaiter(client *ClientConn, name string, revision uint64, role string) {
	key := pendingTunnelProvisionAckKey{
		clientID:   client.ID,
		generation: client.generation,
		name:       name,
		revision:   revision,
		role:       role,
	}

	tr.pendingProvisionAckMu.Lock()
	delete(tr.pendingProvisionAcks, key)
	tr.pendingProvisionAckMu.Unlock()
}

func (tr *TunnelRegistry) resolveProvisionAckWaiter(clientID string, generation uint64, resp provisionAckResult) bool {
	if resp.name == "" {
		return false
	}

	key := pendingTunnelProvisionAckKey{
		clientID:   clientID,
		generation: generation,
		name:       resp.name,
		revision:   resp.revision,
		role:       resp.role,
	}

	tr.pendingProvisionAckMu.Lock()
	ch, ok := tr.pendingProvisionAcks[key]
	if ok {
		delete(tr.pendingProvisionAcks, key)
	}
	tr.pendingProvisionAckMu.Unlock()
	if !ok {
		return false
	}

	ch <- resp
	close(ch)
	return true
}

func (tr *TunnelRegistry) cancelProvisionAckWaiters(clientID string, generation uint64) {
	tr.pendingProvisionAckMu.Lock()
	defer tr.pendingProvisionAckMu.Unlock()

	for key, ch := range tr.pendingProvisionAcks {
		if key.clientID == clientID && key.generation == generation {
			delete(tr.pendingProvisionAcks, key)
			close(ch)
		}
	}
}

func (tr *TunnelRegistry) waitForProvisionAck(s *Server, client *ClientConn, req protocol.ProxyNewRequest) (provisionAckResult, error) {
	if req.ProvisionRevision == 0 {
		return provisionAckResult{}, fmt.Errorf("tunnel %q missing provisioning revision", req.Name)
	}
	ch, err := tr.registerProvisionAckWaiter(client, req.Name, req.ProvisionRevision, "")
	if err != nil {
		return provisionAckResult{}, err
	}

	if err := s.notifyClientProxyProvision(client, req); err != nil {
		tr.unregisterProvisionAckWaiter(client, req.Name, req.ProvisionRevision, "")
		return provisionAckResult{}, err
	}

	timeout := tr.tunnelReadyTimeout
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
		tr.unregisterProvisionAckWaiter(client, req.Name, req.ProvisionRevision, "")
		return provisionAckResult{}, errTunnelProvisionAckTimeout
	}
}

func (tr *TunnelRegistry) registerPreflightWaiter(client *ClientConn, requestID string) (<-chan protocol.TunnelPreflightResponse, error) {
	if requestID == "" {
		return nil, fmt.Errorf("preflight request missing request_id")
	}
	key := pendingTunnelPreflightKey{clientID: client.ID, generation: client.generation, requestID: requestID}
	tr.pendingPreflightMu.Lock()
	defer tr.pendingPreflightMu.Unlock()
	if _, exists := tr.pendingPreflights[key]; exists {
		return nil, fmt.Errorf("preflight request %q already has a pending waiter", requestID)
	}
	ch := make(chan protocol.TunnelPreflightResponse, 1)
	tr.pendingPreflights[key] = ch
	return ch, nil
}

func (tr *TunnelRegistry) unregisterPreflightWaiter(client *ClientConn, requestID string) {
	key := pendingTunnelPreflightKey{clientID: client.ID, generation: client.generation, requestID: requestID}
	tr.pendingPreflightMu.Lock()
	delete(tr.pendingPreflights, key)
	tr.pendingPreflightMu.Unlock()
}

func (tr *TunnelRegistry) cancelPreflightWaiters(clientID string, generation uint64) {
	tr.pendingPreflightMu.Lock()
	defer tr.pendingPreflightMu.Unlock()

	for key, ch := range tr.pendingPreflights {
		if key.clientID == clientID && key.generation == generation {
			delete(tr.pendingPreflights, key)
			close(ch)
		}
	}
}

func (tr *TunnelRegistry) resolvePreflightWaiter(clientID string, generation uint64, resp protocol.TunnelPreflightResponse) bool {
	if resp.RequestID == "" {
		return false
	}
	key := pendingTunnelPreflightKey{clientID: clientID, generation: generation, requestID: resp.RequestID}
	tr.pendingPreflightMu.Lock()
	ch, ok := tr.pendingPreflights[key]
	if ok {
		delete(tr.pendingPreflights, key)
	}
	tr.pendingPreflightMu.Unlock()
	if !ok {
		return false
	}
	ch <- resp
	close(ch)
	return true
}
