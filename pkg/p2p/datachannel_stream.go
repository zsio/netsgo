package p2p

import (
	"io"
	"sync"
)

// A detached WebRTC DataChannel is message-oriented: each Write becomes one
// SCTP user message and Read requires a buffer large enough for that complete
// message. yamux expects an ordinary byte stream and may read its header and
// payload separately. dataChannelByteStream provides that missing adaptation
// and bounds every SCTP message so a large yamux write cannot destroy the
// session with io.ErrShortBuffer on the receiving side.
const dataChannelStreamChunkSize = 16 * 1024

type dataChannelByteStream struct {
	transport io.ReadWriteCloser

	readMu      sync.Mutex
	readBuffer  [dataChannelStreamChunkSize]byte
	readPending []byte
	readErr     error

	writeMu sync.Mutex
}

func newDataChannelByteStream(transport io.ReadWriteCloser) *dataChannelByteStream {
	return &dataChannelByteStream{transport: transport}
}

func (s *dataChannelByteStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if len(s.readPending) > 0 {
		n := copy(p, s.readPending)
		s.readPending = s.readPending[n:]
		return n, nil
	}
	if s.readErr != nil {
		err := s.readErr
		s.readErr = nil
		return 0, err
	}
	n, err := s.transport.Read(s.readBuffer[:])
	if n == 0 {
		if err == nil {
			err = io.ErrNoProgress
		}
		return 0, err
	}
	copied := copy(p, s.readBuffer[:n])
	if copied < n {
		s.readPending = s.readBuffer[copied:n]
	}
	if err != nil {
		s.readErr = err
	}
	return copied, nil
}

func (s *dataChannelByteStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	written := 0
	for written < len(p) {
		end := min(written+dataChannelStreamChunkSize, len(p))
		n, err := s.transport.Write(p[written:end])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
	return written, nil
}

func (s *dataChannelByteStream) Close() error { return s.transport.Close() }
