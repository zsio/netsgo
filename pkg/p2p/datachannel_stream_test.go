package p2p

import (
	"bytes"
	"io"
	"sync"
	"testing"
)

type recordingMessageTransport struct {
	mu       sync.Mutex
	messages [][]byte
	writes   [][]byte
}

func (t *recordingMessageTransport) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.messages) == 0 {
		return 0, io.EOF
	}
	message := t.messages[0]
	t.messages = t.messages[1:]
	if len(p) < len(message) {
		return 0, io.ErrShortBuffer
	}
	return copy(p, message), nil
}

func (t *recordingMessageTransport) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writes = append(t.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (*recordingMessageTransport) Close() error { return nil }

func TestDataChannelByteStreamChunksMessagesAndPreservesByteReads(t *testing.T) {
	payload := bytes.Repeat([]byte("netsgo-p2p-stream-"), 4096)
	transport := &recordingMessageTransport{}
	stream := newDataChannelByteStream(transport)
	if n, err := stream.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("write: n=%d want=%d err=%v", n, len(payload), err)
	}
	if len(transport.writes) < 2 {
		t.Fatalf("large write was not chunked: messages=%d", len(transport.writes))
	}
	for i, message := range transport.writes {
		if len(message) > dataChannelStreamChunkSize {
			t.Fatalf("message %d exceeds chunk bound: %d", i, len(message))
		}
	}
	transport.messages = append(transport.messages, transport.writes...)
	var got bytes.Buffer
	readBuffer := make([]byte, 7)
	for got.Len() < len(payload) {
		n, err := stream.Read(readBuffer)
		if err != nil {
			t.Fatalf("read after %d bytes: %v", got.Len(), err)
		}
		got.Write(readBuffer[:n])
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatal("byte stream changed payload across message and read boundaries")
	}
}
