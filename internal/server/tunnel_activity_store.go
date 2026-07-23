package server

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *TunnelStore) RemoveTunnelByIDWithActivity(clientID, id string, actor ActivityActor) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("tunnel store is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.maybeFailSave(); err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	stored, err := scanStoredTunnel(tx.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("tunnel id %q does not exist (client_id: %s)", id, clientID)
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM tunnel_resource_locks WHERE tunnel_id = ?`, id); err != nil {
		return 0, err
	}
	result, err := tx.Exec(`DELETE FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id)
	if err != nil {
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if rowsAffected == 0 {
		return 0, ErrTunnelNotFound
	}
	activityID, err := s.appendActivityTx(tx, tunnelActivitySpec("deleted", stored, actor))
	if err != nil {
		return 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return activityID, nil
}

func (s *TunnelStore) UpdateTunnelStatesWithActivity(clientID, id, desiredState, runtimeState, errMsg, action string, actor ActivityActor) (StoredTunnel, int64, error) {
	if action != "stopped" && action != "resumed" {
		return StoredTunnel{}, 0, fmt.Errorf("unsupported authoritative tunnel state action %q", action)
	}
	if s == nil || s.db == nil {
		return StoredTunnel{}, 0, errors.New("tunnel store is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return StoredTunnel{}, 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	before, err := scanStoredTunnel(tx.QueryRow(`SELECT `+tunnelSelectColumns+` FROM tunnels WHERE client_id = ? AND id = ?`, clientID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredTunnel{}, 0, ErrTunnelNotFound
	}
	if err != nil {
		return StoredTunnel{}, 0, err
	}
	after := before
	setStoredTunnelStates(&after, desiredState, runtimeState, errMsg)
	storageRuntimeState := storageRuntimeStateFromProtocol(after.RuntimeState)
	after.ActualTransport = TunnelActualTransportUnknown
	if storageRuntimeState == "active" {
		after.ActualTransport = TunnelActualTransportServerRelay
	}
	if before.DesiredState == after.DesiredState &&
		storageRuntimeStateFromProtocol(before.RuntimeState) == storageRuntimeState &&
		before.Error == after.Error && before.ActualTransport == after.ActualTransport {
		if err := commitTx(tx, &committed); err != nil {
			return StoredTunnel{}, 0, err
		}
		return before, 0, nil
	}
	if err := s.maybeFailSave(); err != nil {
		return StoredTunnel{}, 0, err
	}
	after.UpdatedAt = time.Now().UTC()
	result, err := tx.Exec(`UPDATE tunnels SET desired_state = ?, runtime_state = ?, error = ?, actual_transport = ?, updated_at = ?
		WHERE client_id = ? AND id = ? AND revision = ?
		AND desired_state = ? AND runtime_state = ? AND error = ? AND actual_transport = ?`,
		after.DesiredState, storageRuntimeState, after.Error, after.ActualTransport, formatTime(after.UpdatedAt),
		clientID, id, before.Revision, before.DesiredState, storageRuntimeStateFromProtocol(before.RuntimeState), before.Error, before.ActualTransport)
	if err != nil {
		return StoredTunnel{}, 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return StoredTunnel{}, 0, err
	}
	if rowsAffected == 0 {
		return StoredTunnel{}, 0, ErrTunnelRevisionConflict
	}
	activityID, err := s.appendActivityTx(tx, tunnelTransitionActivitySpec(action, before, after, actor))
	if err != nil {
		return StoredTunnel{}, 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return StoredTunnel{}, 0, err
	}
	return after, activityID, nil
}
