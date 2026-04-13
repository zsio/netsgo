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
