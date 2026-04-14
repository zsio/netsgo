package mux

import (
	"io"
	"net"
	"testing"
	"time"
)

func mustClose(t testing.TB, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func mustSetReadDeadline(t testing.TB, conn net.Conn, deadline time.Time) {
	t.Helper()
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline failed: %v", err)
	}
}
