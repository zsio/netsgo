package client

import (
	"bytes"
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

func encodeDataStreamHeader(t *testing.T, header protocol.DataStreamHeader) []byte {
	t.Helper()
	payload, err := protocol.WriteDataStreamHeaderToBytes(header)
	if err != nil {
		t.Fatalf("failed to encode data stream header: %v", err)
	}
	return payload
}

func testDataStreamHeader(tunnelID string) protocol.DataStreamHeader {
	return protocol.DataStreamHeader{
		Kind:             protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:         tunnelID,
		Revision:         1,
		StreamID:         "stream-" + tunnelID,
		OpenClientID:     "server",
		SourceRole:       protocol.DataStreamRoleServer,
		TargetRole:       protocol.DataStreamRoleTarget,
		Direction:        protocol.DataStreamDirectionIngressToTarget,
		Transport:        protocol.ActualTransportServerRelay,
		ServerAuthorized: true,
	}
}

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
		ID:        proxyName,
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

		mustWriteAll(t, serverStream, encodeDataStreamHeader(t, testDataStreamHeader(proxyName)))
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
		ID:        proxyName,
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

		mustWriteAll(t, serverStream, encodeDataStreamHeader(t, testDataStreamHeader(proxyName)))
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

	errCh := make(chan error, 1)
	go func() {
		stream, err := serverSession.Open()
		if err != nil {
			errCh <- err
			return
		}
		if _, err := stream.Write([]byte("BAD!")); err != nil {
			_ = stream.Close()
			errCh <- err
			return
		}
		errCh <- stream.Close()
	}()

	stream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("Client AcceptStream failed: %v", err)
	}
	c.handleStream(stream)

	if err := <-errCh; err != nil {
		t.Fatalf("server stream write failed: %v", err)
	}
}

func TestClient_HandleStream_DialFail(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	proxyName := "fail-proxy"
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		ID:        proxyName,
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
		mustWriteAll(t, stream, encodeDataStreamHeader(t, testDataStreamHeader(proxyName)))

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

func TestClient_HandleStream_RejectsStaleRevisionAndWrongRoles(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	proxyName := "guarded-proxy"
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		ID:                proxyName,
		Name:              proxyName,
		LocalIP:           "127.0.0.1",
		LocalPort:         1,
		TransportPolicy:   protocol.TransportPolicyServerRelayOnly,
		ActualTransport:   protocol.ActualTransportServerRelay,
		ProvisionRevision: 3,
	})

	valid := testDataStreamHeader(proxyName)
	valid.Revision = 3
	for name, mutate := range map[string]func(*protocol.DataStreamHeader){
		"stale revision": func(header *protocol.DataStreamHeader) { header.Revision = 2 },
		"wrong source":   func(header *protocol.DataStreamHeader) { header.SourceRole = protocol.DataStreamRoleTarget },
		"wrong target":   func(header *protocol.DataStreamHeader) { header.TargetRole = protocol.DataStreamRoleIngress },
		"wrong transport": func(header *protocol.DataStreamHeader) {
			header.Transport = protocol.ActualTransportPeerDirect
			header.ServerAuthorized = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer mustClose(t, clientConn)
			defer mustClose(t, serverConn)

			clientSession, err := mux.NewClientSession(clientConn, mux.DefaultConfig())
			if err != nil {
				t.Fatalf("client session: %v", err)
			}
			defer mustClose(t, clientSession)
			serverSession, err := mux.NewServerSession(serverConn, mux.DefaultConfig())
			if err != nil {
				t.Fatalf("server session: %v", err)
			}
			defer mustClose(t, serverSession)

			done := make(chan struct{})
			go func() {
				defer close(done)
				stream, err := serverSession.Open()
				if err != nil {
					return
				}
				defer func() { _ = stream.Close() }()
				header := valid
				mutate(&header)
				mustWriteAll(t, stream, encodeDataStreamHeader(t, header))
			}()

			stream, err := clientSession.AcceptStream()
			if err != nil {
				t.Fatalf("accept stream: %v", err)
			}
			c.handleStream(stream)

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("server stream did not close after rejected header")
			}
		})
	}
}

func TestClient_HandleStream_RejectsDirectOnlyRelayStream(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	proxyName := "direct-only-proxy"
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		ID:                proxyName,
		Name:              proxyName,
		LocalIP:           "127.0.0.1",
		LocalPort:         1,
		TransportPolicy:   protocol.TransportPolicyDirectOnly,
		ActualTransport:   protocol.ActualTransportServerRelay,
		ProvisionRevision: 3,
	})

	clientConn, serverConn := net.Pipe()
	defer mustClose(t, clientConn)
	defer mustClose(t, serverConn)

	clientSession, err := mux.NewClientSession(clientConn, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("client session: %v", err)
	}
	defer mustClose(t, clientSession)
	serverSession, err := mux.NewServerSession(serverConn, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("server session: %v", err)
	}
	defer mustClose(t, serverSession)

	done := make(chan struct{})
	go func() {
		defer close(done)
		stream, err := serverSession.Open()
		if err != nil {
			return
		}
		defer func() { _ = stream.Close() }()
		header := testDataStreamHeader(proxyName)
		header.Revision = 3
		mustWriteAll(t, stream, encodeDataStreamHeader(t, header))
	}()

	stream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	c.handleStream(stream)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server stream did not close after direct_only relay rejection")
	}
}
