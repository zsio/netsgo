package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"netsgo/pkg/fileutil"
	"netsgo/pkg/protocol"
)

const (
	TunnelBindingClientID = "client_id"
)

// StoredTunnel is a tunnel configuration persisted to storage.
type StoredTunnel struct {
	protocol.ProxyNewRequest
	DesiredState string `json:"desired_state,omitempty"` // User's desired state
	RuntimeState string `json:"runtime_state,omitempty"` // Actual runtime state
	Error        string `json:"error,omitempty"`         // Reason when in error state
	ClientID     string `json:"client_id,omitempty"`     // Owning stable Client ID
	Hostname     string `json:"hostname,omitempty"`      // Current hostname (for display)
	Binding      string `json:"binding,omitempty"`       // Only client_id is allowed
}

func (t *StoredTunnel) normalize() error {
	if t.Binding != TunnelBindingClientID {
		return fmt.Errorf("tunnel %q must use %q binding", t.Name, TunnelBindingClientID)
	}
	if t.ClientID == "" {
		return fmt.Errorf("tunnel %q is missing a stable client_id", t.Name)
	}
	if err := validateTunnelStates(t.DesiredState, t.RuntimeState, t.Error); err != nil {
		return err
	}
	t.DesiredState = canonicalDesiredState(t.DesiredState)
	t.Error = tunnelErrorForRuntimeState(t.RuntimeState, t.Error)
	return nil
}

func (t StoredTunnel) matchesClient(clientID, name string) bool {
	return t.Binding == TunnelBindingClientID && t.ClientID == clientID && t.Name == name
}

func (t StoredTunnel) matchesIdentifier(identifier, name string) bool {
	return t.Name == name && t.Binding == TunnelBindingClientID && t.ClientID == identifier
}

// TunnelStore is a JSON-file-backed persistent store for tunnel configurations.
type TunnelStore struct {
	path    string
	mu      sync.RWMutex
	tunnels []StoredTunnel

	// For testing only: inject a save failure to verify the rollback path.
	failSaveErr   error
	failSaveCount int
}

// NewTunnelStore creates or loads a tunnel store.
// An empty store is created if the file does not exist.
func NewTunnelStore(path string) (*TunnelStore, error) {
	store := &TunnelStore{
		path:    path,
		tunnels: []StoredTunnel{},
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		if err := store.load(); err != nil {
			return nil, fmt.Errorf("failed to load tunnel config: %w", err)
		}
	}

	return store, nil
}

func (s *TunnelStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.tunnels); err != nil {
		return err
	}
	for i := range s.tunnels {
		if err := s.tunnels[i].normalize(); err != nil {
			return fmt.Errorf("tunnel %q has invalid state: %w", s.tunnels[i].Name, err)
		}
	}
	return nil
}

func (s *TunnelStore) save() error {
	if s.failSaveErr != nil && s.failSaveCount > 0 {
		err := s.failSaveErr
		s.failSaveCount--
		if s.failSaveCount == 0 {
			s.failSaveErr = nil
		}
		return err
	}
	data, err := json.MarshalIndent(s.tunnels, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(s.path, data, 0o600)
}

func cloneStoredTunnels(tunnels []StoredTunnel) []StoredTunnel {
	cloned := make([]StoredTunnel, len(tunnels))
	copy(cloned, tunnels)
	return cloned
}

// AddTunnel adds a tunnel configuration and persists it.
func (s *TunnelStore) AddTunnel(tunnel StoredTunnel) error {
	if err := tunnel.normalize(); err != nil {
		return err
	}
	if tunnel.ClientID == "" || tunnel.Binding != TunnelBindingClientID {
		return fmt.Errorf("new tunnel must be bound with a stable client_id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.tunnels {
		if existing.matchesClient(tunnel.ClientID, tunnel.Name) {
			return fmt.Errorf("tunnel %q already exists (client_id: %s)", tunnel.Name, tunnel.ClientID)
		}
	}

	previous := cloneStoredTunnels(s.tunnels)
	s.tunnels = append(s.tunnels, tunnel)
	if err := s.save(); err != nil {
		s.tunnels = previous
		return err
	}
	return nil
}

// RemoveTunnel deletes a tunnel configuration and persists the change.
func (s *TunnelStore) RemoveTunnel(clientID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, tunnel := range s.tunnels {
		if tunnel.matchesIdentifier(clientID, name) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
	}

	previous := cloneStoredTunnels(s.tunnels)
	s.tunnels = append(s.tunnels[:idx], s.tunnels[idx+1:]...)
	if err := s.save(); err != nil {
		s.tunnels = previous
		return err
	}
	return nil
}

// UpdateStates directly updates both state fields and persists the change.
func (s *TunnelStore) UpdateStates(clientID, name, desiredState, runtimeState, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, tunnel := range s.tunnels {
		if tunnel.matchesIdentifier(clientID, name) {
			previous := s.tunnels[i]
			setStoredTunnelStates(&s.tunnels[i], desiredState, runtimeState, errMsg)
			if err := s.save(); err != nil {
				s.tunnels[i] = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
}

// UpdateTunnel updates the mutable tunnel configuration (local_ip, local_port, remote_port, domain) and persists it.
func (s *TunnelStore) UpdateTunnel(clientID, name string, localIP string, localPort, remotePort int, domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, tunnel := range s.tunnels {
		if tunnel.matchesIdentifier(clientID, name) {
			previous := s.tunnels[i]
			s.tunnels[i].LocalIP = localIP
			s.tunnels[i].LocalPort = localPort
			s.tunnels[i].RemotePort = remotePort
			s.tunnels[i].Domain = domain
			if err := s.save(); err != nil {
				s.tunnels[i] = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
}

// UpdateHostname updates the display hostname for a given Client.
func (s *TunnelStore) UpdateHostname(clientID, hostname string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	previous := cloneStoredTunnels(s.tunnels)
	for i, tunnel := range s.tunnels {
		if tunnel.Binding == TunnelBindingClientID && tunnel.ClientID == clientID && tunnel.Hostname != hostname {
			s.tunnels[i].Hostname = hostname
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := s.save(); err != nil {
		s.tunnels = previous
		return err
	}
	return nil
}

// GetTunnelsByClientID returns all tunnel configurations for the given stable client_id.
func (s *TunnelStore) GetTunnelsByClientID(clientID string) []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredTunnel, 0, len(s.tunnels))
	for _, tunnel := range s.tunnels {
		if tunnel.Binding == TunnelBindingClientID && tunnel.ClientID == clientID {
			result = append(result, tunnel)
		}
	}
	return result
}

// GetTunnelsByHostname returns all tunnels matching the given hostname (for display/query purposes).
func (s *TunnelStore) GetTunnelsByHostname(hostname string) []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredTunnel, 0, len(s.tunnels))
	for _, tunnel := range s.tunnels {
		if tunnel.Hostname == hostname {
			result = append(result, tunnel)
		}
	}
	return result
}

// GetTunnel looks up a single tunnel by stable client_id and name.
func (s *TunnelStore) GetTunnel(clientID, name string) (StoredTunnel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, tunnel := range s.tunnels {
		if tunnel.matchesIdentifier(clientID, name) {
			return tunnel, true
		}
	}
	return StoredTunnel{}, false
}

// GetAllTunnels returns all tunnel configurations.
func (s *TunnelStore) GetAllTunnels() []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredTunnel, len(s.tunnels))
	copy(result, s.tunnels)
	return result
}
