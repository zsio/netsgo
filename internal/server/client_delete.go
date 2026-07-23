package server

import (
	"database/sql"
	"errors"
	"time"
)

type deletedClientResult struct {
	Client      RegisteredClient
	Tunnels     []StoredTunnel
	ActivityIDs []int64
}

func (s *Server) ensureSharedStoreReferences() {
	if s.serverDB == nil && s.auth != nil && s.auth.adminStore != nil {
		s.serverDB = s.auth.adminStore.db
	}
	if s.activityStore == nil && s.serverDB != nil {
		s.activityStore = newActivityStoreWithDB(s.getStorePath(), s.serverDB, false)
	}
	if s.store != nil && s.store.activityStore == nil {
		s.store.activityStore = s.activityStore
	}
	if s.auth != nil && s.auth.adminStore != nil && s.auth.adminStore.activityStore == nil {
		s.auth.adminStore.activityStore = s.activityStore
	}
}
func (s *Server) deleteRegisteredClientWithActivity(clientID string, actor ActivityActor) (deletedClientResult, error) {
	s.ensureSharedStoreReferences()
	if s.serverDB == nil || s.auth == nil || s.auth.adminStore == nil || s.store == nil {
		return deletedClientResult{}, errors.New("server stores are not initialized")
	}

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.trafficStore != nil {
		s.trafficStore.mu.Lock()
		defer s.trafficStore.mu.Unlock()
	}
	if err := s.store.maybeFailSave(); err != nil {
		return deletedClientResult{}, err
	}
	if err := s.auth.adminStore.maybeFailSave(); err != nil {
		return deletedClientResult{}, err
	}

	tx, err := s.serverDB.Begin()
	if err != nil {
		return deletedClientResult{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	client, err := loadRegisteredClient(tx, `WHERE id = ?`, clientID)
	if errors.Is(err, sql.ErrNoRows) {
		return deletedClientResult{}, ErrRegisteredClientNotFound
	}
	if err != nil {
		return deletedClientResult{}, err
	}
	rows, err := tx.Query(`SELECT `+tunnelSelectColumns+` FROM tunnels
		WHERE client_id = ? OR owner_client_id = ? OR ingress_client_id = ? OR target_client_id = ?
		ORDER BY created_at DESC, name`, clientID, clientID, clientID, clientID)
	if err != nil {
		return deletedClientResult{}, err
	}
	tunnels, err := scanStoredTunnelRows(rows)
	if err != nil {
		return deletedClientResult{}, err
	}

	if _, err := tx.Exec(`DELETE FROM tunnel_resource_locks WHERE tunnel_id IN (
		SELECT id FROM tunnels WHERE client_id = ? OR owner_client_id = ? OR ingress_client_id = ? OR target_client_id = ?
	)`, clientID, clientID, clientID, clientID); err != nil {
		return deletedClientResult{}, err
	}
	if _, err := tx.Exec(`DELETE FROM traffic_buckets WHERE
		client_id = ? OR owner_client_id = ? OR ingress_client_id = ? OR target_client_id = ?
		OR tunnel_id IN (
			SELECT id FROM tunnels WHERE client_id = ? OR owner_client_id = ? OR ingress_client_id = ? OR target_client_id = ?
		)`, clientID, clientID, clientID, clientID, clientID, clientID, clientID, clientID); err != nil {
		return deletedClientResult{}, err
	}
	if _, err := tx.Exec(`DELETE FROM tunnels WHERE client_id = ? OR owner_client_id = ? OR ingress_client_id = ? OR target_client_id = ?`, clientID, clientID, clientID, clientID); err != nil {
		return deletedClientResult{}, err
	}
	if _, err := tx.Exec(`DELETE FROM client_disk_partitions WHERE client_id = ?`, clientID); err != nil {
		return deletedClientResult{}, err
	}
	if _, err := tx.Exec(`DELETE FROM client_stats WHERE client_id = ?`, clientID); err != nil {
		return deletedClientResult{}, err
	}
	if _, err := tx.Exec(`DELETE FROM client_tokens WHERE client_id = ? OR install_id = ?`, clientID, client.InstallID); err != nil {
		return deletedClientResult{}, err
	}
	result, err := tx.Exec(`DELETE FROM registered_clients WHERE id = ?`, clientID)
	if err != nil {
		return deletedClientResult{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return deletedClientResult{}, err
	}
	if count == 0 {
		return deletedClientResult{}, ErrRegisteredClientNotFound
	}

	activityIDs := make([]int64, 0, len(tunnels)+1)
	for _, tunnel := range tunnels {
		id, err := s.activityStore.appendTx(tx, tunnelActivitySpec("deleted", tunnel, actor))
		if err != nil {
			return deletedClientResult{}, err
		}
		activityIDs = append(activityIDs, id)
	}
	payload := newActivityPayload(ActivityCategoryClient, "deleted", ActivitySummaryArgs{ClientName: activityClientDisplayName(client), Count: len(tunnels)})
	clientEventID, err := s.activityStore.appendTx(tx, ActivityEventSpec{
		OccurredAt: time.Now().UTC(), Category: ActivityCategoryClient, Action: "deleted", Source: "server", Actor: actor, Payload: payload,
		Clients: []ActivityClientSubject{{ClientID: client.ID, Relation: "subject", DisplayName: client.DisplayName, Hostname: client.Info.Hostname}},
	})
	if err != nil {
		return deletedClientResult{}, err
	}
	activityIDs = append(activityIDs, clientEventID)

	if err := commitTx(tx, &committed); err != nil {
		return deletedClientResult{}, err
	}
	if s.trafficStore != nil {
		s.trafficStore.evictDeletedClientLocked(clientID, tunnels)
	}
	return deletedClientResult{Client: client, Tunnels: tunnels, ActivityIDs: activityIDs}, nil
}

func (s *TrafficStore) evictDeletedClientLocked(clientID string, tunnels []StoredTunnel) {
	if s == nil {
		return
	}
	tunnelIDs := make(map[string]int64, len(tunnels))
	for _, tunnel := range tunnels {
		tunnelIDs[tunnel.ID] = tunnel.Revision + 1
		if s.minimumRevisionByTunnel == nil {
			s.minimumRevisionByTunnel = make(map[string]int64)
		}
		if s.minimumRevisionByTunnel[tunnel.ID] < tunnel.Revision+1 {
			s.minimumRevisionByTunnel[tunnel.ID] = tunnel.Revision + 1
		}
		if accumulator := s.accumulator.Load(); accumulator != nil {
			accumulator.ResetTunnel(tunnel.ID, tunnel.Revision+1)
		}
	}
	for key, bucket := range s.pendingMinute {
		_, deletedTunnel := tunnelIDs[bucket.TunnelID]
		if deletedTunnel || bucket.ClientID == clientID || bucket.OwnerClientID == clientID || bucket.IngressClientID == clientID || bucket.TargetClientID == clientID {
			delete(s.pendingMinute, key)
		}
	}
	s.realtimeSecond.EvictClient(clientID)
	for _, tunnel := range tunnels {
		ownerID := tunnel.OwnerClientID
		if ownerID == "" {
			ownerID = tunnel.ClientID
		}
		s.realtimeSecond.EvictTunnel(ownerID, tunnel.Name)
	}
	if len(s.pendingMinute) == 0 {
		s.pendingErr = nil
	}
}
