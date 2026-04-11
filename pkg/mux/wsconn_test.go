package mux

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func setupWSPair(t *testing.T, handler func(*websocket.Conn)) (*WSConn, func()) {
	t.Helper()

	serverDone := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade 失败: %v", err)
			close(serverDone)
			return
		}
		defer close(serverDone)
		handler(conn)
	}))

	wsURL := "ws" + ts.URL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("Dial WebSocket 失败: %v", err)
	}

	return NewWSConn(conn), func() {
		_ = conn.Close()
		ts.Close()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Fatal("server handler 未及时退出")
		}
	}
}

func TestWSConnReadSpansBinaryMessages(t *testing.T) {
	wsConn, cleanup := setupWSPair(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte("hello"))
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte("world"))
	})
	defer cleanup()

	buf := make([]byte, len("helloworld"))
	if _, err := io.ReadFull(wsConn, buf); err != nil {
		t.Fatalf("ReadFull 失败: %v", err)
	}
	if string(buf) != "helloworld" {
		t.Fatalf("跨消息读取结果错误: %q", string(buf))
	}
}

func TestWSConnWriteProducesBinaryMessages(t *testing.T) {
	received := make(chan [][]byte, 1)
	wsConn, cleanup := setupWSPair(t, func(conn *websocket.Conn) {
		defer conn.Close()
		payloads := make([][]byte, 0, 2)
		for i := 0; i < 2; i++ {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("ReadMessage 失败: %v", err)
				return
			}
			if messageType != websocket.BinaryMessage {
				t.Errorf("messageType = %d, 期望 binary", messageType)
				return
			}
			payloads = append(payloads, data)
		}
		received <- payloads
	})
	defer cleanup()

	if _, err := wsConn.Write([]byte("abc")); err != nil {
		t.Fatalf("第一次 Write 失败: %v", err)
	}
	if _, err := wsConn.Write([]byte("defg")); err != nil {
		t.Fatalf("第二次 Write 失败: %v", err)
	}

	select {
	case payloads := <-received:
		if len(payloads) != 2 {
			t.Fatalf("message 数量错误: %d", len(payloads))
		}
		if !bytes.Equal(payloads[0], []byte("abc")) {
			t.Fatalf("第一条 payload 错误: %q", payloads[0])
		}
		if !bytes.Equal(payloads[1], []byte("defg")) {
			t.Fatalf("第二条 payload 错误: %q", payloads[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到服务端 payload")
	}
}

func TestWSConnConcurrentWrites(t *testing.T) {
	const writers = 8

	received := make(chan [][]byte, 1)
	wsConn, cleanup := setupWSPair(t, func(conn *websocket.Conn) {
		defer conn.Close()

		payloads := make([][]byte, 0, writers)
		for i := 0; i < writers; i++ {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("ReadMessage 失败: %v", err)
				return
			}
			if messageType != websocket.BinaryMessage {
				t.Errorf("messageType = %d, 期望 binary", messageType)
				return
			}
			payloads = append(payloads, append([]byte(nil), data...))
		}
		received <- payloads
	})
	defer cleanup()

	expected := make(map[string]int, writers)
	var wg sync.WaitGroup
	wg.Add(writers)

	for i := 0; i < writers; i++ {
		payload := []byte{byte('a' + i)}
		expected[string(payload)]++

		go func(payload []byte) {
			defer wg.Done()
			if _, err := wsConn.Write(payload); err != nil {
				t.Errorf("Write 失败: %v", err)
			}
		}(payload)
	}

	wg.Wait()

	select {
	case payloads := <-received:
		if len(payloads) != writers {
			t.Fatalf("message 数量错误: %d", len(payloads))
		}
		for _, payload := range payloads {
			expected[string(payload)]--
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到服务端 payload")
	}

	for payload, count := range expected {
		if count != 0 {
			t.Fatalf("payload %q 收到次数错误: %d", payload, 1-count)
		}
	}
}

func TestWSConnCloseIdempotent(t *testing.T) {
	wsConn, cleanup := setupWSPair(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	})
	defer cleanup()

	if err := wsConn.Close(); err != nil {
		t.Fatalf("第一次 Close 失败: %v", err)
	}
	if err := wsConn.Close(); err != nil {
		t.Fatalf("第二次 Close 应幂等: %v", err)
	}
}

func TestWSConnCloseConcurrentWithWrite(t *testing.T) {
	wsConn, cleanup := setupWSPair(t, func(conn *websocket.Conn) {
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	})
	defer cleanup()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = wsConn.Write(bytes.Repeat([]byte("a"), 1024))
	}()

	time.Sleep(10 * time.Millisecond)
	_ = wsConn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("并发 Write 未及时返回")
	}
}

func TestWSConnReadRejectsNonBinaryMessages(t *testing.T) {
	wsConn, cleanup := setupWSPair(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("not-binary"))
	})
	defer cleanup()

	buf := make([]byte, 16)
	if _, err := wsConn.Read(buf); err == nil {
		t.Fatal("读取 text message 应返回错误")
	}
}

func TestWSConnLocalAndRemoteAddr(t *testing.T) {
	wsConn, cleanup := setupWSPair(t, func(conn *websocket.Conn) {
		defer conn.Close()
		time.Sleep(50 * time.Millisecond)
	})
	defer cleanup()

	if wsConn.LocalAddr() == nil {
		t.Fatal("LocalAddr 不应为 nil")
	}
	if wsConn.RemoteAddr() == nil {
		t.Fatal("RemoteAddr 不应为 nil")
	}
}
