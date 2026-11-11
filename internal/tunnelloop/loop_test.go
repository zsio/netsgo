package tunnelloop

import (
	"encoding/json"
	"errors"
	"testing"

	"netsgo/pkg/protocol"
)

func TestProvenLoopAndLegitimateSamePort(t *testing.T) {
	for _, tc := range []struct {
		name, ingressID, targetID, bind, target string
		targetPort                              int
		blocked                                 bool
	}{
		{"same namespace wildcard", "same", "same", "0.0.0.0", "127.0.0.1", 13200, true},
		{"same namespace IPv6 wildcard", "same", "same", "0.0.0.0", "::1", 13200, true},
		{"specific address", "same", "same", "127.0.0.1", "127.0.0.1", 13200, true},
		{"different hosts", "server", "client", "0.0.0.0", "127.0.0.1", 13200, false},
		{"unknown old client", "server", "", "0.0.0.0", "127.0.0.1", 13200, false},
		{"unknown old server", "", "client", "0.0.0.0", "127.0.0.1", 13200, false},
		{"different port", "same", "same", "0.0.0.0", "127.0.0.1", 23200, false},
		{"different bind address", "same", "same", "127.0.0.1", "127.0.0.2", 13200, false},
		{"remote destination", "same", "same", "0.0.0.0", "192.0.2.234", 13200, false},
		{"hostname checked at dial", "same", "same", "0.0.0.0", "localhost", 13200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := json.Marshal(map[string]any{"bind_ip": tc.bind, "port": 13200})
			guard := New(protocol.TunnelSpec{Ingress: protocol.EndpointSpec{Type: protocol.IngressTypeTCPListen, Config: cfg}, Target: protocol.EndpointSpec{Type: protocol.TargetTypeTCPService}}, tc.ingressID, tc.targetID)
			if err := guard.CheckLiteral(tc.target, tc.targetPort); errors.Is(err, ErrLoop) != tc.blocked {
				t.Fatalf("got %v blocked=%v", err, tc.blocked)
			}
		})
	}
}
