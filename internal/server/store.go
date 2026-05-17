package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

const (
	TunnelBindingClientID = "client_id"
)

var ErrTunnelNotFound = errors.New("tunnel not found")

const (
	TunnelTopologyServerExpose       = "server_expose"
	TunnelTopologyClientToClient     = "client_to_client"
	TunnelIngressTypeTCPListen       = "tcp_listen"
	TunnelIngressTypeUDPListen       = "udp_listen"
	TunnelIngressTypeHTTPHost        = "http_host"
	TunnelTargetTypeTCPService       = "tcp_service"
	TunnelTargetTypeUDPService       = "udp_service"
	TunnelTransportServerRelayOnly   = "server_relay_only"
	TunnelTransportDirectPreferred   = "direct_preferred"
	TunnelTransportDirectOnly        = "direct_only"
	TunnelActualTransportUnknown     = "unknown"
	TunnelActualTransportServerRelay = "server_relay"
	TunnelP2PStateIdle               = "idle"
)

// StoredTunnel is a tunnel configuration persisted to storage.
type EndpointSpec struct {
	Location string          `json:"location"`
	ClientID string          `json:"client_id,omitempty"`
	Type     string          `json:"type"`
	Config   json.RawMessage `json:"config"`
}

type P2PState struct {
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type StoredTunnel struct {
	protocol.ProxyNewRequest
	DesiredState    string       `json:"desired_state,omitempty"` // User's desired state
	RuntimeState    string       `json:"runtime_state,omitempty"` // Actual runtime state
	Error           string       `json:"error,omitempty"`         // Reason when in error state
	ClientID        string       `json:"client_id,omitempty"`     // Owning stable Client ID
	Hostname        string       `json:"hostname,omitempty"`      // Current hostname (for display)
	Binding         string       `json:"binding,omitempty"`       // Only client_id is allowed
	CreatedAt       time.Time    `json:"created_at,omitempty"`    // Creation time
	Revision        int64        `json:"revision,omitempty"`
	Topology        string       `json:"topology,omitempty"`
	OwnerClientID   string       `json:"owner_client_id,omitempty"`
	Ingress         EndpointSpec `json:"ingress,omitempty"`
	Target          EndpointSpec `json:"target,omitempty"`
	TransportPolicy string       `json:"transport_policy,omitempty"`
	ActualTransport string       `json:"actual_transport,omitempty"`
	P2P             P2PState     `json:"p2p,omitempty"`
	CreatedByUserID string       `json:"created_by_user_id,omitempty"`
	UpdatedAt       time.Time    `json:"updated_at,omitempty"`
}

func (t *StoredTunnel) normalize() error {
	if t.ID == "" {
		return fmt.Errorf("tunnel %q is missing a stable id", t.Name)
	}
	if t.Binding == "" {
		t.Binding = TunnelBindingClientID
	}
	if t.Binding != TunnelBindingClientID {
		return fmt.Errorf("tunnel %q must use %q binding", t.Name, TunnelBindingClientID)
	}
	if t.ClientID == "" {
		return fmt.Errorf("tunnel %q is missing a stable client_id", t.Name)
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	if t.Revision == 0 {
		t.Revision = 1
	}
	if err := validateTunnelStates(t.DesiredState, t.RuntimeState, t.Error); err != nil {
		return err
	}
	t.DesiredState = canonicalDesiredState(t.DesiredState)
	t.Error = tunnelErrorForRuntimeState(t.RuntimeState, t.Error)
	normalizeUnifiedTunnelSpec(t)
	return validateUnifiedTunnelSpec(*t)
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

// NewTunnelStore creates or opens a standalone tunnel store that owns its DB.
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

// newTunnelStoreWithDB creates a tunnel store over an existing DB handle.
// When closeDB is false the caller retains DB ownership.
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

const tunnelSelectColumns = `id, client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, created_at, desired_state, runtime_state, error, hostname, binding, revision, topology, owner_client_id, ingress_location, ingress_client_id, ingress_type, ingress_config, target_location, target_client_id, target_type, target_config, transport_policy, actual_transport, p2p_state, p2p_error, p2p_session_id, created_by_user_id, updated_at`

func scanStoredTunnel(row dbScanner) (StoredTunnel, error) {
	var tunnel StoredTunnel
	var createdAt, updatedAt string
	var ingressLocation, ingressClientID, ingressType, ingressConfig string
	var targetLocation, targetClientID, targetType, targetConfig string
	var p2pState, p2pError, p2pSessionID string
	err := row.Scan(
		&tunnel.ID,
		&tunnel.ClientID,
		&tunnel.Name,
		&tunnel.Type,
		&tunnel.LocalIP,
		&tunnel.LocalPort,
		&tunnel.RemotePort,
		&tunnel.Domain,
		&tunnel.IngressBPS,
		&tunnel.EgressBPS,
		&createdAt,
		&tunnel.DesiredState,
		&tunnel.RuntimeState,
		&tunnel.Error,
		&tunnel.Hostname,
		&tunnel.Binding,
		&tunnel.Revision,
		&tunnel.Topology,
		&tunnel.OwnerClientID,
		&ingressLocation,
		&ingressClientID,
		&ingressType,
		&ingressConfig,
		&targetLocation,
		&targetClientID,
		&targetType,
		&targetConfig,
		&tunnel.TransportPolicy,
		&tunnel.ActualTransport,
		&p2pState,
		&p2pError,
		&p2pSessionID,
		&tunnel.CreatedByUserID,
		&updatedAt,
	)
	if err != nil {
		return StoredTunnel{}, err
	}
	if createdAt != "" {
		parsed, err := parseTime(createdAt)
		if err != nil {
			return StoredTunnel{}, err
		}
		tunnel.CreatedAt = parsed
	}
	if updatedAt != "" {
		parsed, err := parseTime(updatedAt)
		if err != nil {
			return StoredTunnel{}, err
		}
		tunnel.UpdatedAt = parsed
	}
	tunnel.Ingress = EndpointSpec{Location: ingressLocation, ClientID: ingressClientID, Type: ingressType, Config: json.RawMessage(ingressConfig)}
	tunnel.Target = EndpointSpec{Location: targetLocation, ClientID: targetClientID, Type: targetType, Config: json.RawMessage(targetConfig)}
	tunnel.P2P = P2PState{State: p2pState, Error: p2pError, SessionID: p2pSessionID}
	if err := tunnel.normalize(); err != nil {
		return StoredTunnel{}, err
	}
	return tunnel, nil
}

func scanStoredTunnelRows(rows *sql.Rows) ([]StoredTunnel, error) {
	defer func() { _ = rows.Close() }()

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
	rows, err := s.db.Query(`SELECT ` + tunnelSelectColumns + ` FROM tunnels ORDER BY client_id, created_at DESC, name`)
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

func (s *TunnelStore) tunnelIDExists(clientID, id string) (bool, error) {
	var existing string
	err := s.db.QueryRow(`SELECT id FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id).Scan(&existing)
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
	if tunnel.ID == "" {
		tunnel.ID = generateUUID()
	}
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

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if err := insertTunnelTx(tx, tunnel); err != nil {
		return err
	}
	if err := replaceTunnelResourceLocksTx(tx, tunnel); err != nil {
		return err
	}
	return commitTx(tx, &committed)
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

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`DELETE FROM tunnel_resource_locks WHERE tunnel_id IN (SELECT id FROM tunnels WHERE client_id = ? AND name = ?)`, clientID, name)
	_ = result
	if err != nil {
		return err
	}
	result, err = tx.Exec(`DELETE FROM tunnels WHERE client_id = ? AND name = ?`, clientID, name)
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
	return commitTx(tx, &committed)
}

func (s *TunnelStore) RemoveTunnelByID(clientID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := s.tunnelIDExists(clientID, id)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("tunnel id %q does not exist (client_id: %s)", id, clientID)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`DELETE FROM tunnel_resource_locks WHERE tunnel_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("tunnel id %q does not exist (client_id: %s)", id, clientID)
	}
	return commitTx(tx, &committed)
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

	storageRuntimeState := storageRuntimeStateFromProtocol(normalized.RuntimeState)
	actualTransport := TunnelActualTransportUnknown
	if storageRuntimeState == "active" {
		actualTransport = TunnelActualTransportServerRelay
	}
	result, err := s.db.Exec(`UPDATE tunnels SET desired_state = ?, runtime_state = ?, error = ?, actual_transport = ?, updated_at = ? WHERE client_id = ? AND name = ?`,
		normalized.DesiredState, storageRuntimeState, normalized.Error, actualTransport, formatTime(time.Now().UTC()), clientID, name)
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
	if err := replaceTunnelResourceLocksTx(tx, stored); err != nil {
		return err
	}
	return commitTx(tx, &committed)
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

	stored, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND name = ?`, clientID, name))
	if err != nil {
		return err
	}
	stored.LocalIP = localIP
	stored.LocalPort = localPort
	stored.RemotePort = remotePort
	stored.Domain = domain
	stored.IngressBPS = ingressBPS
	stored.EgressBPS = egressBPS
	stored.UpdatedAt = time.Now().UTC()
	if err := stored.normalize(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	result, err := tx.Exec(`UPDATE tunnels SET local_ip = ?, local_port = ?, remote_port = ?, domain = ?, ingress_bps = ?, egress_bps = ?, ingress_config = ?, ingress_bind_ip = ?, ingress_port = ?, ingress_domain = ?, target_config = ?, target_host = ?, target_port = ?, target_resource_key = ?, updated_at = ? WHERE client_id = ? AND name = ?`,
		stored.LocalIP, stored.LocalPort, stored.RemotePort, stored.Domain, stored.IngressBPS, stored.EgressBPS, string(stored.Ingress.Config), tunnelIngressBindIP(stored), tunnelIngressPort(stored), tunnelIngressDomain(stored), string(stored.Target.Config), stored.LocalIP, stored.LocalPort, tunnelTargetResourceKey(stored), formatTime(stored.UpdatedAt), clientID, name)
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

// UpdateTunnelByID updates a tunnel by stable id and persists mutable configuration, including display name.
func (s *TunnelStore) UpdateTunnelByID(clientID, id, name string, localIP string, localPort, remotePort int, domain string, ingressBPS, egressBPS int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := s.tunnelIDExists(clientID, id)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("tunnel id %q does not exist (client_id: %s)", id, clientID)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}

	stored, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id))
	if err != nil {
		return err
	}
	stored.Name = name
	stored.LocalIP = localIP
	stored.LocalPort = localPort
	stored.RemotePort = remotePort
	stored.Domain = domain
	stored.IngressBPS = ingressBPS
	stored.EgressBPS = egressBPS
	stored.UpdatedAt = time.Now().UTC()
	if err := stored.normalize(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	result, err := tx.Exec(`UPDATE tunnels SET name = ?, local_ip = ?, local_port = ?, remote_port = ?, domain = ?, ingress_bps = ?, egress_bps = ?, ingress_config = ?, ingress_bind_ip = ?, ingress_port = ?, ingress_domain = ?, target_config = ?, target_host = ?, target_port = ?, target_resource_key = ?, updated_at = ? WHERE client_id = ? AND id = ?`,
		stored.Name, stored.LocalIP, stored.LocalPort, stored.RemotePort, stored.Domain, stored.IngressBPS, stored.EgressBPS, string(stored.Ingress.Config), tunnelIngressBindIP(stored), tunnelIngressPort(stored), tunnelIngressDomain(stored), string(stored.Target.Config), stored.LocalIP, stored.LocalPort, tunnelTargetResourceKey(stored), formatTime(stored.UpdatedAt), clientID, id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("tunnel id %q does not exist (client_id: %s)", id, clientID)
	}
	if err := replaceTunnelResourceLocksTx(tx, stored); err != nil {
		return err
	}
	return commitTx(tx, &committed)
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

	_, err := s.db.Exec(`UPDATE tunnels SET hostname = ?, updated_at = ? WHERE client_id = ? AND hostname <> ?`, hostname, formatTime(time.Now().UTC()), clientID, hostname)
	if err != nil {
		return err
	}
	return nil
}

// GetTunnelsByClientID returns all tunnel configurations for the given stable client_id.
func (s *TunnelStore) GetTunnelsByClientID(clientID string) ([]StoredTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? ORDER BY created_at DESC, name`, clientID)
	if err != nil {
		return nil, err
	}
	tunnels, err := scanStoredTunnelRows(rows)
	if err != nil {
		return nil, err
	}
	return tunnels, nil
}

func (s *TunnelStore) DeleteTunnelsByClientID(clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.maybeFailSave(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if _, err := tx.Exec(`DELETE FROM tunnel_resource_locks WHERE tunnel_id IN (SELECT id FROM tunnels WHERE client_id = ?)`, clientID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tunnels WHERE client_id = ?`, clientID); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// GetTunnelsByHostname returns all tunnels matching the given hostname (for display/query purposes).
func (s *TunnelStore) GetTunnelsByHostname(hostname string) ([]StoredTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE hostname = ? ORDER BY client_id, created_at DESC, name`, hostname)
	if err != nil {
		return nil, err
	}
	tunnels, err := scanStoredTunnelRows(rows)
	if err != nil {
		return nil, err
	}
	return tunnels, nil
}

// GetTunnelE looks up a single tunnel by stable client_id and name and
// distinguishes not found from storage failure.
func (s *TunnelStore) GetTunnelE(clientID, name string) (StoredTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tunnel, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND name = ?`, clientID, name))
	if err == sql.ErrNoRows {
		return StoredTunnel{}, ErrTunnelNotFound
	}
	if err != nil {
		return StoredTunnel{}, err
	}
	return tunnel, nil
}

// GetTunnelByIDE looks up a single tunnel by stable id and client id.
func (s *TunnelStore) GetTunnelByIDE(clientID, id string) (StoredTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tunnel, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id))
	if err == sql.ErrNoRows {
		return StoredTunnel{}, ErrTunnelNotFound
	}
	if err != nil {
		return StoredTunnel{}, err
	}
	return tunnel, nil
}

// GetTunnel looks up a single tunnel by stable client_id and name.
//
// This is a best-effort compatibility wrapper for non-authoritative display or
// legacy paths. Mutation, routing, restore, and event correctness paths must use
// GetTunnelE so storage failures are not collapsed into "not found".
func (s *TunnelStore) GetTunnel(clientID, name string) (StoredTunnel, bool) {
	tunnel, err := s.GetTunnelE(clientID, name)
	if err != nil {
		return StoredTunnel{}, false
	}
	return tunnel, true
}

// GetAllTunnels returns all tunnel configurations.
func (s *TunnelStore) GetAllTunnels() ([]StoredTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT ` + tunnelSelectColumns + ` FROM tunnels ORDER BY client_id, created_at DESC, name`)
	if err != nil {
		return nil, err
	}
	tunnels, err := scanStoredTunnelRows(rows)
	if err != nil {
		return nil, err
	}
	return tunnels, nil
}
