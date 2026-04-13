package client

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

func TestClient_HandleStream_Success(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "ok")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("backend data")); err != nil {
			t.Fatalf("write backend response failed: %v", err)
		}
	}))
	defer backend.Close()

	localPort := mustParseLoopbackPort(t, backend.Listener.Addr().String())

	c := New("ws://localhost:8080", "key")
	proxyName := "test-backend"
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		Name:      proxyName,
		LocalIP:   "127.0.0.1",
		LocalPort: localPort,
	})

	clientConn, serverConn := net.Pipe()
	defer mustClose(t, clientConn)
	defer mustClose(t, serverConn)

	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession
	defer mustClose(t, clientSession)

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	defer mustClose(t, serverSession)

	var serverStream net.Conn
	var streamErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serverStream, streamErr = serverSession.Open()
		if streamErr != nil {
			return
		}

		nameBytes := []byte(proxyName)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
		mustWriteAll(t, serverStream, lenBuf[:])
		mustWriteAll(t, serverStream, nameBytes)
		mustWriteAll(t, serverStream, []byte("GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"))
	}()

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("Client AcceptStream failed: %v", err)
	}

	var relayWg sync.WaitGroup
	relayWg.Add(1)
	go func() {
		defer relayWg.Done()
		c.handleStream(clientStream)
	}()

	wg.Wait()
	if streamErr != nil {
		t.Fatalf("Server OpenStream failed: %v", streamErr)
	}

	respBuf := make([]byte, 1024)
	mustSetDeadline(t, serverStream, time.Now().Add(2*time.Second))
	n, err := serverStream.Read(respBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read backend response: %v", err)
	}
	mustClose(t, serverStream)

	responseStr := string(respBuf[:n])
	if !bytes.Contains([]byte(responseStr), []byte("200 OK")) {
		t.Errorf("expected HTTP 200 OK, got: %s", responseStr)
	}
	if !bytes.Contains([]byte(responseStr), []byte("X-Backend: ok")) {
		t.Errorf("expected header not found: X-Backend")
	}

	relayWg.Wait()
}

func TestClient_HandleStream_HTTPProxy_ReusesTCPPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-HTTP-Tunnel", "ok")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("http tunnel backend")); err != nil {
			t.Fatalf("write http tunnel backend response failed: %v", err)
		}
	}))
	defer backend.Close()

	localPort := mustParseLoopbackPort(t, backend.Listener.Addr().String())

	c := New("ws://localhost:8080", "key")
	proxyName := "http-backend"
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		Name:      proxyName,
		Type:      protocol.ProxyTypeHTTP,
		LocalIP:   "127.0.0.1",
		LocalPort: localPort,
		Domain:    "app.example.com",
	})

	clientConn, serverConn := net.Pipe()
	defer mustClose(t, clientConn)
	defer mustClose(t, serverConn)

	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession
	defer mustClose(t, clientSession)

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	defer mustClose(t, serverSession)

	var serverStream net.Conn
	var streamErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serverStream, streamErr = serverSession.Open()
		if streamErr != nil {
			return
		}

		nameBytes := []byte(proxyName)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
		mustWriteAll(t, serverStream, lenBuf[:])
		mustWriteAll(t, serverStream, nameBytes)
		mustWriteAll(t, serverStream, []byte("GET / HTTP/1.1\r\nHost: app.example.com\r\n\r\n"))
	}()

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("Client AcceptStream failed: %v", err)
	}

	var relayWg sync.WaitGroup
	relayWg.Add(1)
	go func() {
		defer relayWg.Done()
		c.handleStream(clientStream)
	}()

	wg.Wait()
	if streamErr != nil {
		t.Fatalf("Server OpenStream failed: %v", streamErr)
	}

	respBuf := make([]byte, 1024)
	mustSetDeadline(t, serverStream, time.Now().Add(2*time.Second))
	n, err := serverStream.Read(respBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read HTTP tunnel response: %v", err)
	}
	mustClose(t, serverStream)

	responseStr := string(respBuf[:n])
	if !bytes.Contains([]byte(responseStr), []byte("200 OK")) {
		t.Fatalf("expected HTTP 200 OK, got: %s", responseStr)
	}
	if !bytes.Contains([]byte(responseStr), []byte("X-Http-Tunnel: ok")) {
		t.Fatalf("HTTP tunnels should continue to reuse the TCP data plane, actual response: %s", responseStr)
	}

	relayWg.Wait()
}

func TestClient_HandleStream_InvalidHeader(t *testing.T) {
	c := New("ws://localhost:8080", "key")

	clientConn, serverConn := net.Pipe()
	defer mustClose(t, clientConn)
	defer mustClose(t, serverConn)

	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession
	defer mustClose(t, clientSession)

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	defer mustClose(t, serverSession)

	go func() {
		stream, _ := serverSession.Open()
		mustWriteAll(t, stream, []byte{0x00, 0x00})
		mustClose(t, stream)
	}()

	stream, _ := clientSession.AcceptStream()
	c.handleStream(stream)
}

func TestClient_HandleStream_DialFail(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	proxyName := "fail-proxy"
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		Name:      proxyName,
		LocalIP:   "127.0.0.1",
		LocalPort: 99999,
	})

	clientConn, serverConn := net.Pipe()
	defer mustClose(t, clientConn)
	defer mustClose(t, serverConn)

	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession
	defer mustClose(t, clientSession)

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	defer mustClose(t, serverSession)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, _ := serverSession.Open()
		nameBytes := []byte(proxyName)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
		mustWriteAll(t, stream, lenBuf[:])
		mustWriteAll(t, stream, nameBytes)

		buf := make([]byte, 10)
		_, err := stream.Read(buf)
		if err == nil {
			t.Error("expected an error or EOF when the target refused the connection")
		}
		mustClose(t, stream)
	}()

	stream, _ := clientSession.AcceptStream()
	c.handleStream(stream)

	wg.Wait()
}
