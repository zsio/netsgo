package client

import (
	"bytes"
	"encoding/binary"
	"fmt"
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
	// 1. Start a local mock backend service
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "ok")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend data"))
	}))
	defer backend.Close()

	// Parse the backend port
	var localPort int
	fmt.Sscanf(backend.Listener.Addr().String(), "127.0.0.1:%d", &localPort)

	// 2. Initialize the client
	c := New("ws://localhost:8080", "key")
	proxyName := "test-backend"
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		Name:      proxyName,
		LocalIP:   "127.0.0.1",
		LocalPort: localPort,
	})

	// 3. Build a pair of connected pipes to simulate the Server -> Client data channel (yamux)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// Initialize the client-side data session
	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession
	defer clientSession.Close()

	// Initialize the server-side data session
	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	defer serverSession.Close()

	// 4. Have the server actively open a stream to simulate incoming external traffic
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

		// Write the header into the stream (2-byte length + name)
		nameBytes := []byte(proxyName)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
		serverStream.Write(lenBuf[:])
		serverStream.Write(nameBytes)

		// Then send actual HTTP request data
		reqData := []byte("GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
		serverStream.Write(reqData)
	}()

	// 5. Let the client accept the stream and handle it
	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("Client AcceptStream failed: %v", err)
	}

	// Execute the core logic under test: handleStream
	// It should read proxyName, find the config, dial the backend service, and relay data in both directions
	var relayWg sync.WaitGroup
	relayWg.Add(1)
	go func() {
		defer relayWg.Done()
		c.handleStream(clientStream)
	}()

	// 6. Wait for the server-side stream write to finish, then read the response
	wg.Wait()
	if streamErr != nil {
		t.Fatalf("Server OpenStream failed: %v", streamErr)
	}

	// Read the backend response from the server-side stream
	respBuf := make([]byte, 1024)
	serverStream.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := serverStream.Read(respBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read backend response: %v", err)
	}
	serverStream.Close()

	// Verify that the backend handled the request and returned 200 OK
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
		_, _ = w.Write([]byte("http tunnel backend"))
	}))
	defer backend.Close()

	var localPort int
	fmt.Sscanf(backend.Listener.Addr().String(), "127.0.0.1:%d", &localPort)

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
	defer clientConn.Close()
	defer serverConn.Close()

	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession
	defer clientSession.Close()

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	defer serverSession.Close()

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
		_, _ = serverStream.Write(lenBuf[:])
		_, _ = serverStream.Write(nameBytes)
		_, _ = serverStream.Write([]byte("GET / HTTP/1.1\r\nHost: app.example.com\r\n\r\n"))
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
	_ = serverStream.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := serverStream.Read(respBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read HTTP tunnel response: %v", err)
	}
	_ = serverStream.Close()

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
	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())

	go func() {
		stream, _ := serverSession.Open()
		// Send an invalid length
		stream.Write([]byte{0x00, 0x00})
		stream.Close()
	}()

	stream, _ := clientSession.AcceptStream()

	// If it exits without crashing, the defensive validation worked
	c.handleStream(stream)

	clientConn.Close()
	serverConn.Close()
}

func TestClient_HandleStream_DialFail(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	proxyName := "fail-proxy"
	// Set a port that is guaranteed not to connect
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		Name:      proxyName,
		LocalIP:   "127.0.0.1",
		LocalPort: 99999, // invalid port / no listener
	})

	clientConn, serverConn := net.Pipe()
	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, _ := serverSession.Open()
		nameBytes := []byte(proxyName)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
		stream.Write(lenBuf[:])
		stream.Write(nameBytes)

		// The read should hit EOF because the stream is closed immediately when dial fails on the other side
		buf := make([]byte, 10)
		_, err := stream.Read(buf)
		if err == nil {
			t.Error("expected an error or EOF when the target refused the connection")
		}
		stream.Close()
	}()

	stream, _ := clientSession.AcceptStream()
	c.handleStream(stream)

	wg.Wait()
	clientConn.Close()
	serverConn.Close()
}
