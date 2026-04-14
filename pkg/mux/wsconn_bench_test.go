package mux

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

func BenchmarkDataChannelTransport_YamuxOverPipe_vs_WSConn(b *testing.B) {
	for _, tc := range []struct {
		name string
		pair func(*testing.B) (io.ReadWriteCloser, io.ReadWriteCloser, func())
	}{
		{name: "pipe", pair: benchmarkPipePair},
		{name: "wsconn", pair: benchmarkWSConnPair},
	} {
		b.Run(tc.name, func(b *testing.B) {
			clientConn, serverConn, cleanup := tc.pair(b)
			defer cleanup()

			serverSession, err := NewServerSession(serverConn, DefaultConfig())
			if err != nil {
				b.Fatalf("创建服务端 yamux session 失败: %v", err)
			}
			defer func() { _ = serverSession.Close() }()

			clientSession, err := NewClientSession(clientConn, DefaultConfig())
			if err != nil {
				b.Fatalf("创建客户端 yamux session 失败: %v", err)
			}
			defer func() { _ = clientSession.Close() }()

			serverErr := make(chan error, 1)
			go func() {
				for {
					stream, err := serverSession.Accept()
					if err != nil {
						serverErr <- err
						return
					}
					go func(stream net.Conn) {
						defer func() { _ = stream.Close() }()
						if _, err := io.Copy(stream, stream); err != nil {
							serverErr <- err
						}
					}(stream)
				}
			}()

			payload := bytes.Repeat([]byte("netsgo"), 512)
			reply := make([]byte, len(payload))
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				stream, err := clientSession.Open()
				if err != nil {
					b.Fatalf("Open stream 失败: %v", err)
				}
				if _, err := stream.Write(payload); err != nil {
					_ = stream.Close()
					b.Fatalf("写入 payload 失败: %v", err)
				}
				if _, err := io.ReadFull(stream, reply); err != nil {
					_ = stream.Close()
					b.Fatalf("读取回显失败: %v", err)
				}
				if !bytes.Equal(reply, payload) {
					_ = stream.Close()
					b.Fatal("回显内容不匹配")
				}
				_ = stream.Close()
			}

			b.StopTimer()
			select {
			case err := <-serverErr:
				if err != nil && err != io.EOF && err != net.ErrClosed {
					b.Fatalf("服务端回显循环异常: %v", err)
				}
			default:
			}
		})
	}
}

func benchmarkPipePair(b *testing.B) (io.ReadWriteCloser, io.ReadWriteCloser, func()) {
	b.Helper()
	clientConn, serverConn := net.Pipe()
	return clientConn, serverConn, func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	}
}

func benchmarkWSConnPair(b *testing.B) (io.ReadWriteCloser, io.ReadWriteCloser, func()) {
	b.Helper()

	serverConnCh := make(chan *websocket.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			b.Errorf("Upgrade 失败: %v", err)
			return
		}
		serverConnCh <- conn
	}))

	clientWS, _, err := websocket.DefaultDialer.Dial("ws"+ts.URL[len("http"):], nil)
	if err != nil {
		ts.Close()
		b.Fatalf("Dial WebSocket 失败: %v", err)
	}

	serverWS := <-serverConnCh
	clientConn := NewWSConn(clientWS)
	serverTransport := NewWSConn(serverWS)

	return clientConn, serverTransport, func() {
		_ = clientConn.Close()
		_ = serverTransport.Close()
		ts.Close()
	}
}
