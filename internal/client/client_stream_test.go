package client

import (
	"bytes"
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

func requestClientTargetHTTPStream(t *testing.T, c *Client, header protocol.DataStreamHeader, host string, timeout time.Duration) (string, error) {
	t.Helper()

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

	type result struct {
		response string
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		stream, err := serverSession.Open()
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		defer func() { _ = stream.Close() }()

		if _, err := stream.Write(encodeDataStreamHeader(t, header)); err != nil {
			resultCh <- result{err: err}
			return
		}
		req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\n\r\n", host)
		if _, err := stream.Write([]byte(req)); err != nil {
			resultCh <- result{err: err}
			return
		}

		buf := make([]byte, 1024)
		_ = stream.SetReadDeadline(time.Now().Add(timeout))
		n, err := stream.Read(buf)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		resultCh <- result{response: string(buf[:n])}
	}()

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("client accept stream: %v", err)
	}
	done := make(chan struct{})
	go func() {
		c.handleStream(clientStream)
		close(done)
	}()

	select {
	case res := <-resultCh:
		<-done
		return res.response, res.err
	case <-time.After(timeout + time.Second):
		return "", fmt.Errorf("timed out waiting for target stream response")
	}
}

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

func TestClient_HandleStream_FallbackIDScanResolvesNameKeyedLegacyProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Legacy-ID-Match", "ok")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("legacy id backend")); err != nil {
			t.Fatalf("write legacy id backend response failed: %v", err)
		}
	}))
	defer backend.Close()

	localPort := mustParseLoopbackPort(t, backend.Listener.Addr().String())

	c := New("ws://localhost:8080", "key")
	c.proxies.Store("legacy-flat-tcp", protocol.ProxyNewRequest{
		ID:                "legacy-flat-tcp-id",
		Name:              "legacy-flat-tcp",
		Type:              protocol.ProxyTypeTCP,
		LocalIP:           "127.0.0.1",
		LocalPort:         localPort,
		TransportPolicy:   protocol.TransportPolicyServerRelayOnly,
		ActualTransport:   protocol.ActualTransportUnknown,
		ProvisionRevision: 8,
	})
	if _, ok := c.proxies.Load("legacy-flat-tcp-id"); ok {
		t.Fatal("test setup should store the legacy proxy by name only so direct tunnel-id lookup misses")
	}

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

		header := testDataStreamHeader("legacy-flat-tcp-id")
		header.Revision = 8
		mustWriteAll(t, serverStream, encodeDataStreamHeader(t, header))
		mustWriteAll(t, serverStream, []byte("GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"))
	}()

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("client accept stream: %v", err)
	}

	var relayWg sync.WaitGroup
	relayWg.Add(1)
	go func() {
		defer relayWg.Done()
		c.handleStream(clientStream)
	}()

	wg.Wait()
	if streamErr != nil {
		t.Fatalf("server open stream failed: %v", streamErr)
	}

	respBuf := make([]byte, 1024)
	mustSetDeadline(t, serverStream, time.Now().Add(2*time.Second))
	n, err := serverStream.Read(respBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read legacy id backend response: %v", err)
	}
	mustClose(t, serverStream)

	responseStr := string(respBuf[:n])
	if !bytes.Contains([]byte(responseStr), []byte("200 OK")) {
		t.Fatalf("expected HTTP 200 OK for legacy id match, got: %s", responseStr)
	}
	if !bytes.Contains([]byte(responseStr), []byte("X-Legacy-Id-Match: ok")) {
		t.Fatalf("expected legacy id match header, got: %s", responseStr)
	}

	relayWg.Wait()
}

func TestClient_HandleStream_FixedTCPTargetDialsEndpointHostPort(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fixed-Target", "ok")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("fixed tcp target backend")); err != nil {
			t.Fatalf("write fixed target backend response failed: %v", err)
		}
	}))
	defer backend.Close()

	localPort := mustParseLoopbackPort(t, backend.Listener.Addr().String())
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	req.TunnelID = "fixed-tcp-target-id"
	req.Revision = 17
	req.Spec.ID = req.TunnelID
	req.Spec.Name = "fixed-tcp-target"
	req.Spec.Revision = req.Revision
	req.Spec.Target.Config = mustJSON(t, map[string]any{"host": "127.0.0.1", "port": localPort})

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("fixed target provision rejected: %s", ack.Message)
	}
	header := testDataStreamHeader(req.TunnelID)
	header.Revision = req.Revision
	response, err := requestClientTargetHTTPStream(t, c, header, "127.0.0.1", 2*time.Second)
	if err != nil {
		t.Fatalf("fixed target stream should dial endpoint host/port without legacy c.proxies: %v", err)
	}
	if !bytes.Contains([]byte(response), []byte("200 OK")) || !bytes.Contains([]byte(response), []byte("X-Fixed-Target: ok")) {
		t.Fatalf("fixed target stream response mismatch: %s", response)
	}
}

