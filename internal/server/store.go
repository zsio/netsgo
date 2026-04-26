package server

import (
	"database/sql"
	"fmt"
	"sync"

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

// TunnelStore is a SQLite-backed persistent store for tunnel configurations.
type TunnelStore struct {
	path      string
	db        *sql.DB
	closeDB   bool
	mu        sync.RWMutex
	closeOnce sync.Once
	closeErr  error

	// For testing only: inject a save failure before the next SQL mutation.
	failSaveErr   error
	failSaveCount int
}

// NewTunnelStore creates or opens a tunnel store.
func NewTunnelStore(path string) (*TunnelStore, error) {
	db, err := openServerDB(path)
	if err != nil {
		return nil, err
	}
	store, err := newTunnelStoreWithDB(path, db, true)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func newTunnelStoreWithDB(path string, db *sql.DB, closeDB bool) (*TunnelStore, error) {
	store := &TunnelStore{path: path, db: db, closeDB: closeDB}
	if err := store.validateLoadedState(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *TunnelStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if !s.closeDB {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

func (s *TunnelStore) maybeFailSave() error {
	if s.failSaveErr != nil && s.failSaveCount > 0 {
		err := s.failSaveErr
		s.failSaveCount--
		if s.failSaveCount == 0 {
			s.failSaveErr = nil
		}
		return err
	}
	return nil
}

const tunnelSelectColumns = `client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, desired_state, runtime_state, error, hostname, binding`

func scanStoredTunnel(row dbScanner) (StoredTunnel, error) {
	var tunnel StoredTunnel
	err := row.Scan(
		&tunnel.ClientID,
		&tunnel.Name,
		&tunnel.Type,
		&tunnel.LocalIP,
		&tunnel.LocalPort,
		&tunnel.RemotePort,
		&tunnel.Domain,
		&tunnel.IngressBPS,
		&tunnel.EgressBPS,
		&tunnel.DesiredState,
		&tunnel.RuntimeState,
		&tunnel.Error,
		&tunnel.Hostname,
		&tunnel.Binding,
	)
	if err != nil {
		return StoredTunnel{}, err
	}
	if err := tunnel.normalize(); err != nil {
		return StoredTunnel{}, err
	}
	return tunnel, nil
}

func scanStoredTunnelRows(rows *sql.Rows) ([]StoredTunnel, error) {
	defer rows.Close()

	tunnels := []StoredTunnel{}
	for rows.Next() {
		tunnel, err := scanStoredTunnel(rows)
		if err != nil {
			return nil, err
		}
		tunnels = append(tunnels, tunnel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tunnels, nil
}

func (s *TunnelStore) validateLoadedState() error {
	rows, err := s.db.Query(`SELECT ` + tunnelSelectColumns + ` FROM tunnels ORDER BY client_id, name`)
	if err != nil {
		return err
	}
	_, err = scanStoredTunnelRows(rows)
	if err != nil {
		return fmt.Errorf("failed to load tunnel config: %w", err)
	}
	return nil
}

func (s *TunnelStore) tunnelExists(clientID, name string) (bool, error) {
	var existing string
	err := s.db.QueryRow(`SELECT name FROM tunnels WHERE client_id = ? AND name = ?`, clientID, name).Scan(&existing)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
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

	var existing string
	err := s.db.QueryRow(`SELECT name FROM tunnels WHERE client_id = ? AND name = ?`, tunnel.ClientID, tunnel.Name).Scan(&existing)
	if err == nil {
		return fmt.Errorf("tunnel %q already exists (client_id: %s)", tunnel.Name, tunnel.ClientID)
	}
	if err != sql.ErrNoRows {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}

	_, err = s.db.Exec(`INSERT INTO tunnels (client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, desired_state, runtime_state, error, hostname, binding)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tunnel.ClientID,
		tunnel.Name,
		tunnel.Type,
		tunnel.LocalIP,
		tunnel.LocalPort,
		tunnel.RemotePort,
		tunnel.Domain,
		tunnel.IngressBPS,
		tunnel.EgressBPS,
		tunnel.DesiredState,
		tunnel.RuntimeState,
		tunnel.Error,
		tunnel.Hostname,
		tunnel.Binding,
	)
	if err != nil {
		return err
	}
	return nil
}

// RemoveTunnel deletes a tunnel configuration and persists the change.
func (s *TunnelStore) RemoveTunnel(clientID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := s.tunnelExists(clientID, name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}

	result, err := s.db.Exec(`DELETE FROM tunnels WHERE client_id = ? AND name = ?`, clientID, name)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
	}
	return nil
}

// UpdateStates directly updates both state fields and persists the change.
func (s *TunnelStore) UpdateStates(clientID, name, desiredState, runtimeState, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := s.tunnelExists(clientID, name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
	}
	normalized := StoredTunnel{ClientID: clientID, Binding: TunnelBindingClientID}
	setStoredTunnelStates(&normalized, desiredState, runtimeState, errMsg)
	if err := s.maybeFailSave(); err != nil {
		return err
	}

	result, err := s.db.Exec(`UPDATE tunnels SET desired_state = ?, runtime_state = ?, error = ? WHERE client_id = ? AND name = ?`,
		normalized.DesiredState, normalized.RuntimeState, normalized.Error, clientID, name)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
	}
	return nil
}

// UpdateTunnel updates the mutable tunnel configuration and persists it.
func (s *TunnelStore) UpdateTunnel(clientID, name string, localIP string, localPort, remotePort int, domain string, ingressBPS, egressBPS int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := s.tunnelExists(clientID, name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}

	result, err := s.db.Exec(`UPDATE tunnels SET local_ip = ?, local_port = ?, remote_port = ?, domain = ?, ingress_bps = ?, egress_bps = ? WHERE client_id = ? AND name = ?`,
		localIP, localPort, remotePort, domain, ingressBPS, egressBPS, clientID, name)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("tunnel %q does not exist (client_id: %s)", name, clientID)
	}
	return nil
}

// UpdateHostname updates the display hostname for a given Client.
func (s *TunnelStore) UpdateHostname(clientID, hostname string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var changed int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tunnels WHERE client_id = ? AND hostname <> ?`, clientID, hostname).Scan(&changed); err != nil {
		return err
	}
	if changed == 0 {
		return nil
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}

	_, err := s.db.Exec(`UPDATE tunnels SET hostname = ? WHERE client_id = ? AND hostname <> ?`, hostname, clientID, hostname)
	if err != nil {
		return err
	}
	return nil
}

// GetTunnelsByClientID returns all tunnel configurations for the given stable client_id.
func (s *TunnelStore) GetTunnelsByClientID(clientID string) []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? ORDER BY name`, clientID)
	if err != nil {
		return nil
	}
	tunnels, err := scanStoredTunnelRows(rows)
	if err != nil {
		return nil
	}
	return tunnels
}

// GetTunnelsByHostname returns all tunnels matching the given hostname (for display/query purposes).
func (s *TunnelStore) GetTunnelsByHostname(hostname string) []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE hostname = ? ORDER BY client_id, name`, hostname)
	if err != nil {
		return nil
	}
	tunnels, err := scanStoredTunnelRows(rows)
	if err != nil {
		return nil
	}
	return tunnels
}

// GetTunnel looks up a single tunnel by stable client_id and name.
func (s *TunnelStore) GetTunnel(clientID, name string) (StoredTunnel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tunnel, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND name = ?`, clientID, name))
	if err == sql.ErrNoRows {
		return StoredTunnel{}, false
	}
	if err != nil {
		return StoredTunnel{}, false
	}
	return tunnel, true
}

// GetAllTunnels returns all tunnel configurations.
func (s *TunnelStore) GetAllTunnels() []StoredTunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT ` + tunnelSelectColumns + ` FROM tunnels ORDER BY client_id, name`)
	if err != nil {
		return nil
	}
	tunnels, err := scanStoredTunnelRows(rows)
	if err != nil {
		return nil
	}
	return tunnels
}
