package protocol

import (
	"fmt"
	"strings"
	"time"
)

const (
	P2PSignalOffer     = "offer"
	P2PSignalAnswer    = "answer"
	P2PSignalCandidate = "candidate"

	P2PRoleOfferer  = "offerer"
	P2PRoleAnswerer = "answerer"

	P2PMaxSessionIDLen   = 128
	P2PMaxClientIDLen    = 1024
	P2PMaxSDPLen         = 256 * 1024
	P2PMaxCandidateLen   = 16 * 1024
	P2PMaxMIDLen         = 256
	P2PMaxStatusErrorLen = 4096
	P2PMaxGrants         = 4096
	P2PMaxCandidates     = 256
)

// P2PSessionPrepare authorizes one client to participate in a short-lived
// pair session. Peer identity comes from the authenticated control channel;
// clients must never trust a self-declared identity from forwarded signaling.
type P2PSessionPrepare struct {
	SessionID       string           `json:"session_id"`
	PeerClientID    string           `json:"peer_client_id"`
	Role            string           `json:"role"`
	LocalGeneration uint64           `json:"local_generation"`
	PeerGeneration  uint64           `json:"peer_generation"`
	LeaseSequence   uint64           `json:"lease_sequence"`
	ExpiresAt       time.Time        `json:"expires_at"`
	ICEServers      []P2PICEServer   `json:"ice_servers,omitempty"`
	Grants          []P2PTunnelGrant `json:"grants,omitempty"`
}

type P2PICEServer struct {
	URLs []string `json:"urls"`
}

// P2PSignal is forwarded by Server only after matching the authenticated
// sender, current generations, pair session and lease.
type P2PSignal struct {
	SessionID string `json:"session_id"`
	Sequence  uint64 `json:"sequence"`
	Kind      string `json:"kind"`
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	SDPMid    string `json:"sdp_mid,omitempty"`
	MLine     uint16 `json:"mline,omitempty"`
}