func TestClient_HandleStream_FixedUDPTargetRelaysFrames(t *testing.T) {
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp echo backend: %v", err)
	}
	defer mustClose(t, packetConn)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := packetConn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = packetConn.WriteTo(buf[:n], addr)
		}
	}()

	backendAddr := packetConn.LocalAddr().(*net.UDPAddr)
	c := New("ws://localhost:8080", "key")
	req := testUDPTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	req.TunnelID = "fixed-udp-target-id"
	req.Revision = 23
	req.Spec.ID = req.TunnelID
	req.Spec.Name = "fixed-udp-target"
	req.Spec.Revision = req.Revision
	req.Spec.Target.Config = mustJSON(t, map[string]any{"host": backendAddr.IP.String(), "port": backendAddr.Port})

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("fixed UDP target provision rejected: %s", ack.Message)
	}
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

	resultCh := make(chan error, 1)
	go func() {
		stream, err := serverSession.Open()
		if err != nil {
			resultCh <- err
			return
		}
		defer func() { _ = stream.Close() }()
		header := testDataStreamHeader(req.TunnelID)
		header.Revision = req.Revision
		if _, err := stream.Write(encodeDataStreamHeader(t, header)); err != nil {
			resultCh <- err
			return
		}
		payload := []byte("fixed udp target payload")
		if err := mux.WriteUDPFrame(stream, payload); err != nil {
			resultCh <- err
			return
		}
		_ = stream.SetReadDeadline(time.Now().Add(2 * time.Second))
		reply, err := mux.ReadUDPFrame(stream)
		if err != nil {
			resultCh <- err
			return
		}
		if !bytes.Equal(reply, payload) {
			resultCh <- fmt.Errorf("udp reply mismatch: got %q want %q", reply, payload)
			return
		}
		resultCh <- nil
	}()

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("client accept stream: %v", err)
	}
	done := make(chan struct{})
	go func() {
		c.handleStream(clientStream)
		close(done)
	}()

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("fixed UDP target stream should relay framed datagrams without legacy c.proxies: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for fixed UDP target relay")
	}
	<-done
}

func TestClient_HandleStream_FixedHTTPTargetDoesNotMatchByDomain(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fixed-HTTP-Target", "ok")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("fixed http target backend")); err != nil {
			t.Fatalf("write fixed http backend response failed: %v", err)
		}
	}))
	defer backend.Close()

	localPort := mustParseLoopbackPort(t, backend.Listener.Addr().String())
	c := New("ws://localhost:8080", "key")
	req := testTunnelProvisionRequest(t, protocol.DataStreamRoleTarget, reserveClientTCPPort(t))
	req.TunnelID = "fixed-http-target-id"
	req.Revision = 19
	req.Spec.ID = req.TunnelID
	req.Spec.Name = "fixed-http-target"
	req.Spec.Revision = req.Revision
	req.Spec.Topology = protocol.TunnelTopologyServerExpose
	req.Spec.Ingress.Location = protocol.EndpointLocationServer
	req.Spec.Ingress.ClientID = ""
	req.Spec.Ingress.Type = protocol.IngressTypeHTTPHost
	req.Spec.Ingress.Config = mustJSON(t, map[string]any{
		"domain": "endpoint.example.com",
		"auth":   map[string]any{"type": "none"},
	})
	req.Spec.Target.Config = mustJSON(t, map[string]any{"host": "127.0.0.1", "port": localPort})

	ack := c.handleTunnelProvision(&sessionRuntime{}, req)
	if !ack.Accepted {
		t.Fatalf("fixed HTTP target provision rejected: %s", ack.Message)
	}
	header := testDataStreamHeader(req.TunnelID)
	header.Revision = req.Revision
	response, err := requestClientTargetHTTPStream(t, c, header, "flat-field.example.com", 2*time.Second)
	if err != nil {
		t.Fatalf("fixed HTTP target stream should ignore HTTP Host/domain for target matching: %v", err)
	}
	if !bytes.Contains([]byte(response), []byte("200 OK")) || !bytes.Contains([]byte(response), []byte("X-Fixed-Http-Target: ok")) {
		t.Fatalf("fixed HTTP target response mismatch: %s", response)
	}
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
