package client

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"netsgo/pkg/mux"
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

func newTestYamuxSessionPair(t testing.TB) (clientSession, serverSession *yamux.Session) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	clientSession, err := mux.NewClientSession(clientConn, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("create client yamux session: %v", err)
	}
	serverSession, err = mux.NewServerSession(serverConn, mux.DefaultConfig())
	if err != nil {
		_ = clientSession.Close()
		t.Fatalf("create server yamux session: %v", err)
	}
	return clientSession, serverSession
}

func waitForClientCondition(t testing.TB, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if condition() {
		return
	}
	t.Fatalf("condition not met within %s", timeout)
}

func replaceFetchPublicIPs(fn func() (string, string)) func() {
	previous := fetchPublicIPs
	fetchPublicIPs = fn
	return func() {
		fetchPublicIPs = previous
	}
}