type P2PSessionStatus struct {
	SessionID string `json:"session_id"`
	Sequence  uint64 `json:"sequence"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
}

func (s P2PSessionStatus) Validate() error {
	if err := validateP2PIdentity("session_id", s.SessionID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if s.Sequence == 0 {
		return fmt.Errorf("p2p status sequence must be positive")
	}
	if s.State != P2PStateConnected && s.State != P2PStateFailed && s.State != P2PStateClosed {
		return fmt.Errorf("unsupported p2p status state %q", s.State)
	}
	if len(s.Error) > P2PMaxStatusErrorLen {
		return fmt.Errorf("p2p status error is too long")
	}
	return nil
}

type P2PLease struct {
	SessionID string    `json:"session_id"`
	Sequence  uint64    `json:"sequence"`
	ExpiresAt time.Time `json:"expires_at"`
}

type P2PTunnelGrant struct {
	SessionID     string    `json:"session_id"`
	GrantID       string    `json:"grant_id"`
	TunnelID      string    `json:"tunnel_id"`
	Revision      int64     `json:"revision"`
	LocalRole     string    `json:"local_role"`
	PeerRole      string    `json:"peer_role"`
	LeaseSequence uint64    `json:"lease_sequence"`
	ExpiresAt     time.Time `json:"expires_at"`
	TotalBPS      int64     `json:"total_bps,omitempty"`
}

type P2PTunnelRevoke struct {
	SessionID string `json:"session_id"`
	GrantID   string `json:"grant_id"`
	TunnelID  string `json:"tunnel_id"`
	Revision  int64  `json:"revision"`
	Reason    string `json:"reason,omitempty"`
}

// P2PStatsReport contains owner-observed cumulative payload counters. Server
// derives deltas from monotonically increasing reports so control retries are
// idempotent. Transport framing and handshake bytes are excluded.
type P2PStatsReport struct {
	SessionID    string `json:"session_id"`
	GrantID      string `json:"grant_id"`
	TunnelID     string `json:"tunnel_id"`
	Revision     int64  `json:"revision"`
	Epoch        string `json:"epoch"`
	Sequence     uint64 `json:"sequence"`
	IngressBytes uint64 `json:"ingress_bytes"`
	EgressBytes  uint64 `json:"egress_bytes"`
}

type P2PCreditDemand struct {
	SessionID    string `json:"session_id"`
	GrantID      string `json:"grant_id"`
	TunnelID     string `json:"tunnel_id"`
	Revision     int64  `json:"revision"`
	Sequence     uint64 `json:"sequence"`
	DesiredBytes uint64 `json:"desired_bytes"`
}

type P2PCreditGrant struct {
	SessionID    string `json:"session_id"`
	GrantID      string `json:"grant_id"`
	TunnelID     string `json:"tunnel_id"`
	Revision     int64  `json:"revision"`
	Sequence     uint64 `json:"sequence"`
	GrantedBytes uint64 `json:"granted_bytes"`
}

func (d P2PCreditDemand) Validate() error {
	if err := validateP2PCreditIdentity(d.SessionID, d.GrantID, d.TunnelID, d.Revision, d.Sequence); err != nil {
		return err
	}
	if d.DesiredBytes == 0 {
		return fmt.Errorf("p2p credit desired_bytes must be positive")
	}
	return nil
}

func (g P2PCreditGrant) Validate() error {
	if err := validateP2PCreditIdentity(g.SessionID, g.GrantID, g.TunnelID, g.Revision, g.Sequence); err != nil {
		return err
	}
	if g.GrantedBytes == 0 {
		return fmt.Errorf("p2p credit granted_bytes must be positive")
	}
	return nil
}

func validateP2PCreditIdentity(sessionID, grantID, tunnelID string, revision int64, sequence uint64) error {
	if err := validateP2PIdentity("session_id", sessionID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if err := validateP2PIdentity("grant_id", grantID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if err := validateP2PIdentity("tunnel_id", tunnelID, DataStreamHeaderMaxStringLen); err != nil {
		return err
	}
	if revision <= 0 {
		return fmt.Errorf("p2p credit revision must be positive")
	}
	if sequence == 0 {
		return fmt.Errorf("p2p credit sequence must be positive")
	}
	return nil
}

func (r P2PStatsReport) Validate() error {
	if err := validateP2PIdentity("session_id", r.SessionID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if err := validateP2PIdentity("grant_id", r.GrantID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if err := validateP2PIdentity("tunnel_id", r.TunnelID, DataStreamHeaderMaxStringLen); err != nil {
		return err
	}
	if err := validateP2PIdentity("epoch", r.Epoch, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if r.Revision <= 0 {
		return fmt.Errorf("p2p stats revision must be positive")
	}
	if r.Sequence == 0 {
		return fmt.Errorf("p2p stats sequence must be positive")
	}
	return nil
}

func (p P2PSessionPrepare) Validate(now time.Time) error {
	if err := validateP2PIdentity("session_id", p.SessionID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if err := validateP2PIdentity("peer_client_id", p.PeerClientID, P2PMaxClientIDLen); err != nil {
		return err
	}
	if p.Role != P2PRoleOfferer && p.Role != P2PRoleAnswerer {
		return fmt.Errorf("unsupported p2p role %q", p.Role)
	}
	if p.LocalGeneration == 0 || p.PeerGeneration == 0 {
		return fmt.Errorf("p2p generations must be positive")
	}
	if p.LeaseSequence == 0 {
		return fmt.Errorf("p2p lease sequence must be positive")
	}
	if !p.ExpiresAt.After(now) {
		return fmt.Errorf("p2p session lease is expired")
	}
	if len(p.Grants) > P2PMaxGrants {
		return fmt.Errorf("too many p2p tunnel grants")
	}
	for _, grant := range p.Grants {
		if err := grant.Validate(now); err != nil {
			return err
		}
		if grant.SessionID != p.SessionID {
			return fmt.Errorf("p2p grant session mismatch")
		}
	}
	return nil
}

func (s P2PSignal) Validate() error {
	if err := validateP2PIdentity("session_id", s.SessionID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if s.Sequence == 0 {
		return fmt.Errorf("p2p signal sequence must be positive")
	}
	switch s.Kind {
	case P2PSignalOffer, P2PSignalAnswer:
		if strings.TrimSpace(s.SDP) == "" || len(s.SDP) > P2PMaxSDPLen {
			return fmt.Errorf("invalid p2p session description")
		}
		if s.Candidate != "" {
			return fmt.Errorf("candidate is not allowed in p2p description signal")
		}
	case P2PSignalCandidate:
		if strings.TrimSpace(s.Candidate) == "" || len(s.Candidate) > P2PMaxCandidateLen {
			return fmt.Errorf("invalid p2p candidate")
		}
		if len(s.SDPMid) > P2PMaxMIDLen {
			return fmt.Errorf("p2p candidate sdp_mid is too long")
		}
		if s.SDP != "" {
			return fmt.Errorf("sdp is not allowed in p2p candidate signal")
		}
	default:
		return fmt.Errorf("unsupported p2p signal kind %q", s.Kind)
	}
	return nil
}

func (g P2PTunnelGrant) Validate(now time.Time) error {
	if err := validateP2PIdentity("session_id", g.SessionID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if err := validateP2PIdentity("grant_id", g.GrantID, P2PMaxSessionIDLen); err != nil {
		return err
	}
	if err := validateP2PIdentity("tunnel_id", g.TunnelID, DataStreamHeaderMaxStringLen); err != nil {
		return err
	}
	if g.Revision <= 0 {
		return fmt.Errorf("p2p tunnel revision must be positive")
	}
	if g.LocalRole == g.PeerRole || !isP2PTunnelRole(g.LocalRole) || !isP2PTunnelRole(g.PeerRole) {
		return fmt.Errorf("invalid p2p tunnel roles local=%q peer=%q", g.LocalRole, g.PeerRole)
	}
	if g.LeaseSequence == 0 {
		return fmt.Errorf("p2p grant lease sequence must be positive")
	}
	if g.TotalBPS < 0 {
		return fmt.Errorf("p2p total_bps must be non-negative")
	}
	if !g.ExpiresAt.After(now) {
		return fmt.Errorf("p2p tunnel grant is expired")
	}
	return nil
}

func validateP2PIdentity(name, value string, limit int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("p2p %s is required", name)
	}
	if len(value) > limit {
		return fmt.Errorf("p2p %s is too long", name)
	}
	return nil
}

func isP2PTunnelRole(role string) bool {
	return role == DataStreamRoleIngress || role == DataStreamRoleTarget
}
