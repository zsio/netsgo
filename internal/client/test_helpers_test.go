package client

import (
	"fmt"
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

func mustWriteAll(t testing.TB, conn net.Conn, data []byte) {
	t.Helper()
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}

func mustSetDeadline(t testing.TB, conn net.Conn, deadline time.Time) {
	t.Helper()
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline failed: %v", err)
	}
}

func mustParseLoopbackPort(t testing.TB, addr string) int {
	t.Helper()
	var port int
	if _, err := fmt.Sscanf(addr, "127.0.0.1:%d", &port); err != nil {
		t.Fatalf("parse loopback port failed: %v", err)
	}
	return port
}
