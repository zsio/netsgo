package mux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSConn 将 websocket 消息流适配为 yamux 需要的字节流。
type WSConn struct {
	conn      *websocket.Conn
	reader    io.Reader
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func NewWSConn(conn *websocket.Conn) *WSConn {
	return &WSConn{conn: conn}
}

func (w *WSConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		if w.reader == nil {
			messageType, reader, err := w.conn.NextReader()
			if err != nil {
				return 0, err
			}
			if messageType != websocket.BinaryMessage {
				return 0, fmt.Errorf("unexpected websocket message type: %d", messageType)
			}
			w.reader = reader
		}

		n, err := w.reader.Read(p)
		if err == io.EOF {
			w.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (w *WSConn) Write(p []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	writer, err := w.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}

	n, writeErr := writer.Write(p)
	closeErr := writer.Close()

	if writeErr != nil {
		if closeErr != nil {
			return n, fmt.Errorf("write failed: %w; close failed: %v", writeErr, closeErr)
		}
		return n, writeErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func (w *WSConn) Close() error {
	var err error
	w.closeOnce.Do(func() {
		_ = w.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "closing"),
			time.Now().Add(time.Second),
		)
		err = w.conn.Close()
	})
	return err
}

func (w *WSConn) LocalAddr() net.Addr {
	if underlying := w.conn.UnderlyingConn(); underlying != nil {
		return underlying.LocalAddr()
	}
	return nil
}

func (w *WSConn) RemoteAddr() net.Addr {
	if underlying := w.conn.UnderlyingConn(); underlying != nil {
		return underlying.RemoteAddr()
	}
	return nil
}
