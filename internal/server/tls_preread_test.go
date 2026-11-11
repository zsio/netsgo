package server

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type helloCaptureConn struct{ bytes.Buffer }

func (*helloCaptureConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (*helloCaptureConn) Close() error                     { return nil }
func (*helloCaptureConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*helloCaptureConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*helloCaptureConn) SetDeadline(time.Time) error      { return nil }
func (*helloCaptureConn) SetReadDeadline(time.Time) error  { return nil }
func (*helloCaptureConn) SetWriteDeadline(time.Time) error { return nil }

func captureClientHello(t testing.TB, name string, version uint16) []byte {
	t.Helper()
	conn := &helloCaptureConn{}
	client := tls.Client(conn, &tls.Config{ServerName: name, InsecureSkipVerify: true, MinVersion: version, MaxVersion: version})
	_ = client.Handshake()
	data := conn.Bytes()
	if len(data) < 5 {
		t.Fatal("TLS client did not write ClientHello")
	}
	length := int(binary.BigEndian.Uint16(data[3:5]))
	if len(data) < 5+length {
		t.Fatal("incomplete captured hello")
	}
	return bytes.Clone(data[5 : 5+length])
}

func helloRecord(payload []byte) []byte {
	record := []byte{22, 3, 3, byte(len(payload) >> 8), byte(len(payload))}
	return append(record, payload...)
}

func TestTLSPrereadPreservesFragmentedClientHello(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		for _, split := range []int{1, 4, 57} {
			hello := captureClientHello(t, "APP.example.test", version)
			wire := append(helloRecord(hello[:split]), helloRecord(hello[split:])...)
			wire = append(wire, []byte("coalesced bytes after hello")...)
			peer, raw := net.Pipe()
			go func() { _, _ = peer.Write(wire); _ = peer.Close() }()
			conn, name, isTLS, err := prereadTLS(raw)
			if err != nil {
				_ = raw.Close()
				t.Fatal(err)
			}
			got, err := io.ReadAll(conn)
			_ = conn.Close()
			if err != nil || !isTLS || name != "app.example.test" || !bytes.Equal(got, wire) {
				t.Fatalf("version=%x split=%d: name=%q TLS=%v bytes=%d/%d err=%v", version, split, name, isTLS, len(got), len(wire), err)
			}
		}
	}
}

func TestTLSPrereadHTTPAndNoSNI(t *testing.T) {
	for _, wire := range [][]byte{[]byte("GET /api/auth/me HTTP/1.1\r\nHost: localhost\r\n\r\n"), helloRecord(captureClientHello(t, "", tls.VersionTLS13))} {
		peer, raw := net.Pipe()
		go func() { _, _ = peer.Write(wire); _ = peer.Close() }()
		conn, name, isTLS, err := prereadTLS(raw)
		if err != nil {
			_ = raw.Close()
			t.Fatal(err)
		}
		got, err := io.ReadAll(conn)
		_ = conn.Close()
		if err != nil || name != "" || isTLS != (wire[0] == 22) || !bytes.Equal(wire, got) {
			t.Fatalf("unexpected preread result: name=%q TLS=%v err=%v", name, isTLS, err)
		}
	}
}

func TestTLSPrereadRejectsMalformedAndOversizedHellos(t *testing.T) {
	hello := helloRecord(captureClientHello(t, "app.example.test", tls.VersionTLS13))
	for _, wire := range [][]byte{
		{22, 3, 3, 255, 255},
		helloRecord([]byte{2, 0, 0, 0}),
		helloRecord([]byte{1, 255, 255, 255}),
		helloRecord([]byte{1, 0, 0, 1, 0}),
		hello[:len(hello)-3],
	} {
		peer, raw := net.Pipe()
		go func() { _, _ = peer.Write(wire); _ = peer.Close() }()
		_, _, _, err := prereadTLS(raw)
		_ = raw.Close()
		if err == nil {
			t.Fatalf("malformed hello accepted: %x", wire[:min(12, len(wire))])
		}
	}
}

func TestSNIListenerCloseUnblocksPendingHelloAndAccept(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newSNIListener(raw, func(net.Conn, string) bool { return false })
	peer, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	_, _ = peer.Write([]byte{22})
	closed := make(chan struct{})
	go func() { _ = listener.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("listener close blocked on partial ClientHello")
	}
	if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept error=%v", err)
	}
}

func TestSNIListenerCloseDoesNotWaitForForwardingRoute(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	listener := newSNIListener(raw, func(net.Conn, string) bool {
		close(entered)
		// Closing the socket cannot interrupt non-I/O work such as a
		// bandwidth reservation. Server.Shutdown(ctx) bounds its own wait.
		<-release
		close(finished)
		return true
	})
	defer func() {
		close(release)
		_ = listener.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("forwarding route did not finish after release")
		}
	}()
	peer, err := net.DialTimeout("tcp", raw.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	if _, err := peer.Write(helloRecord(captureClientHello(t, "app.example.test", tls.VersionTLS13))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("forwarding route did not start")
	}
	closed := make(chan struct{})
	go func() { _ = listener.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("listener close waited for forwarding work")
	}
}

type resourceLimitedListener struct {
	net.Listener
	failed atomic.Bool
}

func (l *resourceLimitedListener) Accept() (net.Conn, error) {
	if l.failed.CompareAndSwap(false, true) {
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	}
	return l.Listener.Accept()
}

func TestSNIListenerRetriesResourceExhaustion(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newSNIListener(&resourceLimitedListener{Listener: raw}, func(net.Conn, string) bool { return false })
	defer func() { _ = listener.Close() }()
	peer, err := net.DialTimeout("tcp", raw.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	_, _ = peer.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if conn != nil {
			_ = conn.Close()
		}
		accepted <- err
	}()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("listener stopped after transient accept failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not recover after resource exhaustion")
	}
}

func FuzzClientHelloServerName(f *testing.F) {
	f.Add(captureClientHello(f, "app.example.test", tls.VersionTLS13)[4:])
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = clientHelloServerName(data) })
}
