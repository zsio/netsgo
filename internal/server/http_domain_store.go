package server

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"netsgo/pkg/protocol"
)

type httpDomainQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

// Shared by preflight and the write transaction. The transaction is the
// authority: create/update must never rely on a check performed before it.
func findHTTPIngressConflict(db httpDomainQueryer, candidate StoredTunnel, excludeID string) (StoredTunnel, bool, error) {
	if excludeID == "" {
		excludeID = candidate.ID
	}
	location := candidate.Ingress.Location
	if location == "" {
		location = protocol.EndpointLocationServer
	}
	rows, err := db.Query(`SELECT `+tunnelSelectColumns+` FROM tunnels
		WHERE ingress_type = ? AND ingress_location = ? AND ingress_client_id = ? AND id <> ?
		ORDER BY id`, TunnelIngressTypeHTTPHost, location, candidate.Ingress.ClientID, excludeID)
	if err != nil {
		return StoredTunnel{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		existing, err := scanStoredTunnel(rows)
		if err != nil {
			return StoredTunnel{}, false, err
		}
		if httpDomainsConflict(tunnelIngressDomain(candidate), candidate.OwnerUserID, tunnelIngressDomain(existing), existing.OwnerUserID) {
			return existing, true, nil
		}
	}
	return StoredTunnel{}, false, rows.Err()
}

func httpDomainResourceConflictError() error {
	// Do not disclose the other user's client, tunnel name, or domain pattern.
	return newProxyRequestValidationError(fmt.Errorf("HTTP domain is already occupied by a conflicting tunnel"),
		protocol.TunnelMutationFieldDomain, protocol.TunnelMutationErrorCodeIngressResourceConflict, http.StatusConflict)
}

// Domain claims survive pause, disconnect and restart. Consult the durable
// reservation before looking at runtime readiness, so an unavailable specific
// tunnel cannot accidentally send its traffic into a broader wildcard tunnel.
func (s *TunnelStore) findHTTPDomainClaim(host string) (StoredTunnel, bool, error) {
	candidates := httpDomainCandidates(host)
	if len(candidates) == 0 {
		return StoredTunnel{}, false, nil
	}
	keys := make([]any, 0, len(candidates)*2)
	placeholders := make([]string, 0, len(candidates)*2)
	for _, candidate := range candidates {
		// Old exact-domain claims may retain a terminal dot. New writes use
		// canonical keys; accepting both avoids a migration for existing data.
		for _, domain := range []string{candidate, candidate + "."} {
			keys = append(keys, "ingress:server:http_host:"+domain)
			placeholders = append(placeholders, "?")
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT `+prefixedTunnelSelectColumns("t.")+`
		FROM tunnel_resource_locks l JOIN tunnels t ON t.id = l.tunnel_id
		WHERE l.resource_key IN (`+strings.Join(placeholders, ",")+`) ORDER BY t.id`, keys...)
	if err != nil {
		return StoredTunnel{}, false, err
	}
	defer func() { _ = rows.Close() }()
	var best StoredTunnel
	bestRank := len(candidates)
	for rows.Next() {
		stored, err := scanStoredTunnel(rows)
		if err != nil {
			return StoredTunnel{}, false, err
		}
		if stored.Ingress.Type != TunnelIngressTypeHTTPHost || stored.Ingress.Location != protocol.EndpointLocationServer {
			continue
		}
		domain := canonicalHTTPDomain(tunnelIngressDomain(stored))
		for rank, candidate := range candidates {
			if candidate == domain && rank < bestRank {
				best, bestRank = stored, rank
			}
		}
	}
	if err := rows.Err(); err != nil {
		return StoredTunnel{}, false, err
	}
	return best, bestRank < len(candidates), nil
}
