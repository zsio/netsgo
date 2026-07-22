package server

import (
	"testing"
	"time"
)

func TestTouchClientTokenIfDueThrottlesSuccessfulRefreshes(t *testing.T) {
	store := newTestAdminStore(t)
	rawKey := "sk-control-touch"
	if _, err := store.AddAPIKey("test", rawKey, []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey failed: %v", err)
	}
	_, token, err := store.ExchangeToken(rawKey, "install-touch", "client-touch", "192.0.2.10:4321")
	if err != nil {
		t.Fatalf("ExchangeToken failed: %v", err)
	}
	oldActivity := time.Now().Add(-6 * 24 * time.Hour)
	if _, err := store.db.Exec(`UPDATE client_tokens SET last_active_at = ? WHERE id = ?`, formatTime(oldActivity), token.ID); err != nil {
		t.Fatalf("age token: %v", err)
	}

	s := New(0)
	s.auth.adminStore = store
	now := time.Now()
	client := &ClientConn{
		ID:             "client-touch",
		RemoteAddr:     "198.51.100.25:9000",
		clientTokenID:  token.ID,
		nextTokenTouch: now,
	}

	s.touchClientTokenIfDue(client, now)
	first := loadSingleClientToken(t, store, token.ID)
	if first.LastActiveAt.Before(now) {
		t.Fatalf("first LastActiveAt = %s, want >= %s", first.LastActiveAt, now)
	}
	if first.LastIP != "198.51.100.25" {
		t.Fatalf("first LastIP = %q, want 198.51.100.25", first.LastIP)
	}
	if want := now.Add(clientTokenTouchInterval); !client.nextTokenTouch.Equal(want) {
		t.Fatalf("nextTokenTouch = %s, want %s", client.nextTokenTouch, want)
	}

	if _, err := store.db.Exec(`UPDATE client_tokens SET last_active_at = ? WHERE id = ?`, formatTime(oldActivity), token.ID); err != nil {
		t.Fatalf("re-age token: %v", err)
	}
	s.touchClientTokenIfDue(client, now.Add(time.Minute))
	second := loadSingleClientToken(t, store, token.ID)
	if !second.LastActiveAt.Equal(oldActivity.UTC()) {
		t.Fatalf("throttled LastActiveAt = %s, want %s", second.LastActiveAt, oldActivity.UTC())
	}
}

func loadSingleClientToken(t *testing.T, store *AdminStore, tokenID string) ClientToken {
	t.Helper()
	tokens, err := loadClientTokens(store.db, `WHERE id = ?`, tokenID)
	if err != nil {
		t.Fatalf("load token: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("token count = %d, want 1", len(tokens))
	}
	return tokens[0]
}
