package server

import (
	"bytes"
	"testing"
)

func TestP2PActivityUsesStableReasonAndDedupeKey(t *testing.T) {
	secret := "candidate 192.0.2.10:4321 password=secret"
	result := p2pLifecycleResult{
		Session:    p2pSessionSnapshot{SessionID: "session-1", ClientA: "a", ClientB: "b", Grants: []p2pGrantSnapshot{{TunnelID: "t1", Revision: 1}}},
		FailedEdge: true, ActivityActions: map[string][]p2pGrantSnapshot{"failed": {{TunnelID: "t1", Revision: 1}}}, ReasonCode: normalizeP2PCloseReason(secret, "failed"), Sequence: 9,
	}
	specs := p2pActivitySpecs(result)
	if len(specs) != 1 || specs[0].DedupeKey != "p2p:session-1:failed:9" {
		t.Fatalf("P2P fact = %+v", specs)
	}
	payload, err := specs[0].Payload.activityPayloadJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(secret)) || bytes.Contains(payload, []byte("192.0.2.10")) || bytes.Contains(payload, []byte("password")) {
		t.Fatalf("P2P payload leaked raw reason: %s", payload)
	}
}

func TestP2PAttachedAndDetachedUseSingleTunnelSubject(t *testing.T) {
	result := p2pLifecycleResult{
		Session: p2pSessionSnapshot{SessionID: "session-1", ClientA: "a", ClientB: "b", Grants: []p2pGrantSnapshot{{TunnelID: "t1"}, {TunnelID: "t2"}}},
		Grant:   p2pGrantSnapshot{TunnelID: "t2"}, HasGrant: true, GrantCreated: true, Sequence: 2,
	}
	spec := p2pActivitySpecs(result)[0]
	if len(spec.Tunnels) != 1 || spec.Tunnels[0].TunnelID != "t2" || spec.Tunnels[0].Relation != "subject" {
		t.Fatalf("attached subjects = %+v", spec.Tunnels)
	}
}
