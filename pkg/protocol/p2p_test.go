package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestP2PSignalValidate(t *testing.T) {
	validOffer := P2PSignal{SessionID: "session", Sequence: 1, Kind: P2PSignalOffer, SDP: "v=0"}
	tests := []struct {
		name   string
		signal P2PSignal
		ok     bool
	}{
		{name: "offer", signal: validOffer, ok: true},
		{name: "answer", signal: P2PSignal{SessionID: "session", Sequence: 2, Kind: P2PSignalAnswer, SDP: "v=0"}, ok: true},
		{name: "candidate", signal: P2PSignal{SessionID: "session", Sequence: 3, Kind: P2PSignalCandidate, Candidate: "candidate:1", SDPMid: "0"}, ok: true},
		{name: "missing session", signal: P2PSignal{Sequence: 1, Kind: P2PSignalOffer, SDP: "v=0"}},
		{name: "zero sequence", signal: P2PSignal{SessionID: "session", Kind: P2PSignalOffer, SDP: "v=0"}},
		{name: "oversized sdp", signal: P2PSignal{SessionID: "session", Sequence: 1, Kind: P2PSignalOffer, SDP: strings.Repeat("x", P2PMaxSDPLen+1)}},
		{name: "candidate carrying sdp", signal: P2PSignal{SessionID: "session", Sequence: 1, Kind: P2PSignalCandidate, Candidate: "candidate:1", SDP: "v=0"}},
		{name: "unknown kind", signal: P2PSignal{SessionID: "session", Sequence: 1, Kind: "future"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.signal.Validate() == nil; got != tt.ok {
				t.Fatalf("valid=%v want=%v", got, tt.ok)
			}
		})
	}
}

func TestP2PSessionPrepareValidatesBoundGrant(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	grant := P2PTunnelGrant{SessionID: "session", GrantID: "grant", TunnelID: "tunnel", Revision: 2, LocalRole: DataStreamRoleIngress, PeerRole: DataStreamRoleTarget, LeaseSequence: 1, ExpiresAt: now.Add(time.Minute)}
	prepare := P2PSessionPrepare{SessionID: "session", PeerClientID: "peer", Role: P2PRoleOfferer, LocalGeneration: 1, PeerGeneration: 2, LeaseSequence: 1, ExpiresAt: now.Add(time.Minute), Grants: []P2PTunnelGrant{grant}}
	if err := prepare.Validate(now); err != nil {
		t.Fatalf("valid prepare rejected: %v", err)
	}
	prepare.Grants[0].SessionID = "other"
	if err := prepare.Validate(now); err == nil {
		t.Fatal("mismatched grant session accepted")
	}
}

func TestP2PTunnelGrantRejectsExpiredAndSameRole(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	grant := P2PTunnelGrant{SessionID: "session", GrantID: "grant", TunnelID: "tunnel", Revision: 2, LocalRole: DataStreamRoleIngress, PeerRole: DataStreamRoleTarget, LeaseSequence: 1, ExpiresAt: now.Add(time.Minute)}
	if err := grant.Validate(now); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	grant.ExpiresAt = now
	if err := grant.Validate(now); err == nil {
		t.Fatal("expired grant accepted")
	}
	grant.ExpiresAt = now.Add(time.Minute)
	grant.PeerRole = grant.LocalRole
	if err := grant.Validate(now); err == nil {
		t.Fatal("same-role grant accepted")
	}
}

func TestP2PStatsReportValidate(t *testing.T) {
	report := P2PStatsReport{SessionID: "session", GrantID: "grant", TunnelID: "tunnel", Revision: 1, Epoch: "epoch", Sequence: 1}
	if err := report.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	report.Sequence = 0
	if err := report.Validate(); err == nil {
		t.Fatal("zero sequence accepted")
	}
}

func TestP2PCreditMessagesValidate(t *testing.T) {
	demand := P2PCreditDemand{SessionID: "s", GrantID: "g", TunnelID: "t", Revision: 1, Sequence: 1, DesiredBytes: 10}
	if err := demand.Validate(); err != nil {
		t.Fatalf("valid demand rejected: %v", err)
	}
	grant := P2PCreditGrant{SessionID: "s", GrantID: "g", TunnelID: "t", Revision: 1, Sequence: 1, GrantedBytes: 10}
	if err := grant.Validate(); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	grant.GrantedBytes = 0
	if err := grant.Validate(); err == nil {
		t.Fatal("zero credit grant accepted")
	}
}

func TestP2PSessionStatusValidate(t *testing.T) {
	status := P2PSessionStatus{SessionID: "session", Sequence: 1, State: P2PStateConnected}
	if err := status.Validate(); err != nil {
		t.Fatalf("valid status rejected: %v", err)
	}
	status.Sequence = 0
	if err := status.Validate(); err == nil {
		t.Fatal("zero sequence accepted")
	}
	status.Sequence = 1
	status.State = "invented"
	if err := status.Validate(); err == nil {
		t.Fatal("unknown state accepted")
	}
	status.State = P2PStateFailed
	status.Error = strings.Repeat("x", P2PMaxStatusErrorLen+1)
	if err := status.Validate(); err == nil {
		t.Fatal("oversized error accepted")
	}
}
