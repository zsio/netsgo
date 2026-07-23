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
var ErrTunnelRevisionConflict = errors.New("tunnel revision conflict")
var ErrTunnelOwnerNameConflict = errors.New("target client already has a tunnel with this name")
var ErrTunnelMigrationPending = errors.New("pending tunnel cannot be migrated")
var ErrTunnelTargetClientNotFound = errors.New("target client is not registered")

const (
	TunnelTopologyServerExpose           = protocol.TunnelTopologyServerExpose
	TunnelTopologyClientToClient         = protocol.TunnelTopologyClientToClient
	TunnelIngressTypeTCPListen           = protocol.IngressTypeTCPListen
	TunnelIngressTypeUDPListen           = protocol.IngressTypeUDPListen
	TunnelIngressTypeHTTPHost            = protocol.IngressTypeHTTPHost
	TunnelIngressTypeSOCKS5Listen        = protocol.IngressTypeSOCKS5Listen
	TunnelTargetTypeTCPService           = protocol.TargetTypeTCPService
	TunnelTargetTypeUDPService           = protocol.TargetTypeUDPService
	TunnelTargetTypeSOCKS5ConnectHandler = protocol.TargetTypeSOCKS5ConnectHandler
	TunnelTransportServerRelayOnly       = protocol.TransportPolicyServerRelayOnly
	TunnelTransportDirectPreferred       = protocol.TransportPolicyDirectPreferred
	TunnelTransportDirectOnly            = protocol.TransportPolicyDirectOnly
	TunnelActualTransportUnknown         = protocol.ActualTransportUnknown
	TunnelActualTransportServerRelay     = protocol.ActualTransportServerRelay
	TunnelP2PStateIdle                   = protocol.P2PStateIdle
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
	path          string
	db            *sql.DB
	closeDB       bool
	trafficStore  *TrafficStore
	activityStore *ActivityStore
	mu            sync.RWMutex
	closeOnce     sync.Once
	closeErr      error

	// For testing only: inject a save failure before the next SQL mutation.
	failSaveErr   error
	failSaveCount int
}

func (s *TunnelStore) attachTrafficStore(trafficStore *TrafficStore, accumulators ...*trafficAccumulator) {
	s.mu.Lock()
	s.trafficStore = trafficStore
	s.mu.Unlock()
	if trafficStore != nil && len(accumulators) > 0 {
		trafficStore.attachAccumulator(accumulators[0])
	}
}

type P2PProjectionMode string

const (
	P2PProjectionGathering P2PProjectionMode = "gathering"
	P2PProjectionReady     P2PProjectionMode = "ready"
	P2PProjectionFailed    P2PProjectionMode = "failed"
	P2PProjectionClosed    P2PProjectionMode = "closed"
)

type P2PProjectionTransition struct {
	Mode      P2PProjectionMode
	SessionID string
}

type P2PProjectionChange struct {
	Before StoredTunnel
	After  StoredTunnel
}

type P2PProjectionResult struct {
	Changes []P2PProjectionChange
	Stale   []p2pGrantSnapshot
}

