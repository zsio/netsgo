package p2p

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestSessionOpensYamuxStreamOverDetachedDataChannel(t *testing.T) {
	var offerer, answerer *Session
	offererCandidates := make(chan protocol.P2PSignal, 32)
	answererCandidates := make(chan protocol.P2PSignal, 32)
	var err error
	offerer, err = NewSession(protocol.P2PRoleOfferer, nil, func(signal protocol.P2PSignal) { offererCandidates <- signal })
	if err != nil {
		t.Fatalf("new offerer: %v", err)
	}
	defer func() { _ = offerer.Close() }()
	answerer, err = NewSession(protocol.P2PRoleAnswerer, nil, func(signal protocol.P2PSignal) { answererCandidates <- signal })
	if err != nil {
		t.Fatalf("new answerer: %v", err)
	}
	defer func() { _ = answerer.Close() }()

	done := make(chan struct{})
	stopCandidates := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case candidate := <-offererCandidates:
				_ = answerer.AddCandidate(candidate)
			case candidate := <-answererCandidates:
				_ = offerer.AddCandidate(candidate)
			case <-stopCandidates:
				return
			}
		}
	}()
	offer, err := offerer.CreateOffer()
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	answer, err := answerer.AcceptOffer(offer)
	if err != nil {
		t.Fatalf("accept offer: %v", err)
	}
	if err := offerer.AcceptAnswer(answer); err != nil {
		t.Fatalf("accept answer: %v", err)
	}
	waitSessionReady(t, offerer)
	waitSessionReady(t, answerer)

	accepted := make(chan error, 1)
	go func() {
		stream, err := answerer.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer func() { _ = stream.Close() }()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(stream, buf); err != nil {
			accepted <- err
			return
		}
		_, err = stream.Write(buf)
		accepted <- err
	}()
	stream, err := offerer.Open()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("echo: %q", buf)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("accept side: %v", err)
	}

	largePayload := bytes.Repeat([]byte("large-p2p-payload-"), 64*1024)
	largeAccepted := make(chan error, 1)
	go func() {
		stream, err := answerer.Accept()
		if err != nil {
			largeAccepted <- err
			return
		}
		defer func() { _ = stream.Close() }()
		got := make([]byte, len(largePayload))
		if _, err := io.ReadFull(stream, got); err != nil {
			largeAccepted <- err
			return
		}
		if !bytes.Equal(got, largePayload) {
			largeAccepted <- fmt.Errorf("large payload mismatch")
			return
		}
		_, err = stream.Write(got)
		largeAccepted <- err
	}()
	largeStream, err := offerer.Open()
	if err != nil {
		t.Fatalf("open large stream: %v", err)
	}
	if _, err := largeStream.Write(largePayload); err != nil {
		t.Fatalf("write large stream: %v", err)
	}
	largeEcho := make([]byte, len(largePayload))
	if _, err := io.ReadFull(largeStream, largeEcho); err != nil {
		t.Fatalf("read large stream: %v", err)
	}
	_ = largeStream.Close()
	if !bytes.Equal(largeEcho, largePayload) {
		t.Fatal("large echoed payload mismatch")
	}
	if err := <-largeAccepted; err != nil {
		t.Fatalf("large accept side: %v", err)
	}

	const concurrentStreams = 16
	acceptErrors := make(chan error, concurrentStreams)
	go func() {
		for i := 0; i < concurrentStreams; i++ {
			stream, err := answerer.Accept()
			if err != nil {
				acceptErrors <- err
				continue
			}
			go func(conn io.ReadWriteCloser) {
				defer func() { _ = conn.Close() }()
				payload := make([]byte, 32)
				_, err := io.ReadFull(conn, payload)
				if err == nil {
					_, err = conn.Write(payload)
				}
				acceptErrors <- err
			}(stream)
		}
	}()
	var wg sync.WaitGroup
	openErrors := make(chan error, concurrentStreams)
	for i := 0; i < concurrentStreams; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream, err := offerer.Open()
			if err != nil {
				openErrors <- err
				return
			}
			defer func() { _ = stream.Close() }()
			payload := []byte(fmt.Sprintf("stream-%025d", i))
			if _, err := stream.Write(payload); err != nil {
				openErrors <- err
				return
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(stream, got); err != nil {
				openErrors <- err
				return
			}
			if string(got) != string(payload) {
				openErrors <- fmt.Errorf("echo mismatch got=%q want=%q", got, payload)
				return
			}
			openErrors <- nil
		}()
	}
	wg.Wait()
	for i := 0; i < concurrentStreams; i++ {
		if err := <-openErrors; err != nil {
			t.Fatalf("concurrent open stream: %v", err)
		}
		if err := <-acceptErrors; err != nil {
			t.Fatalf("concurrent accept stream: %v", err)
		}
	}
	_ = offerer.Close()
	_ = answerer.Close()
	close(stopCandidates)
	<-done
}

func waitSessionReady(t *testing.T, session *Session) {
	t.Helper()
	select {
	case <-session.Ready():
		if !session.Available() {
			t.Fatal("session signaled ready without an available mux")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for p2p session")
	}
}

func TestSessionBoundsCandidatesBeforeRemoteDescription(t *testing.T) {
	session, err := NewSession(protocol.P2PRoleOfferer, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	for i := 0; i < protocol.P2PMaxCandidates; i++ {
		if err := session.AddCandidate(protocol.P2PSignal{Kind: protocol.P2PSignalCandidate, Candidate: "candidate:1"}); err != nil {
			t.Fatalf("candidate %d rejected: %v", i, err)
		}
	}
	if err := session.AddCandidate(protocol.P2PSignal{Kind: protocol.P2PSignalCandidate, Candidate: "candidate:1"}); err == nil {
		t.Fatal("unbounded pending candidate accepted")
	}
}
