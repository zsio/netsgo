package server

import "testing"

func mustCreateSession(t testing.TB, store *AdminStore, userID, username, role, ip, ua string) *AdminSession {
	t.Helper()
	session, err := store.CreateSession(userID, username, role, ip, ua)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	return session
}

func mustDeleteSession(t testing.TB, store *AdminStore, sessionID string) {
	t.Helper()
	if err := store.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
}