func (s *TunnelStore) ApplyP2PLifecycle(grants []p2pGrantSnapshot, expectedSessionID string, transition P2PProjectionTransition) (P2PProjectionResult, error) {
	if s == nil || s.db == nil || len(grants) == 0 {
		return P2PProjectionResult{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.maybeFailSave(); err != nil {
		return P2PProjectionResult{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return P2PProjectionResult{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	result := P2PProjectionResult{Changes: make([]P2PProjectionChange, 0, len(grants))}
	for _, grant := range grants {
		before, err := scanStoredTunnel(tx.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE id = ? AND revision = ?`, grant.TunnelID, grant.Revision))
		if err == sql.ErrNoRows {
			result.Stale = append(result.Stale, grant)
			continue
		}
		if err != nil {
			return P2PProjectionResult{}, err
		}
		if expectedSessionID != "" && before.P2P.SessionID != expectedSessionID {
			result.Stale = append(result.Stale, grant)
			continue
		}
		state, message, sessionID, actualTransport := p2pProjectionValues(before, transition)
		if before.P2P.State == state && before.P2P.Error == message && before.P2P.SessionID == sessionID && before.ActualTransport == actualTransport {
			continue
		}
		where := `id = ? AND revision = ?`
		args := []any{state, message, sessionID, actualTransport, formatTime(time.Now().UTC()), grant.TunnelID, grant.Revision}
		if expectedSessionID != "" {
			where += ` AND p2p_session_id = ?`
			args = append(args, expectedSessionID)
		}
		update, err := tx.Exec(`UPDATE tunnels SET p2p_state = ?, p2p_error = ?, p2p_session_id = ?, actual_transport = ?, updated_at = ? WHERE `+where, args...)
		if err != nil {
			return P2PProjectionResult{}, err
		}
		rows, err := update.RowsAffected()
		if err != nil {
			return P2PProjectionResult{}, err
		}
		if rows == 0 {
			result.Stale = append(result.Stale, grant)
			continue
		}
		after := before
		after.P2P = P2PState{State: state, Error: message, SessionID: sessionID}
		after.ActualTransport = actualTransport
		result.Changes = append(result.Changes, P2PProjectionChange{Before: before, After: after})
	}
	if err := commitTx(tx, &committed); err != nil {
		return P2PProjectionResult{}, err
	}
	return result, nil
}

func p2pProjectionValues(stored StoredTunnel, transition P2PProjectionTransition) (state, message, sessionID, actualTransport string) {
	sessionID = transition.SessionID
	actualTransport = TunnelActualTransportUnknown
	switch transition.Mode {
	case P2PProjectionGathering:
		state = protocol.P2PStateGathering
		if stored.TransportPolicy == TunnelTransportDirectPreferred {
			actualTransport = TunnelActualTransportServerRelay
		}
	case P2PProjectionReady:
		state, actualTransport = protocol.P2PStateConnected, protocol.ActualTransportPeerDirect
	case P2PProjectionFailed:
		if stored.TransportPolicy == TunnelTransportDirectPreferred {
			state, actualTransport = protocol.P2PStateFallback, TunnelActualTransportServerRelay
		} else {
			state = protocol.P2PStateFailed
		}
	case P2PProjectionClosed:
		state, sessionID = protocol.P2PStateClosed, ""
		if stored.TransportPolicy == TunnelTransportDirectPreferred && stored.DesiredState != protocol.ProxyDesiredStateStopped {
			actualTransport = TunnelActualTransportServerRelay
		}
	default:
		state = stored.P2P.State
		message = stored.P2P.Error
		actualTransport = stored.ActualTransport
	}
	return state, message, sessionID, actualTransport
}

func (s *TunnelStore) UpdateP2PStateIfCurrent(tunnelID string, revision int64, state, message, sessionID, actualTransport string) (bool, error) {
	if s == nil || s.db == nil || tunnelID == "" || revision <= 0 {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`UPDATE tunnels SET p2p_state = ?, p2p_error = ?, p2p_session_id = ?, actual_transport = ?, updated_at = ? WHERE id = ? AND revision = ?`, state, message, sessionID, actualTransport, formatTime(time.Now().UTC()), tunnelID, revision)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
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
	store.activityStore = newActivityStoreWithDB(path, db, false)
	return store, nil
}

// newTunnelStoreWithDB creates a tunnel store over an existing DB handle.
// When closeDB is false the caller retains DB ownership.
func newTunnelStoreWithDB(path string, db *sql.DB, closeDB bool) (*TunnelStore, error) {
	store := &TunnelStore{path: path, db: db, closeDB: closeDB}
	if err := store.validateLoadedState(); err != nil {
		return nil, err
	}
	if err := store.backfillExplicitAllowAllSourceCIDRs(); err != nil {
		return nil, err
	}
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

const tunnelSelectColumns = `id, client_id, name, type, local_ip, local_port, remote_port, domain, ingress_bps, egress_bps, total_bps, created_at, desired_state, runtime_state, error, hostname, binding, revision, topology, owner_client_id, ingress_location, ingress_client_id, ingress_type, ingress_config, target_location, target_client_id, target_type, target_config, transport_policy, actual_transport, p2p_state, p2p_error, p2p_session_id, created_by_user_id, updated_at`

func prefixedTunnelSelectColumns(prefix string) string {
	columns := strings.Split(tunnelSelectColumns, ", ")
	for i, column := range columns {
		columns[i] = prefix + column
	}
	return strings.Join(columns, ", ")
}

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
		&tunnel.TotalBPS,
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
	tunnel.RuntimeState = protocolRuntimeStateFromStorage(tunnel.RuntimeState)
	tunnel.Ingress = EndpointSpec{Location: ingressLocation, ClientID: ingressClientID, Type: ingressType, Config: json.RawMessage(ingressConfig)}
	tunnel.Target = EndpointSpec{Location: targetLocation, ClientID: targetClientID, Type: targetType, Config: json.RawMessage(targetConfig)}
	tunnel.P2P = P2PState{State: p2pState, Error: p2pError, SessionID: p2pSessionID}
	if err := tunnel.normalize(); err != nil {
		return StoredTunnel{}, err
	}
	tunnel.BindIP = tunnelIngressBindIP(tunnel)
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

func (s *TunnelStore) backfillExplicitAllowAllSourceCIDRs() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT id, ingress_type, ingress_config FROM tunnels ORDER BY id`)
	if err != nil {
		return err
	}
	type update struct {
		id     string
		config string
	}
	var updates []update
	for rows.Next() {
		var id, ingressType, rawConfig string
		if err := rows.Scan(&id, &ingressType, &rawConfig); err != nil {
			_ = rows.Close()
			return err
		}
		nextConfig, changed, err := backfillIngressSourceCIDRsConfig(ingressType, json.RawMessage(rawConfig))
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("backfill source CIDRs for tunnel %s: %w", id, err)
		}
		if changed {
			updates = append(updates, update{id: id, config: string(nextConfig)})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	for _, item := range updates {
		if _, err := tx.Exec(`UPDATE tunnels SET ingress_config = ? WHERE id = ?`, item.config, item.id); err != nil {
			return err
		}
	}
	return commitTx(tx, &committed)
}

func backfillIngressSourceCIDRsConfig(ingressType string, raw json.RawMessage) (json.RawMessage, bool, error) {
	switch ingressType {
	case TunnelIngressTypeTCPListen, TunnelIngressTypeUDPListen:
		var cfg tcpListenConfigAPI
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, false, err
		}
		if cfg.AllowedSourceCIDRs != nil {
			return nil, false, nil
		}
		cfg.AllowedSourceCIDRs = allowAllSourceCIDRs()
		return mustRawJSON(cfg), true, nil
	case TunnelIngressTypeSOCKS5Listen:
		var cfg protocol.SOCKS5ListenConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, false, err
		}
		if cfg.AllowedSourceCIDRs != nil {
			return nil, false, nil
		}
		cfg.AllowedSourceCIDRs = allowAllSourceCIDRs()
		return mustRawJSON(cfg), true, nil
	case TunnelIngressTypeHTTPHost:
		var cfg httpHostConfigAPI
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, false, err
		}
		changed := false
		if cfg.AllowedSourceCIDRs == nil {
			cfg.AllowedSourceCIDRs = allowAllSourceCIDRs()
			changed = true
		}
		if cfg.Auth.Type == "" {
			cfg.Auth.Type = protocol.HTTPAuthTypeNone
			changed = true
		}
		if !changed {
			return nil, false, nil
		}
		return mustRawJSON(cfg), true, nil
	default:
		return nil, false, nil
	}
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
func (s *TunnelStore) appendActivityTx(tx *sql.Tx, spec ActivityEventSpec) (int64, error) {
	if s.activityStore == nil {
		return 0, nil
	}
	return s.activityStore.appendTx(tx, spec)
}

func (s *TunnelStore) AddTunnelWithActivity(tunnel StoredTunnel, actor ActivityActor) (int64, error) {
	return s.addTunnel(tunnel, &actor)
}

func (s *TunnelStore) AddTunnel(tunnel StoredTunnel) error {
	_, err := s.addTunnel(tunnel, nil)
	return err
}

func (s *TunnelStore) addTunnel(tunnel StoredTunnel, actor *ActivityActor) (int64, error) {
	if tunnel.ID == "" {
		id, err := generateUUIDE()
		if err != nil {
			return 0, err
		}
		tunnel.ID = id
	}
	if err := tunnel.normalize(); err != nil {
		return 0, err
	}
	if tunnel.ClientID == "" || tunnel.Binding != TunnelBindingClientID {
		return 0, fmt.Errorf("new tunnel must be bound with a stable client_id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var existing string
	err := s.db.QueryRow(`SELECT name FROM tunnels WHERE client_id = ? AND name = ?`, tunnel.ClientID, tunnel.Name).Scan(&existing)
	if err == nil {
		return 0, fmt.Errorf("tunnel %q already exists (client_id: %s)", tunnel.Name, tunnel.ClientID)
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	if err := s.maybeFailSave(); err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if err := insertTunnelTx(tx, tunnel); err != nil {
		return 0, err
	}
	if err := replaceTunnelResourceLocksTx(tx, tunnel); err != nil {
		return 0, err
	}
	var activityID int64
	if actor != nil {
		activityID, err = s.appendActivityTx(tx, tunnelActivitySpec("created", tunnel, *actor))
		if err != nil {
			return 0, err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return activityID, nil
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
	return nil
}

func (s *TunnelStore) UpdateStatesIfCurrent(clientID, id string, revision int64, desiredState, runtimeState, errMsg string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized := StoredTunnel{ClientID: clientID, Binding: TunnelBindingClientID}
	setStoredTunnelStates(&normalized, desiredState, runtimeState, errMsg)
	if err := s.maybeFailSave(); err != nil {
		return false, err
	}

	storageRuntimeState := storageRuntimeStateFromProtocol(normalized.RuntimeState)
	actualTransport := TunnelActualTransportUnknown
	if storageRuntimeState == "active" {
		actualTransport = TunnelActualTransportServerRelay
	}
	result, err := s.db.Exec(
		`UPDATE tunnels SET desired_state = ?, runtime_state = ?, error = ?, actual_transport = ?, updated_at = ? WHERE client_id = ? AND id = ? AND revision = ? AND desired_state = ?`,
		normalized.DesiredState, storageRuntimeState, normalized.Error, actualTransport, formatTime(time.Now().UTC()), clientID, id, revision, normalized.DesiredState,
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

// TransitionRuntimeStateIfCurrent updates a stable tunnel only when its
// identity, desired state, and current runtime state still match. It is used
// for transitions such as pending -> exposed where a concurrent runtime error
// must win instead of being overwritten by a late activation.
func (s *TunnelStore) TransitionRuntimeStateIfCurrent(clientID, id string, revision int64, desiredState, expectedRuntimeState, runtimeState, errMsg string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized := StoredTunnel{ClientID: clientID, Binding: TunnelBindingClientID}
	setStoredTunnelStates(&normalized, desiredState, runtimeState, errMsg)
	if err := s.maybeFailSave(); err != nil {
		return false, err
	}

	storageRuntimeState := storageRuntimeStateFromProtocol(normalized.RuntimeState)
	actualTransport := TunnelActualTransportUnknown
	if storageRuntimeState == "active" {
		actualTransport = TunnelActualTransportServerRelay
	}
	result, err := s.db.Exec(
		`UPDATE tunnels SET desired_state = ?, runtime_state = ?, error = ?, actual_transport = ?, updated_at = ? WHERE client_id = ? AND id = ? AND revision = ? AND desired_state = ? AND runtime_state = ?`,
		normalized.DesiredState,
		storageRuntimeState,
		normalized.Error,
		actualTransport,
		formatTime(time.Now().UTC()),
		clientID,
		id,
		revision,
		normalized.DesiredState,
		storageRuntimeStateFromProtocol(expectedRuntimeState),
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
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
	if err := replaceTunnelResourceLocksTx(tx, stored); err != nil {
		return err
	}
	return commitTx(tx, &committed)
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

// UpdateTunnelByIDWithRevision updates a tunnel by stable id and increments the
// spec revision. The update is conditional on expectedRevision and returns
// ErrTunnelRevisionConflict on stale writes.
func (s *TunnelStore) UpdateTunnelByIDWithRevision(clientID, id string, expectedRevision int64, name string, localIP string, localPort, remotePort int, domain string, ingressBPS, egressBPS int64) (StoredTunnel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := s.tunnelIDExists(clientID, id)
	if err != nil {
		return StoredTunnel{}, err
	}
	if !exists {
		return StoredTunnel{}, fmt.Errorf("tunnel id %q does not exist (client_id: %s)", id, clientID)
	}
	if err := s.maybeFailSave(); err != nil {
		return StoredTunnel{}, err
	}

	stored, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id))
	if err != nil {
		return StoredTunnel{}, err
	}
	if expectedRevision <= 0 {
		return StoredTunnel{}, fmt.Errorf("expected revision is required")
	}
	if stored.Revision != expectedRevision {
		return StoredTunnel{}, ErrTunnelRevisionConflict
	}
	stored.Name = name
	stored.LocalIP = localIP
	stored.LocalPort = localPort
	stored.RemotePort = remotePort
	stored.Domain = domain
	stored.IngressBPS = ingressBPS
	stored.EgressBPS = egressBPS
	stored.Revision++
	stored.UpdatedAt = time.Now().UTC()
	if err := stored.normalize(); err != nil {
		return StoredTunnel{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return StoredTunnel{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `UPDATE tunnels SET name = ?, revision = ?, local_ip = ?, local_port = ?, remote_port = ?, domain = ?, ingress_bps = ?, egress_bps = ?, ingress_config = ?, ingress_bind_ip = ?, ingress_port = ?, ingress_domain = ?, target_config = ?, target_host = ?, target_port = ?, target_resource_key = ?, updated_at = ? WHERE client_id = ? AND id = ?`
	args := []any{stored.Name, stored.Revision, stored.LocalIP, stored.LocalPort, stored.RemotePort, stored.Domain, stored.IngressBPS, stored.EgressBPS, string(stored.Ingress.Config), tunnelIngressBindIP(stored), tunnelIngressPort(stored), tunnelIngressDomain(stored), string(stored.Target.Config), stored.LocalIP, stored.LocalPort, tunnelTargetResourceKey(stored), formatTime(stored.UpdatedAt), clientID, id}
	query += ` AND revision = ?`
	args = append(args, expectedRevision)
	result, err := tx.Exec(query, args...)
	if err != nil {
		return StoredTunnel{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return StoredTunnel{}, err
	}
	if rowsAffected == 0 {
		return StoredTunnel{}, ErrTunnelRevisionConflict
	}
	if err := replaceTunnelResourceLocksTx(tx, stored); err != nil {
		return StoredTunnel{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return StoredTunnel{}, err
	}
	return stored, nil
}

// ReplaceTunnelByID replaces a unified tunnel configuration by stable id and
// expected revision. It preserves the stable id and enforces resource locks.
func (s *TunnelStore) ReplaceTunnelByIDWithActivity(clientID, id string, expectedRevision int64, replacement StoredTunnel, actor ActivityActor) (int64, error) {
	return s.replaceTunnelByID(clientID, id, expectedRevision, replacement, &actor)
}

func (s *TunnelStore) ReplaceTunnelByID(clientID, id string, expectedRevision int64, replacement StoredTunnel) error {
	_, err := s.replaceTunnelByID(clientID, id, expectedRevision, replacement, nil)
	return err
}

func (s *TunnelStore) replaceTunnelByID(clientID, id string, expectedRevision int64, replacement StoredTunnel, actor *ActivityActor) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if expectedRevision <= 0 {
		return 0, fmt.Errorf("expected revision is required")
	}
	existing, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id))
	if err == sql.ErrNoRows {
		return 0, ErrTunnelNotFound
	}
	if err != nil {
		return 0, err
	}
	if existing.Revision != expectedRevision {
		return 0, ErrTunnelRevisionConflict
	}
	replacement.ID = id
	if replacement.ClientID == "" {
		replacement.ClientID = clientID
	}
	if replacement.ClientID != clientID {
		return 0, fmt.Errorf("replacement client_id cannot change")
	}
	if replacement.Revision != expectedRevision+1 {
		replacement.Revision = expectedRevision + 1
	}
	if replacement.CreatedAt.IsZero() {
		replacement.CreatedAt = existing.CreatedAt
	}
	if replacement.UpdatedAt.IsZero() {
		replacement.UpdatedAt = time.Now().UTC()
	}
	if err := replacement.normalize(); err != nil {
		return 0, err
	}
	if err := s.maybeFailSave(); err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE tunnels SET
		name = ?, type = ?, local_ip = ?, local_port = ?, remote_port = ?, domain = ?,
		revision = ?, topology = ?, owner_client_id = ?,
		ingress_location = ?, ingress_client_id = ?, ingress_type = ?, ingress_config = ?, ingress_bind_ip = ?, ingress_port = ?, ingress_domain = ?,
		target_location = ?, target_client_id = ?, target_type = ?, target_config = ?, target_host = ?, target_port = ?, target_resource_key = ?,
		transport_policy = ?, actual_transport = ?, p2p_state = ?, p2p_error = ?, p2p_session_id = ?,
		ingress_bps = ?, egress_bps = ?, total_bps = ?, desired_state = ?, runtime_state = ?, error = ?, updated_at = ?
		WHERE client_id = ? AND id = ? AND revision = ?`,
		replacement.Name,
		replacement.Type,
		replacement.LocalIP,
		replacement.LocalPort,
		replacement.RemotePort,
		replacement.Domain,
		replacement.Revision,
		replacement.Topology,
		replacement.OwnerClientID,
		replacement.Ingress.Location,
		replacement.Ingress.ClientID,
		replacement.Ingress.Type,
		string(replacement.Ingress.Config),
		tunnelIngressBindIP(replacement),
		tunnelIngressPort(replacement),
		tunnelIngressDomain(replacement),
		replacement.Target.Location,
		replacement.Target.ClientID,
		replacement.Target.Type,
		string(replacement.Target.Config),
		replacement.LocalIP,
		replacement.LocalPort,
		tunnelTargetResourceKey(replacement),
		replacement.TransportPolicy,
		storageActualTransport(replacement),
		replacement.P2P.State,
		replacement.P2P.Error,
		replacement.P2P.SessionID,
		replacement.IngressBPS,
		replacement.EgressBPS,
		replacement.TotalBPS,
		replacement.DesiredState,
		storageRuntimeStateFromProtocol(replacement.RuntimeState),
		replacement.Error,
		formatTime(replacement.UpdatedAt),
		clientID,
		id,
		expectedRevision,
	)
	if err != nil {
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if rowsAffected == 0 {
		return 0, ErrTunnelRevisionConflict
	}
	if err := replaceTunnelResourceLocksTx(tx, replacement); err != nil {
		return 0, err
	}
	var activityID int64
	if actor != nil {
		activityID, err = s.appendActivityTx(tx, tunnelTransitionActivitySpec("updated", existing, replacement, *actor))
		if err != nil {
			return 0, err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return activityID, nil
}

// MigrateTunnelTargetByID replaces a tunnel's target-side owner by stable id and
// expected revision. It updates the tunnel row and resource locks atomically and
// returns the stored tunnel before and after migration.
func (s *TunnelStore) MigrateTunnelTargetByIDWithActivity(id string, expectedRevision int64, replacement StoredTunnel, actor ActivityActor) (StoredTunnel, StoredTunnel, int64, error) {
	return s.migrateTunnelTargetByID(id, expectedRevision, replacement, &actor)
}

func (s *TunnelStore) MigrateTunnelTargetByID(id string, expectedRevision int64, replacement StoredTunnel) (StoredTunnel, StoredTunnel, error) {
	before, after, _, err := s.migrateTunnelTargetByID(id, expectedRevision, replacement, nil)
	return before, after, err
}

func (s *TunnelStore) migrateTunnelTargetByID(id string, expectedRevision int64, replacement StoredTunnel, actor *ActivityActor) (StoredTunnel, StoredTunnel, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	trafficStore := s.trafficStore
	if trafficStore != nil {
		trafficStore.mu.Lock()
		defer trafficStore.mu.Unlock()
	}

	if expectedRevision <= 0 {
		return StoredTunnel{}, StoredTunnel{}, 0, fmt.Errorf("expected revision is required")
	}
	existing, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StoredTunnel{}, StoredTunnel{}, 0, ErrTunnelNotFound
	}
	if err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	if existing.Revision != expectedRevision {
		return StoredTunnel{}, StoredTunnel{}, 0, ErrTunnelRevisionConflict
	}
	if existing.RuntimeState == protocol.ProxyRuntimeStatePending {
		return StoredTunnel{}, StoredTunnel{}, 0, ErrTunnelMigrationPending
	}
	replacement.ID = id
	replacement.Revision = expectedRevision + 1
	replacement.CreatedAt = existing.CreatedAt
	replacement.CreatedByUserID = existing.CreatedByUserID
	replacement.Hostname = existing.Hostname
	replacement.Binding = existing.Binding
	if replacement.UpdatedAt.IsZero() {
		replacement.UpdatedAt = time.Now().UTC()
	}
	if err := replacement.normalize(); err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	var conflictingID string
	err = s.db.QueryRow(`SELECT id FROM tunnels WHERE owner_client_id = ? AND name = ? AND id <> ? LIMIT 1`, replacement.OwnerClientID, replacement.Name, id).Scan(&conflictingID)
	if err == nil {
		return StoredTunnel{}, StoredTunnel{}, 0, ErrTunnelOwnerNameConflict
	}
	if err != sql.ErrNoRows {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	if err := s.maybeFailSave(); err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	var targetExists int
	if err := tx.QueryRow(`SELECT 1 FROM registered_clients WHERE id = ?`, replacement.Target.ClientID).Scan(&targetExists); err != nil {
		if err == sql.ErrNoRows {
			return StoredTunnel{}, StoredTunnel{}, 0, ErrTunnelTargetClientNotFound
		}
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}

	result, err := tx.Exec(`UPDATE tunnels SET
		client_id = ?, name = ?, type = ?, local_ip = ?, local_port = ?, remote_port = ?, domain = ?,
		revision = ?, topology = ?, owner_client_id = ?,
		ingress_location = ?, ingress_client_id = ?, ingress_type = ?, ingress_config = ?, ingress_bind_ip = ?, ingress_port = ?, ingress_domain = ?,
		target_location = ?, target_client_id = ?, target_type = ?, target_config = ?, target_host = ?, target_port = ?, target_resource_key = ?,
		transport_policy = ?, actual_transport = ?, p2p_state = ?, p2p_error = ?, p2p_session_id = ?,
		ingress_bps = ?, egress_bps = ?, total_bps = ?, desired_state = ?, runtime_state = ?, error = ?, updated_at = ?
		WHERE id = ? AND revision = ?`,
		replacement.ClientID,
		replacement.Name,
		replacement.Type,
		replacement.LocalIP,
		replacement.LocalPort,
		replacement.RemotePort,
		replacement.Domain,
		replacement.Revision,
		replacement.Topology,
		replacement.OwnerClientID,
		replacement.Ingress.Location,
		replacement.Ingress.ClientID,
		replacement.Ingress.Type,
		string(replacement.Ingress.Config),
		tunnelIngressBindIP(replacement),
		tunnelIngressPort(replacement),
		tunnelIngressDomain(replacement),
		replacement.Target.Location,
		replacement.Target.ClientID,
		replacement.Target.Type,
		string(replacement.Target.Config),
		replacement.LocalIP,
		replacement.LocalPort,
		tunnelTargetResourceKey(replacement),
		replacement.TransportPolicy,
		storageActualTransport(replacement),
		replacement.P2P.State,
		replacement.P2P.Error,
		replacement.P2P.SessionID,
		replacement.IngressBPS,
		replacement.EgressBPS,
		replacement.TotalBPS,
		replacement.DesiredState,
		storageRuntimeStateFromProtocol(replacement.RuntimeState),
		replacement.Error,
		formatTime(replacement.UpdatedAt),
		id,
		expectedRevision,
	)
	if err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	if rowsAffected == 0 {
		return StoredTunnel{}, StoredTunnel{}, 0, ErrTunnelRevisionConflict
	}
	if err := replaceTunnelResourceLocksTx(tx, replacement); err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	if _, err := tx.Exec(`DELETE FROM traffic_buckets WHERE tunnel_id = ?`, id); err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	var activityID int64
	if actor != nil {
		activityID, err = s.appendActivityTx(tx, tunnelMigrationActivitySpec(existing, replacement, *actor))
		if err != nil {
			return StoredTunnel{}, StoredTunnel{}, 0, err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	after, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE id = ?`, id))
	if err != nil {
		return StoredTunnel{}, StoredTunnel{}, 0, err
	}
	if trafficStore != nil {
		trafficStore.resetTunnelAfterMigrationLocked(id, after.Revision)
	}
	return existing, after, activityID, nil
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
	_, err := s.DeleteTunnelsByClientIDReturningDeleted(clientID)
	return err
}

func (s *TunnelStore) DeleteTunnelsByClientIDReturningDeleted(clientID string) ([]StoredTunnel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.maybeFailSave(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	rows, err := tx.Query(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? OR owner_client_id = ? OR ingress_client_id = ? OR target_client_id = ? ORDER BY created_at DESC, name`, clientID, clientID, clientID, clientID)
	if err != nil {
		return nil, err
	}
	deleted, err := scanStoredTunnelRows(rows)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM tunnel_resource_locks WHERE tunnel_id IN (
		SELECT id FROM tunnels WHERE client_id = ? OR owner_client_id = ? OR ingress_client_id = ? OR target_client_id = ?
	)`, clientID, clientID, clientID, clientID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM tunnels WHERE client_id = ? OR owner_client_id = ? OR ingress_client_id = ? OR target_client_id = ?`, clientID, clientID, clientID, clientID); err != nil {
		return nil, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, err
	}
	return deleted, nil
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

// GetTunnelByID looks up a single tunnel by stable id.
func (s *TunnelStore) GetTunnelByID(id string) (StoredTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tunnel, err := scanStoredTunnel(s.db.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE id = ?`, id))
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

func normalizeUnifiedTunnelSpec(t *StoredTunnel) {
	if t.Topology == "" {
		t.Topology = TunnelTopologyServerExpose
	}
	if t.OwnerClientID == "" {
		t.OwnerClientID = t.ClientID
	}
	if t.TransportPolicy == "" {
		t.TransportPolicy = TunnelTransportServerRelayOnly
	}
	if t.ActualTransport == "" {
		t.ActualTransport = TunnelActualTransportUnknown
	}
	if t.P2P.State == "" {
		t.P2P.State = TunnelP2PStateIdle
	}

	if t.Type == "" {
		switch t.Ingress.Type {
		case TunnelIngressTypeUDPListen:
			t.Type = protocol.ProxyTypeUDP
		case TunnelIngressTypeHTTPHost:
			t.Type = protocol.ProxyTypeHTTP
		default:
			t.Type = protocol.ProxyTypeTCP
		}
	}

	if t.Ingress.Type == "" || t.Ingress.Location == "" || isEmptyEndpointConfig(t.Ingress.Config) {
		t.Ingress = legacyIngressSpec(t)
	}
	if t.Target.Type == "" || t.Target.Location == "" || isEmptyEndpointConfig(t.Target.Config) {
		t.Target = legacyTargetSpec(t)
	}
	switch t.Topology {
	case TunnelTopologyServerExpose:
		t.Ingress.Location = "server"
		t.Ingress.ClientID = ""
	case TunnelTopologyClientToClient:
		t.Ingress.Location = "client"
	}
	t.Target.Location = "client"
	if t.Target.ClientID == "" {
		t.Target.ClientID = t.ClientID
	}
	if t.ClientID == "" {
		t.ClientID = t.OwnerClientID
	}
	if t.OwnerClientID == "" {
		t.OwnerClientID = t.Target.ClientID
	}
}

func legacyIngressSpec(t *StoredTunnel) EndpointSpec {
	ingressType := TunnelIngressTypeTCPListen
	switch t.Type {
	case protocol.ProxyTypeUDP:
		ingressType = TunnelIngressTypeUDPListen
	case protocol.ProxyTypeHTTP:
		ingressType = TunnelIngressTypeHTTPHost
	}
	return EndpointSpec{
		Location: "server",
		Type:     ingressType,
		Config:   mustJSONRawMessage(tunnelIngressConfig(t)),
	}
}

func legacyTargetSpec(t *StoredTunnel) EndpointSpec {
	targetType := TunnelTargetTypeTCPService
	if t.Type == protocol.ProxyTypeUDP {
		targetType = TunnelTargetTypeUDPService
	}
	return EndpointSpec{
		Location: "client",
		ClientID: t.ClientID,
		Type:     targetType,
		Config:   mustJSONRawMessage(tunnelTargetConfig(*t)),
	}
}

func isEmptyEndpointConfig(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "{}" || trimmed == "null"
}

func validateUnifiedTunnelSpec(t StoredTunnel) error {
	if t.Topology != TunnelTopologyServerExpose && t.Topology != TunnelTopologyClientToClient {
		return fmt.Errorf("unsupported tunnel topology %q", t.Topology)
	}
	if t.OwnerClientID == "" {
		return fmt.Errorf("tunnel %q is missing owner_client_id", t.Name)
	}
	if t.Topology == TunnelTopologyServerExpose {
		if t.Ingress.Location != "server" || t.Ingress.ClientID != "" {
			return fmt.Errorf("server_expose tunnel %q must use server ingress", t.Name)
		}
		if t.OwnerClientID != t.Target.ClientID {
			return fmt.Errorf("server_expose tunnel %q owner must be target client", t.Name)
		}
	}
	if t.Topology == TunnelTopologyClientToClient {
		if t.Ingress.Location != "client" || t.Ingress.ClientID == "" {
			return fmt.Errorf("client_to_client tunnel %q must use client ingress", t.Name)
		}
		if t.Ingress.ClientID == t.Target.ClientID {
			return fmt.Errorf("client_to_client tunnel %q ingress and target clients must differ", t.Name)
		}
		if t.OwnerClientID != t.Target.ClientID {
			return fmt.Errorf("client_to_client tunnel %q owner must be target client", t.Name)
		}
	}
	if t.Ingress.Location != "server" && t.Ingress.Location != "client" {
		return fmt.Errorf("unsupported ingress location %q", t.Ingress.Location)
	}
	if t.Target.Location != "client" {
		return fmt.Errorf("unsupported target location %q", t.Target.Location)
	}
	switch t.Ingress.Type {
	case TunnelIngressTypeTCPListen, TunnelIngressTypeUDPListen, TunnelIngressTypeHTTPHost, TunnelIngressTypeSOCKS5Listen:
	default:
		return fmt.Errorf("unsupported ingress type %q", t.Ingress.Type)
	}
	switch t.Target.Type {
	case TunnelTargetTypeTCPService, TunnelTargetTypeUDPService, TunnelTargetTypeSOCKS5ConnectHandler:
	default:
		return fmt.Errorf("unsupported target type %q", t.Target.Type)
	}
	switch t.TransportPolicy {
	case TunnelTransportServerRelayOnly, TunnelTransportDirectPreferred, TunnelTransportDirectOnly:
	default:
		return fmt.Errorf("unsupported transport policy %q", t.TransportPolicy)
	}
	if !json.Valid(t.Ingress.Config) {
		return fmt.Errorf("invalid ingress config JSON")
	}
	if !json.Valid(t.Target.Config) {
		return fmt.Errorf("invalid target config JSON")
	}
	return nil
}

func insertTunnelTx(tx *sql.Tx, tunnel StoredTunnel) error {
	_, err := tx.Exec(`INSERT INTO tunnels (
		id, client_id, name, type, local_ip, local_port, remote_port, domain, hostname, binding,
		revision, topology, owner_client_id,
		ingress_location, ingress_client_id, ingress_type, ingress_config, ingress_bind_ip, ingress_port, ingress_domain, ingress_path,
		target_location, target_client_id, target_type, target_config, target_host, target_port, target_path, target_resource_key,
		transport_policy, actual_transport, p2p_state, p2p_error, p2p_session_id,
		ingress_bps, egress_bps, total_bps, desired_state, runtime_state, error, created_by_user_id, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tunnel.ID,
		tunnel.ClientID,
		tunnel.Name,
		tunnel.Type,
		tunnel.LocalIP,
		tunnel.LocalPort,
		tunnel.RemotePort,
		tunnel.Domain,
		tunnel.Hostname,
		tunnel.Binding,
		tunnel.Revision,
		tunnel.Topology,
		tunnel.OwnerClientID,
		tunnel.Ingress.Location,
		tunnel.Ingress.ClientID,
		tunnel.Ingress.Type,
		string(tunnel.Ingress.Config),
		tunnelIngressBindIP(tunnel),
		tunnelIngressPort(tunnel),
		tunnelIngressDomain(tunnel),
		"",
		tunnel.Target.Location,
		tunnel.Target.ClientID,
		tunnel.Target.Type,
		string(tunnel.Target.Config),
		tunnel.LocalIP,
		tunnel.LocalPort,
		"",
		tunnelTargetResourceKey(tunnel),
		tunnel.TransportPolicy,
		storageActualTransport(tunnel),
		tunnel.P2P.State,
		tunnel.P2P.Error,
		tunnel.P2P.SessionID,
		tunnel.IngressBPS,
		tunnel.EgressBPS,
		tunnel.TotalBPS,
		tunnel.DesiredState,
		storageRuntimeStateFromProtocol(tunnel.RuntimeState),
		tunnel.Error,
		tunnel.CreatedByUserID,
		formatTime(tunnel.CreatedAt),
		formatTime(tunnel.UpdatedAt),
	)
	return err
}

func replaceTunnelResourceLocksTx(tx *sql.Tx, tunnel StoredTunnel) error {
	if _, err := tx.Exec(`DELETE FROM tunnel_resource_locks WHERE tunnel_id = ?`, tunnel.ID); err != nil {
		return err
	}
	key, kind, clientID := tunnelIngressResourceLock(tunnel)
	if key == "" {
		return nil
	}
	if err := checkTunnelIngressResourceConflictTx(tx, tunnel); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO tunnel_resource_locks (resource_key, tunnel_id, resource_kind, client_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		key, tunnel.ID, kind, clientID, formatTime(time.Now().UTC()))
	return err
}

func checkTunnelIngressResourceConflictTx(tx *sql.Tx, tunnel StoredTunnel) error {
	keys := tunnelIngressConflictKeys(tunnel)
	patterns := tunnelIngressConflictPatterns(tunnel)
	if len(keys) == 0 && len(patterns) == 0 {
		return nil
	}
	clauses := make([]string, 0, len(keys)+len(patterns))
	args := make([]any, 0, len(keys)+len(patterns)+1)
	if len(keys) > 0 {
		placeholders := make([]string, 0, len(keys))
		for _, key := range keys {
			placeholders = append(placeholders, "?")
			args = append(args, key)
		}
		clauses = append(clauses, "resource_key IN ("+strings.Join(placeholders, ",")+")")
	}
	for _, pattern := range patterns {
		clauses = append(clauses, "resource_key LIKE ?")
		args = append(args, pattern)
	}
	args = append(args, tunnel.ID)
	var existing string
	err := tx.QueryRow(`SELECT resource_key FROM tunnel_resource_locks WHERE (`+strings.Join(clauses, " OR ")+`) AND tunnel_id <> ? LIMIT 1`, args...).Scan(&existing)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("ingress resource conflict: %s", existing)
}

func (s *TunnelStore) findIngressResourceConflict(candidate StoredTunnel, excludeID string) (StoredTunnel, bool, error) {
	keys := tunnelIngressConflictKeys(candidate)
	patterns := tunnelIngressConflictPatterns(candidate)
	if len(keys) == 0 && len(patterns) == 0 {
		return StoredTunnel{}, false, nil
	}

	clauses := make([]string, 0, len(keys)+len(patterns))
	args := make([]any, 0, len(keys)+len(patterns)+1)
	if len(keys) > 0 {
		placeholders := make([]string, 0, len(keys))
		for _, key := range keys {
			placeholders = append(placeholders, "?")
			args = append(args, key)
		}
		clauses = append(clauses, "l.resource_key IN ("+strings.Join(placeholders, ",")+")")
	}
	for _, pattern := range patterns {
		clauses = append(clauses, "l.resource_key LIKE ?")
		args = append(args, pattern)
	}
	if excludeID == "" {
		excludeID = candidate.ID
	}

	query := `SELECT ` + prefixedTunnelSelectColumns("t.") + `
FROM tunnel_resource_locks l
JOIN tunnels t ON t.id = l.tunnel_id
WHERE (` + strings.Join(clauses, " OR ") + `)`
	if excludeID != "" {
		query += ` AND l.tunnel_id <> ?`
		args = append(args, excludeID)
	}
	query += ` ORDER BY t.created_at DESC, t.name LIMIT 1`

	s.mu.RLock()
	defer s.mu.RUnlock()

	conflict, err := scanStoredTunnel(s.db.QueryRow(query, args...))
	if err == sql.ErrNoRows {
		return StoredTunnel{}, false, nil
	}
	if err != nil {
		return StoredTunnel{}, false, err
	}
	return conflict, true, nil
}

func tunnelIngressConflictPatterns(tunnel StoredTunnel) []string {
	switch tunnel.Ingress.Type {
	case TunnelIngressTypeTCPListen, TunnelIngressTypeUDPListen, TunnelIngressTypeSOCKS5Listen:
	default:
		return nil
	}
	if tunnelIngressBindIP(tunnel) != "0.0.0.0" {
		return nil
	}
	port := tunnelIngressPort(tunnel)
	if port <= 0 {
		return nil
	}
	proto := "tcp"
	if tunnel.Ingress.Type == TunnelIngressTypeUDPListen {
		proto = "udp"
	}
	locationID := tunnel.Ingress.Location
	if locationID == "" {
		locationID = "server"
	}
	if tunnel.Ingress.ClientID != "" {
		locationID += ":" + tunnel.Ingress.ClientID
	}
	return []string{fmt.Sprintf("ingress:%s:%s:%%:%d", locationID, proto, port)}
}

func tunnelIngressConflictKeys(tunnel StoredTunnel) []string {
	key, _, _ := tunnelIngressResourceLock(tunnel)
	if key == "" {
		return nil
	}
	switch tunnel.Ingress.Type {
	case TunnelIngressTypeTCPListen, TunnelIngressTypeUDPListen, TunnelIngressTypeSOCKS5Listen:
	default:
		return []string{key}
	}
	bindIP := tunnelIngressBindIP(tunnel)
	port := tunnelIngressPort(tunnel)
	if port <= 0 {
		return nil
	}
	proto := "tcp"
	if tunnel.Ingress.Type == TunnelIngressTypeUDPListen {
		proto = "udp"
	}
	locationID := tunnel.Ingress.Location
	if locationID == "" {
		locationID = "server"
	}
	if tunnel.Ingress.ClientID != "" {
		locationID += ":" + tunnel.Ingress.ClientID
	}
	if bindIP == "0.0.0.0" {
		return []string{key}
	}
	return []string{key, fmt.Sprintf("ingress:%s:%s:0.0.0.0:%d", locationID, proto, port)}
}

func tunnelIngressResourceLock(tunnel StoredTunnel) (key, kind, clientID string) {
	location := tunnel.Ingress.Location
	if location == "" {
		location = "server"
	}
	locationID := location
	if tunnel.Ingress.ClientID != "" {
		locationID += ":" + tunnel.Ingress.ClientID
	}
	switch tunnel.Ingress.Type {
	case TunnelIngressTypeTCPListen, TunnelIngressTypeSOCKS5Listen:
		port := tunnelIngressPort(tunnel)
		if port <= 0 {
			return "", "", ""
		}
		kind := "server_tcp_port"
		if location == "client" {
			kind = "client_tcp_port"
		}
		return "ingress:" + locationID + ":tcp:" + tunnelIngressBindIP(tunnel) + ":" + strconv.Itoa(port), kind, tunnel.Ingress.ClientID
	case TunnelIngressTypeUDPListen:
		port := tunnelIngressPort(tunnel)
		if port <= 0 {
			return "", "", ""
		}
		kind := "server_udp_port"
		if location == "client" {
			kind = "client_udp_port"
		}
		return "ingress:" + locationID + ":udp:" + tunnelIngressBindIP(tunnel) + ":" + strconv.Itoa(port), kind, tunnel.Ingress.ClientID
	case TunnelIngressTypeHTTPHost:
		domain := strings.ToLower(tunnelIngressDomain(tunnel))
		if domain == "" {
			return "", "", ""
		}
		return "ingress:" + locationID + ":http_host:" + domain, "server_http_host", tunnel.Ingress.ClientID
	default:
		return "", "", ""
	}
}

func tunnelIngressConfig(t *StoredTunnel) map[string]any {
	switch t.Type {
	case protocol.ProxyTypeHTTP:
		return map[string]any{
			"domain":               t.Domain,
			"allowed_source_cidrs": allowAllSourceCIDRs(),
			"auth":                 protocol.HTTPAuthConfig{Type: protocol.HTTPAuthTypeNone},
		}
	case protocol.ProxyTypeUDP:
		return map[string]any{"bind_ip": normalizeServerBindIP(t.BindIP), "port": t.RemotePort, "allowed_source_cidrs": allowAllSourceCIDRs()}
	default:
		return map[string]any{"bind_ip": normalizeServerBindIP(t.BindIP), "port": t.RemotePort, "allowed_source_cidrs": allowAllSourceCIDRs()}
	}
}

func tunnelTargetConfig(t StoredTunnel) map[string]any {
	return map[string]any{"host": t.LocalIP, "port": t.LocalPort}
}

func mustJSONRawMessage(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(raw)
}

func tunnelIngressBindIP(tunnel StoredTunnel) string {
	if tunnel.Ingress.Type == TunnelIngressTypeHTTPHost {
		return ""
	}
	var cfg struct {
		BindIP string `json:"bind_ip"`
	}
	if len(tunnel.Ingress.Config) > 0 && json.Unmarshal(tunnel.Ingress.Config, &cfg) == nil && cfg.BindIP != "" {
		return cfg.BindIP
	}
	return "0.0.0.0"
}

func tunnelIngressPort(tunnel StoredTunnel) int {
	if tunnel.Ingress.Type == TunnelIngressTypeHTTPHost {
		return 0
	}
	var cfg struct {
		Port int `json:"port"`
	}
	if len(tunnel.Ingress.Config) > 0 && json.Unmarshal(tunnel.Ingress.Config, &cfg) == nil && cfg.Port > 0 {
		return cfg.Port
	}
	return tunnel.RemotePort
}

func tunnelIngressDomain(tunnel StoredTunnel) string {
	if tunnel.Ingress.Type != TunnelIngressTypeHTTPHost {
		return ""
	}
	var cfg struct {
		Domain string `json:"domain"`
	}
	if len(tunnel.Ingress.Config) > 0 && json.Unmarshal(tunnel.Ingress.Config, &cfg) == nil && cfg.Domain != "" {
		return strings.ToLower(cfg.Domain)
	}
	return strings.ToLower(tunnel.Domain)
}

func tunnelTargetResourceKey(tunnel StoredTunnel) string {
	targetType := tunnel.Target.Type
	if targetType == "" {
		targetType = TunnelTargetTypeTCPService
	}
	targetClientID := tunnel.Target.ClientID
	if targetClientID == "" {
		targetClientID = tunnel.ClientID
	}
	if targetType == TunnelTargetTypeSOCKS5ConnectHandler {
		return "target:client:" + targetClientID + ":" + targetType
	}
	host := tunnel.LocalIP
	var cfg struct {
		IP   string `json:"ip"`
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if len(tunnel.Target.Config) > 0 && json.Unmarshal(tunnel.Target.Config, &cfg) == nil {
		if cfg.IP != "" {
			host = cfg.IP
		} else if cfg.Host != "" {
			host = cfg.Host
		}
		if cfg.Port > 0 {
			tunnel.LocalPort = cfg.Port
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	return "target:client:" + targetClientID + ":" + targetType + ":" + host + ":" + strconv.Itoa(tunnel.LocalPort)
}

func storageRuntimeStateFromProtocol(runtimeState string) string {
	if runtimeState == protocol.ProxyRuntimeStateExposed {
		return "active"
	}
	return runtimeState
}

func protocolRuntimeStateFromStorage(runtimeState string) string {
	if runtimeState == "active" {
		return protocol.ProxyRuntimeStateExposed
	}
	return runtimeState
}

func storageActualTransport(tunnel StoredTunnel) string {
	if tunnel.ActualTransport != "" && tunnel.ActualTransport != TunnelActualTransportUnknown {
		return tunnel.ActualTransport
	}
	if storageRuntimeStateFromProtocol(tunnel.RuntimeState) == "active" {
		return TunnelActualTransportServerRelay
	}
	return TunnelActualTransportUnknown
}
