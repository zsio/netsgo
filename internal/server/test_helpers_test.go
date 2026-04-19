package server

import (
	"encoding/json"
	"io"
	"testing"
	"time"
)

func mustClose(t testing.TB, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

type readDeadliner interface {
	SetReadDeadline(time.Time) error
}

func mustSetReadDeadline(t testing.TB, conn readDeadliner, deadline time.Time) {
	t.Helper()
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline failed: %v", err)
	}
}

func mustDecodeJSON(t testing.TB, reader io.Reader, value any) error {
	t.Helper()
	return json.NewDecoder(reader).Decode(value)
}

func waitForTunnelChangedEvent(t testing.TB, ch <-chan SSEEvent, action, tunnelName string) map[string]any {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case event := <-ch:
			if event.Type != "tunnel_changed" {
				continue
			}

			var payload map[string]any
			if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
				t.Fatalf("failed to parse tunnel_changed event: %v", err)
			}

			gotAction, _ := payload["action"].(string)
			if gotAction != action {
				continue
			}

			tunnelPayload, ok := payload["tunnel"].(map[string]any)
			if !ok {
				t.Fatalf("tunnel_changed.tunnel has invalid type: %#v", payload["tunnel"])
			}

			if tunnelName != "" {
				gotName, _ := tunnelPayload["name"].(string)
				if gotName != tunnelName {
					continue
				}
			}

			return tunnelPayload
		case <-time.After(20 * time.Millisecond):
		}
	}

	t.Fatalf("did not receive tunnel_changed event for action=%q tunnel=%q", action, tunnelName)
	return nil
}

func assertTunnelBandwidthFields(t testing.TB, tunnelPayload map[string]any, ingress, egress int64) {
	t.Helper()

	if tunnelPayload["ingress_bps"] != float64(ingress) {
		t.Fatalf("ingress_bps: want %d, got %v", ingress, tunnelPayload["ingress_bps"])
	}
	if tunnelPayload["egress_bps"] != float64(egress) {
		t.Fatalf("egress_bps: want %d, got %v", egress, tunnelPayload["egress_bps"])
	}
}

func mustCreateSession(t testing.TB, store *AdminStore, userID, username, role, remoteAddr, userAgent string) *AdminSession {
	t.Helper()
	session, err := store.CreateSession(userID, username, role, remoteAddr, userAgent)
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	return session
}

func mustDeleteSession(t testing.TB, store *AdminStore, sessionID string) {
	t.Helper()
	if err := store.DeleteSession(sessionID); err != nil {
		t.Fatalf("delete session failed: %v", err)
	}
}
